package application

import "strings"

func (s *Service) ListLogEvents(in ListLogEventsInput) ([]LogEntryDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if in.BeforeID < 0 || in.Limit < 0 || in.Limit > 200 || len(in.Type) > 128 || len(in.WorkspaceID) > 128 || strings.TrimSpace(in.Type) != in.Type || strings.TrimSpace(in.WorkspaceID) != in.WorkspaceID {
		return nil, ErrInvalidInput
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	items, err := s.store.ListLogEvents(s.ctx, in.WorkspaceID, in.Type, in.BeforeID, limit)
	if err != nil {
		return nil, safe("list log events", err)
	}
	result := make([]LogEntryDTO, 0, len(items))
	for _, item := range items {
		result = append(result, LogEntryDTO{
			Cursor: item.Cursor, ID: item.ID, SessionID: item.SessionID,
			WorkspaceID: item.WorkspaceID, Sequence: item.Sequence, Type: item.Type,
			CreatedAt: item.CreatedAt,
		})
	}
	return result, nil
}
