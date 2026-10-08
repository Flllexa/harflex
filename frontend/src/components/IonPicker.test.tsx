import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { IonPicker } from './IonPicker'

afterEach(cleanup)

it('preserva valor vazio e impede escolher uma opção indisponível', async () => {
  const user = userEvent.setup()
  const changed = vi.fn()
  render(<IonPicker id="test-backend" label="Backend" value="" onChange={changed}
    options={[{ value: '', label: 'Escolha um backend' }, { value: 'local', label: 'Local' }, { value: 'off', label: 'Indisponível', disabled: true }]} required />)

  const root = screen.getByTestId('picker-test-backend')
  const trigger = within(root).getByRole('button')
  expect(trigger).toHaveAccessibleName(/Backend/)
  await user.click(trigger)
  expect(screen.getByRole('option', { name: 'Indisponível' })).toHaveAttribute('aria-disabled', 'true')
  await user.click(screen.getByRole('option', { name: 'Indisponível' }))
  expect(changed).not.toHaveBeenCalled()
  await user.click(screen.getByRole('option', { name: 'Local' }))
  expect(changed).toHaveBeenCalledOnce()
  expect(changed).toHaveBeenCalledWith('local')
})

it('filtra coleção extensa sem transformar pesquisa em escolha', async () => {
  const user = userEvent.setup()
  const changed = vi.fn()
  render(<IonPicker id="test-document" label="Documento" value="" onChange={changed} searchable
    options={[{ value: '', label: 'Todos os documentos' }, { value: 'a', label: 'docs/arquitetura.md' }, { value: 'b', label: 'docs/código.md' }]} />)

  const input = within(screen.getByTestId('picker-test-document')).getByRole('combobox', { name: 'Documento' })
  await user.type(input, 'codigo')
  expect(changed).not.toHaveBeenCalled()
  expect(screen.getByRole('option', { name: 'docs/código.md' })).toBeInTheDocument()
  expect(screen.queryByRole('option', { name: 'docs/arquitetura.md' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('option', { name: 'docs/código.md' }))
  expect(changed).toHaveBeenCalledWith('b')
})

it('abre uma lista pesquisável ao clicar no campo sem exigir digitação', async () => {
  const user = userEvent.setup()
  render(<IonPicker id="provider" label="Provedor API" value="" onChange={() => undefined} searchable required
    options={[{ value: '', label: 'Escolha um provedor' }, { value: 'openai', label: 'OpenAI' }]} />)

  await user.click(screen.getByRole('combobox', { name: 'Provedor API' }))

  expect(screen.getByRole('listbox')).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'OpenAI' })).toBeVisible()
})

it('abre o estado vazio para um seletor pesquisável sem opções disponíveis', async () => {
  const user = userEvent.setup()
  render(<IonPicker id="empty-provider" label="Provedor API" value="" onChange={() => undefined} searchable required
    options={[{ value: '', label: 'Escolha um provedor' }]} />)

  await user.click(screen.getByRole('combobox', { name: 'Provedor API' }))

  expect(screen.getByRole('listbox')).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Escolha um provedor' })).toBeInTheDocument()
})

it('mostra a opção vazia selecionada e fecha a lista após trocar de documento', async () => {
  const user = userEvent.setup()
  function Harness() {
    const [value, setValue] = useState('b')
    return <><output data-testid="selected-value">{value || 'vazio'}</output><IonPicker id="test-all" label="Documento" value={value} onChange={setValue} searchable
      options={[{ value: '', label: 'Todos os documentos' }, { value: 'a', label: 'Arquitetura' }, { value: 'b', label: 'Código' }]} /></>
  }

  render(<Harness />)
  const root = screen.getByTestId('picker-test-all')
  const input = within(root).getByRole('combobox', { name: 'Documento' })
  expect(input).toHaveValue('Código')
  await user.click(within(root).getByRole('button', { name: /Abrir opções de Documento/ }))
  await user.click(screen.getByRole('option', { name: 'Todos os documentos' }))
  expect(screen.getByTestId('selected-value')).toHaveTextContent('vazio')
  expect(input).toHaveValue('Todos os documentos')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
})

it('limpar a pesquisa libera a seleção sem escolher durante a digitação', async () => {
  const user = userEvent.setup()
  const changed = vi.fn()
  function Harness() {
    const [value, setValue] = useState('b')
    return <><output data-testid="selected-value">{value || 'vazio'}</output><IonPicker id="test-clear" label="Documento" value={value}
      onChange={next => { changed(next); setValue(next) }} searchable
      options={[{ value: '', label: 'Todos os documentos' }, { value: 'a', label: 'Arquitetura' }, { value: 'b', label: 'Código' }]} /></>
  }

  render(<Harness />)
  const input = within(screen.getByTestId('picker-test-clear')).getByRole('combobox', { name: 'Documento' })
  await user.clear(input)
  expect(changed).toHaveBeenCalledWith('')
  expect(screen.getByTestId('selected-value')).toHaveTextContent('vazio')
  expect(input).toHaveValue('')
  await user.type(input, 'arq')
  expect(changed).toHaveBeenCalledTimes(1)
  expect(input).toHaveValue('arq')
  await user.tab()
  expect(input).not.toHaveValue('Código')
})

it('reset externo mantém ComboBox obrigatório vazio e impede envio do formulário', async () => {
  const user = userEvent.setup()
  const submitted = vi.fn()
  function Harness() {
    const [value, setValue] = useState('b')
    return <form onSubmit={event => { event.preventDefault(); submitted() }}>
      <IonPicker id="required-reset" label="Workflow" value={value} onChange={setValue} searchable required
        options={[{ value: '', label: 'Escolha workflow' }, { value: 'b', label: 'Workflow B' }]} />
      <button type="button" onClick={() => setValue('')}>Reset externo</button>
      <button type="submit">Salvar</button>
    </form>
  }

  render(<Harness />)
  await user.click(screen.getByRole('button', { name: 'Reset externo' }))
  const input = screen.getByRole('combobox', { name: 'Workflow' })
  expect(input).toHaveValue('')
  expect(input).toBeInvalid()
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Salvar' })).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Salvar' }))
  expect(submitted).not.toHaveBeenCalled()
})

