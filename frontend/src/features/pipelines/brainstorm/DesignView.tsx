import { useState } from 'react'
import { Maximize2, Monitor, Smartphone, X } from 'lucide-react'
import { useT } from '../../../i18n'
import type { BrainstormContent } from '../../../lib/pipelineDesign'

type Screens = NonNullable<BrainstormContent['design']>['screens']

const policy = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:">`

/** The screen's HTML with a content security policy as the first thing in its head: nothing loads from outside, nothing
 * runs. The frame's empty sandbox already forbids scripts; the policy also stops fetches the markup might ask for. */
export function sandboxedDocument(html: string): string {
  const head = /<head(\s[^>]*)?>/i.exec(html)
  if (head) return html.slice(0, head.index + head[0].length) + policy + html.slice(head.index + head[0].length)
  const root = /<html(\s[^>]*)?>/i.exec(html)
  if (root) return html.slice(0, root.index + root[0].length) + `<head>${policy}</head>` + html.slice(root.index + root[0].length)
  return `<!doctype html><html><head>${policy}</head><body>${html}</body></html>`
}

function Frame({ name, html }: { name: string; html: string }) {
  return <iframe title={name} sandbox="" srcDoc={sandboxedDocument(html)} referrerPolicy="no-referrer" />
}

/** The proposed screens: one tab each, at desktop or mobile width, and full size on request. */
export function DesignView({ screens }: { screens: Screens }) {
  const t = useT()
  const [selected, setSelected] = useState(0), [device, setDevice] = useState<'desktop' | 'mobile'>('desktop'), [full, setFull] = useState(false)
  const current = screens[Math.min(selected, screens.length - 1)]
  if (!current) return null
  return <div className="design-view">
    <div className="design-view-tools">
      <div role="tablist" aria-label={t('Telas')} className="design-view-tabs">
        {screens.map((item, index) => <button key={`${index}-${item.name}`} type="button" role="tab" className="touch-target" aria-selected={index === selected} onClick={() => setSelected(index)}>{item.name}</button>)}
      </div>
      <div className="design-view-device" role="group" aria-label={t('Largura do quadro')}>
        <button type="button" className="touch-target" aria-pressed={device === 'desktop'} onClick={() => setDevice('desktop')}><Monitor aria-hidden="true" />Desktop</button>
        <button type="button" className="touch-target" aria-pressed={device === 'mobile'} onClick={() => setDevice('mobile')}><Smartphone aria-hidden="true" />Mobile</button>
        <button type="button" className="touch-target" onClick={() => setFull(true)}><Maximize2 aria-hidden="true" />{t('Abrir em tamanho cheio')}</button>
      </div>
    </div>
    {current.purpose && <p className="muted design-view-purpose">{current.purpose}</p>}
    <div className="design-frame" data-device={device}><Frame name={current.name} html={current.html} /></div>
    {full && <div className="brainstorm-dialog" role="dialog" aria-modal="true" aria-label={current.name} onKeyDown={event => { if (event.key === 'Escape') setFull(false) }}>
      <header><strong>{current.name}</strong><button type="button" className="touch-target secondary-button" onClick={() => setFull(false)} autoFocus><X aria-hidden="true" />{t('Fechar')}</button></header>
      <div className="design-frame design-frame-full" data-device={device}><Frame name={current.name} html={current.html} /></div>
    </div>}
  </div>
}
