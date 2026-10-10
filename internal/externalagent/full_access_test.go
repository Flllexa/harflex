package externalagent

import (
	"slices"
	"testing"
)

func argsOf(t *testing.T, adapter Adapter, r Request) []string {
	t.Helper()
	args, _ := adapter.(*cliAdapter).build(r)
	return args
}

func valueAfter(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func TestFullAccessLiftsTheCLIPermissionLimits(t *testing.T) {
	if got := valueAfter(argsOf(t, NewClaudeCode(""), Request{Prompt: "x"}), "--permission-mode"); got != "acceptEdits" {
		t.Fatalf("Claude Code by default: %q", got)
	}
	if got := valueAfter(argsOf(t, NewClaudeCode(""), Request{Prompt: "x", FullAccess: true}), "--permission-mode"); got != "bypassPermissions" {
		t.Fatalf("Claude Code on Full access: %q", got)
	}
	if got := valueAfter(argsOf(t, NewCodex(""), Request{Prompt: "x"}), "--sandbox"); got != "workspace-write" {
		t.Fatalf("Codex by default: %q", got)
	}
	if got := valueAfter(argsOf(t, NewCodex(""), Request{Prompt: "x", FullAccess: true}), "--sandbox"); got != "danger-full-access" {
		t.Fatalf("Codex on Full access: %q", got)
	}
}

func TestFullAccessIsOnlyForOrdinaryChats(t *testing.T) {
	if err := validateRequest(Request{Prompt: "x", CWD: t.TempDir(), FullAccess: true}, "claude", true); err != nil {
		t.Fatalf("an ordinary chat on Full access: %v", err)
	}
	for name, request := range map[string]Request{
		"a read-only run":  {Sandbox: "read-only", IgnoreUserConfig: true, Ephemeral: true},
		"a sandboxed run":  {Sandbox: "workspace-write"},
		"an ephemeral run": {Ephemeral: true},
		"an isolated Code": {Policy: "sdd_code", Sandbox: "workspace-write", IgnoreUserConfig: true, ApproveForMe: true},
	} {
		request.Prompt, request.CWD, request.FullAccess = "x", t.TempDir(), true
		if err := validateRequest(request, "codex", true); err == nil {
			t.Fatalf("%s took Full access", name)
		}
	}
}
