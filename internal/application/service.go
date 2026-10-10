package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"github.com/persioflexa/harflex/internal/mcpserver"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/modelcatalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/sddworkspace"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/terminal"
)

var ErrInvalidInput = errors.New("invalid input")

type Service struct {
	ctx                context.Context
	store              Store
	secrets            secrets.Store
	external           map[string]ExternalBackend
	executionCacheRoot string
	mu                 sync.RWMutex
	workChatMu         sync.Mutex // one pass at a time gives each work its chat, so none gets two
	// profileGate serializes catalog/credential changes with credential resolution.
	// Never hold mu with profileGate; finish external calls and release profileGate
	// before acquiring mu for lifecycle or session state.
	profileGate         sync.RWMutex
	workspacePolicyGate sync.RWMutex
	mcpGate             sync.Mutex
	pipelineApplyGate   sync.Mutex
	embeddingGate       sync.Mutex
	recoveryGate        sync.RWMutex
	shutdownGate        sync.Mutex
	// Serializes durable authoring admission/ownership registration with shutdown.
	// Provider calls and composition never hold this gate.
	authoringAdmissionGate       sync.Mutex
	authoringActive              map[string]catalog.AuthoringStageRequest
	authoringCodeOwners          map[string]*authoringCodeOwner
	designOwners                 map[string]*pipelineDesignOwner
	sessions                     map[string]sessionRunner
	journals                     map[string]*eventJournal
	providerFactory              func(openai.Config) (agentcore.Provider, error)
	modelHTTPClient              *http.Client
	modelCatalogCache            *modelcatalog.Cache
	privateWorkspaceParent       string
	privateCodeCopy              func(context.Context, string, string, string, sddworkspace.Manifest) (sddworkspace.PrivateCopy, error)
	authoringCodeScan            func(context.Context, string) (sddworkspace.Manifest, error)
	authoringCodeReadbackTimeout time.Duration
	authoringCodePatchMaxBytes   int64
	privateCodeCopyMu            sync.Mutex
	privateCodeCopyInFlight      map[string]struct{}
	catalogTokenKey              [32]byte
	catalogTokenReady            bool
	audit                        AuditExporter
	emit                         func(string, any)
	closing                      bool
	activeCalls                  int
	drained                      chan struct{}
	scheduleWake                 chan struct{}
	scheduleMu                   sync.Mutex
	scheduleRunning              string
	scheduleCanceled             map[string]bool
	scheduleWaitingCursor        string
	delegationTimeout            time.Duration
	projectMemoryMu              sync.Mutex
	terminals                    *terminal.Manager
	// platformMCP serves the Harflex tools to CLI chats; platformMCPTokens holds one token per session.
	platformMCP       *mcpserver.Server
	platformMCPTokens map[string]string
	// qaLoops holds the background QA state of each pipeline (QA lab and fix rounds).
	qaLoopMu sync.Mutex
	qaLoops  map[string]QALoopDTO
	// prWatch tracks the review checks running for the pull requests.
	prWatch           pullRequestWatchState
	projectMemoryRuns map[string]bool
}

func NewService(ctx context.Context, deps Dependencies) *Service {
	external := make(map[string]ExternalBackend, len(deps.External))
	for k, v := range deps.External {
		external[k] = v
	}
	if deps.External == nil {
		external["codex"] = externalagent.NewCodex("")
		external["opencode"] = externalagent.NewOpenCode("")
		external["claude"] = externalagent.NewClaudeCode("")
	}
	factory := deps.ProviderFactory
	if factory == nil {
		factory = func(c openai.Config) (agentcore.Provider, error) { return openai.New(c) }
	}
	service := &Service{ctx: ctx, store: deps.Store, secrets: deps.Secrets, external: external, executionCacheRoot: deps.ExecutionCacheRoot, sessions: make(map[string]sessionRunner), journals: make(map[string]*eventJournal), authoringCodeOwners: make(map[string]*authoringCodeOwner), providerFactory: factory, modelHTTPClient: &http.Client{Transport: http.DefaultTransport}, modelCatalogCache: modelcatalog.NewCache(time.Now), privateWorkspaceParent: deps.PrivateWorkspaceParent, privateCodeCopy: sddworkspace.CreatePrivateCopyAt, authoringCodeScan: sddworkspace.Scan, authoringCodeReadbackTimeout: 15 * time.Minute, authoringCodePatchMaxBytes: sddworkspace.MaxPatchEvidenceBytes, privateCodeCopyInFlight: make(map[string]struct{}), audit: deps.Audit, emit: deps.Emit, drained: make(chan struct{}), scheduleWake: make(chan struct{}, 1), scheduleCanceled: make(map[string]bool), delegationTimeout: defaultDelegationTimeout, projectMemoryRuns: make(map[string]bool)}
	_, tokenErr := rand.Read(service.catalogTokenKey[:])
	service.catalogTokenReady = tokenErr == nil
	if service.audit == nil {
		service.audit = serviceAuditExporter{service}
	}
	return service
}

