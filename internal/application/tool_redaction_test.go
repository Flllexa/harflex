package application

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/events"
)

func TestToolUpdatesRedactEverySplitAcrossStreamsAndCalls(t *testing.T) {
	for _, key := range []string{"synthetic-tool-key", "á秘密🔑"} {
		for split := 1; split < len(key); split++ {
			for _, secondStream := range []string{"stdout", "stderr"} {
				t.Run(strconv.Itoa(len(key))+"/"+strconv.Itoa(split)+"/"+secondStream, func(t *testing.T) {
					s, db, _ := setup(t)
					journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte(key))}
					t.Cleanup(journal.Close)
					var emitted []events.Event
					SetEmitter(s, func(_ string, data any) {
						d := data.(EventDTO)
						emitted = append(emitted, events.Event{Type: d.Type, Data: d.Data, Sequence: d.Sequence})
					})
					appendEvent := func(typ string, data any) events.Event {
						t.Helper()
						e, err := journal.Append(t.Context(), "tools", "agent_session", typ, data)
						if err != nil {
							t.Fatal(err)
						}
						return e
					}
					first := appendEvent("tool.updated", map[string]string{"toolCallId": "one", "stream": "stdout", "text": "before " + key[:split]})
					other := appendEvent("tool.updated", map[string]string{"toolCallId": "two", "stream": "stdout", "text": "other output"})
					appendEvent("tool.updated", map[string]string{"toolCallId": "one", "stream": secondStream, "text": key[split:] + " after"})
					appendEvent("tool.completed", map[string]any{"toolCallId": "one", "name": "bash", "content": map[string]string{"text": key}})
					appendEvent("tool.completed", map[string]any{"toolCallId": "two", "name": "bash", "content": map[string]string{"text": "done"}})
					appendEvent("run.completed", map[string]string{})
					var firstData, otherData map[string]any
					if err := json.Unmarshal(first.Data, &firstData); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(other.Data, &otherData); err != nil {
						t.Fatal(err)
					}
					stored, err := db.ListAfter(t.Context(), "tools", 0)
					if err != nil {
						t.Fatal(err)
					}
					for _, list := range [][]events.Event{stored, emitted} {
						output := map[string]string{}
						closed := map[string]bool{}
						for i, e := range list {
							if e.Sequence != int64(i+1) {
								t.Fatal("noncontiguous journal")
							}
							var data map[string]any
							if err := json.Unmarshal(e.Data, &data); err != nil {
								t.Fatal(err)
							}
							call, _ := data["toolCallId"].(string)
							if e.Type == "tool.updated" {
								if closed[call] {
									t.Fatal("tool tail flushed after completion")
								}
								text := data["text"].(string)
								if !utf8.ValidString(text) {
									t.Fatal("invalid tool UTF-8")
								}
								output[call] += text
							} else if e.Type == "tool.completed" {
								closed[call] = true
							}
						}
						if output[firstData["toolCallId"].(string)] != "before [REDACTED] after" {
							t.Fatal("tool fragments leaked or changed text")
						}
						if output[otherData["toolCallId"].(string)] != "other output" {
							t.Fatal("interleaved tools mixed buffers")
						}
					}
				})
			}
		}
	}
}

func TestToolUpdateTailFlushesBeforeFailureOrRunTerminal(t *testing.T) {
	for _, terminal := range []string{"tool.failed", "run.completed", "run.failed", "run.cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			s, db, _ := setup(t)
			key := "synthetic-tool-secret"
			journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte(key))}
			t.Cleanup(journal.Close)
			for _, text := range []string{key[:8], key[8:]} {
				if _, err := journal.Append(t.Context(), "terminal", "agent_session", "tool.updated", map[string]string{"toolCallId": "call", "stream": "stderr", "text": text}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := journal.Append(t.Context(), "terminal", "agent_session", terminal, map[string]string{"toolCallId": "call"}); err != nil {
				t.Fatal(err)
			}
			items, err := db.ListAfter(t.Context(), "terminal", 0)
			if err != nil {
				t.Fatal(err)
			}
			combined := ""
			for _, e := range items {
				if e.Type == "tool.updated" {
					var d map[string]string
					if err := json.Unmarshal(e.Data, &d); err != nil {
						t.Fatal(err)
					}
					combined += d["text"]
				}
			}
			if strings.Contains(combined, key) || combined != "[REDACTED]" {
				t.Fatal("tool tail was not redacted/flushed")
			}
			if items[len(items)-1].Type != terminal {
				t.Fatal("tail emitted after terminal")
			}
		})
	}
}

func TestShutdownFlushesPendingToolTailsAndClearsAliases(t *testing.T) {
	s, db, _ := setup(t)
	journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte("synthetic-tool-secret"))}
	s.journals["shutdown"] = journal
	var emitted []EventDTO
	SetEmitter(s, func(_ string, data any) { emitted = append(emitted, data.(EventDTO)) })
	if _, err := journal.Append(t.Context(), "shutdown", "agent_session", "tool.updated", map[string]string{"toolCallId": "actual-call", "stream": "stdout", "text": "tail"}); err != nil {
		t.Fatal(err)
	}
	aliases := journal.redactor.aliases
	Shutdown(s)
	stored, err := db.ListAfter(t.Context(), "shutdown", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(emitted) {
		t.Fatal("shutdown flush not emitted after persistence")
	}
	text := ""
	for _, event := range stored {
		var data map[string]string
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		text += data["text"]
	}
	if text != "tail" {
		t.Fatal("shutdown discarded pending tool text")
	}
	if len(aliases.tools) != 0 || len(aliases.actualTools) != 0 || len(aliases.approvals) != 0 || len(aliases.actualApprovals) != 0 {
		t.Fatal("shutdown retained alias registry")
	}
}

func TestConcurrentShutdownWaitsForPendingFlush(t *testing.T) {
	s, db, _ := setup(t)
	journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor([]byte("synthetic-tool-secret"))}
	s.journals["shutdown"] = journal
	if _, err := journal.Append(t.Context(), "shutdown", "agent_session", "tool.updated", map[string]string{"toolCallId": "actual-call", "stream": "stdout", "text": "tail"}); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	SetEmitter(s, func(string, any) { close(started); <-release })
	first, second := make(chan struct{}), make(chan struct{})
	go func() { Shutdown(s); close(first) }()
	<-started
	go func() { Shutdown(s); close(second) }()
	select {
	case <-second:
		close(release)
		<-first
		t.Fatal("concurrent shutdown returned before journal flush completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	<-first
	<-second
}

func TestJournalEmitterCanReenterWithoutLosingOrder(t *testing.T) {
	s, db, _ := setup(t)
	journal := &eventJournal{store: db, service: s, redactor: newSensitiveRedactor()}
	var sequences []int64
	SetEmitter(s, func(_ string, payload any) {
		event := payload.(EventDTO)
		sequences = append(sequences, event.Sequence)
		if event.Type == "message.user" {
			if _, err := journal.Append(t.Context(), "reentrant", "agent_session", "usage.recorded", map[string]int{"inputTokens": 1}); err != nil {
				t.Error(err)
			}
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := journal.Append(t.Context(), "reentrant", "agent_session", "message.user", map[string]string{"content": "hello"})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		if len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 2 {
			t.Fatal("reentrant emission lost journal order")
		}
		journal.Close()
	case <-time.After(500 * time.Millisecond):
		t.Fatal("emitter reentrancy deadlocked the journal")
	}
}
