import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'
import { DesignView, sandboxedDocument } from './DesignView'

afterEach(cleanup)

const csp = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:">`
const screens = [
  { name: 'Lista de contratos', purpose: 'Ver o que vence', html: '<!doctype html><html><head><title>Lista</title></head><body><h1>Lista</h1></body></html>' },
  { name: 'Detalhe', purpose: 'Ver um contrato', html: '<html><body><h1>Detalhe</h1></body></html>' },
]

it('puts the content security policy first in every document', () => {
  expect(sandboxedDocument(screens[0].html)).toBe(`<!doctype html><html><head>${csp}<title>Lista</title></head><body><h1>Lista</h1></body></html>`)
  expect(sandboxedDocument(screens[1].html)).toBe(`<html><head>${csp}</head><body><h1>Detalhe</h1></body></html>`)
  expect(sandboxedDocument('<p>solto</p>')).toBe(`<!doctype html><html><head>${csp}</head><body><p>solto</p></body></html>`)
})

it('shows each screen in a frame that cannot run code', () => {
  render(<DesignView screens={screens} />)
  const frame = screen.getByTitle('Lista de contratos')
  expect(frame.tagName).toBe('IFRAME')
  expect(frame).toHaveAttribute('sandbox', '')
  expect(frame.getAttribute('srcdoc')).toContain(csp)
  expect(screen.getByText('Ver o que vence')).toBeInTheDocument()
})

it('switches screens with tabs and the frame width with Desktop and Mobile', async () => {
  const user = userEvent.setup()
  render(<DesignView screens={screens} />)
  const tabs = screen.getByRole('tablist', { name: 'Telas' })
  await user.click(within(tabs).getByRole('tab', { name: 'Detalhe' }))
  expect(within(tabs).getByRole('tab', { name: 'Detalhe' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByTitle('Detalhe').getAttribute('srcdoc')).toContain('<h1>Detalhe</h1>')
  expect(screen.getByTitle('Detalhe').closest('.design-frame')).toHaveAttribute('data-device', 'desktop')
  await user.click(screen.getByRole('button', { name: 'Mobile' }))
  expect(screen.getByRole('button', { name: 'Mobile' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByTitle('Detalhe').closest('.design-frame')).toHaveAttribute('data-device', 'mobile')
})

it('opens the screen at full size', async () => {
  const user = userEvent.setup()
  render(<DesignView screens={screens} />)
  await user.click(screen.getByRole('button', { name: 'Abrir em tamanho cheio' }))
  const dialog = screen.getByRole('dialog', { name: 'Lista de contratos' })
  expect(within(dialog).getByTitle('Lista de contratos')).toHaveAttribute('sandbox', '')
  await user.click(within(dialog).getByRole('button', { name: 'Fechar' }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})
