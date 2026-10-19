package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/repositories"
	"github.com/persioflexa/harflex/internal/sdd"
)

// publisherRole names the conversation in which the agent opens a pipeline's pull requests.
const publisherRole = "publisher"

// pullRequestOutputTokens bounds one answer of the pull request conversation when it is bound to a catalog model.
const pullRequestOutputTokens = 16384

var ErrPipelineCodeNotApplied = errors.New("the approved Code patch has not been applied to the project yet")
var ErrPipelinePRsBackendUnsupported = errors.New("pull requests need an API backend, or a CLI that continues its conversation and reaches Harflex tools over MCP (Claude Code)")

type FinishPipelinePRsInput struct {
	PipelineID string `json:"pipelineId"`
	// Outcome is "completed" (the agent reported its pull requests) or "skipped".
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// pipelinePRsOpen is the application-side twin of the store's check: QA approved and the stage not yet ended.
func pipelinePRsOpen(run catalog.PipelineRun) bool {
	if run.Status[sdd.Eval] != sdd.Completed {
		return false
	}
	if run.Current == sdd.PRs {
		return run.Status[sdd.PRs] == sdd.Active
	}
	return run.Current == "" && (run.Status[sdd.PRs] == "" || run.Status[sdd.PRs] == sdd.Pending)
}

func clipText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text + "\n[…]"
}

// prRepository is one repository the work changed: its folder ("." for the project's own) and the branch the copy was
// made from, which the pull request starts from.
type prRepository struct {
	Path   string
	Branch string
}

var evidenceFileLine = regexp.MustCompile(`(?m)^# file=("(?:[^"\\]|\\.)*")`)

// pipelinePRRepositories lists the repositories the Code evidence changed, when the copy had nested repositories. A work
// that changed only the project's own repository gets none, and the prompt keeps its single repository steps.
func pipelinePRRepositories(code string, snapshot repositories.ExecutionSnapshot) []prRepository {
	if len(snapshot.Repositories) == 0 {
		return nil
	}
	var paths []string
	for _, match := range evidenceFileLine.FindAllStringSubmatch(code, -1) {
		if path, err := strconv.Unquote(match[1]); err == nil {
			paths = append(paths, path)
		}
	}
	var result []prRepository
	root := false
	for _, path := range paths {
		if _, nested := snapshot.RepositoryFor(path); !nested {
			root = true
		}
	}
	if root {
		result = append(result, prRepository{Path: ".", Branch: snapshot.SourceBranch})
	}
	for _, repository := range snapshot.ChangedRepositories(paths) {
		result = append(result, prRepository{Path: repository.Path, Branch: repository.Branch})
	}
	if len(result) == 1 && result[0].Path == "." {
		return nil
	}
	return result
}

