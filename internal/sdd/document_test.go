package sdd

import (
	"strings"
	"testing"
)

func TestCanonicalAuthoringDocuments(t *testing.T) {
	for _, tc := range []struct {
		stage Stage
		data  string
	}{
		{Spec, `{"summary":"Scope","requirements":["Required"],"nonGoals":["Excluded"],"acceptanceCriteria":[{"id":"AC-1","criterion":"Observable acceptance"}]}`},
		{Plan, `{"summary":"Implementation","tasks":[{"id":"T-1","title":"Implement","files":["internal/example.go"],"steps":["Add behavior"],"tests":["Test behavior"],"dependsOn":[]}],"risks":["Integration"]}`},
	} {
		got, err := CanonicalAuthoringDocument(tc.stage, []byte(tc.data))
		if err != nil || string(got) != tc.data {
			t.Fatalf("canonical %s: %s %v", tc.stage, got, err)
		}
		for _, bad := range []string{tc.data + tc.data, strings.Replace(tc.data, `"summary":"`, `"summary":"duplicate","summary":"`, 1), strings.Replace(tc.data, `"id":"`, `"id":"duplicate","id":"`, 1), strings.Replace(tc.data, `"summary":`, `"Summary":`, 1), `{}`, strings.Repeat("x", 65537)} {
			if _, err := CanonicalAuthoringDocument(tc.stage, []byte(bad)); err == nil {
				t.Fatalf("invalid %s accepted", tc.stage)
			}
		}
	}
}

func TestPlanRejectsUnknownAndCyclicDependencies(t *testing.T) {
	for _, dependency := range []string{"T-1", "missing"} {
		data := `{"summary":"Plan","tasks":[{"id":"T-1","title":"Do","files":["file"],"steps":["Step"],"tests":["Test"],"dependsOn":["` + dependency + `"]}],"risks":["Risk"]}`
		if _, err := CanonicalAuthoringDocument(Plan, []byte(data)); err == nil {
			t.Fatal("invalid dependency accepted")
		}
	}
}
