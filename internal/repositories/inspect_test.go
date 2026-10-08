package repositories

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type readerOnly struct{ reader *strings.Reader }

func (r readerOnly) Read(p []byte) (int, error) { return r.reader.Read(p) }

func TestBoundedBufferEnforcesLimitWhenCopyUsesReaderFrom(t *testing.T) {
	buffer := &boundedBuffer{limit: 4}
	if _, err := io.Copy(buffer, readerOnly{reader: strings.NewReader("abcdefgh")}); err != nil {
		t.Fatal(err)
	}
	if buffer.String() != "abcd" || buffer.Len() != 4 || !buffer.truncated {
		t.Fatalf("unbounded copy escaped limit: len=%d truncated=%v data=%q", buffer.Len(), buffer.truncated, buffer.String())
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func TestInspectShowsStagedUnstagedAndUntrackedChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	git(t, root, "init")
	for _, name := range []string{"staged.txt", "unstaged.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("old\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(root, "staged.txt"), []byte("staged change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "staged.txt")
	for name, body := range map[string]string{"unstaged.txt": "unstaged change\n", "new.txt": "untracked\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Inspect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsRepository || got.Branch == "" || len(got.Files) != 3 {
		t.Fatalf("snapshot: %+v", got)
	}
	if !strings.Contains(got.StagedDiff, "+staged change") || !strings.Contains(got.UnstagedDiff, "+unstaged change") {
		t.Fatal("missing staged/unstaged diff")
	}
	if !strings.Contains(strings.Join(got.Files, " "), "new.txt") {
		t.Fatal("untracked file not listed")
	}
}

func TestInspectDoesNotTraverseToParentRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	git(t, root, "init")
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := Inspect(context.Background(), child)
	if err != nil || got.IsRepository {
		t.Fatalf("parent repository exceeded selected scope: %+v %v", got, err)
	}
}
