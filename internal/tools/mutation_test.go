package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/security"
)

func mutationArgs(path, content string) json.RawMessage {
	data, _ := json.Marshal(map[string]string{"path": path, "content": content})
	return data
}

func assertMutationFile(t *testing.T, root, path, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, path))
	if err != nil || string(got) != want {
		t.Fatalf("file %s = %q, %v; want %q", path, got, err, want)
	}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(d.Name(), ".harflex-") {
			t.Errorf("temporary file remains: %s", path)
		}
		return nil
	})
}

func TestWriteEditSpecs(t *testing.T) {
	g, _ := readOnlyWorkspace(t)
	for _, tc := range []struct {
		tool     Tool
		name     string
		required []string
	}{{NewWriteTool(g), "write", []string{"path", "content"}}, {NewEditTool(g), "edit", []string{"path", "oldText", "newText"}}} {
		spec := tc.tool.Spec()
		var schema struct {
			Type                 string
			Required             []string
			AdditionalProperties bool
		}
		if err := json.Unmarshal(spec.Schema, &schema); err != nil {
			t.Fatal(err)
		}
		if spec.Name != tc.name || spec.Description == "" || tc.tool.Risk() != security.Write || schema.Type != "object" || schema.AdditionalProperties || !reflect.DeepEqual(schema.Required, tc.required) {
			t.Fatalf("invalid spec %+v", spec)
		}
	}
}

func TestWriteCreatesParentsAndReplacesPreservingMode(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	tool := NewWriteTool(g)
	content, details := executeReadOnly(t, tool, `{"path":"a/b/file","content":"old\n"}`)
	if content["text"] == "" || details["created"] != true || details["bytes"] != float64(4) || details["path"] != "a/b/file" {
		t.Fatalf("result=%v %v", content, details)
	}
	info, _ := os.Stat(filepath.Join(root, "a/b/file"))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", info.Mode())
	}
	if err := os.Chmod(filepath.Join(root, "a/b/file"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, details = executeReadOnly(t, tool, `{"path":"a/b/file","content":"new\n"}`)
	assertMutationFile(t, root, "a/b/file", "new\n")
	info, _ = os.Stat(filepath.Join(root, "a/b/file"))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%v", info.Mode())
	}
	if details["created"] != false || !strings.Contains(details["diff"].(string), "-old\n+new\n") {
		t.Fatalf("details=%v", details)
	}
}

func TestWriteEditRejectInvalidParametersAndTargets(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "file", "old")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, tc := range []struct {
		tool Tool
		args []string
	}{
		{NewWriteTool(g), []string{`{}`, `null`, `{"path":"","content":"x"}`, `{"path":"file"}`, `{"path":"file","content":null}`, `{"path":"file","content":"x","unknown":1}`, `{"path":"file","content":"x"}{}`, `{"path":".","content":"x"}`, `{"path":"file/child","content":"x"}`, `{"path":"a/../file","content":"x"}`, fmt.Sprintf(`{"path":%q,"content":"x"}`, outside)}},
		{NewEditTool(g), []string{`{}`, `null`, `{"path":"file","oldText":"old"}`, `{"path":"file","oldText":"","newText":"x"}`, `{"path":"file","oldText":"absent","newText":"x"}`, `{"path":"file","oldText":"old","newText":null}`, `{"path":"file","oldText":"old","newText":"x","unknown":1}`, `{"path":"file","oldText":"old","newText":"x"} true`, `{"path":"missing/sub","oldText":"old","newText":"x"}`, `{"path":".","oldText":"old","newText":"x"}`}},
	} {
		for _, args := range tc.args {
			t.Run(tc.tool.Spec().Name+args, func(t *testing.T) {
				if _, err := tc.tool.Execute(context.Background(), json.RawMessage(args), nil); err == nil {
					t.Fatal("expected rejection")
				}
			})
		}
	}
	assertMutationFile(t, root, "file", "old")
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatalf("edit created parents: %v", err)
	}
}

