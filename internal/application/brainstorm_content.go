package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
)

// MaxBrainstormDocumentBytes bounds the Brainstorm document: the flow plus every screen's HTML.
const MaxBrainstormDocumentBytes = catalog.MaxBrainstormDocumentBytes

const maxBrainstormScreens = 12

// BrainstormScreen is one proposed screen: a complete, self-contained HTML page.
type BrainstormScreen struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	HTML    string `json:"html"`
}

type BrainstormDesign struct {
	Screens []BrainstormScreen `json:"screens"`
}

// BrainstormContent is what the Brainstorm stage holds: the application flow in Mermaid, always, and the design of the
// screens when the solution has any. Without screens, NoDesignReason says why in one sentence.
type BrainstormContent struct {
	Flow           string            `json:"flow"`
	Design         *BrainstormDesign `json:"design"`
	NoDesignReason string            `json:"noDesignReason"`
	Notes          string            `json:"notes"`
}

// Canonical is the stable JSON stored as the document's content.
func (c BrainstormContent) Canonical() string {
	encoded, _ := json.Marshal(c)
	return string(encoded)
}

// parseBrainstormOutput reads the Brainstorm phase's answer: {"brainstorm": BrainstormContent, "reply": string}. A refusal
// wraps errInvalidSDDOutput with a reason the AI can act on when it is asked to answer again.
func parseBrainstormOutput(raw string) (BrainstormContent, string, error) {
	refuse := func(format string, args ...any) (BrainstormContent, string, error) {
		return BrainstormContent{}, "", fmt.Errorf("%w: %s", errInvalidSDDOutput, fmt.Sprintf(format, args...))
	}
	if len(raw) > MaxBrainstormDocumentBytes+8192 || !utf8.ValidString(raw) {
		return refuse("the answer is larger than %d KB or not UTF-8", MaxBrainstormDocumentBytes/1024)
	}
	var answer struct {
		Brainstorm *BrainstormContent `json:"brainstorm"`
		Reply      *string            `json:"reply"`
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(strings.TrimSpace(raw))))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&answer); err != nil {
		return refuse("the answer is not the expected JSON (%v); send the whole object again, complete", err)
	}
	if decoder.More() {
		return refuse("the answer has text after the JSON object")
	}
	if answer.Brainstorm == nil || answer.Reply == nil {
		return refuse("the answer needs both brainstorm and reply")
	}
	content := *answer.Brainstorm
	if strings.TrimSpace(content.Flow) == "" {
		return refuse("flow is empty; the Mermaid flow is always required")
	}
	if content.Design == nil || len(content.Design.Screens) == 0 {
		if strings.TrimSpace(content.NoDesignReason) == "" {
			return refuse("without screens, noDesignReason must say why in one sentence")
		}
		content.Design = nil
	} else {
		if len(content.Design.Screens) > maxBrainstormScreens {
			return refuse("there are %d screens; propose at most %d", len(content.Design.Screens), maxBrainstormScreens)
		}
		for index, screen := range content.Design.Screens {
			if strings.TrimSpace(screen.Name) == "" || strings.TrimSpace(screen.HTML) == "" {
				return refuse("screen %d needs a name and its HTML", index+1)
			}
			if err := validateBrainstormHTML(screen.HTML); err != nil {
				return refuse("screen %q: %v", screen.Name, err)
			}
		}
	}
	if len(content.Canonical()) > MaxBrainstormDocumentBytes {
		return refuse("the document is larger than %d KB; simplify the screens", MaxBrainstormDocumentBytes/1024)
	}
	return content, strings.TrimSpace(*answer.Reply), nil
}

var (
	brainstormForbiddenTag   = regexp.MustCompile(`<\s*(script|iframe|object|embed|frame|frameset|applet|base|meta\s+http-equiv)\b`)
	brainstormEventHandler   = regexp.MustCompile(`\son[a-z]+\s*=`)
	brainstormJavaScriptURL  = regexp.MustCompile(`javascript\s*:`)
	brainstormExternalAttr   = regexp.MustCompile(`\b(href|src|action|srcset|poster|formaction)\s*=\s*["']?\s*(https?:|//)`)
	brainstormExternalCSSURL = regexp.MustCompile(`url\(\s*["']?\s*(https?:|//)`)
	brainstormCSSImport      = regexp.MustCompile(`@import`)
)

// validateBrainstormHTML keeps a proposed screen self-contained and inert: no scripts, no handlers and nothing loaded
// from outside. It is shown in a sandboxed frame too; this check makes the stored document itself safe to keep.
func validateBrainstormHTML(html string) error {
	lower := strings.ToLower(html)
	switch {
	case brainstormForbiddenTag.MatchString(lower):
		return fmt.Errorf("remove scripts, frames, objects and embeds; the screen must be static HTML and CSS")
	case brainstormEventHandler.MatchString(lower):
		return fmt.Errorf("remove event attributes such as onclick; the screen must not run code")
	case brainstormJavaScriptURL.MatchString(lower):
		return fmt.Errorf("remove javascript: links")
	case brainstormExternalAttr.MatchString(lower), brainstormExternalCSSURL.MatchString(lower), brainstormCSSImport.MatchString(lower):
		return fmt.Errorf("load nothing from outside (fonts, images, styles); embed images as data: and use system fonts")
	}
	return nil
}
