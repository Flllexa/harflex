package application

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/persioflexa/harflex/internal/catalog"
)

var ErrProviderEndpointBlocked = errors.New("provider endpoint requires HTTPS or literal loopback")

func parseProfileURL(raw string) (*url.URL, bool) {
	base, err := url.Parse(raw)
	if err != nil || base == nil || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || strings.Contains(raw, "#") {
		return nil, false
	}
	host := base.Hostname()
	if strings.Contains(host, "%") {
		return nil, false
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, false
	}
	if port := base.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, false
		}
	}
	return base, true
}

func canonicalProfileOrigin(base *url.URL) string {
	scheme := strings.ToLower(base.Scheme)
	port := base.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	} else {
		n, _ := strconv.Atoi(port)
		port = strconv.Itoa(n)
	}
	// Fold only ASCII DNS letters; Unicode case folding can alias distinct origins.
	host := []byte(base.Hostname())
	for i, c := range host {
		if c >= 'A' && c <= 'Z' {
			host[i] = c - 'A' + 'a'
		}
	}
	return scheme + "://" + net.JoinHostPort(string(host), port)
}

func providerEndpointPolicy(providerType string, base *url.URL) (keyRequired, allowed bool) {
	if base == nil {
		return false, false
	}
	ip := net.ParseIP(base.Hostname())
	loopback := ip != nil && ip.IsLoopback()
	switch providerType {
	case "openai", "openrouter":
		return true, base.Scheme == "https"
	case "lm_studio", "ollama":
		return false, loopback && (base.Scheme == "http" || base.Scheme == "https")
	case "generic":
		if loopback {
			return false, base.Scheme == "http" || base.Scheme == "https"
		}
		return true, base.Scheme == "https"
	default:
		return false, false
	}
}

func profileNetworkAccess(profile catalog.ProviderProfile) error {
	base, valid := parseProfileURL(profile.BaseURL)
	if profile.Kind != "openai_compatible" || !valid {
		return ErrProviderEndpointBlocked
	}
	keyRequired, allowed := providerEndpointPolicy(profile.ProviderType, base)
	if !allowed {
		return ErrProviderEndpointBlocked
	}
	hasProvider := profile.CredentialProvider != ""
	hasAccount := profile.CredentialAccount != ""
	if hasProvider != hasAccount || (keyRequired && !hasProvider) {
		return ErrBackendNotFound
	}
	return nil
}
