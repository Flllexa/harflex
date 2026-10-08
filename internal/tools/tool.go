package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/security"
)

var ErrToolNotFound = errors.New("tool not found")

type Tool interface {
	Spec() agentcore.ToolSpec
	Risk() security.Risk
	// Streaming tools pass their effective execution context to the trusted sink.
	// Blocking sink work must cooperate with that context to allow bounded return.
	Execute(context.Context, json.RawMessage, agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry(items ...Tool) (*Registry, error) {
	registry := &Registry{tools: make(map[string]Tool, len(items))}
	for _, item := range items {
		if isNilTool(item) {
			return nil, fmt.Errorf("register tool: tool is nil")
		}
		name := item.Spec().Name
		if name == "" {
			return nil, fmt.Errorf("register tool: name is empty")
		}
		if _, exists := registry.tools[name]; exists {
			return nil, fmt.Errorf("register tool %q: duplicate name", name)
		}
		registry.tools[name] = item
	}
	return registry, nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	item, ok := r.tools[name]
	return item, ok
}

func (r *Registry) Specs() []agentcore.ToolSpec {
	specs := make([]agentcore.ToolSpec, 0, len(r.tools))
	for _, item := range r.tools {
		spec := item.Spec()
		spec.Schema = append(json.RawMessage(nil), spec.Schema...)
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

func (r *Registry) Risk(name string) string {
	item, ok := r.Get(name)
	if !ok {
		return string(security.Destructive)
	}
	return string(item.Risk())
}

func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage, update agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	item, ok := r.Get(name)
	if !ok {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("execute tool %q: %w", name, ErrToolNotFound)
	}
	return item.Execute(ctx, args, update)
}

func isNilTool(item Tool) bool {
	if item == nil {
		return true
	}
	value := reflect.ValueOf(item)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
