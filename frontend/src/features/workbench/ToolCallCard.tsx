import { Check, CircleDashed, CircleSlash, LoaderCircle, ShieldAlert, TriangleAlert, Wrench } from 'lucide-react'
import type { ToolCallView, ToolStatus } from '../../state/session'
import { DiffView } from '../../components/DiffView'
import { t, useT } from '../../i18n'

const statusCopy: Record<ToolStatus, string> = {
  pending: 'Na fila', awaiting_approval: 'Aguardando aprovação', running: 'Executando', completed: 'Concluída',
  failed: 'Falhou', denied: 'Negada', skipped: 'Não executada',
}
const statusIcon = { pending: CircleDashed, awaiting_approval: ShieldAlert, running: LoaderCircle, completed: Check, failed: TriangleAlert, denied: CircleSlash, skipped: CircleSlash }

// Failures a tool reports so the model can adjust: the run goes on after them.
const adjustable: Record<string, string> = {
  not_found: 'Não encontrado', invalid_arguments: 'Argumentos inválidos',
  not_a_file: 'Não é um arquivo', not_a_directory: 'Não é uma pasta', no_match: 'Trecho não encontrado no arquivo',
  ambiguous_match: 'O trecho aparece mais de uma vez', binary_file: 'Arquivo binário', range_required: 'Arquivo grande demais para ler de uma vez',
  permission_denied: 'Sem permissão', exit_status: 'O comando terminou com erro', timeout: 'O comando passou do tempo limite',
  mcp_error: 'O servidor MCP recusou a chamada',
}
// For these the explanation is the tool's own answer, shown under "Saída"; the short English note adds nothing.
const answerIsTheDetail = new Set(['mcp_error'])
// Reasons that end the call (and usually the run). The raw error is never shown for them: it can hold private paths or secrets.
const final: Record<string, string> = {
  tool_failed: 'A ferramenta falhou e a execução foi encerrada. O Harflex não exibe o erro bruto para não expor dados sensíveis.',
  invalid_tool_result: 'A ferramenta devolveu um resultado inválido e a execução foi encerrada.',
  policy_denied: 'A política de segurança negou esta ferramenta.',
  approval_denied: 'Você negou esta aprovação.',
  unknown_tool: 'O modelo pediu uma ferramenta que não existe.',
  skipped_after_failure: 'Não executada: uma chamada anterior falhou.',
  cancelled: 'Cancelada.',
}

export type ToolFailureText = { summary: string; detail?: string; note?: string }

/** What to tell the person about a call that did not complete, in plain Portuguese. */
export function toolFailureText(call: Pick<ToolCallView, 'errorCode' | 'error' | 'recoverable'>): ToolFailureText | undefined {
  const code = call.errorCode ?? ''
  if (code === 'outcome_unknown') return { summary: t('Resultado desconhecido após a interrupção. Verifique os efeitos antes de executar novamente.') }
  if (code === 'not_executed') return { summary: t('Não executada antes da interrupção.') }
  if (call.recoverable && adjustable[code]) return { summary: `${t(adjustable[code])}.`, detail: answerIsTheDetail.has(code) ? undefined : call.error, note: t('A execução continuou e o modelo foi avisado.') }
  if (final[code]) return { summary: t(final[code]) }
  return call.error ? { summary: call.error } : undefined
}

/** The most specific human target of a call: a path, a command or a search pattern. */
export function toolTarget(args: unknown): string {
  const data = args && typeof args === 'object' ? (args as Record<string, unknown>) : {}
  for (const key of ['path', 'command', 'pattern']) {
    if (typeof data[key] === 'string' && data[key]) return data[key] as string
  }
  return ''
}

export function ToolCallCard({ call }: { call: ToolCallView }) {
  const t = useT()
  const Icon = statusIcon[call.status]
  const target = toolTarget(call.arguments)
  const output = call.output || call.result
  const failure = toolFailureText(call)
  const diffLines = call.diff ? call.diff.split('\n').length : 0
  return <article className={`tool-card tool-${call.status}`} aria-label={t('Ferramenta {name}', { name: call.name })}>
    <header className="tool-card-header">
      <Wrench aria-hidden="true" />
      <strong className="mono">{call.name}</strong>
      {target && <span className="mono tool-target">{target}</span>}
      <span className="tool-status"><Icon aria-hidden="true" />{t(statusCopy[call.status])}</span>
    </header>
    {failure && <p className="tool-error">{failure.summary}{failure.detail && <> <span className="mono tool-error-detail">{failure.detail}</span></>}{failure.note && <span className="muted tool-error-note"> {failure.note}</span>}</p>}
    {call.diff && <details className="tool-output tool-diff" open={diffLines <= 30}><summary>{call.path ? t('Alterações em {path}', { path: call.path }) : t('Alterações')}</summary><DiffView diff={call.diff} /></details>}
    {output && <details className="tool-output" open={call.status === 'running' || call.status === 'failed'}><summary>{t('Saída')}</summary><pre className="mono">{output}</pre></details>}
  </article>
}
