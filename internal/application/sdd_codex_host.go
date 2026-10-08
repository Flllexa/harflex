package application

import (
	"context"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/security"
	"github.com/persioflexa/harflex/internal/tools"
)

const codexHostRevisionPrefix = "codex-host-v1:"
const claudeHostRevisionPrefix = "claude-host-v1:"
const codexHostRunTimeout = 10 * time.Minute

// QA installs dependencies and runs builds and E2E tests, which take longer than writing a document or code.
func hostRunTimeout(mode string) time.Duration {
	if mode == "evaluation" {
		return 40 * time.Minute
	}
	return codexHostRunTimeout
}

// documentCLISources lists the CLIs that can work as a model only, for the SDD phases, with the catalog source that
// lists their models. The Harflex keeps the tools; the CLI gets none of its own.
var documentCLISources = map[string]string{"codex": "codex_app_server", "claude": "claude_cli"}

func documentCLI(backendID string) bool {
	_, ok := documentCLISources[backendID]
	return ok
}

func hostRevisionPrefix(backendID string) string {
	if backendID == "claude" {
		return claudeHostRevisionPrefix
	}
	return codexHostRevisionPrefix
}

func codexDocumentContractAvailable(adapter ExternalBackend, detected externalagent.Detection) bool {
	if adapter == nil || !documentCLI(adapter.ID()) || !detected.Available {
		return false
	}
	if _, ok := adapter.(externalagent.DocumentGenerator); !ok {
		return false
	}
	if capability, ok := adapter.(interface {
		DocumentContractSupported(externalagent.Detection) bool
	}); ok {
		return capability.DocumentContractSupported(detected)
	}
	return false
}

func (s *Service) codexHostModeSupported(backendID, mode string) bool {
	if !documentCLI(backendID) || (mode != "sdd_code" && mode != "evaluation") {
		return false
	}
	adapter := s.external[backendID]
	if _, ok := adapter.(externalagent.DocumentGenerator); !ok {
		return false
	}
	return codexDocumentContractAvailable(adapter, adapter.Detect())
}

func isCodexHostSession(record catalog.SessionRecord) bool {
	return documentCLI(record.BackendID) && (record.Mode == "sdd_code" || record.Mode == "evaluation") && strings.HasPrefix(record.BackendRevision, hostRevisionPrefix(record.BackendID))
}

func (s *Service) makeCodexHostRunner(record *catalog.SessionRecord, workspace catalog.Workspace, history restoredHistory, selection *catalog.ModelSelection, executionRoot string, journal *eventJournal) (sessionRunner, error) {
	adapter := s.external[record.BackendID]
	generator, ok := adapter.(externalagent.DocumentGenerator)
	if !ok || selection == nil || executionRoot == "" || !s.codexHostModeSupported(record.BackendID, record.Mode) {
		return nil, ErrSDDCLIReadIsolationUnavailable
	}
	detected := adapter.Detect()
	revision, err := cliCatalogRevision(record.BackendID, detected.Path, detected.Version, workspace.Path)
	if err != nil || !detected.Available || selection.SessionID != record.ID || selection.BackendID != record.BackendID || selection.Source != documentCLISources[record.BackendID] || selection.Status != "listed" ||
		selection.WorkspacePath != workspace.Path || selection.ExecutablePath != detected.Path || selection.ExecutableVersion != detected.Version || selection.LocalRevision != revision ||
		selection.Destination != "" || selection.CredentialIdentity != "" || selection.ConfirmUnfiltered || selection.ConfirmUnverifiedManual || selection.ConfirmJITLoad {
		return nil, ErrBackendChanged
	}
	backendRevision := hostRevisionPrefix(record.BackendID) + revision
	if record.BackendRevision != "" && record.BackendRevision != backendRevision {
		return nil, ErrBackendChanged
	}
	guard, err := tools.NewPathGuard(executionRoot)
	if err != nil {
		return nil, safe("open isolated Code tools", err)
	}
	items := []tools.Tool{tools.NewReadTool(guard), tools.NewListTool(guard), tools.NewFindTool(guard), tools.NewGrepTool(guard)}
	if record.Mode == "sdd_code" {
		items = append(items, tools.NewWriteTool(guard), tools.NewEditTool(guard))
	}
	if record.Mode == "evaluation" && isQALabRoot(executionRoot) {
		items = append(items, tools.NewShellTool(tools.ShellConfig{CWD: executionRoot, DefaultTimeout: 10 * time.Minute}))
	}
	registry, err := tools.NewRegistry(items...)
	if err != nil {
		return nil, safe("create isolated Code tools", err)
	}
	provider := externalagent.NewDocumentProvider(generator, externalagent.DocumentRequest{Model: selection.ModelID, ReasoningEffort: selection.ReasoningEffort,
		ExpectedExecutablePath: selection.ExecutablePath, ExpectedExecutableVersion: selection.ExecutableVersion, MaxAssistantOutputBytes: catalog.MaxPipelineDesignDocumentBytes})
	core, err := agentcore.RestoreSessionWithLimit(record.ID, modelProvider{Provider: provider, model: selection.ModelID}, registry, journal, pipelinePolicyProfile(record.Mode, security.Profile(workspace.Profile)), history.messages, pipelineRoleOutputTokens)
	if err != nil {
		return nil, ErrSessionCorrupt
	}
	core.SetPolicyGuard(func(ctx context.Context, risk security.Risk, action func(security.Decision) error) error {
		s.workspacePolicyGate.RLock()
		defer s.workspacePolicyGate.RUnlock()
		current, err := s.store.GetWorkspace(ctx, workspace.ID)
		if err != nil {
			return safe("read isolated Code policy", err)
		}
		return action(security.Decide(security.Request{Profile: pipelinePolicyProfile(record.Mode, security.Profile(current.Profile)), Risk: risk, WithinRoot: true, SandboxReady: false}))
	})
	record.BackendRevision = backendRevision
	selection.MaxOutputTokens = pipelineRoleOutputTokens
	selection.MaxAssistantOutputBytes = catalog.MaxPipelineDesignDocumentBytes
	return &selectedCLIRunner{base: core, service: s, selection: *selection, workspace: workspace, timeout: hostRunTimeout(record.Mode)}, nil
}
