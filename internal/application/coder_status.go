package application

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/sdd"
)

// ErrPipelineCodeNotFinished says the Coder's run ended without the Coder saying it finished the plan: it asked
// something, or stopped partway. The change is not offered for review until it says it is done.
var ErrPipelineCodeNotFinished = errors.New("the Coder has not said it finished the work")

// coderStatusInstructions is how the Coder says where it stands at the end of each turn. The person answers the
// questions in the Code bench and the Coder goes on; the review opens only when it says it is done.
const coderStatusInstructions = "Implemente o plano até o fim. Não crie arquivos de status, pendências, checklists ou próximos passos no lugar do código: o que não puder confirmar no código (use as ferramentas de leitura e busca da cópia), registre como hipótese no próprio código ou na resposta. " +
	"Se precisar de uma decisão ou de uma informação que só a pessoa tem, pare e pergunte: termine a mensagem com o bloco\n```harflex-code\n{\"status\":\"questions\",\"questions\":[\"pergunta 1\",\"pergunta 2\"]}\n```\nA pessoa responde nesta mesma conversa e você continua de onde parou. " +
	"Quando todos os passos do plano estiverem implementados, termine a mensagem com o bloco\n```harflex-code\n{\"status\":\"done\",\"summary\":\"o que foi feito, em uma ou duas frases\"}\n```\nUse status done só quando terminou; a revisão do código abre apenas depois dele.\n"

var coderBlock = regexp.MustCompile("(?s)```harflex-code[^\\n]*\\n(.*?)```")

// coderReport reads the status block at the end of the Coder's message: done, questions, or unfinished when there is none.
func coderReport(text string) (state, summary string, questions []string, message string) {
	matches := coderBlock.FindAllStringSubmatchIndex(text, -1)
	message = strings.TrimSpace(coderBlock.ReplaceAllString(text, ""))
	if len(matches) == 0 {
		return "unfinished", "", nil, message
	}
	last := matches[len(matches)-1]
	var block struct {
		Status    string   `json:"status"`
		Summary   string   `json:"summary"`
		Questions []string `json:"questions"`
	}
	if json.Unmarshal([]byte(text[last[2]:last[3]]), &block) != nil {
		return "unfinished", "", nil, message
	}
	switch block.Status {
	case "done":
		return "done", strings.TrimSpace(block.Summary), nil, message
	case "questions":
		for _, question := range block.Questions {
			if question = strings.TrimSpace(question); question != "" {
				questions = append(questions, question)
			}
		}
		if len(questions) > 0 {
			return "questions", "", questions, message
		}
	}
	return "unfinished", "", nil, message
}

// coderTurn reads a Coder conversation: whether a run is going, and the text of the last reply of the last run.
func (s *Service) coderTurn(sessionID string) (state, text string, err error) {
	state = "none"
	parts := map[string]string{}
	order := []string{}
	reset := func() { parts = map[string]string{}; order = nil; text = "" }
	err = s.walkEvents(s.ctx, sessionID, func(event events.Event) error {
		switch event.Type {
		case "run.started", "external.run.started":
			state = "working"
			reset()
		case "run.completed", "external.run.completed":
			state = "completed"
		case "run.failed", "external.run.failed", "run.cancelled", "external.run.cancelled", "run.interrupted", "external.run.interrupted":
			state = "stopped"
		case "message.assistant":
			var data struct {
				Content string `json:"content"`
			}
			if json.Unmarshal(event.Data, &data) == nil && strings.TrimSpace(data.Content) != "" {
				text = data.Content
			}
		case "external.event":
			var data struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				MessageID string `json:"messageId"`
				Mode      string `json:"mode"`
			}
			if json.Unmarshal(event.Data, &data) != nil || data.Type != "assistant.message" {
				return nil
			}
			key := data.MessageID
			if _, seen := parts[key]; !seen {
				order = append(order, key)
			}
			if data.Mode == "delta" {
				parts[key] += data.Text
			} else {
				parts[key] = data.Text
			}
			if latest := parts[order[len(order)-1]]; strings.TrimSpace(latest) != "" {
				text = latest
			}
		}
		return nil
	})
	return state, text, err
}

// CoderStatusDTO says where the Coder of a work stands, for the Code bench.
type CoderStatusDTO struct {
	PipelineID string   `json:"pipelineId"`
	SessionID  string   `json:"sessionId"`
	State      string   `json:"state"` // none, working, done, questions, unfinished, stopped
	Summary    string   `json:"summary"`
	Questions  []string `json:"questions"`
	Message    string   `json:"message"`
	// CanReply: the conversation takes another message. When it cannot (a CLI run that ended), an answer opens a new
	// Coder round in the same private copy, with the questions and the answer.
	CanReply bool `json:"canReply"`
}

