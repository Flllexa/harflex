package application

import (
	"errors"
	"strings"
	"testing"
)

const brainstormPage = `<!doctype html><html><head><style>body{font-family:system-ui}</style></head><body><h1>Contratos</h1></body></html>`

func TestParseBrainstormOutput(t *testing.T) {
	screen := `{"name":"Lista de contratos","purpose":"Ver o que vence","html":` + quoteJSON(brainstormPage) + `}`
	for _, tc := range []struct {
		name, raw string
		ok        bool
		screens   int
	}{
		{"with design", `{"brainstorm":{"flow":"flowchart TD\n A-->B","design":{"screens":[` + screen + `]},"noDesignReason":"","notes":"## Decisões"},"reply":"Pronto."}`, true, 1},
		{"without design", `{"brainstorm":{"flow":"flowchart TD\n A-->B","design":null,"noDesignReason":"Só backend: sem telas novas.","notes":""},"reply":"Pronto."}`, true, 0},
		{"empty flow", `{"brainstorm":{"flow":"  ","design":null,"noDesignReason":"x","notes":""},"reply":"r"}`, false, 0},
		{"no design without a reason", `{"brainstorm":{"flow":"flowchart TD\n A-->B","design":null,"noDesignReason":"","notes":""},"reply":"r"}`, false, 0},
		{"thirteen screens", `{"brainstorm":{"flow":"flowchart TD\n A-->B","design":{"screens":[` + strings.TrimSuffix(strings.Repeat(screen+",", 13), ",") + `]},"noDesignReason":"","notes":""},"reply":"r"}`, false, 0},
		{"unknown key", `{"brainstorm":{"flow":"flowchart TD\n A-->B","design":null,"noDesignReason":"x","notes":"","extra":1},"reply":"r"}`, false, 0},
		{"truncated", `{"brainstorm":{"flow":"flowchart TD`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, reply, err := parseBrainstormOutput(tc.raw)
			if tc.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if !tc.ok {
				if !errors.Is(err, errInvalidSDDOutput) {
					t.Fatalf("refusal must be an invalid output: %v", err)
				}
				return
			}
			screens := 0
			if content.Design != nil {
				screens = len(content.Design.Screens)
			}
			if reply != "Pronto." || screens != tc.screens || content.Flow == "" {
				t.Fatalf("content = %+v, reply = %q", content, reply)
			}
			again, _, err := parseBrainstormOutput(`{"brainstorm":` + content.Canonical() + `,"reply":"Pronto."}`)
			if err != nil || again.Canonical() != content.Canonical() {
				t.Fatalf("canonical form does not round-trip: %v", err)
			}
		})
	}
}

func TestValidateBrainstormHTML(t *testing.T) {
	page := func(body string) string { return `<!doctype html><html><body>` + body + `</body></html>` }
	for _, tc := range []struct {
		name, html string
		ok         bool
	}{
		{"inline style", `<!doctype html><html><head><style>.a{color:red}</style></head><body><p class="a">ok</p></body></html>`, true},
		{"data image", page(`<img alt="logo" src="data:image/png;base64,iVBORw0KGgo=">`), true},
		{"script", page(`<script>alert(1)</script>`), false},
		{"event handler", page(`<button onclick="go()">Ir</button>`), false},
		{"javascript url", page(`<a href="javascript:go()">Ir</a>`), false},
		{"iframe", page(`<iframe src="data:text/html,x"></iframe>`), false},
		{"object", page(`<object data="x"></object>`), false},
		{"embed", page(`<embed src="x">`), false},
		{"external form", page(`<form action="https://example.com/send"></form>`), false},
		{"external font", `<!doctype html><html><head><link rel="stylesheet" href="https://fonts.googleapis.com/css?family=Inter"></head><body></body></html>`, false},
		{"external css url", page(`<div style="background:url(https://x.test/y.png)"></div>`), false},
		{"protocol relative", page(`<img src="//x.test/y.png">`), false},
		{"uppercase script", page(`<SCRIPT>alert(1)</SCRIPT>`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBrainstormHTML(tc.html)
			if tc.ok != (err == nil) {
				t.Fatalf("validateBrainstormHTML = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func quoteJSON(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}