func TestWriteEditSymlinkSemantics(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "target", "old")
	outside := t.TempDir()
	putReadOnlyFile(t, outside, "secret", "old")
	for name, target := range map[string]string{"link": filepath.Join(root, "target"), "external": filepath.Join(outside, "secret")} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}
	for _, tool := range []Tool{NewWriteTool(g), NewEditTool(g)} {
		args := mutationArgs("external", "new")
		if tool.Spec().Name == "edit" {
			args = json.RawMessage(`{"path":"external","oldText":"old","newText":"new"}`)
		}
		if _, err := tool.Execute(context.Background(), args, nil); err == nil {
			t.Fatal("external symlink accepted")
		}
	}
	executeReadOnly(t, NewWriteTool(g), `{"path":"link","content":"written"}`)
	executeReadOnly(t, NewEditTool(g), `{"path":"link","oldText":"written","newText":"edited"}`)
	assertMutationFile(t, root, "target", "edited")
	info, _ := os.Lstat(filepath.Join(root, "link"))
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced symlink")
	}
	assertMutationFile(t, outside, "secret", "old")
}

func TestEditUniqueTextModeAndDiff(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "file", "before\nold\nafter\n")
	if err := os.Chmod(filepath.Join(root, "file"), 0o640); err != nil {
		t.Fatal(err)
	}
	_, d := executeReadOnly(t, NewEditTool(g), `{"path":"file","oldText":"old","newText":"new"}`)
	assertMutationFile(t, root, "file", "before\nnew\nafter\n")
	info, _ := os.Stat(filepath.Join(root, "file"))
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatal("mode changed")
	}
	if d["created"] != false || d["bytes"] != float64(17) || !strings.Contains(d["diff"].(string), "-old\n+new\n") {
		t.Fatalf("details=%v", d)
	}
}

func TestEditRejectMultipleBinaryAndOversize(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	for name, data := range map[string]string{"multiple": "old old", "binary": "old\x00", "invalid": "old\xff", "large": "old" + strings.Repeat("x", maxReadBytes)} {
		t.Run(name, func(t *testing.T) {
			putReadOnlyFile(t, root, "file", data)
			if _, err := NewEditTool(g).Execute(context.Background(), json.RawMessage(`{"path":"file","oldText":"old","newText":"new"}`), nil); err == nil {
				t.Fatal("invalid source accepted")
			}
			assertMutationFile(t, root, "file", data)
		})
	}
}

// Inspecting temporary bytes makes cancellation deterministic without timing sleeps.
type mutationContext struct {
	context.Context
	root    string
	cancel  context.CancelFunc
	trigger func(string, int64) bool
}

func (c *mutationContext) Err() error {
	entries, _ := os.ReadDir(c.root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".harflex-") {
			info, _ := e.Info()
			if info != nil && c.trigger(e.Name(), info.Size()) {
				c.cancel()
			}
		}
	}
	return c.Context.Err()
}

func TestWriteEditCancellationAndCleanup(t *testing.T) {
	for _, name := range []string{"write", "edit"} {
		for _, when := range []string{"before", "during", "commit"} {
			t.Run(name+"/"+when, func(t *testing.T) {
				g, root := readOnlyWorkspace(t)
				putReadOnlyFile(t, root, "file", "old")
				data := strings.Repeat("x", 3*64*1024)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if when == "before" {
					cancel()
				}
				controlled := &mutationContext{Context: ctx, root: root, cancel: cancel, trigger: func(_ string, n int64) bool {
					if when == "during" {
						return n >= 64*1024 && n < int64(len(data))
					}
					return when == "commit" && n == int64(len(data))
				}}
				tool := NewWriteTool(g)
				args := mutationArgs("file", data)
				if name == "edit" {
					tool = NewEditTool(g)
					raw, _ := json.Marshal(map[string]string{"path": "file", "oldText": "old", "newText": data})
					args = raw
				}
				if _, err := tool.Execute(controlled, args, nil); !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
				assertMutationFile(t, root, "file", "old")
			})
		}
	}
}

func TestWriteAtomicRenameFailureAndSameDirectory(t *testing.T) {
	_, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "file", "old")
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	info, _ := r.Stat("file")
	fault := errors.New("rename failed")
	err = atomicReplace(context.Background(), r, "file", "new", 0o600, info, func(from, to string) error {
		if filepath.Dir(from) != "." || to != "file" || !strings.HasPrefix(from, ".harflex-") {
			t.Fatalf("rename paths %q %q", from, to)
		}
		return fault
	})
	if !errors.Is(err, fault) {
		t.Fatalf("error=%v", err)
	}
	assertMutationFile(t, root, "file", "old")
}

