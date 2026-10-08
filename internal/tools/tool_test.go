package tools

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/security"
)

type testTool struct {
	spec    agentcore.ToolSpec
	risk    security.Risk
	execute func(context.Context, json.RawMessage, agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error)
}

func (t *testTool) Spec() agentcore.ToolSpec { return t.spec }
func (t *testTool) Risk() security.Risk      { return t.risk }
func (t *testTool) Execute(ctx context.Context, args json.RawMessage, update agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	return t.execute(ctx, args, update)
}

func TestRegistryRejectsInvalidTools(t *testing.T) {
	var typedNil *testTool
	for _, tc := range []struct {
		name  string
		items []Tool
	}{
		{"nil", []Tool{nil}},
		{"typed nil", []Tool{typedNil}},
		{"empty name", []Tool{&testTool{}}},
		{"duplicate", []Tool{&testTool{spec: agentcore.ToolSpec{Name: "same"}}, &testTool{spec: agentcore.ToolSpec{Name: "same"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewRegistry(tc.items...); err == nil {
				t.Fatal("NewRegistry succeeded, want error")
			}
		})
	}
}

func TestRegistryGetAndRisk(t *testing.T) {
	item := &testTool{spec: agentcore.ToolSpec{Name: "read"}, risk: security.ReadOnly}
	registry, err := NewRegistry(item)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := registry.Get("read"); !ok || got != item {
		t.Fatalf("Get(read) = %v, %v", got, ok)
	}
	if got, ok := registry.Get("absent"); ok || got != nil {
		t.Fatalf("Get(absent) = %v, %v", got, ok)
	}
	if got := registry.Risk("read"); got != string(security.ReadOnly) {
		t.Fatalf("Risk(read) = %q", got)
	}
	if got := registry.Risk("absent"); got != string(security.Destructive) {
		t.Fatalf("Risk(absent) = %q, want fail-closed destructive", got)
	}
}

func TestRegistrySpecsSortedAndDefensive(t *testing.T) {
	registry, err := NewRegistry(
		&testTool{spec: agentcore.ToolSpec{Name: "z", Schema: json.RawMessage(`{"type":"object"}`)}},
		&testTool{spec: agentcore.ToolSpec{Name: "a", Description: "first", Schema: json.RawMessage(`{"type":"string"}`)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []agentcore.ToolSpec{
		{Name: "a", Description: "first", Schema: json.RawMessage(`{"type":"string"}`)},
		{Name: "z", Schema: json.RawMessage(`{"type":"object"}`)},
	}
	got := registry.Specs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Specs = %+v, want %+v", got, want)
	}
	got[0].Name = "changed"
	got[0].Schema[0] = '!'
	got[1] = agentcore.ToolSpec{}
	if next := registry.Specs(); !reflect.DeepEqual(next, want) {
		t.Fatalf("mutating Specs changed registry: %+v", next)
	}
}

func TestRegistryExecuteRejectsUnknownTool(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	result, err := registry.Execute(context.Background(), "absent", nil, nil)
	if !errors.Is(err, ErrToolNotFound) || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("Execute error = %v, want named ErrToolNotFound", err)
	}
	if !reflect.DeepEqual(result, agentcore.ToolExecutionResult{}) {
		t.Fatalf("Execute unknown returned result: %+v", result)
	}
}

func TestRegistryExecuteDelegates(t *testing.T) {
	for _, executionErr := range []error{nil, errors.New("execution failed")} {
		t.Run("error="+errorLabel(executionErr), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			args := json.RawMessage(`{"path":"file"}`)
			wantResult := agentcore.ToolExecutionResult{Content: json.RawMessage(`"done"`), Details: json.RawMessage(`{"bytes":4}`)}
			wantUpdate := agentcore.ToolUpdate{Stream: "stdout", Text: "progress"}
			var updates []agentcore.ToolUpdate
			called := false
			item := &testTool{spec: agentcore.ToolSpec{Name: "selected"}, execute: func(gotCtx context.Context, gotArgs json.RawMessage, update agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
				called = true
				if gotCtx != ctx || !reflect.DeepEqual(gotArgs, args) {
					t.Fatalf("Execute changed context or arguments")
				}
				update(gotCtx, wantUpdate)
				return wantResult, executionErr
			}}
			other := &testTool{spec: agentcore.ToolSpec{Name: "other"}, execute: func(context.Context, json.RawMessage, agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
				t.Fatal("wrong tool executed")
				return agentcore.ToolExecutionResult{}, nil
			}}
			registry, err := NewRegistry(other, item)
			if err != nil {
				t.Fatal(err)
			}
			got, err := registry.Execute(ctx, "selected", args, func(effective context.Context, update agentcore.ToolUpdate) {
				if effective != ctx {
					t.Error("registry changed the effective sink context")
				}
				updates = append(updates, update)
			})
			if !called || !reflect.DeepEqual(got, wantResult) || !errors.Is(err, executionErr) || !reflect.DeepEqual(updates, []agentcore.ToolUpdate{wantUpdate}) {
				t.Fatalf("Execute = %+v, %v; called=%v, updates=%+v", got, err, called, updates)
			}
		})
	}
}

func errorLabel(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}
