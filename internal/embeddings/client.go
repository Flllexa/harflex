package embeddings

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Kind string

const (
	Ollama           Kind = "ollama"
	LMStudio         Kind = "lm_studio"
	MaxBatchSize          = 8
	MaxTextBytes          = 8192
	MaxDimensions         = 8192
	MaxResponseBytes      = 4 * 1024 * 1024
)

var ErrInvalidProfile = errors.New("invalid local embedding profile")
var ErrInvalidResponse = errors.New("invalid local embedding response")

type Profile struct {
	Kind        Kind   `json:"kind"`
	BaseURL     string `json:"baseUrl"`
	Model       string `json:"model"`
	Fingerprint string `json:"fingerprint"`
}

func ValidateProfile(profile Profile) (Profile, error) {
	if profile.Kind != Ollama && profile.Kind != LMStudio || len(profile.Model) == 0 || len(profile.Model) > 128 || !utf8.ValidString(profile.Model) || strings.TrimSpace(profile.Model) != profile.Model || strings.IndexFunc(profile.Model, unicode.IsSpace) >= 0 || strings.IndexFunc(profile.Model, unicode.IsControl) >= 0 {
		return Profile{}, ErrInvalidProfile
	}
	u, err := url.Parse(profile.BaseURL)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return Profile{}, ErrInvalidProfile
	}
	addr, err := netip.ParseAddr(u.Hostname())
	if err != nil || !addr.IsLoopback() || addr.Zone() != "" {
		return Profile{}, ErrInvalidProfile
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return Profile{}, ErrInvalidProfile
	}
	profile.BaseURL = "http://" + net.JoinHostPort(addr.String(), strconv.Itoa(port))
	sum := sha256.Sum256([]byte(string(profile.Kind) + "\x00" + profile.BaseURL + "\x00" + profile.Model))
	profile.Fingerprint = hex.EncodeToString(sum[:])
	return profile, nil
}

type Client struct {
	profile Profile
	http    *http.Client
}

func NewClient(profile Profile) *Client {
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: 20 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, ErrInvalidProfile
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !ip.IsLoopback() {
				return nil, ErrInvalidProfile
			}
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			remote, ok := conn.RemoteAddr().(*net.TCPAddr)
			if !ok || !remote.IP.IsLoopback() {
				_ = conn.Close()
				return nil, ErrInvalidProfile
			}
			return conn, nil
		},
	}
	return &Client{profile: profile, http: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) Embed(ctx context.Context, inputs []string) ([][]float64, error) {
	if len(inputs) == 0 || len(inputs) > MaxBatchSize {
		return nil, ErrInvalidResponse
	}
	for _, input := range inputs {
		if len(input) == 0 || len(input) > MaxTextBytes || !utf8.ValidString(input) || strings.IndexByte(input, 0) >= 0 {
			return nil, ErrInvalidResponse
		}
	}
	profile, err := ValidateProfile(c.profile)
	if err != nil {
		return nil, err
	}
	path := "/api/embed"
	if profile.Kind == LMStudio {
		path = "/v1/embeddings"
	}
	body, err := json.Marshal(struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}{profile.Model, inputs})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, profile.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request local embeddings: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local embeddings status %d: %w", response.StatusCode, ErrInvalidResponse)
	}
	limited := io.LimitReader(response.Body, MaxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read local embeddings: %w", err)
	}
	if len(data) > MaxResponseBytes {
		return nil, ErrInvalidResponse
	}
	var vectors [][]float64
	if profile.Kind == Ollama {
		var result struct {
			Embeddings [][]float64 `json:"embeddings"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, ErrInvalidResponse
		}
		vectors = result.Embeddings
	} else {
		var result struct {
			Data []struct {
				Index     *int      `json:"index"`
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &result); err != nil || len(result.Data) != len(inputs) {
			return nil, ErrInvalidResponse
		}
		vectors = make([][]float64, len(inputs))
		for _, item := range result.Data {
			if item.Index == nil || *item.Index < 0 || *item.Index >= len(inputs) || vectors[*item.Index] != nil {
				return nil, ErrInvalidResponse
			}
			vectors[*item.Index] = item.Embedding
		}
	}
	if len(vectors) != len(inputs) {
		return nil, ErrInvalidResponse
	}
	dimension := len(vectors[0])
	if dimension == 0 || dimension > MaxDimensions {
		return nil, ErrInvalidResponse
	}
	for _, vector := range vectors {
		if len(vector) != dimension {
			return nil, ErrInvalidResponse
		}
		var norm float64
		for _, value := range vector {
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > math.MaxFloat32 {
				return nil, ErrInvalidResponse
			}
			norm += value * value
		}
		if norm == 0 || math.IsInf(norm, 0) {
			return nil, ErrInvalidResponse
		}
	}
	return vectors, nil
}
