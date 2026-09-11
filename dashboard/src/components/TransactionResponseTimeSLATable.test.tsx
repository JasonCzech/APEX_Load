// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TransactionResponseTimeSLAResult } from '../types'
import TransactionResponseTimeSLATable from './TransactionResponseTimeSLATable'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function row(group: string, status: TransactionResponseTimeSLAResult['status'], missedPct: number): TransactionResponseTimeSLAResult {
  return { group, transaction: 'Checkout', threshold_ms: 3000, total_count: 21, eligible_success_count: 20, excluded_error_count: 1, missed_count: missedPct ? 2 : 0, missed_pct: missedPct, met_pct: 100 - missedPct, required_met_pct: 95, status }
}

describe('TransactionResponseTimeSLATable', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('keeps same-named transactions separate, sorts breaches first, and selects group plus transaction', async () => {
    const onSelect = vi.fn()
    await act(async () => root.render(<TransactionResponseTimeSLATable rows={[row('Passing group', 'pass', 0), row('Failing group', 'fail', 10)]} onSelect={onSelect} />))
    const bodyRows = container.querySelectorAll<HTMLTableRowElement>('tbody tr')
    expect(bodyRows).toHaveLength(2)
    expect(bodyRows[0].textContent).toContain('Failing group')
    expect(bodyRows[0].textContent).toContain('10.00%')
    expect(bodyRows[0].querySelector('[aria-label="SLA status: Fail"]')).not.toBeNull()
    const selection = bodyRows[1].querySelector<HTMLButtonElement>('button[aria-label="Show archived samples for Checkout in Passing group"]')
    expect(selection).not.toBeNull()
    expect(selection?.getAttribute('aria-pressed')).toBe('false')
    await act(async () => selection?.click())
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ group: 'Passing group', transaction: 'Checkout' }))
  })

  it('labels zero-success evidence as not evaluated', async () => {
    const empty: TransactionResponseTimeSLAResult = { group: 'Empty', transaction: '', threshold_ms: 500, total_count: 0, eligible_success_count: 0, excluded_error_count: 0, missed_count: 0, missed_pct: 0, met_pct: 0, required_met_pct: 95, status: 'not_evaluated' }
    await act(async () => root.render(<TransactionResponseTimeSLATable rows={[empty]} />))
    expect(container.textContent).toContain('No named transaction observed')
    expect(container.querySelector('[aria-label="SLA status: Not evaluated"]')).not.toBeNull()
  })
})
