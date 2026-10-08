package id_test

import (
	"regexp"
	"testing"

	"github.com/persioflexa/harflex/internal/id"
)

func TestNewGeneratesDistinctLowercaseHexIDs(t *testing.T) {
	first := id.New()
	second := id.New()

	if first == second {
		t.Fatalf("New() returned duplicate IDs: %q", first)
	}

	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(first) {
		t.Fatalf("New() returned invalid ID: %q", first)
	}
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(second) {
		t.Fatalf("New() returned invalid ID: %q", second)
	}
}
