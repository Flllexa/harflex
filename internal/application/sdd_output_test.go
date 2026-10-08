package application

import (
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
)

func TestParseSDDQuestionOutput(t *testing.T) {
	want := "Qual é o objetivo?"
	got, err := parseSDDQuestionOutput(`{"question":"Qual é o objetivo?"}`)
	if err != nil || got != want {
		t.Fatalf("question = %q, %v", got, err)
	}
	invalid := []string{
		``, `{}`, `{"question":""}`, `{"question":"  "}`,
		`{"question":"Sem interrogação"}`, `{"question":"Uma? Duas?"}`,
		`{"question":"Linha\nquebrada?"}`, `{"question":"Linha\rquebrada?"}`,
		`{"question":"Qual?","extra":1}`, `{"question":"Qual?","question":"Outra?"}`,
		`{"question":"Linha\u0085quebrada?"}`, `{"question":"Linha\u000bquebrada?"}`, `{"question":"Linha\u000cquebrada?"}`,
		`{"question":"Linha\u2028quebrada?"}`, `{"question":"Linha\u2029quebrada?"}`,
		`{"Question":"Qual?"}`, `{"question":null}`, `{"question":12}`,
		"```json\n{\"question\":\"Qual?\"}\n```", `{"question":"Qual?"} trailing`,
		`{"question":"` + strings.Repeat("é", 281) + `?"}`,
		`{"question":"` + strings.Repeat("é", 1025) + `?"}`,
		`{"question":"` + string([]byte{0xff}) + `?"}`,
	}
	for _, raw := range invalid {
		t.Run(rawName(raw), func(t *testing.T) {
			if _, err := parseSDDQuestionOutput(raw); err == nil {
				t.Fatal("accepted invalid question output")
			}
		})
	}
}

func TestParseSDDQuestionExactBoundaries(t *testing.T) {
	for _, count := range []int{280, 281} {
		question := strings.Repeat("界", count-1) + "?"
		_, err := parseSDDQuestionOutput(`{"question":"` + question + `"}`)
		if (err == nil) != (count == 280) {
			t.Fatalf("character count %d: %v", count, err)
		}
	}
	base := `{"question":"` + strings.Repeat(`\u0061`, 279) + `?"}`
	for _, bytes := range []int{2048, 2049} {
		output := strings.Repeat(" ", bytes-len(base)) + base
		if len(output) != bytes {
			t.Fatal("test fixture length")
		}
		_, err := parseSDDQuestionOutput(output)
		if (err == nil) != (bytes == 2048) {
			t.Fatalf("output bytes %d: %v", bytes, err)
		}
	}
}

func TestParseSDDSynthesisOutput(t *testing.T) {
	want := catalog.BrainstormSynthesisContent{Scope: "Produto", Decisions: []string{"Priorizar API"}, OpenQuestions: []string{"Prazo?"}}
	got, err := parseSDDSynthesisOutput(`{"decisions":["Priorizar API"],"openQuestions":["Prazo?"],"scope":"Produto"}`)
	if err != nil || got.Scope != want.Scope || len(got.Decisions) != 1 || got.Decisions[0] != want.Decisions[0] || len(got.OpenQuestions) != 1 || got.OpenQuestions[0] != want.OpenQuestions[0] {
		t.Fatalf("synthesis = %+v, %v", got, err)
	}
	invalid := []string{
		``, `{}`, `{"decisions":[],"openQuestions":[],"scope":"Produto"}`,
		`{"decisions":[""],"openQuestions":[],"scope":"Produto"}`,
		`{"decisions":["  "],"openQuestions":[],"scope":"Produto"}`,
		`{"decisions":["Decisão"],"openQuestions":[],"scope":"  "}`,
		`{"decisions":["Decisão"],"openQuestions":[""],"scope":"Produto"}`,
		`{"decisions":["Decisão"],"openQuestions":null,"scope":"Produto"}`,
		`{"decisions":["Decisão"],"openQuestions":[],"scope":"Produto","extra":1}`,
		`{"decisions":["Decisão"],"decisions":["Outra"],"openQuestions":[],"scope":"Produto"}`,
		`{"decisions":["Decisão"],"openQuestions":[],"scope":"Produto"} {}`,
		"```json\n{\"decisions\":[\"Decisão\"],\"openQuestions\":[],\"scope\":\"Produto\"}\n```",
		`{"decisions":["Decisão"],"openQuestions":[],"scope":"` + strings.Repeat("a", 65536) + `"}`,
		`{"decisions":["Decisão"],"openQuestions":[],"scope":"` + string([]byte{0xff}) + `"}`,
	}
	for _, raw := range invalid {
		t.Run(rawName(raw), func(t *testing.T) {
			if _, err := parseSDDSynthesisOutput(raw); err == nil {
				t.Fatal("accepted invalid synthesis output")
			}
		})
	}
}

func TestParseSDDSynthesisExactByteBoundary(t *testing.T) {
	prefix := `{"decisions":["Decision"],"openQuestions":[],"scope":"`
	suffix := `"}`
	for _, bytes := range []int{65536, 65537} {
		output := prefix + strings.Repeat("a", bytes-len(prefix)-len(suffix)) + suffix
		if len(output) != bytes {
			t.Fatal("test fixture length")
		}
		_, err := parseSDDSynthesisOutput(output)
		if (err == nil) != (bytes == 65536) {
			t.Fatalf("output bytes %d: %v", bytes, err)
		}
	}
}

func rawName(raw string) string {
	if len(raw) > 60 {
		return raw[:60]
	}
	return raw
}
