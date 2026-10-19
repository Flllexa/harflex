package agentcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// defaultContextWindow is the window assumed when neither the model nor the person said how large it is.
	defaultContextWindow = 200_000
	// A conversation compacts before a turn that would take it past this share of its window.
	compactAt = 0.85
	// keptToolResultBytes is what stays of a long tool result in the latest exchange kept after compaction.
	keptToolResultBytes = 2048
	summaryMarker       = "[Resumo da conversa até aqui]"
)

// ContextLengthError is a provider's refusal of a request that does not fit the model's context window.
type ContextLengthError interface{ ContextLengthExceeded() bool }

func contextLengthExceeded(err error) bool {
	var full ContextLengthError
	return errors.As(err, &full) && full.ContextLengthExceeded()
}

// Compacted is the journal record of a compaction: the history the model works from afterwards, without the system
// instructions (the runner adds them again when a conversation is reopened).
type Compacted struct {
	Summary      string    `json:"summary"`
	Messages     []Message `json:"messages"`
	BeforeTokens int       `json:"beforeTokens"`
	AfterTokens  int       `json:"afterTokens"`
}

func summaryInstruction(window int) string {
	return fmt.Sprintf(`Resuma a conversa até aqui para que ela continue sem o histórico completo. Escreva em português, com estes títulos:
## Pedido e objetivo
## Instruções explícitas da pessoa
## Decisões tomadas
## Arquivos lidos e alterados
## Estado atual
## Problemas em aberto
## Próximos passos
Seja fiel ao que aconteceu, cite caminhos de arquivos e comandos exatos, e não invente nada. Responda só com o resumo, em até %d tokens.`, max(window/10, 1000))
}

// SetContextWindow sets the window this conversation keeps within; 0 means the default.
func (s *Session) SetContextWindow(tokens int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tokens <= 0 {
		tokens = defaultContextWindow
	}
	s.contextWindow = tokens
}

// ContextWindow is the window this conversation keeps within.
func (s *Session) ContextWindow() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.windowLocked()
}

func (s *Session) windowLocked() int {
	if s.contextWindow <= 0 {
		return defaultContextWindow
	}
	return s.contextWindow
}

func messagesBytes(messages []Message) int {
	encoded, _ := json.Marshal(messages)
	return len(encoded)
}

// estimateLocked is what the next request will weigh: what the provider last counted plus what came in since, or,
// before any count, the size of the conversation at about four bytes a token.
func (s *Session) estimateLocked() int {
	now := messagesBytes(s.messages)
	if s.usageTokens > 0 {
		return s.usageTokens + max(0, now-s.usageBytes)/4
	}
	// The provider's count already holds the tools sent with each request; a guess from the history adds them.
	tools, _ := json.Marshal(s.tools.Specs())
	return (now + len(tools)) / 4
}

// Compact summarizes the conversation now, between runs (the person asked for it).
func (s *Session) Compact(ctx context.Context) error {
	s.mu.Lock()
	if s.sealed || s.active || s.pending != nil {
		s.mu.Unlock()
		return ErrSessionBusy
	}
	if len(s.messages) == 0 {
		s.mu.Unlock()
		return nil
	}
	opCtx := s.beginLocked(ctx)
	s.mu.Unlock()
	defer s.end()
	return s.compact(opCtx, false)
}

// maybeCompact compacts at a turn boundary when the next request would pass the threshold. Callers hold no lock and
// only call it with every tool call answered.
func (s *Session) maybeCompact(ctx context.Context) error {
	s.mu.Lock()
	estimate, window := s.estimateLocked(), s.windowLocked()
	// After a compaction that barely shrank the history, summarizing again only helps once it grew by a fifth of the window.
	due := len(s.queue) == 0 && len(s.unanswered) == 0 && float64(estimate) > compactAt*float64(window) && (s.stuckAt == 0 || estimate > s.stuckAt+window/5)
	s.mu.Unlock()
	if !due {
		return nil
	}
	return s.compact(ctx, false)
}