func TestDiffUnifiedContextAndNewlines(t *testing.T) {
	for _, tc := range []struct{ name, before, after, want string }{
		{"equal", "same", "same", ""},
		{"create", "", "new\n", "--- file\n+++ file\n@@ -0,0 +1 @@\n+new\n"},
		{"delete", "old\n", "", "--- file\n+++ file\n@@ -1 +0,0 @@\n-old\n"},
		{"modify", "old\n", "new\n", "--- file\n+++ file\n@@ -1 +1 @@\n-old\n+new\n"},
		{"newline", "old", "old\n", "--- file\n+++ file\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+old\n"},
		{"context", "0\n1\n2\n3\nold\n5\n6\n7\n8\n", "0\n1\n2\n3\nnew\n5\n6\n7\n8\n", "--- file\n+++ file\n@@ -2,7 +2,7 @@\n 1\n 2\n 3\n-old\n+new\n 5\n 6\n 7\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := unifiedDiff("file", tc.before, tc.after)
			if err != nil || got != tc.want {
				t.Fatalf("diff=%q (%v), want %q", got, err, tc.want)
			}
		})
	}
}

func TestWriteEditRejectSymlinkParents(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "real/file", "old")
	if err := os.Symlink("real", filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, tc := range []struct {
		tool Tool
		args string
	}{{NewWriteTool(g), `{"path":"linked/new/file","content":"new"}`}, {NewEditTool(g), `{"path":"linked/file","oldText":"old","newText":"new"}`}} {
		if _, err := tc.tool.Execute(context.Background(), json.RawMessage(tc.args), nil); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlink parent error=%v", err)
		}
	}
	assertMutationFile(t, root, "real/file", "old")
	if _, err := os.Stat(filepath.Join(root, "real/new")); !os.IsNotExist(err) {
		t.Fatalf("created through link: %v", err)
	}
}

func TestWriteEditRejectFIFOWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO is a Unix filesystem type")
	}
	mkfifo, err := exec.LookPath("mkfifo")
	if err != nil {
		t.Skip("mkfifo unavailable")
	}
	g, root := readOnlyWorkspace(t)
	fifo := filepath.Join(root, "pipe")
	if out, err := exec.Command(mkfifo, fifo).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo: %s %v", out, err)
	}
	for _, tc := range []struct {
		tool Tool
		args string
	}{{NewWriteTool(g), `{"path":"pipe","content":"new"}`}, {NewEditTool(g), `{"path":"pipe","oldText":"old","newText":"new"}`}} {
		done := make(chan error, 1)
		go func() { _, err := tc.tool.Execute(context.Background(), json.RawMessage(tc.args), nil); done <- err }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("special file error=%v", err)
			}
		case <-time.After(time.Second):
			rescue, e := os.OpenFile(fifo, os.O_RDWR, 0)
			if e == nil {
				rescue.Close()
			}
			t.Fatal("FIFO operation blocked")
		}
	}
}

func TestWriteLargeContentAndEditSizeBoundary(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	large := strings.Repeat("x", maxReadBytes+1)
	if _, err := NewWriteTool(g).Execute(context.Background(), mutationArgs("large", large), nil); err != nil {
		t.Fatal(err)
	}
	assertMutationFile(t, root, "large", large)
	boundary := "old" + strings.Repeat("x", maxReadBytes-3)
	putReadOnlyFile(t, root, "boundary", boundary)
	executeReadOnly(t, NewEditTool(g), `{"path":"boundary","oldText":"old","newText":""}`)
	assertMutationFile(t, root, "boundary", boundary[3:])
}

func TestWriteRevalidationPreservesConcurrentReplacement(t *testing.T) {
	_, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "file", "old")
	parent, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	old, _ := parent.Stat("file")
	putReadOnlyFile(t, root, "other", "concurrent")
	if err := os.Rename(filepath.Join(root, "other"), filepath.Join(root, "file")); err != nil {
		t.Fatal(err)
	}
	err = atomicReplace(context.Background(), parent, "file", "ours", 0o600, old, parent.Rename)
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("revalidation error=%v", err)
	}
	assertMutationFile(t, root, "file", "concurrent")
}

func TestWriteEmptyAndAbsolutePath(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	if _, err := NewWriteTool(g).Execute(context.Background(), mutationArgs(filepath.Join(root, "empty"), ""), nil); err != nil {
		t.Fatal(err)
	}
	assertMutationFile(t, root, "empty", "")
}

