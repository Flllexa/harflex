package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewPathGuardRejectsInvalidRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	writeGuardFile(t, file)
	for _, input := range []string{"", filepath.Join(root, "missing"), file} {
		t.Run(input, func(t *testing.T) {
			if _, err := NewPathGuard(input); err == nil {
				t.Fatalf("NewPathGuard(%q) succeeded", input)
			}
		})
	}
}

func TestPathGuardResolve(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "work")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeGuardFile(t, filepath.Join(root, "inside.txt"))
	writeGuardFile(t, filepath.Join(parent, "outside"))
	writeGuardFile(t, filepath.Join(parent, "work-evil"))
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := NewPathGuard(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		input        string
		allowMissing bool
		want         string
	}{
		{"existing file", "inside.txt", false, filepath.Join(canonicalRoot, "inside.txt")},
		{"root dot", ".", false, canonicalRoot},
		{"root empty", "", false, canonicalRoot},
		{"absolute internal", filepath.Join(root, "inside.txt"), false, filepath.Join(canonicalRoot, "inside.txt")},
		{"parent traversal", "../outside", false, ""},
		{"absolute external", filepath.Join(parent, "outside"), false, ""},
		{"similar prefix", filepath.Join(parent, "work-evil"), false, ""},
		{"new internal file", "new.txt", true, filepath.Join(canonicalRoot, "new.txt")},
		{"missing ancestors", "new-dir/nested/new.txt", true, filepath.Join(canonicalRoot, "new-dir", "nested", "new.txt")},
		{"missing disallowed", "missing.txt", false, ""},
		{"missing external", "../missing.txt", true, ""},
		{"file ancestor", "inside.txt/child", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := guard.Resolve(tc.input, tc.allowMissing)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("Resolve(%q) = %q, want error", tc.input, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Resolve(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
			}
		})
	}
}

func TestPathGuardSymlinks(t *testing.T) {
	for _, tc := range []struct {
		name         string
		target       string
		input        string
		allowMissing bool
		wantSuffix   string
	}{
		{"external file", "outside/file", "link", false, ""},
		{"external directory", "outside", "link", false, ""},
		{"external ancestor", "outside", "link/new.txt", true, ""},
		{"external missing ancestors", "outside", "link/new-dir/new.txt", true, ""},
		{"dangling external target", "outside/missing", "link/new.txt", true, ""},
		{"internal file", "work/inside.txt", "link", false, "inside.txt"},
		{"internal directory", "work/nested", "link/new.txt", true, "nested/new.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "work")
			for _, path := range []string{filepath.Join(root, "nested"), filepath.Join(parent, "outside")} {
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			writeGuardFile(t, filepath.Join(root, "inside.txt"))
			writeGuardFile(t, filepath.Join(parent, "outside", "file"))
			guardSymlink(t, filepath.Join(parent, filepath.FromSlash(tc.target)), filepath.Join(root, "link"))
			guard, err := NewPathGuard(root)
			if err != nil {
				t.Fatal(err)
			}
			got, err := guard.Resolve(filepath.FromSlash(tc.input), tc.allowMissing)
			if tc.wantSuffix == "" {
				if err == nil {
					t.Fatalf("Resolve(%q) = %q, want error", tc.input, got)
				}
				return
			}
			canonicalRoot, canonicalErr := filepath.EvalSymlinks(root)
			if canonicalErr != nil {
				t.Fatal(canonicalErr)
			}
			want := filepath.Join(canonicalRoot, filepath.FromSlash(tc.wantSuffix))
			if err != nil || got != want {
				t.Fatalf("Resolve(%q) = %q, %v; want %q", tc.input, got, err, want)
			}
		})
	}
}

func TestPathGuardCanonicalizesSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "real")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeGuardFile(t, filepath.Join(root, "inside.txt"))
	writeGuardFile(t, filepath.Join(parent, "outside.txt"))
	link := filepath.Join(parent, "alias")
	guardSymlink(t, root, link)
	guard, err := NewPathGuard(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(root, "inside.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := guard.Resolve("inside.txt", false); err != nil || got != want {
		t.Fatalf("Resolve = %q, %v; want %q", got, err, want)
	}
	if _, err := guard.Resolve("../outside.txt", false); err == nil {
		t.Fatal("Resolve allowed escape from canonical root")
	}
}

func writeGuardFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func guardSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if os.IsPermission(err) {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		t.Fatal(err)
	}
}
