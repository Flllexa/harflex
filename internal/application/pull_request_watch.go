package application

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/security"
	"github.com/persioflexa/harflex/internal/tools"
)

// The PRs conversation records each pull request it opens; the review watch then asks that same conversation, every
// ten minutes, to read new review comments, fix them on the branch, push and answer them, until the PR is merged or
// closed. The agent never merges or force-pushes, and the watch only runs with Acesso total, since nobody is there to
// approve each command.

const pullRequestWatchInterval = 10 * time.Minute

var ErrPullRequestNotFound = errors.New("pull request not found")

type PullRequestEventDTO struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Summary string    `json:"summary"`
}

type PullRequestDTO struct {
	ID            string                `json:"id"`
	PipelineID    string                `json:"pipelineId"`
	SessionID     string                `json:"sessionId"`
	URL           string                `json:"url"`
	Title         string                `json:"title"`
	Branch        string                `json:"branch"`
	State         string                `json:"state"`
	Watch         bool                  `json:"watch"`
	LastCheckedAt *time.Time            `json:"lastCheckedAt,omitempty"`
	NextCheckAt   *time.Time            `json:"nextCheckAt,omitempty"`
	Checking      bool                  `json:"checking"`
	Timeline      []PullRequestEventDTO `json:"timeline"`
}

type SetPullRequestWatchInput struct {
	ID    string `json:"id"`
	Watch bool   `json:"watch"`
}

type pullRequestWatchState struct {
	mu       sync.Mutex
	checking map[string]bool
	lastTick time.Time
	// creating maps a project to the pipeline whose PRs conversation is being created there.
	creating map[string]string
}

// pullRequestPipelineFor finds the pipeline whose PRs conversation this is, including one still being created.
func (s *Service) pullRequestPipelineFor(sessionID, workspaceID string) string {
	if pipelineID, err := s.store.GetPipelineIDForPRSession(s.ctx, sessionID); err == nil {
		return pipelineID
	}
	s.prWatch.mu.Lock()
	defer s.prWatch.mu.Unlock()
	return s.prWatch.creating[workspaceID]
}

func (s *Service) pullRequestDTO(pr catalog.PipelinePullRequest) PullRequestDTO {
	dto := PullRequestDTO{ID: pr.ID, PipelineID: pr.PipelineID, SessionID: pr.SessionID, URL: pr.URL, Title: pr.Title, Branch: pr.Branch, State: pr.State, Watch: pr.Watch, Timeline: []PullRequestEventDTO{}}
	if !pr.LastCheckedAt.IsZero() {
		checked := pr.LastCheckedAt
		dto.LastCheckedAt = &checked
		if pr.Watch && pr.State == "open" {
			next := checked.Add(pullRequestWatchInterval)
			dto.NextCheckAt = &next
		}
	}
	s.prWatch.mu.Lock()
	dto.Checking = s.prWatch.checking[pr.ID]
	s.prWatch.mu.Unlock()
	for _, event := range pr.Timeline {
		dto.Timeline = append(dto.Timeline, PullRequestEventDTO{At: event.At, Kind: event.Kind, Summary: event.Summary})
	}
	return dto
}

func (s *Service) emitPullRequests(pipelineID string) {
	s.mu.RLock()
	emit := s.emit
	s.mu.RUnlock()
	if emit != nil {
		emit("harflex:pull-requests", map[string]string{"pipelineId": pipelineID})
	}
}

func appendPullRequestEvent(pr *catalog.PipelinePullRequest, kind, summary string) {
	pr.Timeline = append(pr.Timeline, catalog.PullRequestEvent{At: time.Now().UTC(), Kind: kind, Summary: clipUTF8(strings.TrimSpace(summary), 600)})
}

// ListPipelinePullRequests returns the pull requests the PRs conversation opened for a pipeline.
func (s *Service) ListPipelinePullRequests(pipelineID string) ([]PullRequestDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	found, err := s.store.ListPipelinePullRequests(s.ctx, pipelineID)
	if err != nil {
		return nil, safe("list pull requests", err)
	}
	result := make([]PullRequestDTO, 0, len(found))
	for _, pr := range found {
		result = append(result, s.pullRequestDTO(pr))
	}
	return result, nil
}

