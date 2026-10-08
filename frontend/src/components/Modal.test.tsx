import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useRef } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { IonPicker } from './IonPicker'
import { Modal } from './Modal'

afterEach(cleanup)

it('closes the picker before the dialog while keeping focus inside the modal layer', async () => {
  const user = userEvent.setup()
  const onClose = vi.fn()
  function Example() {
    const first = useRef<HTMLInputElement>(null)
    return <Modal title="Editar provedor" titleId="title" initialFocus={first} onClose={onClose}>
      <input ref={first} aria-label="Nome" />
      <IonPicker id="dialog-type" label="Tipo de provedor" value="generic" onChange={() => undefined}
        options={[{ value: 'generic', label: 'Genérico' }, { value: 'lm_studio', label: 'LM Studio' }]} />
    </Modal>
  }
  const { container } = render(<Example />)
  const dialog = screen.getByRole('dialog', { name: 'Editar provedor' })
  await user.click(screen.getByTestId('picker-dialog-type').querySelector('button')!)
  const listbox = screen.getByRole('listbox')
  const overlayHost = dialog.parentElement?.querySelector('.dialog-overlay-host')
  expect(overlayHost).toContainElement(listbox)
  expect(dialog).not.toContainElement(listbox)
  expect(container.inert).toBe(true)
  expect(document.activeElement && dialog.parentElement?.contains(document.activeElement)).toBe(true)
  await user.keyboard('{Escape}')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  expect(onClose).not.toHaveBeenCalled()
  await user.keyboard('{Escape}')
  expect(onClose).toHaveBeenCalledOnce()
})

it('keeps Tab away from the inert shell while the picker is open', async () => {
  const user = userEvent.setup()
  function Example() {
    const first = useRef<HTMLInputElement>(null)
    return <Modal title="Editar provedor" titleId="tab-title" initialFocus={first} onClose={() => undefined}>
      <input ref={first} aria-label="Nome" />
      <IonPicker id="tab-type" label="Tipo de provedor" value="generic" onChange={() => undefined}
        options={[{ value: 'generic', label: 'Genérico' }, { value: 'lm_studio', label: 'LM Studio' }]} />
    </Modal>
  }
  const { container } = render(<><button type="button">Shell</button><Example /></>)
  const layer = screen.getByRole('dialog').parentElement
  await user.click(screen.getByTestId('picker-tab-type').querySelector('button')!)
  expect(screen.getByRole('listbox')).toBeInTheDocument()
  await user.tab()
  expect(container.inert).toBe(true)
  expect(layer?.contains(document.activeElement)).toBe(true)
  expect(document.activeElement).not.toHaveTextContent('Shell')
})
