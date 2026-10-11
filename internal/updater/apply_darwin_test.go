//go:build darwin

package updater

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
		must(os.WriteFile(script, []byte(swapScript(gone.Process.Pid, bundle, next, staging, "/usr/bin/true", "")), 0o700))
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

func TestSwapScriptInstallsWhereNoAppIsYetAndEjectsTheImageItRanFrom(t *testing.T) {
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "Applications", "harflex.app")
	staging := filepath.Join(root, "Applications", ".harflex-update-1")
	next := filepath.Join(staging, "harflex.app")
	if err := os.MkdirAll(next, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(next, "version"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	opened := filepath.Join(root, "opened")
	open := filepath.Join(root, "open.sh")
	if err := os.WriteFile(open, []byte("#!/bin/sh\necho \"$1\" > "+shellQuote(opened)+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := swapScript(gone.Process.Pid, target, next, staging, open, "/Volumes/harflex-test-not-mounted")
	if !strings.Contains(script, "hdiutil detach '/Volumes/harflex-test-not-mounted'") {
		t.Fatalf("the disk image is not ejected: %s", script)
	}
	path := filepath.Join(root, "swap.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/sh", path).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if got, err := os.ReadFile(filepath.Join(target, "version")); err != nil || string(got) != "new" {
		t.Fatalf("the new app was not installed: %q %v", got, err)
	}
	if got, _ := os.ReadFile(opened); strings.TrimSpace(string(got)) != target {
		t.Fatalf("opened %q, want %q", got, target)
	}
}

func TestInstallPlaceMovesAnAppRunFromADiskImageToApplications(t *testing.T) {
	writable := func(dirs ...string) func(string) bool {
		return func(dir string) bool { return slices.Contains(dirs, dir) }
	}
	never := func(string) bool { return false }
	readOnly := func(path string) bool { return strings.HasPrefix(path, "/Volumes/") }
	for name, test := range map[string]struct {
		bundle        string
		writable      func(string) bool
		readOnly      func(string) bool
		target, eject string
		ok            bool
	}{
		"installed app, replaced where it is": {"/Applications/harflex.app", writable("/Applications"), never, "/Applications/harflex.app", "", true},
		"run from a disk image":               {"/Volumes/harflex 1/harflex.app", writable("/Applications"), readOnly, "/Applications/harflex.app", "/Volumes/harflex 1", true},
		"disk image, no admin rights":         {"/Volumes/harflex/harflex.app", writable("/Users/ana/Applications"), readOnly, "/Users/ana/Applications/harflex.app", "/Volumes/harflex", true},
		"a folder nobody can write":           {"/opt/apps/harflex.app", writable("/Applications"), never, "/Applications/harflex.app", "", true},
		"nowhere to install":                  {"/opt/apps/harflex.app", never, never, "", "", false},
	} {
		target, eject, ok := installPlace(test.bundle, "/Users/ana", test.writable, test.readOnly)
		if target != test.target || eject != test.eject || ok != test.ok {
			t.Fatalf("%s: got %q %q %v", name, target, eject, ok)
		}
	}
}
