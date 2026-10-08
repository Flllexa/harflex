import { useEffect, useRef, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { UNSAFE_PortalProvider } from '@react-aria/overlays'
import { X } from 'lucide-react'

/** Shared modal boundary: traps focus, isolates the shell and restores its trigger. */
export function Modal({ title, titleId, initialFocus, onClose, children }: {
  title: string; titleId: string; initialFocus: RefObject<HTMLElement>; onClose: () => void; children: ReactNode
}) {
  const layer = useRef<HTMLDivElement>(null)
  const dialog = useRef<HTMLDivElement>(null)
  const overlayHost = useRef<HTMLDivElement>(null)
  const close = useRef(onClose)
  close.current = onClose
  useEffect(() => {
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const background = Array.from(document.body.children).filter((element): element is HTMLElement => element instanceof HTMLElement && element !== layer.current)
      .map(element => ({ element, inert: !!element.inert }))
    background.forEach(({ element }) => { element.inert = true })
    initialFocus.current?.focus()
    const keydown = (event: globalThis.KeyboardEvent) => {
      const pickerOpen = !!layer.current?.querySelector('[data-ion-picker-popover]')
      if (event.key === 'Escape') {
        if (pickerOpen) return
        event.preventDefault(); event.stopImmediatePropagation(); close.current(); return
      }
      if (event.key !== 'Tab' || pickerOpen) return
      const controls = Array.from(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), [href], [tabindex="0"]') ?? [])
        .filter(element => element.getClientRects().length > 0)
      const first = controls[0], last = controls[controls.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
      event.stopImmediatePropagation()
    }
    const focus = (event: FocusEvent) => { if (!layer.current?.contains(event.target as Node)) initialFocus.current?.focus() }
    document.addEventListener('keydown', keydown, true)
    document.addEventListener('focusin', focus)
    return () => {
      document.removeEventListener('keydown', keydown, true)
      document.removeEventListener('focusin', focus)
      background.forEach(({ element, inert }) => { element.inert = inert })
      if (trigger?.isConnected) trigger.focus()
    }
  }, [initialFocus])
  return createPortal(<div ref={layer} className="dialog-layer">
    <div className="drawer-scrim" aria-hidden="true" onClick={onClose} />
    <div ref={dialog} className="dialog" role="dialog" aria-modal="true" aria-labelledby={titleId}>
      <div className="panel-heading"><h2 id={titleId}>{title}</h2><button type="button" className="touch-target icon-button" aria-label="Fechar" onClick={onClose}><X aria-hidden="true" /></button></div>
      <UNSAFE_PortalProvider getContainer={() => overlayHost.current}>{children}</UNSAFE_PortalProvider>
    </div>
    <div ref={overlayHost} className="dialog-overlay-host" />
  </div>, document.body)
}
