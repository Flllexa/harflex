package application

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// maxChatAttachmentBytes bounds one file attached to a chat message.
const maxChatAttachmentBytes = 20 << 20

type SaveChatAttachmentInput struct {
	// SessionID is empty for the first message of a chat that does not exist yet.
	SessionID   string `json:"sessionId"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	// Data is the file, base64-encoded.
	Data string `json:"data"`
}

type ChatAttachmentDTO struct {
	// Path is relative to the folder the conversation's tools read, with forward slashes.
	Path string `json:"path"`
}

var unsafeAttachmentName = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)

// SaveChatAttachment keeps a file the person attached (an image the model reads with its tools) in the folder the
// conversation works in: the project, or the private copy of a Code or QA run. It lives under .harflex/attachments,
// which ignores itself in git, so it never reaches a commit, a patch or a pull request.
func (s *Service) SaveChatAttachment(in SaveChatAttachmentInput) (ChatAttachmentDTO, error) {
	if err := s.beginCall(); err != nil {
		return ChatAttachmentDTO{}, err
	}
	defer s.endCall()
	if strings.TrimSpace(in.WorkspaceID) == "" || strings.TrimSpace(in.Name) == "" || base64.StdEncoding.DecodedLen(len(in.Data)) > maxChatAttachmentBytes+3 {
		return ChatAttachmentDTO{}, ErrInvalidInput
	}
	content, err := base64.StdEncoding.DecodeString(in.Data)
	if err != nil || len(content) > maxChatAttachmentBytes {
		return ChatAttachmentDTO{}, ErrInvalidInput
	}
	workspace, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID)
	if err != nil {
		return ChatAttachmentDTO{}, ErrWorkspaceNotFound
	}
	root := workspace.Path
	if in.SessionID != "" {
		record, err := s.store.GetSession(s.ctx, in.SessionID)
		if err != nil || record.WorkspaceID != workspace.ID {
			return ChatAttachmentDTO{}, ErrSessionNotFound
		}
		if executionRoot, err := s.executionRootForSession(in.SessionID); err == nil && executionRoot != "" {
			root = executionRoot
		}
	}
	name := strings.Trim(unsafeAttachmentName.ReplaceAllString(filepath.Base(strings.ReplaceAll(in.Name, "\\", "/")), "-"), "-.")
	if name == "" {
		name = "anexo"
	}
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	nonce := make([]byte, 4)
	if _, err := rand.Read(nonce); err != nil {
		return ChatAttachmentDTO{}, safe("name attachment", err)
	}
	folder := filepath.Join(root, ".harflex", "attachments")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return ChatAttachmentDTO{}, safe("create attachments folder", err)
	}
	ignore := filepath.Join(root, ".harflex", ".gitignore")
	if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(ignore, []byte("*\n"), 0o600); err != nil {
			return ChatAttachmentDTO{}, safe("ignore attachments folder", err)
		}
	}
	file := hex.EncodeToString(nonce) + "-" + name
	if err := os.WriteFile(filepath.Join(folder, file), content, 0o600); err != nil {
		return ChatAttachmentDTO{}, safe("save attachment", err)
	}
	return ChatAttachmentDTO{Path: ".harflex/attachments/" + file}, nil
}
