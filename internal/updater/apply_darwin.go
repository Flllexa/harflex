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

// Apply puts the app inside the downloaded .dmg in place of the running one, once this process has exited.
func Apply(ctx context.Context, installer string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if executable, err = filepath.EvalSymlinks(executable); err != nil {
		return err
	}
	bundle, ok := bundleOf(executable)
	if !ok {
		return ErrNotInstalled
	}
	parent := filepath.Dir(bundle)
	staging, err := os.MkdirTemp(parent, ".harflex-update-")
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
	next := filepath.Join(staging, filepath.Base(bundle))
	out, err := exec.CommandContext(ctx, "ditto", apps[0], next).CombinedOutput()
	detach()
	if err != nil {
		return fmt.Errorf("copy the new app: %s", strings.TrimSpace(string(out)))
	}
	if err := verifyBundle(ctx, bundle, next); err != nil {
		return err
	}

	script := filepath.Join(staging, "swap.sh")
	if err := os.WriteFile(script, []byte(swapScript(os.Getpid(), bundle, next, staging, opener)), 0o700); err != nil {
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

// swapScript waits for the app to exit, moves the new bundle in (and the old one back if that fails), opens it, and cleans up.
func swapScript(pid int, bundle, next, staging, open string) string {
	return strings.Join([]string{
		"#!/bin/sh",
		"i=0",
		"while kill -0 " + strconv.Itoa(pid) + " 2>/dev/null && [ $i -lt 300 ]; do sleep 0.2; i=$((i+1)); done",
		"old=" + shellQuote(bundle+".old"),
		"rm -rf \"$old\"",
		"if mv " + shellQuote(bundle) + " \"$old\"; then",
		"  if mv " + shellQuote(next) + " " + shellQuote(bundle) + "; then rm -rf \"$old\"; else mv \"$old\" " + shellQuote(bundle) + "; fi",
		"fi",
		shellQuote(open) + " " + shellQuote(bundle),
		"rm -rf " + shellQuote(staging),
		"",
	}, "\n")
}

// CanApply says whether this copy runs from an app bundle in a folder the user can write to.
func CanApply() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if executable, err = filepath.EvalSymlinks(executable); err != nil {
		return err
	}
	bundle, ok := bundleOf(executable)
	if !ok {
		return ErrNotInstalled
	}
	if syscall.Access(filepath.Dir(bundle), 2) != nil { // W_OK
		return ErrNotWritable
	}
	return nil
}
