package automation

import (
	"errors"
	"time"
	_ "time/tzdata"
)

var ErrInvalidRule = errors.New("invalid schedule rule")

// Rule stores wall-clock intent separately from the UTC instant so daylight
// saving changes do not drift recurring schedules.
type Rule struct {
	Frequency string
	Timezone  string
	LocalDate string
	LocalTime string
}

// NextAfter returns the first valid wall-clock occurrence strictly after after.
// A nonexistent daily wall clock is skipped; an invalid one-time clock is an error.
func NextAfter(rule Rule, after time.Time) (time.Time, error) {
	if len(rule.Timezone) == 0 || len(rule.Timezone) > 128 {
		return time.Time{}, ErrInvalidRule
	}
	location, err := time.LoadLocation(rule.Timezone)
	if err != nil {
		return time.Time{}, ErrInvalidRule
	}
	clock, err := time.Parse("15:04", rule.LocalTime)
	if err != nil || clock.Format("15:04") != rule.LocalTime {
		return time.Time{}, ErrInvalidRule
	}
	hour, minute := clock.Hour(), clock.Minute()
	localAfter := after.In(location)
	if rule.Frequency == "once" {
		date, err := time.Parse("2006-01-02", rule.LocalDate)
		if err != nil || date.Format("2006-01-02") != rule.LocalDate {
			return time.Time{}, ErrInvalidRule
		}
		candidate := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, location)
		wallClock := rule.LocalDate + "T" + rule.LocalTime
		if candidate.In(location).Format("2006-01-02T15:04") != wallClock {
			return time.Time{}, ErrInvalidRule
		}
		for delta := -180; delta <= 180; delta += 15 {
			if delta != 0 && candidate.Add(time.Duration(delta)*time.Minute).In(location).Format("2006-01-02T15:04") == wallClock {
				return time.Time{}, ErrInvalidRule
			}
		}
		if candidate.After(after) {
			return candidate.UTC(), nil
		}
		return time.Time{}, nil
	}
	if rule.Frequency != "daily" || rule.LocalDate != "" {
		return time.Time{}, ErrInvalidRule
	}
	for day := 0; day <= 370; day++ {
		date := time.Date(localAfter.Year(), localAfter.Month(), localAfter.Day()+day, 12, 0, 0, 0, location)
		candidate := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, location)
		if candidate.In(location).Format("2006-01-02T15:04") != date.Format("2006-01-02")+"T"+rule.LocalTime {
			continue
		}
		if candidate.After(after) {
			return candidate.UTC(), nil
		}
	}
	return time.Time{}, ErrInvalidRule
}
