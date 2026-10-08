package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/secrets"
)

// ExportJSONL atomically replaces the explicitly selected destination. The
// redactor sees canonical event JSON; invalid output aborts before publication.
func ExportJSONL(ctx context.Context, items []events.Event, destination string, redact func([]byte) []byte) error {
	return exportJSONL(ctx, destination, redact, func(yield func(events.Event) error) error {
		for _, event := range items {
			if err := yield(event); err != nil {
				return err
			}
		}
		return nil
	})
}

func exportJSONL(ctx context.Context, destination string, redact func([]byte) []byte, each func(func(events.Event) error) error) (result error) {
	defer func() { result = safe("export audit", result) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(destination) == "" {
		return ErrInvalidInput
	}
	dir := filepath.Dir(destination)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ErrInvalidInput
	}
	if info, err := os.Stat(destination); err == nil {
		if !info.Mode().IsRegular() {
			return ErrInvalidInput
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary := filepath.Join(dir, ".harflex-audit-"+id.New())
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(temporary) }()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	err = each(func(event events.Event) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if redact != nil {
			line = redact(line)
		}
		if !json.Valid(line) {
			return errors.New("invalid redacted event JSON")
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, line); err != nil {
			return err
		}
		line = append(compact.Bytes(), '\n')
		for len(line) > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			size := min(len(line), 64*1024)
			n, err := file.Write(line[:size])
			if err != nil {
				return err
			}
			if n != size {
				return io.ErrShortWrite
			}
			line = line[size:]
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return err
	}
	return syncAuditDirectory(dir)
}

type serviceAuditExporter struct{ service *Service }

func (a serviceAuditExporter) Export(ctx context.Context, in ExportAuditInput) (string, error) {
	redact, err := a.service.auditRedactor(ctx)
	if err != nil {
		return "", err
	}
	defer redact.close()
	// Stream individual rows under the shared history byte/count budget instead
	// of materializing a session's whole history; oversized legacy exports fail closed.
	err = exportJSONL(ctx, in.Destination, redact.auditEvent, func(yield func(events.Event) error) error { return a.service.walkEvents(ctx, in.SessionID, yield) })
	if err != nil {
		return "", err
	}
	return in.Destination, nil
}

func (s *Service) auditRedactor(ctx context.Context) (*sensitiveRedactor, error) {
	s.profileGate.RLock()
	defer s.profileGate.RUnlock()
	profiles, err := s.store.ListCredentialReferences(ctx)
	if err != nil {
		return nil, safe("list audit credentials", err)
	}
	redactor := newSensitiveRedactor()
	retained := false
	defer func() {
		if !retained {
			redactor.close()
		}
	}()
	refs := make(map[secrets.Reference]struct{})
	for _, profile := range profiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if profile.Provider == "" || profile.Account == "" {
			continue
		}
		ref := secrets.Reference{Provider: profile.Provider, Account: profile.Account}
		if _, ok := refs[ref]; ok {
			continue
		}
		refs[ref] = struct{}{}
		if s.secrets == nil {
			return nil, errors.New("audit credential store unavailable")
		}
		value, err := s.secrets.Get(ctx, ref)
		if errors.Is(err, secrets.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, safe("resolve audit credentials", err)
		}
		if value != "" {
			redactor.patterns = append(redactor.patterns, []byte(value))
		}
	}
	retained = true
	return redactor, nil
}

func (r *sensitiveRedactor) auditEvent(line []byte) []byte {
	if r.closed {
		return nil
	}
	if len(r.patterns) == 0 {
		return append([]byte(nil), line...)
	}
	if !json.Valid(line) {
		return nil
	}
	// Decode payload strings before matching: serialized escape bytes are not
	// credential characters. UseNumber preserves exact numeric audit evidence.
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	event, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if data, exists := event["data"]; exists {
		// Historical payload IDs/names are evidence, not live operation handles.
		// Preserve only JSON keys and the canonical event envelope in an export.
		event["data"] = redactAuditValue(data, r.patterns)
	}
	canonical, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	return canonical
}

// Payload keys and the canonical event envelope are structural evidence.
// Only decoded payload string values are credential-bearing surfaces.
func redactAuditValue(value any, patterns [][]byte) any {
	switch value := value.(type) {
	case string:
		return string(redactAuditBytes([]byte(value), patterns))
	case []any:
		for index, item := range value {
			value[index] = redactAuditValue(item, patterns)
		}
		return value
	case map[string]any:
		for key, item := range value {
			value[key] = redactAuditValue(item, patterns)
		}
		return value
	default:
		return value
	}
}

// Union matches before replacing so overlaps cannot expose secret fragments.
// Expand byte matches to rune boundaries to avoid emitting partial UTF-8.
func redactAuditBytes(input []byte, patterns [][]byte) []byte {
	type interval struct{ start, end int }
	matches := make([]interval, 0)
	for _, pattern := range patterns {
		if len(pattern) == 0 {
			continue
		}
		for offset := 0; offset < len(input); {
			index := bytes.Index(input[offset:], pattern)
			if index < 0 {
				break
			}
			start := offset + index
			end := start + len(pattern)
			offset = start + 1
			for start > 0 && !utf8.RuneStart(input[start]) {
				start--
			}
			for end < len(input) && !utf8.RuneStart(input[end]) {
				end++
			}
			matches = append(matches, interval{start, end})
		}
	}
	if len(matches) == 0 {
		return append([]byte(nil), input...)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].start < matches[j].start })
	output := make([]byte, 0, len(input))
	replacement := redactionMarker(patterns)
	last := 0
	for i := 0; i < len(matches); {
		match := matches[i]
		i++
		for i < len(matches) && matches[i].start <= match.end {
			match.end = max(match.end, matches[i].end)
			i++
		}
		output = append(output, input[last:match.start]...)
		output = append(output, replacement...)
		last = match.end
	}
	return append(output, input[last:]...)
}

// A replacement must not itself contain a key or complete a key at either edge.
func redactionMarker(patterns [][]byte) string {
	marker := []byte("[REDACTED]")
	unsafe := false
	for _, pattern := range patterns {
		if len(pattern) == 0 {
			continue
		}
		unsafe = unsafe || bytes.Contains(marker, pattern) || bytes.Contains(pattern, marker)
		for size := 1; size <= min(len(marker), len(pattern)); size++ {
			unsafe = unsafe || bytes.Equal(pattern[:size], marker[len(marker)-size:]) || bytes.Equal(pattern[len(pattern)-size:], marker[:size])
		}
	}
	if !unsafe {
		return string(marker)
	}
	// An absent Unicode rune separates both edges and cannot be a secret itself.
	for candidate := rune(0xE000); candidate <= utf8.MaxRune; candidate++ {
		encoded := []byte(string(candidate))
		found := false
		for _, pattern := range patterns {
			if bytes.Contains(pattern, encoded) {
				found = true
				break
			}
		}
		if !found {
			return string(candidate)
		}
	}
	return ""
}
