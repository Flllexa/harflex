package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/updater"
)

type updateFixture struct {
	s       *Service
	mu      sync.Mutex
	events  []UpdateStateDTO
	applied []string
	quit    chan struct{}
}

// newUpdateFixture serves a newer release from a fake source and records what the service does with it.
func newUpdateFixture(t *testing.T, current string, applyErr error) *updateFixture {
	t.Helper()
	s, _, _ := setup(t)
	f := &updateFixture{s: s, quit: make(chan struct{})}
	SetEmitter(s, func(name string, payload any) {
		if name != "harflex:update" {
			return
		}
		f.mu.Lock()
		f.events = append(f.events, payload.(UpdateStateDTO))
		f.mu.Unlock()
	})
	u := s.updates()
	u.mu.Lock()
	u.current, u.disabled = current, false
	u.canApply = func() error { return nil }
	u.stagingDir = func() (string, error) { return t.TempDir(), nil }
	u.apply = func(_ context.Context, installer string) error {
		f.mu.Lock()
		f.applied = append(f.applied, installer)
		f.mu.Unlock()
		return applyErr
	}
	u.quit = func() { close(f.quit) }
	u.mu.Unlock()
	return f
}

func (f *updateFixture) release(r *updater.Release) {
	u := f.s.updates()
	u.mu.Lock()
	u.latest = r
	u.mu.Unlock()
}

func (f *updateFixture) phases() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.events))
	for i, e := range f.events {
		out[i] = e.Phase
	}
	return out
}

func TestCheckForUpdateIsOffWhenDisabled(t *testing.T) {
	f := newUpdateFixture(t, "0.2.2", nil)
	u := f.s.updates()
	u.mu.Lock()
	u.disabled = true
	u.mu.Unlock()
	info, err := f.s.CheckForUpdate()
	if err != nil || info.Available || info.CurrentVersion != "0.2.2" {
		t.Fatalf("disabled check: %+v %v", info, err)
	}
}

func TestInstallUpdateNeedsAReleaseThisCopyCanApply(t *testing.T) {
	f := newUpdateFixture(t, "0.2.2", nil)
	if err := f.s.InstallUpdate(); !errors.Is(err, ErrNoUpdateAvailable) {
		t.Fatalf("no release found yet: %v", err)
	}
	f.release(&updater.Release{Version: "0.3.0", PageURL: "https://example.test/r"})
	if err := f.s.InstallUpdate(); !errors.Is(err, ErrUpdateNotInstallable) {
		t.Fatalf("a release with no installer for this platform: %v", err)
	}
	f.release(&updater.Release{Version: "0.3.0", AssetName: "a.dmg", AssetURL: "u", ChecksumURL: "c"})
	u := f.s.updates()
	u.mu.Lock()
	u.canApply = func() error { return updater.ErrNotInstalled }
	u.mu.Unlock()
	if err := f.s.InstallUpdate(); !errors.Is(err, ErrUpdateNotInstallable) {
		t.Fatalf("a copy that cannot replace itself: %v", err)
	}
	if ErrorCode(ErrUpdateNotWritable) != "update_not_writable" || ErrorCode(ErrNoUpdateAvailable) != "no_update_available" {
		t.Fatal("update errors need stable codes")
	}
}

// installerServer serves one installer and the checksum list that vouches for it.
func installerServer(t *testing.T) (updater.Source, updater.Release) {
	t.Helper()
	payload := []byte("installer bytes")
	sum := sha256.Sum256(payload)
	mux := http.NewServeMux()
	mux.HandleFunc("/Harflex-0.3.0-macos-universal.dmg", func(w http.ResponseWriter, _ *http.Request) { w.Write(payload) })
	mux.HandleFunc("/SHA256SUMS.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  Harflex-0.3.0-macos-universal.dmg\n", hex.EncodeToString(sum[:]))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return updater.Source{Client: server.Client(), AssetPrefix: server.URL + "/"}, updater.Release{Version: "0.3.0", AssetName: "Harflex-0.3.0-macos-universal.dmg", AssetURL: server.URL + "/Harflex-0.3.0-macos-universal.dmg", ChecksumURL: server.URL + "/SHA256SUMS.txt"}
}

func (f *updateFixture) waitFor(t *testing.T, phase string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, got := range f.phases() {
			if got == phase {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never reached %q: %v", phase, f.phases())
}

func TestInstallUpdateDownloadsAppliesAndQuitsOnlyWhenTheSwapIsArmed(t *testing.T) {
	f := newUpdateFixture(t, "0.2.2", nil)
	source, release := installerServer(t)
	u := f.s.updates()
	u.mu.Lock()
	u.source = source
	u.mu.Unlock()
	f.release(&release)
	if err := f.s.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	f.waitFor(t, "restarting")
	select {
	case <-f.quit:
	case <-time.After(5 * time.Second):
		t.Fatal("the app never quit after the swap was armed")
	}
	phases := f.phases()
	if phases[0] != "downloading" || phases[len(phases)-2] != "installing" || phases[len(phases)-1] != "restarting" {
		t.Fatalf("phases: %v", phases)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.applied) != 1 || filepath.Base(f.applied[0]) != release.AssetName {
		t.Fatalf("applied: %v", f.applied)
	}
}

func TestInstallUpdateThatCannotBeAppliedFailsWithACodeAndCanBeTriedAgain(t *testing.T) {
	f := newUpdateFixture(t, "0.2.2", updaterNotWritable)
	source, release := installerServer(t)
	u := f.s.updates()
	u.mu.Lock()
	u.source = source
	u.mu.Unlock()
	f.release(&release)
	if err := f.s.InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	f.waitFor(t, "failed")
	f.mu.Lock()
	last := f.events[len(f.events)-1]
	f.mu.Unlock()
	if last.ErrorCode != "update_not_writable" {
		t.Fatalf("failure: %+v", last)
	}
	select {
	case <-f.quit:
		t.Fatal("the app quit although the update failed")
	default:
	}
	u.mu.Lock()
	installing := u.installing
	u.mu.Unlock()
	if installing {
		t.Fatal("a failed update stays locked")
	}
}

func TestUpdateErrorCodes(t *testing.T) {
	for err, want := range map[error]string{
		updater.ErrChecksum: "update_checksum", updater.ErrNoInstaller: "update_not_installable", updater.ErrNotInstalled: "update_not_installable",
		updaterNotWritable: "update_not_writable", errors.New("boom"): "update_failed",
	} {
		if got := updateErrorCode(err); got != want {
			t.Fatalf("%v: %s, want %s", err, got, want)
		}
	}
}
