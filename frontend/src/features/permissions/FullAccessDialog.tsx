import { useRef } from 'react'
import { Modal } from '../../components/Modal'

type Props = { pending: boolean; error?: string; onConfirm: () => void; onClose: () => void }

/** Turning approvals off is a project-wide choice, so it is confirmed once, in words, before it is saved. */
export function FullAccessDialog({ pending, error, onConfirm, onClose }: Props) {
  const cancel = useRef<HTMLButtonElement>(null)
  return <Modal title="Ativar acesso total neste projeto?" titleId="full-access-title" initialFocus={cancel} onClose={() => { if (!pending) onClose() }}>
    <p className="dialog-copy">O agente passa a criar e editar arquivos, executar comandos e chamar ferramentas de rede (como GitHub e Bitbucket) <strong>sem pedir a sua aprovação</strong>, em todas as conversas deste projeto.</p>
    <ul className="muted dialog-list">
      <li>As ferramentas de arquivo continuam limitadas à pasta do projeto, mas o terminal e os servidores MCP locais rodam com os seus privilégios, sem isolamento: podem ler e alterar qualquer coisa que você possa.</li>
      <li>Vale já para as conversas abertas e também para agendamentos, workflows e subagentes do projeto, que rodam sem ninguém por perto.</li>
      <li>Pedidos que já estavam esperando continuam esperando a sua resposta.</li>
      <li>Você volta a ser consultado escolhendo outra opção no mesmo menu.</li>
    </ul>
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="dialog-actions">
      <button ref={cancel} type="button" className="touch-target secondary-button" disabled={pending} onClick={onClose}>Cancelar</button>
      <button type="button" className="touch-target danger-button" disabled={pending} onClick={onConfirm}>{pending ? 'Ativando…' : 'Ativar acesso total'}</button>
    </div>
  </Modal>
}
