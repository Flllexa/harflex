package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

const maxCLIConfigBytes = 8 << 20

// cliCatalogRevision binds a selected model to the executable, workspace and
// known configuration sources. File bytes (which can contain credentials) are
// hashed in memory and never returned, journaled or logged.
func cliCatalogRevision(backend, executable, version, cwd string) (string, error) {
	if !filepath.IsAbs(cwd) || executable == "" || version == "" {
		return "", ErrInvalidInput
	}
	h := sha256.New()
	envNames := []string{"PATH", "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"}
	if backend == "codex" {
		envNames = append(envNames, "CODEX_HOME")
	}
	if backend == "claude" {
		envNames = append(envNames, "CLAUDE_CONFIG_DIR")
	}
	fields := []string{backend, executable, version, cwd}
	for _, name := range envNames {
		fields = append(fields, name+"="+os.Getenv(name))
	}
	encoded, _ := json.Marshal(fields)
	_, _ = h.Write(encoded)
	paths, err := cliConfigPaths(backend, cwd)
	if err != nil {
		return "", err
	}
	remaining := int64(maxCLIConfigBytes)
	for _, path := range paths {
		if err := hashCLIConfigFile(h, path, &remaining); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func cliConfigPaths(backend, cwd string) ([]string, error) {
	paths := map[string]bool{}
	add := func(path string) {
		if path != "" {
			paths[filepath.Clean(path)] = true
		}
	}
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig == "" && home != "" {
		xdgConfig = filepath.Join(home, ".config")
	}
	xdgData := os.Getenv("XDG_DATA_HOME")
	if xdgData == "" && home != "" {
		xdgData = filepath.Join(home, ".local", "share")
	}
	switch backend {
	case "codex":
		configHome := os.Getenv("CODEX_HOME")
		if configHome == "" && home != "" {
			configHome = filepath.Join(home, ".codex")
		}
		if configHome != "" {
			add(filepath.Join(configHome, "config.toml"))
			add(filepath.Join(configHome, "auth.json"))
		}
	case "opencode":
		if xdgConfig != "" {
			for _, name := range []string{"opencode.json", "opencode.jsonc"} {
				add(filepath.Join(xdgConfig, "opencode", name))
			}
		}
		if xdgData != "" {
			add(filepath.Join(xdgData, "opencode", "auth.json"))
		}
		if runtime.GOOS == "darwin" {
			for _, name := range []string{"opencode.json", "opencode.jsonc"} {
				add(filepath.Join("/Library/Application Support/opencode", name))
			}
		}
	case "claude":
		// Settings only: ~/.claude.json and the stored credentials change on every run and token refresh,
		// and would retire a chosen model for nothing.
		configHome := os.Getenv("CLAUDE_CONFIG_DIR")
		if configHome == "" && home != "" {
			configHome = filepath.Join(home, ".claude")
		}
		if configHome != "" {
			add(filepath.Join(configHome, "settings.json"))
		}
		if runtime.GOOS == "darwin" {
			add("/Library/Application Support/ClaudeCode/managed-settings.json")
		}
	default:
		return nil, ErrInvalidInput
	}
	dir := cwd
	for depth := 0; depth < 64; depth++ {
		if backend == "codex" {
			add(filepath.Join(dir, ".codex", "config.toml"))
		} else if backend == "claude" {
			add(filepath.Join(dir, ".claude", "settings.json"))
			add(filepath.Join(dir, ".claude", "settings.local.json"))
		} else {
			for _, name := range []string{"opencode.json", "opencode.jsonc"} {
				add(filepath.Join(dir, name))
				add(filepath.Join(dir, ".opencode", name))
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			result := make([]string, 0, len(paths))
			for path := range paths {
				result = append(result, path)
			}
			sort.Strings(result)
			return result, nil
		}
		dir = parent
	}
	return nil, ErrInvalidInput
}

func hashCLIConfigFile(h hash.Hash, path string, remaining *int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintf(h, "absent:%s\x00", path)
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > *remaining {
		return ErrInvalidInput
	}
	file, err := os.Open(path)
	if err != nil {
		return ErrInvalidInput
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, *remaining+1))
	if err != nil || int64(len(data)) > *remaining {
		clear(data)
		return ErrInvalidInput
	}
	*remaining -= int64(len(data))
	_, _ = fmt.Fprintf(h, "present:%s:%d\x00", path, len(data))
	_, _ = h.Write(data)
	clear(data)
	return nil
}
