package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/repositories"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/security"
	"github.com/persioflexa/harflex/internal/terminal"
	"github.com/persioflexa/harflex/internal/tools"
)

// localTestMode is the conversation that prepares and runs the project in its own folder so the person can try a
// Code change before approving it; testerRole is its pipeline role.
const (
	localTestMode = "local_test"
	testerRole    = "tester"
)

var serviceName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ansiSequence matches terminal control sequences, left out of what the model reads back.
var ansiSequence = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07]*\x07")

const serviceOutputBytes = 16 * 1024

var errNoLocalTest = errors.New("this project has no local test applied")

// localTestTools start the project's long-running processes in terminals the person sees, and read them back.
func (s *Service) localTestTools(workspaceID, sessionID string) []tools.Tool {
	return []tools.Tool{
		harflexTool{service: s, workspaceID: workspaceID, sessionID: sessionID, risk: security.Shell, spec: agentcore.ToolSpec{
			Name:        "start_service",
			Description: "Start a long-running process of the project (dev server, API, worker, database) in its own terminal, which the person sees in Harflex's Terminal panel. name is a short id such as web or api; command runs in the project folder, or in cwd (a folder inside the project). Starting a name again replaces that terminal. Use service_output to check that it came up.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,39}$"},"command":{"type":"string","minLength":1,"maxLength":4096},"cwd":{"type":"string"}},"required":["name","command"],"additionalProperties":false}`),
		}, run: startService},
		harflexTool{service: s, workspaceID: workspaceID, sessionID: sessionID, risk: security.ReadOnly, spec: agentcore.ToolSpec{
			Name:        "service_output",
			Description: "Read the latest output (up to 16 KB) of a service started with start_service, and whether it is still running.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1}},"required":["name"],"additionalProperties":false}`),
		}, run: serviceOutput},
	}
}

func startService(s *Service, workspaceID, _ string, args json.RawMessage) (any, error) {
	var in struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		CWD     string `json:"cwd"`
	}
	if err := json.Unmarshal(args, &in); err != nil || !serviceName.MatchString(in.Name) || strings.TrimSpace(in.Command) == "" || len(in.Command) > 4096 {
		return nil, errHarflexArguments
	}
	workspace, err := s.knowledgeWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	dir := workspace.Path
	if strings.TrimSpace(in.CWD) != "" {
		guard, err := tools.NewPathGuard(workspace.Path)
		if err != nil {
			return nil, err
		}
		if dir, err = guard.Resolve(in.CWD, false); err != nil {
			return nil, errHarflexArguments
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return nil, errHarflexArguments
		}
	}
	test, err := s.store.ActivePipelineLocalTest(s.ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoLocalTest
	}
	if err != nil {
		return nil, err
	}
	manager := s.terminalManager()
	for _, service := range test.Services {
		if service.Name == in.Name {
			_ = manager.Close(service.TerminalID)
			manager.Forget(service.TerminalID)
		}
	}
	id, err := manager.StartNamed(dir, in.Name, 120, 32)
	if errors.Is(err, terminal.ErrUnsupported) {
		return map[string]any{"started": false, "message": "Terminais não estão disponíveis neste sistema. Entregue à pessoa os comandos para subir cada serviço."}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := manager.Write(id, in.Command+"\n"); err != nil {
		return nil, err
	}
	if err := s.store.AddPipelineLocalTestService(s.ctx, test.ID, catalog.LocalTestService{Name: in.Name, TerminalID: id, Command: in.Command}); err != nil {
		return nil, err
	}
	return map[string]any{"started": true, "name": in.Name, "terminalId": id}, nil
}

func serviceOutput(s *Service, workspaceID, _ string, args json.RawMessage) (any, error) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		return nil, errHarflexArguments
	}
	test, err := s.store.ActivePipelineLocalTest(s.ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNoLocalTest
	}
	if err != nil {
		return nil, err
	}
	manager := s.terminalManager()
	for _, service := range test.Services {
		if service.Name != in.Name {
			continue
		}
		recent, err := manager.Recent(service.TerminalID)
		if err != nil {
			return map[string]any{"name": in.Name, "running": false, "output": "", "message": "Este serviço não está mais aberto (o app foi reiniciado). Suba-o de novo com start_service."}, nil
		}
		text := ansiSequence.ReplaceAllString(string(recent), "")
		if len(text) > serviceOutputBytes {
			text = text[len(text)-serviceOutputBytes:]
		}
		running := false
		for _, info := range manager.List() {
			if info.ID == service.TerminalID {
				running = info.Running
			}
		}
		return map[string]any{"name": in.Name, "running": running, "output": strings.ToValidUTF8(text, "")}, nil
	}
	return nil, errHarflexArguments
}

