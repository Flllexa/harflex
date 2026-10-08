package modelcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type HTTPQuery struct {
	BackendID     string
	ProviderType  string
	BaseURL       string
	InferenceKey  string
	ManagementKey string
	SearchTerm    string
}

type httpItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	OwnedBy       string `json:"owned_by"`
	ContextLength int    `json:"context_length"`
}

type lmStudioItem struct {
	Type            string          `json:"type"`
	Key             string          `json:"key"`
	DisplayName     string          `json:"display_name"`
	LoadedInstances json.RawMessage `json:"loaded_instances"`
	Capabilities    struct {
		Reasoning struct {
			AllowedOptions json.RawMessage `json:"allowed_options"`
			Default        string          `json:"default"`
		} `json:"reasoning"`
	} `json:"capabilities"`
}

type ollamaItem struct {
	Name string `json:"name"`
}

type lmStudioLoadedInstance struct {
	ID     string `json:"id"`
	Config struct {
		ContextLength int `json:"context_length"`
	} `json:"config"`
}

func decodeBoundedArray[T any](raw json.RawMessage, maxItems int) ([]T, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, ErrUnavailable
	}
	items := make([]T, 0, 32)
	for decoder.More() {
		if len(items) == maxItems {
			return nil, ErrLimit
		}
		var item T
		if err := decoder.Decode(&item); err != nil {
			return nil, ErrUnavailable
		}
		items = append(items, item)
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim(']') {
		return nil, ErrUnavailable
	}
	return items, nil
}

func optionalBoundedArray[T any](raw json.RawMessage, maxItems int) ([]T, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	return decodeBoundedArray[T](raw, maxItems)
}

func validatedLoadedInstances(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return false, ErrUnavailable
	}
	loaded := false
	for decoder.More() {
		var instance lmStudioLoadedInstance
		if err := decoder.Decode(&instance); err != nil || !safeID(instance.ID) || instance.Config.ContextLength < 1 {
			return false, ErrUnavailable
		}
		loaded = true
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim(']') {
		return false, ErrUnavailable
	}
	return loaded, nil
}

