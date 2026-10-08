package knowledge

import "strings"

const MaxChunkRunes = 1200
const MaxDocumentBytes = 2 * 1024 * 1024

type Chunk struct {
	Ordinal   int
	LineStart int
	Content   string
}

// ChunkText retains every rune so search excerpts can be traced to the source line.
func ChunkText(content string) []Chunk {
	if content == "" {
		return nil
	}
	chunks := make([]Chunk, 0, len(content)/MaxChunkRunes+1)
	var text strings.Builder
	line, startLine, count := 1, 1, 0
	flush := func() {
		if count == 0 {
			return
		}
		chunks = append(chunks, Chunk{Ordinal: len(chunks), LineStart: startLine, Content: text.String()})
		text.Reset()
		count = 0
		startLine = line
	}
	for _, character := range content {
		text.WriteRune(character)
		count++
		if character == '\n' {
			line++
		}
		if count == MaxChunkRunes {
			flush()
		}
	}
	flush()
	return chunks
}
