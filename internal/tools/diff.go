package tools

import (
	"path/filepath"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

func unifiedDiff(path, before, after string) (string, error) {
	if before == after {
		return "", nil
	}
	path = filepath.ToSlash(filepath.Clean(path))
	path = strings.NewReplacer("\r", `\r`, "\n", `\n`, "\t", `\t`).Replace(path)
	return difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: diffLines(before), B: diffLines(after), FromFile: path, ToFile: path, Context: 3})
}

func diffLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	// A missing terminal newline must remain a visible content difference.
	lines[len(lines)-1] += "\n\\ No newline at end of file\n"
	return lines
}