// pipelinePRPrompt is what the agent is asked to do. It carries the approved plan and the QA criteria for the
// description, and spells out the limits: no force push, no merge, nothing outside this work.
func pipelinePRPrompt(run catalog.PipelineRun, repos []prRepository) string {
	var prompt strings.Builder
	prompt.WriteString("Abra o pull request deste trabalho. As mudanças aprovadas já estão aplicadas na pasta do projeto.\n\n")
	fmt.Fprintf(&prompt, "Título do trabalho: %s\nObjetivo: %s\n", strings.TrimSpace(run.Title), clipText(run.Objective, 2000))
	if plan := run.Artifacts[sdd.Plan].Content; strings.TrimSpace(plan) != "" {
		fmt.Fprintf(&prompt, "\nPlano aprovado:\n%s\n", clipText(plan, 6000))
	}
	var verdict struct {
		Criteria []pipelineEvaluationCriterion `json:"criteria"`
	}
	if json.Unmarshal([]byte(run.Artifacts[sdd.Eval].Content), &verdict) == nil && len(verdict.Criteria) > 0 {
		prompt.WriteString("\nCritérios que o QA conferiu:\n")
		for index, item := range verdict.Criteria {
			if index >= 20 {
				break
			}
			fmt.Fprintf(&prompt, "- %s\n", clipText(item.Criterion, 300))
		}
	}
	prompt.WriteString(`
Faça nesta ordem:
1. Veja o estado do repositório com git: status, diff, branch atual e remotos. Se a pasta não for um repositório Git ou não houver remoto, pare e explique.
2. Crie uma branch chamada harflex/<resumo-curto-em-kebab-case> a partir da branch atual. Se o nome já existir, acrescente um sufixo curto.
3. Faça commit somente das mudanças deste trabalho, com mensagem curta no imperativo (por exemplo "feat: export invoices as CSV"). Não inclua arquivos que não pertençam ao trabalho nem segredos.
4. Envie a branch ao remoto com git push -u. Se o push falhar por falta de credencial ou de permissão, diga isso e pare.
5. Abra o pull request pelas ferramentas MCP do GitHub ou do Bitbucket que você tiver, escolhendo o servidor pelo remoto origin. Se nenhuma ferramenta MCP estiver disponível, use a CLI da plataforma (como gh), se estiver instalada e autenticada. Título curto. Descrição com o que mudou, como foi verificado (os critérios do QA acima), riscos e pontos de atenção. Se não houver ferramenta MCP para o remoto, entregue o link que o git mostrou ao enviar a branch e diga que o PR precisa ser aberto por lá.
6. Se o trabalho exigir mais de um pull request, abra um de cada vez.

Regras: não use push forçado, não altere nem apague a branch principal, não apague branches, não faça merge e não aprove o próprio pull request.

Logo depois de abrir cada pull request, chame harflex_register_pull_request com a URL, o título e a branch, para a pessoa ver o PR na tela de PRs e poder ligar a vigia da revisão.
Termine com um relatório curto em Markdown: cada pull request com link, branch, base e uma frase de resumo. Se algo não deu certo, diga o que e por quê.
`)
	if len(repos) > 0 {
		prompt.WriteString("\nEste trabalho mudou mais de um repositório Git: as pastas abaixo são repositórios próprios dentro do projeto. Abra um pull request para cada um:\n")
		for _, repository := range repos {
			if repository.Path == "." {
				fmt.Fprintf(&prompt, "- `.` (o repositório do projeto), a partir da branch %s\n", repository.Branch)
			} else {
				fmt.Fprintf(&prompt, "- `%s`, a partir da branch %s\n", repository.Path, repository.Branch)
			}
		}
		prompt.WriteString(`Para cada repositório, faça os passos 1 a 5 dentro da pasta dele (cd na pasta, ou git -C pasta): a branch nova sai da branch indicada acima, o commit leva só as mudanças daquela pasta, e o push e o pull request vão para o remoto origin daquele repositório. Nunca faça commit de uma pasta de outro repositório no repositório do projeto.
Ao registrar cada pull request com harflex_register_pull_request, informe também repository com a pasta do repositório ("." para o do projeto).
`)
	}
	return prompt.String()
}

// createPipelinePRSession opens the conversation in which the agent publishes the pipeline. It is an ordinary chat
// in the project folder, with shell, file and MCP tools, linked to the pipeline so the bar and the page can follow it.
func (s *Service) createPipelinePRSession(in CreatePipelineSessionInput) (PipelineSessionDTO, error) {
	if strings.TrimSpace(in.BackendID) == "" {
		return PipelineSessionDTO{}, ErrPipelinePRsBackendUnsupported
	}
	// An API backend runs the conversation in the Harflex tool loop. A CLI runs it with its own terminal and files and
	// reaches Harflex's tools over MCP; the review watch writes to the same conversation again and again, so the CLI
	// has to continue it (Claude Code does; Codex takes one message per conversation).
	adapter, external := s.external[in.BackendID]
	if external && (!documentCLI(in.BackendID) || !adapter.Capabilities().Resumable || !adapter.Detect().Available) {
		return PipelineSessionDTO{}, ErrPipelinePRsBackendUnsupported
	}
	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	if !pipelinePRsOpen(run) {
		return PipelineSessionDTO{}, sdd.ErrInvalidTransition
	}
	// The agent works on the project folder, so the approved patch has to be there before it starts.
	var repos []prRepository
	if link, err := s.latestPipelineSession(run.ID, "coder"); err == nil {
		if link.ExecutionAppliedAt.IsZero() && len(link.ExecutionSnapshot) > 0 {
			return PipelineSessionDTO{}, ErrPipelineCodeNotApplied
		}
		if snapshot, err := s.executionSnapshotForLink(link); err == nil {
			repos = pipelinePRRepositories(run.Artifacts[sdd.Code].Content, snapshot)
		}
	} else if !errors.Is(err, sdd.ErrEvidenceRequired) {
		return PipelineSessionDTO{}, err
	}
	prompt := pipelinePRPrompt(run, repos)
	if len(prompt) > maxPromptBytes {
		return PipelineSessionDTO{}, ErrHistoryTooLarge
	}
	modelSelection, err := s.pipelineChatModelSelection(run, in, external)
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	// The conversation is composed before it is linked, so its pull request tools come from this hint.
	s.prWatch.mu.Lock()
	if s.prWatch.creating == nil {
		s.prWatch.creating = map[string]string{}
	}
	s.prWatch.creating[run.WorkspaceID] = run.ID
	s.prWatch.mu.Unlock()
	defer func() {
		s.prWatch.mu.Lock()
		delete(s.prWatch.creating, run.WorkspaceID)
		s.prWatch.mu.Unlock()
	}()
	session, err := s.createSessionWithExecutionRoot(CreateSessionInput{WorkspaceID: run.WorkspaceID, BackendID: in.BackendID}, nil, modelSelection, "")
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	if err := s.store.LinkPipelinePRSession(s.ctx, catalog.PipelinePRSession{ID: id.New(), PipelineID: run.ID, SessionID: session.ID, CreatedAt: time.Now().UTC()}); err != nil {
		return PipelineSessionDTO{}, safe("link pull request session", err)
	}
	return PipelineSessionDTO{Session: session, Prompt: prompt, Role: publisherRole}, nil
}

