package externalagent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/modelcatalog"
)

const maxCodexCatalogFrames = 256

type codexCatalogFrame struct {
	data []byte
	err  error
}

type codexCatalogProtocol struct {
	encoder *json.Encoder
	frames  <-chan codexCatalogFrame
	nextID  int
}

func validCatalogText(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func codexCatalogFailure(code string) modelcatalog.Result {
	return modelcatalog.Result{BackendID: "codex", Status: modelcatalog.StatusFailed, ErrorCode: code, CheckedAt: time.Now().UTC(), Models: []modelcatalog.Model{}}
}

func (a *cliAdapter) QueryModels(parent context.Context, cwd string) (modelcatalog.Result, error) {
	switch a.id {
	case "codex":
		return a.queryCodexModels(parent, cwd)
	case "opencode":
		return a.queryOpenCodeModels(parent, cwd)
	case "claude":
		return a.queryClaudeModels(parent, cwd)
	default:
		return modelcatalog.Result{BackendID: a.id, Status: modelcatalog.StatusUnsupported, ErrorCode: "catalog_unsupported"}, nil
	}
}

func (a *cliAdapter) queryCodexModels(parent context.Context, cwd string) (modelcatalog.Result, error) {
	if parent == nil {
		return codexCatalogFailure("catalog_workspace_unavailable"), nil
	}
	if err := parent.Err(); err != nil {
		result := codexCatalogFailure("catalog_cancelled")
		result.Status = modelcatalog.StatusInterrupted
		return result, nil
	}
	if !filepath.IsAbs(cwd) {
		return codexCatalogFailure("catalog_workspace_unavailable"), nil
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return codexCatalogFailure("catalog_workspace_unavailable"), nil
	}
	path, err := a.executable()
	if err != nil {
		return codexCatalogFailure("catalog_cli_unavailable"), nil
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	cmd := exec.Command(path, "app-server", "--stdio")
	cmd.Dir = cwd
	cmd.Env = buildEnvironment("codex", os.Environ(), runtime.GOOS)
	cmd.WaitDelay = 250 * time.Millisecond
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return codexCatalogFailure("catalog_cli_unavailable"), nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return codexCatalogFailure("catalog_cli_unavailable"), nil
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return codexCatalogFailure("catalog_cli_unavailable"), nil
	}
	owned, err := ownCommand(cmd)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return codexCatalogFailure("catalog_cli_unavailable"), nil
	}
	defer owned.close()
	if err := owned.start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return codexCatalogFailure("catalog_cli_unavailable"), nil
	}
	frames := make(chan codexCatalogFrame)
	readerDone := make(chan struct{})
	go readCodexCatalogFrames(ctx, stdout, frames, readerDone)
	stderrDone := make(chan struct{})
	go func() { defer close(stderrDone); _, _ = io.Copy(io.Discard, stderr) }()
	protocol := codexCatalogProtocol{encoder: json.NewEncoder(stdin), frames: frames, nextID: 1}
	result := protocol.run(ctx)
	catalogCanceled := ctx.Err() != nil
	_ = stdin.Close()
	// The app-server should exit on EOF. A bounded owner wait terminates its
	// process group if it does not; no child is left behind on success or error.
	cleanupCtx, stop := context.WithTimeout(context.Background(), 750*time.Millisecond)
	cleanupErr := owned.wait(cleanupCtx)
	stop()
	cancel()
	_ = stdout.Close()
	_ = stderr.Close()
	select {
	case <-readerDone:
	case <-time.After(250 * time.Millisecond):
		result.Status, result.Complete, result.ErrorCode = modelcatalog.StatusFailed, false, "catalog_process_unconfirmed"
	}
	select {
	case <-stderrDone:
	case <-time.After(250 * time.Millisecond):
		result.Status, result.Complete, result.ErrorCode = modelcatalog.StatusFailed, false, "catalog_process_unconfirmed"
	}
	if cleanupErr != nil && !catalogCanceled && result.Status != modelcatalog.StatusInterrupted {
		result.Status, result.Complete, result.ErrorCode = modelcatalog.StatusFailed, false, "catalog_process_unconfirmed"
	}
	if catalogCanceled && result.Status != modelcatalog.StatusFailed {
		result.Status, result.Complete, result.ErrorCode = modelcatalog.StatusInterrupted, false, "catalog_cancelled"
	}
	return result, nil
}

