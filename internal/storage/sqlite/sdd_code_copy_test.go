package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/sddworkspace"
)

func codeCopyFixture(t *testing.T) (*Store, string, catalog.PipelineRun) {
	t.Helper()
	store, path, run := authoringFixture(t)
	if err := store.CreateAuthoringPipeline(t.Context(), run, "Full source discovery"); err != nil {
		t.Fatal(err)
	}
	statuses := `{"discovery":"completed","spec":"completed","plan":"completed","code":"active","eval":"pending"}`
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET current_stage='code',stage_status=?,discovery_frozen_version=1,revision=revision+1 WHERE id=?`, statuses, run.ID); err != nil {
		t.Fatal(err)
	}
	ready, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, path, ready
}

func codeCopyAttempt(run catalog.PipelineRun, requestID, id string) catalog.AuthoringCodeCopyAttempt {
	preference := catalog.AuthoringCodeCopyPreferenceSnapshot{
		PipelineID: run.ID, Stage: "code", ModelMode: "inherit", EffortMode: "inherit",
		Resolution: "ready", ModelSource: "brainstorm", PreferenceRevision: 0,
		Selection: catalog.AuthoringCodeCopySelection{
			BackendID: "profile", ModelID: "model", CatalogRevision: "catalog_revision",
			Source: "openai_models", Destination: "https://example.test", Status: "listed",
			MaxOutputTokens: 4096, CheckedAt: time.Now().UTC(),
		},
	}
	selectionHash, err := catalog.HashAuthoringCodeSelection(preference)
	if err != nil {
		panic(err)
	}
	preference.SelectionHash = selectionHash
	manifest := catalog.AuthoringCodeCopyManifest{Version: 1, Entries: []catalog.AuthoringCodeCopyEntry{}, Excluded: []catalog.AuthoringCodeCopyExclusion{}}
	manifest.Hash = sddworkspace.HashManifest(sddworkspace.Manifest{Version: 1, Entries: []sddworkspace.Entry{}, Excluded: []sddworkspace.Exclusion{}})
	return catalog.AuthoringCodeCopyAttempt{
		ID: id, PipelineID: run.ID, WorkspaceID: run.WorkspaceID, RequestID: requestID,
		IntentHash: strings.Repeat("a", 64), PipelineRevision: run.Revision, CodePreferenceRevision: 0,
		SourcePath: "/project", PrivatePath: "/private/.harflex-sdd-code-" + id,
		ManifestHash:       manifest.Hash,
		PreferenceSnapshot: preference,
		ManifestSnapshot:   manifest,
		Status:             "preparing",
	}
}

func TestAuthoringCodeCopyReservationIsIdempotentAndRejectsConflictingIntent(t *testing.T) {
	store, _, run := codeCopyFixture(t)
	first := codeCopyAttempt(run, "copy_request_0001", "copy_attempt_0001")
	saved, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), first)
	if err != nil || !created || saved.ID != first.ID || saved.Status != "preparing" {
		t.Fatalf("first reservation = %+v, created=%v, error=%v", saved, created, err)
	}

	replay := first
	replay.ID = "different-generated-id"
	saved, created, err = store.BeginAuthoringCodeCopyAttempt(t.Context(), replay)
	if err != nil || created || saved.ID != first.ID || saved.PrivatePath != first.PrivatePath {
		t.Fatalf("idempotent replay = %+v, created=%v, error=%v", saved, created, err)
	}
	conflict := replay
	conflict.IntentHash = strings.Repeat("c", 64)
	if _, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), conflict); !errors.Is(err, ErrPipelineConflict) || created {
		t.Fatalf("conflicting request replay = created %v, error %v; want pipeline conflict", created, err)
	}
}

func TestAuthoringCodeCopyReservationSerializesPreparingAndAllowsExplicitNewRootAfterPrepared(t *testing.T) {
	store, path, run := codeCopyFixture(t)
	peer, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	inputs := []catalog.AuthoringCodeCopyAttempt{
		codeCopyAttempt(run, "copy_concurrent_0001", "copy_concurrent_a"),
		codeCopyAttempt(run, "copy_concurrent_0002", "copy_concurrent_b"),
	}
	stores := []*Store{store, peer}
	start := make(chan struct{})
	results := make(chan error, len(inputs))
	var wg sync.WaitGroup
	for i := range inputs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, err := stores[i].BeginAuthoringCodeCopyAttempt(t.Context(), inputs[i])
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	created, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrPipelineConflict):
			conflicted++
		default:
			t.Fatalf("concurrent reservation error = %v", err)
		}
	}
	if created != 1 || conflicted != 1 {
		t.Fatalf("concurrent reservations created=%d conflicts=%d; want one of each", created, conflicted)
	}
	attempt, err := store.ListAuthoringCodeCopyAttempts(t.Context(), run.ID)
	if err != nil || len(attempt) != 1 {
		t.Fatalf("stored concurrent attempts = %d, error = %v", len(attempt), err)
	}
	completed, err := store.CompleteAuthoringCodeCopyAttempt(t.Context(), attempt[0].ID, attempt[0].PrivatePath, attempt[0].ManifestHash, attempt[0].PreferenceSnapshot.SelectionHash)
	if err != nil || completed.Status != "prepared" {
		t.Fatalf("complete attempt = %+v, error = %v", completed, err)
	}
	replay := codeCopyAttempt(run, completed.RequestID, "different-replay-id")
	replay.IntentHash = completed.IntentHash
	savedReplay, replayCreated, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), replay)
	if err != nil || replayCreated || savedReplay.ID != completed.ID || savedReplay.Status != "prepared" {
		t.Fatalf("prepared RequestID replay = %+v, created=%v, error=%v", savedReplay, replayCreated, err)
	}
	second, secondCreated, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), codeCopyAttempt(run, "copy_after_prepared_0001", "copy_after_prepared"))
	if err != nil || !secondCreated || second.Status != "preparing" || second.PrivatePath == completed.PrivatePath {
		t.Fatalf("explicit second copy after prepared = %+v, created=%v, error=%v", second, secondCreated, err)
	}
	all, err := store.ListAuthoringCodeCopyAttempts(t.Context(), run.ID)
	preparedCount, preparingCount := 0, 0
	for _, attempt := range all {
		if attempt.Status == "prepared" {
			preparedCount++
		}
		if attempt.Status == "preparing" {
			preparingCount++
		}
	}
	if err != nil || len(all) != 2 || preparedCount != 1 || preparingCount != 1 {
		t.Fatalf("second-root receipt history = %+v, error=%v", all, err)
	}
}

func TestAuthoringCodeCopyCompletionMarksChangedPipelineStaleAndRecoveryNeverRetries(t *testing.T) {
	store, _, run := codeCopyFixture(t)
	attempt, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), codeCopyAttempt(run, "copy_stale_request_0001", "copy_stale_attempt"))
	if err != nil || !created {
		t.Fatalf("reserve stale test attempt = created %v, error %v", created, err)
	}
	statuses := `{"discovery":"completed","spec":"completed","plan":"completed","code":"active","eval":"pending"}`
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET revision=revision+1,stage_status=? WHERE id=?`, statuses, run.ID); err != nil {
		t.Fatal(err)
	}
	stale, err := store.CompleteAuthoringCodeCopyAttempt(t.Context(), attempt.ID, attempt.PrivatePath, attempt.ManifestHash, attempt.PreferenceSnapshot.SelectionHash)
	if !errors.Is(err, ErrPipelineConflict) || stale.Status != "stale" || stale.PrivatePath != attempt.PrivatePath {
		t.Fatalf("stale completion = %+v, error = %v", stale, err)
	}

	currentRun, err := store.GetPipeline(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	preferenceAttempt := codeCopyAttempt(currentRun, "copy_stale_preference_0001", "copy_stale_preference_attempt")
	if _, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), preferenceAttempt); err != nil || !created {
		t.Fatalf("reserve preference-stale test attempt = created %v, error %v", created, err)
	}
	if _, err := store.SaveAuthoringStageModelPreference(t.Context(), catalog.AuthoringStageModelPreference{
		PipelineID: run.ID, Stage: sdd.Code, ModelMode: "inherit", EffortMode: "automatic",
	}, 0); err != nil {
		t.Fatal(err)
	}
	stalePreference, err := store.CompleteAuthoringCodeCopyAttempt(t.Context(), preferenceAttempt.ID, preferenceAttempt.PrivatePath, preferenceAttempt.ManifestHash, preferenceAttempt.PreferenceSnapshot.SelectionHash)
	if !errors.Is(err, ErrPipelineConflict) || stalePreference.Status != "stale" || stalePreference.ErrorCode != "code_preference_revision_changed" {
		t.Fatalf("stale preference completion = %+v, error = %v", stalePreference, err)
	}
	currentRun, err = store.GetPipeline(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	interruptedInput := codeCopyAttempt(currentRun, "copy_recovery_request_0001", "copy_interrupted_attempt")
	interruptedInput.CodePreferenceRevision = 1
	interruptedInput.PreferenceSnapshot.PreferenceRevision = 1
	interruptedInput.PreferenceSnapshot.EffortMode = "automatic"
	interruptedInput.PreferenceSnapshot.EffortSource = "automatic"
	interruptedInput.PreferenceSnapshot.SelectionHash, err = catalog.HashAuthoringCodeSelection(interruptedInput.PreferenceSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), interruptedInput); err != nil || !created {
		t.Fatalf("reserve recovery test attempt = created %v, error %v", created, err)
	}
	if err := store.InterruptPreparingAuthoringCodeCopyAttempts(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.InterruptPreparingAuthoringCodeCopyAttempts(t.Context()); err != nil {
		t.Fatal(err)
	}
	all, err := store.ListAuthoringCodeCopyAttempts(t.Context(), run.ID)
	if err != nil || len(all) != 3 {
		t.Fatalf("recovered attempts = %+v, error = %v", all, err)
	}
	byID := map[string]catalog.AuthoringCodeCopyAttempt{}
	for _, item := range all {
		byID[item.ID] = item
	}
	if byID[attempt.ID].Status != "stale" || byID[preferenceAttempt.ID].Status != "stale" || byID[interruptedInput.ID].Status != "interrupted" ||
		byID[interruptedInput.ID].PrivatePath != interruptedInput.PrivatePath || byID[interruptedInput.ID].ErrorCode == "" {
		t.Fatalf("recovery lost status or registered path: %+v", all)
	}
}

