package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	mcpclient "github.com/persioflexa/harflex/internal/mcp"
	"github.com/persioflexa/harflex/internal/secrets"
)

var ErrMCPServerNotFound = errors.New("MCP server not found")
var ErrMCPConnectionFailed = errors.New("MCP connection failed")

func mcpServerDTO(item catalog.MCPServer) MCPServerDTO {
	tools := make([]MCPToolDTO, 0, len(item.Tools))
	for _, tool := range item.Tools {
		tools = append(tools, MCPToolDTO{Name: tool.Name, AgentName: tool.AgentName, Description: tool.Description, Schema: append([]byte(nil), tool.Schema...)})
	}
	return MCPServerDTO{ID: item.ID, WorkspaceID: item.WorkspaceID, Name: item.Name, Transport: item.Transport, Command: item.Command, Args: append([]string{}, item.Args...), URL: item.URL, TokenEnvVar: item.TokenEnvVar, AuthScheme: mcpAuthSchemeOf(item), Enabled: item.Enabled, Tools: tools, HasCredential: item.CredentialProvider != "" && item.CredentialAccount != "", CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

// mcpAuthSchemeOf is the scheme of a stored server; rows that predate the column are bearer servers.
func mcpAuthSchemeOf(item catalog.MCPServer) string {
	if item.AuthScheme == "" {
		return mcpclient.AuthBearer
	}
	return item.AuthScheme
}

// mcpSecretForms lists the ways a stored MCP credential can come back in text a server sends: as it is, and for
// Basic also the secret alone and the base64 of the pair, which is what an echoed Authorization header carries.
// Longer forms come first so a shorter one never leaves half of a longer one behind.
func mcpSecretForms(scheme, credential string) []string {
	if credential == "" {
		return nil
	}
	forms := []string{credential}
	if scheme == mcpclient.AuthBasic {
		forms = append(forms, base64.StdEncoding.EncodeToString([]byte(credential)))
		if _, secret, found := strings.Cut(credential, ":"); found && secret != "" {
			forms = append(forms, secret)
		}
	}
	sort.SliceStable(forms, func(a, b int) bool { return len(forms[a]) > len(forms[b]) })
	return forms
}

// scrubMCPSecrets removes every form of the credential from text before the model, the journal or the screen sees it.
func scrubMCPSecrets(text string, forms []string) string {
	for _, form := range forms {
		if len(form) >= 4 {
			text = strings.ReplaceAll(text, form, "[credencial removida]")
		}
	}
	return text
}

func safeToolName(serverID, name string) string {
	var clean strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			clean.WriteRune(r)
		} else {
			clean.WriteByte('_')
		}
		if clean.Len() >= 35 {
			break
		}
	}
	hash := sha256.Sum256([]byte(name))
	return fmt.Sprintf("mcp_%s_%s_%x", serverID[:8], clean.String(), hash[:4])
}

func toolEchoesCredential(tool mcpclient.Tool, credential string) bool {
	if credential == "" {
		return false
	}
	if strings.Contains(tool.Name, credential) || strings.Contains(tool.Description, credential) {
		return true
	}
	decoder := json.NewDecoder(bytes.NewReader(tool.Schema))
	for {
		value, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return true
		}
		if text, ok := value.(string); ok && strings.Contains(text, credential) {
			return true
		}
	}
}

