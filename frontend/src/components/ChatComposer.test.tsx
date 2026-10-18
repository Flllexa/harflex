import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ChatComposer, type ComposerProps } from './ChatComposer'

afterEach(cleanup)

function Harness(props: Partial<ComposerProps>) {
  const [value, setValue] = useState(props.value ?? '')
  return <ChatComposer id="test-composer" label="Mensagem" canSend={value.trim() !== ''} onSend={() => undefined} {...props} value={value} onChange={next => { setValue(next); props.onChange?.(next) }} />
}

describe('ChatComposer', () => {
  it('sends with Enter and keeps Shift+Enter for a new line', async () => {
    const onSend = vi.fn()
    render(<Harness onSend={onSend} />)
    const box = screen.getByLabelText('Mensagem')
    await userEvent.type(box, 'Olá{Shift>}{Enter}{/Shift}mundo')
    expect(box).toHaveValue('Olá\nmundo')
    expect(onSend).not.toHaveBeenCalled()
    await userEvent.type(box, '{Enter}')
    expect(onSend).toHaveBeenCalledTimes(1)
  })

  it('queues with Cmd+Enter while the AI answers, and offers to stop instead of sending', async () => {
    const onQueue = vi.fn(), onStop = vi.fn()
    render(<Harness running canSend={false} onQueue={onQueue} onStop={onStop} />)
    const box = screen.getByLabelText('Mensagem')
    expect(box).toHaveAttribute('placeholder', '↵ para enviar, ⌘↵ para adicionar à fila')
    await userEvent.type(box, 'Depois disso, rode os testes')
    fireEvent.keyDown(box, { key: 'Enter', metaKey: true })
    expect(onQueue).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('button', { name: 'Enviar' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Cancelar execução' }))
    expect(onStop).toHaveBeenCalledTimes(1)
  })

  it('shows what is queued and lets it be taken back', async () => {
    const onUnqueue = vi.fn()
    render(<Harness queued={['Rode os testes', 'Abra o PR']} onUnqueue={onUnqueue} />)
    const queue = screen.getByRole('list', { name: 'Na fila · 2' })
    expect(queue).toHaveTextContent('Rode os testes')
    await userEvent.click(screen.getAllByRole('button', { name: /Tirar da fila/ })[1])
    expect(onUnqueue).toHaveBeenCalledWith(1)
  })

  it('attaches files with the plus button and shows them as removable chips', async () => {
    const onAttach = vi.fn(), onRemoveAttachment = vi.fn()
    render(<Harness onAttach={onAttach} onRemoveAttachment={onRemoveAttachment} attachments={[{ id: 'a1', name: 'notas.md', kind: 'text', size: 12 }]} />)
    const file = new File(['# Notas'], 'notas.md', { type: 'text/markdown' })
    expect(screen.getByRole('button', { name: 'Anexar arquivos' })).toBeEnabled()
    await userEvent.upload(screen.getByLabelText('Arquivos para anexar'), file)
    expect(onAttach).toHaveBeenCalledWith([file])
    await userEvent.click(screen.getByRole('button', { name: 'Remover notas.md' }))
    expect(onRemoveAttachment).toHaveBeenCalledWith('a1')
  })

  it('puts permissions under the box on the left and the model on the right', () => {
    render(<Harness footerStart={<span>Ignorar permissões</span>} footerEnd={<span>Opus 5.5 Alto</span>} />)
    const footer = screen.getByText('Ignorar permissões').closest('.chat-composer-footer')
    expect(footer).toContainElement(screen.getByText('Opus 5.5 Alto'))
  })
})
