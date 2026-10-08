package modelcatalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCollectCompleteEmptyAndPagination(t *testing.T) {
	calls := 0
	got := Collect(t.Context(), func(_ context.Context, cursor string, remaining int64) (Page, error) {
		calls++
		switch calls {
		case 1:
			if cursor != "" || remaining != MaxBytes {
				t.Fatalf("first cursor=%q remaining=%d", cursor, remaining)
			}
			return Page{Models: []Model{{ID: "Vendor/Exact"}}, NextCursor: "two", Bytes: 100, Source: "provider", AccountFiltered: true}, nil
		case 2:
			if cursor != "two" || remaining != MaxBytes-100 {
				t.Fatalf("second cursor=%q remaining=%d", cursor, remaining)
			}
			return Page{Models: []Model{{ID: "vendor/exact"}}, Bytes: 100, Source: "provider", AccountFiltered: true}, nil
		default:
			t.Fatal("unexpected extra page")
			return Page{}, nil
		}
	})
	if calls != 2 || got.Status != StatusComplete || !got.Complete || got.ErrorCode != "" || got.NextCursor != "" ||
		len(got.Models) != 2 || got.Models[0].ID != "Vendor/Exact" || got.Models[1].ID != "vendor/exact" ||
		got.Source != "provider" || !got.AccountFiltered || got.CheckedAt.Location() != time.UTC {
		t.Fatalf("result=%+v calls=%d", got, calls)
	}

	empty := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
		return Page{Bytes: 2}, nil
	})
	if empty.Status != StatusEmpty || !empty.Complete || len(empty.Models) != 0 || empty.NextCursor != "" {
		t.Fatal(empty)
	}
}

func TestCollectRejectsAccountFilteringChangeAcrossPages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		first  bool
		second bool
	}{
		{"general to account", false, true},
		{"account to general", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
				calls++
				if calls == 1 {
					return Page{Models: []Model{{ID: "first"}}, NextCursor: "two", Bytes: 1, Source: "provider", AccountFiltered: tc.first}, nil
				}
				if calls == 2 {
					return Page{Models: []Model{{ID: "second"}}, Bytes: 1, Source: "provider", AccountFiltered: tc.second}, nil
				}
				t.Fatal("collector fetched beyond conflicting page")
				return Page{}, nil
			})
			if calls != 2 || got.Complete || got.Status != StatusPartial || got.ErrorCode != "catalog_unavailable" ||
				got.AccountFiltered != tc.first || len(got.Models) != 1 || got.Models[0].ID != "first" {
				t.Fatalf("result=%+v calls=%d", got, calls)
			}
		})
	}
}

func TestCollectNeverCallsRepeatedOrEmptyProgressCursor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		second Page
	}{
		{"repeated cursor", Page{Models: []Model{{ID: "a"}}, NextCursor: "again", Bytes: 2}},
		{"no new models", Page{Models: []Model{{ID: "a"}}, NextCursor: "later", Bytes: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
				calls++
				if calls == 1 {
					return Page{Models: []Model{{ID: "a"}}, NextCursor: "again", Bytes: 2}, nil
				}
				if calls == 2 {
					return tc.second, nil
				}
				t.Fatal("collector followed a cursor without progress")
				return Page{}, nil
			})
			if calls != 2 || got.Status != StatusPartial || got.Complete || len(got.Models) != 1 {
				t.Fatalf("result=%+v calls=%d", got, calls)
			}
		})
	}
}

func TestCollectStopsOnRepeatedCursorEvenWithNewModel(t *testing.T) {
	calls := 0
	got := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
		calls++
		if calls == 1 {
			return Page{Models: []Model{{ID: "a"}}, NextCursor: "again", Bytes: 1}, nil
		}
		if calls == 2 {
			return Page{Models: []Model{{ID: "b"}}, NextCursor: "again", Bytes: 1}, nil
		}
		t.Fatal("collector refetched a repeated cursor")
		return Page{}, nil
	})
	if calls != 2 || got.Status != StatusPartial || got.Complete || len(got.Models) != 2 {
		t.Fatalf("result=%+v calls=%d", got, calls)
	}
}

func TestCollectLimitsAndLateFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fetch    FetchPage
		maxCalls int
	}{
		{"entries", func(context.Context, string, int64) (Page, error) {
			models := make([]Model, MaxModels+1)
			for i := range models {
				models[i].ID = fmt.Sprintf("m-%d", i)
			}
			return Page{Models: models, Bytes: 100}, nil
		}, 1},
		{"bytes", func(context.Context, string, int64) (Page, error) {
			return Page{Models: []Model{{ID: "a"}}, Bytes: MaxBytes + 1}, nil
		}, 1},
		{"pages", func(context.Context, string, int64) (Page, error) {
			return Page{}, nil
		}, MaxPages},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			fetch := tc.fetch
			if tc.name == "pages" {
				fetch = func(_ context.Context, cursor string, _ int64) (Page, error) {
					return Page{Models: []Model{{ID: "m-" + cursor}}, NextCursor: cursor + "x", Bytes: 1}, nil
				}
			}
			got := Collect(t.Context(), func(ctx context.Context, cursor string, remaining int64) (Page, error) {
				calls++
				return fetch(ctx, cursor, remaining)
			})
			if got.Complete || got.Status != StatusPartial || got.ErrorCode != "catalog_limit" || len(got.Models) > MaxModels || calls != tc.maxCalls {
				t.Fatalf("result=%+v calls=%d", got, calls)
			}
			if tc.name == "pages" && len(got.Models) != MaxPages {
				t.Fatal(got)
			}
		})
	}

	calls := 0
	late := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
		calls++
		if calls == 1 {
			return Page{Models: []Model{{ID: "a"}}, NextCursor: "two", Bytes: 2}, nil
		}
		return Page{}, errors.New("private provider text")
	})
	if calls != 2 || late.Status != StatusPartial || late.ErrorCode != "catalog_unavailable" ||
		len(late.Models) != 1 || strings.Contains(fmt.Sprintf("%+v", late), "private provider text") {
		t.Fatalf("result=%+v calls=%d", late, calls)
	}
}

func TestCollectUnsupportedUnauthorizedAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status Status
		code   string
	}{
		{"unsupported", ErrUnsupported, StatusUnsupported, "catalog_unsupported"},
		{"unauthorized", ErrUnauthorized, StatusFailed, "catalog_unauthorized"},
		{"cancelled", context.Canceled, StatusInterrupted, "catalog_cancelled"},
		{"deadline", context.DeadlineExceeded, StatusInterrupted, "catalog_cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
				return Page{}, tc.err
			})
			if got.Status != tc.status || got.ErrorCode != tc.code || got.Complete || len(got.Models) != 0 {
				t.Fatal(got)
			}
		})
	}
}

func TestCollectRejectsOversizedModelLabel(t *testing.T) {
	got := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
		return Page{Models: []Model{{ID: "valid", DisplayName: strings.Repeat("x", 513)}}, Bytes: 600}, nil
	})
	if got.Status != StatusFailed || got.Complete || len(got.Models) != 0 || got.ErrorCode != "catalog_unavailable" {
		t.Fatal(got)
	}
}

func TestCollectRejectsInvalidDefaultReasoningEffort(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"over 64 bytes", strings.Repeat("x", 65)},
		{"control character", "high\nlow"},
		{"invalid utf8", "\xff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
				return Page{Models: []Model{{ID: "valid", DefaultReasoningEffort: tc.value}}, Bytes: 1}, nil
			})
			if got.Complete || got.Status != StatusFailed || got.ErrorCode != "catalog_unavailable" || len(got.Models) != 0 {
				t.Fatal(got)
			}
		})
	}
}

func TestCollectRequiresNonemptyUnpaddedSupportedReasoningEfforts(t *testing.T) {
	for _, effort := range []string{"", " ", "\u00a0", " high", "high ", "\u2003high"} {
		t.Run(fmt.Sprintf("invalid_%q", effort), func(t *testing.T) {
			got := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
				return Page{Models: []Model{{ID: "model", SupportedReasoningEfforts: []string{effort}}}, Bytes: 1}, nil
			})
			if got.Complete || got.Status != StatusFailed || got.ErrorCode != "catalog_unavailable" || len(got.Models) != 0 {
				t.Fatalf("invalid effort accepted as complete: %+v", got)
			}
		})
	}
	got := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
		return Page{Models: []Model{{ID: "model", SupportedReasoningEfforts: []string{"high", "low", "high"}}}, Bytes: 1}, nil
	})
	if !got.Complete || got.Status != StatusComplete || len(got.Models) != 1 || strings.Join(got.Models[0].SupportedReasoningEfforts, ",") != "high,low,high" {
		t.Fatal("valid values must be preserved without normalization or deduplication", got)
	}
}

