package application

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
)

const maxChannelMessageBytes = 64 * 1024
const maxChannelInboxEntries = 500

var ErrChannelNotFound = errors.New("local channel not found")
var ErrChannelFolderUnavailable = errors.New("local channel folder unavailable")
var ErrChannelInboxLimit = errors.New("local channel inbox entry limit")
var ErrChannelSourceUnavailable = errors.New("local channel source unavailable")
var ErrChannelSourceChanged = errors.New("local channel source changed")
var ErrChannelMessageInvalid = errors.New("local channel message invalid")
var ErrChannelOutboxConflict = errors.New("local channel outbox conflict")
var ErrChannelWriteFailed = errors.New("local channel write failed")
var ErrChannelRequestConflict = errors.New("local channel request conflict")
var ErrChannelConfigLocked = errors.New("local channel folder cannot change")

var channelRequestID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func localChannelDTO(item catalog.LocalChannel) LocalChannelDTO {
	return LocalChannelDTO{ID: item.ID, WorkspaceID: item.WorkspaceID, Name: item.Name, Folder: item.Folder, Status: item.Status, LastError: item.LastError, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func channelMessageDTO(item catalog.ChannelMessage) ChannelMessageDTO {
	return ChannelMessageDTO{ID: item.ID, ChannelID: item.ChannelID, Direction: item.Direction, FileName: item.FileName, Content: item.Content, Status: item.Status, ErrorCode: item.ErrorCode, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func normalizeChannelFolder(folder string) (string, error) {
	if folder == "" || len(folder) > 240 || strings.TrimSpace(folder) != folder || !utf8.ValidString(folder) {
		return "", ErrInvalidInput
	}
	for _, character := range folder {
		if unicode.IsControl(character) {
			return "", ErrInvalidInput
		}
	}
	portable := strings.ReplaceAll(folder, `\`, "/")
	if strings.HasPrefix(portable, "/") || strings.Contains(portable, ":") {
		return "", ErrInvalidInput
	}
	for _, segment := range strings.Split(portable, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", ErrInvalidInput
		}
	}
	clean := path.Clean(portable)
	if clean == "." || path.IsAbs(clean) || !filepath.IsLocal(filepath.FromSlash(clean)) {
		return "", ErrInvalidInput
	}
	return clean, nil
}

func openChannelFolder(workspacePath, folder string, create bool) (*os.Root, error) {
	workspace, err := os.OpenRoot(workspacePath)
	if err != nil {
		return nil, ErrChannelFolderUnavailable
	}
	defer workspace.Close()
	if create {
		if err := workspace.MkdirAll(folder, 0700); err != nil {
			return nil, ErrChannelFolderUnavailable
		}
	}
	channel, err := workspace.OpenRoot(folder)
	if err != nil {
		return nil, ErrChannelFolderUnavailable
	}
	if create {
		for _, subdir := range []string{"inbox", "outbox"} {
			if err := channel.MkdirAll(subdir, 0700); err != nil {
				channel.Close()
				return nil, ErrChannelFolderUnavailable
			}
			child, err := channel.OpenRoot(subdir)
			if err != nil {
				channel.Close()
				return nil, ErrChannelFolderUnavailable
			}
			child.Close()
		}
	}
	return channel, nil
}

func (s *Service) getLocalChannel(channelID string) (catalog.LocalChannel, catalog.Workspace, error) {
	if channelID == "" {
		return catalog.LocalChannel{}, catalog.Workspace{}, ErrInvalidInput
	}
	item, err := s.store.GetLocalChannel(s.ctx, channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return item, catalog.Workspace{}, ErrChannelNotFound
	}
	if err != nil {
		return item, catalog.Workspace{}, safe("get local channel", err)
	}
	workspace, err := s.knowledgeWorkspace(item.WorkspaceID)
	return item, workspace, err
}

func (s *Service) SaveLocalChannel(in SaveLocalChannelInput) (LocalChannelDTO, error) {
	if err := s.beginCall(); err != nil {
		return LocalChannelDTO{}, err
	}
	defer s.endCall()
	name := strings.TrimSpace(in.Name)
	if len(name) == 0 || len(name) > 80 || !utf8.ValidString(name) {
		return LocalChannelDTO{}, ErrInvalidInput
	}
	folder, err := normalizeChannelFolder(in.Folder)
	if err != nil {
		return LocalChannelDTO{}, err
	}
	workspace, err := s.knowledgeWorkspace(in.WorkspaceID)
	if err != nil {
		return LocalChannelDTO{}, err
	}
	now := time.Now().UTC()
	item := catalog.LocalChannel{ID: id.New(), WorkspaceID: workspace.ID, Name: name, Folder: folder, Status: "ready", CreatedAt: now, UpdatedAt: now}
	if in.ID == "" {
		previous, err := s.store.GetLocalChannelByFolder(s.ctx, workspace.ID, folder)
		if err == nil {
			item.ID, item.CreatedAt, item.Status, item.LastError, item.InboxError, item.OutboxError = previous.ID, previous.CreatedAt, previous.Status, previous.LastError, previous.InboxError, previous.OutboxError
		} else if !errors.Is(err, sql.ErrNoRows) {
			return LocalChannelDTO{}, safe("find local channel folder", err)
		}
	}
	if in.ID != "" {
		previous, previousWorkspace, err := s.getLocalChannel(in.ID)
		if err != nil {
			return LocalChannelDTO{}, err
		}
		if previousWorkspace.ID != workspace.ID {
			return LocalChannelDTO{}, ErrChannelNotFound
		}
		if previous.Folder != folder {
			return LocalChannelDTO{}, ErrChannelConfigLocked
		}
		item.ID, item.CreatedAt, item.Status, item.LastError, item.InboxError, item.OutboxError = previous.ID, previous.CreatedAt, previous.Status, previous.LastError, previous.InboxError, previous.OutboxError
	}
	root, err := openChannelFolder(workspace.Path, item.Folder, true)
	if err != nil {
		return LocalChannelDTO{}, err
	}
	root.Close()
	if err := s.store.SaveLocalChannel(s.ctx, item); err != nil {
		return LocalChannelDTO{}, safe("save local channel", err)
	}
	return localChannelDTO(item), nil
}

func (s *Service) ListLocalChannels(workspaceID string) ([]LocalChannelDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if _, err := s.knowledgeWorkspace(workspaceID); err != nil {
		return nil, err
	}
	items, err := s.store.ListLocalChannels(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("list local channels", err)
	}
	result := make([]LocalChannelDTO, 0, len(items))
	for _, item := range items {
		result = append(result, localChannelDTO(item))
	}
	return result, nil
}

func readChannelSource(root *os.Root, name string) (string, error) {
	file, err := root.Open(name)
	if err != nil {
		return "", ErrChannelSourceUnavailable
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return "", ErrChannelSourceUnavailable
	}
	if before.Size() > maxChannelMessageBytes {
		return "", ErrChannelMessageInvalid
	}
	content, err := io.ReadAll(io.LimitReader(file, maxChannelMessageBytes+1))
	if err != nil {
		return "", ErrChannelSourceUnavailable
	}
	if len(content) > maxChannelMessageBytes || !utf8.Valid(content) || strings.ContainsRune(string(content), 0) || strings.TrimSpace(string(content)) == "" {
		return "", ErrChannelMessageInvalid
	}
	after, err := file.Stat()
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || int64(len(content)) != after.Size() {
		return "", ErrChannelSourceChanged
	}
	return string(content), nil
}

func (s *Service) setChannelStatus(channelID, side, code string) (LocalChannelDTO, error) {
	item, err := s.store.SetLocalChannelStatus(s.ctx, channelID, side, code)
	if err != nil {
		return LocalChannelDTO{}, safe("set local channel status", err)
	}
	return localChannelDTO(item), nil
}

func (s *Service) ImportChannelInbox(channelID string) (ChannelImportDTO, error) {
	if err := s.beginCall(); err != nil {
		return ChannelImportDTO{}, err
	}
	defer s.endCall()
	channel, workspace, err := s.getLocalChannel(channelID)
	if err != nil {
		return ChannelImportDTO{}, err
	}
	root, err := openChannelFolder(workspace.Path, channel.Folder, false)
	if err != nil {
		_, _ = s.setChannelStatus(channel.ID, "inbox", "folder_unavailable")
		return ChannelImportDTO{}, err
	}
	defer root.Close()
	inbox, err := root.OpenRoot("inbox")
	if err != nil {
		_, _ = s.setChannelStatus(channel.ID, "inbox", "folder_unavailable")
		return ChannelImportDTO{}, ErrChannelFolderUnavailable
	}
	defer inbox.Close()
	dir, err := inbox.Open(".")
	if err != nil {
		return ChannelImportDTO{}, ErrChannelFolderUnavailable
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxChannelInboxEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return ChannelImportDTO{}, ErrChannelFolderUnavailable
	}
	if len(entries) > maxChannelInboxEntries {
		_, _ = s.setChannelStatus(channel.ID, "inbox", "inbox_limit")
		return ChannelImportDTO{}, ErrChannelInboxLimit
	}
	result := ChannelImportDTO{}
	lastError := ""
	for _, entry := range entries {
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".txt" && ext != ".md" {
			continue
		}
		content, readErr := readChannelSource(inbox, entry.Name())
		if readErr != nil {
			result.Failed++
			switch {
			case errors.Is(readErr, ErrChannelMessageInvalid):
				lastError = "message_invalid"
			case errors.Is(readErr, ErrChannelSourceChanged):
				lastError = "source_changed"
			default:
				lastError = "source_unavailable"
			}
			now := time.Now().UTC()
			failure := catalog.ChannelMessage{ID: id.New(), ChannelID: channel.ID, Direction: "incoming", FileName: entry.Name(), ContentHash: "error:" + lastError, Status: "failed", ErrorCode: lastError, CreatedAt: now, UpdatedAt: now}
			if _, err := s.store.AddIncomingChannelMessage(s.ctx, failure); err != nil {
				return ChannelImportDTO{}, safe("record failed incoming message", err)
			}
			continue
		}
		now := time.Now().UTC()
		message := catalog.ChannelMessage{ID: id.New(), ChannelID: channel.ID, Direction: "incoming", FileName: entry.Name(), ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(content))), Content: content, Status: "received", CreatedAt: now, UpdatedAt: now}
		inserted, err := s.store.AddIncomingChannelMessage(s.ctx, message)
		if err != nil {
			return ChannelImportDTO{}, safe("record incoming message", err)
		}
		if inserted {
			result.Imported++
		}
	}
	result.Channel, err = s.setChannelStatus(channel.ID, "inbox", lastError)
	if err != nil {
		return ChannelImportDTO{}, err
	}
	return result, nil
}

func (s *Service) ListChannelMessages(channelID string) ([]ChannelMessageDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if _, _, err := s.getLocalChannel(channelID); err != nil {
		return nil, err
	}
	items, err := s.store.ListChannelMessages(s.ctx, channelID)
	if err != nil {
		return nil, safe("list channel messages", err)
	}
	result := make([]ChannelMessageDTO, 0, len(items))
	for _, item := range items {
		result = append(result, channelMessageDTO(item))
	}
	return result, nil
}

func validChannelContent(content string) bool {
	return len(content) > 0 && len(content) <= maxChannelMessageBytes && utf8.ValidString(content) && !strings.ContainsRune(content, 0) && strings.TrimSpace(content) != ""
}

func (s *Service) deliverChannelMessage(channel catalog.LocalChannel, workspace catalog.Workspace, item catalog.ChannelMessage) (ChannelMessageDTO, error) {
	if item.Status == "sent" {
		if _, err := s.setChannelStatus(channel.ID, "outbox", ""); err != nil {
			return ChannelMessageDTO{}, err
		}
		return channelMessageDTO(item), nil
	}
	root, err := openChannelFolder(workspace.Path, channel.Folder, false)
	if err != nil {
		return s.failChannelDelivery(channel.ID, item.ID, "folder_unavailable", ErrChannelFolderUnavailable)
	}
	defer root.Close()
	outbox, err := root.OpenRoot("outbox")
	if err != nil {
		return s.failChannelDelivery(channel.ID, item.ID, "folder_unavailable", ErrChannelFolderUnavailable)
	}
	defer outbox.Close()
	// A complete staging file becomes visible under the final name only after
	// the hard link succeeds. Link never replaces a preexisting user file.
	staging := ".harflex-" + id.New() + ".pending"
	file, err := outbox.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return s.failChannelDelivery(channel.ID, item.ID, "write_failed", ErrChannelWriteFailed)
	}
	defer outbox.Remove(staging)
	_, writeErr := io.WriteString(file, item.Content)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return s.failChannelDelivery(channel.ID, item.ID, "write_failed", ErrChannelWriteFailed)
	}
	if err := outbox.Link(staging, item.FileName); errors.Is(err, fs.ErrExist) {
		content, readErr := readChannelSource(outbox, item.FileName)
		if readErr != nil || content != item.Content {
			return s.failChannelDelivery(channel.ID, item.ID, "outbox_conflict", ErrChannelOutboxConflict)
		}
	} else if err != nil {
		return s.failChannelDelivery(channel.ID, item.ID, "write_failed", ErrChannelWriteFailed)
	}
	stored, err := s.store.SetChannelMessageStatus(s.ctx, channel.ID, item.ID, "sent", "")
	if err != nil {
		return ChannelMessageDTO{}, safe("record channel delivery", err)
	}
	if _, err := s.setChannelStatus(channel.ID, "outbox", ""); err != nil {
		return ChannelMessageDTO{}, err
	}
	return channelMessageDTO(stored), nil
}

func (s *Service) failChannelDelivery(channelID, messageID, code string, cause error) (ChannelMessageDTO, error) {
	if _, err := s.store.SetChannelMessageStatus(s.ctx, channelID, messageID, "failed", code); err != nil {
		return ChannelMessageDTO{}, safe("record failed channel delivery", err)
	}
	if _, err := s.setChannelStatus(channelID, "outbox", code); err != nil {
		return ChannelMessageDTO{}, err
	}
	return ChannelMessageDTO{}, cause
}

func (s *Service) SendChannelMessage(in SendChannelMessageInput) (ChannelMessageDTO, error) {
	if err := s.beginCall(); err != nil {
		return ChannelMessageDTO{}, err
	}
	defer s.endCall()
	if !channelRequestID.MatchString(in.RequestID) || !validChannelContent(in.Content) {
		return ChannelMessageDTO{}, ErrChannelMessageInvalid
	}
	channel, workspace, err := s.getLocalChannel(in.ChannelID)
	if err != nil {
		return ChannelMessageDTO{}, err
	}
	now := time.Now().UTC()
	item := catalog.ChannelMessage{ID: id.New(), ChannelID: channel.ID, RequestID: in.RequestID, Direction: "outgoing", FileName: "harflex-" + in.RequestID + ".md", ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(in.Content))), Content: in.Content, Status: "pending", CreatedAt: now, UpdatedAt: now}
	stored, err := s.store.ReserveOutgoingChannelMessage(s.ctx, item)
	if err != nil {
		return ChannelMessageDTO{}, safe("reserve channel message", err)
	}
	if stored.ContentHash != item.ContentHash || stored.Content != item.Content {
		return ChannelMessageDTO{}, ErrChannelRequestConflict
	}
	return s.deliverChannelMessage(channel, workspace, stored)
}

func (s *Service) RetryChannelMessage(in RetryChannelMessageInput) (ChannelMessageDTO, error) {
	if err := s.beginCall(); err != nil {
		return ChannelMessageDTO{}, err
	}
	defer s.endCall()
	if !channelRequestID.MatchString(in.RequestID) {
		return ChannelMessageDTO{}, ErrChannelMessageInvalid
	}
	channel, workspace, err := s.getLocalChannel(in.ChannelID)
	if err != nil {
		return ChannelMessageDTO{}, err
	}
	item, err := s.store.GetOutgoingChannelMessage(s.ctx, channel.ID, in.RequestID)
	if errors.Is(err, sql.ErrNoRows) {
		return ChannelMessageDTO{}, ErrChannelNotFound
	}
	if err != nil {
		return ChannelMessageDTO{}, safe("get channel message", err)
	}
	return s.deliverChannelMessage(channel, workspace, item)
}
