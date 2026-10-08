package sdd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxAuthoringDrafts           = 3
	MaxAuthoringAttempts         = 6
	MaxAuthoringOutputTokens     = 4096
	MaxAuthoringCodeTurns        = 6
	MaxAuthoringCodeOutputTokens = MaxAuthoringCodeTurns * MaxAuthoringOutputTokens
	MaxAuthoringCodeToolCalls    = 64
	MaxAuthoringDocumentBytes    = 64 * 1024
	MaxAuthoringInputTokens      = 262144
	AuthoringInputBudget         = MaxAuthoringAttempts * MaxAuthoringInputTokens
	AuthoringOutputBudget        = MaxAuthoringAttempts * MaxAuthoringOutputTokens
	AuthoringAttemptTimeout      = 90 * time.Second
)

var ErrInvalidDocument = errors.New("invalid authoring document")
var ErrAuthoringBudgetExceeded = errors.New("authoring budget exceeded")
var ErrAuthoringCancellationPending = errors.New("authoring cancellation pending")
var documentID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

type AcceptanceCriterion struct {
	ID        string `json:"id"`
	Criterion string `json:"criterion"`
}
type SpecDocument struct {
	Summary            string                `json:"summary"`
	Requirements       []string              `json:"requirements"`
	NonGoals           []string              `json:"nonGoals"`
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptanceCriteria"`
}
type PlanTask struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Files     []string `json:"files"`
	Steps     []string `json:"steps"`
	Tests     []string `json:"tests"`
	DependsOn []string `json:"dependsOn"`
}
type PlanDocument struct {
	Summary string     `json:"summary"`
	Tasks   []PlanTask `json:"tasks"`
	Risks   []string   `json:"risks"`
}

func documentText(v string) bool {
	return strings.TrimSpace(v) != "" && len(v) <= 16*1024 && utf8.ValidString(v) && !strings.ContainsRune(v, 0)
}
func documentStrings(v []string, empty bool) bool {
	if v == nil || len(v) > 128 || (!empty && len(v) == 0) {
		return false
	}
	for _, s := range v {
		if !documentText(s) {
			return false
		}
	}
	return true
}

// Each object is walked before typed decoding: duplicate, case-folded, unknown
// and missing keys must not be silently normalized by encoding/json.
func documentObject(data []byte, keys ...string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrInvalidDocument
	}
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, ErrInvalidDocument
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || fields[key] != nil {
			return nil, ErrInvalidDocument
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil {
			return nil, ErrInvalidDocument
		}
		fields[key] = raw
	}
	if token, err = dec.Token(); err != nil || token != json.Delim('}') || len(fields) != len(keys) {
		return nil, ErrInvalidDocument
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, ErrInvalidDocument
	}
	return fields, nil
}

// CanonicalAuthoringDocument validates the complete bounded output and preserves
// model-supplied stable IDs. It performs no filesystem, network or tool work.
func CanonicalAuthoringDocument(stage Stage, data []byte) (json.RawMessage, error) {
	if len(data) == 0 || len(data) > MaxAuthoringDocumentBytes || !utf8.Valid(data) {
		return nil, ErrInvalidDocument
	}
	var value any
	switch stage {
	case Spec:
		fields, err := documentObject(data, "summary", "requirements", "nonGoals", "acceptanceCriteria")
		if err != nil {
			return nil, err
		}
		var raw []json.RawMessage
		if json.Unmarshal(fields["acceptanceCriteria"], &raw) != nil || len(raw) == 0 || len(raw) > 128 {
			return nil, ErrInvalidDocument
		}
		for _, item := range raw {
			if _, err := documentObject(item, "id", "criterion"); err != nil {
				return nil, err
			}
		}
		var doc SpecDocument
		if json.Unmarshal(data, &doc) != nil || !documentText(doc.Summary) || !documentStrings(doc.Requirements, false) || !documentStrings(doc.NonGoals, false) {
			return nil, ErrInvalidDocument
		}
		ids := map[string]bool{}
		for _, c := range doc.AcceptanceCriteria {
			if !documentID.MatchString(c.ID) || ids[c.ID] || !documentText(c.Criterion) {
				return nil, ErrInvalidDocument
			}
			ids[c.ID] = true
		}
		value = doc
	case Plan:
		fields, err := documentObject(data, "summary", "tasks", "risks")
		if err != nil {
			return nil, err
		}
		var raw []json.RawMessage
		if json.Unmarshal(fields["tasks"], &raw) != nil || len(raw) == 0 || len(raw) > 128 {
			return nil, ErrInvalidDocument
		}
		for _, item := range raw {
			if _, err := documentObject(item, "id", "title", "files", "steps", "tests", "dependsOn"); err != nil {
				return nil, err
			}
		}
		var doc PlanDocument
		if json.Unmarshal(data, &doc) != nil || !documentText(doc.Summary) || !documentStrings(doc.Risks, false) {
			return nil, ErrInvalidDocument
		}
		graph := map[string][]string{}
		for _, task := range doc.Tasks {
			if !documentID.MatchString(task.ID) || graph[task.ID] != nil || !documentText(task.Title) || !documentStrings(task.Files, true) || !documentStrings(task.Steps, false) || !documentStrings(task.Tests, false) || !documentStrings(task.DependsOn, true) {
				return nil, ErrInvalidDocument
			}
			graph[task.ID] = task.DependsOn
		}
		visiting, done := map[string]bool{}, map[string]bool{}
		var visit func(string) bool
		visit = func(id string) bool {
			deps, exists := graph[id]
			if !exists || visiting[id] {
				return false
			}
			if done[id] {
				return true
			}
			visiting[id] = true
			seen := map[string]bool{}
			for _, dep := range deps {
				if seen[dep] || !visit(dep) {
					return false
				}
				seen[dep] = true
			}
			visiting[id] = false
			done[id] = true
			return true
		}
		for id := range graph {
			if !visit(id) {
				return nil, ErrInvalidDocument
			}
		}
		value = doc
	default:
		return nil, ErrInvalidDocument
	}
	canonical, err := json.Marshal(value)
	if err != nil || len(canonical) > MaxAuthoringDocumentBytes {
		return nil, ErrInvalidDocument
	}
	return canonical, nil
}
