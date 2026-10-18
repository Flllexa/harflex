package application

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/tools"
)

func toolNamed(t *testing.T, items []tools.Tool, name string) tools.Tool {
	t.Helper()
	for _, item := range items {
		if item.Spec().Name == name {
			return item
		}
	}
	t.Fatalf("no tool %s", name)
	return nil
}

func runTool(t *testing.T, tool tools.Tool, args string) (string, error) {
	t.Helper()
	result, err := tool.Execute(t.Context(), json.RawMessage(args), nil)
	return string(result.Content), err
}

// localTestWorkspace is a project with a pipeline whose local test is applied, as the tester sees it.
func localTestWorkspace(t *testing.T) (*Service, WorkspaceDTO, catalog.PipelineLocalTest) {
	t.Helper()
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "TODO", Objective: "Testar localmente"})
	if err != nil {
		t.Fatal(err)
	}
	test := catalog.PipelineLocalTest{ID: "test-1", PipelineID: run.ID, WorkspaceID: workspace.ID, ArtifactVersion: 1, BackupPath: t.TempDir(), Applied: json.RawMessage(`{}`), Status: "applied", CreatedAt: time.Now().UTC()}
	if err := db.CreatePipelineLocalTest(t.Context(), test); err != nil {
		t.Fatal(err)
	}
	return s, workspace, test
}

func TestStartServiceOpensANamedTerminalAndRecordsIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no pseudo-terminal on Windows")
	}
	t.Setenv("SHELL", "/bin/sh")
	s, workspace, test := localTestWorkspace(t)
	items := s.localTestTools(workspace.ID, "tester-session")
	started, err := runTool(t, toolNamed(t, items, "start_service"), `{"name":"web","command":"printf 'servidor-''pronto\\n'"}`)
	if err != nil || !strings.Contains(started, `"name":"web"`) {
		t.Fatalf("start_service: %s %v", started, err)
	}
	t.Cleanup(func() { s.terminalManager().CloseAll() })
	list, err := s.ListTerminals(ListTerminalsInput{WorkspaceID: workspace.ID})
	if err != nil || len(list) != 1 || list[0].Name != "web" {
		t.Fatalf("terminals: %+v %v", list, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		output, err := runTool(t, toolNamed(t, items, "service_output"), `{"name":"web"}`)
		if err == nil && strings.Contains(output, "servidor-pronto") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service_output: %s %v", output, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	record, err := s.store.LatestPipelineLocalTest(t.Context(), test.PipelineID)
	if err != nil || len(record.Services) != 1 || record.Services[0].Name != "web" || record.Services[0].TerminalID != list[0].ID {
		t.Fatalf("the service was not recorded: %+v %v", record.Services, err)
	}
}

func TestStartServiceRejectsABadName(t *testing.T) {
	s, workspace, _ := localTestWorkspace(t)
	if _, err := runTool(t, toolNamed(t, s.localTestTools(workspace.ID, "tester-session"), "start_service"), `{"name":"../x","command":"true"}`); err == nil {
		t.Fatal("a service name with a path was accepted")
	}
	if _, err := runTool(t, toolNamed(t, s.localTestTools(workspace.ID, "tester-session"), "start_service"), `{"name":"web","command":"true","cwd":"../.."}`); err == nil {
		t.Fatal("a service folder outside the project was accepted")
	}
}

func TestTesterWritesOnlyIgnoredFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	guard, err := tools.NewPathGuard(root)
	if err != nil {
		t.Fatal(err)
	}
	write := ignoredOnlyTool(tools.NewWriteTool(guard), root)
	if _, err := runTool(t, write, `{"path":".env","content":"PORT=5173\n"}`); err != nil {
		t.Fatalf("writing .env was refused: %v", err)
	}
	_, err = runTool(t, write, `{"path":"src/app.js","content":"x"}`)
	var failure *agentcore.ToolFailure
	if err == nil || !errors.As(err, &failure) || !strings.Contains(failure.Message, "arquivos ignorados pelo git") {
		t.Fatalf("writing a project file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "src/app.js")); !os.IsNotExist(err) {
		t.Fatal("the refused write still wrote the file")
	}
	edit := ignoredOnlyTool(tools.NewEditTool(guard), root)
	if _, err := runTool(t, edit, `{"path":".gitignore","oldText":".env","newText":".env\nx"}`); err == nil {
		t.Fatal("editing a tracked-looking project file was accepted")
	}
}

func TestLocalTestReportReadsTheBlock(t *testing.T) {
	text := "Pronto.\n\n```harflex-local\n{\"urls\":[{\"label\":\"App\",\"url\":\"http://localhost:5173\"},{\"label\":\"x\",\"url\":\"javascript:alert(1)\"}],\"stop\":\"Ctrl-C nos terminais\",\"notes\":\"Login: admin\"}\n```\n"
	report, ok := localTestReport(text)
	if !ok || len(report.URLs) != 1 || report.URLs[0].URL != "http://localhost:5173" || report.URLs[0].Label != "App" || report.Stop != "Ctrl-C nos terminais" || report.Notes != "Login: admin" {
		t.Fatalf("report: %+v %v", report, ok)
	}
	if _, ok := localTestReport("sem bloco"); ok {
		t.Fatal("a reply without the block was read as a report")
	}
	if prompt := localTestPrompt(catalog.PipelineRun{Title: "TODO", Objective: "Lista"}, "/projeto"); !strings.Contains(prompt, "harflex-local") || !strings.Contains(prompt, "start_service") {
		t.Fatalf("prompt: %s", prompt)
	}
}
