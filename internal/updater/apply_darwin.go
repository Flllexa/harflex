//go:build darwin

package updater

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// ErrNotWritable says the folder holding the app does not let this user replace it (a root-owned /Applications).
var ErrNotWritable = errors.New("the folder that holds the app is not writable by this user")

// StagingDir is where the installer is downloaded.
func StagingDir() (string, error) {
	return os.MkdirTemp("", "harflex-update-")
}

// bundleOf returns the .app that contains the executable, and false for a binary that does not run from one.
func bundleOf(executable string) (string, bool) {
	dir := filepath.Dir(executable) // Foo.app/Contents/MacOS
	if filepath.Base(dir) != "MacOS" || filepath.Base(filepath.Dir(dir)) != "Contents" {
		return "", false
	}
	bundle := filepath.Dir(filepath.Dir(dir))
	return bundle, strings.HasSuffix(bundle, ".app")
}

var teamPattern = regexp.MustCompile(`(?m)^TeamIdentifier=(\S+)$`)

// teamOf reads the Developer ID team that signed a bundle; "" when it is unsigned or ad hoc.
func teamOf(ctx context.Context, bundle string) string {
	out, _ := exec.CommandContext(ctx, "codesign", "-dv", "--verbose=4", bundle).CombinedOutput()
	if match := teamPattern.FindSubmatch(out); match != nil && string(match[1]) != "not" {
		return string(match[1])
	}
	return ""
}

// verifyBundle accepts the new app only when its signature is intact, Gatekeeper accepts it, and the team that signed it is
// the one that signed the running app. A running app without a team (a local build) accepts any valid signature.
var verifyBundle = func(ctx context.Context, current, next string) error {
	if out, err := exec.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", next).CombinedOutput(); err != nil {
		return fmt.Errorf("the new app's signature is not valid: %s", strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "spctl", "--assess", "--type", "execute", next).CombinedOutput(); err != nil {
		return fmt.Errorf("macOS does not accept the new app: %s", strings.TrimSpace(string(out)))
	}
	if team := teamOf(ctx, current); team != "" && teamOf(ctx, next) != team {
		return errors.New("the new app was signed by another developer")
	}
	return nil
}

// opener is what starts the app again once it is replaced.
var opener = "/usr/bin/open"

// installPlace says where the new app goes and which disk image to eject afterwards. An app run from its folder is
// replaced there. One run from a mounted .dmg (read-only) or from a folder this user cannot write goes to /Applications,
// or to ~/Applications, and the disk image it ran from is ejected, so the person ends up with one installed copy.
func installPlace(bundle, home string, writable, readOnly func(string) bool) (target, eject string, ok bool) {
	if strings.HasPrefix(bundle, "/Volumes/") && readOnly(bundle) {
		parts := strings.SplitN(strings.TrimPrefix(bundle, "/Volumes/"), "/", 2)
		eject = "/Volumes/" + parts[0]
	} else if writable(filepath.Dir(bundle)) {
		return bundle, "", true
	}
	for _, dir := range []string{"/Applications", filepath.Join(home, "Applications")} {
		if dir == "Applications" || !writable(dir) {
			continue
		}
		return filepath.Join(dir, filepath.Base(bundle)), eject, true
	}
	return "", "", false
}

func writableDir(dir string) bool {
	if dir == "" {
		return false
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		// ~/Applications may not exist yet: it can be made when its parent is writable.
		return os.IsNotExist(err) && syscall.Access(filepath.Dir(dir), 2) == nil
	}
	return syscall.Access(dir, 2) == nil // W_OK
}

// mntReadOnly is MNT_RDONLY from <sys/mount.h>: the volume is mounted read-only, as a .dmg is.
const mntReadOnly = 0x1

func readOnlyVolume(path string) bool {
	var st syscall.Statfs_t
	return syscall.Statfs(path, &st) == nil && st.Flags&mntReadOnly != 0
}

