//go:build darwin && !ios && cgo

package sddworkspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestDarwinCreatePrivateCopyAllowsPrivateParentWithoutAllowACL(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	privateParent := filepath.Join(base, "private")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(privateParent, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "file.txt"), []byte("source"), 0o600)

	copy, err := CreatePrivateCopy(context.Background(), source, privateParent)
	if err != nil {
		t.Fatalf("CreatePrivateCopy() error = %v, want safe Darwin ACL parent to be supported", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(copy.Root) })

	info, err := os.Stat(copy.Root)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("private staging mode = %v, want owner-only 0700 directory", info.Mode())
	}
	stagingDirectory, err := os.Open(copy.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer stagingDirectory.Close()
	if err := verifyPrivateParent(stagingDirectory); err != nil {
		t.Fatalf("staging ACL/mode readback failed: %v", err)
	}
}

func TestDarwinCreatePrivateCopyRejectsBroadAllowACLBeforeStaging(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	privateParent := filepath.Join(base, "private")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(privateParent, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "file.txt"), []byte("source"), 0o600)
	addDarwinACL(t, privateParent, "everyone allow list,search,file_inherit,directory_inherit")

	copy, err := CreatePrivateCopy(context.Background(), source, privateParent)
	if copy.Root != "" {
		_ = os.RemoveAll(copy.Root)
	}
	if !errors.Is(err, ErrPrivatePermissionsUnavailable) || copy.Root != "" {
		t.Fatalf("CreatePrivateCopy() = %#v, %v; want broad ACL rejection", copy, err)
	}
	contents, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("unsafe ACL parent received staging content: %#v", contents)
	}
}

func TestDarwinCreatePrivateCopyRejectsWritableParentBeforeStaging(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	privateParent := filepath.Join(base, "private")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(privateParent, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "file.txt"), []byte("source"), 0o600)
	if err := os.Chmod(privateParent, 0o770); err != nil {
		t.Fatal(err)
	}

	copy, err := CreatePrivateCopy(context.Background(), source, privateParent)
	if copy.Root != "" {
		_ = os.RemoveAll(copy.Root)
	}
	if !errors.Is(err, ErrPrivatePermissionsUnavailable) || copy.Root != "" {
		t.Fatalf("CreatePrivateCopy() = %#v, %v; want writable-parent rejection", copy, err)
	}
	contents, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("writable private parent received staging content: %#v", contents)
	}
}

func TestDarwinCreatePrivateCopyRejectsBroadAllowACLOnAncestor(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	privateParent := filepath.Join(base, "private")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(privateParent, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "file.txt"), []byte("source"), 0o600)
	addDarwinACL(t, base, "everyone allow list,search,file_inherit,directory_inherit")

	copy, err := CreatePrivateCopy(context.Background(), source, privateParent)
	if copy.Root != "" {
		_ = os.RemoveAll(copy.Root)
	}
	if !errors.Is(err, ErrPrivatePermissionsUnavailable) || copy.Root != "" {
		t.Fatalf("CreatePrivateCopy() = %#v, %v; want ancestor ACL rejection", copy, err)
	}
	contents, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) != 0 {
		t.Fatalf("unsafe ancestor ACL created staging content: %#v", contents)
	}
}

func TestDarwinCreatePrivateCopyAllowsDenyOnlyACL(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	privateParent := filepath.Join(base, "private")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(privateParent, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "file.txt"), []byte("source"), 0o600)
	addDarwinACL(t, privateParent, "everyone deny delete")

	copy, err := CreatePrivateCopy(context.Background(), source, privateParent)
	if err != nil {
		t.Fatalf("CreatePrivateCopy() error = %v, want deny-only ACL to be accepted", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(copy.Root) })
}

