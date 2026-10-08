package externalagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var ErrOpenCodePureUnavailable = errors.New("OpenCode pure mode is unavailable")

// verifyOpenCodePure checks the installed executable in private global and
// project plugin fixtures before admitting a catalog query or agent run. The
// marker plugins have no effect outside the private temporary directory.
func verifyOpenCodePure(parent context.Context, path string) error {
	if parent == nil {
		return ErrOpenCodePureUnavailable
	}
	if err := parent.Err(); err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "harflex-opencode-probe-")
	if err != nil {
		return ErrOpenCodePureUnavailable
	}
	defer func() { _ = os.RemoveAll(root) }()
	privateHome := filepath.Join(root, "home")
	global := filepath.Join(privateHome, ".config", "opencode")
	config := filepath.Join(root, "config")
	project := filepath.Join(root, "project")
	for _, dir := range []string{filepath.Join(global, "plugins"), filepath.Join(config, "plugins"), filepath.Join(project, ".opencode", "plugins"), filepath.Join(root, "data"), filepath.Join(root, "cache"), filepath.Join(root, "state"), filepath.Join(root, "appdata")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return ErrOpenCodePureUnavailable
		}
	}
	configFile := filepath.Join(config, "opencode.json")
	if err := os.WriteFile(configFile, []byte("{}\n"), 0600); err != nil {
		return ErrOpenCodePureUnavailable
	}
	for _, target := range []struct{ plugin, marker string }{
		{filepath.Join(global, "plugins", "harflex-global-probe.js"), filepath.Join(root, "global-executed")},
		{filepath.Join(config, "plugins", "harflex-custom-probe.js"), filepath.Join(root, "custom-executed")},
		{filepath.Join(project, ".opencode", "plugins", "harflex-probe.js"), filepath.Join(root, "project-executed")},
	} {
		quoted, _ := json.Marshal(target.marker)
		source := fmt.Sprintf("import { writeFileSync } from 'node:fs';\nwriteFileSync(%s, 'imported');\nexport const HarflexProbe = async () => ({});\n", quoted)
		if err := os.WriteFile(target.plugin, []byte(source), 0600); err != nil {
			return ErrOpenCodePureUnavailable
		}
	}
	env := buildEnvironment("opencode", os.Environ(), runtime.GOOS)
	overrides := []string{
		// These overrides apply only to disposable probe subprocesses. The real
		// catalog and runner retain the user's authenticated CLI configuration.
		"HOME=" + privateHome, "USERPROFILE=" + privateHome, "APPDATA=" + filepath.Join(root, "appdata"),
		"OPENCODE_CONFIG_DIR=" + config, "OPENCODE_CONFIG=" + configFile,
		"XDG_CONFIG_HOME=" + filepath.Join(privateHome, ".config"), "XDG_DATA_HOME=" + filepath.Join(root, "data"),
		"XDG_CACHE_HOME=" + filepath.Join(root, "cache"), "XDG_STATE_HOME=" + filepath.Join(root, "state"),
	}
	for _, entry := range overrides {
		name, _, _ := strings.Cut(entry, "=")
		filtered := env[:0]
		for _, existing := range env {
			current, _, _ := strings.Cut(existing, "=")
			if !strings.EqualFold(current, name) {
				filtered = append(filtered, existing)
			}
		}
		env = append(filtered, entry)
	}
	if err := probeOpenCodeVersion(parent, path, env, project); err != nil {
		return err
	}
	if err := runOpenCodeProbe(parent, path, env, project, "--pure", "models"); err != nil {
		return err
	}
	for _, name := range []string{"global-executed", "custom-executed", "project-executed", "probe-executed"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil || !errors.Is(err, os.ErrNotExist) {
			return ErrOpenCodePureUnavailable
		}
		if _, err := os.Stat(filepath.Join(project, name)); err == nil || !errors.Is(err, os.ErrNotExist) {
			return ErrOpenCodePureUnavailable
		}
	}
	return nil
}

func runOpenCodeProbe(parent context.Context, path string, env []string, project string, args ...string) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.Command(path, args...)
	cmd.Dir = project
	cmd.Env = env
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.WaitDelay = 250 * time.Millisecond
	owned, err := ownCommand(cmd)
	if err != nil {
		return ErrOpenCodePureUnavailable
	}
	defer owned.close()
	if err := owned.start(); err != nil {
		return ErrOpenCodePureUnavailable
	}
	err = owned.wait(ctx)
	if parent.Err() != nil {
		return parent.Err()
	}
	if err != nil || ctx.Err() != nil {
		return ErrOpenCodePureUnavailable
	}
	return nil
}

func probeOpenCodeVersion(parent context.Context, path string, env []string, cwd string) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	cmd := exec.Command(path, "--version")
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdin = strings.NewReader("")
	output := &limitedBuffer{limit: 128}
	cmd.Stdout, cmd.Stderr = output, io.Discard
	cmd.WaitDelay = 250 * time.Millisecond
	owned, err := ownCommand(cmd)
	if err != nil {
		return ErrOpenCodePureUnavailable
	}
	defer owned.close()
	if err := owned.start(); err != nil {
		return ErrOpenCodePureUnavailable
	}
	err = owned.wait(ctx)
	if parent.Err() != nil {
		return parent.Err()
	}
	version := strings.TrimSpace(output.String())
	if err != nil || ctx.Err() != nil || output.truncated || version == "" || !validCatalogText(version, 128) {
		return ErrOpenCodePureUnavailable
	}
	return nil
}