func TestAuthoringCodeCopyAttemptLimitAllowsExactReplayAtLimit(t *testing.T) {
	store, _, run := codeCopyFixture(t)
	for i := range sdd.MaxAuthoringAttempts {
		attempt := codeCopyAttempt(run, fmt.Sprintf("copy_limit_request_%02d", i), fmt.Sprintf("copy_limit_attempt_%02d", i))
		attempt.IntentHash = fmt.Sprintf("%064x", i+1)
		reserved, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), attempt)
		if err != nil || !created {
			t.Fatalf("reserve attempt %d = created %v, error %v", i, created, err)
		}
		if err := store.FailAuthoringCodeCopyAttempt(t.Context(), reserved.ID, "copy_failed"); err != nil {
			t.Fatalf("fail attempt %d: %v", i, err)
		}
	}
	previous := codeCopyAttempt(run, "copy_limit_request_00", "unused-id")
	previous.IntentHash = fmt.Sprintf("%064x", 1)
	replay, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), previous)
	if err != nil || created || replay.Status != "failed" {
		t.Fatalf("replay at attempt limit = %+v, created=%v, error=%v", replay, created, err)
	}
	next := codeCopyAttempt(run, "copy_limit_request_06", "copy_limit_attempt_06")
	if _, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), next); !errors.Is(err, ErrAuthoringCodeCopyLimit) || created {
		t.Fatalf("seventh preparation = created %v, error %v; want attempt limit", created, err)
	}
}

