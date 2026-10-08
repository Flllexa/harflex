package sddworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestScanRejectsOversizeBeforeReadingFileContents(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "large.bin"), []byte("too large"), 0o600)
	readCalled := false
	_, err := scanWithLimitsAndHooks(context.Background(), root, scanLimits{maxFiles: 10, maxBytes: 1, maxEntries: 10}, scanHooks{
		hashFile: func(context.Context, *os.Root, string, string, os.FileInfo, string) (Entry, error) {
			readCalled = true
			return Entry{}, nil
		},
	})
	if !errors.Is(err, ErrManifestLimit) {
		t.Fatalf("scan error = %v, want ErrManifestLimit", err)
	}
	if readCalled {
		t.Fatal("oversize file content was opened before rejecting the manifest")
	}
}

func TestScanEnforcesInjectableFileCountBeforeOpeningNextFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), []byte("a"), 0o600)
	writeFile(t, filepath.Join(root, "b.txt"), []byte("b"), 0o600)
	opened := make([]string, 0, 1)
	_, err := scanWithLimitsAndHooks(context.Background(), root, scanLimits{maxFiles: 1, maxBytes: 10, maxEntries: 10}, scanHooks{
		hashFile: func(ctx context.Context, parent *os.Root, name, relative string, info os.FileInfo, rootFSID string) (Entry, error) {
			opened = append(opened, relative)
			return hashRegularFile(ctx, parent, name, relative, info, rootFSID)
		},
	})
	if !errors.Is(err, ErrManifestLimit) {
		t.Fatalf("scan error = %v, want ErrManifestLimit", err)
	}
	if len(opened) != 1 || (opened[0] != "a.txt" && opened[0] != "b.txt") {
		t.Fatalf("opened paths = %#v, want only one file before limit rejection", opened)
	}
}

func TestScanBoundsExcludedEntries(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".env"), []byte("do not read"), 0o600)
	writeFile(t, filepath.Join(root, "signing.pem"), []byte("do not read"), 0o600)
	_, err := scanWithLimitsAndHooks(context.Background(), root, scanLimits{maxFiles: 10, maxBytes: 10, maxEntries: 1}, scanHooks{})
	if !errors.Is(err, ErrManifestLimit) {
		t.Fatalf("scan error = %v, want ErrManifestLimit for excluded entries", err)
	}
}

func TestScanExcludesCredentialPatternsWithoutOpeningTheirContents(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".env"), []byte("secret"), 0)
	writeFile(t, filepath.Join(root, ".env.local"), []byte("secret"), 0)
	writeFile(t, filepath.Join(root, ".env.example"), []byte("SAFE=example"), 0o600)
	writeFile(t, filepath.Join(root, ".env.example.local"), []byte("secret"), 0)
	writeFile(t, filepath.Join(root, ".ssh", "id_rsa"), []byte("private key"), 0)
	writeFile(t, filepath.Join(root, "certs", "server.pem"), []byte("certificate"), 0)
	writeFile(t, filepath.Join(root, "certs", "signing.key"), []byte("private key"), 0)
	writeFile(t, filepath.Join(root, "certs", "bundle.p12"), []byte("private key"), 0)
	writeFile(t, filepath.Join(root, "certs", "bundle.pfx"), []byte("private key"), 0)
	writeFile(t, filepath.Join(root, "nested", "credentials.json"), []byte("credentials"), 0)
	writeFile(t, filepath.Join(root, "nested", "safe.txt"), []byte("safe"), 0o600)

	opened := make([]string, 0, 2)
	manifest, err := scanWithLimitsAndHooks(context.Background(), root, defaultScanLimits, scanHooks{
		hashFile: func(ctx context.Context, parent *os.Root, name, relative string, info os.FileInfo, rootFSID string) (Entry, error) {
			opened = append(opened, relative)
			return hashRegularFile(ctx, parent, name, relative, info, rootFSID)
		},
	})
	if err != nil {
		t.Fatalf("scanWithLimitsAndHooks() error = %v", err)
	}
	if len(manifest.Excluded) != 9 {
		t.Fatalf("excluded paths = %#v, want nine explicit exclusion records", manifest.Excluded)
	}
	sort.Strings(opened)
	if len(opened) != 2 || opened[0] != ".env.example" || opened[1] != "nested/safe.txt" {
		t.Fatalf("opened regular-file contents = %#v, want only the example and safe file", opened)
	}
	if manifest.FileCount != 2 {
		t.Fatalf("included file count = %d, want 2", manifest.FileCount)
	}
}

func TestScanRejectsPortableInvalidPath(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("backslash is a native path separator on Windows")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "not\\portable.txt"), []byte("x"), 0o600)
	_, err := Scan(context.Background(), root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Scan() error = %v, want ErrUnsafePath", err)
	}
}

