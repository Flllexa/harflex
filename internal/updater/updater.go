// Package updater finds a newer Harflex release on GitHub, downloads its installer for this platform, checks it
// against the checksums published with the release, and hands it to the platform step that swaps the app.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultRepo    = "Flllexa/harflex"
	defaultAPIBase = "https://api.github.com"
	maxAssetBytes  = 1 << 30
	maxSumsBytes   = 1 << 20
	maxAPIBytes    = 4 << 20
)

var (
	// ErrNoInstaller says the release has nothing this platform can install by itself; the release page is the way.
	ErrNoInstaller = errors.New("the release has no installer this platform can apply")
	// ErrChecksum says the download does not match the checksum published with the release.
	ErrChecksum = errors.New("the download does not match the release checksum")
	// ErrNotInstalled says this run is not an installed copy (a development build or a package manager's), so it cannot replace itself.
	ErrNotInstalled = errors.New("this copy of the app cannot replace itself")
)

// Release is a published version newer than the running one.
type Release struct {
	Version     string // X.Y.Z, without the leading v
	PageURL     string
	AssetName   string // the installer for this platform, empty when there is none
	AssetURL    string
	ChecksumURL string
}

// CanInstall reports whether the release carries an installer for this platform.
func (r Release) CanInstall() bool {
	return r.AssetName != "" && r.AssetURL != "" && r.ChecksumURL != ""
}

// Source reads releases of one repository.
type Source struct {
	Client      *http.Client
	Repo        string // owner/name
	APIBase     string
	AssetPrefix string // asset downloads must start with it
	OS, Arch    string
}

// NewSource reads the Harflex releases for the given platform.
func NewSource(goos, goarch string) Source {
	return Source{Client: http.DefaultClient, Repo: defaultRepo, APIBase: defaultAPIBase, AssetPrefix: "https://github.com/" + defaultRepo + "/releases/download/", OS: goos, Arch: goarch}
}

type apiRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Latest returns the newest release when it is newer than current, and nil when current is the latest.
func (s Source) Latest(ctx context.Context, current string) (*Release, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.APIBase, "/")+"/repos/"+s.Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Harflex-updater")
	response, err := s.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil // no release published yet
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release lookup answered %d", response.StatusCode)
	}
	var found apiRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, maxAPIBytes)).Decode(&found); err != nil {
		return nil, fmt.Errorf("read release: %w", err)
	}
	version := strings.TrimPrefix(found.TagName, "v")
	if found.Draft || found.Prerelease || !Newer(current, version) {
		return nil, nil
	}
	release := &Release{Version: version, PageURL: found.HTMLURL}
	wanted := installerSuffix(s.OS, s.Arch)
	for _, asset := range found.Assets {
		if !strings.HasPrefix(asset.URL, s.AssetPrefix) {
			continue
		}
		switch {
		case wanted != "" && strings.HasSuffix(asset.Name, wanted):
			release.AssetName, release.AssetURL = asset.Name, asset.URL
		case asset.Name == "SHA256SUMS.txt":
			release.ChecksumURL = asset.URL
		}
	}
	return release, nil
}

// installerSuffix is the end of the installer's name in a release for the platform, as the release workflow names it.
func installerSuffix(goos, goarch string) string {
	switch {
	case goos == "darwin":
		return "-macos-universal.dmg"
	case goos == "windows" && goarch == "amd64":
		return "-windows-amd64-setup.exe"
	case goos == "linux" && goarch == "amd64":
		return "-linux-x86_64.AppImage"
	}
	return ""
}

// Newer reports whether candidate is a higher X.Y.Z than current. Anything that is not X.Y.Z (a development build) is never updated.
func Newer(current, candidate string) bool {
	a, okA := parseVersion(current)
	b, okB := parseVersion(candidate)
	if !okA || !okB {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return b[i] > a[i]
		}
	}
	return false
}

func parseVersion(value string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || part != strconv.Itoa(n) {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Progress is told how much of the installer has arrived, 0 to 100.
type Progress func(percent int)

// Download fetches the release's installer into dir and returns its path, after the checksum published with the release matches.
func (s Source) Download(ctx context.Context, release Release, dir string, progress Progress) (string, error) {
	if !release.CanInstall() {
		return "", ErrNoInstaller
	}
	if !strings.HasPrefix(release.AssetURL, s.AssetPrefix) || !strings.HasPrefix(release.ChecksumURL, s.AssetPrefix) || filepath.Base(release.AssetName) != release.AssetName {
		return "", ErrNoInstaller
	}
	sums, err := s.fetchSmall(ctx, release.ChecksumURL)
	if err != nil {
		return "", err
	}
	want := checksumFor(sums, release.AssetName)
	if want == "" {
		return "", ErrChecksum
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, release.AssetName)
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return "", err
	}
	got, err := s.copyAsset(ctx, release.AssetURL, file, progress)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(target)
		return "", err
	}
	if got != want {
		_ = os.Remove(target)
		return "", ErrChecksum
	}
	return target, nil
}

func (s Source) open(ctx context.Context, rawURL string) (*http.Response, error) {
	if s.Client == nil {
		s.Client = http.DefaultClient
	}
	if parsed, err := url.Parse(rawURL); err != nil || parsed.Scheme != "https" && !strings.HasPrefix(rawURL, s.AssetPrefix) {
		return nil, ErrNoInstaller
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Harflex-updater")
	response, err := s.Client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("download answered %d", response.StatusCode)
	}
	return response, nil
}

func (s Source) fetchSmall(ctx context.Context, rawURL string) (string, error) {
	response, err := s.open(ctx, rawURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSumsBytes))
	return string(body), err
}

func (s Source) copyAsset(ctx context.Context, rawURL string, dst io.Writer, progress Progress) (string, error) {
	response, err := s.open(ctx, rawURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	hash := sha256.New()
	counter := &countWriter{total: response.ContentLength, progress: progress}
	n, err := io.Copy(io.MultiWriter(dst, hash, counter), io.LimitReader(response.Body, maxAssetBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxAssetBytes {
		return "", errors.New("the installer is larger than expected")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type countWriter struct {
	total, seen int64
	last        int
	progress    Progress
}

func (c *countWriter) Write(p []byte) (int, error) {
	c.seen += int64(len(p))
	if c.progress != nil && c.total > 0 {
		if percent := int(c.seen * 100 / c.total); percent != c.last {
			c.last = percent
			c.progress(percent)
		}
	}
	return len(p), nil
}

// checksumFor reads a sha256sum listing ("<hash>  <name>") and returns the hash of name, lowercase.
func checksumFor(listing, name string) string {
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}
