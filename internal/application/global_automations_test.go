package application

import "testing"

// MCP servers, skills and workflows belong to the whole app: set up in one project, they serve every project.
// A schedule is listed everywhere too, and still runs in its own project.
func TestAutomationsSetUpInOneProjectServeEveryProject(t *testing.T) {
	s, _, _ := setup(t)
	first, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	server, err := s.SaveMCPServer(SaveMCPServerInput{WorkspaceID: first.ID, Name: "Bitbucket", Transport: "http", URL: "http://127.0.0.1:3333/mcp", Token: "synthetic-mcp-token"})
	if err != nil {
		t.Fatal(err)
	}
	servers, err := s.ListMCPServers(second.ID)
	if err != nil || len(servers) != 1 || servers[0].ID != server.ID {
		t.Fatalf("an MCP server set up in another project: %+v %v", servers, err)
	}
	if _, err := s.SaveMCPServer(SaveMCPServerInput{ID: server.ID, WorkspaceID: second.ID, Name: "Bitbucket Cloud", Transport: "http", URL: "http://127.0.0.1:3333/mcp"}); err != nil {
		t.Fatalf("editing it from another project: %v", err)
	}
	if again, _ := s.ListMCPServers(first.ID); len(again) != 1 || again[0].Name != "Bitbucket Cloud" {
		t.Fatalf("the edit did not reach every project: %+v", again)
	}

	skill, err := s.SaveSkill(SaveSkillInput{WorkspaceID: first.ID, Name: "Revisão", Description: "Analisa testes", Content: "Revise cobertura antes de concluir", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if skills, err := s.ListSkills(second.ID); err != nil || len(skills) != 1 || skills[0].ID != skill.ID {
		t.Fatalf("a skill set up in another project: %+v %v", skills, err)
	}

	workflow, err := s.SaveWorkflow(SaveWorkflowInput{WorkspaceID: first.ID, Name: "Revisar e documentar", Steps: []WorkflowStepInput{{Name: "Revisar", Prompt: "Revise o código"}}})
	if err != nil {
		t.Fatal(err)
	}
	if workflows, err := s.ListWorkflows(second.ID); err != nil || len(workflows) != 1 {
		t.Fatalf("a workflow set up in another project: %+v %v", workflows, err)
	}
	run, err := s.StartWorkflow(StartWorkflowInput{WorkflowID: workflow.ID, WorkspaceID: second.ID, BackendID: "local"})
	if err != nil || run.WorkspaceID != second.ID {
		t.Fatalf("a workflow runs in the project it was started from: %+v %v", run, err)
	}
	if origin, err := s.StartWorkflow(StartWorkflowInput{WorkflowID: workflow.ID, BackendID: "local"}); err != nil || origin.WorkspaceID != first.ID {
		t.Fatalf("without a project it runs where it was set up: %+v %v", origin, err)
	}

	schedule, err := s.SaveSchedule(SaveScheduleInput{WorkspaceID: second.ID, Name: "Revisão diária", TargetKind: "workflow", WorkflowID: workflow.ID, BackendID: "local", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true})
	if err != nil {
		t.Fatalf("a schedule of a workflow set up in another project: %v", err)
	}
	schedules, err := s.ListSchedules(first.ID)
	if err != nil || len(schedules) != 1 || schedules[0].ID != schedule.ID || schedules[0].WorkspaceID != second.ID || schedules[0].WorkspacePath != second.Path {
		t.Fatalf("schedules are listed from every project with their own: %+v %v", schedules, err)
	}
}
