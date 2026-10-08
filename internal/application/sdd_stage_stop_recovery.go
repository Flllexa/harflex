package application

import (
	"context"
	"encoding/json"

	"github.com/persioflexa/harflex/internal/events"
)

// A real terminal journal event can confirm a stopped runner. Synthetic recovery
// interruptions and elapsed time are not proof that an old runner has joined.
func (s *Service) recoverAuthoringStops(ctx context.Context) (map[string]bool, error) {
	stops, err := s.store.ListPendingAuthoringStops(ctx)
	if err != nil {
		return nil, safe("read authoring stops", err)
	}
	blocked := make(map[string]bool)
	for _, stop := range stops {
		var last string
		err := s.walkEvents(ctx, stop.SessionID, func(event events.Event) error {
			if !json.Valid(event.Data) {
				return ErrSessionCorrupt
			}
			last = event.Type
			return nil
		})
		if err != nil || last != "run.completed" && last != "run.failed" && last != "run.cancelled" {
			blocked[stop.SessionID] = true
			continue
		}
		if _, err := s.store.ConfirmAuthoringStageStop(ctx, stop.PipelineID, stop.Stage, stop.AttemptID, "terminal_journal"); err != nil {
			return blocked, safe("confirm recovered authoring stop", err)
		}
	}
	return blocked, nil
}
