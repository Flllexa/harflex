package application

import (
	"errors"
	"strings"
	"testing"
)

func TestAuthoringCodePatchPageUsesBoundedUTF8ByteCursors(t *testing.T) {
	content := `{"patch":"` + strings.Repeat("a", 11) + "ç" + strings.Repeat("b", 9) + `"}`
	encoded := []byte(content)
	var rebuilt strings.Builder
	var cursor int64
	for {
		page, err := authoringCodePatchPage(encoded, cursor, 12)
		if err != nil {
			t.Fatalf("authoringCodePatchPage(cursor=%d): %v", cursor, err)
		}
		if len([]byte(page.Content)) > 12 {
			t.Fatalf("page exceeded byte limit: %d", len([]byte(page.Content)))
		}
		rebuilt.WriteString(page.Content)
		if page.NextCursor == nil {
			break
		}
		if *page.NextCursor <= cursor {
			t.Fatalf("cursor did not advance: current=%d next=%d", cursor, *page.NextCursor)
		}
		cursor = *page.NextCursor
	}
	if rebuilt.String() != content {
		t.Fatalf("reconstructed patch differs: got %q want %q", rebuilt.String(), content)
	}
	if _, err := authoringCodePatchPage(encoded, 22, 12); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cursor in middle of a UTF-8 rune error = %v; want ErrInvalidInput", err)
	}
	if _, err := authoringCodePatchPage(encoded, int64(len(encoded)), 12); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cursor at end of patch error = %v; want ErrInvalidInput", err)
	}
}