func (s *Service) SaveMCPServer(in SaveMCPServerInput) (MCPServerDTO, error) {
	defer func() { in.Token = "" }()
	if err := s.beginCall(); err != nil {
		return MCPServerDTO{}, err
	}
	defer s.endCall()
	in.Name = strings.TrimSpace(in.Name)
	if in.WorkspaceID == "" || in.Name == "" || len(in.Name) > 128 || len(in.Token) > 4096 || strings.ContainsAny(in.Token, "\r\n") || (in.ID != "" && (len(in.ID) < 8 || !profileID.MatchString(in.ID))) {
		return MCPServerDTO{}, ErrInvalidInput
	}
	switch in.AuthScheme {
	case "", mcpclient.AuthBearer, mcpclient.AuthBasic:
	default:
		return MCPServerDTO{}, ErrInvalidInput
	}
	workspace, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return MCPServerDTO{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return MCPServerDTO{}, safe("get MCP workspace", err)
	}
	cfg := mcpclient.Config{Transport: in.Transport, Command: in.Command, Args: in.Args, URL: in.URL, WorkspacePath: workspace.Path, TokenEnvVar: in.TokenEnvVar, AuthScheme: in.AuthScheme, Token: in.Token}
	s.mcpGate.Lock()
	defer s.mcpGate.Unlock()
	s.profileGate.Lock()
	defer s.profileGate.Unlock()
	now := time.Now().UTC()
	item := catalog.MCPServer{ID: in.ID, WorkspaceID: in.WorkspaceID, Name: in.Name, Transport: in.Transport, Command: in.Command, Args: append([]string(nil), in.Args...), URL: in.URL, TokenEnvVar: in.TokenEnvVar, AuthScheme: in.AuthScheme, Enabled: false, Tools: []catalog.MCPTool{}, CreatedAt: now, UpdatedAt: now}
	if in.ID == "" {
		item.ID = id.New()
	} else {
		previous, err := s.store.GetMCPServer(s.ctx, in.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return MCPServerDTO{}, ErrMCPServerNotFound
		}
		if err != nil {
			return MCPServerDTO{}, safe("get MCP server", err)
		}
		if previous.WorkspaceID != in.WorkspaceID {
			return MCPServerDTO{}, ErrMCPServerNotFound
		}
		item.CreatedAt = previous.CreatedAt
		item.CredentialProvider, item.CredentialAccount = previous.CredentialProvider, previous.CredentialAccount
		if in.AuthScheme == "" {
			item.AuthScheme, cfg.AuthScheme = previous.AuthScheme, previous.AuthScheme
		}
		if cfg.Token == "" && previous.CredentialProvider != "" && previous.CredentialAccount != "" {
			if s.secrets == nil {
				return MCPServerDTO{}, errRedactionUnavailable
			}
			cfg.Token, err = s.secrets.Get(s.ctx, secrets.Reference{Provider: previous.CredentialProvider, Account: previous.CredentialAccount})
			if err != nil {
				return MCPServerDTO{}, safe("get MCP credential", err)
			}
		}
	}
	if err := mcpclient.Validate(cfg); err != nil {
		cfg.Token = ""
		return MCPServerDTO{}, ErrInvalidInput
	}
	if cfg.Token != "" {
		decodedURL, _ := url.PathUnescape(in.URL)
		if strings.Contains(in.Name, cfg.Token) || strings.Contains(in.Command, cfg.Token) || strings.Contains(decodedURL, cfg.Token) || strings.Contains(in.TokenEnvVar, cfg.Token) {
			cfg.Token = ""
			return MCPServerDTO{}, ErrInvalidInput
		}
		for _, arg := range in.Args {
			if strings.Contains(arg, cfg.Token) {
				cfg.Token = ""
				return MCPServerDTO{}, ErrInvalidInput
			}
		}
	}
	cfg.Token = ""
	var freshRef secrets.Reference
	if in.Token != "" {
		if s.secrets == nil {
			return MCPServerDTO{}, errRedactionUnavailable
		}
		freshRef = secrets.Reference{Provider: "mcp", Account: item.ID + "-" + id.New()}
		if err := s.secrets.Put(s.ctx, freshRef, in.Token); err != nil {
			return MCPServerDTO{}, safe("save MCP credential", err)
		}
		item.CredentialProvider, item.CredentialAccount = freshRef.Provider, freshRef.Account
	}
	if err := s.store.PublishMCPServer(s.ctx, item); err != nil {
		if freshRef.Provider != "" {
			_ = s.secrets.Delete(s.ctx, freshRef)
		}
		return MCPServerDTO{}, safe("save MCP server", err)
	}
	return mcpServerDTO(item), nil
}