func TestCollectRejectsInvalidModelFieldsAndSourceChange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model Model
	}{
		{"empty id", Model{}},
		{"invalid utf8 id", Model{ID: "\xff"}},
		{"control id", Model{ID: "a\nb"}},
		{"long id", Model{ID: strings.Repeat("a", 513)}},
		{"long owner", Model{ID: "a", OwnedBy: strings.Repeat("x", 257)}},
		{"too many efforts", Model{ID: "a", SupportedReasoningEfforts: make([]string, 33)}},
		{"long effort", Model{ID: "a", SupportedReasoningEfforts: []string{strings.Repeat("x", 65)}}},
		{"control effort", Model{ID: "a", SupportedReasoningEfforts: []string{"a\nb"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
				return Page{Models: []Model{tc.model}, Bytes: 1}, nil
			})
			if got.Status != StatusFailed || got.Complete || len(got.Models) != 0 {
				t.Fatal(got)
			}
		})
	}

	calls := 0
	changed := Collect(t.Context(), func(context.Context, string, int64) (Page, error) {
		calls++
		if calls == 1 {
			return Page{Models: []Model{{ID: "a"}}, NextCursor: "two", Bytes: 1}, nil
		}
		return Page{Models: []Model{{ID: "b"}}, Bytes: 1, Source: "changed"}, nil
	})
	if calls != 2 || changed.Status != StatusPartial || changed.Complete || len(changed.Models) != 1 || changed.Source != "" {
		t.Fatalf("result=%+v calls=%d", changed, calls)
	}
}

type scheduledDeadline struct {
	at     time.Duration
	cancel context.CancelFunc
}

type fakeDeadlineClock struct {
	now       time.Duration
	durations []time.Duration
	pending   []scheduledDeadline
}

func (c *fakeDeadlineClock) WithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	c.durations = append(c.durations, d)
	c.pending = append(c.pending, scheduledDeadline{at: c.now + d, cancel: cancel})
	return ctx, cancel
}

func (c *fakeDeadlineClock) Advance(d time.Duration) {
	c.now += d
	for _, timer := range c.pending {
		if timer.at <= c.now {
			timer.cancel()
		}
	}
}

func TestCollectPageDeadlineAtTenSecondsWithoutSleeping(t *testing.T) {
	clock := &fakeDeadlineClock{}
	calls := 0
	got := collectWithDeadline(t.Context(), func(ctx context.Context, _ string, _ int64) (Page, error) {
		calls++
		clock.Advance(10 * time.Second)
		if ctx.Err() == nil {
			t.Fatal("page context survived its ten-second deadline")
		}
		// The fetcher ignores cancellation; the collector still must reject a late page.
		return Page{Models: []Model{{ID: "late"}}, Bytes: 1}, nil
	}, clock.WithTimeout)
	if calls != 1 || got.Status != StatusInterrupted || got.Complete || len(got.Models) != 0 ||
		len(clock.durations) != 2 || clock.durations[0] != 30*time.Second || clock.durations[1] != 10*time.Second {
		t.Fatal(got, calls, clock.durations)
	}
}

func TestCollectTotalDeadlineAtThirtySecondsWithoutSleeping(t *testing.T) {
	clock := &fakeDeadlineClock{}
	calls := 0
	got := collectWithDeadline(t.Context(), func(ctx context.Context, _ string, _ int64) (Page, error) {
		calls++
		if calls < 4 {
			clock.Advance(9 * time.Second)
		} else {
			clock.Advance(3 * time.Second)
		}
		if ctx.Err() != nil {
			return Page{}, ctx.Err()
		}
		return Page{Models: []Model{{ID: fmt.Sprintf("m-%d", calls)}}, NextCursor: fmt.Sprintf("page-%d", calls), Bytes: 1}, nil
	}, clock.WithTimeout)
	if calls != 4 || clock.now != 30*time.Second || got.Status != StatusInterrupted || got.Complete ||
		len(got.Models) != 3 || len(clock.durations) != 5 {
		t.Fatal(got, calls, clock.now, clock.durations)
	}
	if clock.durations[0] != 30*time.Second {
		t.Fatal(clock.durations)
	}
	for _, d := range clock.durations[1:] {
		if d != 10*time.Second {
			t.Fatal(clock.durations)
		}
	}
}
