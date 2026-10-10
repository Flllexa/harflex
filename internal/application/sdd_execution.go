package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/repositories"
	"github.com/persioflexa/harflex/internal/sdd"
)

var ErrUntrackedEvidenceRequiresStage = errors.New("untracked files require git stage for SDD evidence")
var ErrPipelineGitRequired = errors.New("pipeline CLI code requires a Git repository for evidence")
var ErrPipelineGitDirty = errors.New("pipeline CLI code requires a clean Git baseline for attributable evidence")
var ErrPipelineGitBaselineUnavailable = errors.New("pipeline code Git baseline could not be captured completely")
var ErrPipelineCodeSnapshotUnavailable = errors.New("pipeline Code execution snapshot is unavailable")
var ErrPipelineCodeSessionClosed = errors.New("pipeline Code session is no longer active")
var ErrPipelineEvaluationCriteriaChanged = errors.New("pipeline evaluation criteria changed after admission")

const maxPipelineGitBaselineUntrackedBytes = 64 * 1024 * 1024

type pipelineEvaluationCriterion struct {
	Criterion string `json:"criterion"`
	Evidence  string `json:"evidence"`
}

type pipelineEvaluationArtifact struct {
	Passed bool `json:"passed"`
	// Checks the evaluator ran in the QA lab; older evaluations have none.
	Checks         []pipelineQACheck                          `json:"checks,omitempty"`
	Findings       []string                                   `json:"findings"`
	Improvements   []string                                   `json:"improvements,omitempty"`
	Criteria       []pipelineEvaluationCriterion              `json:"criteria"`
	CriteriaSource catalog.PipelineEvaluationCriteriaSnapshot `json:"criteriaSource"`
}

func authoringPlanAllowsCode(run catalog.PipelineRun) bool {
	return run.Kind != "ai_authoring" || run.Status[sdd.Plan] == sdd.Completed && run.Artifacts[sdd.Plan].Content != ""
}

func authoringCodeAllowsEvaluation(run catalog.PipelineRun) bool {
	return authoringPlanAllowsCode(run) && run.Status[sdd.Code] == sdd.Completed && run.Artifacts[sdd.Code].Content != "" && pipelineArtifactApproved(run, sdd.Code)
}

// pipelineCLIModel is the model Settings saved for a CLI that works as a model only, confirmed in its catalog.
func (s *Service) pipelineCLIModel(workspaceID, backendID string) (string, string, error) {
	settings, err := s.store.GetSettings(s.ctx)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (settings.DefaultModelBackendID != backendID || settings.DefaultModelID == "") {
		return "", "", nil
	}
	if err != nil {
		return "", "", safe("read pipeline model preference", err)
	}
	result, err := s.QueryCLIModelCatalog(s.ctx, CLIModelCatalogQuery{WorkspaceID: workspaceID, BackendID: backendID})
	if err != nil {
		return "", "", err
	}
	if !result.Complete || result.Status != "complete" || result.ProfileRevision == "" {
		return "", "", ErrBackendChanged
	}
	for _, model := range result.Models {
		if model.BackendID == backendID && model.ID == settings.DefaultModelID && model.Source == result.Source && model.Availability != "unavailable" {
			return model.ID, result.ProfileRevision, nil
		}
	}
	return "", "", ErrBackendChanged
}

