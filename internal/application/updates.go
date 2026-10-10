package application

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/persioflexa/harflex/internal/buildinfo"
	"github.com/persioflexa/harflex/internal/updater"
)

// Update errors the frontend branches on.
var (
	ErrNoUpdateAvailable    = errors.New("there is no newer version to install")
	ErrUpdateNotInstallable = errors.New("this copy of the app cannot update itself")
	ErrUpdateNotWritable    = errors.New("the app's folder does not let this user replace it")
)

// UpdateInfoDTO says whether a newer version is published, and whether this copy can install it by itself.
type UpdateInfoDTO struct {
	CurrentVersion string `json:"currentVersion"`
	Available      bool   `json:"available"`
	Version        string `json:"version"`
	PageURL        string `json:"pageUrl"`
	CanInstall     bool   `json:"canInstall"`
}

// UpdateStateDTO is the progress of an install, sent as the "harflex:update" event.
type UpdateStateDTO struct {
	Phase     string `json:"phase"` // downloading, installing, restarting or failed
	Percent   int    `json:"percent"`
	ErrorCode string `json:"errorCode"`
}

const updateCheckTimeout = 20 * time.Second

// updateState holds what the update check found and the seams the tests replace.
type updateState struct {
	mu         sync.Mutex
	ready      bool
	current    string
	disabled   bool
	source     updater.Source
	latest     *updater.Release
	installing bool
	quit       func()
	stagingDir func() (string, error)
	canApply   func() error
	apply      func(context.Context, string) error
}

// updates fills the defaults on first use: this build's version, the GitHub releases, the platform's installer step.
// HARFLEX_NO_UPDATE_CHECK turns the check off (development and test runs).
func (s *Service) updates() *updateState {
	u := &s.update
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.ready {
		u.ready = true
		u.current = buildinfo.Version
		u.disabled = os.Getenv("HARFLEX_NO_UPDATE_CHECK") != ""
		u.source = updater.NewSource(runtime.GOOS, runtime.GOARCH)
		u.stagingDir = updater.StagingDir
		u.canApply = updater.CanApply
		u.apply = updater.Apply
	}
	return u
}

// SetQuitHandler is configured by the composition root: it closes the app once an update is armed.
func SetQuitHandler(s *Service, quit func()) {
	u := s.updates()
	u.mu.Lock()
	u.quit = quit
	u.mu.Unlock()
}

// CheckForUpdate asks GitHub whether a version newer than this one is published. Failing to reach it is an error the
// caller may ignore: nobody has to be told that the check could not run.
func (s *Service) CheckForUpdate() (UpdateInfoDTO, error) {
	u := s.updates()
	info := UpdateInfoDTO{CurrentVersion: u.current}
	if u.disabled {
		return info, nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, updateCheckTimeout)
	defer cancel()
	release, err := u.source.Latest(ctx, u.current)
	if err != nil {
		return info, safe("check for updates", err)
	}
	u.mu.Lock()
	u.latest = release
	u.mu.Unlock()
	if release == nil {
		return info, nil
	}
	info.Available, info.Version, info.PageURL = true, release.Version, release.PageURL
	info.CanInstall = release.CanInstall() && u.canApply() == nil
	return info, nil
}

// InstallUpdate downloads the newer version, checks it, swaps the app and restarts it. It returns at once; the
// "harflex:update" event carries the progress, and the app quits by itself when the swap is armed.
func (s *Service) InstallUpdate() error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	u := s.updates()
	u.mu.Lock()
	release := u.latest
	if u.installing {
		u.mu.Unlock()
		return nil
	}
	if release == nil {
		u.mu.Unlock()
		return ErrNoUpdateAvailable
	}
	if !release.CanInstall() {
		u.mu.Unlock()
		return ErrUpdateNotInstallable
	}
	if err := u.canApply(); err != nil {
		u.mu.Unlock()
		if errors.Is(err, updaterNotWritable) {
			return ErrUpdateNotWritable
		}
		return ErrUpdateNotInstallable
	}
	u.installing = true
	u.mu.Unlock()
	go s.runUpdate(u, *release)
	return nil
}

func (s *Service) runUpdate(u *updateState, release updater.Release) {
	report := func(phase string, percent int, err error) {
		s.mu.RLock()
		emit := s.emit
		s.mu.RUnlock()
		state := UpdateStateDTO{Phase: phase, Percent: percent}
		if err != nil {
			state.ErrorCode = updateErrorCode(err)
		}
		if emit != nil {
			emit("harflex:update", state)
		}
	}
	fail := func(err error) {
		u.mu.Lock()
		u.installing = false
		u.mu.Unlock()
		report("failed", 0, err)
	}
	dir, err := u.stagingDir()
	if err != nil {
		fail(err)
		return
	}
	last := -1
	installer, err := u.source.Download(s.ctx, release, dir, func(percent int) {
		if percent != last {
			last = percent
			report("downloading", percent, nil)
		}
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		fail(err)
		return
	}
	report("installing", 100, nil)
	if err := u.apply(s.ctx, installer); err != nil {
		_ = os.RemoveAll(dir)
		fail(err)
		return
	}
	report("restarting", 100, nil)
	u.mu.Lock()
	quit := u.quit
	u.mu.Unlock()
	if quit != nil {
		quit()
	}
}

func updateErrorCode(err error) string {
	switch {
	case errors.Is(err, updater.ErrChecksum):
		return "update_checksum"
	case errors.Is(err, updater.ErrNoInstaller), errors.Is(err, updater.ErrNotInstalled):
		return "update_not_installable"
	case errors.Is(err, updaterNotWritable):
		return "update_not_writable"
	}
	return "update_failed"
}
