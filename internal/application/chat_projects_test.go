package application

import (
	"errors"
	"testing"
)

func TestChatProjectsGroupConversationsAndKeepPinsAndSkipArchived(t *testing.T) {
	s, _, _ := setup(t)
	first, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	archived, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspaceArchived(SetWorkspaceArchivedInput{WorkspaceID: archived.ID, Archived: true}); err != nil {
		t.Fatal(err)
	}
	s.external["fake"] = &fakeExternal{available: true}
	var firstChats []SessionDTO
	for range 3 {
		session, err := s.CreateSession(CreateSessionInput{WorkspaceID: first.ID, BackendID: "fake"})
		if err != nil {
			t.Fatal(err)
		}
		firstChats = append(firstChats, session)
	}
	if _, err := s.CreateSession(CreateSessionInput{WorkspaceID: second.ID, BackendID: "fake"}); err != nil {
		t.Fatal(err)
	}
	oldest := firstChats[0]
	if err := s.SetSessionPinned(SetSessionPinnedInput{SessionID: oldest.ID, Pinned: true}); err != nil {
		t.Fatal(err)
	}
	projects, err := s.ListChatProjects(ListChatProjectsInput{PerProject: 1})
	if err != nil || len(projects) != 2 {
		t.Fatalf("projects = %+v, %v", projects, err)
	}
	var group ChatProjectDTO
	for _, project := range projects {
		if project.Workspace.ID == archived.ID {
			t.Fatal("an archived project must stay out of the sidebar")
		}
		if project.Workspace.ID == first.ID {
			group = project
		}
	}
	if group.Total != 3 || len(group.Chats) != 2 {
		t.Fatalf("one recent chat plus the pinned one, out of three: %+v", group)
	}
	pinned := 0
	for _, chat := range group.Chats {
		if chat.Pinned {
			pinned++
			if chat.ID != oldest.ID {
				t.Fatalf("wrong chat pinned: %+v", chat)
			}
		}
	}
	if pinned != 1 {
		t.Fatalf("the pinned chat must come along: %+v", group.Chats)
	}
	if err := s.SetSessionPinned(SetSessionPinnedInput{SessionID: oldest.ID}); err != nil {
		t.Fatal(err)
	}
	if projects, _ := s.ListChatProjects(ListChatProjectsInput{PerProject: 1}); len(projects[0].Chats)+len(projects[1].Chats) != 2 {
		t.Fatalf("unpinned chat must leave the short list: %+v", projects)
	}
	if err := s.SetSessionPinned(SetSessionPinnedInput{SessionID: "missing", Pinned: true}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("pinning a missing chat: %v", err)
	}
	if err := s.RevealWorkspace("workspace-missing"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("revealing a missing project: %v", err)
	}
}
