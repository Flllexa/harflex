package externalagent

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// A Windows app opened from the Start menu keeps the PATH of the login, so a CLI installed later is found where its
// installer puts it.
func TestWindowsCLIsAreLookedUpWhereTheirInstallersPutThem(t *testing.T) {
	home, roaming, local := `C:\Users\p`, `C:\Users\p\AppData\Roaming`, `C:\Users\p\AppData\Local`
	if got, want := windowsCLICandidates("claude", home, roaming, local), []string{
		`C:\Users\p\.local\bin\claude.exe`,
		`C:\Users\p\AppData\Roaming\npm\claude.cmd`,
		`C:\Users\p\AppData\Local\pnpm\claude.cmd`,
		`C:\Users\p\scoop\shims\claude.exe`,
		`C:\Users\p\AppData\Local\Microsoft\WinGet\Links\claude.exe`,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Claude Code on Windows: %v", got)
	}
	if got := windowsCLICandidates("codex", home, roaming, local); len(got) != 5 || got[1] != `C:\Users\p\AppData\Roaming\npm\codex.cmd` {
		t.Fatalf("Codex on Windows: %v", got)
	}
	if got := windowsCLICandidates("codex", "", "", ""); len(got) != 0 {
		t.Fatalf("without the user's folders there is nothing to look up: %v", got)
	}
}

// Node, npm shims and Claude Code read these on Windows; none of them carries a secret.
func TestWindowsCLIsKeepTheSystemFoldersTheyNeed(t *testing.T) {
	source := []string{`Path=C:\bin`, `APPDATA=C:\Users\p\AppData\Roaming`, `LocalAppData=C:\Users\p\AppData\Local`, `ProgramFiles=C:\Program Files`, `SystemDrive=C:`, `USERNAME=p`, `CLAUDE_CODE_GIT_BASH_PATH=C:\Git\bin\bash.exe`, `OPENAI_API_KEY=blocked`}
	claude := buildEnvironment("claude", source, "windows")
	for _, want := range []string{`APPDATA=C:\Users\p\AppData\Roaming`, `LocalAppData=C:\Users\p\AppData\Local`, `ProgramFiles=C:\Program Files`, `SystemDrive=C:`, `USERNAME=p`, `CLAUDE_CODE_GIT_BASH_PATH=C:\Git\bin\bash.exe`} {
		if !slices.Contains(claude, want) {
			t.Fatalf("Claude Code on Windows lost %s: %v", want, claude)
		}
	}
	if slices.Contains(claude, "OPENAI_API_KEY=blocked") {
		t.Fatal("a key leaked into the CLI environment")
	}
	if slices.Contains(buildEnvironment("codex", source, "windows"), `CLAUDE_CODE_GIT_BASH_PATH=C:\Git\bin\bash.exe`) {
		t.Fatal("Codex received Claude Code's setting")
	}
	if slices.Contains(buildEnvironment("claude", source, "linux"), `APPDATA=C:\Users\p\AppData\Roaming`) {
		t.Fatal("Windows folders reached a Unix CLI")
	}
}

func TestDetectionSaysWhyACLIIsUnavailable(t *testing.T) {
	missing := &cliAdapter{id: "codex", path: filepath.Join(t.TempDir(), "codex")}
	if got := missing.Detect(); got.Available || got.Reason != "not_found" {
		t.Fatalf("missing CLI: %+v", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	broken := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\necho 'needs git-bash' >&2\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	got := (&cliAdapter{id: "codex", path: broken}).Detect()
	if got.Available || got.Reason != "failed" || !strings.Contains(got.Detail, "needs git-bash") {
		t.Fatalf("CLI that fails --version: %+v", got)
	}
}
