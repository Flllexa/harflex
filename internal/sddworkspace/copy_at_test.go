package sddworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCreatePrivateCopyAtUsesStableRootAndMatchesExpectedManifest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows ACL gate is covered separately")
	}
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "bin", "app"), []byte("source"), 0o755)
	if err := os.Symlink("bin/app", filepath.Join(source, "entrypoint")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	expected, err := Scan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(privateParent, ".harflex-sdd-code-attempt_001")
	planned, err := PrivateCopyDestination(privateParent, filepath.Base(destination))
	if err != nil {
		t.Fatal(err)
	}

	copy, err := CreatePrivateCopyAt(context.Background(), source, privateParent, destination, expected)
	if err != nil {
		t.Fatalf("CreatePrivateCopyAt() error = %v", err)
	}
	if copy.Root != planned || copy.Baseline.Hash != expected.Hash {
		t.Fatalf("copy root/baseline = %q/%q, want %q/%q", copy.Root, copy.Baseline.Hash, planned, expected.Hash)
	}
	info, err := os.Stat(copy.Root)
	if err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private root info = %v, error = %v", info, err)
	}
	sourceInfo, err := os.Stat(filepath.Join(source, "bin", "app"))
	if err != nil {
		t.Fatal(err)
	}
	copyInfo, err := os.Stat(filepath.Join(copy.Root, "bin", "app"))
	if err != nil || os.SameFile(sourceInfo, copyInfo) || copyInfo.Mode().Perm()&0o077 != 0 || copyInfo.Mode().Perm()&0o100 == 0 {
		t.Fatalf("copied file must be independent, private, and executable: info=%v error=%v", copyInfo, err)
	}
	if target, err := os.Readlink(filepath.Join(copy.Root, "entrypoint")); err != nil || target != "bin/app" {
		t.Fatalf("copied symlink target = %q, error = %v", target, err)
	}
	original, err := os.ReadFile(filepath.Join(source, "bin", "app"))
	if err != nil || string(original) != "source" {
		t.Fatalf("source changed during copy: %q, %v", original, err)
	}
}

func TestCreatePrivateCopyAtRejectsManifestDriftBeforeCreatingRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows ACL gate is covered separately")
	}
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "app"), []byte("before"), 0o755)
	expected, err := Scan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "app"), []byte("after"), 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(privateParent, ".harflex-sdd-code-attempt_002")

	_, err = CreatePrivateCopyAt(context.Background(), source, privateParent, destination, expected)
	if !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("CreatePrivateCopyAt() error = %v, want ErrSourceDrift", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("manifest mismatch created target before failing: %v", err)
	}
	entries, err := os.ReadDir(privateParent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("manifest mismatch left private files: entries=%v error=%v", entries, err)
	}
}

func TestCreatePrivateCopyAtRejectsExistingAndOutOfParentDestinations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows ACL gate is covered separately")
	}
	source := t.TempDir()
	privateParent := t.TempDir()
	outsideParent := t.TempDir()
	writeFile(t, filepath.Join(source, "app"), []byte("source"), 0o600)
	expected, err := Scan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(privateParent, "existing")
	writeFile(t, filepath.Join(existing, "keep.txt"), []byte("keep"), 0o600)
	if _, err := CreatePrivateCopyAt(context.Background(), source, privateParent, existing, expected); err == nil {
		t.Fatal("CreatePrivateCopyAt() overwrote an existing destination")
	}
	kept, err := os.ReadFile(filepath.Join(existing, "keep.txt"))
	if err != nil || string(kept) != "keep" {
		t.Fatalf("existing destination changed: %q, %v", kept, err)
	}
	outside := filepath.Join(outsideParent, "outside")
	if _, err := CreatePrivateCopyAt(context.Background(), source, privateParent, outside, expected); err == nil {
		t.Fatal("CreatePrivateCopyAt() accepted a destination outside privateParent")
	}
	if _, err := os.Lstat(outside); !os.IsNotExist(err) {
		t.Fatalf("out-of-parent destination was created: %v", err)
	}
}

func TestCreatePrivateCopyAtCleansStableRootAfterCopyDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows ACL gate is covered separately")
	}
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "a.txt"), []byte("A"), 0o600)
	writeFile(t, filepath.Join(source, "z.txt"), []byte("original"), 0o600)
	expected, err := Scan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := PrivateCopyDestination(privateParent, ".harflex-sdd-code-attempt_drift")
	if err != nil {
		t.Fatal(err)
	}
	drifted := false
	hooks := createCopyHooks{afterFile: func(relativePath string) {
		if relativePath == "a.txt" && !drifted {
			drifted = true
			if err := os.WriteFile(filepath.Join(source, "z.txt"), []byte("changed during copy"), 0o600); err != nil {
				t.Fatalf("simulate source drift: %v", err)
			}
		}
	}}
	_, err = createPrivateCopyWithDestination(context.Background(), source, privateParent, destination, &expected, defaultScanLimits, hooks)
	if !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("create stable copy after source drift error = %v; want ErrSourceDrift", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("partial stable root was not removed: %v", err)
	}
	entries, err := os.ReadDir(privateParent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed stable copy left private files: entries=%v error=%v", entries, err)
	}
}

func TestVerifyPrivateCopyAtReconcilesCompleteDestinationWithoutMaterializing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows ACL gate is covered separately")
	}
	source := t.TempDir()
	privateParent := t.TempDir()
	writeFile(t, filepath.Join(source, "app.txt"), []byte("original"), 0o755)
	expected, err := Scan(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := PrivateCopyDestination(privateParent, ".harflex-sdd-code-reconcile")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePrivateCopyAt(context.Background(), source, privateParent, destination, expected); err != nil {
		t.Fatalf("CreatePrivateCopyAt() error = %v", err)
	}
	if err := VerifyPrivateCopyAt(context.Background(), source, privateParent, destination, expected); err != nil {
		t.Fatalf("VerifyPrivateCopyAt() complete copy error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "app.txt"), []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPrivateCopyAt(context.Background(), source, privateParent, destination, expected); !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("VerifyPrivateCopyAt() changed destination error = %v; want ErrSourceDrift", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "app.txt"), []byte("original"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "app.txt"), []byte("source drift"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPrivateCopyAt(context.Background(), source, privateParent, destination, expected); !errors.Is(err, ErrSourceDrift) {
		t.Fatalf("VerifyPrivateCopyAt() changed source error = %v; want ErrSourceDrift", err)
	}
}
