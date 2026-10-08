package sqlite

import (
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestBrainstormHistoryListingIsBoundedOrderedAndSurvivesReopen(t *testing.T) {
	store, path, first, _ := brainstormFixture(t)
	if err := store.ReviseAuthoringDiscovery(t.Context(), first.PipelineID, 2, 1, "Discovery version two", "Second", "Second objective"); err != nil {
		t.Fatal(err)
	}
	second, err := store.StartBrainstorming(t.Context(), catalog.StartBrainstormingRequest{
		PipelineID: first.PipelineID, RequestID: "start_history_v2_01", PipelineRevision: 3, DiscoveryVersion: 2, Selection: brainstormChoice(),
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.ListBrainstormingByPipeline(t.Context(), first.PipelineID, 0)
	if err != nil || len(items) != 2 || items[0].ID != second.ID || items[0].DiscoveryVersion != 2 || items[1].ID != first.ID || items[1].DiscoveryVersion != 1 {
		t.Fatalf("ordered history = %+v, %v", items, err)
	}
	limited, err := store.ListBrainstormingByPipeline(t.Context(), first.PipelineID, 1)
	if err != nil || len(limited) != 1 || limited[0].ID != second.ID {
		t.Fatalf("limited history = %+v, %v", limited, err)
	}
	for _, limit := range []int{-1, 101} {
		if _, err := store.ListBrainstormingByPipeline(t.Context(), first.PipelineID, limit); err == nil {
			t.Fatalf("invalid limit %d accepted", limit)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	items, err = reopened.ListBrainstormingByPipeline(t.Context(), first.PipelineID, 10)
	if err != nil || len(items) != 2 || items[0].DiscoveryVersion != 2 || items[1].DiscoveryVersion != 1 {
		t.Fatalf("reopened history = %+v, %v", items, err)
	}
}
