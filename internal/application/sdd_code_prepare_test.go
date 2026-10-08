//go:build (linux && !android) || (darwin && !ios && cgo)

package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/sddworkspace"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestPrepareAuthoringCodeRequiresExplicitConfirmationBeforePersistence(t *testing.T) {
	s, db, created, _, _ := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, catalog.PipelineRun{ID: created.ID, Revision: created.Revision})
	markPipelineCodePreflightReady(t, db, created.ID, "completed")
	pipeline, err := db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "source.txt"), []byte("original source"), 0o640); err != nil {
		t.Fatal(err)
	}
	privateParent := t.TempDir()
	s.privateWorkspaceParent = privateParent
	preflight, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeTables := codePreflightTableCounts(t, db)
	beforeParent, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	beforeSource, err := os.ReadFile(filepath.Join(workspace.Path, "source.txt"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.PrepareAuthoringCode(PrepareAuthoringCodeInput{
		PipelineID: created.ID, RequestID: "copy-confirmation-required",
		ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
		ExpectedCodeSelectionHash: preflight.CodeSelectionHash,
		ExpectedManifestHash:      preflight.ManifestHash, ConfirmCopy: false,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("PrepareAuthoringCode() without confirmation error = %v; want ErrInvalidInput", err)
	}
	if after := codePreflightTableCounts(t, db); !reflect.DeepEqual(beforeTables, after) {
		t.Fatalf("unconfirmed preparation wrote SQLite rows: before=%v after=%v", beforeTables, after)
	}
	afterParent, err := os.ReadDir(privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterParent) != len(beforeParent) {
		t.Fatalf("unconfirmed preparation created private files: before=%d after=%d", len(beforeParent), len(afterParent))
	}
	afterSource, err := os.ReadFile(filepath.Join(workspace.Path, "source.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeSource) != string(afterSource) {
		t.Fatalf("unconfirmed preparation changed source: before=%q after=%q", beforeSource, afterSource)
	}
	if got, err := db.ListAllSessions(t.Context()); err != nil || len(got) != 0 {
		t.Fatalf("unconfirmed preparation created sessions: sessions=%v error=%v", got, err)
	}
}

func TestPrepareAuthoringCodePersistsThenCreatesOnePrivateCopyAndReadsBack(t *testing.T) {
	s, db, created, _, catalogCalls := authoringModelPreferenceFixture(t)
	brain := startSkippedBrainstormForPreference(t, s, catalog.PipelineRun{ID: created.ID, Revision: created.Revision})
	markPipelineCodePreflightReady(t, db, created.ID, "completed")
	pipeline, err := db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace.Path, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "bin", "app"), []byte("source executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, ".env"), []byte("PRIVATE_VALUE=source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bin/app", filepath.Join(workspace.Path, "entrypoint")); err != nil {
		t.Fatal(err)
	}
	privateParent := t.TempDir()
	s.privateWorkspaceParent = privateParent
	catalogCalls.Store(0)
	preflight, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: created.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceHash := preflight.ManifestHash
	if sourceHash == "" || len(preflight.Manifest.Excluded) == 0 {
		t.Fatalf("preflight must bind a full hash and exclusions: %+v", preflight.Manifest)
	}

	prepared, err := s.PrepareAuthoringCode(PrepareAuthoringCodeInput{
		PipelineID: created.ID, RequestID: "code-copy-prepared-0001",
		ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
		ExpectedCodeSelectionHash: preflight.CodeSelectionHash,
		ExpectedManifestHash:      sourceHash, ConfirmCopy: true,
	})
	if err != nil {
		t.Fatalf("PrepareAuthoringCode() error = %v", err)
	}
	if prepared.Status != "prepared" || prepared.ID == "" || prepared.PrivatePath == "" ||
		prepared.ManifestHash != sourceHash || prepared.Manifest.Hash != sourceHash || prepared.CodePreference.Selection.ModelID != brain.Selection.ModelID {
		t.Fatalf("prepared result did not retain the confirmed snapshot: %+v", prepared)
	}
	plannedPath, err := sddworkspace.PrivateCopyDestination(privateParent, filepath.Base(prepared.PrivatePath))
	if err != nil || plannedPath != prepared.PrivatePath || !strings.Contains(filepath.Base(prepared.PrivatePath), prepared.ID) {
		t.Fatalf("private path %q is not the stable child associated with attempt %q", prepared.PrivatePath, prepared.ID)
	}
	rootInfo, err := os.Stat(prepared.PrivatePath)
	if err != nil || rootInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private root mode/info = %v, error = %v", rootInfo, err)
	}
	copyInfo, err := os.Stat(filepath.Join(prepared.PrivatePath, "bin", "app"))
	if err != nil || copyInfo.Mode().Perm()&0o077 != 0 || copyInfo.Mode().Perm()&0o100 == 0 {
		t.Fatalf("private executable mode/info = %v, error = %v", copyInfo, err)
	}
	copyBytes, err := os.ReadFile(filepath.Join(prepared.PrivatePath, "bin", "app"))
	if err != nil || string(copyBytes) != "source executable" {
		t.Fatalf("private copy bytes = %q, error = %v", copyBytes, err)
	}
	if _, err := os.Lstat(filepath.Join(prepared.PrivatePath, ".env")); !os.IsNotExist(err) {
		t.Fatalf("excluded .env appeared in private copy, error = %v", err)
	}
	if target, err := os.Readlink(filepath.Join(prepared.PrivatePath, "entrypoint")); err != nil || target != "bin/app" {
		t.Fatalf("private symlink target = %q, error = %v", target, err)
	}
	currentSource, err := sddworkspace.Scan(t.Context(), workspace.Path)
	if err != nil || currentSource.Hash != sourceHash {
		t.Fatalf("source manifest changed: hash=%q want=%q error=%v", currentSource.Hash, sourceHash, err)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("preparation queried the provider catalog %d times", catalogCalls.Load())
	}
	sessions, err := db.ListAllSessions(t.Context())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("preparation created sessions: sessions=%v error=%v", sessions, err)
	}

	fresh := NewService(t.Context(), Dependencies{Store: db, PrivateWorkspaceParent: privateParent, External: map[string]ExternalBackend{}})
	t.Cleanup(func() { _ = Shutdown(fresh) })
	readback, err := fresh.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: created.ID})
	if err != nil {
		t.Fatalf("read preparation after service restart: %v", err)
	}
	if readback.AttemptCount != 1 || readback.MaxAttemptCount != 6 || len(readback.Attempts) != 1 ||
		readback.Attempts[0].ID != prepared.ID || readback.Attempts[0].Status != "prepared" ||
		readback.Attempts[0].Manifest.Hash != sourceHash || len(readback.Attempts[0].Manifest.Excluded) == 0 {
		t.Fatalf("durable preparation readback = %+v", readback)
	}
}

