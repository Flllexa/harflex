package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

// brainstormOutputTokens is the Brainstorm phase's own output budget: screens are full HTML pages.
const brainstormOutputTokens = 32768

// brainstormPromptReserve is the room a Brainstorm prompt needs besides the answer: instructions, guide, Discovery, SPEC
// and the project's design. A model whose context cannot hold both is refused before anything starts.
const brainstormPromptReserve = 16384

const maxProjectDesignBytes = 32 * 1024

const maxBrainstormPromptBytes = 768 * 1024

// ErrBrainstormModelTooSmall says the model chosen for Brainstorm cannot hold its prompt and answer.
var ErrBrainstormModelTooSmall = errors.New("the model chosen for Brainstorm cannot write a document of this size")

const brainstormSystemPrompt = `Você é o designer de produto do Harflex. Esta é uma tarefa de texto, sem ferramentas ou acesso a arquivos. Use apenas o pedido e os documentos fornecidos como dados. A partir do Discovery e da SPEC, escreva o Brainstorm: o fluxo da aplicação e, quando houver telas, o design delas. Não escreva o Plan nem mude a SPEC. O campo projectMemory, quando presente, descreve o que o projeto já tem; o campo projectDesign, quando presente, é o DESIGN.md do projeto: siga a identidade dele (cores, tipografia, componentes, tom) em vez de inventar outra.

Responda somente um objeto JSON com as chaves brainstorm e reply. brainstorm tem:
- flow: um diagrama Mermaid (flowchart) com o caminho da pessoa e do sistema, incluindo erros e estados alternativos relevantes. Sempre obrigatório. Use rótulos curtos entre colchetes ou aspas.
- design: {"screens": [{"name", "purpose", "html"}]} com até 12 telas, ou null quando não há telas.
- noDesignReason: vazio quando há design; sem design, uma frase dizendo por quê.
- notes: Markdown com uma seção "Decisões" em tabela | # | Questão | Decisão | Justificativa | (ids D1, D2…) e uma seção "Perguntas em aberto" numerada P1, P2…, como nos outros documentos.
reply resume em uma ou duas frases o que você desenhou ou mudou.

Regra do design: há design quando a SPEC cria ou muda algo que uma pessoa vê ou usa (telas, formulários, painéis, e-mails). Não há design quando a mudança é só backend, integração, job ou dados; então design é null e noDesignReason explica.

Cada tela é uma página HTML completa e autocontida, em português do Brasil, com CSS embutido em <style>. Proibido: <script>, atributos on* (onclick…), links javascript:, <iframe>, <object>, <embed>, <base>, <meta http-equiv>, @import e qualquer URL externa (http:, https:, //) em href, src, action ou url(). Use fontes do sistema e imagens só como data:. A tela é estática: mostre o estado principal e, quando fizer diferença, os outros estados lado a lado ou empilhados.

Guia de design do Harflex:
1. Hierarquia: um título claro por tela, a ação principal evidente e uma só; ações secundárias com menos peso. Espaçamento consistente em múltiplos de 4 px; agrupe o que é relacionado e separe o resto com espaço, não com bordas.
2. Estados: projete o vazio (com o próximo passo), o carregando, o erro (dizendo o que aconteceu e como resolver) e o sucesso. Nunca deixe a pessoa sem saída.
3. Textos reais: rótulos, mensagens e exemplos com o vocabulário do projeto; nada de lorem ipsum. Botões dizem o que fazem ("Salvar contrato", não "OK").
4. Acessibilidade: contraste AA, foco visível, rótulos em todos os campos, alvos de toque com pelo menos 44 px, nada que dependa só de cor.
5. Responsividade: a tela funciona de 360 px a desktop; em telas estreitas, uma coluna e a ação principal ao alcance.
6. Nada de moldura dentro de moldura: evite cartões dentro de cartões e caixas aninhadas; use espaço e tipografia para estruturar.
7. Consistência: siga a identidade do projeto (projectDesign e projectMemory); sem ela, use um visual neutro e sóbrio.

Preserve decisões e edições do usuário que o novo pedido não altera. Não declare testes ou implementação realizados: você está preparando documentos para revisão.`

