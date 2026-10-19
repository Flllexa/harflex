package application

import (
	"context"
	"errors"
	"strings"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
)

// defaultContextWindow is assumed when the model's catalog does not say how large its window is (the CLIs never do).
const defaultContextWindow = 200_000

// contextKeeper is a conversation that keeps itself within a window and can compact on demand.
type contextKeeper interface {
	SetContextWindow(tokens int)
	Compact(ctx context.Context) error
}

// effectiveContextWindow is the window a conversation keeps within. A model that reports its window keeps the smaller of
// it and the maximum set in Settings; one that does not (the CLIs) takes that maximum as its window, or 200 thousand.
func (s *Service) effectiveContextWindow(ctx context.Context, selection catalog.ModelSelection) int {
	maximum := 0
	if settings, err := s.store.GetSettings(ctx); err == nil {
		maximum = settings.MaxContextTokens
	}
	window := selection.ContextLength
	switch {
	case window <= 0 && maximum > 0:
		return maximum
	case window <= 0:
		return defaultContextWindow
	case maximum > 0 && maximum < window:
		return maximum
	}
	return window
}

// sessionContextWindow is the window of a stored conversation, from its saved model selection.
func (s *Service) sessionContextWindow(ctx context.Context, sessionID string) int {
	selection, _ := s.store.GetSessionModelSelection(ctx, sessionID)
	return s.effectiveContextWindow(ctx, selection)
}

// keepWithinWindow sets a runner's window before each turn, so a change in Settings applies from the next message.
func (s *Service) keepWithinWindow(ctx context.Context, base sessionRunner, selection catalog.ModelSelection) {
	if keeper, ok := base.(contextKeeper); ok {
		keeper.SetContextWindow(s.effectiveContextWindow(ctx, selection))
	}
}

// CompactSession summarizes a conversation now, between its runs, as the person asked from the context gauge.
func (s *Service) CompactSession(in CompactSessionInput) (SessionDTO, error) {
	if err := s.beginCall(); err != nil {
		return SessionDTO{}, err
	}
	defer s.endCall()
	if strings.TrimSpace(in.SessionID) == "" {
		return SessionDTO{}, ErrInvalidInput
	}
	s.mu.RLock()
	runner := s.sessions[in.SessionID]
	s.mu.RUnlock()
	if runner == nil {
		return SessionDTO{}, ErrSessionNotFound
	}
	compacter, ok := runner.(interface{ Compact(context.Context) error })
	if !ok {
		return SessionDTO{}, ErrInvalidInput
	}
	if err := compacter.Compact(s.ctx); err != nil {
		if errors.Is(err, agentcore.ErrSessionBusy) {
			return SessionDTO{}, err
		}
		return SessionDTO{}, safe("compact session", err)
	}
	record, err := s.store.GetSession(s.ctx, in.SessionID)
	if err != nil {
		return SessionDTO{}, safe("read session", err)
	}
	return s.sessionDTO(record), nil
}