func (s *Service) setPrivateCodeCopyInFlight(attemptID string, inFlight bool) {
	s.privateCodeCopyMu.Lock()
	defer s.privateCodeCopyMu.Unlock()
	if inFlight {
		s.privateCodeCopyInFlight[attemptID] = struct{}{}
		return
	}
	delete(s.privateCodeCopyInFlight, attemptID)
}

func (s *Service) privateCodeCopyIsInFlight(attemptID string) bool {
	s.privateCodeCopyMu.Lock()
	defer s.privateCodeCopyMu.Unlock()
	_, ok := s.privateCodeCopyInFlight[attemptID]
	return ok
}

func (s *Service) beginCall() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return context.Canceled
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.activeCalls++
	return nil
}
func (s *Service) endCall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeCalls--
	if s.closing && s.activeCalls == 0 {
		close(s.drained)
	}
}

// Shutdown prevents admission and drains calls after the owner cancels the lifecycle.
// This keeps terminal journal writes ahead of closing the catalog.
func Shutdown(s *Service) error {
	s.shutdownGate.Lock()
	defer s.shutdownGate.Unlock()
	s.mu.Lock()
	terminals := s.terminals
	platformMCP := s.platformMCP
	s.mu.Unlock()
	if terminals != nil {
		terminals.CloseAll()
	}
	if platformMCP != nil {
		_ = platformMCP.Close()
	}
	s.authoringAdmissionGate.Lock()
	s.mu.Lock()
	if !s.closing {
		s.closing = true
		if s.activeCalls == 0 {
			close(s.drained)
		}
	}
	s.mu.Unlock()
	s.mu.Lock()
	var sddRunners []interface{ Abort(context.Context) error }
	codeOwners := make([]*authoringCodeOwner, 0, len(s.authoringCodeOwners))
	for _, owner := range s.authoringCodeOwners {
		codeOwners = append(codeOwners, owner)
	}
	for _, runner := range s.sessions {
		switch internal := runner.(type) {
		case *sddAttemptRunner:
			sddRunners = append(sddRunners, internal)
		case *authoringStageRunner:
			sddRunners = append(sddRunners, internal)
		}
	}
	owners := make(map[string]catalog.AuthoringStageRequest, len(s.authoringActive))
	for id, ref := range s.authoringActive {
		owners[id] = ref
	}
	designOwners := make(map[string]*pipelineDesignOwner, len(s.designOwners))
	for id, owner := range s.designOwners {
		designOwners[id] = owner
	}
	s.mu.Unlock()
	// Fence every owned admission, including one still composing without a
	// runner, before aborting or joining. Never interrupt another Service's work.
	fencedAuthoring := make(map[string]bool, len(owners))
	for id, ref := range owners {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		fencedAuthoring[id] = s.fenceAuthoringOwner(ctx, id, ref)
		cancel()
	}
	for id, owner := range designOwners {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		_, _ = s.store.RequestPipelineDesignCancellation(ctx, owner.pipelineID, id)
		cancel()
		owner.cancel()
	}
	s.authoringAdmissionGate.Unlock()
	// Internal generation is never drained into another retry on app close.
	// Ordinary sessions retain their existing lifecycle behavior.
	uncertainAbort := false
	for _, owner := range codeOwners {
		owner.cancel()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		_ = owner.abort(ctx)
		select {
		case <-owner.done:
		case <-ctx.Done():
			uncertainAbort = true
		}
		cancel()
	}
	for _, runner := range sddRunners {
		if internal, ok := runner.(*authoringStageRunner); ok && !fencedAuthoring[internal.attempt.ID] {
			// Without durable proof, do not abort. The closing guard prevents
			// publication while the owner's bounded attempt drains naturally.
			if _, owned := owners[internal.attempt.ID]; owned {
				uncertainAbort = true
			}
			continue
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		if internal, ok := runner.(*authoringStageRunner); ok {
			if s.authoringStopConfirmed(ctx, internal.ref, internal.attempt.ID) {
				cancel()
				continue
			}
			abortErr := runner.Abort(ctx)
			cancel()
			if abortErr != nil {
				// Preserve runners/journals and the durable pending stop. A caller
				// must not close the catalog while a late runner may still write.
				uncertainAbort = true
			}
			continue
		}
		_ = runner.Abort(ctx)
		cancel()
	}
	if uncertainAbort {
		return sdd.ErrAuthoringCancellationPending
	}
	if len(owners) > 0 {
		select {
		case <-s.drained:
		case <-time.After(2 * time.Second):
			return sdd.ErrAuthoringCancellationPending
		}
	} else {
		<-s.drained
	}
	s.mu.Lock()
	journals := make([]*eventJournal, 0, len(s.journals))
	for _, journal := range s.journals {
		journals = append(journals, journal)
	}
	clear(s.journals)
	clear(s.sessions)
	clear(s.authoringCodeOwners)
	s.mu.Unlock()
	for _, journal := range journals {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		_ = journal.shutdown(ctx)
		cancel()
	}
	return nil
}

// Public errors retain their identity without exposing adapter diagnostics.
type safeError struct {
	operation string
	cause     error
}

func (e safeError) Error() string { return e.operation + ": operation failed" }
func (e safeError) Unwrap() error { return e.cause }
func safe(operation string, err error) error {
	if err == nil {
		return nil
	}
	return safeError{operation, err}
}

func (s *Service) OpenWorkspace(path string) (WorkspaceDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorkspaceDTO{}, err
	}
	defer s.endCall()
	if strings.TrimSpace(path) == "" {
		return WorkspaceDTO{}, ErrInvalidInput
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return WorkspaceDTO{}, ErrInvalidInput
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return WorkspaceDTO{}, ErrInvalidInput
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return WorkspaceDTO{}, ErrInvalidInput
	}
	workspaceID := fmt.Sprintf("workspace-%x", sha256.Sum256([]byte(canonical)))
	workspace, err := s.store.GetWorkspace(s.ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		workspace = catalog.Workspace{ID: workspaceID, Path: canonical, Profile: "ask", CreatedAt: time.Now().UTC()}
	} else if err != nil {
		return WorkspaceDTO{}, safe("open workspace", err)
	}
	if err := s.store.UpsertWorkspace(s.ctx, workspace); err != nil {
		return WorkspaceDTO{}, safe("open workspace", err)
	}
	return WorkspaceDTO{workspace.ID, workspace.Path, workspace.Profile}, nil
}

