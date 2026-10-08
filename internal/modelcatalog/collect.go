package modelcatalog

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func safeText(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func safeID(value string) bool {
	return value != "" && safeText(value, 512)
}

func incomplete(result Result, err error) Result {
	result.Complete = false
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		result.Status, result.ErrorCode = StatusInterrupted, "catalog_cancelled"
	case errors.Is(err, ErrUnsupported):
		result.Status, result.ErrorCode = StatusUnsupported, "catalog_unsupported"
		if len(result.Models) > 0 {
			result.Status = StatusPartial
		}
	case errors.Is(err, ErrUnauthorized):
		result.Status, result.ErrorCode = StatusFailed, "catalog_unauthorized"
		if len(result.Models) > 0 {
			result.Status = StatusPartial
		}
	case errors.Is(err, ErrLimit):
		result.Status, result.ErrorCode = StatusPartial, "catalog_limit"
	default:
		result.Status, result.ErrorCode = StatusFailed, "catalog_unavailable"
		if len(result.Models) > 0 {
			result.Status = StatusPartial
		}
	}
	return result
}

type deadlineFactory func(context.Context, time.Duration) (context.Context, context.CancelFunc)

func Collect(parent context.Context, fetch FetchPage) Result {
	return collectWithDeadline(parent, fetch, context.WithTimeout)
}

func collectWithDeadline(parent context.Context, fetch FetchPage, withDeadline deadlineFactory) Result {
	result := Result{Models: []Model{}, CheckedAt: time.Now().UTC()}
	ctx, cancel := withDeadline(parent, 30*time.Second)
	defer cancel()

	cursor := ""
	seenCursors := make(map[string]bool, MaxPages)
	seenModels := make(map[string]bool, MaxModels)
	remaining := MaxBytes
	sourceSeen := false

	for pageNumber := 0; pageNumber < MaxPages; pageNumber++ {
		pageCtx, stopPage := withDeadline(ctx, 10*time.Second)
		page, err := fetch(pageCtx, cursor, remaining)
		deadlineErr := pageCtx.Err()
		stopPage()
		if err == nil && deadlineErr != nil {
			err = deadlineErr
		}
		if err != nil {
			result.NextCursor = cursor
			return incomplete(result, err)
		}
		if page.Bytes < 0 || page.Bytes > remaining {
			result.NextCursor = cursor
			return incomplete(result, ErrLimit)
		}
		remaining -= page.Bytes
		if sourceSeen && (result.Source != page.Source || result.AccountFiltered != page.AccountFiltered) {
			result.NextCursor = cursor
			return incomplete(result, ErrUnavailable)
		}
		sourceSeen = true
		result.Source, result.AccountFiltered = page.Source, page.AccountFiltered

		added := 0
		for _, item := range page.Models {
			if !safeID(item.ID) || !safeText(item.DisplayName, 512) || !safeText(item.OwnedBy, 256) ||
				!safeText(item.DefaultReasoningEffort, 64) ||
				len(item.SupportedReasoningEfforts) > 32 {
				result.NextCursor = cursor
				return incomplete(result, ErrUnavailable)
			}
			for _, effort := range item.SupportedReasoningEfforts {
				if effort == "" || strings.TrimSpace(effort) != effort || !safeText(effort, 64) {
					result.NextCursor = cursor
					return incomplete(result, ErrUnavailable)
				}
			}
			if seenModels[item.ID] {
				continue
			}
			if len(result.Models) == MaxModels {
				result.NextCursor = cursor
				return incomplete(result, ErrLimit)
			}
			seenModels[item.ID] = true
			result.Models = append(result.Models, item)
			added++
		}
		if page.NextCursor == "" {
			result.NextCursor = ""
			result.Status, result.Complete = StatusComplete, true
			if len(result.Models) == 0 {
				result.Status = StatusEmpty
			}
			return result
		}
		result.NextCursor = page.NextCursor
		if page.NextCursor == cursor || seenCursors[page.NextCursor] || added == 0 {
			return incomplete(result, ErrLimit)
		}
		seenCursors[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return incomplete(result, ErrLimit)
}
