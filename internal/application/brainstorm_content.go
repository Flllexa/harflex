package application

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"

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

// ErrBrainstormDocumentInvalid says a Brainstorm the person wrote is not a valid flow and design, or a screen breaks the
// rules that keep it inert.
var ErrBrainstormDocumentInvalid = errors.New("the Brainstorm is not a valid flow and design")

// validateBrainstormDocument checks a Brainstorm written by hand by the rules the AI's answer meets, and returns it in
// canonical form.
func validateBrainstormDocument(content string) (string, error) {
	brainstorm, _, err := parseBrainstormOutput(`{"brainstorm":` + content + `,"reply":"."}`)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrBrainstormDocumentInvalid, strings.TrimPrefix(err.Error(), errInvalidSDDOutput.Error()+": "))
	}
	return brainstorm.Canonical(), nil
}

// Canonical is the stable JSON stored as the document's content. The HTML stays as written (no \u003c escapes), so the
// document reads and edits as it is and its size is the size of what it holds.
func (c BrainstormContent) Canonical() string {
	return marshalReadable(c)
}

// marshalReadable is compact JSON that keeps <, > and & as they are.
func marshalReadable(value any) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return ""
	}
	return strings.TrimSuffix(out.String(), "\n")
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

// The tags a proposed screen may not contain: they run code, embed other documents or change where the page loads from.
var brainstormForbiddenTags = map[string]bool{"script": true, "iframe": true, "frame": true, "frameset": true, "object": true, "embed": true, "applet": true, "base": true, "portal": true, "noscript": true}

// The attributes whose value is a URL the browser would fetch or follow.
var brainstormURLAttributes = map[string]bool{"href": true, "src": true, "action": true, "srcset": true, "poster": true, "formaction": true, "background": true, "xlink:href": true, "data": true, "cite": true, "ping": true, "manifest": true, "codebase": true, "lowsrc": true, "dynsrc": true, "longdesc": true, "imagesrcset": true}

var errBrainstormExternal = errors.New("load nothing from outside (fonts, images, styles); embed images as data: and use system fonts")

// validateBrainstormHTML keeps a proposed screen self-contained and inert: no scripts, no handlers and nothing loaded
// from outside. It reads the markup as a browser would (entities decoded, tags and attributes however they are
// written), so an encoded or unusual spelling cannot slip through. The screen is also shown in a sandboxed frame; this
// check makes the stored document itself safe to keep and to hand to the Coder.
func validateBrainstormHTML(source string) error {
	tokens := html.NewTokenizer(strings.NewReader(source))
	inStyle := false
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			if errors.Is(tokens.Err(), io.EOF) {
				return nil
			}
			return fmt.Errorf("the HTML could not be read: %v", tokens.Err())
		case html.TextToken:
			if inStyle {
				if err := validateBrainstormCSS(string(tokens.Text())); err != nil {
					return err
				}
			}
		case html.EndTagToken:
			if name, _ := tokens.TagName(); string(name) == "style" {
				inStyle = false
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokens.Token()
			name := strings.ToLower(token.Data)
			if brainstormForbiddenTags[name] {
				return fmt.Errorf("remove scripts, frames, objects and embeds; the screen must be static HTML and CSS")
			}
			inStyle = name == "style"
			for _, attribute := range token.Attr {
				key := strings.ToLower(attribute.Key)
				if attribute.Namespace != "" {
					key = strings.ToLower(attribute.Namespace) + ":" + key
				}
				switch {
				case strings.HasPrefix(key, "on"):
					return fmt.Errorf("remove event attributes such as onclick; the screen must not run code")
				case key == "srcdoc":
					return fmt.Errorf("remove scripts, frames, objects and embeds; the screen must be static HTML and CSS")
				case name == "meta" && key == "http-equiv":
					return fmt.Errorf("remove <meta http-equiv>; the screen must not redirect or change its own rules")
				case key == "style":
					if err := validateBrainstormCSS(attribute.Val); err != nil {
						return err
					}
				case brainstormURLAttributes[key]:
					if err := validateBrainstormURLs(key, attribute.Val); err != nil {
						return err
					}
				}
			}
		}
	}
}

// validateBrainstormURLs accepts only references that stay inside the page: fragments, relative paths (they resolve to
// nothing in the frame) and data: URLs.
func validateBrainstormURLs(attribute, value string) error {
	candidates := []string{value}
	if attribute == "srcset" || attribute == "imagesrcset" {
		candidates = nil
		for _, candidate := range strings.Split(value, ",") {
			if fields := strings.Fields(candidate); len(fields) > 0 {
				candidates = append(candidates, fields[0])
			}
		}
	}
	for _, candidate := range candidates {
		if err := validateBrainstormURL(candidate); err != nil {
			return err
		}
	}
	return nil
}

func validateBrainstormURL(value string) error {
	// Browsers drop whitespace and control characters inside a URL's scheme, so "java\tscript:" is javascript:.
	compact := strings.ToLower(strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f {
			return -1
		}
		return r
	}, value))
	if strings.HasPrefix(compact, "//") || strings.HasPrefix(compact, `\\`) || strings.HasPrefix(compact, `/\`) {
		return errBrainstormExternal
	}
	if scheme := brainstormScheme.FindString(compact); scheme != "" && scheme != "data:" {
		if scheme == "javascript:" || scheme == "vbscript:" {
			return fmt.Errorf("remove javascript: links")
		}
		return errBrainstormExternal
	}
	return nil
}

var brainstormScheme = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`)

// validateBrainstormCSS refuses CSS that imports or fetches anything that is not data:, after undoing the escapes and
// comments CSS allows ("\68ttps", "@\69mport", "u/**/rl").
func validateBrainstormCSS(css string) error {
	decoded := strings.ToLower(brainstormCSSComment.ReplaceAllString(decodeCSSEscapes(css), ""))
	switch {
	case strings.Contains(decoded, "@import"), strings.Contains(decoded, "://"):
		return errBrainstormExternal
	case strings.Contains(decoded, "expression("), strings.Contains(decoded, "-moz-binding"), strings.Contains(decoded, "behavior:"):
		return fmt.Errorf("remove scripts, frames, objects and embeds; the screen must be static HTML and CSS")
	}
	for _, match := range brainstormCSSURL.FindAllStringSubmatch(decoded, -1) {
		if err := validateBrainstormURL(strings.Trim(strings.TrimSpace(match[1]), `"'`)); err != nil {
			return err
		}
	}
	return nil
}

var (
	brainstormCSSComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	brainstormCSSURL     = regexp.MustCompile(`url\(([^)]*)\)`)
)

// decodeCSSEscapes turns CSS escapes into the characters they stand for: a backslash and up to six hex digits (with one
// optional space after them), or a backslash and any other character.
func decodeCSSEscapes(css string) string {
	var out strings.Builder
	for i := 0; i < len(css); i++ {
		if css[i] != '\\' || i+1 >= len(css) {
			out.WriteByte(css[i])
			continue
		}
		j := i + 1
		for j < len(css) && j-i <= 6 && strings.IndexByte("0123456789abcdefABCDEF", css[j]) >= 0 {
			j++
		}
		if j == i+1 {
			out.WriteByte(css[j])
			i = j
			continue
		}
		if value, err := strconv.ParseUint(css[i+1:j], 16, 32); err == nil && utf8.ValidRune(rune(value)) {
			out.WriteRune(rune(value))
		}
		if j < len(css) && (css[j] == ' ' || css[j] == '\t' || css[j] == '\n') {
			j++
		}
		i = j - 1
	}
	return out.String()
}
