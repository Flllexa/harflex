import type { KeyboardEvent } from 'react'
import { ShieldAlert } from 'lucide-react'
import type { Approval } from '../../lib/backend'
import { DiffView, clip } from '../../components/DiffView'
import { toolTarget } from './ToolCallCard'
import { useT } from '../../i18n'

const effects: Record<string, string> = {
  read_only: 'Lê arquivos do projeto.',
  write: 'Cria ou altera arquivos no projeto.',
  shell: 'Executa um comando no seu computador.',
  network: 'Acessa a rede.',
  destructive: 'Pode remover ou sobrescrever dados.',
}

type Props = { approval: Approval; disabled: boolean; onResolve: (allow: boolean) => void }
const text = (value: unknown) => (typeof value === 'string' ? value : '')

/** What the call is about to do, shown before the decision so approving is informed. */
function Preview({ name, args }: { name: string; args: unknown }) {
  const t = useT()
  const data = args && typeof args === 'object' ? (args as Record<string, unknown>) : {}
  if (typeof data.command === 'string' && data.command) {
    return <div className="approval-preview"><span className="approval-preview-label">{t('Comando')}</span><pre className="mono" tabIndex={0} aria-label={t('Comando a executar')}>{data.command}</pre></div>
  }
  if (name === 'write' && typeof data.content === 'string') {
    const { text: shown, hiddenLines } = clip(data.content)
    return <div className="approval-preview"><span className="approval-preview-label">{data.content === '' ? t('Conteúdo a gravar (arquivo vazio)') : t('Conteúdo a gravar')}</span>
      {data.content !== '' && <pre className="mono" tabIndex={0} aria-label={t('Conteúdo a gravar')}>{shown}</pre>}
      {hiddenLines > 0 && <span className="muted approval-preview-more">{t('+ {count} {unit} não exibidas', { count: hiddenLines, unit: hiddenLines === 1 ? t('linha') : t('linhas') })}</span>}</div>
  }
  if (name === 'edit' && (typeof data.oldText === 'string' || typeof data.newText === 'string')) {
    const removed = clip(text(data.oldText), 10), added = clip(text(data.newText), 10)
    const diff = [...removed.text.split('\n').map(line => `-${line}`), ...added.text.split('\n').map(line => `+${line}`)].join('\n')
    const hidden = removed.hiddenLines + added.hiddenLines
    return <div className="approval-preview"><span className="approval-preview-label">{t('Substituição')}</span><DiffView diff={diff} label={t('Substituição proposta')} />
      {hidden > 0 && <span className="muted approval-preview-more">{t('+ {count} {unit} não exibidas', { count: hidden, unit: hidden === 1 ? t('linha') : t('linhas') })}</span>}</div>
  }
  const rest = Object.entries(data).filter(([key]) => !['path', 'pattern'].includes(key))
  if (rest.length === 0) return null
  const { text: shown, hiddenLines } = clip(JSON.stringify(Object.fromEntries(rest), null, 2), 12, 1200)
  return <div className="approval-preview"><span className="approval-preview-label">{t('Argumentos')}</span><pre className="mono" tabIndex={0} aria-label={t('Argumentos da chamada')}>{shown}</pre>
    {hiddenLines > 0 && <span className="muted approval-preview-more">{t('+ {count} {unit} não exibidas', { count: hiddenLines, unit: hiddenLines === 1 ? t('linha') : t('linhas') })}</span>}</div>
}

export function ApprovalCard({ approval, disabled, onResolve }: Props) {
  const t = useT()
  const target = toolTarget(approval.arguments)
  const args = approval.arguments && typeof approval.arguments === 'object' ? (approval.arguments as Record<string, unknown>) : {}
  // A shell command is shown whole in the preview, so the one-line target would only repeat it.
  const showTarget = target && typeof args.command !== 'string'
  // Enter never approves implicitly: approval takes a click or Space on the button.
  const ignoreEnter = (event: KeyboardEvent<HTMLButtonElement>) => { if (event.key === 'Enter') event.preventDefault() }
  return <section className="approval-card" role="group" aria-labelledby={`approval-${approval.approvalId}`}>
    <div className="approval-heading"><ShieldAlert aria-hidden="true" /><h3 id={`approval-${approval.approvalId}`}>{t('Aprovação necessária')}</h3></div>
    <dl className="approval-facts">
      <div><dt>{t('Ferramenta')}</dt><dd className="mono">{approval.name}</dd></div>
      {showTarget && <div><dt>{t('Alvo')}</dt><dd className="mono">{target}</dd></div>}
      <div><dt>{t('Efeito')}</dt><dd>{t(effects[approval.risk] ?? 'Efeito desconhecido; revise antes de aprovar.')}</dd></div>
    </dl>
    <Preview name={approval.name} args={approval.arguments} />
    <div className="approval-actions">
      <button type="button" className="touch-target secondary-button" disabled={disabled} onClick={() => onResolve(false)}>{t('Negar')}</button>
      <button type="button" className="touch-target primary-button" disabled={disabled} onKeyDown={ignoreEnter} onClick={() => onResolve(true)}>{t('Aprovar')}</button>
    </div>
  </section>
}