var brainstormOutputSchema = json.RawMessage(`{"type":"object","properties":{"brainstorm":{"type":"object","properties":{"flow":{"type":"string"},"design":{"anyOf":[{"type":"null"},{"type":"object","properties":{"screens":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"purpose":{"type":"string"},"html":{"type":"string"}},"required":["name","purpose","html"],"additionalProperties":false}}},"required":["screens"],"additionalProperties":false}]},"noDesignReason":{"type":"string"},"notes":{"type":"string"}},"required":["flow","design","noDesignReason","notes"],"additionalProperties":false},"reply":{"type":"string"}},"required":["brainstorm","reply"],"additionalProperties":false}`)

// projectDesignContext reads the project's DESIGN.md, up to 32 KB, so the screens follow its identity. A missing file,
// or one that is not a plain file (a link could point outside the project), gives nothing.
func projectDesignContext(projectPath string) string {
	if projectPath == "" {
		return ""
	}
	path := filepath.Join(projectPath, "DESIGN.md")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxProjectDesignBytes))
	if err != nil {
		return ""
	}
	for len(content) > 0 && !utf8.Valid(content) {
		content = content[:len(content)-1]
	}
	return strings.TrimSpace(string(content))
}

// brainstormSelectionFits refuses, before anything starts, a model whose output budget or context cannot hold a
// Brainstorm document.
func brainstormSelectionFits(choice catalog.ModelSelection) error {
	// Zero means no answer limit, which always fits; a smaller explicit limit or context does not.
	if choice.MaxOutputTokens > 0 && choice.MaxOutputTokens < brainstormOutputTokens || choice.ContextLength > 0 && choice.ContextLength < brainstormOutputTokens+brainstormPromptReserve {
		return ErrBrainstormModelTooSmall
	}
	return nil
}

// brainstormPrompt is the Brainstorm phase's request. refused carries the reason a previous answer was refused, so the
// AI can correct it.
func brainstormPrompt(message string, documents map[sdd.Stage]catalog.PipelineDesignDocument, conversation []catalog.PipelineDesignMessage, projectMemory, projectDesign, refused string) (string, error) {
	if len(conversation) > 12 {
		conversation = conversation[len(conversation)-12:]
	}
	encoded := marshalReadable(struct {
		Target                string                          `json:"target"`
		OutputDocument        string                          `json:"outputDocument"`
		Request               string                          `json:"request"`
		Documents             map[string]string               `json:"documents"`
		Conversation          []catalog.PipelineDesignMessage `json:"conversation"`
		ProjectMemory         string                          `json:"projectMemory,omitempty"`
		ProjectDesign         string                          `json:"projectDesign,omitempty"`
		PreviousAnswerRefused string                          `json:"previousAnswerRefused,omitempty"`
	}{
		Target:         string(sdd.Brainstorm),
		OutputDocument: "Escreva somente o Brainstorm no campo brainstorm. Use Discovery e SPEC como fonte; o Brainstorm atual, quando houver, é a versão a ajustar.",
		Request:        message,
		Documents: map[string]string{
			string(sdd.Discovery):  documents[sdd.Discovery].Content,
			string(sdd.Spec):       documents[sdd.Spec].Content,
			string(sdd.Brainstorm): documents[sdd.Brainstorm].Content,
		},
		Conversation:          conversation,
		ProjectMemory:         projectMemory,
		ProjectDesign:         projectDesign,
		PreviousAnswerRefused: refusedReason(refused),
	})
	if encoded == "" || len(encoded) > maxBrainstormPromptBytes {
		return "", ErrInvalidInput
	}
	return encoded, nil
}

func refusedReason(reason string) string {
	if reason == "" {
		return ""
	}
	return "Sua resposta anterior foi recusada: " + reason + ". Envie de novo o objeto JSON completo, corrigido."
}

