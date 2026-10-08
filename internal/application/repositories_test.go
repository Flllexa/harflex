package application

import (
	"errors"
	"os/exec"
	"testing"
)

func TestInspectRepositoryRequiresRegisteredWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	s, _, _ := setup(t)
	if _, err := s.InspectRepository("unknown"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("unknown workspace admitted: %v", err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.InspectRepository(workspace.ID)
	if err != nil || result.IsRepository {
		t.Fatalf("non-repo inspection: %+v %v", result, err)
	}
}
