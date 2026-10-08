package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

type committedAuthoringErrorStore struct{ Store }

type missingRequestBarrier struct {
	arrivals atomic.Int32
	release  chan struct{}
}

func (b *missingRequestBarrier) wait(ctx context.Context) error {
	if b.arrivals.Add(1) == 2 {
		close(b.release)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("timed out waiting for two absent request lookups")
	}
}

type firstMissingRequestBarrierStore struct {
	Store
	barrier *missingRequestBarrier
	first   sync.Once
	lookups atomic.Int32
}

func (s *firstMissingRequestBarrierStore) GetAuthoringPipelineByRequest(ctx context.Context, workspaceID, parentID, requestID string) (catalog.PipelineRun, error) {
	s.lookups.Add(1)
	run, err := s.Store.GetAuthoringPipelineByRequest(ctx, workspaceID, parentID, requestID)
	if errors.Is(err, sql.ErrNoRows) {
		var waitErr error
		s.first.Do(func() { waitErr = s.barrier.wait(ctx) })
		if waitErr != nil {
			return catalog.PipelineRun{}, waitErr
		}
	}
	return run, err
}

type failedRecoveryLookupStore struct {
	Store
	readErr error
	reads   int
}

func (s *failedRecoveryLookupStore) GetAuthoringPipelineByRequest(ctx context.Context, workspaceID, parentID, requestID string) (catalog.PipelineRun, error) {
	s.reads++
	if s.reads == 2 {
		return catalog.PipelineRun{}, s.readErr
	}
	return s.Store.GetAuthoringPipelineByRequest(ctx, workspaceID, parentID, requestID)
}

func (s committedAuthoringErrorStore) CreateAuthoringPipeline(ctx context.Context, run catalog.PipelineRun, discovery string) error {
	if err := s.Store.CreateAuthoringPipeline(ctx, run, discovery); err != nil {
		return err
	}
	return errors.New("response lost after create commit")
}

func (s committedAuthoringErrorStore) DeriveAuthoringPipeline(ctx context.Context, parentID string, expectedRevision int64, run catalog.PipelineRun, discovery string) error {
	if err := s.Store.DeriveAuthoringPipeline(ctx, parentID, expectedRevision, run, discovery); err != nil {
		return err
	}
	return errors.New("response lost after derive commit")
}

func openAuthoringRequestService(t *testing.T, path string) (*Service, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewService(t.Context(), Dependencies{Store: store, External: map[string]ExternalBackend{}}), store
}

func authoringRequestCount(t *testing.T, store *sqlite.Store, workspaceID string) int {
	t.Helper()
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pipeline_runs WHERE workspace_id = ? AND kind = 'ai_authoring'`, workspaceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func authoringRequestEventCount(t *testing.T, store *sqlite.Store, pipelineID string) int {
	t.Helper()
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM events WHERE stream_id = ?`, pipelineID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func expectedAuthoringRequestHash(t *testing.T, fields ...string) string {
	t.Helper()
	canonical, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:])
}

func TestAuthoringRequestRequiresValidIDBeforeWrites(t *testing.T) {
	s, store, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "valid-parent-0001", Discovery: "Parent Discovery"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FreezeDiscovery(t.Context(), parent.ID, parent.Revision, parent.Artifacts["discovery"].Version); err != nil {
		t.Fatal(err)
	}
	parent, err = s.GetPipeline(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeRows := authoringRequestCount(t, store, workspace.ID)
	beforeEvents := authoringRequestEventCount(t, store, parent.ID)
	for _, requestID := range []string{"", "short", strings.Repeat("x", 65), "bad request id-0001", "bad/request-id-0001", "invalid-é-0000001"} {
		t.Run(requestID, func(t *testing.T) {
			if _, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: requestID, Discovery: "New Discovery"}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("create accepted invalid request ID %q: %v", requestID, err)
			}
			if _, err := s.DeriveAuthoringPipeline(DeriveAuthoringPipelineInput{ParentPipelineID: parent.ID, RequestID: requestID, ExpectedRevision: parent.Revision, Discovery: "Child Discovery"}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("derive accepted invalid request ID %q: %v", requestID, err)
			}
			if count := authoringRequestCount(t, store, workspace.ID); count != beforeRows {
				t.Fatalf("invalid request changed pipeline count: %d", count)
			}
			if count := authoringRequestEventCount(t, store, parent.ID); count != beforeEvents {
				t.Fatalf("invalid request changed parent event count: %d", count)
			}
		})
	}
}

