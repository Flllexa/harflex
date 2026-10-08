package knowledge

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunkTextPreservesUnicodeAndLineProvenance(t *testing.T) {
	source := strings.Repeat("á", 1199) + "\n" + strings.Repeat("bússola", 200)
	chunks := ChunkText(source)
	if len(chunks) < 2 {
		t.Fatalf("expected bounded chunks, got %d", len(chunks))
	}
	var combined strings.Builder
	for i, chunk := range chunks {
		if chunk.Ordinal != i || chunk.LineStart < 1 || !utf8.ValidString(chunk.Content) || utf8.RuneCountInString(chunk.Content) > MaxChunkRunes {
			t.Fatalf("invalid chunk %+v", chunk)
		}
		combined.WriteString(chunk.Content)
	}
	if combined.String() != source || chunks[1].LineStart != 2 {
		t.Fatalf("chunking lost text or line: %+v", chunks)
	}
}
