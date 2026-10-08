package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appservice "github.com/persioflexa/harflex/internal/application"
	"github.com/persioflexa/harflex/internal/catalog"
)

func TestEmbeddedAssetsIncludeCleanCheckoutAnchor(t *testing.T) {
	if _, err := fs.Stat(assets, "frontend/dist.placeholder"); err != nil {
		t.Fatalf("clean checkout embed anchor missing: %v", err)
	}
}

func TestDesktopOptionsAndLocalCatalog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Harflex")
	store, err := openCatalog(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal(info, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "harflex.db")); err != nil {
		t.Fatal(err)
	}
	stopped := false
	options := desktopOptions(appservice.NewService(t.Context(), appservice.Dependencies{Store: store}), func() { stopped = true })
	if options.Name != "Harflex" || len(options.Services) != 1 {
		t.Fatal("desktop services not registered")
	}
	if options.SingleInstance == nil || options.SingleInstance.UniqueID != "ai.harflex.desktop" {
		t.Fatalf("desktop single-instance lock is not configured: %+v", options.SingleInstance)
	}
	options.OnShutdown()
	if !stopped {
		t.Fatal("shutdown does not cancel lifecycle")
	}
	window := windowOptions()
	if window.Width != 1440 || window.Height != 900 || window.MinWidth != 320 || window.MinHeight != 640 {
		t.Fatal(window)
	}
}

func TestStartupRecoveryRunsAfterSingleInstanceAcquisition(t *testing.T) {
	store, err := openCatalog(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	workspacePath := t.TempDir()
	if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: "workspace", Path: workspacePath, Profile: "ask", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSession(t.Context(), catalog.SessionRecord{ID: "session", WorkspaceID: "workspace", BackendID: "local", Status: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(t.Context(), "session", "agent_session", "run.started", nil); err != nil {
		t.Fatal(err)
	}
	lockAcquired := false
	service, err := newRecoveredServiceAfterInstanceLock(t.Context(), appservice.Dependencies{Store: store}, func(*appservice.Service) error {
		if lockAcquired {
			t.Fatal("single-instance acquisition ran more than once")
		}
		record, err := store.GetSession(t.Context(), "session")
		if err != nil {
			return err
		}
		if record.Status != "running" {
			t.Fatalf("recovery ran before the instance-lock callback: status=%q", record.Status)
		}
		lockAcquired = true
		return nil
	})
	if err != nil || service == nil || !lockAcquired {
		t.Fatalf("startup after lock: service=%v locked=%v error=%v", service != nil, lockAcquired, err)
	}
	record, err := store.GetSession(t.Context(), "session")
	items, listErr := store.ListAfter(t.Context(), "session", 0)
	if err != nil || listErr != nil || record.Status != "paused" || len(items) != 2 || items[1].Type != "run.interrupted" {
		t.Fatalf("session was not recovered after lock: record=%+v events=%+v errors=%v/%v", record, items, err, listErr)
	}
}

func TestStartupRecoversBeforeServiceIsReturned(t *testing.T) {
	store, err := openCatalog(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: "workspace", Path: t.TempDir(), Profile: "ask", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSession(t.Context(), catalog.SessionRecord{ID: "session", WorkspaceID: "workspace", BackendID: "local", Status: "running", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(t.Context(), "session", "agent_session", "run.started", nil); err != nil {
		t.Fatal(err)
	}
	service, err := newRecoveredService(t.Context(), appservice.Dependencies{Store: store})
	if err != nil || service == nil {
		t.Fatal(err)
	}
	items, err := store.ListAfter(t.Context(), "session", 0)
	record, _ := store.GetSession(t.Context(), "session")
	if err != nil || len(items) != 2 || items[1].Type != "run.interrupted" || record.Status != "paused" {
		t.Fatal("startup skipped recovery", err)
	}
}

func TestStartupRecoveryFailureDoesNotReturnRunnableService(t *testing.T) {
	store, err := openCatalog(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	service, err := newRecoveredService(context.Background(), appservice.Dependencies{Store: store})
	if err == nil || service != nil || strings.Contains(err.Error(), "sql:") {
		t.Fatal("startup did not fail closed", service, err)
	}
}

func TestStartupInterruptsUnfinishedScheduleJobs(t *testing.T) {
	store, err := openCatalog(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	if err := store.UpsertWorkspace(t.Context(), catalog.Workspace{ID: "workspace", Path: t.TempDir(), Profile: "ask", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	item := catalog.Schedule{ID: "schedule", WorkspaceID: "workspace", Name: "Review", TargetKind: "prompt", BackendID: "local", Prompt: "Review", Frequency: "daily", Timezone: "UTC", LocalTime: "09:00", MissedPolicy: "skip", Enabled: true, NextRunAt: now.Add(time.Hour), Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveSchedule(t.Context(), item, 0); err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateManualScheduleJob(t.Context(), item.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	job.Status, job.StartedAt = "running", now
	if err := store.UpdateScheduleJob(t.Context(), job, "queued"); err != nil {
		t.Fatal(err)
	}
	if _, err := newRecoveredService(t.Context(), appservice.Dependencies{Store: store}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetScheduleJob(t.Context(), job.ID)
	if err != nil || loaded.Status != "interrupted" {
		t.Fatalf("job was not interrupted: %+v %v", loaded, err)
	}
}

func TestResolveDataDirKeepsInstalledDefaultsAndIsolatesOverrides(t *testing.T) {
	config := func() (string, error) { return "/config", nil }
	dir, lock, err := resolveDataDir("", config)
	if err != nil || dir != filepath.Join("/config", "Harflex") || lock != "ai.harflex.desktop" {
		t.Fatalf("default data dir changed: %q %q %v", dir, lock, err)
	}
	first := filepath.Join(t.TempDir(), "dev-a")
	second := filepath.Join(t.TempDir(), "dev-b")
	dirA, lockA, err := resolveDataDir(first, func() (string, error) { t.Fatal("override must not read the user config dir"); return "", nil })
	if err != nil || dirA != first {
		t.Fatalf("override ignored: %q %v", dirA, err)
	}
	_, lockB, err := resolveDataDir(second, config)
	if err != nil || lockA == lock || lockA == lockB || !strings.HasPrefix(lockA, "ai.harflex.desktop.") {
		t.Fatalf("overrides must not share the installed app's lock: %q %q %q %v", lock, lockA, lockB, err)
	}
	if _, lockRepeat, _ := resolveDataDir(first, config); lockRepeat != lockA {
		t.Fatal("the same directory must keep the same lock across launches")
	}
	if _, _, err := resolveDataDir("", func() (string, error) { return "", os.ErrNotExist }); err == nil {
		t.Fatal("a missing user config dir must fail without an override")
	}
}

func TestExecutionCopiesFollowAnIsolatedDataDir(t *testing.T) {
	if got := executionCacheRootFor("", "/home/dev/.config/Harflex"); got != "" {
		t.Fatalf("without an override the service keeps its default cache, got %q", got)
	}
	dir := filepath.Join(t.TempDir(), "dev")
	if got, want := executionCacheRootFor(dir, dir), filepath.Join(dir, "sdd-executions"); got != want {
		t.Fatalf("isolated execution cache = %q, want %q", got, want)
	}
}
