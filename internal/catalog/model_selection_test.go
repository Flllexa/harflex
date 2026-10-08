package catalog

import (
	"encoding/json"
	"testing"
	"time"
)

func TestUnknownContextPreservesPre029ModelSelectionJSON(t *testing.T) {
	selection := ModelSelection{BackendID: "api", ModelID: "chosen", CatalogRevision: "revision", Source: "openai_models", Destination: "https://provider.example/v1", Status: "listed", MaxOutputTokens: 256, CheckedAt: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	// This exact pre-029 shape is embedded in persisted Start/Begin payload
	// hashes. Adding an explicit contextLength:0 would break their replay.
	const before029 = `{"sessionId":"","backendId":"api","modelId":"chosen","reasoningEffort":"","catalogRevision":"revision","localRevision":"","source":"openai_models","destination":"https://provider.example/v1","status":"listed","confirmUnverifiedManual":false,"confirmUnfiltered":false,"confirmJitLoad":false,"maxOutputTokens":256,"executablePath":"","executableVersion":"","workspacePath":"","checkedAt":"2026-09-28T12:00:00Z"}`
	encoded, err := json.Marshal(selection)
	if err != nil || string(encoded) != before029 {
		t.Fatalf("pre-029 JSON changed: %s; error: %v", encoded, err)
	}
	selection.ContextLength = 8192
	encoded, err = json.Marshal(selection)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ModelSelection
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.ContextLength != 8192 {
		t.Fatalf("known context lost: %+v; error: %v", decoded, err)
	}
}
