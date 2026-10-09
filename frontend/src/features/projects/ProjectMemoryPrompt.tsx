import { useRef, useState } from 'react'
import { Brain } from 'lucide-react'
import { Modal } from '../../components/Modal'
import './projectMemory.css'
import { errorMessage, type Backend } from '../../lib/backend'
import { useT } from '../../i18n'

type Props = { backend: Backend; workspaceId: string; projectName: string; onClose: (answer: 'read' | 'declined') => void }

/** Asks, the first time a project is opened, whether the AI should read it into the project memory. */
export function ProjectMemoryPrompt({ backend, workspaceId, projectName, onClose }: Props) {
  const t = useT()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const later = useRef<HTMLButtonElement>(null)

  async function answer(read: boolean) {
    if (pending) return
    setPending(true)
    setError('')
    try {
      if (read) await backend.refreshProjectMemory(workspaceId)
      else await backend.declineProjectMemory(workspaceId)
      onClose(read ? 'read' : 'declined')
    } catch (failure) {
      setError(errorMessage(failure))
      setPending(false)
    }
  }

  return <Modal title={t('Ler o projeto com a IA?')} titleId="project-memory-prompt-title" initialFocus={later} onClose={() => { if (!pending) void answer(false) }}>
    <div className="project-memory-prompt">
      <span className="project-memory-prompt-icon"><Brain aria-hidden="true" /></span>
      <p className="dialog-copy">{t('A IA lê os documentos, manifestos e repositórios de')} <strong>{projectName}</strong> {t('e guarda na memória do projeto um resumo das tecnologias, domínios, repositórios e serviços que ele já tem. Assim, Discovery, SPEC, Plan e os chats já sabem disso sem você precisar explicar.')}</p>
    </div>
    <ul className="project-memory-prompt-facts muted">
      <li>{t('Leva cerca de um minuto e usa a IA da fase Discovery (ou o padrão das Configurações).')}</li>
      <li>{t('Segredos como')} <code>.env</code>{t(', chaves e certificados nunca são lidos.')}</li>
      <li>{t('Você pode ver, editar ou ler de novo a qualquer momento na página Memória.')}</li>
    </ul>
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="dialog-actions">
      <button ref={later} type="button" className="touch-target secondary-button" disabled={pending} onClick={() => void answer(false)}>{t('Agora não')}</button>
      <button type="button" className="touch-target primary-button" disabled={pending} onClick={() => void answer(true)}><Brain aria-hidden="true" />{pending ? t('Começando…') : t('Ler o projeto')}</button>
    </div>
  </Modal>
}
