package events

import (
	"encoding/json"
	"errors"
)

var ErrEventBudgetExceeded = errors.New("journal event budget exceeded")
var ErrStreamBudgetExceeded = errors.New("journal stream budget exceeded")

// Two 16 MiB tool fields plus 2 MiB for JSON envelope, correlation metadata and
// encoding overhead. The serialized limit is enforced by both writer and reader;
// escaping can grow otherwise-valid fields and must still fit this envelope.
const MaxDataBytes = (2*16 + 2) << 20
const MaxStreamDataBytes = 64 << 20

// ValidateDataSize counts encoded bytes without keeping an additional payload
// copy. json.Encoder emits one trailing newline, which is not journal data.
func ValidateDataSize(data any) error {
	sink := &dataSizeSink{}
	return json.NewEncoder(sink).Encode(data)
}

type dataSizeSink struct{ size int }

func (s *dataSizeSink) Write(data []byte) (int, error) {
	if len(data) > MaxDataBytes+1-s.size {
		return 0, ErrEventBudgetExceeded
	}
	s.size += len(data)
	return len(data), nil
}

// RecoveryRequest binds a repair to the complete journal snapshot inspected by
// the caller. A nonzero InterruptedSequence repairs an existing interruption.
type RecoveryRequest struct {
	StartSequence       int64
	ObservedSequence    int64
	InterruptedSequence int64
	Closures            []ToolClosure
}
