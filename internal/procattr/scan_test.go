package procattr

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A process started from the desktop app opens a visible console window on Windows unless it is hidden. Every
// launch in the app's code goes through Hide, through the CLIs' ownCommand, or sets CREATE_NO_WINDOW itself.
func TestEveryProcessTheAppStartsStaysHiddenOnWindows(t *testing.T) {
	root := filepath.Join("..", "..")
	exempt := map[string]string{
		"internal/application/chat_projects.go": "opens the folder in Explorer, a window the person asked for",
		"internal/terminal/terminal.go":         "a pseudo-terminal, not offered on Windows",
		"internal/updater/apply_darwin.go":      "macOS only",
		"internal/updater/apply_linux.go":       "Linux only",
	}
	var files []string
	for _, dir := range []string{"internal", "."} {
		walk := func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && dir == "." && path != root {
				return filepath.SkipDir
			}
			if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		}
		if err := filepath.WalkDir(filepath.Join(root, dir), walk); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		launches := strings.Count(text, "exec.Command(") + strings.Count(text, "exec.CommandContext(")
		if launches == 0 {
			continue
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if _, ok := exempt[relative]; ok || strings.Contains(text, "CREATE_NO_WINDOW") {
			continue
		}
		if hidden := strings.Count(text, "procattr.Hide(") + strings.Count(text, "ownCommand("); hidden < launches {
			t.Errorf("%s starts %d processes but hides %d on Windows", relative, launches, hidden)
		}
	}
}
