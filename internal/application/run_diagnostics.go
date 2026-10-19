package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
)

// diagnosticLogBytes is where harflex.log rolls over to harflex.log.1.
const diagnosticLogBytes = 5 * 1024 * 1024

const diagnosticDetailBytes = 4096

// secretShapes masks what looks like a credential in a cause the vault does not know (a key in a CLI's message).
var secretShapes = regexp.MustCompile(`(?i)\b(sk|pk|rk|ghp|gho|xox[abp])[-_][A-Za-z0-9_-]{6,}|\bbearer\s+\S+|\b(api[_-]?key|token|secret|password)\s*[:=]\s*\S+`)

// diagnosticLog keeps, one JSON line each, why runs ended in an error: the person can read it and send it along.
type diagnosticLog struct {
	mu   sync.Mutex
	path string
}

func newDiagnosticLog(dir string) *diagnosticLog {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	return &diagnosticLog{path: filepath.Join(dir, "harflex.log")}
}

func (d *diagnosticLog) write(entry map[string]any) {
	if d == nil {
		return
	}
	entry["time"] = time.Now().UTC().Format(time.RFC3339Nano)
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(d.path), 0o700); err != nil {
		return
	}
	if info, err := os.Stat(d.path); err == nil && info.Size() > diagnosticLogBytes {
		_ = os.Rename(d.path, d.path+".1")
	}
	file, err := os.OpenFile(d.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = file.Write(append(line, '\n'))
	_ = file.Close()
}

// diagnosticText is every message along an error's causes, the ones the public text hides included (a CLI's masked
// stderr, a SQLite error), each once.
func diagnosticText(err error) string {
	var texts []string
	var walk func(error)
	walk = func(err error) {
		if err == nil {
			return
		}
		texts = append(texts, err.Error())
		switch wrapped := err.(type) {
		case interface{ Unwrap() []error }:
			for _, inner := range wrapped.Unwrap() {
				walk(inner)
			}
		case interface{ Unwrap() error }:
			walk(wrapped.Unwrap())
		}
	}
	walk(err)
	var kept []string
	for index, text := range texts {
		covered := false
		for other, longer := range texts {
			if other != index && len(longer) > len(text) && strings.Contains(longer, text) {
				covered = true
				break
			}
		}
		if !covered && !containsString(kept, text) {
			kept = append(kept, text)
		}
	}
	return strings.Join(kept, " | ")
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

// logRunFailure records why a run ended in an error, masked with the session's credentials and the usual secret shapes.
func (s *Service) logRunFailure(sessionID, operation string, runErr error) {
	if s.diagnostics == nil || runErr == nil {
		return
	}
	detail := diagnosticText(runErr)
	s.mu.RLock()
	journal := s.journals[sessionID]
	s.mu.RUnlock()
	if journal != nil {
		journal.mu.Lock()
		if journal.redactor != nil && !journal.redactor.closed {
			detail = string(redactAuditBytes([]byte(detail), journal.redactor.patterns))
		}
		journal.mu.Unlock()
	}
	detail = secretShapes.ReplaceAllString(detail, "[redacted]")
	detail = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(detail, "�"))
	if len(detail) > diagnosticDetailBytes {
		detail = detail[:diagnosticDetailBytes] + " [truncated]"
	}
	entry := map[string]any{"event": "run.error", "sessionId": sessionID, "operation": operation, "code": ErrorCode(runErr), "detail": detail}
	var ended *agentcore.RunError
	if errors.As(runErr, &ended) {
		entry["reason"] = ended.Reason
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) {
		entry["exitCode"] = exit.ExitCode()
	}
	s.diagnostics.write(entry)
}

// closeFaultedRun ends a run whose journal could not be written. The runner stops writing at the first failure, so
// the run is closed from the journal the way recovery closes one after a crash: its pending tool calls are answered,
// it is marked interrupted, and the session leaves "running". The next message opens the conversation again.
func (s *Service) closeFaultedRun(record catalog.SessionRecord) bool {
	// Only a run the journal still shows open is closed: one whose end was written (the catalog failed after it)
	// keeps the runner's own outcome, and reopening reconciles its status.
	readCtx, cancelRead := context.WithTimeout(s.ctx, 30*time.Second)
	history, err := s.restoreHistory(readCtx, record.ID, true)
	cancelRead()
	if err != nil || !history.active {
		return false
	}
	s.mu.Lock()
	journal := s.journals[record.ID]
	delete(s.sessions, record.ID)
	delete(s.journals, record.ID)
	s.mu.Unlock()
	if journal != nil {
		journal.Close()
	}
	// The write failed because the database was busy or briefly unavailable: recovery waits for it a while.
	deadline := time.Now().Add(30 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		current, err := s.store.GetSession(ctx, record.ID)
		if err == nil {
			err = s.recoverSession(ctx, current)
		}
		cancel()
		if err == nil {
			// The conversation goes on from its journal: the next message finds it open, as after a restart.
			_, _ = s.OpenSession(OpenSessionInput{SessionID: record.ID, WorkspaceID: record.WorkspaceID})
			return true
		}
		if time.Now().After(deadline) || s.ctx.Err() != nil {
			s.diagnostics.write(map[string]any{"event": "run.close_failed", "sessionId": record.ID, "detail": secretShapes.ReplaceAllString(diagnosticText(err), "[redacted]")})
			return false
		}
		select {
		case <-s.ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
}
