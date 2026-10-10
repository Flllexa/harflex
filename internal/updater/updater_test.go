package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewerComparesPlainVersionsOnly(t *testing.T) {
	for _, test := range []struct {
		current, candidate string
		want               bool
	}{
		{"0.2.2", "0.2.3", true}, {"0.2.2", "0.10.0", true}, {"0.9.9", "1.0.0", true}, {"v0.2.2", "v0.2.3", true},
		{"0.2.2", "0.2.2", false}, {"0.2.3", "0.2.2", false}, {"1.0.0", "0.99.99", false},
		{"dev", "0.2.3", false}, {"0.2.2", "0.2.3-rc1", false}, {"0.2", "0.2.3", false}, {"0.2.02", "0.2.3", false},
	} {
		if got := Newer(test.current, test.candidate); got != test.want {
			t.Fatalf("Newer(%q, %q) = %v, want %v", test.current, test.candidate, got, test.want)
		}
	}
}

// release serves a GitHub-shaped release with one installer per platform and the checksum list.
func release(t *testing.T, tag string, payload []byte, mutate func(sum *string)) (Source, *httptest.Server) {
	t.Helper()
	hash := sha256.Sum256(payload)
	sum := hex.EncodeToString(hash[:])
	if mutate != nil {
		mutate(&sum)
	}
	var server *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/app/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		base := server.URL + "/dl/"
		fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://example.test/release","assets":[
			{"name":"Harflex-0.3.0-macos-universal.dmg","browser_download_url":%q},
			{"name":"Harflex-0.3.0-windows-amd64-setup.exe","browser_download_url":%q},
			{"name":"Harflex-0.3.0-linux-x86_64.AppImage","browser_download_url":%q},
			{"name":"Harflex-0.3.0-linux-amd64.deb","browser_download_url":%q},
			{"name":"SHA256SUMS.txt","browser_download_url":%q},
			{"name":"Elsewhere.dmg-macos-universal.dmg","browser_download_url":"https://evil.test/x-macos-universal.dmg"}]}`, tag,
			base+"Harflex-0.3.0-macos-universal.dmg", base+"Harflex-0.3.0-windows-amd64-setup.exe", base+"Harflex-0.3.0-linux-x86_64.AppImage", base+"Harflex-0.3.0-linux-amd64.deb", base+"SHA256SUMS.txt")
	})
	mux.HandleFunc("/dl/SHA256SUMS.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  Harflex-0.3.0-macos-universal.dmg\n%s  Harflex-0.3.0-windows-amd64-setup.exe\n", sum, sum)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, _ *http.Request) { w.Write(payload) })
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return Source{Client: server.Client(), Repo: "acme/app", APIBase: server.URL, AssetPrefix: server.URL + "/dl/", OS: "darwin", Arch: "arm64"}, server
}

func TestLatestFindsTheInstallerOfThisPlatformAndOnlyFromTheReleaseDownloads(t *testing.T) {
	source, _ := release(t, "v0.3.0", []byte("dmg"), nil)
	found, err := source.Latest(context.Background(), "0.2.2")
	if err != nil || found == nil || found.Version != "0.3.0" || found.AssetName != "Harflex-0.3.0-macos-universal.dmg" || !found.CanInstall() || found.PageURL != "https://example.test/release" {
		t.Fatalf("macOS release: %+v %v", found, err)
	}
	source.OS, source.Arch = "windows", "amd64"
	if found, _ = source.Latest(context.Background(), "0.2.2"); found.AssetName != "Harflex-0.3.0-windows-amd64-setup.exe" {
		t.Fatalf("Windows installer: %+v", found)
	}
	// A .deb/.rpm install belongs to the package manager: there is a release, with nothing to apply by itself.
	source.OS, source.Arch = "linux", "arm64"
	if found, _ = source.Latest(context.Background(), "0.2.2"); found == nil || found.CanInstall() {
		t.Fatalf("an arm64 Linux has no installer: %+v", found)
	}
	if found, err = source.Latest(context.Background(), "0.3.0"); found != nil || err != nil {
		t.Fatalf("already current: %+v %v", found, err)
	}
	if found, err = source.Latest(context.Background(), "dev"); found != nil || err != nil {
		t.Fatalf("a development build is never updated: %+v %v", found, err)
	}
}

func TestDownloadChecksTheReleaseChecksum(t *testing.T) {
	payload := []byte("the installer")
	source, _ := release(t, "v0.3.0", payload, nil)
	found, err := source.Latest(context.Background(), "0.2.2")
	if err != nil {
		t.Fatal(err)
	}
	var last int
	path, err := source.Download(context.Background(), *found, t.TempDir(), func(percent int) { last = percent })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(payload) || filepath.Base(path) != found.AssetName || last != 100 {
		t.Fatalf("downloaded %q to %s, progress %d", got, path, last)
	}

	bad, _ := release(t, "v0.3.0", payload, func(sum *string) { *sum = "00" + (*sum)[2:] })
	found, _ = bad.Latest(context.Background(), "0.2.2")
	dir := t.TempDir()
	if _, err := bad.Download(context.Background(), *found, dir, nil); !errors.Is(err, ErrChecksum) {
		t.Fatalf("a wrong checksum was accepted: %v", err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("the rejected download stayed on disk: %v", left)
	}
}

func TestDownloadRefusesAnInstallerFromOutsideTheReleaseDownloads(t *testing.T) {
	source, _ := release(t, "v0.3.0", []byte("x"), nil)
	found, _ := source.Latest(context.Background(), "0.2.2")
	for name, mutated := range map[string]Release{
		"another host":       {AssetName: found.AssetName, AssetURL: "https://evil.test/a.dmg", ChecksumURL: found.ChecksumURL},
		"a path in the name": {AssetName: "../a.dmg", AssetURL: found.AssetURL, ChecksumURL: found.ChecksumURL},
		"no checksum":        {AssetName: found.AssetName, AssetURL: found.AssetURL},
	} {
		if _, err := source.Download(context.Background(), mutated, t.TempDir(), nil); !errors.Is(err, ErrNoInstaller) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
