package security

import "testing"

func TestDecide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request Request
		want    Decision
	}{
		{
			name:    "ask allows read-only requests within the root",
			request: Request{Profile: Ask, Risk: ReadOnly, WithinRoot: true},
			want:    Allow,
		},
		{
			name:    "ask requires confirmation for write requests within the root",
			request: Request{Profile: Ask, Risk: Write, WithinRoot: true},
			want:    AskUser,
		},
		{
			name:    "ask requires confirmation for shell requests within the root",
			request: Request{Profile: Ask, Risk: Shell, WithinRoot: true},
			want:    AskUser,
		},
		{
			name:    "ask requires confirmation for network requests within the root",
			request: Request{Profile: Ask, Risk: Network, WithinRoot: true},
			want:    AskUser,
		},
		{
			name:    "trusted workspace allows read-only requests within the root",
			request: Request{Profile: TrustedWorkspace, Risk: ReadOnly, WithinRoot: true},
			want:    Allow,
		},
		{
			name:    "trusted workspace allows write requests within the root",
			request: Request{Profile: TrustedWorkspace, Risk: Write, WithinRoot: true},
			want:    Allow,
		},
		{
			name:    "trusted workspace requires confirmation for shell requests within the root",
			request: Request{Profile: TrustedWorkspace, Risk: Shell, WithinRoot: true},
			want:    AskUser,
		},
		{
			name:    "trusted workspace requires confirmation for network requests within the root",
			request: Request{Profile: TrustedWorkspace, Risk: Network, WithinRoot: true},
			want:    AskUser,
		},
		{
			name:    "ask denies read-only requests outside the root",
			request: Request{Profile: Ask, Risk: ReadOnly, WithinRoot: false},
			want:    Deny,
		},
		{
			name:    "trusted workspace denies write requests outside the root",
			request: Request{Profile: TrustedWorkspace, Risk: Write, WithinRoot: false},
			want:    Deny,
		},
		{
			name:    "sandbox denies shell requests outside the root",
			request: Request{Profile: Sandbox, Risk: Shell, WithinRoot: false, SandboxReady: true},
			want:    Deny,
		},
		{
			name:    "sandbox denies shell requests when unavailable",
			request: Request{Profile: Sandbox, Risk: Shell, WithinRoot: true, SandboxReady: false},
			want:    Deny,
		},
		{
			name:    "unavailable sandbox denies read-only requests within the root",
			request: Request{Profile: Sandbox, Risk: ReadOnly, WithinRoot: true, SandboxReady: false},
			want:    Deny,
		},
		{
			name:    "unavailable sandbox denies write requests within the root",
			request: Request{Profile: Sandbox, Risk: Write, WithinRoot: true, SandboxReady: false},
			want:    Deny,
		},
		{
			name:    "unavailable sandbox denies network requests within the root",
			request: Request{Profile: Sandbox, Risk: Network, WithinRoot: true, SandboxReady: false},
			want:    Deny,
		},
		{
			name:    "ready sandbox allows read-only requests within the root",
			request: Request{Profile: Sandbox, Risk: ReadOnly, WithinRoot: true, SandboxReady: true},
			want:    Allow,
		},
		{
			name:    "ready sandbox allows write requests within the root",
			request: Request{Profile: Sandbox, Risk: Write, WithinRoot: true, SandboxReady: true},
			want:    Allow,
		},
		{
			name:    "ready sandbox allows shell requests within the root",
			request: Request{Profile: Sandbox, Risk: Shell, WithinRoot: true, SandboxReady: true},
			want:    Allow,
		},
		{
			name:    "ready sandbox allows network requests within the root",
			request: Request{Profile: Sandbox, Risk: Network, WithinRoot: true, SandboxReady: true},
			want:    Allow,
		},
		{
			name:    "ask requires confirmation for destructive requests within the root",
			request: Request{Profile: Ask, Risk: Destructive, WithinRoot: true},
			want:    AskUser,
		},
		{
			name:    "trusted workspace requires confirmation for destructive requests within the root",
			request: Request{Profile: TrustedWorkspace, Risk: Destructive, WithinRoot: true},
			want:    AskUser,
		},
		{
			name:    "ready sandbox requires confirmation for destructive requests within the root",
			request: Request{Profile: Sandbox, Risk: Destructive, WithinRoot: true, SandboxReady: true},
			want:    AskUser,
		},
		{
			name:    "unavailable sandbox denies destructive requests before confirmation",
			request: Request{Profile: Sandbox, Risk: Destructive, WithinRoot: true, SandboxReady: false},
			want:    Deny,
		},
		{
			name:    "full access allows reads within the root",
			request: Request{Profile: FullAccess, Risk: ReadOnly, WithinRoot: true},
			want:    Allow,
		},
		{
			name:    "full access allows writes within the root",
			request: Request{Profile: FullAccess, Risk: Write, WithinRoot: true},
			want:    Allow,
		},
		{
			name:    "full access allows shell commands within the root without asking",
			request: Request{Profile: FullAccess, Risk: Shell, WithinRoot: true},
			want:    Allow,
		},
		{
			name:    "full access allows network tools within the root without asking",
			request: Request{Profile: FullAccess, Risk: Network, WithinRoot: true},
			want:    Allow,
		},
		{
			name:    "full access does not ask for destructive requests within the root",
			request: Request{Profile: FullAccess, Risk: Destructive, WithinRoot: true},
			want:    Allow,
		},
		{
			name:    "full access still denies unknown risks",
			request: Request{Profile: FullAccess, Risk: Risk("unknown"), WithinRoot: true},
			want:    Deny,
		},
		{
			name:    "full access never reaches outside the root",
			request: Request{Profile: FullAccess, Risk: Shell, WithinRoot: false},
			want:    Deny,
		},
		{
			name:    "unknown profile is denied",
			request: Request{Profile: Profile("unknown"), Risk: ReadOnly, WithinRoot: true},
			want:    Deny,
		},
		{
			name:    "unknown risk is denied",
			request: Request{Profile: Ask, Risk: Risk("unknown"), WithinRoot: true},
			want:    Deny,
		},
		{
			name:    "trusted workspace denies unknown risks",
			request: Request{Profile: TrustedWorkspace, Risk: Risk("unknown"), WithinRoot: true},
			want:    Deny,
		},
		{
			name:    "ready sandbox denies unknown risks",
			request: Request{Profile: Sandbox, Risk: Risk("unknown"), WithinRoot: true, SandboxReady: true},
			want:    Deny,
		},
		{
			name:    "unknown profiles deny destructive requests",
			request: Request{Profile: Profile("unknown"), Risk: Destructive, WithinRoot: true},
			want:    Deny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := Decide(tt.request); got != tt.want {
				t.Fatalf("Decide(%+v) = %q, want %q", tt.request, got, tt.want)
			}
		})
	}
}

func TestDecideDeniesEveryProfileAndRiskOutsideRoot(t *testing.T) {
	t.Parallel()

	profiles := []Profile{Ask, TrustedWorkspace, FullAccess, Sandbox, Profile("unknown")}
	risks := []Risk{ReadOnly, Write, Shell, Network, Destructive, Risk("unknown")}

	for _, profile := range profiles {
		for _, risk := range risks {
			name := string(profile) + "_" + string(risk)
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				request := Request{
					Profile:      profile,
					Risk:         risk,
					WithinRoot:   false,
					SandboxReady: true,
				}
				if got := Decide(request); got != Deny {
					t.Fatalf("Decide(%+v) = %q, want %q", request, got, Deny)
				}
			})
		}
	}
}
