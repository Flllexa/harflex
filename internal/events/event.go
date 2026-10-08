package events

import (
	"encoding/json"
	"time"
)

type Event struct {
	ID        string          `json:"id"`
	StreamID  string          `json:"streamId"`
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"createdAt"`
}
