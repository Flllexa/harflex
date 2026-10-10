/**
 * When the person asks for something complete in a chat, the agent does not start: it says what it understood and ends its message
 * with a `harflex-choice` block. Harflex draws the choices itself (so they are the same in every language and every model), and the
 * block never reaches the screen as text.
 */
export type PipelineChoice = 'casual' | 'professional' | 'chat'

const complete = /```harflex-choice[^\n]*\n([\s\S]*?)```/
const unfinished = /```harflex-choice[\s\S]*$/

/** The message without the block, and whether the agent asked the person to choose. */
export function splitPipelineChoice(text: string): { text: string; ask: boolean } {
  const found = complete.exec(text)
  if (found) {
    let kind: unknown
    try { kind = (JSON.parse(found[1]) as { kind?: unknown }).kind } catch { kind = undefined }
    return { text: text.replace(complete, '').trimEnd(), ask: kind === 'pipeline' }
  }
  // Still being written: hide what has arrived of it.
  return { text: text.replace(unfinished, '').trimEnd(), ask: false }
}

/** What the choice says to the agent. It is the agent's prompt, so it stays in Portuguese like the other prompts. */
export function pipelineChoicePrompt(choice: PipelineChoice): string {
  switch (choice) {
    case 'casual': return 'Quero seguir com uma pipeline de execução e conduzir tudo por aqui, no modo Casual. Combine comigo o que ainda estiver em aberto e crie a pipeline.'
    case 'professional': return 'Quero seguir com uma pipeline de execução no modo Profissional. Combine comigo o que ainda estiver em aberto e crie a pipeline; eu abro o modo Profissional.'
    default: return 'Resolva por aqui, na conversa, sem abrir uma pipeline.'
  }
}
