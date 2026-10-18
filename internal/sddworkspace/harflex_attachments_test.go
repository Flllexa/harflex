package sddworkspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Images attached to chats (.harflex) never ride into a private copy or count against its bounds.
func TestScanLeavesOutChatAttachments(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".harflex", "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".harflex", "attachments", "tela.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range manifest.Entries {
		if entry.Path == ".harflex" || filepath.Dir(entry.Path) == ".harflex/attachments" {
			t.Fatalf("chat attachments entered the manifest: %+v", entry)
		}
	}
}
