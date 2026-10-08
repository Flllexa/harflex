package application

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestLocalChannelImportSendAndRestart(t *testing.T) {
	workspacePath := t.TempDir()
	database := filepath.Join(t.TempDir(), "channels.db")
	store, err := sqlite.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(t.Context(), Dependencies{Store: store, External: map[string]ExternalBackend{}})
	workspace, err := service.OpenWorkspace(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Equipe", Folder: "channels/equipe"})
	if err != nil {
		t.Fatal(err)
	}
	if channel.WorkspaceID != workspace.ID || channel.Folder != "channels/equipe" || channel.Status != "ready" {
		t.Fatalf("unexpected channel: %+v", channel)
	}
	repeated, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Equipe", Folder: "channels/equipe"})
	if err != nil || repeated.ID != channel.ID {
		t.Fatalf("same folder created a duplicate channel: %+v %v", repeated, err)
	}
	inbox := filepath.Join(workspacePath, "channels", "equipe", "inbox")
	outbox := filepath.Join(workspacePath, "channels", "equipe", "outbox")
	if err := os.WriteFile(filepath.Join(inbox, "pedido.md"), []byte("Preciso de uma resposta."), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := service.ImportChannelInbox(channel.ID)
	if err != nil || first.Imported != 1 || first.Failed != 0 {
		t.Fatalf("first import: %+v %v", first, err)
	}
	second, err := service.ImportChannelInbox(channel.ID)
	if err != nil || second.Imported != 0 || second.Failed != 0 {
		t.Fatalf("duplicate import: %+v %v", second, err)
	}
	requestID := "0123456789abcdef0123456789abcdef"
	sent, err := service.SendChannelMessage(SendChannelMessageInput{ChannelID: channel.ID, RequestID: requestID, Content: "Resposta aprovada."})
	if err != nil || sent.Status != "sent" || sent.FileName != "harflex-"+requestID+".md" {
		t.Fatalf("send: %+v %v", sent, err)
	}
	again, err := service.SendChannelMessage(SendChannelMessageInput{ChannelID: channel.ID, RequestID: requestID, Content: "Resposta aprovada."})
	if err != nil || again.ID != sent.ID {
		t.Fatalf("send was not idempotent: %+v %v", again, err)
	}
	content, err := os.ReadFile(filepath.Join(outbox, sent.FileName))
	if err != nil || string(content) != "Resposta aprovada." {
		t.Fatalf("outbox content: %q %v", content, err)
	}
	original, err := os.ReadFile(filepath.Join(inbox, "pedido.md"))
	if err != nil || string(original) != "Preciso de uma resposta." {
		t.Fatalf("source changed: %q %v", original, err)
	}
	Shutdown(service)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service = NewService(t.Context(), Dependencies{Store: store, External: map[string]ExternalBackend{}})
	channels, err := service.ListLocalChannels(workspace.ID)
	if err != nil || len(channels) != 1 || channels[0].ID != channel.ID {
		t.Fatalf("channels after restart: %+v %v", channels, err)
	}
	messages, err := service.ListChannelMessages(channel.ID)
	if err != nil || len(messages) != 2 || messages[0].Status != "sent" || messages[1].Status != "received" {
		t.Fatalf("messages after restart: %+v %v", messages, err)
	}
}

func TestLocalChannelConfinesFilesAndRecordsRecoverableErrors(t *testing.T) {
	service, store, _ := setup(t)
	workspacePath := t.TempDir()
	workspace, err := service.OpenWorkspace(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspacePath, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Escape", Folder: "escape"}); err == nil {
		t.Fatal("accepted channel outside workspace")
	}
	for _, folder := range []string{"../escape", outside, ".", "channels/../../escape"} {
		if _, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Bad", Folder: folder}); err == nil {
			t.Fatalf("accepted unsafe folder %q", folder)
		}
	}
	channel, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Safe", Folder: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(workspacePath, "safe", "inbox")
	secret := filepath.Join(outside, "private.md")
	if err := os.WriteFile(secret, []byte("private text"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(inbox, "private.md")); err != nil {
		t.Fatal(err)
	}
	result, err := service.ImportChannelInbox(channel.ID)
	if err != nil || result.Imported != 0 || result.Failed != 1 || result.Channel.Status != "error" || result.Channel.LastError == "" {
		t.Fatalf("unsafe import did not persist a bounded error: %+v %v", result, err)
	}
	if strings.Contains(result.Channel.LastError, outside) {
		t.Fatal("outside path leaked in status")
	}
	repeated, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Safe", Folder: "safe"})
	if err != nil || repeated.Status != "error" || repeated.LastError != "source_unavailable" {
		t.Fatalf("configuration retry erased unresolved error: %+v %v", repeated, err)
	}
	messages, err := service.ListChannelMessages(channel.ID)
	if err != nil || len(messages) != 1 || messages[0].FileName != "private.md" || messages[0].Status != "failed" || messages[0].ErrorCode != "source_unavailable" {
		t.Fatalf("source failure missing from history: %+v %v", messages, err)
	}
	var journalCode string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT error_code FROM channel_events WHERE channel_id=? AND kind='import' AND status='failed'", channel.ID).Scan(&journalCode); err != nil || journalCode != "source_unavailable" {
		t.Fatalf("source failure missing from journal: %q %v", journalCode, err)
	}
	if _, err := service.SendChannelMessage(SendChannelMessageInput{ChannelID: channel.ID, RequestID: "22222222222222222222222222222222", Content: "answer"}); err != nil {
		t.Fatal(err)
	}
	channels, err := service.ListLocalChannels(workspace.ID)
	if err != nil || len(channels) != 1 || channels[0].Status != "error" || channels[0].LastError != "source_unavailable" {
		t.Fatalf("send hid inbox error: %+v %v", channels, err)
	}
	if err := os.Remove(filepath.Join(inbox, "private.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "private.md"), []byte("safe text"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = service.ImportChannelInbox(channel.ID)
	if err != nil || result.Imported != 1 || result.Channel.Status != "ready" || result.Channel.LastError != "" {
		t.Fatalf("recoverable import did not clear status: %+v %v", result, err)
	}
}

func TestLocalChannelSendConflictCanRetryWithoutOverwriting(t *testing.T) {
	service, _, _ := setup(t)
	workspacePath := t.TempDir()
	workspace, err := service.OpenWorkspace(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Safe", Folder: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	requestID := "fedcba9876543210fedcba9876543210"
	file := filepath.Join(workspacePath, "safe", "outbox", "harflex-"+requestID+".md")
	if err := os.WriteFile(file, []byte("another message"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = service.SendChannelMessage(SendChannelMessageInput{ChannelID: channel.ID, RequestID: requestID, Content: "approved text"})
	if !errors.Is(err, ErrChannelOutboxConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	content, err := os.ReadFile(file)
	if err != nil || string(content) != "another message" {
		t.Fatalf("existing file overwritten: %q %v", content, err)
	}
	messages, err := service.ListChannelMessages(channel.ID)
	if err != nil || len(messages) != 1 || messages[0].Status != "failed" || messages[0].ErrorCode != "outbox_conflict" {
		t.Fatalf("failed delivery not durable: %+v %v", messages, err)
	}
	if _, err := service.ImportChannelInbox(channel.ID); err != nil {
		t.Fatal(err)
	}
	channels, err := service.ListLocalChannels(workspace.ID)
	if err != nil || len(channels) != 1 || channels[0].Status != "error" || channels[0].LastError != "outbox_conflict" {
		t.Fatalf("empty import hid outbox error: %+v %v", channels, err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	retried, err := service.RetryChannelMessage(RetryChannelMessageInput{ChannelID: channel.ID, RequestID: requestID})
	if err != nil || retried.Status != "sent" {
		t.Fatalf("retry failed: %+v %v", retried, err)
	}
}

func TestLocalChannelConcurrentSameRequestHasOneDelivery(t *testing.T) {
	service, _, _ := setup(t)
	workspacePath := t.TempDir()
	workspace, err := service.OpenWorkspace(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Safe", Folder: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	input := SendChannelMessageInput{ChannelID: channel.ID, RequestID: "11111111111111111111111111111111", Content: "one delivery"}
	start := make(chan struct{})
	var wait sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			message, err := service.SendChannelMessage(input)
			if err == nil && message.Status != "sent" {
				err = errors.New("delivery did not settle")
			}
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent request failed: %v", err)
		}
	}
	messages, err := service.ListChannelMessages(channel.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("duplicate messages: %+v %v", messages, err)
	}
	files, err := os.ReadDir(filepath.Join(workspacePath, "safe", "outbox"))
	if err != nil || len(files) != 1 {
		t.Fatalf("duplicate files: %+v %v", files, err)
	}
}

func TestLocalChannelRetryReconcilesSentMessageStatus(t *testing.T) {
	service, store, _ := setup(t)
	workspace, err := service.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	channel, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Safe", Folder: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	requestID := "33333333333333333333333333333333"
	if _, err := service.SendChannelMessage(SendChannelMessageInput{ChannelID: channel.ID, RequestID: requestID, Content: "already delivered"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE local_channels SET status='error',outbox_error='write_failed',last_error='write_failed' WHERE id=?", channel.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetryChannelMessage(RetryChannelMessageInput{ChannelID: channel.ID, RequestID: requestID}); err != nil {
		t.Fatal(err)
	}
	channels, err := service.ListLocalChannels(workspace.ID)
	if err != nil || len(channels) != 1 || channels[0].Status != "ready" || channels[0].LastError != "" {
		t.Fatalf("retry did not reconcile known delivery: %+v %v", channels, err)
	}
}

func TestLocalChannelFolderUsesPortableRelativeSeparators(t *testing.T) {
	service, _, _ := setup(t)
	root := t.TempDir()
	workspace, err := service.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Equipe", Folder: `channels\equipe`})
	if err != nil || channel.Folder != "channels/equipe" {
		t.Fatalf("portable folder rejected or not normalized: %+v %v", channel, err)
	}
	if _, err := os.Stat(filepath.Join(root, "channels", "equipe", "inbox")); err != nil {
		t.Fatalf("normalized inbox missing: %v", err)
	}
	for _, folder := range []string{`C:\outside`, `..\outside`, `channels\..\outside`, `\\server\share`, `channels/../outside`} {
		if _, err := service.SaveLocalChannel(SaveLocalChannelInput{WorkspaceID: workspace.ID, Name: "Unsafe", Folder: folder}); err == nil {
			t.Fatalf("unsafe portable path accepted: %q", folder)
		}
	}
}
