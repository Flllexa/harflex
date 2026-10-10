//go:build darwin

package updater

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBundleOfOnlyAcceptsAnExecutableInsideAnApp(t *testing.T) {
	if bundle, ok := bundleOf("/Applications/Harflex.app/Contents/MacOS/harflex"); !ok || bundle != "/Applications/Harflex.app" {
		t.Fatalf("an installed app: %q %v", bundle, ok)
	}
	for _, path := range []string{"/tmp/harflex", "/usr/local/bin/harflex", "/x/MacOS/harflex", "/x/Contents/MacOS/harflex"} {
		if _, ok := bundleOf(path); ok {
			t.Fatalf("%s is not inside an app", path)
		}
	}
}

// The helper has to run on a real shell: it swaps the bundle once the process is gone, opens the new app, and cleans up.
func TestSwapScriptReplacesTheBundleAndRestoresItWhenTheNewOneIsMissing(t *testing.T) {
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	for name, hasNew := range map[string]bool{"swaps": true, "restores": false} {
		root := t.TempDir()
		bundle := filepath.Join(root, "it's Harflex.app")
		staging := filepath.Join(root, ".harflex-update-1")
		next := filepath.Join(staging, "it's Harflex.app")
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		must(os.MkdirAll(bundle, 0o755))
		must(os.WriteFile(filepath.Join(bundle, "version"), []byte("old"), 0o644))
		must(os.MkdirAll(staging, 0o755))
		if hasNew {
			must(os.MkdirAll(next, 0o755))
			must(os.WriteFile(filepath.Join(next, "version"), []byte("new"), 0o644))
		}
		script := filepath.Join(staging, "swap.sh")
		must(os.WriteFile(script, []byte(swapScript(gone.Process.Pid, bundle, next, staging, "/usr/bin/true")), 0o700))
		if out, err := exec.Command("/bin/sh", script).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", name, err, out)
		}
		want := "old"
		if hasNew {
			want = "new"
		}
		got, err := os.ReadFile(filepath.Join(bundle, "version"))
		if err != nil || string(got) != want {
			t.Fatalf("%s: bundle holds %q (%v), want %q", name, got, err, want)
		}
		if _, err := os.Stat(bundle + ".old"); err == nil {
			t.Fatalf("%s: the old bundle was left behind", name)
		}
		if _, err := os.Stat(staging); err == nil {
			t.Fatalf("%s: the staging folder was left behind", name)
		}
	}
}