func readCodexCatalogFrames(ctx context.Context, stdout io.Reader, out chan<- codexCatalogFrame, done chan<- struct{}) {
	defer close(done)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), int(modelcatalog.MaxBytes)+2)
	var total int64
	count := 0
	for scanner.Scan() {
		count++
		total += int64(len(scanner.Bytes()) + 1)
		if count > maxCodexCatalogFrames || total > modelcatalog.MaxBytes {
			select {
			case out <- codexCatalogFrame{err: modelcatalog.ErrLimit}:
			case <-ctx.Done():
			}
			return
		}
		frame := bytes.Clone(scanner.Bytes())
		select {
		case out <- codexCatalogFrame{data: frame}:
		case <-ctx.Done():
			return
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	} else {
		// Scanner's bounded token failed (or the stream could not be read).
		// Never classify an incomplete oversized frame as a complete catalog.
		err = modelcatalog.ErrLimit
	}
	select {
	case out <- codexCatalogFrame{err: err}:
	case <-ctx.Done():
	}
}

func (p *codexCatalogProtocol) send(method string, id int, params any) error {
	request := map[string]any{"method": method, "params": params}
	if id != 0 {
		request["id"] = id
	}
	return p.encoder.Encode(request)
}

func (p *codexCatalogProtocol) await(ctx context.Context, wantID int) (json.RawMessage, int64, error) {
	for {
		var frame codexCatalogFrame
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case frame = <-p.frames:
		}
		if frame.err != nil {
			return nil, 0, frame.err
		}
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(frame.data, &envelope) != nil {
			return nil, 0, modelcatalog.ErrUnavailable
		}
		if envelope.Method != "" {
			if len(envelope.ID) != 0 {
				// A catalog request never grants permission for a server-initiated action.
				return nil, 0, modelcatalog.ErrUnavailable
			}
			continue
		}
		var gotID int
		if len(envelope.ID) == 0 || json.Unmarshal(envelope.ID, &gotID) != nil || gotID != wantID ||
			len(envelope.Error) != 0 && !bytes.Equal(envelope.Error, []byte("null")) || len(envelope.Result) == 0 {
			return nil, 0, modelcatalog.ErrUnavailable
		}
		return envelope.Result, int64(len(frame.data) + 1), nil
	}
}

func (p *codexCatalogProtocol) run(ctx context.Context) modelcatalog.Result {
	result := codexCatalogFailure("catalog_unavailable")
	initID := p.nextID
	p.nextID++
	if err := p.send("initialize", initID, map[string]any{"clientInfo": map[string]string{"name": "harflex", "title": "Harflex", "version": "0.1.0"}}); err != nil {
		return result
	}
	if _, _, err := p.await(ctx, initID); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result.Status, result.ErrorCode = modelcatalog.StatusInterrupted, "catalog_cancelled"
		}
		return result
	}
	if err := p.send("initialized", 0, map[string]any{}); err != nil {
		return result
	}
	result = modelcatalog.Collect(ctx, func(pageCtx context.Context, cursor string, remaining int64) (modelcatalog.Page, error) {
		id := p.nextID
		p.nextID++
		params := map[string]any{"limit": 100, "includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := p.send("model/list", id, params); err != nil {
			if pageCtx.Err() != nil {
				return modelcatalog.Page{}, pageCtx.Err()
			}
			return modelcatalog.Page{}, modelcatalog.ErrUnavailable
		}
		raw, size, err := p.await(pageCtx, id)
		if err != nil {
			return modelcatalog.Page{}, err
		}
		if size > remaining {
			return modelcatalog.Page{}, modelcatalog.ErrLimit
		}
		return decodeCodexModelPage(raw, size)
	})
	result.BackendID = "codex"
	result.Destination = "Codex CLI"
	if result.Complete {
		fingerprint, err := p.readEffectiveContext(ctx)
		if err != nil {
			result.Models = nil
			result.Complete = false
			result.Status, result.ErrorCode = modelcatalog.StatusFailed, "catalog_context_unverified"
			if ctx.Err() != nil {
				result.Status, result.ErrorCode = modelcatalog.StatusInterrupted, "catalog_cancelled"
			}
		} else {
			result.ProfileRevision = fingerprint
		}
	}
	return result
}

