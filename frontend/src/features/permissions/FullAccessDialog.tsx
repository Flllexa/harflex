import { useRef } from 'react'
import { Modal } from '../../components/Modal'
import { useT } from '../../i18n'

type Props = { pending: boolean; error?: string; onConfirm: () => void; onClose: () => void }

/** Turning approvals off is a project-wide choice, so it is confirmed once, in words, before it is saved. */
export function FullAccessDialog({ pending, error, onConfirm, onClose }: Props) {
  const t = useT()
  const cancel = useRef<HTMLButtonElement>(null)
  return <Modal title={t('Ativar acesso total neste projeto?')} titleId="full-access-title" initialFocus={cancel} onClose={() => { if (!pending) onClose() }}>
    <p className="dialog-copy">{t('O agente passa a criar e editar arquivos, executar comandos e chamar ferramentas de rede (como GitHub e Bitbucket) ')}<strong>{t('sem pedir a sua aprovação')}</strong>{t(', em todas as conversas deste projeto.')}</p>
    <ul className="muted dialog-list">
      <li>{t('As ferramentas de arquivo continuam limitadas à pasta do projeto, mas o terminal e os servidores MCP locais rodam com os seus privilégios, sem isolamento: podem ler e alterar qualquer coisa que você possa.')}</li>
      <li>{t('Vale já para as conversas abertas e também para agendamentos, workflows e subagentes do projeto, que rodam sem ninguém por perto.')}</li>
      <li>{t('Pedidos que já estavam esperando continuam esperando a sua resposta.')}</li>
      <li>{t('Você volta a ser consultado escolhendo outra opção no mesmo menu.')}</li>
    </ul>
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="dialog-actions">
      <button ref={cancel} type="button" className="touch-target secondary-button" disabled={pending} onClick={onClose}>{t('Cancelar')}</button>
      <button type="button" className="touch-target danger-button" disabled={pending} onClick={onConfirm}>{pending ? t('Ativando…') : t('Ativar acesso total')}</button>
    </div>
  </Modal>
}
