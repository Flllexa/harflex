import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ToolCallView } from '../../state/session'
import { ToolCallCard, toolFailureText } from './ToolCallCard'

const call = (overrides: Partial<ToolCallView> = {}): ToolCallView => ({
  toolCallId: 'call-1', name: 'read', arguments: { path: 'index.html' }, status: 'failed', output: '', ...overrides,
})

describe('failed tool calls', () => {
  it('explains in Portuguese a failure the model could act on, and says the run went on', () => {
    render(<ToolCallCard call={call({ errorCode: 'not_found', error: '"index.html" does not exist', recoverable: true })} />)
    const card = screen.getByRole('article', { name: 'Ferramenta read' })
    expect(card).toHaveTextContent('Falhou')
    expect(card).toHaveTextContent('Não encontrado.')
    expect(within(card).getByText('"index.html" does not exist')).toBeInTheDocument()
    expect(card).toHaveTextContent('A execução continuou e o modelo foi avisado.')
  })

  it('shows what a failed command printed, already open', () => {
    render(<ToolCallCard call={call({ name: 'bash', arguments: { command: 'go test ./...' }, errorCode: 'exit_status', error: 'the command exited with status 1', recoverable: true, result: 'FAIL: TestCriar' })} />)
    const card = screen.getByRole('article', { name: 'Ferramenta bash' })
    expect(card).toHaveTextContent('O comando terminou com erro.')
    expect(card).toHaveTextContent('the command exited with status 1')
    expect(within(card).getByText('Saída').closest('details')).toHaveAttribute('open')
    expect(card).toHaveTextContent('FAIL: TestCriar')
  })

  it('lets the server speak for itself when an MCP tool refuses a call', () => {
    render(<ToolCallCard call={call({ name: 'mcp_a1b2c3d4_create_pull_request_00ff', arguments: {}, errorCode: 'mcp_error', error: 'the MCP server reported an error; its answer is in result', recoverable: true, result: 'A pull request already exists for this branch' })} />)
    const card = screen.getByRole('article', { name: /Ferramenta mcp_/ })
    expect(card).toHaveTextContent('O servidor MCP recusou a chamada.')
    expect(card).toHaveTextContent('A execução continuou e o modelo foi avisado.')
    expect(card).toHaveTextContent('A pull request already exists for this branch')
    expect(card).not.toHaveTextContent('its answer is in result')
  })

  it('does not show the raw error of a failure that ended the run', () => {
    render(<ToolCallCard call={call({ errorCode: 'tool_failed', error: 'tool execution failed' })} />)
    const card = screen.getByRole('article', { name: 'Ferramenta read' })
    expect(card).toHaveTextContent('A ferramenta falhou e a execução foi encerrada.')
    expect(card).toHaveTextContent('não exibe o erro bruto')
    expect(card).not.toHaveTextContent('tool execution failed')
    expect(card).not.toHaveTextContent('A execução continuou')
  })

  it('words the other reasons a call did not complete', () => {
    expect(toolFailureText({ errorCode: 'policy_denied', error: 'tool denied by security policy' })?.summary).toBe('A política de segurança negou esta ferramenta.')
    expect(toolFailureText({ errorCode: 'skipped_after_failure', error: 'tool call not executed' })?.summary).toBe('Não executada: uma chamada anterior falhou.')
    expect(toolFailureText({ errorCode: 'approval_denied', error: 'tool call not executed: approval_denied' })?.summary).toBe('Você negou esta aprovação.')
    expect(toolFailureText({ errorCode: 'outcome_unknown' })?.summary).toContain('Resultado desconhecido')
    expect(toolFailureText({ errorCode: 'not_executed' })?.summary).toBe('Não executada antes da interrupção.')
  })

  it('falls back to the error it was given for a code it does not know', () => {
    expect(toolFailureText({ errorCode: 'algo_novo', error: 'texto que o backend mandou' })).toEqual({ summary: 'texto que o backend mandou' })
    expect(toolFailureText({ errorCode: 'algo_novo' })).toBeUndefined()
    // A code that only counts when the run went on is not claimed for a call that ended it.
    expect(toolFailureText({ errorCode: 'not_found', error: 'x', recoverable: false })).toEqual({ summary: 'x' })
  })
})