func TestAuthoringRequestCreateRetryReturnsCurrentPipelineAcrossRevisionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authoring-retry.db")
	s, store := openAuthoringRequestService(t, path)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in := CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "root-retry-000001", Discovery: "# Original\nFull Discovery"}
	created, err := s.CreateAuthoringPipeline(in)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetPipeline(t.Context(), created.ID)
	if err != nil || stored.CreationRequestID != in.RequestID || stored.CreationRequestHash != expectedAuthoringRequestHash(t, "create_authoring_pipeline", in.WorkspaceID, in.Discovery) {
		t.Fatalf("creation fingerprint: %+v, %v", stored, err)
	}
	retried, err := s.CreateAuthoringPipeline(in)
	if err != nil || !reflect.DeepEqual(retried, created) || authoringRequestCount(t, store, workspace.ID) != 1 || authoringRequestEventCount(t, store, created.ID) != 2 {
		t.Fatalf("immediate retry: %+v, %v", retried, err)
	}
	revised, err := s.ReviseAuthoringDiscovery(ReviseAuthoringDiscoveryInput{PipelineID: created.ID, ExpectedRevision: created.Revision,
		ExpectedVersion: created.Artifacts["discovery"].Version, Discovery: "# Revised\nCurrent Discovery"})
	if err != nil {
		t.Fatal(err)
	}
	retried, err = s.CreateAuthoringPipeline(in)
	if err != nil || !reflect.DeepEqual(retried, revised) || authoringRequestEventCount(t, store, created.ID) != 3 {
		t.Fatalf("retry after revision: %+v, %v", retried, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, reopened := openAuthoringRequestService(t, path)
	retried, err = restarted.CreateAuthoringPipeline(in)
	if err != nil || !reflect.DeepEqual(retried, revised) || authoringRequestCount(t, reopened, workspace.ID) != 1 || authoringRequestEventCount(t, reopened, created.ID) != 3 {
		t.Fatalf("retry after restart: %+v, %v", retried, err)
	}
}

func TestAuthoringRequestCreateChangedDiscoveryConflicts(t *testing.T) {
	s, store, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in := CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "root-conflict-0001", Discovery: "First Discovery"}
	created, err := s.CreateAuthoringPipeline(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Discovery = "Second Discovery"
	if _, err := s.CreateAuthoringPipeline(in); !errors.Is(err, ErrPipelineRequestConflict) || ErrorCode(err) != "pipeline_request_conflict" {
		t.Fatalf("changed Discovery reused request: %v", err)
	}
	if count := authoringRequestCount(t, store, workspace.ID); count != 1 {
		t.Fatalf("conflict created %d pipelines", count)
	}
	if count := authoringRequestEventCount(t, store, created.ID); count != 2 {
		t.Fatalf("conflict appended %d events", count)
	}
}

func TestAuthoringRequestRecoversCommittedCreateAndDeriveAfterLostResponse(t *testing.T) {
	s, store, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uncertain := NewService(t.Context(), Dependencies{Store: committedAuthoringErrorStore{Store: store}, External: map[string]ExternalBackend{}})
	parent, err := uncertain.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "uncertain-root-0001", Discovery: "Parent Discovery"})
	if err != nil || parent.ID == "" || parent.Artifacts["discovery"].Content != "Parent Discovery" || authoringRequestEventCount(t, store, parent.ID) != 2 {
		t.Fatalf("committed create was not recovered: %+v, %v", parent, err)
	}
	if err := store.FreezeDiscovery(t.Context(), parent.ID, parent.Revision, 1); err != nil {
		t.Fatal(err)
	}
	parent, err = s.GetPipeline(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := uncertain.DeriveAuthoringPipeline(DeriveAuthoringPipelineInput{ParentPipelineID: parent.ID, RequestID: "uncertain-child-0001", ExpectedRevision: parent.Revision, Discovery: "Child Discovery"})
	if err != nil || child.ID == "" || child.DerivedFromPipelineID != parent.ID || child.Artifacts["discovery"].Content != "Child Discovery" || authoringRequestEventCount(t, store, child.ID) != 2 {
		t.Fatalf("committed derive was not recovered: %+v, %v", child, err)
	}
	if authoringRequestCount(t, store, workspace.ID) != 2 {
		t.Fatal("lost responses duplicated an authoring pipeline")
	}
}

