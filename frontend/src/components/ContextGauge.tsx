import { useState } from 'react'
import { localeTag, useT } from '../i18n'

const radius = 8
const circumference = 2 * Math.PI * radius

function thousands(value: number) {
  return new Intl.NumberFormat(localeTag(), { notation: 'compact', maximumFractionDigits: 0 }).format(value)
}

/** How full the conversation's context is, as a ring that fills; a click offers to compact it now. */
export function ContextGauge({ used, window, onCompact, disabled = false }: { used: number; window: number; onCompact: () => void; disabled?: boolean }) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const percent = window > 0 ? Math.min(100, Math.round(used / window * 100)) : 0
  const tone = percent >= 85 ? ' is-danger' : percent >= 70 ? ' is-warning' : ''
  const label = t('Contexto: {percent}% de {window} tokens', { percent, window: thousands(window) })
  return <span className="context-gauge-wrap">
    <button type="button" className={`context-gauge${tone}`} aria-label={label} title={label} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(current => !current)}>
      <svg viewBox="0 0 20 20" aria-hidden="true">
        <circle className="context-gauge-track" cx="10" cy="10" r={radius} />
        <circle className="context-gauge-fill" cx="10" cy="10" r={radius} strokeDasharray={circumference} strokeDashoffset={circumference * (1 - percent / 100)} />
      </svg>
    </button>
    {open && <div className="context-gauge-menu" role="menu" aria-label={label}>
      <p className="muted">{label}</p>
      <button type="button" role="menuitem" disabled={disabled} onClick={() => { setOpen(false); onCompact() }}>{t('Compactar agora')}</button>
    </div>}
  </span>
}
