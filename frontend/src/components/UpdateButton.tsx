import { useCallback, useEffect, useState } from 'react'
import { ArrowUpCircle, LoaderCircle } from 'lucide-react'
import type { Backend, UpdateInfo, UpdateState } from '../lib/backend'
import { openExternal } from '../lib/desktop'
import { useT } from '../i18n'

const recheckEvery = 6 * 60 * 60 * 1000
const rememberFor = 10 * 60 * 1000
// Switching between Casual and Professional mounts the button again; GitHub is not asked again for that.
const lastCheck = new WeakMap<Backend, { at: number; info: UpdateInfo }>()

/** What the app knows about a newer version: asked at start and every few hours, and whatever the install reports. */
export function useAppUpdate(backend?: Backend) {
  const [info, setInfo] = useState<UpdateInfo>()
  const [state, setState] = useState<UpdateState>()
  useEffect(() => {
    if (!backend) return
    let live = true
    const check = (force: boolean) => {
      const known = lastCheck.get(backend)
      if (!force && known && Date.now() - known.at < rememberFor) { setInfo(known.info); return }
      backend.checkForUpdate().then(found => { lastCheck.set(backend, { at: Date.now(), info: found }); if (live) setInfo(found) }, () => undefined)
    }
    check(false)
    const timer = setInterval(() => check(true), recheckEvery)
    const stop = backend.onUpdateState(next => { if (live) setState(next) })
    return () => { live = false; clearInterval(timer); stop() }
  }, [backend])
  const install = useCallback(async () => {
    if (!backend) return
    setState({ phase: 'downloading', percent: 0, errorCode: '' })
    try { await backend.installUpdate() } catch { setState({ phase: 'failed', percent: 0, errorCode: 'update_failed' }) }
  }, [backend])
  return { info, state, install }
}

/** Beside the logo when a newer version is published: one click downloads, installs and reopens the app. */
export function UpdateButton({ backend }: { backend?: Backend }) {
  const t = useT()
  const { info, state, install } = useAppUpdate(backend)
  if (!info?.available) return null
  const label = `v${info.version}`
  const working = state && state.phase !== 'failed'
  const download = () => void openExternal(info.pageUrl)
  const failure = state?.phase === 'failed' ? state.errorCode : ''
  const reason = failure === 'update_checksum' ? t('O arquivo baixado não confere com o publicado, então nada foi instalado.')
    : failure === 'update_not_writable' ? t('O app está numa pasta que você não pode alterar. Baixe a versão e instale por cima.')
    : failure === 'update_not_installable' ? t('Esta cópia do app não se atualiza sozinha. Baixe a versão e instale.')
    : t('Não foi possível atualizar agora.')
  const progress = !working ? '' : state.phase === 'downloading' ? t('Baixando {percent}%', { percent: state.percent })
    : state.phase === 'installing' ? t('Instalando…') : t('Reiniciando…')

  return <div className="update-slot">
    {working
      ? <span className="update-pill is-working" role="status"><LoaderCircle className="qa-spin" aria-hidden="true" /><span className="update-pill-text">{progress}</span></span>
      : info.canInstall
        ? <button type="button" className="update-pill" onClick={() => void install()} title={t('Baixar e instalar a versão {version} e reabrir o app', { version: label })} aria-label={t('Atualizar para {version}', { version: label })}>
          <ArrowUpCircle aria-hidden="true" /><span className="update-pill-text">{t('Atualizar')} {label}</span>
        </button>
        : <button type="button" className="update-pill" onClick={download} title={t('Abrir a página da versão {version} para baixar', { version: label })} aria-label={t('Baixar {version}', { version: label })}>
          <ArrowUpCircle aria-hidden="true" /><span className="update-pill-text">{t('Baixar')} {label}</span>
        </button>}
    {failure && <div className="update-failure" role="alert"><span>{reason}</span><button type="button" className="text-button" onClick={download}>{t('Baixar a versão')}</button></div>}
  </div>
}