func FetchHTTPPage(ctx context.Context, client *http.Client, q HTTPQuery, cursor string, remaining int64) (Page, error) {
	if remaining < 1 || client == nil {
		return Page{}, ErrLimit
	}
	base, err := url.Parse(q.BaseURL)
	if err != nil || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery ||
		base.Fragment != "" || strings.Contains(q.BaseURL, "#") || (base.Scheme != "https" && base.Scheme != "http") {
		return Page{}, ErrUnavailable
	}

	endpoint := *base
	page := Page{}
	bearer := q.InferenceKey
	suffix := ""
	switch q.ProviderType {
	case "openai", "generic":
		suffix = "/models"
		if q.ProviderType == "openai" {
			page.Source = "openai_models"
		} else {
			page.Source = "generic_models"
		}
	case "openrouter":
		suffix = "/models"
		page.Source = "openrouter_general_unfiltered"
		if q.ManagementKey != "" {
			suffix += "/user"
			bearer = q.ManagementKey
			page.Source = "openrouter_account"
			page.AccountFiltered = true
		}
		offset := 0
		if cursor != "" {
			offset, err = strconv.Atoi(cursor)
			if err != nil || offset < 0 || offset > MaxModels {
				return Page{}, ErrUnavailable
			}
		}
		values := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"100"}}
		if !page.AccountFiltered && q.SearchTerm != "" {
			values.Set("q", q.SearchTerm)
		}
		endpoint.RawQuery = values.Encode()
	case "lm_studio":
		endpoint.Path, endpoint.RawPath = "/api/v1/models", ""
		page.Source = "lm_studio_native"
	case "ollama":
		endpoint.Path, endpoint.RawPath = "/api/tags", ""
		page.Source = "ollama_tags"
	default:
		return Page{}, ErrUnsupported
	}
	if suffix != "" {
		escaped := strings.TrimRight(base.EscapedPath(), "/") + suffix
		decoded, decodeErr := url.PathUnescape(escaped)
		if decodeErr != nil {
			return Page{}, ErrUnavailable
		}
		endpoint.Path, endpoint.RawPath = decoded, escaped
	}

	// This adapter must not follow a redirect with either credential to another origin.
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	copied.Jar = nil
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Page{}, ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := copied.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Page{}, ctx.Err()
		}
		return Page{}, ErrUnavailable
	}
	defer resp.Body.Close()
	// Error response bodies may contain provider diagnostics or credentials; never read them.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Page{}, ErrUnauthorized
	}
	if (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) && q.ProviderType == "generic" {
		return Page{}, ErrUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return Page{}, ErrUnavailable
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return Page{}, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, remaining+1))
	if err != nil {
		if ctx.Err() != nil {
			return Page{}, ctx.Err()
		}
		return Page{}, ErrUnavailable
	}
	if int64(len(body)) > remaining {
		return Page{}, ErrLimit
	}
	page.Bytes = int64(len(body))

	switch q.ProviderType {
	case "openai", "generic":
		var data struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &data) != nil {
			return Page{}, ErrUnavailable
		}
		items, err := decodeBoundedArray[httpItem](data.Data, MaxModels)
		if err != nil {
			return Page{}, err
		}
		for _, item := range items {
			page.Models = append(page.Models, Model{ID: item.ID, DisplayName: item.ID, BackendID: q.BackendID,
				Source: page.Source, Availability: "listed", OwnedBy: item.OwnedBy})
		}
	case "openrouter":
		var data struct {
			Data       json.RawMessage `json:"data"`
			TotalCount *int            `json:"total_count"`
		}
		if json.Unmarshal(body, &data) != nil || data.TotalCount == nil {
			return Page{}, ErrUnavailable
		}
		items, err := decodeBoundedArray[httpItem](data.Data, MaxModels)
		if err != nil {
			return Page{}, err
		}
		offset, _ := strconv.Atoi(cursor)
		if *data.TotalCount < offset+len(items) {
			return Page{}, ErrUnavailable
		}
		for _, item := range items {
			name := item.Name
			if name == "" {
				name = item.ID
			}
			availability := "listed"
			if !page.AccountFiltered {
				availability = "listed_unfiltered"
			}
			page.Models = append(page.Models, Model{ID: item.ID, DisplayName: name, BackendID: q.BackendID,
				Source: page.Source, Availability: availability, ContextLength: item.ContextLength})
		}
		if offset+len(items) < *data.TotalCount {
			page.NextCursor = strconv.Itoa(offset + len(items))
		}
	case "lm_studio":
		var data struct {
			Models json.RawMessage `json:"models"`
		}
		if json.Unmarshal(body, &data) != nil {
			return Page{}, ErrUnavailable
		}
		items, err := decodeBoundedArray[lmStudioItem](data.Models, MaxModels)
		if err != nil {
			return Page{}, err
		}
		for _, item := range items {
			if item.Type != "llm" {
				continue
			}
			loaded, err := validatedLoadedInstances(item.LoadedInstances)
			if err != nil {
				return Page{}, err
			}
			efforts, err := optionalBoundedArray[string](item.Capabilities.Reasoning.AllowedOptions, 32)
			if err != nil {
				return Page{}, err
			}
			name := item.DisplayName
			if name == "" {
				name = item.Key
			}
			page.Models = append(page.Models, Model{ID: item.Key, DisplayName: name, BackendID: q.BackendID,
				Source: page.Source, Availability: "listed", Loaded: &loaded,
				SupportedReasoningEfforts: efforts,
				DefaultReasoningEffort:    item.Capabilities.Reasoning.Default})
		}
	case "ollama":
		var data struct {
			Models json.RawMessage `json:"models"`
		}
		if json.Unmarshal(body, &data) != nil {
			return Page{}, ErrUnavailable
		}
		items, err := decodeBoundedArray[ollamaItem](data.Models, MaxModels)
		if err != nil {
			return Page{}, err
		}
		for _, item := range items {
			page.Models = append(page.Models, Model{ID: item.Name, DisplayName: item.Name,
				BackendID: q.BackendID, Source: page.Source, Availability: "listed"})
		}
	}
	return page, nil
}