func TestAuthoringCodeCopyReceiptProtectsImmutableSnapshotsAndAllowsTerminalTransition(t *testing.T) {
	store, _, run := codeCopyFixture(t)
	attempt, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), codeCopyAttempt(run, "copy_immutable_request_01", "copy_immutable_attempt"))
	if err != nil || !created {
		t.Fatalf("reserve immutable receipt = created %v, error %v", created, err)
	}
	for _, statement := range []string{
		`UPDATE pipeline_authoring_code_copy_attempts SET request_id='changed_request' WHERE id=?`,
		`UPDATE pipeline_authoring_code_copy_attempts SET private_path='/private/changed' WHERE id=?`,
		`UPDATE pipeline_authoring_code_copy_attempts SET manifest_hash='` + strings.Repeat("c", 64) + `' WHERE id=?`,
		`UPDATE pipeline_authoring_code_copy_attempts SET preference_snapshot_json='{}' WHERE id=?`,
		`UPDATE pipeline_authoring_code_copy_attempts SET manifest_snapshot_json='{}' WHERE id=?`,
		`DELETE FROM pipeline_authoring_code_copy_attempts WHERE id=?`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement, attempt.ID); err == nil {
			t.Fatalf("immutable receipt mutation was accepted: %s", statement)
		}
	}
	if err := store.FailAuthoringCodeCopyAttempt(t.Context(), attempt.ID, "private_copy_failed"); err != nil {
		t.Fatalf("allowed preparing-to-failed transition: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_authoring_code_copy_attempts SET status='preparing' WHERE id=?`, attempt.ID); err == nil {
		t.Fatal("terminal receipt status moved back to preparing")
	}
	got, err := store.GetAuthoringCodeCopyAttempt(t.Context(), run.ID, attempt.RequestID)
	if err != nil || got.ID != attempt.ID || got.PrivatePath != attempt.PrivatePath || got.ManifestHash != attempt.ManifestHash ||
		got.Status != "failed" || got.ErrorCode != "private_copy_failed" {
		t.Fatalf("immutable receipt readback = %+v, error = %v", got, err)
	}
}

