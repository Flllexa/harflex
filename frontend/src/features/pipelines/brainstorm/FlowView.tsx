import { useEffect, useId, useRef, useState, type PointerEvent } from 'react'
import { Code2, Maximize2, Minus, Plus, Sparkles, Workflow, X } from 'lucide-react'
import { useT } from '../../../i18n'

type Rendered = { state: 'loading' } | { state: 'ready'; svg: string } | { state: 'invalid'; error: string }

const zoomSteps = [0.5, 0.75, 1, 1.25, 1.5, 2]

/** Mermaid is large and only this screen needs it: it loads the first time a flow is shown, in strict mode (labels are
 * text, never HTML, and no click handlers). */
async function loadMermaid() {
  const { default: mermaid } = await import('mermaid')
  const light = document.documentElement.dataset.theme === 'light'
  mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: light ? 'default' : 'dark', fontFamily: 'inherit' })
  return mermaid
}

function useRenderedFlow(source: string) {
  const id = `flow-${useId().replace(/[^a-zA-Z0-9]/g, '')}`
  const [rendered, setRendered] = useState<Rendered>({ state: 'loading' })
  useEffect(() => {
    let live = true
    setRendered({ state: 'loading' })
    void (async () => {
      try {
        const mermaid = await loadMermaid()
        await mermaid.parse(source)
        const { svg } = await mermaid.render(id, source)
        if (live) setRendered({ state: 'ready', svg })
      } catch (failure) {
        if (live) setRendered({ state: 'invalid', error: failure instanceof Error ? failure.message : String(failure) })
      }
    })()
    return () => { live = false }
  }, [id, source])
  return rendered
}

/** A canvas the person can drag to move around a flow larger than its box. */
function Canvas({ svg, zoom }: { svg: string; zoom: number }) {
  const canvas = useRef<HTMLDivElement>(null), drag = useRef<{ x: number; y: number; left: number; top: number }>()
  const start = (event: PointerEvent<HTMLDivElement>) => {
    const box = canvas.current
    if (!box) return
    drag.current = { x: event.clientX, y: event.clientY, left: box.scrollLeft, top: box.scrollTop }
    box.setPointerCapture?.(event.pointerId)
  }
  const move = (event: PointerEvent<HTMLDivElement>) => {
    const box = canvas.current, from = drag.current
    if (!box || !from) return
    box.scrollLeft = from.left - (event.clientX - from.x); box.scrollTop = from.top - (event.clientY - from.y)
  }
  return <div ref={canvas} className="flow-canvas" data-zoom={String(zoom)} onPointerDown={start} onPointerMove={move} onPointerUp={() => { drag.current = undefined }}>
    <div className="flow-svg" style={{ zoom }} dangerouslySetInnerHTML={{ __html: svg }} />
  </div>
}

/** The application flow in Mermaid: rendered, with zoom and a larger view, or its parser error and a way to have the AI
 * fix it. */
export function FlowView({ source, onAskFix }: { source: string; onAskFix(error: string): void }) {
  const t = useT()
  const rendered = useRenderedFlow(source)
  const [code, setCode] = useState(false), [zoom, setZoom] = useState(1), [large, setLarge] = useState(false)
  const step = (direction: 1 | -1) => setZoom(current => zoomSteps[Math.min(zoomSteps.length - 1, Math.max(0, zoomSteps.indexOf(current) + direction))])
  return <div className="flow-view">
    <div className="flow-view-tools">
      {rendered.state === 'ready' && !code && <>
        <button type="button" className="touch-target" aria-label={t('Afastar')} onClick={() => step(-1)} disabled={zoom === zoomSteps[0]}><Minus aria-hidden="true" /></button>
        <button type="button" className="touch-target" aria-label={t('Aproximar')} onClick={() => step(1)} disabled={zoom === zoomSteps[zoomSteps.length - 1]}><Plus aria-hidden="true" /></button>
        <button type="button" className="touch-target" onClick={() => setLarge(true)}><Maximize2 aria-hidden="true" />{t('Ampliar')}</button>
      </>}
      <button type="button" className="touch-target" onClick={() => setCode(value => !value)}>{code ? <><Workflow aria-hidden="true" />{t('Ver fluxo')}</> : <><Code2 aria-hidden="true" />{t('Ver código')}</>}</button>
    </div>
    {code ? <pre className="mono flow-source">{source}</pre>
      : rendered.state === 'loading' ? <p className="muted flow-loading" role="status">{t('Desenhando o fluxo…')}</p>
      : rendered.state === 'invalid' ? <div className="flow-invalid" role="alert"><p>{t('O fluxo não pôde ser desenhado: {error}', { error: rendered.error })}</p><button type="button" className="touch-target secondary-button" onClick={() => onAskFix(rendered.error)}><Sparkles aria-hidden="true" />{t('Pedir correção à IA')}</button></div>
      : <Canvas svg={rendered.svg} zoom={zoom} />}
    {large && rendered.state === 'ready' && <div className="brainstorm-dialog" role="dialog" aria-modal="true" aria-label={t('Fluxo')} onKeyDown={event => { if (event.key === 'Escape') setLarge(false) }}>
      <header><strong>{t('Fluxo')}</strong><span className="muted">{t('Arraste para mover.')}</span><button type="button" className="touch-target secondary-button" onClick={() => setLarge(false)} autoFocus><X aria-hidden="true" />{t('Fechar')}</button></header>
      <Canvas svg={rendered.svg} zoom={Math.max(zoom, 1.5)} />
    </div>}
  </div>
}
