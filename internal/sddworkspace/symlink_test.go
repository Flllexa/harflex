package sddworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanPreservesInternalSymlinkText(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "main.go"), []byte("safe"), 0o600)
	if err := os.Symlink("src/main.go", filepath.Join(root, "entrypoint")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	manifest, err := Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	for _, entry := range manifest.Entries {
		if entry.Path == "entrypoint" {
			if entry.Type != EntrySymlink || entry.Target != "src/main.go" || entry.Size != int64(len("src/main.go")) {
				t.Fatalf("symlink entry = %#v", entry)
			}
			return
		}
	}
	t.Fatal("manifest omitted internal symlink")
}

func TestScanNoFollowRejectsSymlinkRoot(t *testing.T) {
	realRoot := t.TempDir()
	rootParent := t.TempDir()
	linkRoot := filepath.Join(rootParent, "source")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := ScanNoFollow(context.Background(), linkRoot); err == nil {
		t.Fatal("ScanNoFollow accepted a symlink as the source root")
	}
}

func TestScanRejectsDirectAndTransitiveSymlinkEscapes(t *testing.T) {
	for _, test := range []struct {
		name string
		make func(t *testing.T, root string)
	}{
		{
			name: "direct relative escape",
			make: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Symlink("../../outside", filepath.Join(root, "dir", "escape")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "transitive escape",
			make: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Symlink("../../outside", filepath.Join(root, "dir", "redirect")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("dir/redirect", filepath.Join(root, "indirect")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
				t.Fatal(err)
			}
			test.make(t, root)
			_, err := Scan(context.Background(), root)
			if !errors.Is(err, ErrSymlinkEscape) {
				t.Fatalf("Scan() error = %v, want ErrSymlinkEscape", err)
			}
		})
	}
}

func TestScanRejectsCaseFoldedSymlinkAliasAsNonPortable(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "dir", "file"), []byte("safe"), 0o600)
	if err := os.Symlink("file", filepath.Join(root, "dir", "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink("DIR/escape", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	_, err := Scan(context.Background(), root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Scan() error = %v, want ErrUnsafePath for case-folded alias", err)
	}
}

func TestScanRejectsUnicodeNormalizationAliasInSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	composedDirectory := "\u00e9"
	decomposedDirectory := "e\u0301"
	writeFile(t, filepath.Join(root, composedDirectory, "target"), []byte("safe"), 0o600)
	if err := os.Symlink(filepath.Join(decomposedDirectory, "target"), filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Scan(context.Background(), root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Scan() error = %v, want ErrUnsafePath for NFC/NFD alias", err)
	}
}

func TestScanRejectsAbsoluteSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("/tmp/outside", filepath.Join(root, "absolute")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Scan(context.Background(), root)
	if !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Scan() error = %v, want ErrSymlinkEscape", err)
	}
}

func TestScanRejectsSymlinkTargetingExcludedCredentialWithoutOpeningIt(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("/outside/credential", filepath.Join(root, ".env")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(".env", filepath.Join(root, "credential-alias")); err != nil {
		t.Fatal(err)
	}
	_, err := Scan(context.Background(), root)
	if !errors.Is(err, ErrSymlinkExcludedTarget) {
		t.Fatalf("Scan() error = %v, want ErrSymlinkExcludedTarget for link into excluded credential", err)
	}
}

func TestScanRejectsSymlinkCyclesWithoutHanging(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("b", filepath.Join(root, "a")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink("a", filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Scan(context.Background(), root)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSymlinkEscape) {
			t.Fatalf("Scan() error = %v, want conservative symlink-cycle rejection", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Scan() did not terminate on a symlink cycle")
	}
}

func TestScanReturnsCancellationAfterLastEntry(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "file"), []byte("x"), 0o600)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := scanWithLimitsAndHooks(ctx, root, defaultScanLimits, scanHooks{
		hashFile: func(_ context.Context, _ *os.Root, _ string, relative string, info os.FileInfo, _ string) (Entry, error) {
			cancel()
			return Entry{Path: relative, Type: EntryFile, Mode: fileGitMode(info), Size: info.Size(), SHA256: emptyContentSHA256}, nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan error = %v, want context.Canceled", err)
	}
}