// brainstormSummary is the Brainstorm as another document's source sees it: the flow, each screen by name and purpose,
// and the notes. The HTML stays out; the Plan needs what the screens are, not their markup.
func brainstormSummary(content string) string {
	if content == "" {
		return ""
	}
	var brainstorm BrainstormContent
	if err := json.Unmarshal([]byte(content), &brainstorm); err != nil || strings.TrimSpace(brainstorm.Flow) == "" {
		return freeTextBrainstorm(content, brainstormCodeBudget)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "## Fluxo\n\n```mermaid\n%s\n```\n", strings.TrimSpace(brainstorm.Flow))
	if brainstorm.Design != nil && len(brainstorm.Design.Screens) > 0 {
		text.WriteString("\n## Telas\n")
		for _, screen := range brainstorm.Design.Screens {
			fmt.Fprintf(&text, "- **%s**: %s\n", screen.Name, screen.Purpose)
		}
	} else if brainstorm.NoDesignReason != "" {
		fmt.Fprintf(&text, "\n## Sem design\n\n%s\n", brainstorm.NoDesignReason)
	}
	if notes := strings.TrimSpace(brainstorm.Notes); notes != "" {
		fmt.Fprintf(&text, "\n%s\n", notes)
	}
	return strings.TrimSpace(text.String())
}

// brainstormCodeBudget bounds the approved screens the Coder receives in full.
const brainstormCodeBudget = 200 * 1024

// brainstormExecutionContext is the approved Brainstorm as the Coder reads it: the flow, then each screen's HTML while
// they fit in budget, then the remaining screens by name and purpose.
func brainstormExecutionContext(content string, budget int) string {
	if content == "" {
		return ""
	}
	var brainstorm BrainstormContent
	if err := json.Unmarshal([]byte(content), &brainstorm); err != nil || strings.TrimSpace(brainstorm.Flow) == "" {
		return freeTextBrainstorm(content, budget)
	}
	var text strings.Builder
	fmt.Fprintf(&text, "## Fluxo\n\n```mermaid\n%s\n```\n", strings.TrimSpace(brainstorm.Flow))
	if brainstorm.Design == nil || len(brainstorm.Design.Screens) == 0 {
		if brainstorm.NoDesignReason != "" {
			fmt.Fprintf(&text, "\n## Sem design\n\n%s\n", brainstorm.NoDesignReason)
		}
	} else {
		text.WriteString("\n## Telas\n")
		remaining := []BrainstormScreen{}
		for _, screen := range brainstorm.Design.Screens {
			if len(remaining) > 0 || text.Len()+len(screen.HTML) > budget {
				remaining = append(remaining, screen)
				continue
			}
			fmt.Fprintf(&text, "\n### %s\n\n%s\n\n```html\n%s\n```\n", screen.Name, screen.Purpose, screen.HTML)
		}
		if len(remaining) > 0 {
			text.WriteString("\nTelas que não cabem aqui (por nome e propósito):\n")
			for _, screen := range remaining {
				fmt.Fprintf(&text, "- %s: %s\n", screen.Name, screen.Purpose)
			}
		}
	}
	if notes := strings.TrimSpace(brainstorm.Notes); notes != "" {
		fmt.Fprintf(&text, "\n%s\n", notes)
	}
	return strings.TrimSpace(text.String())
}

// brainstormPromptFits says whether the model's context holds a document call's prompt and answer (every document
// phase uses it; Brainstorm also checks before its session exists, to say the model is too small). The context is counted in
// tokens; the prompt is estimated at three bytes a token, which errs on the side of a fit for HTML and Portuguese text.
func brainstormPromptFits(choice catalog.ModelSelection, systemPrompt, prompt string) bool {
	if choice.ContextLength <= 0 {
		return true
	}
	return (len(prompt)+len(systemPrompt))/3+2048+choice.MaxOutputTokens <= choice.ContextLength
}

// freeTextBrainstorm is a Brainstorm a manual pipeline wrote as plain text: kept as written, cut to the budget on a
// character boundary.
func freeTextBrainstorm(content string, budget int) string {
	text := strings.TrimSpace(content)
	if len(text) <= budget {
		return text
	}
	text = text[:budget]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}
