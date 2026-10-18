package application

import (
	"errors"
	"testing"
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