func TestWriteEditConcurrentSingleMatch(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "file", "old")
	// Register both tool instances as waiters before letting either read state.
	unlock, err := mutationLocks.acquire(context.Background(), g.root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	results := make(chan error, 2)
	contexts := []*mutationLockContext{}
	for range 2 {
		ctx := &mutationLockContext{Context: context.Background(), queued: make(chan struct{})}
		contexts = append(contexts, ctx)
		go func() {
			_, err := NewEditTool(g).Execute(ctx, json.RawMessage(`{"path":"file","oldText":"old","newText":"new"}`), nil)
			results <- err
		}()
	}
	for _, ctx := range contexts {
		<-ctx.queued
	}
	mutationLocks.mu.Lock()
	refs := mutationLocks.entries[g.root].refs
	mutationLocks.mu.Unlock()
	if refs != 3 {
		t.Fatalf("registered references=%d", refs)
	}
	unlock()
	success := 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if !strings.Contains(err.Error(), "found 0") {
			t.Fatalf("second edit error=%v", err)
		}
	}
	if success != 1 {
		t.Fatalf("successful edits=%d, want 1", success)
	}
	assertMutationFile(t, root, "file", "new")
}

func TestDiffEscapesHeaderControlCharacters(t *testing.T) {
	diff, err := unifiedDiff("file\nforged\r\tname", "old\n", "new\n")
	if err != nil {
		t.Fatal(err)
	}
	want := "--- file\\nforged\\r\\tname\n+++ file\\nforged\\r\\tname\n"
	if !strings.HasPrefix(diff, want) {
		t.Fatalf("unsafe headers: %q", diff)
	}
}

type mutationLockContext struct {
	context.Context
	queued chan struct{}
	once   sync.Once
}

func (c *mutationLockContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.queued) })
	return c.Context.Done()
}

func TestWriteEditKeyedLockSerializationAndCleanup(t *testing.T) {
	var manager mutationLockManager
	unlock, err := manager.acquire(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	entry := manager.entries["same"]
	manager.mu.Unlock()
	if entry == nil || entry.refs != 1 {
		unlock()
		t.Fatal("acquisition was not registered")
	}
	queued := &mutationLockContext{Context: context.Background(), queued: make(chan struct{})}
	acquired := make(chan func(), 1)
	go func() {
		release, err := manager.acquire(queued, "same")
		if err != nil {
			panic(err)
		}
		acquired <- release
	}()
	<-queued.queued
	select {
	case release := <-acquired:
		release()
		unlock()
		t.Fatal("same key did not serialize")
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	other, err := manager.acquire(ctx, "different")
	if err != nil {
		t.Fatal("different key blocked", err)
	}
	other()
	unlock()
	release := <-acquired
	release()
	manager.mu.Lock()
	remaining := len(manager.entries)
	manager.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("leaked entries=%d", remaining)
	}
}

func TestWriteEditKeyedLockCanceledWaiterCleanup(t *testing.T) {
	var manager mutationLockManager
	unlock, err := manager.acquire(context.Background(), "file")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := manager.acquire(ctx, "file"); !errors.Is(err, context.Canceled) {
		if release != nil {
			release()
		}
		t.Fatalf("canceled lock error=%v", err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.entries) != 1 || manager.entries["file"].refs != 1 {
		t.Fatal("canceled waiter leaked reference")
	}
}

func TestWriteEditParentSwapRejectsIdentityChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires symlink creation privileges")
	}
	_, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "parent/file", "old")
	putReadOnlyFile(t, root, "other/file", "untouched")
	parent, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	expected, err := parent.Lstat("parent")
	if err != nil {
		t.Fatal(err)
	}
	child, err := openMutationDirectory("parent", expected, func(name string) (*os.Root, error) {
		if err := parent.Rename(name, "original"); err != nil {
			return nil, err
		}
		if err := parent.Symlink("other", name); err != nil {
			return nil, err
		}
		return parent.OpenRoot(name)
	})
	if child != nil {
		child.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("parent swap error=%v", err)
	}
	assertMutationFile(t, root, "other/file", "untouched")
}

func TestWriteEditKeyedLockWaitingCancellation(t *testing.T) {
	var manager mutationLockManager
	unlock, err := manager.acquire(context.Background(), "file")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &mutationLockContext{Context: ctx, queued: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		release, err := manager.acquire(observed, "file")
		if release != nil {
			release()
		}
		done <- err
	}()
	<-observed.queued
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting lock error=%v", err)
	}
	manager.mu.Lock()
	refs := manager.entries["file"].refs
	manager.mu.Unlock()
	if refs != 1 {
		t.Fatalf("waiter references=%d", refs)
	}
	unlock()
	manager.mu.Lock()
	remaining := len(manager.entries)
	manager.mu.Unlock()
	if remaining != 0 {
		t.Fatal("entries remain after unlock")
	}
}

