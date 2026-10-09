import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Modal } from '../../components/Modal'
import { parse, type Backend } from '../../lib/backend'
import { useT } from '../../i18n'

export function AuditDialog({ backend, sessionId, onClose }: { backend: Backend; sessionId: string; onClose: () => void }) {
  const t = useT()
  const [destination, setDestination] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const [saved, setSaved] = useState<string>()
  const firstField = useRef<HTMLInputElement>(null)
  const mounted = useRef(false)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  let valid = false
  try { parse.auditPath(destination); valid = true } catch { /* Invalid paths stay local. */ }
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!valid || pending) return
    setPending(true); setError(undefined); setSaved(undefined)
    try {
      const result = await backend.exportAudit(sessionId, destination)
      if (mounted.current) setSaved(result)
    } catch {
      if (mounted.current) setError(t('Não foi possível exportar a auditoria. Verifique o caminho e a permissão de escrita e tente novamente.'))
    } finally { if (mounted.current) setPending(false) }
  }
  return <Modal title={t('Exportar auditoria')} titleId="audit-title" initialFocus={firstField} onClose={onClose}>
    <p className="muted dialog-copy">{t('Salve o histórico de eventos da sessão em um arquivo JSONL, com um evento por linha. A exportação respeita o limite de leitura do histórico.')}</p>
    <form className="form-grid" onSubmit={submit}>
      <label>{t('Caminho de destino')}<input ref={firstField} className="mono" required value={destination} placeholder={t('Ex.: /caminho/auditoria.jsonl')} onChange={event => setDestination(event.target.value)} autoComplete="off" spellCheck={false} /></label>
      {error && <p className="form-error" role="alert">{error}</p>}
      {saved && <p className="form-success audit-result" role="status">{t('Auditoria JSONL exportada: {path}', { path: saved })}</p>}
      <div className="dialog-actions"><button type="button" className="touch-target secondary-button" onClick={onClose}>{t('Fechar')}</button><button type="submit" className="touch-target primary-button" disabled={pending || !valid}>{pending ? t('Exportando') : t('Exportar')}</button></div>
    </form>
  </Modal>
}