func TestPrepareAuthoringCodeRejectsConcurrentDifferentRequestsForOnePipeline(t *testing.T) {
	s, _, pipeline, _, privateParent, _ := authoringCodeCopyReadyFixture(t)
	request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-concurrent-a")
	originalCopy := s.privateCodeCopy
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unlock)
	s.privateCodeCopy = func(ctx context.Context, source, parent, destination string, expected sddworkspace.Manifest) (sddworkspace.PrivateCopy, error) {
		close(entered)
		<-release
		return originalCopy(ctx, source, parent, destination, expected)
	}
	firstResult := make(chan error, 1)
	go func() {
		_, err := s.PrepareAuthoringCode(request)
		firstResult <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first copy did not reach its private-copy boundary")
	}
	secondRequest := request
	secondRequest.RequestID = "code-copy-concurrent-b"
	if _, err := s.PrepareAuthoringCode(secondRequest); !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("second non-idempotent preparation error = %v; want pipeline conflict", err)
	}
	readback, err := s.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
	if err != nil || readback.AttemptCount != 1 || len(readback.Attempts) != 1 || readback.Attempts[0].Status != "preparing" {
		t.Fatalf("preparing readback = %+v, error = %v", readback, err)
	}
	if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
		t.Fatalf("blocked first copy created files too early: entries=%v error=%v", entries, err)
	}
	unlock()
	if err := <-firstResult; err != nil {
		t.Fatalf("first confirmed preparation: %v", err)
	}
}

func TestPrepareAuthoringCodeReplayReturnsSavedReceiptAndConflictingIntentFails(t *testing.T) {
	s, _, pipeline, _, privateParent, catalogCalls := authoringCodeCopyReadyFixture(t)
	request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-idempotent-001")
	first, err := s.PrepareAuthoringCode(request)
	if err != nil || first.Status != "prepared" {
		t.Fatalf("first preparation = %+v, error = %v", first, err)
	}
	before, err := os.ReadDir(privateParent)
	if err != nil || len(before) != 1 {
		t.Fatalf("private roots after first call = %v, error = %v", before, err)
	}
	replay, err := s.PrepareAuthoringCode(request)
	if err != nil || replay.ID != first.ID || replay.PrivatePath != first.PrivatePath || replay.Status != "prepared" {
		t.Fatalf("exact replay = %+v, error = %v", replay, err)
	}
	conflict := request
	conflict.ExpectedManifestHash = strings.Repeat("0", 64)
	if _, err := s.PrepareAuthoringCode(conflict); !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("conflicting RequestID intent error = %v; want pipeline conflict", err)
	}
	after, err := os.ReadDir(privateParent)
	if err != nil || len(after) != len(before) || after[0].Name() != before[0].Name() {
		t.Fatalf("replay/conflict created another root: before=%v after=%v error=%v", before, after, err)
	}
	readback, err := s.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
	if err != nil || readback.AttemptCount != 1 || len(readback.Attempts) != 1 || readback.Attempts[0].ID != first.ID {
		t.Fatalf("idempotency readback = %+v, error = %v", readback, err)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("replay or conflict queried the provider catalog %d times", catalogCalls.Load())
	}
}

func TestPrepareAuthoringCodeAllowsExplicitFreshRootAfterPreparedAttempt(t *testing.T) {
	s, _, pipeline, _, privateParent, _ := authoringCodeCopyReadyFixture(t)
	firstRequest := authoringCodeCopyRequest(t, s, pipeline, "code-copy-root-first-0001")
	first, err := s.PrepareAuthoringCode(firstRequest)
	if err != nil || first.Status != "prepared" {
		t.Fatalf("first preparation = %+v, error = %v", first, err)
	}
	secondRequest := firstRequest
	secondRequest.RequestID = "code-copy-root-second-01"
	second, err := s.PrepareAuthoringCode(secondRequest)
	if err != nil || second.Status != "prepared" || second.ID == first.ID || second.PrivatePath == first.PrivatePath {
		t.Fatalf("explicit second preparation = %+v, error = %v", second, err)
	}
	entries, err := os.ReadDir(privateParent)
	if err != nil || len(entries) != 2 {
		t.Fatalf("private roots after second confirmation = %v, error=%v", entries, err)
	}
	readback, err := s.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
	if err != nil || readback.AttemptCount != 2 || len(readback.Attempts) != 2 || readback.MaxAttemptCount != 6 {
		t.Fatalf("second-root readback = %+v, error=%v", readback, err)
	}
}

