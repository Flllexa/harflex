package sqlite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/sddworkspace"
)

func codeRunStorageFixture(t *testing.T) (*Store, catalog.AuthoringCodeRun) {
	t.Helper()
	store, _, pipeline := codeRunStorageFixtureWithPath(t)
	return store, pipeline
}

func codeRunStorageFixtureWithPath(t *testing.T) (*Store, string, catalog.AuthoringCodeRun) {
	t.Helper()
	store, path, pipeline := codeCopyFixture(t)
	statuses := `{"discovery":"completed","spec":"completed","plan":"skipped","code":"active","eval":"pending"}`
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET stage_status=?,revision=revision+1 WHERE id=?`, statuses, pipeline.ID); err != nil {
		t.Fatal(err)
	}
	now := formatCatalogTime(time.Now().UTC())
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO pipeline_authoring_stages(
	 pipeline_id,stage,revision,state,artifact_version,attempt_count,input_budget_remaining,output_budget_remaining,created_at,updated_at)
	 VALUES(?,'plan',0,'ready',0,0,1572864,24576,?,?)`, pipeline.ID, now, now); err != nil {
		t.Fatal(err)
	}
	pipeline, err := store.GetPipeline(t.Context(), pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_authoring_stages SET state='skipped',revision=revision+1 WHERE pipeline_id=? AND stage='plan'`, pipeline.ID); err != nil {
		t.Fatal(err)
	}
	var planRevision, planVersion int64
	if err := store.DB().QueryRowContext(t.Context(), `SELECT revision,artifact_version FROM pipeline_authoring_stages WHERE pipeline_id=? AND stage='plan'`, pipeline.ID).Scan(&planRevision, &planVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO pipeline_authoring_requests(
	 pipeline_id,request_id,stage,action,payload_hash,client_intent_hash,attempt_id,artifact_version,actor,feedback,reason,
	 result_stage_revision,result_pipeline_revision,result_snapshot,created_at)
	 VALUES(?,?,'plan','skip',?,?, '',?,'local_user','','bypass Plan for storage fixture',?,?, '{}',?)`,
		pipeline.ID, "storage_fixture_skip_plan", strings.Repeat("a", 64), strings.Repeat("b", 64), planVersion,
		planRevision, pipeline.Revision, formatCatalogTime(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	workspace, err := store.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	copyAttempt := codeCopyAttempt(pipeline, "storage_code_preparation_0001", "storage_code_preparation")
	copyAttempt.SourcePath = workspace.Path
	copyAttempt, created, err := store.BeginAuthoringCodeCopyAttempt(t.Context(), copyAttempt)
	if err != nil || !created {
		t.Fatalf("begin preparation: %+v created=%v error=%v", copyAttempt, created, err)
	}
	copyAttempt, err = store.CompleteAuthoringCodeCopyAttempt(t.Context(), copyAttempt.ID, copyAttempt.PrivatePath, copyAttempt.ManifestHash, copyAttempt.PreferenceSnapshot.SelectionHash)
	if err != nil || copyAttempt.Status != "prepared" {
		t.Fatalf("prepare receipt: %+v error=%v", copyAttempt, err)
	}
	attempt := catalog.AuthoringCodeRun{
		ID: "storage_code_run_0001", PipelineID: pipeline.ID, PreparationID: copyAttempt.ID,
		PreparationRequestID: copyAttempt.RequestID, RequestID: "storage_code_start_0001", IntentHash: strings.Repeat("c", 64),
		WorkspaceID: pipeline.WorkspaceID, PipelineRevision: pipeline.Revision, PlanStageRevision: planRevision,
		PlanArtifactVersion: planVersion, CodePreferenceRevision: copyAttempt.CodePreferenceRevision,
		CodeSelectionHash: copyAttempt.PreferenceSnapshot.SelectionHash, ManifestHash: copyAttempt.ManifestHash,
		PlanSourceHash: strings.Repeat("d", 64), PrivatePath: copyAttempt.PrivatePath,
		PreferenceSnapshot: copyAttempt.PreferenceSnapshot, ManifestSnapshot: copyAttempt.ManifestSnapshot,
		MaxPromptBytes: 1024 * 1024, MaxOutputTokens: sdd.MaxAuthoringCodeOutputTokens,
		MaxOutputTokensPerTurn: copyAttempt.PreferenceSnapshot.Selection.MaxOutputTokens,
		MaxTurns:               sdd.MaxAuthoringCodeTurns, MaxToolCalls: sdd.MaxAuthoringCodeToolCalls,
		TimeoutMillis: sdd.AuthoringAttemptTimeout.Milliseconds(), Status: "running",
	}
	return store, path, attempt
}

func TestAuthoringCodeRunReservationIsIdempotentAndOneUsePerPreparation(t *testing.T) {
	store, first := codeRunStorageFixture(t)
	stalePipeline := first
	stalePipeline.ID = "stale_pipeline_revision_run"
	stalePipeline.RequestID = "stale_pipeline_revision_request"
	stalePipeline.PipelineRevision--
	if _, created, err := store.BeginAuthoringCodeRun(t.Context(), stalePipeline); !errors.Is(err, ErrPipelineConflict) || created {
		t.Fatalf("Code run with stale pipeline revision created=%v error=%v", created, err)
	}
	stalePlan := first
	stalePlan.ID = "stale_plan_revision_run"
	stalePlan.RequestID = "stale_plan_revision_request"
	stalePlan.PlanStageRevision--
	if _, created, err := store.BeginAuthoringCodeRun(t.Context(), stalePlan); !errors.Is(err, ErrPipelineConflict) || created {
		t.Fatalf("Code run with stale Plan revision created=%v error=%v", created, err)
	}
	saved, created, err := store.BeginAuthoringCodeRun(t.Context(), first)
	if err != nil || !created || saved.ID != first.ID || saved.Status != "running" || saved.SessionID != "" {
		t.Fatalf("first Code reservation = %+v created=%v error=%v", saved, created, err)
	}
	replay := first
	replay.ID = "storage_code_run_replay"
	saved, created, err = store.BeginAuthoringCodeRun(t.Context(), replay)
	if err != nil || created || saved.ID != first.ID || saved.Status != "running" {
		t.Fatalf("idempotent Code replay = %+v created=%v error=%v", saved, created, err)
	}
	conflict := replay
	conflict.IntentHash = strings.Repeat("e", 64)
	if _, created, err := store.BeginAuthoringCodeRun(t.Context(), conflict); !errors.Is(err, ErrPipelineConflict) || created {
		t.Fatalf("conflicting Code RequestID created=%v error=%v", created, err)
	}
	otherRequest := first
	otherRequest.ID, otherRequest.RequestID = "storage_code_run_reuse", "storage_code_start_reuse"
	if _, created, err := store.BeginAuthoringCodeRun(t.Context(), otherRequest); !errors.Is(err, ErrPipelineConflict) || created {
		t.Fatalf("second Code run reused prepared root: created=%v error=%v", created, err)
	}
}

func TestAuthoringCodeRunRecoveryInterruptsWithoutReplayingPrompt(t *testing.T) {
	store, attempt := codeRunStorageFixture(t)
	reserved, created, err := store.BeginAuthoringCodeRun(t.Context(), attempt)
	if err != nil || !created {
		t.Fatalf("reserve interrupted Code run: %+v created=%v error=%v", reserved, created, err)
	}
	if attempt, err := store.RequestAuthoringCodeRunCancel(t.Context(), reserved.ID); err != nil || attempt.Status != "cancelling" {
		t.Fatalf("record pending stop before simulated restart: %+v error=%v", attempt, err)
	}
	if err := store.InterruptRunningAuthoringCodeRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	interrupted, err := store.GetAuthoringCodeRunByID(t.Context(), reserved.ID)
	if err != nil || interrupted.Status != "interrupted" || interrupted.ErrorCode != "app_restart" {
		t.Fatalf("interrupted Code run readback = %+v error=%v", interrupted, err)
	}
	replay := attempt
	replay.ID = "different-id-after-restart"
	again, created, err := store.BeginAuthoringCodeRun(t.Context(), replay)
	if err != nil || created || again.ID != reserved.ID || again.Status != "interrupted" {
		t.Fatalf("restart replay repeated or lost Code run: %+v created=%v error=%v", again, created, err)
	}
}

func TestAuthoringCodeRunTerminalReadbackCannotBeRewritten(t *testing.T) {
	store, attempt := codeRunStorageFixture(t)
	reserved, created, err := store.BeginAuthoringCodeRun(t.Context(), attempt)
	if err != nil || !created {
		t.Fatalf("reserve Code run: %+v created=%v error=%v", reserved, created, err)
	}
	manifest := reserved.ManifestSnapshot
	patchJSON, err := json.Marshal(sddworkspace.PatchEvidence{Version: 1, BaselineHash: manifest.Hash, SourceManifestHash: manifest.Hash, ResultManifestHash: manifest.Hash, Changes: []sddworkspace.PatchChange{}})
	if err != nil {
		t.Fatal(err)
	}
	patchDigest := sha256.Sum256(patchJSON)
	patchHash := hex.EncodeToString(patchDigest[:])
	terminal, err := store.FinishAuthoringCodeRun(t.Context(), reserved.ID, "completed", "", nil, &manifest, &manifest, []catalog.CodeFileChange{}, patchJSON, patchHash)
	if err != nil || terminal.Status != "completed" || terminal.SourceManifest == nil {
		t.Fatalf("complete Code readback = %+v error=%v", terminal, err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_authoring_code_runs SET patch_json='{}' WHERE id=?`, reserved.ID); err == nil {
		t.Fatal("terminal patch evidence was mutable")
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_authoring_code_runs SET updated_at=? WHERE id=?`, formatCatalogTime(time.Now().UTC().Add(time.Minute)), reserved.ID); err == nil {
		t.Fatal("terminal Code run timestamp was mutable")
	}
}

func TestAuthoringCodeRunCannotCompleteWithoutImmutablePatchEvidence(t *testing.T) {
	store, attempt := codeRunStorageFixture(t)
	reserved, created, err := store.BeginAuthoringCodeRun(t.Context(), attempt)
	if err != nil || !created {
		t.Fatalf("reserve Code run: %+v created=%v error=%v", reserved, created, err)
	}
	manifest := reserved.ManifestSnapshot
	if _, err := store.FinishAuthoringCodeRun(t.Context(), reserved.ID, "completed", "", nil, &manifest, &manifest, []catalog.CodeFileChange{}, nil, ""); err == nil {
		t.Fatal("Code run completed without persisted patch bytes and patch hash")
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_authoring_code_runs SET status='completed',result_manifest_json=?,source_manifest_json=?,changes_json='[]' WHERE id=?`, manifestJSON, manifestJSON, reserved.ID); err == nil {
		t.Fatal("SQLite allowed a completed Code run without patch bytes and patch hash")
	}
	readback, err := store.GetAuthoringCodeRunByID(t.Context(), reserved.ID)
	if err != nil || readback.Status != "running" || len(readback.PatchJSON) != 0 || readback.PatchHash != "" {
		t.Fatalf("missing patch changed the admitted run: %+v error=%v", readback, err)
	}
}

func TestAuthoringCodeDecisionRejectsRunningRunWithoutPipelineEffects(t *testing.T) {
	store, attempt := codeRunStorageFixture(t)
	reserved, created, err := store.BeginAuthoringCodeRun(t.Context(), attempt)
	if err != nil || !created {
		t.Fatalf("reserve running Code run: created=%v error=%v", created, err)
	}
	request := catalog.AuthoringCodeDecisionRequest{
		PipelineID: reserved.PipelineID, PipelineRevision: reserved.PipelineRevision,
		RunID: reserved.ID, RequestID: "running_decision_request_01", Action: "approve",
		PatchHash: strings.Repeat("a", 64), BaselineHash: reserved.ManifestHash,
		SourceManifestHash: reserved.ManifestHash, ResultManifestHash: reserved.ManifestHash,
		IntentHash: strings.Repeat("b", 64),
	}
	if _, err := store.DecideAuthoringCode(t.Context(), request); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("decision for running Code run error = %v; want pipeline conflict", err)
	}
	pipeline, err := store.GetPipeline(t.Context(), reserved.PipelineID)
	if err != nil || pipeline.Current != sdd.Code || pipeline.Status[sdd.Code] != sdd.Active || pipeline.Revision != reserved.PipelineRevision {
		t.Fatalf("rejected decision changed pipeline: %+v error=%v", pipeline, err)
	}
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pipeline_authoring_code_decisions WHERE pipeline_id=?`, reserved.PipelineID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected running decision persisted %d records, error=%v", count, err)
	}
}

func TestAuthoringCodeDecisionPersistsAndReplaysAcrossStoreHandles(t *testing.T) {
	store, path, attempt := codeRunStorageFixtureWithPath(t)
	reserved, created, err := store.BeginAuthoringCodeRun(t.Context(), attempt)
	if err != nil || !created {
		t.Fatalf("reserve Code run: created=%v error=%v", created, err)
	}
	snapshot := reserved.ManifestSnapshot
	patchJSON, err := json.Marshal(sddworkspace.PatchEvidence{
		Version: 1, BaselineHash: snapshot.Hash, SourceManifestHash: snapshot.Hash,
		ResultManifestHash: snapshot.Hash, Changes: []sddworkspace.PatchChange{},
	})
	if err != nil {
		t.Fatal(err)
	}
	patchDigest := sha256.Sum256(patchJSON)
	patchHash := hex.EncodeToString(patchDigest[:])
	finished, err := store.FinishAuthoringCodeRun(t.Context(), reserved.ID, "completed", "", nil, &snapshot, &snapshot, []catalog.CodeFileChange{}, patchJSON, patchHash)
	if err != nil || finished.Status != "completed" {
		t.Fatalf("complete Code run evidence: status=%q error=%v", finished.Status, err)
	}
	request := catalog.AuthoringCodeDecisionRequest{
		PipelineID: finished.PipelineID, PipelineRevision: finished.PipelineRevision,
		RunID: finished.ID, RequestID: "durable_code_decision_request_01", Action: "approve",
		PatchHash: patchHash, BaselineHash: finished.ManifestHash,
		SourceManifestHash: snapshot.Hash, ResultManifestHash: snapshot.Hash,
		IntentHash: strings.Repeat("c", 64),
	}
	decision, err := store.DecideAuthoringCode(t.Context(), request)
	if err != nil || decision.Actor != "local_user" || decision.ResultPipelineRevision != finished.PipelineRevision+1 {
		t.Fatalf("approve stored Code decision = %+v error=%v", decision, err)
	}
	peer, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	readback, err := peer.GetAuthoringCodeDecision(t.Context(), finished.PipelineID, request.RequestID)
	if err != nil || !reflect.DeepEqual(readback, decision) {
		t.Fatalf("decision read from peer store = %+v error=%v; want %+v", readback, err, decision)
	}
	replay, err := peer.DecideAuthoringCode(t.Context(), request)
	if err != nil || !reflect.DeepEqual(replay, decision) {
		t.Fatalf("decision replay from peer store = %+v error=%v; want %+v", replay, err, decision)
	}
	conflict := request
	conflict.IntentHash = strings.Repeat("d", 64)
	if _, err := peer.DecideAuthoringCode(t.Context(), conflict); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("divergent decision replay error = %v; want pipeline conflict", err)
	}
	pipeline, err := peer.GetPipeline(t.Context(), finished.PipelineID)
	if err != nil || pipeline.Current != sdd.Eval || pipeline.Status[sdd.Code] != sdd.Completed || pipeline.Status[sdd.Eval] != sdd.Active {
		t.Fatalf("stored decision pipeline state = %+v error=%v", pipeline, err)
	}
}

func TestAuthoringCodeDecisionRejectsRunOlderThanLatestCodeVersion(t *testing.T) {
	store, _, attempt := codeRunStorageFixtureWithPath(t)
	reserved, created, err := store.BeginAuthoringCodeRun(t.Context(), attempt)
	if err != nil || !created {
		t.Fatalf("reserve first Code run: created=%v error=%v", created, err)
	}
	snapshot := reserved.ManifestSnapshot
	patchJSON, err := json.Marshal(sddworkspace.PatchEvidence{
		Version: 1, BaselineHash: snapshot.Hash, SourceManifestHash: snapshot.Hash,
		ResultManifestHash: snapshot.Hash, Changes: []sddworkspace.PatchChange{},
	})
	if err != nil {
		t.Fatal(err)
	}
	patchDigest := sha256.Sum256(patchJSON)
	patchHash := hex.EncodeToString(patchDigest[:])
	first, err := store.FinishAuthoringCodeRun(t.Context(), reserved.ID, "completed", "", nil, &snapshot, &snapshot, []catalog.CodeFileChange{}, patchJSON, patchHash)
	if err != nil {
		t.Fatalf("complete first Code run: %v", err)
	}
	latestAt := formatCatalogTime(first.CreatedAt.Add(time.Second))
	const latestPreparationID = "storage_code_preparation_latest"
	const latestPreparationRequest = "storage_code_preparation_latest_request"
	const latestPrivatePath = "/private/storage_code_preparation_latest"
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO pipeline_authoring_code_copy_attempts(
	 id,pipeline_id,workspace_id,request_id,intent_hash,pipeline_revision,code_preference_revision,source_path,private_path,
	 manifest_hash,preference_snapshot_json,manifest_snapshot_json,status,error_code,created_at,updated_at)
	 SELECT ?,pipeline_id,workspace_id,?,intent_hash,pipeline_revision,code_preference_revision,source_path,?,manifest_hash,
	 preference_snapshot_json,manifest_snapshot_json,'prepared','',?,? FROM pipeline_authoring_code_copy_attempts WHERE id=?`,
		latestPreparationID, latestPreparationRequest, latestPrivatePath, latestAt, latestAt, first.PreparationID); err != nil {
		t.Fatalf("insert newer prepared-copy fixture: %v", err)
	}
	const latestRunID = "storage_code_run_latest"
	const latestRunRequest = "storage_code_run_latest_request"
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO pipeline_authoring_code_runs(
	 id,pipeline_id,preparation_id,preparation_request_id,request_id,intent_hash,workspace_id,pipeline_revision,
	 plan_stage_revision,plan_artifact_version,code_preference_revision,code_selection_hash,manifest_hash,plan_source_hash,
	 private_path,preference_snapshot_json,manifest_snapshot_json,max_prompt_bytes,max_output_tokens,max_output_tokens_per_turn,
	 max_turns,max_tool_calls,timeout_millis,session_id,status,error_code,usage_json,result_manifest_json,source_manifest_json,
	 changes_json,patch_json,patch_hash,created_at,updated_at)
	 SELECT ?,pipeline_id,?,?,?,intent_hash,workspace_id,pipeline_revision,plan_stage_revision,plan_artifact_version,
	 code_preference_revision,code_selection_hash,manifest_hash,plan_source_hash,?,preference_snapshot_json,manifest_snapshot_json,
	 max_prompt_bytes,max_output_tokens,max_output_tokens_per_turn,max_turns,max_tool_calls,timeout_millis,session_id,'completed',
	 error_code,usage_json,result_manifest_json,source_manifest_json,changes_json,patch_json,patch_hash,?,?
	 FROM pipeline_authoring_code_runs WHERE id=?`,
		latestRunID, latestPreparationID, latestPreparationRequest, latestRunRequest,
		latestPrivatePath, latestAt, latestAt, first.ID); err != nil {
		t.Fatalf("insert newer completed-run fixture: %v", err)
	}
	request := catalog.AuthoringCodeDecisionRequest{
		PipelineID: first.PipelineID, PipelineRevision: first.PipelineRevision,
		RunID: first.ID, RequestID: "decide_older_code_run_request", Action: "approve",
		PatchHash: first.PatchHash, BaselineHash: first.ManifestHash,
		SourceManifestHash: first.SourceManifest.Hash, ResultManifestHash: first.ResultManifest.Hash,
		IntentHash: strings.Repeat("e", 64),
	}
	if _, err := store.DecideAuthoringCode(t.Context(), request); !errors.Is(err, ErrPipelineConflict) {
		t.Fatalf("decision for non-current Code run error = %v; want pipeline conflict", err)
	}
	pipeline, err := store.GetPipeline(t.Context(), first.PipelineID)
	if err != nil || pipeline.Current != sdd.Code || pipeline.Revision != first.PipelineRevision {
		t.Fatalf("rejected stale-run decision changed pipeline: %+v error=%v", pipeline, err)
	}
}