// ignoredOnlyTool holds the tester's write and edit to files Git ignores (.env, local configuration): the project's
// own files are the Code under test, and undo must find them as the change left them.
func ignoredOnlyTool(inner tools.Tool, root string) tools.Tool {
	return ignoredOnly{Tool: inner, root: root}
}

type ignoredOnly struct {
	tools.Tool
	root string
}

func (t ignoredOnly) Execute(ctx context.Context, args json.RawMessage, sink agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Path) == "" {
		return t.Tool.Execute(ctx, args, sink)
	}
	guard, err := tools.NewPathGuard(t.root)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	full, err := guard.Resolve(in.Path, true)
	if err != nil {
		return t.Tool.Execute(ctx, args, sink)
	}
	root, _ := filepath.EvalSymlinks(t.root)
	relative, err := filepath.Rel(root, full)
	ignored := false
	if err == nil {
		ignored, err = repositories.IsIgnored(ctx, root, filepath.ToSlash(relative))
	}
	if err != nil || !ignored {
		return agentcore.ToolExecutionResult{}, &agentcore.ToolFailure{Code: "local_test_tracked_file", Message: "Neste teste você só pode escrever em arquivos ignorados pelo git, como .env ou configuração local. Não altere arquivos do projeto."}
	}
	return t.Tool.Execute(ctx, args, sink)
}

var localTestBlock = regexp.MustCompile("(?s)```harflex-local[^\\n]*\\n(.*?)```")

type LocalTestURLDTO struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// LocalTestReportDTO is what the tester says at the end: where to open the project and how to stop it.
type LocalTestReportDTO struct {
	URLs  []LocalTestURLDTO `json:"urls"`
	Stop  string            `json:"stop"`
	Notes string            `json:"notes"`
}

// localTestReport reads the tester's last harflex-local block. Only web addresses are kept, up to ten.
func localTestReport(text string) (LocalTestReportDTO, bool) {
	blocks := localTestBlock.FindAllStringSubmatch(text, -1)
	if len(blocks) == 0 {
		return LocalTestReportDTO{}, false
	}
	var raw LocalTestReportDTO
	if json.Unmarshal([]byte(strings.TrimSpace(blocks[len(blocks)-1][1])), &raw) != nil {
		return LocalTestReportDTO{}, false
	}
	report := LocalTestReportDTO{URLs: []LocalTestURLDTO{}, Stop: clipText(raw.Stop, 2000), Notes: clipText(raw.Notes, 4000)}
	for _, item := range raw.URLs {
		parsed, err := url.Parse(strings.TrimSpace(item.URL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || len(report.URLs) == 10 {
			continue
		}
		report.URLs = append(report.URLs, LocalTestURLDTO{Label: clipText(strings.TrimSpace(item.Label), 120), URL: parsed.String()})
	}
	return report, true
}

// localTestPrompt is the tester's first message: what the person wants to try and how to hand the project over.
func localTestPrompt(run catalog.PipelineRun, workspacePath string) string {
	var prompt strings.Builder
	prompt.WriteString("Prepare este projeto para a pessoa testar manualmente, na própria pasta dela, a mudança que o Code fez. As mudanças do Code já estão aplicadas na pasta do projeto.\n\n")
	fmt.Fprintf(&prompt, "Pasta do projeto: %s\nTítulo do trabalho: %s\nObjetivo: %s\n", workspacePath, strings.TrimSpace(run.Title), clipText(run.Objective, 2000))
	if plan := run.Artifacts[sdd.Plan].Content; strings.TrimSpace(plan) != "" {
		fmt.Fprintf(&prompt, "\nPlano aprovado:\n%s\n", clipText(plan, 4000))
	}
	prompt.WriteString(`
O que fazer:
1. Leia o README, Makefile, package.json, docker-compose e afins para saber como o projeto roda localmente.
2. Instale as dependências e prepare o que faltar: um .env a partir dos exemplos (.env.example), migrações, dados de exemplo.
3. Suba cada processo que fica rodando (servidor web, API, worker, banco) com a ferramenta start_service, um nome por serviço, e confira com service_output que ele subiu. Não rode esses processos com bash: bash é para comandos que terminam.
4. Se algo falhar, leia o erro, corrija o ambiente e tente de novo.

Regras:
- Nunca faça commit, push, nem mude de branch.
- Não altere arquivos do projeto que o git acompanha: eles são a mudança em teste. Você só pode escrever arquivos ignorados pelo git (.env, configuração local).
- Se precisar de algo que só a pessoa tem (uma chave, um login), pergunte.

Ao terminar, responda com um resumo curto e este bloco, com os endereços para abrir e como parar:
` + "```harflex-local\n{\"urls\":[{\"label\":\"App\",\"url\":\"http://localhost:3000\"}],\"stop\":\"Como parar os serviços\",\"notes\":\"Logins de teste e o que conferir\"}\n```\n")
	return prompt.String()
}