const maxCoderMessage = 8000

// GetPipelineCoderStatus reads the latest Coder conversation of a work: working, done, asking, or stopped partway.
func (s *Service) GetPipelineCoderStatus(pipelineID string) (CoderStatusDTO, error) {
	if err := s.beginCall(); err != nil {
		return CoderStatusDTO{}, err
	}
	defer s.endCall()
	out := CoderStatusDTO{PipelineID: pipelineID, State: "none"}
	links, err := s.store.ListPipelineSessions(s.ctx, pipelineID, "coder")
	if err != nil {
		return out, safe("list Coder sessions", err)
	}
	if len(links) == 0 {
		return out, nil
	}
	link, err := s.latestPipelineSession(pipelineID, "coder")
	if err != nil {
		return out, err
	}
	out.SessionID = link.SessionID
	state, text, err := s.coderTurn(link.SessionID)
	if err != nil {
		return out, safe("read Coder conversation", err)
	}
	out.State = state
	if state == "completed" {
		out.State, out.Summary, out.Questions, out.Message = coderReport(text)
	} else if state == "stopped" {
		_, _, _, out.Message = coderReport(text)
	}
	if len(out.Message) > maxCoderMessage {
		out.Message = clipUTF8(out.Message, maxCoderMessage)
	}
	if active, _ := s.pipelineCodeSessionActive(link.SessionID); active {
		if record, err := s.store.GetSession(s.ctx, link.SessionID); err == nil {
			resumable, _, err := s.sessionContinuation(record)
			out.CanReply = err == nil && resumable
		}
	}
	return out, nil
}

// ReplyToPipelineCoderInput is the person's answer to the Coder, and who works on it when a new round is needed.
type ReplyToPipelineCoderInput struct {
	PipelineID string             `json:"pipelineId"`
	Text       string             `json:"text"`
	Coder      PipelineRoleChoice `json:"coder"`
}

// ReplyToPipelineCoder prepares the person's answer for the Coder. The conversation that asked takes it when it can;
// otherwise a new Coder round opens in the same private copy, told what was asked and answered. The caller sends the
// returned prompt in the returned session, so the run shows like any other.
func (s *Service) ReplyToPipelineCoder(in ReplyToPipelineCoderInput) (PipelineSessionDTO, error) {
	text := strings.TrimSpace(in.Text)
	if in.PipelineID == "" || text == "" || len(text) > maxPromptBytes || !utf8.ValidString(text) {
		return PipelineSessionDTO{}, ErrInvalidInput
	}
	status, err := s.GetPipelineCoderStatus(in.PipelineID)
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	if status.SessionID == "" || status.State == "working" {
		return PipelineSessionDTO{}, sdd.ErrInvalidTransition
	}
	reply := "Respostas da pessoa:\n" + text + "\n\nContinue o trabalho de onde parou com essas respostas."
	if status.CanReply {
		record, err := s.store.GetSession(s.ctx, status.SessionID)
		if err != nil {
			return PipelineSessionDTO{}, safe("read Coder session", err)
		}
		return PipelineSessionDTO{Session: s.sessionDTO(record), Prompt: reply, Role: "coder"}, nil
	}
	choice := in.Coder
	record, err := s.store.GetSession(s.ctx, status.SessionID)
	if err != nil {
		return PipelineSessionDTO{}, safe("read Coder session", err)
	}
	if strings.TrimSpace(choice.BackendID) == "" {
		choice.BackendID = record.BackendID
	}
	// A CLI round keeps the model the previous one ran on, confirmed against the catalog again.
	if _, external := s.external[choice.BackendID]; external && choice.Selection == nil && choice.BackendID == record.BackendID {
		if previous, err := s.store.GetSessionModelSelection(s.ctx, record.ID); err == nil && previous.ModelID != "" {
			choice.Selection = &APIModelSelectionInput{Executor: "cli", BackendID: choice.BackendID, Source: documentCLISources[choice.BackendID], ModelID: previous.ModelID, ReasoningEffort: previous.ReasoningEffort}
		}
	}
	choice = s.freshRoleChoice(in.PipelineID, choice)
	created, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: in.PipelineID, BackendID: choice.BackendID, Role: "coder", Selection: choice.Selection, ContinuePreviousCopy: true})
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	var previous strings.Builder
	previous.WriteString(created.Prompt)
	previous.WriteString("\nA rodada anterior do Coder, nesta mesma cópia, parou com esta mensagem:\n")
	previous.WriteString(status.Message)
	if len(status.Questions) > 0 {
		previous.WriteString("\nPerguntas que ela fez:\n- ")
		previous.WriteString(strings.Join(status.Questions, "\n- "))
	}
	previous.WriteString("\n\n")
	previous.WriteString(reply)
	created.Prompt = previous.String()
	return created, nil
}
