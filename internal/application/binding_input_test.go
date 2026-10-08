package application

import (
	"encoding/json"
	"testing"
)

// The frontend has no catalog choice to send when the server resolves a saved phase
// preference. Wails decodes arguments with encoding/json, which rejects "" for time.Time, so
// the UI sends the zero time. This pins both halves of that contract.
func TestSelectionTimeContractWithFrontend(t *testing.T) {
	const noCatalogTime = "0001-01-01T00:00:00Z" // NO_CATALOG_TIME in frontend/src/lib/backend.ts
	var accepted GenerateAuthoringStageInput
	if err := json.Unmarshal([]byte(`{"selection":{"checkedAt":"`+noCatalogTime+`","maxOutputTokens":4096}}`), &accepted); err != nil {
		t.Fatalf("the frontend's no-selection payload no longer decodes: %v", err)
	}
	if !accepted.Selection.CheckedAt.IsZero() || accepted.Selection.MaxOutputTokens != 4096 {
		t.Fatalf("zero time must stay the zero time: %+v", accepted.Selection)
	}
	var save SaveAuthoringStageModelPreferenceInput
	if err := json.Unmarshal([]byte(`{"selection":{"checkedAt":"`+noCatalogTime+`"}}`), &save); err != nil {
		t.Fatalf("saving an inherited preference no longer decodes: %v", err)
	}
	var rejected GenerateAuthoringStageInput
	if err := json.Unmarshal([]byte(`{"selection":{"checkedAt":""}}`), &rejected); err == nil {
		t.Fatal("an empty time decodes now; the frontend workaround and this test can be retired")
	}
}