it('texto livre sem opção selecionada não satisfaz workflow obrigatório', async () => {
  const user = userEvent.setup()
  const submitted = vi.fn()
  render(<form data-testid="required-form" onSubmit={event => { event.preventDefault(); submitted() }}>
    <IonPicker id="required-search" label="Workflow" value="" onChange={() => undefined} searchable required
      options={[{ value: '', label: 'Escolha workflow' }, { value: 'b', label: 'Workflow B' }]} />
    <button type="submit">Salvar</button>
  </form>)

  const input = screen.getByRole('combobox', { name: 'Workflow' })
  await user.type(input, 'inexistente')
  expect(input).toHaveValue('inexistente')
  expect(input).toBeInvalid()
  expect((screen.getByTestId('required-form') as HTMLFormElement).checkValidity()).toBe(false)
  await user.click(screen.getByText('Salvar'))
  expect(submitted).not.toHaveBeenCalled()
})

it('mantém o valor externo e o texto longo após seleção', async () => {
  const user = userEvent.setup()
  function Harness() {
    const [value, setValue] = useState('')
    return <><output data-testid="selected-value">{value}</output><IonPicker id="test-long" label="Destino" value={value} onChange={setValue}
      options={[{ value: '', label: 'Todos' }, { value: 'long', label: 'Documento com nome muito longo e conteúdo identificável na lista' }]} />
    </>
  }

  render(<Harness />)
  await user.click(within(screen.getByTestId('picker-test-long')).getByRole('button'))
  await user.click(screen.getByRole('option', { name: 'Documento com nome muito longo e conteúdo identificável na lista' }))
  expect(screen.getByTestId('selected-value')).toHaveTextContent('long')
  expect(within(screen.getByTestId('picker-test-long')).getByRole('button')).toHaveTextContent('Documento com nome muito longo e conteúdo identificável na lista')
  expect(within(screen.getByTestId('picker-test-long')).getByRole('button')).toHaveAttribute('title', 'Documento com nome muito longo e conteúdo identificável na lista')
})

it('abre por teclado e mantém o foco visível', async () => {
  const user = userEvent.setup()
  render(<IonPicker id="test-keyboard" label="Fuso horário" value="" onChange={() => undefined}
    options={[{ value: '', label: 'Todos' }, { value: 'UTC', label: 'UTC' }]} />)

  await user.tab()
  const trigger = within(screen.getByTestId('picker-test-keyboard')).getByRole('button')
  expect(trigger).toHaveFocus()
  expect(trigger).toHaveAttribute('data-focus-visible', 'true')
  await user.keyboard('{Enter}')
  expect(screen.getByRole('listbox')).toBeInTheDocument()
  await user.keyboard('{Escape}')
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
})

it('não abre opções enquanto estiver desabilitado', async () => {
  const user = userEvent.setup()
  const changed = vi.fn()
  render(<IonPicker id="test-disabled" label="Backend" value="local" onChange={changed}
    options={[{ value: 'local', label: 'Local' }, { value: 'api', label: 'API' }]} disabled />)

  const trigger = within(screen.getByTestId('picker-test-disabled')).getByRole('button')
  expect(trigger).toBeDisabled()
  await user.click(trigger)
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  expect(changed).not.toHaveBeenCalled()
})

it('abre a lista completa ao clicar em qualquer ponto do campo pesquisável', async () => {
  const user = userEvent.setup()
  render(<IonPicker id="test-open" label="Provedor" value="" onChange={vi.fn()} searchable
    options={[{ value: '', label: 'Escolha um provedor' }, { value: 'a', label: 'Alfa' }, { value: 'b', label: 'Beta' }]} />)

  const input = within(screen.getByTestId('picker-test-open')).getByRole('combobox', { name: 'Provedor' })
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  await user.click(input)
  expect(screen.getByRole('listbox')).toBeInTheDocument()
  expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Escolha um provedor', 'Alfa', 'Beta'])
  await user.click(input)
  expect(screen.getByRole('listbox')).toBeInTheDocument()
})

it('com uma opção já escolhida, o clique mostra todas e digitar substitui o nome sem precisar apagar', async () => {
  const user = userEvent.setup()
  function Picker() {
    const [value, setValue] = useState('astra')
    return <IonPicker id="test-chosen" label="Modelo" value={value} onChange={setValue} searchable
      options={[{ value: 'astra', label: 'GPT-6-Astra' }, { value: 'sol', label: 'GPT-6-Sol' }, { value: 'opus', label: 'Opus (mais recente)' }]} />
  }
  render(<Picker />)
  const input = within(screen.getByTestId('picker-test-chosen')).getByRole('combobox', { name: 'Modelo' })
  await user.click(input)
  expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['GPT-6-Astra', 'GPT-6-Sol', 'Opus (mais recente)'])
  await user.keyboard('opus')
  expect(input).toHaveValue('opus')
  expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Opus (mais recente)'])
  await user.click(screen.getByRole('option', { name: 'Opus (mais recente)' }))
  expect(input).toHaveValue('Opus (mais recente)')
  await user.keyboard('{Escape}')
  input.blur()
  await user.keyboard('{Tab}')
  input.focus()
  await user.keyboard('{ArrowDown}')
  expect(screen.getAllByRole('option')).toHaveLength(3)
})