// pipelinePRReport reads how a pull request conversation ended: whether its last run completed, and its final answer.
func (s *Service) pipelinePRReport(sessionID string) (completed bool, report string, err error) {
	// The conversation may run on an API profile or on a CLI, whose runs and replies are journaled as external events.
	state, text, err := s.coderTurn(sessionID)
	return state == "completed", text, err
}

// FinishPipelinePRs ends the last stage of the pipeline. Completing it needs the agent's finished report; skipping
// is the person's decision and needs nothing else.
func (s *Service) FinishPipelinePRs(in FinishPipelinePRsInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	in.Reason = strings.TrimSpace(in.Reason)
	if len(in.Reason) > 1000 || !utf8.ValidString(in.Reason) || (in.Outcome != string(sdd.Completed) && in.Outcome != string(sdd.Skipped)) {
		return PipelineDTO{}, ErrInvalidInput
	}
	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	if !pipelinePRsOpen(run) {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	finish := catalog.PipelinePRsFinish{PipelineID: run.ID, ExpectedRevision: run.Revision, Outcome: in.Outcome, Reason: in.Reason}
	if in.Outcome == string(sdd.Completed) {
		links, err := s.store.ListPipelinePRSessions(s.ctx, run.ID)
		if err != nil {
			return PipelineDTO{}, safe("read pull request sessions", err)
		}
		if len(links) == 0 {
			return PipelineDTO{}, sdd.ErrEvidenceRequired
		}
		latest := links[len(links)-1]
		completed, report, err := s.pipelinePRReport(latest.SessionID)
		if err != nil {
			return PipelineDTO{}, safe("read pull request evidence", err)
		}
		if !completed || strings.TrimSpace(report) == "" {
			return PipelineDTO{}, sdd.ErrEvidenceRequired
		}
		finish.SessionID, finish.Report = latest.SessionID, report
	}
	if err := s.store.FinishPipelinePRs(s.ctx, finish); err != nil {
		return PipelineDTO{}, safe("finish pull request stage", err)
	}
	if in.Outcome == string(sdd.Completed) {
		// The PRs were accepted: the work folder goes, the conversations stay as history.
		if err := s.prunePipelineCopies(s.ctx, run.ID, true); err != nil {
			s.diagnostics.write(map[string]any{"event": "copy.prune_failed", "pipelineId": run.ID, "detail": diagnosticText(err)})
		}
	}
	updated, err := s.loadPipeline(run.ID)
	if err != nil {
		return PipelineDTO{}, err
	}
	return s.pipelineDTOWithCodeApplyStatus(updated)
}

// pipelineChatModelSelection is the model of a pipeline conversation that works like a chat in the project's folder
// (the PRs agent, the local test): the profile's own, unless the person picked another one from the catalog. The
// pick is bound to the conversation, which keeps it however long the person's approvals take.
func (s *Service) pipelineChatModelSelection(run catalog.PipelineRun, in CreatePipelineSessionInput, external bool) (*catalog.ModelSelection, error) {
	if in.Selection == nil {
		return nil, nil
	}
	if external {
		// The CLI's own model, confirmed against its catalog like in any chat.
		if in.Selection.Executor != "cli" || in.Selection.BackendID != in.BackendID {
			return nil, ErrInvalidInput
		}
		workspace, err := s.store.GetWorkspace(s.ctx, run.WorkspaceID)
		if err != nil {
			return nil, ErrWorkspaceNotFound
		}
		return s.prepareCLIModelSelectionContext(s.ctx, CreateSessionInput{BackendID: in.BackendID, ModelID: in.Selection.ModelID, ReasoningEffort: in.Selection.ReasoningEffort, CatalogRevision: in.Selection.CatalogRevision}, workspace)
	}
	if in.Selection.Executor != "api" {
		return nil, ErrInvalidInput
	}
	modelSelection, err := s.preparePipelineRoleModelSelection(s.ctx, run.WorkspaceID, in.BackendID, *in.Selection)
	if err != nil {
		return nil, err
	}
	// Code and QA answer in bounded turns; this is a conversation with a terminal, where a reasoning model spends part
	// of the cap thinking and a cut tool call becomes an invalid one, so it gets the room an unbound chat has.
	modelSelection.MaxOutputTokens = pullRequestOutputTokens
	return modelSelection, nil
}