func (s *Service) ListMCPServers(workspaceID string) ([]MCPServerDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	items, err := s.store.ListMCPServers(s.ctx, workspaceID)
	if err != nil {
		return nil, safe("list MCP servers", err)
	}
	result := make([]MCPServerDTO, 0, len(items))
	for _, item := range items {
		result = append(result, mcpServerDTO(item))
	}
	return result, nil
}

func (s *Service) mcpConfig(ctx context.Context, item catalog.MCPServer) (mcpclient.Config, error) {
	workspace, err := s.store.GetWorkspace(ctx, item.WorkspaceID)
	if err != nil {
		return mcpclient.Config{}, safe("get MCP workspace", err)
	}
	cfg := mcpclient.Config{Transport: item.Transport, Command: item.Command, Args: append([]string(nil), item.Args...), URL: item.URL, WorkspacePath: workspace.Path, TokenEnvVar: item.TokenEnvVar, AuthScheme: item.AuthScheme}
	if item.CredentialProvider != "" && item.CredentialAccount != "" {
		if s.secrets == nil {
			return mcpclient.Config{}, errRedactionUnavailable
		}
		cfg.Token, err = s.secrets.Get(ctx, secrets.Reference{Provider: item.CredentialProvider, Account: item.CredentialAccount})
		if err != nil {
			return mcpclient.Config{}, safe("get MCP credential", err)
		}
	}
	return cfg, nil
}

func (s *Service) ConnectMCPServer(id string) (MCPServerDTO, error) {
	if err := s.beginCall(); err != nil {
		return MCPServerDTO{}, err
	}
	defer s.endCall()
	if id == "" {
		return MCPServerDTO{}, ErrInvalidInput
	}
	s.mcpGate.Lock()
	defer s.mcpGate.Unlock()
	item, err := s.store.GetMCPServer(s.ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return MCPServerDTO{}, ErrMCPServerNotFound
	}
	if err != nil {
		return MCPServerDTO{}, safe("get MCP server", err)
	}
	cfg, err := s.mcpConfig(s.ctx, item)
	if err != nil {
		return MCPServerDTO{}, err
	}
	tools, err := mcpclient.Discover(s.ctx, cfg)
	if err != nil {
		cfg.Token = ""
		return MCPServerDTO{}, safe("connect MCP server", ErrMCPConnectionFailed)
	}
	for _, tool := range tools {
		if toolEchoesCredential(tool, cfg.Token) {
			cfg.Token = ""
			item.Enabled = false
			item.Tools = []catalog.MCPTool{}
			item.UpdatedAt = time.Now().UTC()
			if err := s.store.UpsertMCPServer(s.ctx, item); err != nil {
				return MCPServerDTO{}, safe("disable unsafe MCP server", err)
			}
			return MCPServerDTO{}, safe("connect MCP server", ErrMCPConnectionFailed)
		}
	}
	cfg.Token = ""
	item.Tools = make([]catalog.MCPTool, 0, len(tools))
	for _, tool := range tools {
		item.Tools = append(item.Tools, catalog.MCPTool{Name: tool.Name, AgentName: safeToolName(item.ID, tool.Name), Description: tool.Description, Schema: append([]byte(nil), tool.Schema...)})
	}
	item.Enabled = true
	item.UpdatedAt = time.Now().UTC()
	if err := s.store.UpsertMCPServer(s.ctx, item); err != nil {
		return MCPServerDTO{}, safe("publish MCP tools", err)
	}
	return mcpServerDTO(item), nil
}

func (s *Service) DisableMCPServer(id string) (MCPServerDTO, error) {
	if err := s.beginCall(); err != nil {
		return MCPServerDTO{}, err
	}
	defer s.endCall()
	s.mcpGate.Lock()
	defer s.mcpGate.Unlock()
	item, err := s.store.GetMCPServer(s.ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return MCPServerDTO{}, ErrMCPServerNotFound
	}
	if err != nil {
		return MCPServerDTO{}, safe("get MCP server", err)
	}
	item.Enabled = false
	item.UpdatedAt = time.Now().UTC()
	if err := s.store.UpsertMCPServer(s.ctx, item); err != nil {
		return MCPServerDTO{}, safe("disable MCP server", err)
	}
	return mcpServerDTO(item), nil
}