func TestCompletedAuthoringCodeReadbackRejectsTamperedSourceManifest(t *testing.T) {
	store, attempt := codeRunStorageFixture(t)
	reserved, created, err := store.BeginAuthoringCodeRun(t.Context(), attempt)
	if err != nil || !created {
		t.Fatalf("reserve Code run: %+v created=%v error=%v", reserved, created, err)
	}
	manifest := reserved.ManifestSnapshot
	patchJSON, err := json.Marshal(sddworkspace.PatchEvidence{Version: 1, BaselineHash: manifest.Hash, SourceManifestHash: manifest.Hash, ResultManifestHash: manifest.Hash, Changes: []sddworkspace.PatchChange{}})
	if err != nil {
		t.Fatal(err)
	}
	patchDigest := sha256.Sum256(patchJSON)
	if _, err := store.FinishAuthoringCodeRun(t.Context(), reserved.ID, "completed", "", nil, &manifest, &manifest, []catalog.CodeFileChange{}, patchJSON, hex.EncodeToString(patchDigest[:])); err != nil {
		t.Fatalf("complete Code run: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `DROP TRIGGER authoring_code_runs_terminal_evidence`); err != nil {
		t.Fatalf("disable terminal guard in isolated corruption fixture: %v", err)
	}
	broken := manifest
	broken.Excluded = []catalog.AuthoringCodeCopyExclusion{{Path: "ignored.txt", Reason: "fixture corruption"}}
	brokenJSON, err := json.Marshal(broken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_authoring_code_runs SET source_manifest_json=? WHERE id=?`, brokenJSON, reserved.ID); err != nil {
		t.Fatalf("write corruption into isolated fixture: %v", err)
	}
	if _, err := store.GetAuthoringCodeRunByID(t.Context(), reserved.ID); err == nil {
		t.Fatal("completed run readback accepted a source manifest whose entries no longer match its hash")
	}
}