func TestAuthoringCodeCopyReservationRejectsSelectionHashThatDoesNotMatchSnapshot(t *testing.T) {
	store, _, run := codeCopyFixture(t)
	attempt := codeCopyAttempt(run, "copy_selection_hash_mismatch", "copy_selection_hash_attempt")
	attempt.PreferenceSnapshot.SelectionHash = strings.Repeat("c", 64)
	if _, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), attempt); !errors.Is(err, sdd.ErrInvalidTransition) || created {
		t.Fatalf("mismatched selection hash reservation = created %v, error %v; want invalid transition", created, err)
	}
	stored, err := store.ListAuthoringCodeCopyAttempts(t.Context(), run.ID)
	if err != nil || len(stored) != 0 {
		t.Fatalf("invalid snapshot hash persisted attempts: %+v, error=%v", stored, err)
	}
}

func TestAuthoringCodeCopyReservationRejectsManifestEntriesWithMismatchedDigest(t *testing.T) {
	store, _, run := codeCopyFixture(t)
	attempt := codeCopyAttempt(run, "copy_manifest_hash_mismatch", "copy_manifest_hash_attempt")
	attempt.ManifestSnapshot.Entries = []catalog.AuthoringCodeCopyEntry{{
		Path: "app.txt", Type: "file", Mode: 0o100644, Size: 1, SHA256: strings.Repeat("a", 64),
	}}
	attempt.ManifestSnapshot.FileCount = 1
	attempt.ManifestSnapshot.TotalBytes = 1
	if _, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), attempt); !errors.Is(err, sdd.ErrInvalidTransition) || created {
		t.Fatalf("manifest/digest mismatch reservation = created %v, error %v; want invalid transition", created, err)
	}
	stored, err := store.ListAuthoringCodeCopyAttempts(t.Context(), run.ID)
	if err != nil || len(stored) != 0 {
		t.Fatalf("invalid manifest digest persisted attempts: %+v, error=%v", stored, err)
	}
}
