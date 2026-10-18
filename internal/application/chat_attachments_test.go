package application

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveChatAttachmentKeepsImagesWhereTheAICanReadThemAndGitIgnoresThem(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	workspace, err := s.store.GetWorkspace(t.Context(), session.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveChatAttachment(SaveChatAttachmentInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID, Name: "../tela final.png", Data: base64.StdEncoding.EncodeToString([]byte("png-bytes"))})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(saved.Path, ".harflex/attachments/") || strings.Contains(saved.Path, "..") || !strings.HasSuffix(saved.Path, "tela-final.png") {
		t.Fatalf("path = %q", saved.Path)
	}
	content, err := os.ReadFile(filepath.Join(workspace.Path, filepath.FromSlash(saved.Path)))
	if err != nil || string(content) != "png-bytes" {
		t.Fatalf("content = %q %v", content, err)
	}
	ignore, err := os.ReadFile(filepath.Join(workspace.Path, ".harflex", ".gitignore"))
	if err != nil || strings.TrimSpace(string(ignore)) != "*" {
		t.Fatalf("the attachments are not ignored by git: %q %v", ignore, err)
	}
	if _, err := s.SaveChatAttachment(SaveChatAttachmentInput{WorkspaceID: session.WorkspaceID, Name: "big.png", Data: base64.StdEncoding.EncodeToString(make([]byte, maxChatAttachmentBytes+1))}); err == nil {
		t.Fatal("an oversized attachment was saved")
	}
}
