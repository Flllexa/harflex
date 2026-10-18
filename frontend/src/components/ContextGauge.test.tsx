import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { ContextGauge } from './ContextGauge'

afterEach(cleanup)

it('says how full the context is and turns red near the limit', () => {
  const { rerender } = render(<ContextGauge used={84000} window={200000} onCompact={() => undefined} />)
  const gauge = screen.getByRole('button', { name: /^Contexto: 42% de 200\smil tokens$/ })
  expect(gauge).not.toHaveClass('is-warning')
  rerender(<ContextGauge used={180000} window={200000} onCompact={() => undefined} />)
  expect(screen.getByRole('button', { name: /^Contexto: 90% de 200\smil tokens$/ })).toHaveClass('is-danger')
})

it('compacts now from its menu', async () => {
  const onCompact = vi.fn()
  render(<ContextGauge used={120000} window={200000} onCompact={onCompact} />)
  await userEvent.click(screen.getByRole('button', { name: /Contexto: 60%/ }))
  await userEvent.click(screen.getByRole('menuitem', { name: 'Compactar agora' }))
  expect(onCompact).toHaveBeenCalledTimes(1)
})
