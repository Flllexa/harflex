package application

import (
	"errors"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestSettingsKeepMaxContextAndRefuseTooSmall(t *testing.T) {
	s, _, _ := setup(t)
	value := 100000
	saved, err := s.SaveSettings(SaveSettingsInput{MaxContextTokens: &value})
	if err != nil || saved.MaxContextTokens != 100000 {
		t.Fatalf("saved = %+v %v", saved, err)
	}
	// A save that does not mention it keeps it: other screens save the default provider alone.
	if again, err := s.SaveSettings(SaveSettingsInput{}); err != nil || again.MaxContextTokens != 100000 {
		t.Fatalf("an unrelated save reset the maximum context: %+v %v", again, err)
	}
	automatic := 0
	if read, err := s.SaveSettings(SaveSettingsInput{MaxContextTokens: &automatic}); err != nil || read.MaxContextTokens != 0 {
		t.Fatalf("automatic = %+v %v", read, err)
	}
	small := 4000
	if _, err := s.SaveSettings(SaveSettingsInput{MaxContextTokens: &small}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("a window below 8000 tokens was accepted: %v", err)
	}
	read, err := s.GetSettings()
	if err != nil || read.MaxContextTokens != 0 {
		t.Fatalf("read = %+v %v", read, err)
	}
}

// The maximum in Settings is the window of a model that does not say its own (the CLIs never do): 800 thousand set,
// 800 thousand used. A model that reports its window keeps the smaller of the two.
func TestTheMaximumContextIsTheWindowOfAModelThatDoesNotSayItsOwn(t *testing.T) {
	s, _, _ := setup(t)
	for _, c := range []struct {
		setting, model, want int
	}{
		{setting: 0, model: 0, want: 200_000},
		{setting: 800_000, model: 0, want: 800_000},
		{setting: 800_000, model: 128_000, want: 128_000},
		{setting: 300_000, model: 1_000_000, want: 300_000},
		{setting: 0, model: 1_000_000, want: 1_000_000},
	} {
		setting := c.setting
		if _, err := s.SaveSettings(SaveSettingsInput{MaxContextTokens: &setting}); err != nil {
			t.Fatal(err)
		}
		if got := s.effectiveContextWindow(t.Context(), catalog.ModelSelection{ContextLength: c.model}); got != c.want {
			t.Errorf("setting %d, model %d: window %d, want %d", c.setting, c.model, got, c.want)
		}
	}
}
