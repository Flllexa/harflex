package application

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
)

var ErrDelegationLimit = errors.New("subagent delegation limit reached")
var ErrDelegationBudgetExceeded = errors.New("subagent prompt budget exhausted")
var ErrDelegationRequestConflict = errors.New("delegation request ID was reused for another task")

var delegationRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

const maxDelegationDepth = 3
const maxChildDelegations = 5
const maxDelegatedPrompts = 3
const defaultDelegationTimeout = 5 * time.Minute

type delegationCreation struct {
	link    catalog.Delegation
	prompt  string
	created bool
}

func delegationDTO(link catalog.Delegation) DelegationDTO {
	return DelegationDTO{ID: link.ID, ParentSessionID: link.ParentSessionID, ChildSessionID: link.ChildSessionID, AgentID: link.AgentID, TaskPrompt: delegationTaskPreview(link.TaskPrompt), PromptCount: link.PromptCount, PromptLimit: maxDelegatedPrompts, TimeoutSeconds: int(defaultDelegationTimeout.Seconds()), Depth: link.Depth, CreatedAt: link.CreatedAt}
}

func delegationTaskPreview(prompt string) string {
	const maxRunes = 280
	if utf8.RuneCountInString(prompt) <= maxRunes {
		return prompt
	}
	count := 0
	for index := range prompt {
		if count == maxRunes {
			return prompt[:index] + "…"
		}
		count++
	}
	return prompt
}

func (s *Service) DelegateToAgent(in DelegateToAgentInput) (DelegatedSessionDTO, error) {
	if err := s.beginCall(); err != nil {
		return DelegatedSessionDTO{}, err
	}
	defer s.endCall()
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.ParentSessionID == "" || in.AgentID == "" || !delegationRequestID.MatchString(in.RequestID) || in.Prompt == "" || len(in.Prompt) > maxPromptBytes || !utf8.ValidString(in.Prompt) || strings.ContainsRune(in.Prompt, '\x00') {
		return DelegatedSessionDTO{}, ErrInvalidInput
	}
	parent, err := s.store.GetSession(s.ctx, in.ParentSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return DelegatedSessionDTO{}, ErrSessionNotFound
	}
	if err != nil {
		return DelegatedSessionDTO{}, safe("get parent session", err)
	}
	agent, err := s.store.GetAgent(s.ctx, in.AgentID)
	if errors.Is(err, sql.ErrNoRows) {
		return DelegatedSessionDTO{}, ErrAgentNotFound
	}
	if err != nil {
		return DelegatedSessionDTO{}, safe("get delegate agent", err)
	}
	hash := sha256.Sum256([]byte(in.Prompt))
	taskHash := hex.EncodeToString(hash[:])
	existing, err := s.store.GetDelegationByRequestID(s.ctx, parent.ID, in.RequestID)
	if err == nil {
		if existing.AgentID != agent.ID || existing.TaskHash != taskHash {
			return DelegatedSessionDTO{}, ErrDelegationRequestConflict
		}
		child, err := s.store.GetSession(s.ctx, existing.ChildSessionID)
		if err != nil {
			return DelegatedSessionDTO{}, safe("get existing delegated session", err)
		}
		if child.WorkspaceID != parent.WorkspaceID {
			return DelegatedSessionDTO{}, ErrSessionNotFound
		}
		return DelegatedSessionDTO{Session: s.sessionDTO(child), Prompt: in.Prompt, Depth: existing.Depth, Created: false}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DelegatedSessionDTO{}, safe("find delegated task", err)
	}
	depth, err := s.store.GetDelegationDepth(s.ctx, in.ParentSessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DelegatedSessionDTO{}, safe("get delegation depth", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		depth = 0
	}
	children, err := s.store.ListDelegations(s.ctx, in.ParentSessionID)
	if err != nil {
		return DelegatedSessionDTO{}, safe("count delegated children", err)
	}
	if depth >= maxDelegationDepth || len(children) >= maxChildDelegations {
		return DelegatedSessionDTO{}, ErrDelegationLimit
	}
	creation := &delegationCreation{link: catalog.Delegation{ID: id.New(), ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: in.RequestID, TaskHash: taskHash, Depth: depth + 1, CreatedAt: time.Now().UTC()}, prompt: in.Prompt}
	session, err := s.createSession(CreateSessionInput{WorkspaceID: parent.WorkspaceID, BackendID: agent.BackendID, AgentID: agent.ID}, creation)
	if errors.Is(err, catalog.ErrDelegationChildLimit) {
		return DelegatedSessionDTO{}, ErrDelegationLimit
	}
	if err != nil {
		return DelegatedSessionDTO{}, err
	}
	return DelegatedSessionDTO{Session: session, Prompt: in.Prompt, Depth: creation.link.Depth, Created: creation.created}, nil
}

func (s *Service) ListDelegations(parentSessionID string) ([]DelegationDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if parentSessionID == "" {
		return nil, ErrInvalidInput
	}
	parent, err := s.store.GetSession(s.ctx, parentSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	} else if err != nil {
		return nil, safe("get parent session", err)
	}
	links, err := s.store.ListDelegations(s.ctx, parentSessionID)
	if err != nil {
		return nil, safe("list delegations", err)
	}
	result := make([]DelegationDTO, 0, len(links))
	for _, link := range links {
		child, err := s.store.GetSession(s.ctx, link.ChildSessionID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		if err != nil {
			return nil, safe("get delegated session", err)
		}
		if child.WorkspaceID != parent.WorkspaceID {
			return nil, ErrSessionNotFound
		}
		outcome, err := s.store.GetDelegationOutcome(s.ctx, link.ChildSessionID)
		if err != nil {
			return nil, safe("read delegated outcome", err)
		}
		if child.Status == "history_too_large" {
			outcome = catalog.DelegationOutcome{Status: child.Status}
		}
		item := delegationDTO(link)
		item.Status, item.Result, item.ErrorCode = outcome.Status, outcome.Result, outcome.ErrorCode
		result = append(result, item)
	}
	return result, nil
}

func (s *Service) GetParentDelegation(childSessionID string) (*DelegationDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if strings.TrimSpace(childSessionID) == "" {
		return nil, ErrInvalidInput
	}
	child, err := s.store.GetSession(s.ctx, childSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, safe("get child session", err)
	}
	link, err := s.store.GetParentDelegation(s.ctx, childSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, safe("get parent delegation", err)
	}
	parent, err := s.store.GetSession(s.ctx, link.ParentSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, safe("get parent session", err)
	}
	if parent.WorkspaceID != child.WorkspaceID {
		return nil, ErrSessionNotFound
	}
	outcome, err := s.store.GetDelegationOutcome(s.ctx, childSessionID)
	if err != nil {
		return nil, safe("read delegated outcome", err)
	}
	if child.Status == "history_too_large" {
		outcome = catalog.DelegationOutcome{Status: child.Status}
	}
	item := delegationDTO(link)
	item.Status, item.Result, item.ErrorCode = outcome.Status, outcome.Result, outcome.ErrorCode
	return &item, nil
}
