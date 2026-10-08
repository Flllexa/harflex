package application

import (
	"context"
	"errors"
	"testing"

	"github.com/persioflexa/harflex/internal/events"
)

type limitedEventStore struct {
	Store
	calls  int
	stream string
	after  int64
	limit  int
}

func (s *limitedEventStore) ListAfter(context.Context, string, int64) ([]events.Event, error) {
	return nil, errors.New("unbounded event read forbidden")
}
func (s *limitedEventStore) ListAfterLimit(_ context.Context, stream string, after int64, limit int) ([]events.Event, error) {
	s.calls++
	s.stream = stream
	s.after = after
	s.limit = limit
	return []events.Event{}, nil
}
func TestListEventsUsesOnlyBoundedStoreRead(t *testing.T) {
	s, db, _, session := sessionSetup(t, &fakeProvider{})
	spy := &limitedEventStore{Store: db}
	s.store = spy
	for _, limit := range []int{0, 1, 10, 1000} {
		_, err := s.ListEvents(ListEventsInput{SessionID: session.ID, AfterSequence: 12, Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		want := limit
		if want == 0 {
			want = 200
		}
		if spy.stream != session.ID || spy.after != 12 || spy.limit != want {
			t.Fatal("incorrect bounded read")
		}
	}
	if spy.calls != 4 {
		t.Fatal("bounded read not called")
	}
}