func TestPrepareAuthoringCodeRejectsStaleRevisionStageAndManifestBeforeReservation(t *testing.T) {
	t.Run("stale pipeline revision", func(t *testing.T) {
		s, _, pipeline, _, privateParent, _ := authoringCodeCopyReadyFixture(t)
		request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-stale-pipeline")
		request.ExpectedPipelineRevision--
		if _, err := s.PrepareAuthoringCode(request); !errors.Is(err, sqlite.ErrPipelineConflict) {
			t.Fatalf("stale pipeline revision error = %v; want conflict", err)
		}
		assertNoAuthoringCodeCopyAttempt(t, s, pipeline.ID)
		if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
			t.Fatalf("stale revision created files: %v, %v", entries, err)
		}
	})

	t.Run("stale preference revision", func(t *testing.T) {
		s, db, pipeline, _, privateParent, _ := authoringCodeCopyReadyFixture(t)
		request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-stale-preference")
		if _, err := db.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
			PipelineID: pipeline.ID, Stage: sdd.Code, ModelMode: "inherit", EffortMode: "automatic",
		}, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PrepareAuthoringCode(request); !errors.Is(err, sqlite.ErrPipelineConflict) {
			t.Fatalf("stale preference revision error = %v; want conflict", err)
		}
		assertNoAuthoringCodeCopyAttempt(t, s, pipeline.ID)
		if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
			t.Fatalf("stale preference created files: %v, %v", entries, err)
		}
	})

	t.Run("Plan pending", func(t *testing.T) {
		s, db, pipeline, _, privateParent, _ := authoringCodeCopyReadyFixture(t)
		request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-plan-pending")
		statuses := `{"discovery":"completed","spec":"completed","plan":"pending","code":"active","eval":"pending"}`
		if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET stage_status=?,revision=revision+1 WHERE id=?`, statuses, pipeline.ID); err != nil {
			t.Fatal(err)
		}
		current, err := db.GetPipeline(t.Context(), pipeline.ID)
		if err != nil {
			t.Fatal(err)
		}
		request.ExpectedPipelineRevision = current.Revision
		if _, err := s.PrepareAuthoringCode(request); !errors.Is(err, sdd.ErrInvalidTransition) {
			t.Fatalf("pending Plan error = %v; want invalid transition", err)
		}
		assertNoAuthoringCodeCopyAttempt(t, s, pipeline.ID)
		if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
			t.Fatalf("pending Plan created files: %v, %v", entries, err)
		}
	})

	t.Run("manifest changed after preflight", func(t *testing.T) {
		s, db, pipeline, workspace, privateParent, _ := authoringCodeCopyReadyFixture(t)
		request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-manifest-drift")
		if err := os.WriteFile(filepath.Join(workspace.Path, "after-preflight.txt"), []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PrepareAuthoringCode(request); !errors.Is(err, sqlite.ErrPipelineConflict) {
			t.Fatalf("manifest drift error = %v; want conflict", err)
		}
		assertNoAuthoringCodeCopyAttempt(t, s, pipeline.ID)
		if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
			t.Fatalf("manifest drift created private files: %v, %v", entries, err)
		}
		var count int
		if err := db.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pipeline_authoring_code_copy_attempts WHERE pipeline_id=?`, pipeline.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("manifest drift persisted %d attempts, error=%v", count, err)
		}
	})

	t.Run("private parent inside source", func(t *testing.T) {
		s, db, pipeline, workspace, _, _ := authoringCodeCopyReadyFixture(t)
		request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-parent-inside-source")
		inside := filepath.Join(workspace.Path, "private")
		if err := os.Mkdir(inside, 0o700); err != nil {
			t.Fatal(err)
		}
		s.privateWorkspaceParent = inside
		if _, err := s.PrepareAuthoringCode(request); err == nil {
			t.Fatal("private parent inside source was accepted")
		}
		assertNoAuthoringCodeCopyAttempt(t, s, pipeline.ID)
		entries, err := os.ReadDir(inside)
		if err != nil || len(entries) != 0 {
			t.Fatalf("parent containment failure created files: %v, %v", entries, err)
		}
		var count int
		if err := db.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pipeline_authoring_code_copy_attempts WHERE pipeline_id=?`, pipeline.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("parent containment failure persisted %d attempts, error=%v", count, err)
		}
	})
}

func assertNoAuthoringCodeCopyAttempt(t *testing.T, s *Service, pipelineID string) {
	t.Helper()
	readback, err := s.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipelineID})
	if err != nil || readback.AttemptCount != 0 || len(readback.Attempts) != 0 {
		t.Fatalf("unexpected Code copy attempts: %+v, error=%v", readback, err)
	}
}

func TestPrepareAuthoringCodeFailsClosedForStaleOrUnconfiguredModelWithoutCatalogCalls(t *testing.T) {
	t.Run("stale phase selection", func(t *testing.T) {
		s, db, pipeline, _, privateParent, catalogCalls := authoringCodeCopyReadyFixture(t)
		brain, err := db.GetBrainstormingByPipeline(t.Context(), pipeline.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		staleSelection := brain.Selection
		staleSelection.ModelID = "stale-code-model"
		staleSelection.CatalogRevision = "stale-code-catalog"
		staleSelection.ReasoningEffort = ""
		if _, err := db.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
			PipelineID: pipeline.ID, Stage: sdd.Code, ModelMode: "override", EffortMode: "automatic", Selection: staleSelection,
		}, 0); err != nil {
			t.Fatal(err)
		}
		preflight, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
			PipelineID: pipeline.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 1,
		})
		if err != nil || preflight.CodePreference.Resolution != "stale" {
			t.Fatalf("stale selection preflight = %+v, error = %v", preflight.CodePreference, err)
		}
		catalogCalls.Store(0)
		input := PrepareAuthoringCodeInput{
			PipelineID: pipeline.ID, RequestID: "code-copy-stale-model-001",
			ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 1,
			ExpectedCodeSelectionHash: preflight.CodeSelectionHash,
			ExpectedManifestHash:      preflight.ManifestHash, ConfirmCopy: true,
		}
		if _, err := s.PrepareAuthoringCode(input); !errors.Is(err, ErrBackendChanged) {
			t.Fatalf("stale model preparation error = %v; want ErrBackendChanged", err)
		}
		assertNoAuthoringCodeCopyAttempt(t, s, pipeline.ID)
		if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
			t.Fatalf("stale model created private files: %v, %v", entries, err)
		}
		if catalogCalls.Load() != 0 {
			t.Fatalf("stale model resolution called catalog %d times", catalogCalls.Load())
		}
	})

	t.Run("unconfigured inherited selection", func(t *testing.T) {
		s, db, created, _, catalogCalls := authoringModelPreferenceFixture(t)
		markPipelineCodePreflightReady(t, db, created.ID, "skipped")
		pipeline, err := db.GetPipeline(t.Context(), created.ID)
		if err != nil {
			t.Fatal(err)
		}
		privateParent := t.TempDir()
		s.privateWorkspaceParent = privateParent
		catalogCalls.Store(0)
		preflight, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
			PipelineID: pipeline.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
		})
		if err != nil || preflight.CodePreference.Resolution != "unconfigured" {
			t.Fatalf("unconfigured selection preflight = %+v, error = %v", preflight.CodePreference, err)
		}
		input := PrepareAuthoringCodeInput{
			PipelineID: pipeline.ID, RequestID: "code-copy-unconfigured-01",
			ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
			ExpectedCodeSelectionHash: preflight.CodeSelectionHash,
			ExpectedManifestHash:      preflight.ManifestHash, ConfirmCopy: true,
		}
		if _, err := s.PrepareAuthoringCode(input); !errors.Is(err, ErrBackendNotFound) {
			t.Fatalf("unconfigured model preparation error = %v; want ErrBackendNotFound", err)
		}
		assertNoAuthoringCodeCopyAttempt(t, s, pipeline.ID)
		if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
			t.Fatalf("unconfigured model created private files: %v, %v", entries, err)
		}
		if catalogCalls.Load() != 0 {
			t.Fatalf("unconfigured model resolution called catalog %d times", catalogCalls.Load())
		}
	})

	t.Run("pending catalog validation", func(t *testing.T) {
		s, db, created, _, catalogCalls := authoringModelPreferenceFixture(t)
		markPipelineCodePreflightReady(t, db, created.ID, "completed")
		if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
			PipelineID: created.ID, Stage: sdd.Code, ModelMode: "inherit", EffortMode: "explicit", ExplicitEffort: "high",
		}, 0); err != nil {
			t.Fatal(err)
		}
		pipeline, err := db.GetPipeline(t.Context(), created.ID)
		if err != nil {
			t.Fatal(err)
		}
		privateParent := t.TempDir()
		s.privateWorkspaceParent = privateParent
		catalogCalls.Store(0)
		preflight, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
			PipelineID: pipeline.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 1,
		})
		if err != nil || !preflight.CodePreference.CatalogValidationRequired {
			t.Fatalf("catalog-validation preflight = %+v, error = %v", preflight.CodePreference, err)
		}
		input := PrepareAuthoringCodeInput{
			PipelineID: pipeline.ID, RequestID: "code-copy-catalog-validation",
			ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 1,
			ExpectedCodeSelectionHash: preflight.CodeSelectionHash,
			ExpectedManifestHash:      preflight.ManifestHash, ConfirmCopy: true,
		}
		if _, err := s.PrepareAuthoringCode(input); !errors.Is(err, ErrBackendChanged) {
			t.Fatalf("pending catalog validation error = %v; want ErrBackendChanged", err)
		}
		assertNoAuthoringCodeCopyAttempt(t, s, pipeline.ID)
		if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
			t.Fatalf("catalog validation gate created private files: %v, %v", entries, err)
		}
		if catalogCalls.Load() != 0 {
			t.Fatalf("catalog-validation gate called catalog %d times", catalogCalls.Load())
		}
	})
}

func TestRecoverMarksInterruptedCodeCopyWithoutReexecutingOrDeletingItsRoot(t *testing.T) {
	s, db, pipeline, workspace, privateParent, catalogCalls := authoringCodeCopyReadyFixture(t)
	request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-recovery-0001")
	preflight, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: pipeline.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	const attemptID = "authoring_code_copy_crashed_attempt"
	privatePath, err := sddworkspace.PrivateCopyDestination(privateParent, ".harflex-sdd-code-"+attemptID)
	if err != nil {
		t.Fatal(err)
	}
	intentHash, err := authoringCodeCopyIntentHash(request)
	if err != nil {
		t.Fatal(err)
	}
	attempt, created, err := db.BeginAuthoringCodeCopyAttempt(t.Context(), catalog.AuthoringCodeCopyAttempt{
		ID: attemptID, PipelineID: pipeline.ID, WorkspaceID: workspace.ID, RequestID: request.RequestID,
		IntentHash: intentHash, PipelineRevision: pipeline.Revision, CodePreferenceRevision: 0,
		SourcePath: preflight.SourcePath, PrivatePath: privatePath, ManifestHash: preflight.ManifestHash,
		PreferenceSnapshot: authoringCodeCopyPreferenceSnapshot(preflight.CodePreference, preflight.CodeSelectionHash),
		ManifestSnapshot:   authoringCodeCopyManifestSnapshot(preflight.Manifest), Status: "preparing",
	})
	if err != nil || !created {
		t.Fatalf("persist simulated preparing attempt = %+v, created=%v, error=%v", attempt, created, err)
	}
	if err := os.Mkdir(privatePath, 0o700); err != nil {
		t.Fatal(err)
	}
	const partial = "partial copy to inspect after restart"
	if err := os.WriteFile(filepath.Join(privatePath, "partial.txt"), []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}
	catalogCalls.Store(0)
	fresh := NewService(t.Context(), Dependencies{Store: db, PrivateWorkspaceParent: privateParent, External: map[string]ExternalBackend{}})
	t.Cleanup(func() { _ = Shutdown(fresh) })
	if err := fresh.Recover(t.Context()); err != nil {
		t.Fatalf("recover interrupted Code copy: %v", err)
	}
	readback, err := fresh.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
	if err != nil || readback.AttemptCount != 1 || len(readback.Attempts) != 1 {
		t.Fatalf("recovered Code copy readback = %+v, error=%v", readback, err)
	}
	got := readback.Attempts[0]
	if got.ID != attemptID || got.Status != "interrupted" || got.ErrorCode != "process_interrupted" || got.PrivatePath != privatePath {
		t.Fatalf("recovery did not preserve the registered interrupted attempt: %+v", got)
	}
	partialBytes, err := os.ReadFile(filepath.Join(privatePath, "partial.txt"))
	if err != nil || string(partialBytes) != partial {
		t.Fatalf("recovery changed/deleted the interrupted root: bytes=%q error=%v", partialBytes, err)
	}
	if sessions, err := db.ListAllSessions(t.Context()); err != nil || len(sessions) != 0 {
		t.Fatalf("recovery created a session: sessions=%v error=%v", sessions, err)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("recovery queried the model catalog %d times", catalogCalls.Load())
	}
	if err := fresh.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	again, err := fresh.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
	if err != nil || again.AttemptCount != 1 || again.Attempts[0].Status != "interrupted" || again.Attempts[0].PrivatePath != privatePath {
		t.Fatalf("repeated recovery changed attempt: %+v, error=%v", again, err)
	}
}

func TestPrepareAuthoringCodePersistsSafeFailureWithoutChangingSource(t *testing.T) {
	s, db, pipeline, workspace, privateParent, _ := authoringCodeCopyReadyFixture(t)
	request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-error-cleanup-01")
	before, err := sddworkspace.Scan(t.Context(), workspace.Path)
	if err != nil {
		t.Fatal(err)
	}
	s.privateCodeCopy = func(context.Context, string, string, string, sddworkspace.Manifest) (sddworkspace.PrivateCopy, error) {
		return sddworkspace.PrivateCopy{}, errors.New("secret source bytes should never persist")
	}
	if _, err := s.PrepareAuthoringCode(request); err == nil || strings.Contains(err.Error(), "secret source bytes") {
		t.Fatalf("copy failure was missing or leaked diagnostic text: %v", err)
	}
	readback, err := s.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
	if err != nil || readback.AttemptCount != 1 || len(readback.Attempts) != 1 {
		t.Fatalf("failed copy readback = %+v, error=%v", readback, err)
	}
	failed := readback.Attempts[0]
	if failed.Status != "failed" || failed.ErrorCode != "private_copy_failed" || failed.PrivatePath == "" ||
		strings.Contains(failed.ErrorCode, "secret") {
		t.Fatalf("failed attempt did not retain safe inspectable state: %+v", failed)
	}
	if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
		t.Fatalf("failed copy left private files: entries=%v error=%v", entries, err)
	}
	after, err := sddworkspace.Scan(t.Context(), workspace.Path)
	if err != nil || after.Hash != before.Hash {
		t.Fatalf("failed copy changed source: before=%q after=%q error=%v", before.Hash, after.Hash, err)
	}
	if sessions, err := db.ListAllSessions(t.Context()); err != nil || len(sessions) != 0 {
		t.Fatalf("failed copy created a session: sessions=%v error=%v", sessions, err)
	}
}

func TestPrepareAuthoringCodeReconcilesVerifiedCopyAfterCompletionWriteFailure(t *testing.T) {
	s, db, pipeline, _, privateParent, catalogCalls := authoringCodeCopyReadyFixture(t)
	request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-complete-replay-001")
	store := &authoringCodeCopyFailOnceCompleteStore{Store: s.store, failFor: 1}
	s.store = store
	copyCalls := new(atomic.Int32)
	realCopy := s.privateCodeCopy
	s.privateCodeCopy = func(ctx context.Context, source, parent, destination string, expected sddworkspace.Manifest) (sddworkspace.PrivateCopy, error) {
		copyCalls.Add(1)
		return realCopy(ctx, source, parent, destination, expected)
	}
	catalogCalls.Store(0)

	prepared, err := s.PrepareAuthoringCode(request)
	if err != nil || prepared.Status != "prepared" || store.completeCalls.Load() != 2 || copyCalls.Load() != 1 {
		t.Fatalf("completion retry result=%+v error=%v completeCalls=%d copyCalls=%d", prepared, err, store.completeCalls.Load(), copyCalls.Load())
	}
	readback, err := s.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
	if err != nil || readback.AttemptCount != 1 || len(readback.Attempts) != 1 || readback.Attempts[0].Status != "prepared" || readback.Attempts[0].ID != prepared.ID {
		t.Fatalf("completion retry receipt=%+v error=%v", readback, err)
	}
	if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 1 {
		t.Fatalf("completion retry created duplicate roots: entries=%v error=%v", entries, err)
	}
	if sessions, err := db.ListAllSessions(t.Context()); err != nil || len(sessions) != 0 {
		t.Fatalf("completion retry created a session: sessions=%v error=%v", sessions, err)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("completion reconciliation queried the model catalog %d times", catalogCalls.Load())
	}
}

type authoringCodeCopyFailOnceCompleteStore struct {
	Store
	completeCalls atomic.Int32
	failFor       int32
}

func (s *authoringCodeCopyFailOnceCompleteStore) CompleteAuthoringCodeCopyAttempt(ctx context.Context, id, path, manifestHash, selectionHash string) (catalog.AuthoringCodeCopyAttempt, error) {
	if s.completeCalls.Add(1) <= s.failFor {
		return catalog.AuthoringCodeCopyAttempt{}, errors.New("injected completion write failure")
	}
	return s.Store.CompleteAuthoringCodeCopyAttempt(ctx, id, path, manifestHash, selectionHash)
}

type authoringCodeCopyCommitThenFailBeginStore struct {
	Store
	beginCalls atomic.Int32
}

func (s *authoringCodeCopyCommitThenFailBeginStore) BeginAuthoringCodeCopyAttempt(ctx context.Context, attempt catalog.AuthoringCodeCopyAttempt) (catalog.AuthoringCodeCopyAttempt, bool, error) {
	saved, created, err := s.Store.BeginAuthoringCodeCopyAttempt(ctx, attempt)
	if err == nil && s.beginCalls.Add(1) == 1 {
		return catalog.AuthoringCodeCopyAttempt{}, false, errors.New("injected uncertain Begin commit")
	}
	return saved, created, err
}

func TestPrepareAuthoringCodeResumesVerifiedPreparingReceiptOnExactReplay(t *testing.T) {
	s, db, pipeline, _, privateParent, catalogCalls := authoringCodeCopyReadyFixture(t)
	request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-complete-replay-twice")
	store := &authoringCodeCopyFailOnceCompleteStore{Store: s.store, failFor: 2}
	s.store = store
	copyCalls := new(atomic.Int32)
	realCopy := s.privateCodeCopy
	s.privateCodeCopy = func(ctx context.Context, source, parent, destination string, expected sddworkspace.Manifest) (sddworkspace.PrivateCopy, error) {
		copyCalls.Add(1)
		return realCopy(ctx, source, parent, destination, expected)
	}
	catalogCalls.Store(0)
	first, err := s.PrepareAuthoringCode(request)
	if err == nil || store.completeCalls.Load() != 2 || copyCalls.Load() != 1 {
		t.Fatalf("first completion failures were not retained: result=%+v error=%v completeCalls=%d copyCalls=%d", first, err, store.completeCalls.Load(), copyCalls.Load())
	}
	if first.Status != "preparing" || first.PrivatePath == "" {
		t.Fatalf("uncommitted completion did not retain its inspectable receipt: %+v", first)
	}
	if _, err := os.Stat(first.PrivatePath); err != nil {
		t.Fatalf("verified copy root was lost before replay: %v", err)
	}

	resumed, err := s.PrepareAuthoringCode(request)
	if err != nil || resumed.Status != "prepared" || resumed.ID != first.ID || resumed.PrivatePath != first.PrivatePath ||
		store.completeCalls.Load() != 3 || copyCalls.Load() != 1 {
		t.Fatalf("exact replay did not finish the verified root: result=%+v error=%v completeCalls=%d copyCalls=%d",
			resumed, err, store.completeCalls.Load(), copyCalls.Load())
	}
	readback, err := s.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
	if err != nil || readback.AttemptCount != 1 || len(readback.Attempts) != 1 || readback.Attempts[0].Status != "prepared" {
		t.Fatalf("resumed receipt readback=%+v error=%v", readback, err)
	}
	if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 1 {
		t.Fatalf("exact replay created another root: entries=%v error=%v", entries, err)
	}
	if sessions, err := db.ListAllSessions(t.Context()); err != nil || len(sessions) != 0 {
		t.Fatalf("exact replay created a session: sessions=%v error=%v", sessions, err)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("exact replay queried model catalog %d times", catalogCalls.Load())
	}
}

func TestPrepareAuthoringCodeTerminalizesUnverifiablePreparingReceiptWithoutRecopy(t *testing.T) {
	s, db, pipeline, workspace, privateParent, catalogCalls := authoringCodeCopyReadyFixture(t)
	request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-unverified-preparing")
	preflight, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: pipeline.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	const attemptID = "authoring_code_copy_no_destination"
	privatePath, err := sddworkspace.PrivateCopyDestination(privateParent, ".harflex-sdd-code-"+attemptID)
	if err != nil {
		t.Fatal(err)
	}
	intentHash, err := authoringCodeCopyIntentHash(request)
	if err != nil {
		t.Fatal(err)
	}
	_, created, err := db.BeginAuthoringCodeCopyAttempt(t.Context(), catalog.AuthoringCodeCopyAttempt{
		ID: attemptID, PipelineID: pipeline.ID, WorkspaceID: workspace.ID, RequestID: request.RequestID,
		IntentHash: intentHash, PipelineRevision: pipeline.Revision, CodePreferenceRevision: 0,
		SourcePath: preflight.SourcePath, PrivatePath: privatePath, ManifestHash: preflight.ManifestHash,
		PreferenceSnapshot: authoringCodeCopyPreferenceSnapshot(preflight.CodePreference, preflight.CodeSelectionHash),
		ManifestSnapshot:   authoringCodeCopyManifestSnapshot(preflight.Manifest), Status: "preparing",
	})
	if err != nil || !created {
		t.Fatalf("reserve incomplete preparing receipt: created=%v error=%v", created, err)
	}
	catalogCalls.Store(0)
	reconciled, err := s.PrepareAuthoringCode(request)
	if err != nil || reconciled.ID != attemptID || reconciled.Status != "failed" || reconciled.ErrorCode != "copy_reconciliation_failed" {
		t.Fatalf("unverifiable receipt reconciliation = %+v, error=%v", reconciled, err)
	}
	if _, err := os.Lstat(privatePath); !os.IsNotExist(err) {
		t.Fatalf("unverifiable receipt replay materialized a root: %v", err)
	}
	readback, err := s.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
	if err != nil || readback.AttemptCount != 1 || len(readback.Attempts) != 1 || readback.Attempts[0].Status != "failed" {
		t.Fatalf("unverifiable receipt readback = %+v, error=%v", readback, err)
	}
	next := request
	next.RequestID = "code-copy-after-unverified"
	prepared, err := s.PrepareAuthoringCode(next)
	if err != nil || prepared.Status != "prepared" || prepared.ID == attemptID {
		t.Fatalf("new confirmation after terminal failure = %+v, error=%v", prepared, err)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("receipt reconciliation queried provider catalog %d times", catalogCalls.Load())
	}
}

func TestPrepareAuthoringCodeTerminalizesAmbiguousBeginCommitWithoutCopy(t *testing.T) {
	s, db, pipeline, _, privateParent, catalogCalls := authoringCodeCopyReadyFixture(t)
	request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-begin-commit-uncertain")
	s.store = &authoringCodeCopyCommitThenFailBeginStore{Store: s.store}
	catalogCalls.Store(0)
	if _, err := s.PrepareAuthoringCode(request); err == nil {
		t.Fatal("uncertain Begin commit error was hidden")
	}
	readback, err := s.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
	if err != nil || readback.AttemptCount != 1 || len(readback.Attempts) != 1 ||
		readback.Attempts[0].Status != "failed" || readback.Attempts[0].ErrorCode != "reservation_failed" {
		t.Fatalf("uncertain Begin receipt = %+v, error=%v", readback, err)
	}
	if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
		t.Fatalf("ambiguous reservation failure created files: entries=%v error=%v", entries, err)
	}
	next := request
	next.RequestID = "code-copy-after-begin-failure"
	prepared, err := s.PrepareAuthoringCode(next)
	if err != nil || prepared.Status != "prepared" {
		t.Fatalf("new confirmation after uncertain reservation = %+v, error=%v", prepared, err)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("reservation reconciliation queried provider catalog %d times", catalogCalls.Load())
	}
	if sessions, err := db.ListAllSessions(t.Context()); err != nil || len(sessions) != 0 {
		t.Fatalf("reservation recovery created a session: sessions=%v error=%v", sessions, err)
	}
}

func TestPrepareAuthoringCodeDoesNotResumeSameReceiptWhileCopyIsInFlight(t *testing.T) {
	s, _, pipeline, _, privateParent, _ := authoringCodeCopyReadyFixture(t)
	request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-same-inflight-request")
	realCopy := s.privateCodeCopy
	copyCalls := new(atomic.Int32)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unlock)
	s.privateCodeCopy = func(ctx context.Context, source, parent, destination string, expected sddworkspace.Manifest) (sddworkspace.PrivateCopy, error) {
		copyCalls.Add(1)
		copyResult, err := realCopy(ctx, source, parent, destination, expected)
		if err != nil {
			return copyResult, err
		}
		close(entered)
		<-release
		return copyResult, nil
	}
	firstResult := make(chan error, 1)
	go func() {
		_, err := s.PrepareAuthoringCode(request)
		firstResult <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first copy did not reach its in-flight barrier")
	}
	concurrent, err := s.PrepareAuthoringCode(request)
	if err != nil || concurrent.Status != "preparing" {
		t.Fatalf("same RequestID during active copy = %+v, error=%v; want preparing receipt", concurrent, err)
	}
	unlock()
	if err := <-firstResult; err != nil {
		t.Fatalf("original copy completion: %v", err)
	}
	if copyCalls.Load() != 1 {
		t.Fatalf("same RequestID launched %d materializations", copyCalls.Load())
	}
	if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 1 {
		t.Fatalf("same RequestID created multiple roots: entries=%v error=%v", entries, err)
	}
}

func TestPrepareAuthoringCodeMarksCopyStaleWhenRevisionChangesDuringMaterialization(t *testing.T) {
	for _, change := range []string{"pipeline", "Code preference", "provider profile"} {
		t.Run(change, func(t *testing.T) {
			s, db, pipeline, workspace, _, _ := authoringCodeCopyReadyFixture(t)
			request := authoringCodeCopyRequest(t, s, pipeline, "code-copy-stale-during-copy")
			before, err := sddworkspace.Scan(t.Context(), workspace.Path)
			if err != nil {
				t.Fatal(err)
			}
			realCopy := s.privateCodeCopy
			s.privateCodeCopy = func(ctx context.Context, source, parent, destination string, expected sddworkspace.Manifest) (sddworkspace.PrivateCopy, error) {
				copyResult, err := realCopy(ctx, source, parent, destination, expected)
				if err != nil {
					return copyResult, err
				}
				switch change {
				case "pipeline":
					_, err = db.DB().ExecContext(ctx, `UPDATE pipeline_runs SET revision=revision+1 WHERE id=?`, pipeline.ID)
				case "Code preference":
					_, err = db.SaveAuthoringStageModelPreference(ctx, catalog.AuthoringStageModelPreference{
						PipelineID: pipeline.ID, Stage: sdd.Code, ModelMode: "inherit", EffortMode: "automatic",
					}, 0)
				case "provider profile":
					profile, readErr := db.GetProviderProfile(ctx, "lm")
					if readErr != nil {
						return copyResult, readErr
					}
					_, err = s.SaveProviderProfile(SaveProviderProfileInput{
						ID: profile.ID, Name: profile.Name, Kind: profile.Kind, ProviderType: profile.ProviderType,
						BaseURL: profile.BaseURL, Model: "local/other",
					})
				}
				return copyResult, err
			}

			prepared, err := s.PrepareAuthoringCode(request)
			if !errors.Is(err, sqlite.ErrPipelineConflict) || prepared.Status != "stale" || prepared.PrivatePath == "" {
				t.Fatalf("stale revision result = %+v, error = %v", prepared, err)
			}
			if _, err := os.Stat(prepared.PrivatePath); err != nil {
				t.Fatalf("stale root was not retained for inspection: %v", err)
			}
			readback, err := s.GetAuthoringCodeCopyPreparations(GetAuthoringCodeCopyPreparationsInput{PipelineID: pipeline.ID})
			if err != nil || readback.AttemptCount != 1 || len(readback.Attempts) != 1 || readback.Attempts[0].Status != "stale" {
				t.Fatalf("stale attempt readback = %+v, error=%v", readback, err)
			}
			after, err := sddworkspace.Scan(t.Context(), workspace.Path)
			if err != nil || after.Hash != before.Hash {
				t.Fatalf("stale preparation changed source: before=%q after=%q error=%v", before.Hash, after.Hash, err)
			}
			if sessions, err := db.ListAllSessions(t.Context()); err != nil || len(sessions) != 0 {
				t.Fatalf("stale preparation created a session: sessions=%v error=%v", sessions, err)
			}
		})
	}
}

func authoringCodeCopyReadyFixture(t *testing.T) (*Service, *sqlite.Store, catalog.PipelineRun, catalog.Workspace, string, *atomic.Int32) {
	t.Helper()
	s, db, created, _, catalogCalls := authoringModelPreferenceFixture(t)
	startSkippedBrainstormForPreference(t, s, catalog.PipelineRun{ID: created.ID, Revision: created.Revision})
	markPipelineCodePreflightReady(t, db, created.ID, "completed")
	pipeline, err := db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	privateParent := t.TempDir()
	s.privateWorkspaceParent = privateParent
	catalogCalls.Store(0)
	return s, db, pipeline, workspace, privateParent, catalogCalls
}

func authoringCodeCopyRequest(t *testing.T, s *Service, pipeline catalog.PipelineRun, requestID string) PrepareAuthoringCodeInput {
	t.Helper()
	preflight, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: pipeline.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return PrepareAuthoringCodeInput{
		PipelineID: pipeline.ID, RequestID: requestID,
		ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
		ExpectedCodeSelectionHash: preflight.CodeSelectionHash,
		ExpectedManifestHash:      preflight.ManifestHash, ConfirmCopy: true,
	}
}

func TestPrepareAuthoringCodeRejectsOldEffectiveSelectionHashAndAcceptsFreshValidatedSelection(t *testing.T) {
	s, db, created, _, catalogCalls := authoringModelPreferenceFixture(t)
	markPipelineCodePreflightReady(t, db, created.ID, "completed")
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: "lm"}); err != nil {
		t.Fatal(err)
	}
	pipeline, err := db.GetPipeline(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	privateParent := t.TempDir()
	s.privateWorkspaceParent = privateParent
	catalogCalls.Store(0)
	first, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: pipeline.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil || first.CodeSelectionHash == "" || !first.CodePreference.CatalogValidationRequired {
		t.Fatalf("initial default preflight = %+v, error = %v", first, err)
	}
	profile, err := db.GetProviderProfile(t.Context(), "lm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(SaveProviderProfileInput{
		ID: profile.ID, Name: profile.Name, Kind: profile.Kind, ProviderType: profile.ProviderType,
		BaseURL: profile.BaseURL, Model: "local/other",
	}); err != nil {
		t.Fatal(err)
	}
	current, err := db.GetPipeline(t.Context(), pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != pipeline.Revision {
		t.Fatalf("profile update changed pipeline revision: before=%d after=%d", pipeline.Revision, current.Revision)
	}
	second, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: pipeline.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
	})
	if err != nil || second.CodePreferenceRevision != 0 || second.CodeSelectionHash == first.CodeSelectionHash ||
		second.CodePreference.Selection.ModelID != "local/other" || second.ManifestHash != first.ManifestHash {
		t.Fatalf("updated default preflight = %+v, error = %v", second, err)
	}
	oldConfirmation := PrepareAuthoringCodeInput{
		PipelineID: pipeline.ID, RequestID: "code-copy-old-default-hash-01",
		ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 0,
		ExpectedCodeSelectionHash: first.CodeSelectionHash, ExpectedManifestHash: first.ManifestHash, ConfirmCopy: true,
	}
	if _, err := s.PrepareAuthoringCode(oldConfirmation); !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("confirmation bound to old default selection error = %v; want conflict", err)
	}
	newDefaultConfirmation := oldConfirmation
	newDefaultConfirmation.RequestID = "code-copy-new-default-pending"
	newDefaultConfirmation.ExpectedCodeSelectionHash = second.CodeSelectionHash
	if _, err := s.PrepareAuthoringCode(newDefaultConfirmation); !errors.Is(err, ErrBackendChanged) {
		t.Fatalf("unvalidated fresh global default error = %v; want catalog validation gate", err)
	}
	assertNoAuthoringCodeCopyAttempt(t, s, pipeline.ID)
	if entries, err := os.ReadDir(privateParent); err != nil || len(entries) != 0 {
		t.Fatalf("stale/default confirmation created private files: %v, %v", entries, err)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("preflight/prepare queried provider catalog %d times", catalogCalls.Load())
	}

	catalogResult, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	selection := apiSelectionInput(catalogResult, "lm", "local/other", 256)
	if _, err := s.SaveAuthoringStageModelPreference(SaveAuthoringStageModelPreferenceInput{
		PipelineID: pipeline.ID, Stage: string(sdd.Code), ExpectedRevision: 0,
		ModelMode: "override", EffortMode: "automatic", Selection: selection,
	}); err != nil {
		t.Fatalf("save fresh validated Code selection: %v", err)
	}
	currentPreferencePreflight, err := s.PreflightAuthoringCode(PreflightAuthoringCodeInput{
		PipelineID: pipeline.ID, ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 1,
	})
	if err != nil || currentPreferencePreflight.CodePreference.Resolution != "ready" ||
		currentPreferencePreflight.CodePreference.CatalogValidationRequired || currentPreferencePreflight.CodeSelectionHash == first.CodeSelectionHash {
		t.Fatalf("validated Code override preflight = %+v, error=%v", currentPreferencePreflight, err)
	}
	newConfirmation := PrepareAuthoringCodeInput{
		PipelineID: pipeline.ID, RequestID: "code-copy-confirm-fresh-selection",
		ExpectedPipelineRevision: pipeline.Revision, ExpectedCodePreferenceRevision: 1,
		ExpectedCodeSelectionHash: currentPreferencePreflight.CodeSelectionHash,
		ExpectedManifestHash:      currentPreferencePreflight.ManifestHash, ConfirmCopy: true,
	}
	catalogCalls.Store(0)
	prepared, err := s.PrepareAuthoringCode(newConfirmation)
	if err != nil || prepared.Status != "prepared" || prepared.CodeSelectionHash != currentPreferencePreflight.CodeSelectionHash {
		t.Fatalf("fresh validated selection confirmation = %+v, error=%v", prepared, err)
	}
	if catalogCalls.Load() != 0 {
		t.Fatalf("PrepareAuthoringCode queried provider catalog %d times", catalogCalls.Load())
	}
}