func runningPlace() (bundle, target, eject string, err error) {
	executable, err := os.Executable()
	if err != nil {
		return "", "", "", err
	}
	if executable, err = filepath.EvalSymlinks(executable); err != nil {
		return "", "", "", err
	}
	bundle, ok := bundleOf(executable)
	if !ok {
		return "", "", "", ErrNotInstalled
	}
	home, _ := os.UserHomeDir()
	target, eject, ok = installPlace(bundle, home, writableDir, readOnlyVolume)
	if !ok {
		return "", "", "", ErrNotWritable
	}
	return bundle, target, eject, nil
}

// CanApply says whether this copy has a place to install the new app: its own folder, /Applications or ~/Applications.
func CanApply() error {
	_, _, _, err := runningPlace()
	return err
}

// Apply installs the app inside the downloaded .dmg (in place of the running one, or in /Applications when it runs from a
// disk image) once this process has exited, opens it, and ejects the disk image it ran from.
func Apply(ctx context.Context, installer string) error {
	bundle, target, eject, err := runningPlace()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return ErrNotWritable
	}
	staging, err := os.MkdirTemp(filepath.Dir(target), ".harflex-update-")
	if err != nil {
		return ErrNotWritable
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(staging)
		}
	}()

	mount := filepath.Join(staging, "volume")
	if err := os.Mkdir(mount, 0o700); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mount, installer).CombinedOutput(); err != nil {
		return fmt.Errorf("open the installer: %s", strings.TrimSpace(string(out)))
	}
	detach := func() { _ = exec.Command("hdiutil", "detach", "-force", mount).Run() }
	apps, _ := filepath.Glob(filepath.Join(mount, "*.app"))
	if len(apps) != 1 {
		detach()
		return errors.New("the installer does not hold one app")
	}
	next := filepath.Join(staging, filepath.Base(target))
	out, err := exec.CommandContext(ctx, "ditto", apps[0], next).CombinedOutput()
	detach()
	if err != nil {
		return fmt.Errorf("copy the new app: %s", strings.TrimSpace(string(out)))
	}
	if err := verifyBundle(ctx, bundle, next); err != nil {
		return err
	}

	script := filepath.Join(staging, "swap.sh")
	if err := os.WriteFile(script, []byte(swapScript(os.Getpid(), target, next, staging, opener, eject)), 0o700); err != nil {
		return err
	}
	helper := exec.Command("/bin/sh", script)
	helper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := helper.Start(); err != nil {
		return err
	}
	_ = helper.Process.Release()
	cleanup = false // the helper removes the staging folder when it is done
	return nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }

// swapScript waits for the app to exit, puts the new bundle at target (setting aside the one there, and bringing it back if
// the move fails), ejects the disk image the old app ran from, opens the new one, and cleans up.
func swapScript(pid int, target, next, staging, open, eject string) string {
	lines := []string{
		"#!/bin/sh",
		"i=0",
		"while kill -0 " + strconv.Itoa(pid) + " 2>/dev/null && [ $i -lt 300 ]; do sleep 0.2; i=$((i+1)); done",
		"target=" + shellQuote(target),
		"old=" + shellQuote(target+".old"),
		"rm -rf \"$old\"",
		"moved=0",
		"if [ -e \"$target\" ]; then mv \"$target\" \"$old\" && moved=1; fi",
		"if [ ! -e \"$target\" ] && mv " + shellQuote(next) + " \"$target\"; then rm -rf \"$old\"; elif [ $moved = 1 ]; then rm -rf \"$target\"; mv \"$old\" \"$target\"; fi",
	}
	if eject != "" {
		lines = append(lines, "hdiutil detach "+shellQuote(eject)+" -quiet >/dev/null 2>&1 || true")
	}
	lines = append(lines, shellQuote(open)+" \"$target\"", "rm -rf "+shellQuote(staging), "")
	return strings.Join(lines, "\n")
}
