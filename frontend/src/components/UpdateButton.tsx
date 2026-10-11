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

/** Beside the logo when a newer version is published: one click downloads, installs and reopens the app. Only an icon,
 *  so it fits the narrow sidebar; the version and what it does are in its name and tooltip. */
export function UpdateButton({ backend }: { backend?: Backend }) {
  const t = useT()
  const { info, state, install } = useAppUpdate(backend)
  if (!info?.available) return null
  const label = `v${info.version}`
  const working = state && state.phase !== 'failed'
  const download = () => void openExternal(info.pageUrl)
  const failure = state?.phase === 'failed' ? state.errorCode : ''
  const reason = failure === 'update_checksum' ? t('O arquivo baixado não confere com o publicado, então nada foi instalado.')
    : failure === 'update_not_writable' ? t('Não há uma pasta de aplicativos em que você possa instalar. Tente de novo ou baixe a versão.')
    : failure === 'update_not_installable' ? t('Esta cópia do app não se atualiza sozinha. Baixe a versão e instale.')
    : t('Não foi possível atualizar agora.')
  const progress = !working ? '' : state.phase === 'downloading' ? t('Baixando {percent}%', { percent: state.percent })
    : state.phase === 'installing' ? t('Instalando…') : t('Reiniciando…')
  const title = info.canInstall ? t('Atualizar para {version}: baixa, instala e reabre o app', { version: label }) : t('Abrir a página da versão {version} para baixar', { version: label })

  return <div className="update-slot">
    {working
      ? <span className="update-icon is-working" role="status" aria-label={progress} title={progress} style={{ ['--done' as string]: `${state.phase === 'downloading' ? state.percent : 100}%` }}>
        <LoaderCircle className="qa-spin" aria-hidden="true" />
      </span>
      : <button type="button" className={`update-icon${failure ? ' is-failed' : ''}`} onClick={info.canInstall ? () => void install() : download} title={title} aria-label={info.canInstall ? t('Atualizar para {version}', { version: label }) : t('Baixar {version}', { version: label })}>
        <ArrowUpCircle aria-hidden="true" />
      </button>}
    {failure && <div className="update-failure" role="alert"><span>{reason}</span><div className="update-failure-actions">{info.canInstall && <button type="button" className="touch-target primary-button" onClick={() => void install()}>{t('Tentar de novo')}</button>}<button type="button" className="text-button" onClick={download}>{t('Baixar a versão')}</button></div></div>}
  </div>
}