// SetPullRequestWatch turns the review watch on or off; turning it on checks right away.
func (s *Service) SetPullRequestWatch(in SetPullRequestWatchInput) (PullRequestDTO, error) {
	if err := s.beginCall(); err != nil {
		return PullRequestDTO{}, err
	}
	defer s.endCall()
	pr, err := s.store.GetPipelinePullRequest(s.ctx, in.ID)
	if err != nil {
		return PullRequestDTO{}, ErrPullRequestNotFound
	}
	if pr.Watch != in.Watch {
		pr.Watch = in.Watch
		if in.Watch {
			appendPullRequestEvent(&pr, "watch_on", "Vigia da revisão ligada")
			pr.LastCheckedAt = time.Time{}
		} else {
			appendPullRequestEvent(&pr, "watch_off", "Vigia da revisão desligada")
		}
		pr.UpdatedAt = time.Now().UTC()
		if pr, err = s.store.SavePipelinePullRequest(s.ctx, pr); err != nil {
			return PullRequestDTO{}, safe("save pull request watch", err)
		}
		s.emitPullRequests(pr.PipelineID)
	}
	return s.pullRequestDTO(pr), nil
}

// CheckPullRequestNow asks the PRs conversation to look at the pull request now, whatever the watch's schedule.
func (s *Service) CheckPullRequestNow(prID string) (PullRequestDTO, error) {
	if err := s.beginCall(); err != nil {
		return PullRequestDTO{}, err
	}
	defer s.endCall()
	pr, err := s.store.GetPipelinePullRequest(s.ctx, prID)
	if err != nil {
		return PullRequestDTO{}, ErrPullRequestNotFound
	}
	if pr.State != "open" {
		return s.pullRequestDTO(pr), nil
	}
	if err := s.startPullRequestCheck(pr); err != nil {
		return PullRequestDTO{}, err
	}
	pr, _ = s.store.GetPipelinePullRequest(s.ctx, prID)
	return s.pullRequestDTO(pr), nil
}

var ErrPullRequestWatchNeedsFullAccess = errors.New("the review watch needs Acesso total: nobody is there to approve each command")

// startPullRequestCheck records the check and sends the request to the PRs conversation in the background.
func (s *Service) startPullRequestCheck(pr catalog.PipelinePullRequest) error {
	run, err := s.store.GetPipeline(s.ctx, pr.PipelineID)
	if err != nil {
		return safe("read pull request pipeline", err)
	}
	workspace, err := s.store.GetWorkspace(s.ctx, run.WorkspaceID)
	if err != nil {
		return safe("read pull request project", err)
	}
	if security.Profile(workspace.Profile) != security.FullAccess {
		return ErrPullRequestWatchNeedsFullAccess
	}
	s.prWatch.mu.Lock()
	if s.prWatch.checking == nil {
		s.prWatch.checking = map[string]bool{}
	}
	if s.prWatch.checking[pr.ID] {
		s.prWatch.mu.Unlock()
		return nil
	}
	s.prWatch.checking[pr.ID] = true
	s.prWatch.mu.Unlock()
	since := pr.LastCheckedAt
	pr.LastCheckedAt = time.Now().UTC()
	appendPullRequestEvent(&pr, "checking", "Conferindo comentários novos")
	pr.UpdatedAt = pr.LastCheckedAt
	if _, err := s.store.SavePipelinePullRequest(s.ctx, pr); err != nil {
		s.finishPullRequestCheck(pr.ID)
		return safe("save pull request check", err)
	}
	s.emitPullRequests(pr.PipelineID)
	go func() {
		defer func() { s.finishPullRequestCheck(pr.ID); s.emitPullRequests(pr.PipelineID) }()
		result, err := s.Prompt(PromptInput{SessionID: pr.SessionID, Text: pullRequestWatchPrompt(pr, since)})
		if err == nil && result.Status == RunCompleted {
			return
		}
		latest, readErr := s.store.GetPipelinePullRequest(s.ctx, pr.ID)
		if readErr != nil {
			return
		}
		reason := "A conferência não terminou"
		if err != nil {
			reason += ": " + err.Error()
		} else if result.Status != "" {
			reason += " (" + result.Status + ")"
		}
		appendPullRequestEvent(&latest, "error", reason)
		latest.UpdatedAt = time.Now().UTC()
		_, _ = s.store.SavePipelinePullRequest(s.ctx, latest)
	}()
	return nil
}