func TestScanRejectsExcessiveDepth(t *testing.T) {
	root := t.TempDir()
	nested := root
	for range maxPathDepth + 1 {
		nested = filepath.Join(nested, "a")
	}
	writeFile(t, filepath.Join(nested, "file"), []byte("x"), 0o600)
	_, err := Scan(context.Background(), root)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("Scan() error = %v, want ErrUnsafePath", err)
	}
}

func TestScanExcludesCaseInsensitiveCredentialNames(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".ENV"), []byte("secret"), 0o600)
	writeFile(t, filepath.Join(root, ".ENV.local"), []byte("secret"), 0o600)
	writeFile(t, filepath.Join(root, ".GIT"), []byte("git metadata"), 0o600)
	writeFile(t, filepath.Join(root, ".SSH", "id_ed25519"), []byte("private key"), 0o600)
	writeFile(t, filepath.Join(root, "ID_RSA"), []byte("private key"), 0o600)
	writeFile(t, filepath.Join(root, "bundle.PEM"), []byte("private key"), 0o600)
	writeFile(t, filepath.Join(root, ".ENV.Example"), []byte("SAFE=example"), 0o600)
	writeFile(t, filepath.Join(root, "safe.txt"), []byte("safe"), 0o600)

	manifest, err := Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(manifest.Excluded) != 6 {
		t.Fatalf("case-insensitive excluded paths = %#v, want six", manifest.Excluded)
	}
	if manifest.FileCount != 2 {
		t.Fatalf("included file count = %d, want example and safe file", manifest.FileCount)
	}
	if got := entryPaths(manifest.Entries); len(got) != 2 || got[0] != ".ENV.Example" || got[1] != "safe.txt" {
		t.Fatalf("included paths = %#v", got)
	}
}

func TestScanExcludesGitMetadataFileWithoutOpeningIt(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git"), []byte("gitdir: elsewhere"), 0)
	writeFile(t, filepath.Join(root, "safe.txt"), []byte("safe"), 0o600)
	opened := make([]string, 0, 1)
	manifest, err := scanWithLimitsAndHooks(context.Background(), root, defaultScanLimits, scanHooks{
		hashFile: func(ctx context.Context, parent *os.Root, name, relative string, info os.FileInfo, rootFSID string) (Entry, error) {
			opened = append(opened, relative)
			return hashRegularFile(ctx, parent, name, relative, info, rootFSID)
		},
	})
	if err != nil {
		t.Fatalf("scanWithLimitsAndHooks() error = %v", err)
	}
	if len(manifest.Excluded) != 1 || manifest.Excluded[0].Path != ".git" {
		t.Fatalf("excluded paths = %#v", manifest.Excluded)
	}
	if len(opened) != 1 || opened[0] != "safe.txt" {
		t.Fatalf("opened paths = %#v, want only safe.txt", opened)
	}
}

func TestFilesystemBoundaryRejectsDifferentMountIdentity(t *testing.T) {
	if err := ensureSameFilesystem("mount:root", "mount:nested", "nested"); !errors.Is(err, ErrFilesystemBoundary) {
		t.Fatalf("ensureSameFilesystem() error = %v, want ErrFilesystemBoundary", err)
	}
	if err := ensureSameFilesystem("mount:root", "mount:root", "same"); err != nil {
		t.Fatalf("same filesystem identity rejected: %v", err)
	}
}

func TestScanRejectsSubdirectoryReplacementDuringTraversal(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "nested")
	writeFile(t, filepath.Join(directory, "file.txt"), []byte("original"), 0o600)
	replaced := false
	_, err := scanWithLimitsAndHooks(context.Background(), root, defaultScanLimits, scanHooks{
		hashFile: func(ctx context.Context, parent *os.Root, name, relative string, info os.FileInfo, rootFSID string) (Entry, error) {
			entry, err := hashRegularFile(ctx, parent, name, relative, info, rootFSID)
			if err != nil {
				return Entry{}, err
			}
			if !replaced {
				replaced = true
				moved := filepath.Join(root, "moved")
				if err := os.Rename(directory, moved); err != nil {
					return Entry{}, err
				}
				if err := os.Mkdir(directory, 0o700); err != nil {
					return Entry{}, err
				}
			}
			return entry, nil
		},
	})
	if !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("scan error = %v, want ErrSourceDrift after subdirectory replacement", err)
	}
}

func TestScanRejectsSameSizeContentDriftAfterWalk(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "file.txt")
	writeFile(t, filePath, []byte("alpha"), 0o600)
	before, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = scanWithLimitsAndHooks(context.Background(), root, defaultScanLimits, scanHooks{
		afterWalk: func(*os.Root) error {
			if err := os.WriteFile(filePath, []byte("bravo"), 0o600); err != nil {
				return err
			}
			return os.Chtimes(filePath, before.ModTime(), before.ModTime())
		},
	})
	if !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("scan error = %v, want ErrSourceDrift for same-size content change", err)
	}
}
