import { useState } from 'react'
import { Plus, X } from 'lucide-react'
import { useT } from '../i18n'

/**
 * Creation forms stay open while a page has nothing to show, then step aside for the list
 * once items exist. An explicit choice (or starting an edit) always wins over that default.
 */
export function useCreateForm(ready: boolean, itemCount: number) {
  const [choice, setChoice] = useState<boolean>()
  const open = choice ?? (ready ? itemCount === 0 : true)
  return { open, setOpen: setChoice, collapsible: ready && itemCount > 0 }
}

export function CreateToggle({ open, onToggle, label }: { open: boolean; onToggle: () => void; label: string }) {
  const t = useT()
  return <button type="button" className="touch-target secondary-button" aria-expanded={open} onClick={onToggle}>
    {open ? <X aria-hidden="true" /> : <Plus aria-hidden="true" />}{open ? t('Fechar formulário') : label}
  </button>
}