func TestWriteEditShareWorkspaceLockAndRevalidateAfterWaiting(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "file", "old")
	putReadOnlyFile(t, root, "other", "untouched")
	if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, tool := range []Tool{NewWriteTool(g), NewEditTool(g)} {
		t.Run(tool.Spec().Name, func(t *testing.T) {
			unlock, err := mutationLocks.acquire(context.Background(), g.root)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			observed := &mutationLockContext{Context: context.Background(), queued: make(chan struct{})}
			done := make(chan error, 1)
			args := json.RawMessage(`{"path":"link","content":"new"}`)
			if tool.Spec().Name == "edit" {
				args = json.RawMessage(`{"path":"link","oldText":"old","newText":"new"}`)
			}
			go func() { _, err := tool.Execute(observed, args, nil); done <- err }()
			<-observed.queued
			if err := os.Remove(filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("other", filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			unlock()
			if err := <-done; err == nil || !strings.Contains(err.Error(), "target changed") {
				t.Fatalf("changed target error=%v", err)
			}
			assertMutationFile(t, root, "file", "old")
			assertMutationFile(t, root, "other", "untouched")
			if err := os.Remove(filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWriteEditWorkspaceLockSerializesDifferentPaths(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "first", "old")
	putReadOnlyFile(t, root, "second", "old")
	other, otherRoot := readOnlyWorkspace(t)
	putReadOnlyFile(t, otherRoot, "file", "old")
	release, err := lockMutation(context.Background(), g, "first", false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	mutationLocks.mu.Lock()
	entry := mutationLocks.entries[g.root]
	mutationLocks.mu.Unlock()
	if entry == nil {
		t.Fatal("mutation was not locked by workspace")
	}
	observed := &mutationLockContext{Context: context.Background(), queued: make(chan struct{})}
	acquired := make(chan func(), 1)
	go func() {
		unlock, err := lockMutation(observed, g, "second", false)
		if err != nil {
			panic(err)
		}
		acquired <- unlock
	}()
	<-observed.queued
	select {
	case unlock := <-acquired:
		unlock()
		t.Fatal("different paths in one workspace did not serialize")
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	otherRelease, err := lockMutation(ctx, other, "file", false)
	if err != nil {
		t.Fatalf("different workspace blocked: %v", err)
	}
	otherRelease()
	release()
	secondRelease := <-acquired
	secondRelease()
	mutationLocks.mu.Lock()
	remaining := len(mutationLocks.entries)
	mutationLocks.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("workspace lock entries remain: %d", remaining)
	}
}

func TestWriteEditCaseAliasesSerializeByWorkspace(t *testing.T) {
	g, root := readOnlyWorkspace(t)
	putReadOnlyFile(t, root, "lowercase", "old")
	lower, err := os.Stat(filepath.Join(root, "lowercase"))
	if err != nil {
		t.Fatal(err)
	}
	upper, err := os.Stat(filepath.Join(root, "LOWERCASE"))
	if os.IsNotExist(err) {
		t.Skip("filesystem is case-sensitive: uppercase alias does not exist")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(lower, upper) {
		t.Skip("filesystem resolves uppercase and lowercase to distinct files")
	}
	release, err := mutationLocks.acquire(context.Background(), g.root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	results := make(chan error, 2)
	contexts := []*mutationLockContext{}
	for _, path := range []string{"lowercase", "LOWERCASE"} {
		observed := &mutationLockContext{Context: context.Background(), queued: make(chan struct{})}
		contexts = append(contexts, observed)
		args, _ := json.Marshal(map[string]string{"path": path, "oldText": "old", "newText": "new"})
		go func() { _, err := NewEditTool(g).Execute(observed, args, nil); results <- err }()
	}
	for _, ctx := range contexts {
		<-ctx.queued
	}
	mutationLocks.mu.Lock()
	refs := mutationLocks.entries[g.root].refs
	mutationLocks.mu.Unlock()
	if refs != 3 {
		t.Errorf("workspace references=%d, want 3", refs)
	}
	release()
	success := 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if !strings.Contains(err.Error(), "found 0") {
			t.Errorf("second alias edit error=%v", err)
		}
	}
	if success != 1 {
		t.Fatalf("successful edits via aliases=%d, want 1", success)
	}
	assertMutationFile(t, root, "lowercase", "new")
}