var profileID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func clearProviderProfileInput(in *SaveProviderProfileInput) {
	in.APIKey = ""
}

func (s *Service) SaveProviderProfile(in SaveProviderProfileInput) (BackendDTO, error) {
	defer clearProviderProfileInput(&in)
	if err := s.beginCall(); err != nil {
		return BackendDTO{}, err
	}
	defer s.endCall()
	base, valid := parseProfileURL(in.BaseURL)
	keyRequired, allowed := providerEndpointPolicy(in.ProviderType, base)
	if !profileID.MatchString(in.ID) || in.ID == "codex" || in.ID == "opencode" || in.ID == "claude" || strings.TrimSpace(in.Name) == "" || in.Kind != "openai_compatible" || strings.TrimSpace(in.Model) == "" || !valid || !allowed || len(in.APIKey) > 2048 || strings.ContainsAny(in.APIKey, "\r\n") || (in.APIKey != "" && strings.TrimSpace(in.APIKey) == "") {
		return BackendDTO{}, ErrInvalidInput
	}
	s.profileGate.Lock()
	defer s.profileGate.Unlock()
	if err := s.ctx.Err(); err != nil {
		return BackendDTO{}, err
	}
	previous, err := s.store.GetProviderProfile(s.ctx, in.ID)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return BackendDTO{}, safe("save provider", err)
	}
	hasPreviousReference := exists && previous.CredentialProvider != "" && previous.CredentialAccount != ""
	previousHasPartialReference := exists && (previous.CredentialProvider == "") != (previous.CredentialAccount == "")
	originChanged := false
	if exists {
		previousBase, previousValid := parseProfileURL(previous.BaseURL)
		originChanged = !previousValid || canonicalProfileOrigin(previousBase) != canonicalProfileOrigin(base)
	}
	if in.ClearCredential {
		if !exists || !hasPreviousReference || keyRequired || in.APIKey != "" {
			return BackendDTO{}, ErrInvalidInput
		}
	} else if in.APIKey == "" {
		if previousHasPartialReference || (keyRequired && !hasPreviousReference) || (hasPreviousReference && originChanged) {
			return BackendDTO{}, ErrInvalidInput
		}
	}
	// Clearing publishes an empty active reference; historical versions remain.
	var ref secrets.Reference
	if in.APIKey == "" && !in.ClearCredential && hasPreviousReference {
		ref = secrets.Reference{Provider: previous.CredentialProvider, Account: previous.CredentialAccount}
	} else if in.APIKey != "" {
		// Publish a fresh immutable reference after writing its secret.
		// Old versions remain untouched; version/orphan collection is a future task.
		ref = secrets.Reference{Provider: in.Kind, Account: in.ID + "-" + id.New()}
		if err := s.secrets.Put(s.ctx, ref, in.APIKey); err != nil {
			return BackendDTO{}, safe("save credential", err)
		}
	}
	now := time.Now().UTC()
	created := now
	if exists {
		created = previous.CreatedAt
	}
	p := catalog.ProviderProfile{ID: in.ID, Name: in.Name, Kind: in.Kind, ProviderType: in.ProviderType, BaseURL: in.BaseURL, Model: in.Model, CredentialProvider: ref.Provider, CredentialAccount: ref.Account, CreatedAt: created, UpdatedAt: now}
	if err := s.store.PublishProviderProfile(s.ctx, p); err != nil {
		var rollback error
		if in.APIKey != "" {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
			defer cancel()
			rollback = s.secrets.Delete(ctx, ref)
		}
		return BackendDTO{}, safe("save provider", errors.Join(err, rollback))
	}
	return backendDTO(p), nil
}
func backendDTO(p catalog.ProviderProfile) BackendDTO {
	return BackendDTO{ID: p.ID, Name: p.Name, Kind: "api", Available: profileNetworkAccess(p) == nil, Capabilities: agentcore.Capabilities{Streaming: true, ToolCalls: true}}
}
