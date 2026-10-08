package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

var ErrProfileChanged = errors.New("embedding profile changed")
var ErrVectorLimit = errors.New("local vector search limit exceeded")
var ErrVectorSourceChanged = errors.New("knowledge source changed during vector indexing")

type VectorChunk struct {
	DocumentID  string
	Ordinal     int
	Content     string
	ContentHash string
}

type VectorWrite struct {
	DocumentID  string
	Ordinal     int
	ContentHash string
	Values      []float64
}

type VectorProgress struct {
	Total     int `json:"total"`
	Indexed   int `json:"indexed"`
	Dimension int `json:"dimension"`
}

func HashChunk(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
