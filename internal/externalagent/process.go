package externalagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxLine = 1024 * 1024

func normalize(line []byte) Event {
	if !utf8.Valid(line) || !json.Valid(line) {
		return Event{Type: "external.stdout", Text: strings.ToValidUTF8(string(line), "�")}
	}
	e := Event{Type: "external.raw", Raw: bytes.Clone(line)}
	var obj map[string]json.RawMessage
	if json.Unmarshal(line, &obj) == nil {
		for _, field := range []string{"sessionID", "session_id"} {
			var value string
			if json.Unmarshal(obj[field], &value) == nil && ValidSessionID(value) {
				e.SessionID = value
				break
			}
		}
		if e.SessionID == "" {
			var session struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(obj["session"], &session) == nil && ValidSessionID(session.ID) {
				e.SessionID = session.ID
			}
		}
		var typ, text string
		if t, ok := obj["type"]; ok && len(t) > 0 && t[0] == '"' && json.Unmarshal(t, &typ) == nil {
			if v, ok := obj["text"]; ok && len(v) > 0 && v[0] == '"' && json.Unmarshal(v, &text) == nil {
				e.Type, e.Text = typ, text
			}
		}
	}
	return e
}

type limitedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
	onLimit   func()
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := min(len(p), b.limit-b.buffer.Len())
	b.buffer.Write(p[:n])
	if n < len(p) && !b.truncated {
		b.truncated = true
		if b.onLimit != nil {
			b.onLimit()
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buffer.String() }

// safeCause retains errors.Is/As without including executable paths or arguments.
type safeCause struct{ err error }

func (safeCause) Error() string   { return "process operation failed" }
func (e safeCause) Unwrap() error { return e.err }

// Refresh the drain deadline when the consumer asks for another chunk. Time
// spent applying stream backpressure must never discard buffered process output.
type drainReader struct {
	file   *os.File
	exited <-chan struct{}
}

func (r drainReader) Read(p []byte) (int, error) {
	select {
	case <-r.exited:
		if err := r.file.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			return 0, err
		}
	default:
	}
	return r.file.Read(p)
}

func runProcess(parent context.Context, path string, args []string, input string, r Request, env []string, out chan<- Event, normalizeEvent eventNormalizer) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	cmd := exec.Command(path, args...)
	cmd.Env = env
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Dir = r.CWD
	cmd.Stdin = strings.NewReader(input)
	owned, err := ownCommand(cmd)
	if err != nil {
		return fmt.Errorf("external agent: %w", safeCause{err})
	}
	defer owned.close()
	stdout, sw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("external stdout: %w", safeCause{err})
	}
	defer stdout.Close()
	defer sw.Close()
	stderr, ew, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("external stderr: %w", safeCause{err})
	}
	defer stderr.Close()
	defer ew.Close()
	cmd.Stdout, cmd.Stderr = sw, ew
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = owned.start(); err != nil {
		return fmt.Errorf("external agent start: %w", safeCause{err})
	}
	sw.Close()
	ew.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	var readErr error
	exited := make(chan struct{})
	diagnostic := &limitedBuffer{limit: 64 * 1024}
	go func() { defer wg.Done(); _, _ = io.Copy(diagnostic, stderr) }()
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(drainReader{file: stdout, exited: exited})
		scanner.Buffer(make([]byte, 4096), maxLine+2)
		scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				return i + 1, data[:i], nil
			}
			if atEOF && len(data) > 0 {
				return len(data), data, nil
			}
			return 0, nil, nil
		})
		for scanner.Scan() {
			if len(scanner.Bytes()) > maxLine {
				readErr = errors.New("external stdout line exceeds 1 MiB")
				cancel()
				return
			}
			e := normalizeEvent(scanner.Bytes())
			select {
			case out <- e:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
			readErr = errors.New("external stdout read failed or line exceeds 1 MiB")
			cancel()
		}
	}()
	waitErr := owned.wait(ctx)
	waitErr = errors.Join(waitErr, owned.close())
	close(exited)
	// Wake reads held open by detached descendants; the stdout reader refreshes
	// this deadline after each consumer pause instead of truncating backpressure.
	for _, pipe := range []*os.File{stdout, stderr} {
		if err := pipe.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			waitErr = errors.Join(waitErr, err)
			_ = pipe.Close()
		}
	}
	wg.Wait()
	cause := errors.Join(waitErr, readErr, parent.Err())
	if cause == nil {
		return nil
	}
	detail := sanitizeDiagnostic(diagnostic.String(), r, diagnostic.truncated)
	if diagnostic.truncated {
		detail += " [truncated]"
	}
	return fmt.Errorf("external agent failed: %s: %w", detail, safeCause{cause})
}
func sanitizeDiagnostic(text string, r Request, truncated bool, sensitive ...string) string {
	if truncated {
		// A capture boundary can split the final rune. Drop only that incomplete
		// sequence before normalization, so its replacement rune cannot hide a
		// sensitive prefix. Complete runes and invalid interior bytes remain.
		for start := len(text) - 1; start >= max(0, len(text)-utf8.UTFMax); start-- {
			if !utf8.RuneStart(text[start]) {
				continue
			}
			if !utf8.FullRuneInString(text[start:]) {
				text = text[:start]
			}
			break
		}
	}
	normalize := func(value string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, strings.ToValidUTF8(value, "�"))
	}
	text = normalize(text)
	values := append([]string{r.Prompt, r.Model, r.SessionID, r.CWD}, sensitive...)
	unique := make(map[string]bool)
	for _, value := range values {
		value = normalize(value)
		if value == "" {
			continue
		}
		unique[value] = true
		// The bounded stderr capture may end within a sensitive value.
		for n := min(len(value)-1, len(text)); truncated && n > 0; n-- {
			if utf8.RuneStart(value[n]) && strings.HasSuffix(text, value[:n]) {
				unique[value[:n]] = true
				break
			}
		}
	}
	type interval struct{ start, end int }
	var matches []interval
	// Search the original normalized bytes, including occurrences that overlap.
	// Replacing first would hide later matches and expose part of a credential.
	for value := range unique {
		for start := 0; start <= len(text)-len(value); {
			i := strings.Index(text[start:], value)
			if i < 0 {
				break
			}
			start += i
			matches = append(matches, interval{start, start + len(value)})
			start++
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].start == matches[j].start {
			return matches[i].end > matches[j].end
		}
		return matches[i].start < matches[j].start
	})
	merged := matches[:0]
	for _, match := range matches {
		if n := len(merged); n > 0 && match.start <= merged[n-1].end {
			merged[n-1].end = max(merged[n-1].end, match.end)
		} else {
			merged = append(merged, match)
		}
	}
	var output strings.Builder
	cursor := 0
	for _, match := range merged {
		output.WriteString(text[cursor:match.start])
		output.WriteString("[REDACTED]")
		cursor = match.end
	}
	output.WriteString(text[cursor:])
	text = output.String()
	if len(text) > 64*1024 {
		n := 64 * 1024
		for n > 0 && !utf8.RuneStart(text[n]) {
			n--
		}
		text = text[:n] + " [truncated]"
	}
	return text
}