func TestDarwinACLDescriptorErrorFailsClosed(t *testing.T) {
	if err := verifyDarwinExtendedACLFD(-1); !errors.Is(err, ErrPrivatePermissionsUnavailable) {
		t.Fatalf("verifyDarwinExtendedACLFD(-1) error = %v, want ErrPrivatePermissionsUnavailable", err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if err := verifyDarwinExtendedACL(reader); !errors.Is(err, ErrPrivatePermissionsUnavailable) {
		t.Fatalf("verifyDarwinExtendedACL(pipe) error = %v, want unsupported descriptor to fail closed", err)
	}
}

func TestDarwinACLIterationClassifiesEINVALOnlyAfterAnEntry(t *testing.T) {
	tests := []struct {
		name             string
		status           int
		errorNumber      int
		isNextEntry      bool
		hasClassifiedACE bool
		aclValidated     bool
		wantFinished     bool
		wantError        bool
	}{
		{name: "first entry EINVAL is unclassified", status: -1, errorNumber: int(syscall.EINVAL), wantError: true},
		{name: "next entry EINVAL follows a classified ACE and valid ACL", status: -1, errorNumber: int(syscall.EINVAL), isNextEntry: true, hasClassifiedACE: true, aclValidated: true, wantFinished: true},
		{name: "next entry EINVAL with unvalidated ACL fails closed", status: -1, errorNumber: int(syscall.EINVAL), isNextEntry: true, hasClassifiedACE: true, wantError: true},
		{name: "next entry EINVAL without a classified ACE is unclassified", status: -1, errorNumber: int(syscall.EINVAL), isNextEntry: true, wantError: true},
		{name: "other API errors fail closed", status: -1, errorNumber: int(syscall.EACCES), isNextEntry: true, hasClassifiedACE: true, wantError: true},
		{name: "successful entry continues iteration", status: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finished, err := darwinACLIterationResult(test.status, test.errorNumber, test.isNextEntry, test.hasClassifiedACE, test.aclValidated)
			if finished != test.wantFinished {
				t.Fatalf("iteration finished = %t, want %t", finished, test.wantFinished)
			}
			if test.wantError != errors.Is(err, ErrPrivatePermissionsUnavailable) {
				t.Fatalf("iteration error = %v, want unavailable=%t", err, test.wantError)
			}
		})
	}
}

func TestDarwinACLValidationResultFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		errorNumber int
		wantError   bool
	}{
		{name: "valid ACL", status: 0},
		{name: "invalid ACL", status: -1, errorNumber: int(syscall.EINVAL), wantError: true},
		{name: "ACL validation API error", status: -1, errorNumber: int(syscall.EIO), wantError: true},
		{name: "unclassifiable API result", status: 1, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := darwinACLValidationResult(test.status, test.errorNumber)
			if test.wantError != errors.Is(err, ErrPrivatePermissionsUnavailable) {
				t.Fatalf("validation error = %v, want unavailable=%t", err, test.wantError)
			}
		})
	}
}

func TestDarwinSecurePrivateRootRejectsAllowACLAfterChmod(t *testing.T) {
	stage := t.TempDir()
	addDarwinACL(t, stage, "everyone allow list,search")
	directory, err := os.Open(stage)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	if err := securePrivateRoot(directory); !errors.Is(err, ErrPrivatePermissionsUnavailable) {
		t.Fatalf("securePrivateRoot() error = %v, want staging ACL rejection", err)
	}
	info, err := directory.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("staging mode after securePrivateRoot = %04o, want 0700", info.Mode().Perm())
	}
}

func addDarwinACL(t *testing.T, directory, ace string) {
	t.Helper()
	command := exec.Command("/bin/chmod", "+a", ace, directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("add ACL fixture %q: %v: %s", ace, err, output)
	}
	t.Cleanup(func() {
		command := exec.Command("/bin/chmod", "-a", ace, directory)
		if output, err := command.CombinedOutput(); err != nil {
			t.Errorf("remove ACL fixture %q: %v: %s", ace, err, output)
		}
	})
}