func (s *Service) finishPullRequestCheck(prID string) {
	s.prWatch.mu.Lock()
	delete(s.prWatch.checking, prID)
	s.prWatch.mu.Unlock()
}

func pullRequestWatchPrompt(pr catalog.PipelinePullRequest, since time.Time) string {
	var prompt strings.Builder
	prompt.WriteString("Vigia da revisão do pull request " + pr.URL)
	if pr.Branch != "" {
		prompt.WriteString(" (branch " + pr.Branch + ")")
	}
	prompt.WriteString(".\n")
	if since.IsZero() {
		prompt.WriteString("Leia todos os comentários e revisões abertos do PR.\n")
	} else {
		prompt.WriteString("Leia os comentários e revisões novos desde " + since.Format(time.RFC3339) + ".\n")
	}
	prompt.WriteString("Para cada apontamento que pede mudança: corrija no mesmo branch, rode os testes do projeto, faça commit e push, e responda o comentário dizendo o que mudou. " +
		"Se discordar de um apontamento, responda explicando, sem mudar o código. Nunca faça merge, force push nem aprove o próprio PR.\n" +
		"Ao terminar, chame harflex_pull_request_status com a URL do PR, o estado dele (open, merged ou closed), quantos apontamentos você tratou e um resumo curto do que fez. " +
		"Se não houver nada novo, chame a ferramenta com handledComments 0.\n")
	return prompt.String()
}

// tickPullRequestWatch starts the checks that are due; called by the scheduler every second, it looks every 30 seconds.
func (s *Service) tickPullRequestWatch(now time.Time) {
	s.prWatch.mu.Lock()
	if now.Sub(s.prWatch.lastTick) < 30*time.Second {
		s.prWatch.mu.Unlock()
		return
	}
	s.prWatch.lastTick = now
	s.prWatch.mu.Unlock()
	if err := s.beginCall(); err != nil {
		return
	}
	defer s.endCall()
	watched, err := s.store.ListWatchedPullRequests(s.ctx)
	if err != nil {
		return
	}
	for _, pr := range watched {
		if !pr.LastCheckedAt.IsZero() && now.Sub(pr.LastCheckedAt) < pullRequestWatchInterval {
			continue
		}
		if err := s.startPullRequestCheck(pr); errors.Is(err, ErrPullRequestWatchNeedsFullAccess) {
			// Said once on the timeline, and checked again at the next interval.
			if n := len(pr.Timeline); n == 0 || pr.Timeline[n-1].Kind != "needs_access" {
				appendPullRequestEvent(&pr, "needs_access", "A vigia precisa de Acesso total no projeto para corrigir e fazer push sozinha")
			}
			pr.LastCheckedAt, pr.UpdatedAt = now, now
			_, _ = s.store.SavePipelinePullRequest(s.ctx, pr)
			s.emitPullRequests(pr.PipelineID)
		}
	}
}

// pullRequestTools are the PRs conversation's own: it records the pull requests it opens and reports on each check.
func (s *Service) pullRequestTools(pipelineID, sessionID string) []tools.Tool {
	return []tools.Tool{
		harflexTool{service: s, workspaceID: pipelineID, risk: security.ReadOnly, spec: agentcore.ToolSpec{
			Name:        "harflex_register_pull_request",
			Description: "Record in Harflex a pull request you just opened for this pipeline, so the person sees it on the PRs screen and can turn on the review watch. Call it once per pull request, right after opening it.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":8},"title":{"type":"string"},"branch":{"type":"string"}},"required":["url"],"additionalProperties":false}`),
		}, run: func(s *Service, pipelineID string, args json.RawMessage) (any, error) {
			return s.registerPullRequest(pipelineID, sessionID, args)
		}},
		harflexTool{service: s, workspaceID: pipelineID, risk: security.ReadOnly, spec: agentcore.ToolSpec{
			Name:        "harflex_pull_request_status",
			Description: "Report the outcome of a review check of a pull request recorded in Harflex: its state (open, merged or closed), how many review comments you handled and a short summary.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":8},"state":{"type":"string","enum":["open","merged","closed"]},"handledComments":{"type":"integer","minimum":0},"summary":{"type":"string"}},"required":["url","state"],"additionalProperties":false}`),
		}, run: func(s *Service, pipelineID string, args json.RawMessage) (any, error) {
			return s.reportPullRequest(pipelineID, args)
		}},
	}
}

func validPullRequestURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && len(raw) <= 2000 && utf8.ValidString(raw)
}

func (s *Service) registerPullRequest(pipelineID, sessionID string, args json.RawMessage) (any, error) {
	var in struct {
		URL    string `json:"url"`
		Title  string `json:"title"`
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal(args, &in); err != nil || !validPullRequestURL(in.URL) || len(in.Title) > 500 || len(in.Branch) > 300 {
		return nil, errHarflexArguments
	}
	now := time.Now().UTC()
	pr := catalog.PipelinePullRequest{ID: id.New(), PipelineID: pipelineID, SessionID: sessionID, URL: strings.TrimSpace(in.URL), Title: strings.TrimSpace(in.Title), Branch: strings.TrimSpace(in.Branch), State: "open", CreatedAt: now, UpdatedAt: now}
	if existing, err := s.store.ListPipelinePullRequests(s.ctx, pipelineID); err == nil {
		for _, item := range existing {
			if item.URL == pr.URL {
				pr.ID, pr.Watch, pr.LastCheckedAt, pr.Timeline, pr.CreatedAt, pr.State = item.ID, item.Watch, item.LastCheckedAt, item.Timeline, item.CreatedAt, item.State
			}
		}
	}
	if len(pr.Timeline) == 0 {
		appendPullRequestEvent(&pr, "opened", "PR aberto: "+pr.Title)
	}
	saved, err := s.store.SavePipelinePullRequest(s.ctx, pr)
	if err != nil {
		return nil, err
	}
	s.emitPullRequests(pipelineID)
	return map[string]any{"recorded": true, "id": saved.ID, "next": "The person can turn on the review watch for this pull request on the PRs screen."}, nil
}

func (s *Service) reportPullRequest(pipelineID string, args json.RawMessage) (any, error) {
	var in struct {
		URL             string `json:"url"`
		State           string `json:"state"`
		HandledComments int    `json:"handledComments"`
		Summary         string `json:"summary"`
	}
	if err := json.Unmarshal(args, &in); err != nil || !validPullRequestURL(in.URL) || (in.State != "open" && in.State != "merged" && in.State != "closed") || in.HandledComments < 0 || len(in.Summary) > 4000 {
		return nil, errHarflexArguments
	}
	found, err := s.store.ListPipelinePullRequests(s.ctx, pipelineID)
	if err != nil {
		return nil, err
	}
	for _, pr := range found {
		if pr.URL != strings.TrimSpace(in.URL) {
			continue
		}
		pr.State = in.State
		switch {
		case in.State == "merged":
			appendPullRequestEvent(&pr, "merged", firstNonEmpty(in.Summary, "PR mergeado; a vigia parou"))
			pr.Watch = false
		case in.State == "closed":
			appendPullRequestEvent(&pr, "closed", firstNonEmpty(in.Summary, "PR fechado; a vigia parou"))
			pr.Watch = false
		case in.HandledComments > 0:
			appendPullRequestEvent(&pr, "fixed", firstNonEmpty(in.Summary, "Apontamentos corrigidos e respondidos"))
		default:
			appendPullRequestEvent(&pr, "quiet", firstNonEmpty(in.Summary, "Nenhum comentário novo"))
		}
		pr.UpdatedAt = time.Now().UTC()
		if _, err := s.store.SavePipelinePullRequest(s.ctx, pr); err != nil {
			return nil, err
		}
		s.emitPullRequests(pipelineID)
		return map[string]any{"recorded": true, "state": pr.State, "watch": pr.Watch}, nil
	}
	return nil, ErrPullRequestNotFound
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