func (s *Service) GetPipelineForSession(in GetPipelineForSessionInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	if in.SessionID == "" || in.WorkspaceID == "" {
		return PipelineDTO{}, ErrInvalidInput
	}
	session, err := s.store.GetSession(s.ctx, in.SessionID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && session.WorkspaceID != in.WorkspaceID) {
		return PipelineDTO{}, ErrSessionNotFound
	}
	if err != nil {
		return PipelineDTO{}, safe("get linked session", err)
	}
	pipelineID, err := s.store.GetPipelineIDForSession(s.ctx, in.SessionID)
	if errors.Is(err, sql.ErrNoRows) {
		// The conversation of the pull request stage is linked on its own.
		pipelineID, err = s.store.GetPipelineIDForPRSession(s.ctx, in.SessionID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		// A chat that coordinates the work (the one that created it, or the one opened for it).
		pipelineID, err = s.store.GetPipelineIDForCoordinator(s.ctx, in.SessionID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return PipelineDTO{}, ErrPipelineNotFound
	}
	if err != nil {
		return PipelineDTO{}, safe("get linked pipeline", err)
	}
	run, err := s.loadPipeline(pipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	if run.WorkspaceID != in.WorkspaceID {
		return PipelineDTO{}, ErrPipelineNotFound
	}
	return s.pipelineDTOWithCodeApplyStatus(run)
}

func gitFingerprint(snapshot repositories.Snapshot) string {
	if !snapshot.IsRepository || snapshot.Truncated {
		return ""
	}
	data, _ := json.Marshal([]any{snapshot.Files, snapshot.StagedDiff, snapshot.UnstagedDiff})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func pipelineGitFileHashes(ctx context.Context, workspace string, files []string) (map[string]string, error) {
	hashes := make(map[string]string, len(files))
	untrackedBytes := int64(0)
	for _, file := range files {
		path := repositories.StatusPath(file)
		if path == "" {
			continue
		}
		if repositories.IsUntrackedStatus(file) {
			hash, size, err := repositories.HashUntrackedPath(ctx, workspace, path, maxPipelineGitBaselineUntrackedBytes-untrackedBytes)
			if err != nil {
				return nil, err
			}
			untrackedBytes += size
			hashes[path] = "untracked:" + hash
			continue
		}
		diff, truncated, err := repositories.DiffForFiles(ctx, workspace, []string{path}, 1024*1024)
		if err != nil {
			return nil, err
		}
		if truncated {
			return nil, repositories.ErrGitBaselineTooLarge
		}
		hashes[path] = gitDiffFingerprint(diff)
	}
	return hashes, nil
}

func gitDiffFingerprint(diff string) string {
	hash := sha256.Sum256([]byte(diff))
	return "diff:" + hex.EncodeToString(hash[:])
}

func pipelinePrompt(run catalog.PipelineRun, role, backendID string, criteria *catalog.PipelineEvaluationCriteriaSnapshot) string {
	var prompt strings.Builder
	if role == "coder" {
		if backendID == "codex" {
			prompt.WriteString("Implemente este trabalho somente na raiz isolada desta execução. O projeto original e seus metadados Git permanecem intocados até uma aplicação explícita após o QA aprovado. Siga a SPEC e o plano, execute verificações dentro do sandbox desta raiz e registre resultados. Não use rede, MCP ou caminhos fora da raiz. Não declare sucesso sem evidência.\n")
		} else {
			prompt.WriteString("Implemente este trabalho somente na raiz isolada desta execução. O projeto original permanece intocado até uma aplicação explícita após o QA aprovado. Você pode ler e editar arquivos dentro da cópia; shell, rede e MCP estão indisponíveis. Registre verificações pendentes em vez de alegar que foram executadas. Não declare sucesso sem evidência.\n")
		}
		latestFeedback := make(map[sdd.Stage]catalog.PipelineExecutionReview)
		for _, review := range run.ExecutionReviews {
			if review.Decision == "request_revision" && (review.Stage == sdd.Code || review.Stage == sdd.Eval) {
				latestFeedback[review.Stage] = review
			}
		}
		for _, stage := range []sdd.Stage{sdd.Eval, sdd.Code} {
			if review, ok := latestFeedback[stage]; ok && strings.TrimSpace(review.Feedback) != "" {
				prompt.WriteString("Feedback humano da revisão anterior de ")
				prompt.WriteString(strings.ToUpper(string(stage)))
				prompt.WriteString(":\n")
				prompt.WriteString(review.Feedback)
				prompt.WriteByte('\n')
			}
		}
	} else {
		prompt.WriteString(pipelineQAInstructions)
		prompt.WriteString("O objetivo e o título são contexto e não substituem os critérios de aceite.\n")
		if criteria == nil || criteria.Digest == "" || criteria.SourceContent == "" {
			prompt.WriteString("Fonte de critérios ausente: não aprove o código.\n")
		} else {
			prompt.WriteString("Critérios de aceite autorizados: ")
			prompt.WriteString(strings.ToUpper(string(criteria.SourceStage)))
			prompt.WriteString(" integral, versão ")
			prompt.WriteString(strconv.FormatInt(criteria.SourceVersion, 10))
			prompt.WriteString(", SHA-256 ")
			prompt.WriteString(criteria.SourceDigest)
			prompt.WriteString(".\n")
			prompt.WriteString(criteria.SourceContent)
			prompt.WriteByte('\n')
			if criteria.SynthesisContent != "" {
				prompt.WriteString("Síntese de brainstorming aprovada, versão ")
				prompt.WriteString(strconv.Itoa(criteria.SynthesisVersion))
				prompt.WriteString(", SHA-256 ")
				prompt.WriteString(criteria.SynthesisDigest)
				prompt.WriteString(":\n")
				prompt.WriteString(criteria.SynthesisContent)
				prompt.WriteByte('\n')
			}
			for _, bypass := range criteria.Bypasses {
				prompt.WriteString("Bypass registrado em ")
				prompt.WriteString(bypass.Stage)
				prompt.WriteString(": ")
				prompt.WriteString(bypass.Reason)
				prompt.WriteByte('\n')
			}
		}
		if run.Objective != "" {
			prompt.WriteString("Contexto do trabalho (não é critério de aceite): ")
			prompt.WriteString(run.Objective)
			prompt.WriteByte('\n')
		}
		if plan := run.Artifacts[sdd.Plan]; plan.Content != "" {
			prompt.WriteString("Plano de implementação aprovado (contexto, não substitui critérios):\n")
			prompt.WriteString(plan.Content)
			prompt.WriteByte('\n')
		}
		prompt.WriteString("Mudanças observadas:\n")
		prompt.WriteString(run.Artifacts[sdd.Code].Content)
		return prompt.String()
	}
	prompt.WriteString("Objetivo: ")
	prompt.WriteString(run.Objective)
	prompt.WriteByte('\n')
	for _, stage := range []sdd.Stage{sdd.Discovery, sdd.Spec, sdd.Plan} {
		if artifact := run.Artifacts[stage]; artifact.Content != "" {
			prompt.WriteString(strings.ToUpper(string(stage)))
			prompt.WriteString(":\n")
			prompt.WriteString(artifact.Content)
			prompt.WriteByte('\n')
		}
	}
	if role == "evaluator" {
		prompt.WriteString("Mudanças observadas:\n")
		prompt.WriteString(run.Artifacts[sdd.Code].Content)
	}
	return prompt.String()
}

func (s *Service) PreviewPipelineCodeWorkspace(workspaceID string) (PipelineCodeCopyPreviewDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineCodeCopyPreviewDTO{}, err
	}
	defer s.endCall()
	if strings.TrimSpace(workspaceID) == "" {
		return PipelineCodeCopyPreviewDTO{}, ErrInvalidInput
	}
	workspace, err := s.store.GetWorkspace(s.ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return PipelineCodeCopyPreviewDTO{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return PipelineCodeCopyPreviewDTO{}, safe("get Code workspace preview", err)
	}
	preview, err := repositories.PreviewExecutionSnapshot(s.ctx, workspace.Path)
	if errors.Is(err, repositories.ErrExecutionSourceDirty) {
		return PipelineCodeCopyPreviewDTO{}, ErrPipelineGitDirty
	}
	if err != nil {
		return PipelineCodeCopyPreviewDTO{}, err
	}
	return PipelineCodeCopyPreviewDTO{IsGit: preview.IsGit, FileCount: preview.FileCount, TotalBytes: preview.TotalBytes,
		ExcludedPaths: append([]string{}, preview.ExcludedPaths...), UnsafePaths: append([]string{}, preview.UnsafePaths...)}, nil
}

func (s *Service) CreatePipelineSession(in CreatePipelineSessionInput) (PipelineSessionDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineSessionDTO{}, err
	}
	defer s.endCall()
	if in.Role == publisherRole {
		return s.createPipelinePRSession(in)
	}
	if in.Role != "coder" && in.Role != "evaluator" {
		return PipelineSessionDTO{}, ErrInvalidInput
	}
	if in.Role == "coder" {
		if _, external := s.external[in.BackendID]; external && !s.codexHostModeSupported(in.BackendID, "sdd_code") {
			return PipelineSessionDTO{}, ErrSDDCLIReadIsolationUnavailable
		}
	}
	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	if (in.Role == "coder" && (run.Current != sdd.Code || run.Status[sdd.Code] != sdd.Active)) ||
		(in.Role == "evaluator" && (run.Current != sdd.Eval || run.Status[sdd.Eval] != sdd.Active)) {
		return PipelineSessionDTO{}, sdd.ErrInvalidTransition
	}
	if (in.Role == "coder" && !authoringPlanAllowsCode(run)) || (in.Role == "evaluator" && !authoringCodeAllowsEvaluation(run)) {
		return PipelineSessionDTO{}, sdd.ErrInvalidTransition
	}
	if in.Role == "evaluator" {
		if _, external := s.external[in.BackendID]; external && !s.codexHostModeSupported(in.BackendID, "evaluation") {
			return PipelineSessionDTO{}, ErrSDDCLIReadIsolationUnavailable
		}
		if run.Artifacts[sdd.Code].Content == "" {
			return PipelineSessionDTO{}, sdd.ErrEvidenceRequired
		}
	}
	var evaluationCriteria *catalog.PipelineEvaluationCriteriaSnapshot
	var encodedEvaluationCriteria json.RawMessage
	if in.Role == "evaluator" {
		criteria, err := s.pipelineEvaluationCriteria(run)
		if err != nil {
			return PipelineSessionDTO{}, err
		}
		evaluationCriteria = &criteria
		encodedEvaluationCriteria, err = json.Marshal(criteria)
		if err != nil {
			return PipelineSessionDTO{}, ErrPipelineEvaluationCriteriaChanged
		}
	}
	baselineGitHash := ""
	baselineGitFiles := []string(nil)
	baselineGitFileHashes := map[string]string{}
	var executionSnapshot repositories.ExecutionSnapshot
	var encodedExecutionSnapshot json.RawMessage
	executionRoot := ""
	removeExecutionRoot := false
	defer func() {
		if removeExecutionRoot && executionRoot != "" {
			_ = os.RemoveAll(executionRoot)
		}
	}()
	linkID := id.New()
	if in.Role == "coder" && in.ContinuePreviousCopy {
		// A fix round goes on in the same private copy, so the Code evidence keeps every earlier change.
		previous, err := s.latestPipelineSession(run.ID, "coder")
		if err != nil {
			return PipelineSessionDTO{}, err
		}
		executionSnapshot, err = s.executionSnapshotForLink(previous)
		if err != nil {
			return PipelineSessionDTO{}, err
		}
		executionRoot = executionSnapshot.Root
		encodedExecutionSnapshot = append(json.RawMessage(nil), previous.ExecutionSnapshot...)
		baselineGitHash = executionSnapshot.SourceHead
		for _, entry := range executionSnapshot.Manifest {
			baselineGitFiles = append(baselineGitFiles, entry.Path)
			baselineGitFileHashes[entry.Path] = entry.Hash
		}
	} else if in.Role == "coder" {
		workspace, err := s.store.GetWorkspace(s.ctx, run.WorkspaceID)
		if err != nil {
			return PipelineSessionDTO{}, safe("get coder workspace", err)
		}
		if s.executionCacheRoot == "" {
			executionSnapshot, err = repositories.PrepareExecutionSnapshot(s.ctx, workspace.Path, in.ConfirmWorkspaceCopy)
		} else {
			executionSnapshot, err = repositories.PrepareExecutionSnapshotAt(s.ctx, workspace.Path, in.ConfirmWorkspaceCopy, s.executionCacheRoot)
		}
		if errors.Is(err, repositories.ErrExecutionSourceDirty) {
			return PipelineSessionDTO{}, ErrPipelineGitDirty
		}
		if err != nil {
			return PipelineSessionDTO{}, err
		}
		executionRoot = executionSnapshot.Root
		removeExecutionRoot = true
		encodedExecutionSnapshot, err = json.Marshal(executionSnapshot)
		if err != nil {
			return PipelineSessionDTO{}, ErrPipelineCodeSnapshotUnavailable
		}
		baselineGitHash = executionSnapshot.SourceHead
		for _, entry := range executionSnapshot.Manifest {
			baselineGitFiles = append(baselineGitFiles, entry.Path)
			baselineGitFileHashes[entry.Path] = entry.Hash
		}
	} else {
		coderLink, err := s.latestPipelineSession(run.ID, "coder")
		if err != nil {
			return PipelineSessionDTO{}, err
		}
		executionSnapshot, err = s.executionSnapshotForLink(coderLink)
		if err != nil {
			return PipelineSessionDTO{}, err
		}
		executionRoot = executionSnapshot.Root
		encodedExecutionSnapshot = append(json.RawMessage(nil), coderLink.ExecutionSnapshot...)
		executionEvidence, _, err := pipelineCodeEvidence(s.ctx, executionSnapshot, 1024*1024)
		if err != nil {
			return PipelineSessionDTO{}, err
		}
		if executionEvidence == "" || executionEvidence != run.Artifacts[sdd.Code].Content {
			return PipelineSessionDTO{}, repositories.ErrExecutionSourceDrift
		}
		// QA installs, builds and tests in a copy of its own, so nothing it does reaches the Code patch.
		labRoot := qaLabRoot(executionSnapshot.Root, linkID)
		if err := copyQALab(executionSnapshot.Root, labRoot); err != nil {
			_ = os.RemoveAll(labRoot)
			return PipelineSessionDTO{}, safe("prepare QA lab", err)
		}
		executionRoot = labRoot
		removeExecutionRoot = true
	}
	prompt := pipelinePrompt(run, in.Role, in.BackendID, evaluationCriteria)
	if len(prompt) > maxPromptBytes {
		return PipelineSessionDTO{}, ErrHistoryTooLarge
	}
	mode := ""
	if in.Role == "coder" {
		mode = "sdd_code"
	}
	if in.Role == "evaluator" {
		mode = "evaluation"
	}
	var modelSelection *catalog.ModelSelection
	if in.Selection != nil {
		modelSelection, err = s.preparePipelineRoleModelSelection(s.ctx, run.WorkspaceID, in.BackendID, *in.Selection)
		if err != nil {
			return PipelineSessionDTO{}, err
		}
	}
	create := CreateSessionInput{WorkspaceID: run.WorkspaceID, BackendID: in.BackendID, Mode: mode}
	if documentCLI(in.BackendID) && modelSelection == nil {
		create.ModelID, create.CatalogRevision, err = s.pipelineCLIModel(run.WorkspaceID, in.BackendID)
		if err != nil {
			return PipelineSessionDTO{}, err
		}
	}
	session, err := s.createSessionWithExecutionRoot(create, nil, modelSelection, executionRoot)
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	link := catalog.PipelineSession{ID: linkID, PipelineID: run.ID, SessionID: session.ID, Role: in.Role, EvaluationCriteria: encodedEvaluationCriteria, CreatedAt: time.Now().UTC()}
	link.BaselineGitHash = baselineGitHash
	link.BaselineGitFiles = baselineGitFiles
	link.BaselineGitFileHashes = baselineGitFileHashes
	link.ExecutionSnapshot = encodedExecutionSnapshot
	if err := s.store.LinkPipelineSession(s.ctx, link); err != nil {
		return PipelineSessionDTO{}, safe("link pipeline session", err)
	}
	removeExecutionRoot = false
	return PipelineSessionDTO{Session: session, Prompt: prompt, Role: in.Role}, nil
}

func (s *Service) latestPipelineSession(pipelineID, role string) (catalog.PipelineSession, error) {
	links, err := s.store.ListPipelineSessions(s.ctx, pipelineID, role)
	if err != nil {
		return catalog.PipelineSession{}, safe("list pipeline sessions", err)
	}
	if len(links) == 0 {
		return catalog.PipelineSession{}, sdd.ErrEvidenceRequired
	}
	return links[0], nil
}

func (s *Service) executionSnapshotForLink(link catalog.PipelineSession) (repositories.ExecutionSnapshot, error) {
	if len(link.ExecutionSnapshot) == 0 || !json.Valid(link.ExecutionSnapshot) {
		return repositories.ExecutionSnapshot{}, ErrPipelineCodeSnapshotUnavailable
	}
	var snapshot repositories.ExecutionSnapshot
	if err := json.Unmarshal(link.ExecutionSnapshot, &snapshot); err != nil || snapshot.Root == "" || snapshot.SourceRoot == "" {
		return repositories.ExecutionSnapshot{}, ErrPipelineCodeSnapshotUnavailable
	}
	return snapshot, nil
}

func (s *Service) executionRootForSession(sessionID string) (string, error) {
	pipelineID, err := s.store.GetPipelineIDForSession(s.ctx, sessionID)
	if err != nil {
		return "", err
	}
	for _, role := range []string{"coder", "evaluator"} {
		links, err := s.store.ListPipelineSessions(s.ctx, pipelineID, role)
		if err != nil {
			return "", safe("read pipeline execution root", err)
		}
		for _, link := range links {
			if link.SessionID != sessionID {
				continue
			}
			snapshot, err := s.executionSnapshotForLink(link)
			if err != nil {
				return "", err
			}
			run, err := s.store.GetPipeline(s.ctx, pipelineID)
			if err != nil {
				return "", safe("read pipeline execution", err)
			}
			workspace, err := s.store.GetWorkspace(s.ctx, run.WorkspaceID)
			if err != nil {
				return "", safe("read pipeline execution workspace", err)
			}
			sourceRoot, sourceErr := filepath.EvalSymlinks(workspace.Path)
			storedRoot, rootErr := filepath.EvalSymlinks(snapshot.Root)
			rootInfo, statErr := os.Lstat(snapshot.Root)
			if sourceErr != nil || rootErr != nil || statErr != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || rootInfo.Mode().Perm() != 0o700 || sourceRoot != snapshot.SourceRoot || storedRoot != snapshot.Root || !separateExecutionRoot(sourceRoot, storedRoot) {
				return "", ErrPipelineCodeSnapshotUnavailable
			}
			if role == "evaluator" {
				// Evaluators since the QA lab work in their own copy; older ones read the Coder's copy.
				lab := qaLabRoot(snapshot.Root, link.ID)
				if info, err := os.Lstat(lab); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm() == 0o700 {
					return lab, nil
				}
			}
			return snapshot.Root, nil
		}
	}
	return "", sql.ErrNoRows
}

func (s *Service) pipelineCodeSessionActive(sessionID string) (bool, error) {
	pipelineID, err := s.store.GetPipelineIDForSession(s.ctx, sessionID)
	if err != nil {
		return false, ErrPipelineCodeSnapshotUnavailable
	}
	run, err := s.loadPipeline(pipelineID)
	if err != nil {
		return false, err
	}
	if run.Current != sdd.Code || run.Status[sdd.Code] != sdd.Active {
		return false, nil
	}
	link, err := s.latestPipelineSession(run.ID, "coder")
	if err != nil {
		return false, err
	}
	return link.SessionID == sessionID && !pipelineArtifactRevisionRequested(run, sdd.Code, sessionID), nil
}

func separateExecutionRoot(sourceRoot, executionRoot string) bool {
	contains := func(base, path string) bool {
		relative, err := filepath.Rel(base, path)
		return err == nil && !filepath.IsAbs(relative) && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
	}
	return !contains(sourceRoot, executionRoot) && !contains(executionRoot, sourceRoot)
}

func (s *Service) CompletePipelineCode(pipelineID string) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	run, err := s.loadPipeline(pipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	if run.Current != sdd.Code || run.Status[sdd.Code] != sdd.Active {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	if !authoringPlanAllowsCode(run) {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	link, err := s.latestPipelineSession(run.ID, "coder")
	if err != nil {
		return PipelineDTO{}, err
	}
	completed := false
	err = s.walkEvents(s.ctx, link.SessionID, func(event events.Event) error {
		switch event.Type {
		case "run.completed", "external.run.completed":
			completed = true
		case "run.started", "external.run.started", "run.failed", "external.run.failed", "run.cancelled", "external.run.cancelled":
			completed = false
		}
		return nil
	})
	if err != nil {
		return PipelineDTO{}, safe("read coder evidence", err)
	}
	if !completed {
		return PipelineDTO{}, sdd.ErrEvidenceRequired
	}
	executionRoot, err := s.executionRootForSession(link.SessionID)
	if err != nil {
		return PipelineDTO{}, err
	}
	executionSnapshot, err := s.executionSnapshotForLink(link)
	if err != nil || executionSnapshot.Root != executionRoot {
		return PipelineDTO{}, ErrPipelineCodeSnapshotUnavailable
	}
	evidence, changed, err := pipelineCodeEvidence(s.ctx, executionSnapshot, 1024*1024)
	if err != nil {
		return PipelineDTO{}, err
	}
	if len(changed) == 0 || evidence == "" {
		return PipelineDTO{}, sdd.ErrEvidenceRequired
	}
	if err := s.store.SavePipelineExecutionArtifact(s.ctx, run.ID, sdd.Code, evidence, link.SessionID, run.Revision); err != nil {
		return PipelineDTO{}, safe("save code evidence", err)
	}
	run, err = s.loadPipeline(run.ID)
	if err != nil {
		return PipelineDTO{}, err
	}
	return pipelineDTO(run), nil
}

func (s *Service) CompletePipelineEvaluation(pipelineID string) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	run, err := s.loadPipeline(pipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	if run.Current != sdd.Eval || run.Status[sdd.Eval] != sdd.Active {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	if !authoringCodeAllowsEvaluation(run) {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	link, err := s.latestPipelineSession(run.ID, "evaluator")
	if err != nil {
		return PipelineDTO{}, err
	}
	criteria, err := s.pipelineEvaluationCriteria(run)
	if err != nil {
		return PipelineDTO{}, err
	}
	var admittedCriteria catalog.PipelineEvaluationCriteriaSnapshot
	if len(link.EvaluationCriteria) == 0 || json.Unmarshal(link.EvaluationCriteria, &admittedCriteria) != nil || admittedCriteria.Digest == "" || admittedCriteria.Digest != criteria.Digest {
		return PipelineDTO{}, ErrPipelineEvaluationCriteriaChanged
	}
	var response string
	completed := false
	evaluatorSession, err := s.store.GetSession(s.ctx, link.SessionID)
	if err != nil {
		return PipelineDTO{}, safe("read evaluator session", err)
	}
	if (evaluatorSession.BackendID == "codex" || s.external[evaluatorSession.BackendID] != nil) && !isCodexHostSession(evaluatorSession) {
		return PipelineDTO{}, ErrSDDCLIReadIsolationUnavailable
	}
	err = s.walkEvents(s.ctx, link.SessionID, func(event events.Event) error {
		switch event.Type {
		case "run.completed":
			completed = true
		case "run.started", "run.failed", "run.cancelled":
			completed = false
		case "message.assistant":
			var message struct {
				Content string `json:"content"`
			}
			if json.Unmarshal(event.Data, &message) == nil {
				response = message.Content
			}
		}
		return nil
	})
	if err != nil {
		return PipelineDTO{}, safe("read evaluator evidence", err)
	}
	var verdict struct {
		Passed       *bool                         `json:"passed"`
		Checks       []pipelineQACheck             `json:"checks"`
		Findings     []string                      `json:"findings"`
		Improvements []string                      `json:"improvements"`
		Criteria     []pipelineEvaluationCriterion `json:"criteria"`
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(response)))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&verdict)
	if decodeErr == nil {
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			if err == nil {
				decodeErr = errors.New("multiple JSON values")
			} else {
				decodeErr = err
			}
		}
	}
	if !completed || decodeErr != nil || verdict.Passed == nil || len(response) > 1024*1024 || !validQAReport(*verdict.Passed, verdict.Checks, verdict.Findings, verdict.Improvements) {
		return PipelineDTO{}, sdd.ErrEvidenceRequired
	}
	if *verdict.Passed {
		criteriaSource := normalizedEvidence(evaluationCriteriaText(criteria))
		codeEvidence := normalizedEvidence(resultingCode(run.Artifacts[sdd.Code].Content))
		if len(verdict.Findings) != 0 || len(verdict.Criteria) == 0 || len(verdict.Criteria) > 100 {
			return PipelineDTO{}, sdd.ErrEvidenceRequired
		}
		for _, item := range verdict.Criteria {
			criterion, evidence := strings.TrimSpace(item.Criterion), strings.TrimSpace(item.Evidence)
			if len(criterion) < 4 || len(criterion) > 500 || len(evidence) < 4 || len(evidence) > 500 || !strings.Contains(criteriaSource, normalizedEvidence(criterion)) || !strings.Contains(codeEvidence, normalizedEvidence(evidence)) {
				return PipelineDTO{}, sdd.ErrEvidenceRequired
			}
		}
	}
	artifact, err := json.Marshal(pipelineEvaluationArtifact{Passed: *verdict.Passed, Checks: verdict.Checks, Findings: verdict.Findings, Improvements: verdict.Improvements, Criteria: verdict.Criteria, CriteriaSource: criteria})
	if err != nil {
		return PipelineDTO{}, sdd.ErrEvidenceRequired
	}
	if err := s.store.SavePipelineExecutionArtifact(s.ctx, run.ID, sdd.Eval, string(artifact), link.SessionID, run.Revision); err != nil {
		return PipelineDTO{}, safe("save evaluation", err)
	}
	run, err = s.loadPipeline(run.ID)
	if err != nil {
		return PipelineDTO{}, err
	}
	return pipelineDTO(run), nil
}

// resultingCode keeps what the change leaves in the files: added and context lines without their diff
// markers, so evidence quoted across several lines of code is found in the diff.
func resultingCode(diff string) string {
	var code strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "), strings.HasPrefix(line, "-"):
			continue
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, " "):
			line = line[1:]
		}
		code.WriteString(line)
		code.WriteByte('\n')
	}
	return code.String()
}

// normalizedEvidence compares text regardless of case, indentation and line breaks.
func normalizedEvidence(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// ApplyPipelineCode applies only the exact Code snapshot that passed Eval. It
// is a separate, explicit operation from both Code execution and Eval.
func (s *Service) ApplyPipelineCode(pipelineID string) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	s.pipelineApplyGate.Lock()
	defer s.pipelineApplyGate.Unlock()

	run, err := s.loadPipeline(pipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	// The patch is applied once QA approved it: while the pull request stage is current, or on a pipeline that finished before that stage existed.
	if (run.Current != "" && run.Current != sdd.PRs) || run.Status[sdd.Code] != sdd.Completed || run.Status[sdd.Eval] != sdd.Completed ||
		!pipelineArtifactApproved(run, sdd.Code) || !pipelineArtifactApproved(run, sdd.Eval) {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	link, err := s.latestPipelineSession(run.ID, "coder")
	if err != nil {
		return PipelineDTO{}, err
	}
	codeArtifact := run.Artifacts[sdd.Code]
	if codeArtifact.Content == "" || codeArtifact.SourceSessionID == "" || codeArtifact.SourceSessionID != link.SessionID {
		return PipelineDTO{}, ErrPipelineCodeSnapshotUnavailable
	}
	snapshot, err := s.executionSnapshotForLink(link)
	if err != nil {
		return PipelineDTO{}, err
	}
	if !link.ExecutionAppliedAt.IsZero() {
		applied, err := repositories.ExecutionSnapshotApplied(s.ctx, snapshot)
		if err != nil || !applied {
			return PipelineDTO{}, repositories.ErrExecutionApplyConflict
		}
		return s.pipelineDTOWithCodeApplyStatus(run)
	}
	if applied, err := repositories.ExecutionSnapshotApplied(s.ctx, snapshot); err != nil {
		return PipelineDTO{}, err
	} else if !applied {
		currentEvidence, _, err := pipelineCodeEvidence(s.ctx, snapshot, 1024*1024)
		if err != nil {
			return PipelineDTO{}, err
		}
		if currentEvidence != codeArtifact.Content {
			return PipelineDTO{}, repositories.ErrExecutionSourceDrift
		}
		if _, err := repositories.ApplyExecutionSnapshot(s.ctx, snapshot, 1024*1024); err != nil {
			return PipelineDTO{}, err
		}
	}
	if applied, err := repositories.ExecutionSnapshotApplied(s.ctx, snapshot); err != nil || !applied {
		return PipelineDTO{}, repositories.ErrExecutionApplyConflict
	}
	appliedAt := time.Now().UTC()
	if err := s.store.MarkPipelineCodeApplied(s.ctx, run.ID, link.SessionID, run.Revision, appliedAt); err != nil {
		return PipelineDTO{}, safe("record Code patch readback", err)
	}
	run, err = s.loadPipeline(run.ID)
	if err != nil {
		return PipelineDTO{}, err
	}
	dto, err := s.pipelineDTOWithCodeApplyStatus(run)
	if err != nil {
		return PipelineDTO{}, err
	}
	return dto, nil
}

func pipelineCodeEvidence(ctx context.Context, snapshot repositories.ExecutionSnapshot, maxBytes int) (string, []repositories.ExecutionManifestEntry, error) {
	diff, changed, err := repositories.ExecutionDiff(ctx, snapshot, maxBytes)
	if err != nil {
		return "", nil, err
	}
	if diff == "" || len(changed) == 0 {
		return "", nil, nil
	}
	finalManifest, err := repositories.ExecutionManifest(ctx, snapshot.Root)
	if err != nil {
		return "", nil, err
	}
	finalJSON, err := json.Marshal(finalManifest)
	if err != nil {
		return "", nil, ErrPipelineCodeSnapshotUnavailable
	}
	manifestHash := sha256.Sum256(finalJSON)
	before := make(map[string]repositories.ExecutionManifestEntry, len(snapshot.Manifest))
	after := make(map[string]repositories.ExecutionManifestEntry, len(finalManifest))
	for _, entry := range snapshot.Manifest {
		before[entry.Path] = entry
	}
	for _, entry := range finalManifest {
		after[entry.Path] = entry
	}
	changedPaths := make([]string, 0, len(changed))
	seen := make(map[string]struct{}, len(changed))
	for _, entry := range changed {
		if _, exists := seen[entry.Path]; !exists {
			seen[entry.Path] = struct{}{}
			changedPaths = append(changedPaths, entry.Path)
		}
	}
	sort.Strings(changedPaths)
	var evidence strings.Builder
	evidence.WriteString(diff)
	evidence.WriteString("\n# Harflex isolated Code provenance\n")
	evidence.WriteString("# execution_root_id=")
	evidence.WriteString(strconv.Quote(filepath.Base(snapshot.Root)))
	evidence.WriteString(" source_root=")
	evidence.WriteString(strconv.Quote(snapshot.SourceRoot))
	if snapshot.IsGit {
		evidence.WriteString(" source_head=")
		evidence.WriteString(strconv.Quote(snapshot.SourceHead))
		evidence.WriteString(" source_branch=")
		evidence.WriteString(strconv.Quote(snapshot.SourceBranch))
	}
	evidence.WriteString("\n# final_manifest_sha256=")
	evidence.WriteString(hex.EncodeToString(manifestHash[:]))
	evidence.WriteByte('\n')
	for _, path := range changedPaths {
		oldEntry, hadOld := before[path]
		newEntry, hasNew := after[path]
		action := "modified"
		if !hadOld {
			action = "added"
		} else if !hasNew {
			action = "removed"
		}
		oldHash, oldMode, oldKind := "absent", "-", "-"
		if hadOld {
			oldHash, oldMode, oldKind = oldEntry.Hash, fmt.Sprintf("%04o", oldEntry.Mode), oldEntry.Kind
		}
		newHash, newMode, newKind := "absent", "-", "-"
		if hasNew {
			newHash, newMode, newKind = newEntry.Hash, fmt.Sprintf("%04o", newEntry.Mode), newEntry.Kind
		}
		fmt.Fprintf(&evidence, "# file=%s action=%s before=%s mode=%s type=%s after=%s mode=%s type=%s\n",
			strconv.Quote(path), action, oldHash, oldMode, oldKind, newHash, newMode, newKind)
		if evidence.Len() > maxBytes {
			return "", nil, repositories.ErrExecutionEvidenceTooLarge
		}
	}
	if evidence.Len() > maxBytes {
		return "", nil, repositories.ErrExecutionEvidenceTooLarge
	}
	return evidence.String(), changed, nil
}