func TestAuthoringRequestDerivedRetryPrecedesStaleParentGate(t *testing.T) {
	s, store, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "derived-parent-0001", Discovery: "Parent Discovery"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FreezeDiscovery(t.Context(), parent.ID, parent.Revision, 1); err != nil {
		t.Fatal(err)
	}
	parent, err = s.GetPipeline(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := DeriveAuthoringPipelineInput{ParentPipelineID: parent.ID, RequestID: "derived-retry-0001", ExpectedRevision: parent.Revision, Discovery: "Child Discovery"}
	child, err := s.DeriveAuthoringPipeline(in)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetPipeline(t.Context(), child.ID)
	if err != nil || stored.CreationRequestID != in.RequestID || stored.CreationRequestHash != expectedAuthoringRequestHash(t, "derive_authoring_pipeline", parent.ID, parent.WorkspaceID, "2", in.Discovery) {
		t.Fatalf("derived fingerprint: %+v, %v", stored, err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET revision = revision + 1, discovery_frozen_version = 0 WHERE id = ?`, parent.ID); err != nil {
		t.Fatal(err)
	}
	retried, err := s.DeriveAuthoringPipeline(in)
	if err != nil || !reflect.DeepEqual(retried, child) || authoringRequestCount(t, store, workspace.ID) != 2 || authoringRequestEventCount(t, store, child.ID) != 2 {
		t.Fatalf("derived retry after parent changed: %+v, %v", retried, err)
	}
	in.Discovery = "Different Child Discovery"
	if _, err := s.DeriveAuthoringPipeline(in); !errors.Is(err, ErrPipelineRequestConflict) {
		t.Fatalf("changed derived Discovery reused key: %v", err)
	}
	in.Discovery = "Child Discovery"
	in.ExpectedRevision++
	if _, err := s.DeriveAuthoringPipeline(in); !errors.Is(err, ErrPipelineRequestConflict) {
		t.Fatalf("changed expected revision reused key: %v", err)
	}
}

func TestAuthoringRequestDerivedKeyIsScopedToParent(t *testing.T) {
	s, store, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "shared-derived-0001"
	var children []PipelineDTO
	for _, suffix := range []string{"one", "two"} {
		parent, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "parent-" + suffix + "-00001", Discovery: "Parent " + suffix})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.FreezeDiscovery(t.Context(), parent.ID, parent.Revision, 1); err != nil {
			t.Fatal(err)
		}
		parent, err = s.GetPipeline(parent.ID)
		if err != nil {
			t.Fatal(err)
		}
		child, err := s.DeriveAuthoringPipeline(DeriveAuthoringPipelineInput{ParentPipelineID: parent.ID, RequestID: key, ExpectedRevision: parent.Revision, Discovery: "Same Discovery"})
		if err != nil {
			t.Fatal(err)
		}
		children = append(children, child)
	}
	if children[0].ID == children[1].ID || children[0].DerivedFromPipelineID == children[1].DerivedFromPipelineID || authoringRequestCount(t, store, workspace.ID) != 4 {
		t.Fatalf("derived keys crossed parents: %+v", children)
	}
}

func TestAuthoringRequestConcurrentServicesConverge(t *testing.T) {
	for _, differentDiscovery := range []bool{false, true} {
		name := "identical"
		if differentDiscovery {
			name = "conflicting"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "racing.db")
			workspaceService, firstStore := openAuthoringRequestService(t, path)
			_, secondStore := openAuthoringRequestService(t, path)
			workspace, err := workspaceService.OpenWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			barrier := &missingRequestBarrier{release: make(chan struct{})}
			firstStoreGate := &firstMissingRequestBarrierStore{Store: firstStore, barrier: barrier}
			secondStoreGate := &firstMissingRequestBarrierStore{Store: secondStore, barrier: barrier}
			first := NewService(t.Context(), Dependencies{Store: firstStoreGate, External: map[string]ExternalBackend{}})
			second := NewService(t.Context(), Dependencies{Store: secondStoreGate, External: map[string]ExternalBackend{}})
			inputs := []CreateAuthoringPipelineInput{
				{WorkspaceID: workspace.ID, RequestID: "racing-request-0001", Discovery: "First Discovery"},
				{WorkspaceID: workspace.ID, RequestID: "racing-request-0001", Discovery: "First Discovery"},
			}
			if differentDiscovery {
				inputs[1].Discovery = "Second Discovery"
			}
			services := []*Service{first, second}
			type result struct {
				pipeline PipelineDTO
				err      error
			}
			results := make([]result, 2)
			start := make(chan struct{})
			var workers sync.WaitGroup
			for i := range services {
				workers.Add(1)
				go func(i int) {
					defer workers.Done()
					<-start
					results[i].pipeline, results[i].err = services[i].CreateAuthoringPipeline(inputs[i])
				}(i)
			}
			close(start)
			workers.Wait()
			if barrier.arrivals.Load() != 2 || firstStoreGate.lookups.Load()+secondStoreGate.lookups.Load() != 3 {
				t.Fatalf("race did not force two missing lookups and one recovery lookup: arrivals=%d lookups=%d,%d",
					barrier.arrivals.Load(), firstStoreGate.lookups.Load(), secondStoreGate.lookups.Load())
			}
			if differentDiscovery {
				successes, conflicts := 0, 0
				for _, result := range results {
					switch {
					case result.err == nil:
						successes++
					case errors.Is(result.err, ErrPipelineRequestConflict):
						conflicts++
					default:
						t.Fatalf("unexpected race outcome: %+v", results)
					}
				}
				if successes != 1 || conflicts != 1 {
					t.Fatalf("conflicting race outcomes: %+v", results)
				}
			} else if results[0].err != nil || results[1].err != nil || results[0].pipeline.ID != results[1].pipeline.ID {
				t.Fatalf("identical race outcomes: %+v", results)
			}
			if authoringRequestCount(t, firstStore, workspace.ID) != 1 {
				t.Fatalf("racing request created multiple pipelines: %+v", results)
			}
			var existing PipelineDTO
			for _, result := range results {
				if result.err == nil {
					existing = result.pipeline
				}
			}
			if count := authoringRequestEventCount(t, firstStore, existing.ID); count != 2 {
				t.Fatalf("racing request appended %d events", count)
			}
		})
	}
}

func TestAuthoringRequestPreservesWriteAndRecoveryReadErrors(t *testing.T) {
	s, store, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_authoring_for_recovery BEFORE INSERT ON pipeline_runs
		WHEN NEW.kind = 'ai_authoring' BEGIN SELECT RAISE(ABORT, 'forced write failure'); END`); err != nil {
		t.Fatal(err)
	}
	readErr := errors.New("forced recovery read failure")
	faultedStore := &failedRecoveryLookupStore{Store: store, readErr: readErr}
	faultedService := NewService(t.Context(), Dependencies{Store: faultedStore, External: map[string]ExternalBackend{}})
	_, err = faultedService.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "failed-recovery-0001", Discovery: "Discovery"})
	if err == nil || !errors.Is(err, readErr) || !strings.Contains(errors.Unwrap(err).Error(), "forced write failure") ||
		strings.Contains(err.Error(), "forced write failure") || strings.Contains(err.Error(), "forced recovery read failure") || faultedStore.reads != 2 {
		t.Fatalf("write/recovery causes were lost or exposed: %v", err)
	}
	if authoringRequestCount(t, store, workspace.ID) != 0 {
		t.Fatal("failed write created a pipeline")
	}
}

func TestAuthoringRequestUnrelatedInsertFailureDoesNotBecomeSuccess(t *testing.T) {
	s, store, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_authoring BEFORE INSERT ON pipeline_runs
		WHEN NEW.kind = 'ai_authoring' BEGIN SELECT RAISE(ABORT, 'forced unrelated failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "failed-insert-0001", Discovery: "Discovery"})
	if err == nil || errors.Is(err, ErrPipelineRequestConflict) || ErrorCode(err) != "internal" || strings.Contains(err.Error(), "forced unrelated failure") {
		t.Fatalf("unrelated insert failure was misclassified or leaked: %v", err)
	}
	if authoringRequestCount(t, store, workspace.ID) != 0 {
		t.Fatal("failed insert created a pipeline")
	}
	if _, err := store.GetAuthoringPipelineByRequest(t.Context(), workspace.ID, "", "failed-insert-0001"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed request unexpectedly has readback: %v", err)
	}
}
