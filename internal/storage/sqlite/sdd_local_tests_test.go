package sqlite

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func localTestFixture(t *testing.T) (*Store, catalog.PipelineRun, catalog.PipelineRun) {
	t.Helper()
	store, _, run := authoringFixture(t)
	if err := store.CreatePipeline(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	other := run
	other.ID, other.Title = run.ID+"-other", "Outro trabalho"
	if err := store.CreatePipeline(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	return store, run, other
}

func localTestRecord(run catalog.PipelineRun, id string) catalog.PipelineLocalTest {
	return catalog.PipelineLocalTest{ID: id, PipelineID: run.ID, WorkspaceID: run.WorkspaceID, ArtifactVersion: 2, BackupPath: "/cache/sdd-local-tests/" + id,
		Applied: json.RawMessage(`{"backup":[]}`), Status: "applied", CreatedAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
}

func TestOneLocalTestPerWorkspace(t *testing.T) {
	store, run, other := localTestFixture(t)
	if err := store.CreatePipelineLocalTest(t.Context(), localTestRecord(run, "first")); err != nil {
		t.Fatal(err)
	}
	err := store.CreatePipelineLocalTest(t.Context(), localTestRecord(other, "second"))
	var busy *catalog.LocalTestBusyError
	if !errors.As(err, &busy) || busy.PipelineID != run.ID || !errors.Is(err, catalog.ErrLocalTestBusy) {
		t.Fatalf("a second test in the project: %v", err)
	}
	if err := store.FinishPipelineLocalTest(t.Context(), "first", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivePipelineLocalTest(t.Context(), run.WorkspaceID); err == nil {
		t.Fatal("a finished test is still the project's active one")
	}
	if err := store.CreatePipelineLocalTest(t.Context(), localTestRecord(other, "second")); err != nil {
		t.Fatalf("a test after the first ended: %v", err)
	}
}

func TestLocalTestServicesReplaceByName(t *testing.T) {
	store, run, _ := localTestFixture(t)
	if err := store.CreatePipelineLocalTest(t.Context(), localTestRecord(run, "first")); err != nil {
		t.Fatal(err)
	}
	for _, service := range []catalog.LocalTestService{{Name: "web", TerminalID: "term-1", Command: "npm run dev"}, {Name: "api", TerminalID: "term-2", Command: "go run ."}, {Name: "web", TerminalID: "term-3", Command: "npm run dev -- --port 5174"}} {
		if err := store.AddPipelineLocalTestService(t.Context(), "first", service); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.LatestPipelineLocalTest(t.Context(), run.ID)
	want := []catalog.LocalTestService{{Name: "api", TerminalID: "term-2", Command: "go run ."}, {Name: "web", TerminalID: "term-3", Command: "npm run dev -- --port 5174"}}
	if err != nil || !reflect.DeepEqual(got.Services, want) {
		t.Fatalf("services: %+v %v", got.Services, err)
	}
}

func TestLocalTestRecordRoundTrips(t *testing.T) {
	store, run, _ := localTestFixture(t)
	record := localTestRecord(run, "first")
	if err := store.CreatePipelineLocalTest(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPipelineLocalTestSession(t.Context(), "first", "tester-session"); err != nil {
		t.Fatal(err)
	}
	got, err := store.LatestPipelineLocalTest(t.Context(), run.ID)
	record.SessionID, record.Services = "tester-session", []catalog.LocalTestService{}
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatalf("round trip:\n got %+v\nwant %+v\n%v", got, record, err)
	}
	active, err := store.ActivePipelineLocalTest(t.Context(), run.WorkspaceID)
	if err != nil || active.ID != "first" {
		t.Fatalf("active: %+v %v", active, err)
	}
}
