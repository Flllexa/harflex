package automation

import (
	"testing"
	"time"
)

func TestNextAfterUsesChosenTimezoneAndSkipsInvalidWallClock(t *testing.T) {
	zone := "America/New_York"
	beforeDST := time.Date(2026, 3, 7, 7, 0, 0, 0, time.UTC)
	next, err := NextAfter(Rule{Frequency: "daily", Timezone: zone, LocalTime: "02:30"}, beforeDST)
	if err != nil || !next.Equal(time.Date(2026, 3, 7, 7, 30, 0, 0, time.UTC)) {
		t.Fatalf("first local run: %s %v", next, err)
	}
	next, err = NextAfter(Rule{Frequency: "daily", Timezone: zone, LocalTime: "02:30"}, next)
	if err != nil || !next.Equal(time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC)) {
		t.Fatalf("nonexistent local time should skip DST day: %s %v", next, err)
	}
}

func TestNextAfterOnceIsStrictAndRejectsInvalidLocalTime(t *testing.T) {
	rule := Rule{Frequency: "once", Timezone: "America/Sao_Paulo", LocalDate: "2026-09-27", LocalTime: "09:15"}
	want := time.Date(2026, 9, 27, 12, 15, 0, 0, time.UTC)
	got, err := NextAfter(rule, want.Add(-time.Second))
	if err != nil || !got.Equal(want) {
		t.Fatalf("once in Sao Paulo: %s %v", got, err)
	}
	if next, err := NextAfter(rule, want); err != nil || !next.IsZero() {
		t.Fatalf("once should not repeat: %s %v", next, err)
	}
	if _, err := NextAfter(Rule{Frequency: "once", Timezone: "America/New_York", LocalDate: "2026-03-08", LocalTime: "02:30"}, want); err == nil {
		t.Fatal("nonexistent wall clock must be rejected")
	}
	if _, err := NextAfter(Rule{Frequency: "once", Timezone: "America/New_York", LocalDate: "2026-11-01", LocalTime: "01:30"}, want); err == nil {
		t.Fatal("ambiguous one-time wall clock must be rejected")
	}
}
