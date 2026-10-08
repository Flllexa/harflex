import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { Markdown } from './Markdown'

const desktop = vi.hoisted(() => ({ openExternal: vi.fn(async () => true), copyText: vi.fn(async () => true) }))
vi.mock('../lib/desktop', () => desktop)

afterEach(() => { cleanup(); desktop.openExternal.mockClear(); desktop.copyText.mockClear() })

it('renders an assistant answer as real elements', () => {
  const { container } = render(<Markdown text={'## Resumo\n\nUse **negrito**, *itálico* e `código`.\n\n- um\n- dois\n\n1. primeiro\n2. segundo\n\n> nota\n\n| A | B |\n| - | - |\n| 1 | 2 |'} />)
  expect(screen.getByRole('heading', { level: 4, name: 'Resumo' })).toBeInTheDocument()
  expect(container.querySelector('strong')).toHaveTextContent('negrito')
  expect(container.querySelector('em')).toHaveTextContent('itálico')
  expect(container.querySelector('.md-inline-code')).toHaveTextContent('código')
  expect(screen.getAllByRole('list')).toHaveLength(2)
  expect(container.querySelector('blockquote')).toHaveTextContent('nota')
  const table = screen.getByRole('region', { name: 'Tabela' })
  expect(within(table).getAllByRole('columnheader').map(cell => cell.textContent)).toEqual(['A', 'B'])
  expect(within(table).getAllByRole('cell').map(cell => cell.textContent)).toEqual(['1', '2'])
})

it('never turns model text into live HTML or remote loads', () => {
  const { container } = render(<Markdown text={'<img src=x onerror="alert(1)"> <script>alert(1)</script>\n\n![logo](https://example.com/a.png)\n\n[ruim](javascript:alert(1))'} />)
  expect(container.querySelector('img, script')).toBeNull()
  expect(container.querySelector('a')).toBeNull()
  expect(container).toHaveTextContent('<script>alert(1)</script>')
  expect(container).toHaveTextContent('[imagem: logo]')
})

it('opens links in the system browser instead of navigating the app', async () => {
  const user = userEvent.setup()
  render(<Markdown text="Veja [a documentação](https://example.com/docs) e https://example.com/x." />)
  const docs = screen.getByRole('link', { name: 'a documentação' })
  expect(docs).toHaveAttribute('href', 'https://example.com/docs')
  await user.click(docs)
  await user.click(screen.getByRole('link', { name: 'https://example.com/x' }))
  expect(desktop.openExternal.mock.calls).toEqual([['https://example.com/docs'], ['https://example.com/x']])
})

it('shows code blocks with their language and a copy action that confirms', async () => {
  const user = userEvent.setup()
  render(<Markdown text={'```go\nfmt.Println("oi")\n```'} />)
  const block = screen.getByRole('figure')
  expect(within(block).getByText('go')).toBeInTheDocument()
  expect(within(block).getByLabelText('Código go')).toHaveTextContent('fmt.Println("oi")')
  await user.click(within(block).getByRole('button', { name: 'Copiar código go' }))
  expect(desktop.copyText).toHaveBeenCalledWith('fmt.Println("oi")')
  expect(await within(block).findByRole('button', { name: 'Copiado' })).toBeInTheDocument()
})

it('keeps rendering while an answer is still streaming', () => {
  const { rerender, container } = render(<Markdown text={'```sh\necho um'} />)
  expect(container.querySelector('pre')).toHaveTextContent('echo um')
  rerender(<Markdown text={'```sh\necho um\n```\n\nFim **parcial'} />)
  expect(container.querySelectorAll('pre')).toHaveLength(1)
  expect(container).toHaveTextContent('Fim **parcial')
})