func (p *codexCatalogProtocol) readEffectiveContext(ctx context.Context) (string, error) {
	read := func(method string, params any) (map[string]json.RawMessage, error) {
		id := p.nextID
		p.nextID++
		if err := p.send(method, id, params); err != nil {
			return nil, modelcatalog.ErrUnavailable
		}
		raw, _, err := p.await(ctx, id)
		if err != nil {
			return nil, err
		}
		var result map[string]json.RawMessage
		if json.Unmarshal(raw, &result) != nil || result == nil {
			return nil, modelcatalog.ErrUnavailable
		}
		return result, nil
	}
	config, err := read("config/read", map[string]any{"includeLayers": false})
	if err != nil || len(config["config"]) == 0 || bytes.TrimSpace(config["config"])[0] != '{' {
		return "", modelcatalog.ErrUnavailable
	}
	account, err := read("account/read", map[string]any{"refreshToken": false})
	if err != nil || len(account["account"]) == 0 || len(account["requiresOpenaiAuth"]) == 0 {
		return "", modelcatalog.ErrUnavailable
	}
	var requires bool
	if json.Unmarshal(account["requiresOpenaiAuth"], &requires) != nil || bytes.Equal(bytes.TrimSpace(account["account"]), []byte("null")) {
		return "", modelcatalog.ErrUnavailable
	}
	// Re-encoding parsed JSON sorts object keys. Only the digest crosses the
	// adapter boundary; raw account and effective config never reach UI/logs.
	canonical := make([][]byte, 0, 2)
	for _, raw := range []json.RawMessage{config["config"], account["account"]} {
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return "", modelcatalog.ErrUnavailable
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", modelcatalog.ErrUnavailable
		}
		canonical = append(canonical, encoded)
	}
	digest := sha256.Sum256(bytes.Join(canonical, []byte{0}))
	return hex.EncodeToString(digest[:]), nil
}

func decodeCodexModelPage(raw json.RawMessage, size int64) (modelcatalog.Page, error) {
	var payload struct {
		Data       json.RawMessage `json:"data"`
		NextCursor *string         `json:"nextCursor"`
	}
	if json.Unmarshal(raw, &payload) != nil || len(payload.Data) == 0 {
		return modelcatalog.Page{}, modelcatalog.ErrUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(payload.Data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return modelcatalog.Page{}, modelcatalog.ErrUnavailable
	}
	page := modelcatalog.Page{Source: "codex_app_server", AccountFiltered: true, Bytes: size}
	count := 0
	for decoder.More() {
		count++
		if count > modelcatalog.MaxModels {
			return modelcatalog.Page{}, modelcatalog.ErrLimit
		}
		var item struct {
			ID                     string          `json:"id"`
			Model                  string          `json:"model"`
			DisplayName            string          `json:"displayName"`
			Hidden                 bool            `json:"hidden"`
			SupportedReasoningRaw  json.RawMessage `json:"supportedReasoningEfforts"`
			DefaultReasoningEffort string          `json:"defaultReasoningEffort"`
		}
		if decoder.Decode(&item) != nil || !validCatalogText(item.ID, 512) || item.ID == "" ||
			!validCatalogText(item.Model, 512) || item.Model == "" || strings.HasPrefix(item.Model, "-") ||
			!validCatalogText(item.DisplayName, 512) || !validCatalogText(item.DefaultReasoningEffort, 64) {
			return modelcatalog.Page{}, modelcatalog.ErrUnavailable
		}
		if item.Hidden {
			continue
		}
		efforts, err := decodeCodexEfforts(item.SupportedReasoningRaw)
		if err != nil {
			return modelcatalog.Page{}, err
		}
		defaultEffort := item.DefaultReasoningEffort
		if defaultEffort != "" {
			found := false
			for _, effort := range efforts {
				found = found || effort == defaultEffort
			}
			if !found {
				defaultEffort = "" // Never advertise an unverified default.
			}
		}
		name := item.DisplayName
		if name == "" {
			name = item.Model
		}
		page.Models = append(page.Models, modelcatalog.Model{ID: item.Model, DisplayName: name, BackendID: "codex", Source: page.Source,
			Availability: "listed", SupportedReasoningEfforts: efforts, DefaultReasoningEffort: defaultEffort})
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim(']') {
		return modelcatalog.Page{}, modelcatalog.ErrUnavailable
	}
	if payload.NextCursor != nil {
		if !validCatalogText(*payload.NextCursor, 512) {
			return modelcatalog.Page{}, modelcatalog.ErrUnavailable
		}
		page.NextCursor = *payload.NextCursor
	}
	return page, nil
}

func decodeCodexEfforts(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, modelcatalog.ErrUnavailable
	}
	values := make([]string, 0, 4)
	for decoder.More() {
		if len(values) == 32 {
			return nil, modelcatalog.ErrLimit
		}
		var item struct {
			ReasoningEffort string `json:"reasoningEffort"`
		}
		if decoder.Decode(&item) != nil || item.ReasoningEffort == "" || !validCatalogText(item.ReasoningEffort, 64) {
			return nil, modelcatalog.ErrUnavailable
		}
		values = append(values, item.ReasoningEffort)
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim(']') {
		return nil, modelcatalog.ErrUnavailable
	}
	return values, nil
}