// compact replaces the history with a summary. After a refusal the window the model really has is smaller than
// what was sent, so the summary request is cut to half of it rather than to the share of the assumed window.
func (s *Session) compact(ctx context.Context, refused bool) error {
	s.mu.Lock()
	messages := cloneMessages(s.messages)
	window := s.windowLocked()
	before := s.estimateLocked()
	s.mu.Unlock()
	lead := 0
	for lead < len(messages) && messages[lead].Role == RoleSystem {
		lead++
	}
	if lead == len(messages) {
		return nil
	}
	budget := window * 7 / 10
	if refused {
		budget = min(budget, before/2)
	}
	request := append(fitForSummary(messages, lead, budget), Message{Role: RoleUser, Content: summaryInstruction(window)})
	summary, err := s.collectText(ctx, ChatRequest{Messages: request})
	if err != nil {
		return err
	}
	if strings.TrimSpace(summary) == "" {
		return errors.New("the model returned an empty summary")
	}
	first := messages[lead]
	// A first request larger than a tenth of the window (instructions carrying a whole diff) would leave no room: its
	// head and tail stay, and the summary carries the rest.
	if budget := window / 10 * 4; len(first.Content) > budget {
		first.Content = shortenMiddle(first.Content, budget)
	}
	kept := []Message{first, {Role: RoleUser, Content: summaryMarker + "\n\n" + strings.TrimSpace(summary)}}
	latest := latestExchange(messages, lead+1)
	for index := range latest {
		if latest[index].Role == RoleTool && len(latest[index].Content)/4 > window/10 {
			latest[index].Content = shortenText(latest[index].Content)
		}
	}
	kept = append(kept, latest...)
	record := Compacted{Summary: strings.TrimSpace(summary), Messages: kept, BeforeTokens: before, AfterTokens: messagesBytes(kept) / 4}
	if err := s.persist(ctx, "context.compacted", record); err != nil {
		return err
	}
	s.mu.Lock()
	s.messages = append(cloneMessages(messages[:lead]), cloneMessages(kept)...)
	s.usageTokens, s.usageBytes = 0, 0
	s.stuckAt = 0
	if after := s.estimateLocked(); after*5 > before*4 {
		s.stuckAt = after
	}
	s.checkCompaction = true
	s.mu.Unlock()
	return nil
}

// latestExchange is the model's last answer with the results it is waiting for, or the person's newest message.
func latestExchange(messages []Message, from int) []Message {
	last := len(messages) - 1
	if last < from {
		return nil
	}
	if messages[last].Role == RoleUser {
		return cloneMessages(messages[last:])
	}
	for index := last; index >= from; index-- {
		if messages[index].Role == RoleAssistant {
			return cloneMessages(messages[index:])
		}
	}
	return nil
}

// fitForSummary keeps the request for the summary inside the window even when the conversation outgrew it: the
// oldest long tool results are shortened first, then the oldest exchanges are left out.
func fitForSummary(messages []Message, lead, budgetTokens int) []Message {
	result := cloneMessages(messages)
	for index := lead; index < len(result) && messagesBytes(result)/4 > budgetTokens; index++ {
		if result[index].Role == RoleTool && len(result[index].Content) > keptToolResultBytes {
			result[index].Content = shortenText(result[index].Content)
		}
	}
	// An exchange leaves whole (an answer with every result it got), and the newest one stays.
	newest := len(result) - len(latestExchange(result, lead+1))
	for messagesBytes(result)/4 > budgetTokens {
		drop := lead + 1
		end := drop + 1
		for end < len(result) && result[end].Role == RoleTool {
			end++
		}
		if end > newest {
			break
		}
		result = append(result[:drop], result[end:]...)
		newest -= end - drop
	}
	return result
}

// shortenText keeps the head of a long text. A tool result stays valid JSON (history checks require it), so the
// shortened one becomes a JSON string.
func shortenText(content string) string {
	cut := min(keptToolResultBytes, len(content))
	for cut > 0 && cut < len(content) && !utf8.RuneStart(content[cut]) {
		cut--
	}
	shortened := content[:cut] + fmt.Sprintf("\n[… %d bytes deixados de fora ao compactar o contexto]", len(content)-cut)
	if json.Valid([]byte(content)) {
		encoded, _ := json.Marshal(shortened)
		return string(encoded)
	}
	return shortened
}

// collectText runs one request without tools and returns the text the model wrote.
func (s *Session) collectText(ctx context.Context, request ChatRequest) (string, error) {
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	items, failures := s.provider.Stream(streamCtx, request)
	var text strings.Builder
	var failure error
	for items != nil || failures != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case err, ok := <-failures:
			if !ok {
				failures = nil
				continue
			}
			if err != nil && failure == nil {
				failure = err
			}
		case item, ok := <-items:
			if !ok {
				items = nil
				continue
			}
			if item.Type == "text_delta" && text.Len() < maxMessageBytes {
				text.WriteString(item.Delta)
			}
		}
	}
	if failure != nil {
		return "", failure
	}
	return text.String(), nil
}

// shortenMiddle keeps the head and the tail of a long text within budget bytes, saying how much was left out.
func shortenMiddle(content string, budget int) string {
	half := max(budget/2, 256)
	if len(content) <= 2*half {
		return content
	}
	head, tail := half, len(content)-half
	for head > 0 && !utf8.RuneStart(content[head]) {
		head--
	}
	for tail < len(content) && !utf8.RuneStart(content[tail]) {
		tail++
	}
	return content[:head] + fmt.Sprintf("\n\n[… %d bytes do pedido original deixados de fora ao compactar o contexto; o resumo traz o que importa deles]\n\n", tail-head) + content[tail:]
}
