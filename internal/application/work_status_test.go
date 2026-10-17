package application

import "testing"

func TestWorkStatusKnowsTheBrainstormStage(t *testing.T) {
	var keys []string
	for _, item := range workStageOrder {
		keys = append(keys, item.key)
	}
	want := []string{"discovery", "spec", "brainstorm", "plan", "code", "eval", "prs"}
	if len(keys) != len(want) {
		t.Fatalf("stages = %v, want %v", keys, want)
	}
	for index := range want {
		if keys[index] != want[index] {
			t.Fatalf("stages = %v, want %v", keys, want)
		}
	}
	if label := workStageLabel("brainstorm"); label != "Brainstorm" {
		t.Fatalf("label = %q", label)
	}
}
