// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { RunArchiveSLARow } from '../../types'
import TransactionSlaScoreboard from './TransactionSlaScoreboard'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const rows: RunArchiveSLARow[] = [
  { name: 'Checkout', volume: 100, p95_ms: 750, sla_ms: 700, has_sla: true, error_pct: 0.1, result: 'Fail', result_kind: 'fail' },
  { name: 'Search', volume: 200, p95_ms: 600, sla_ms: 700, has_sla: true, error_pct: 0, result: 'Pass', result_kind: 'pass' },
  { name: 'Login', volume: 50, p95_ms: 680, sla_ms: 700, has_sla: true, error_pct: 0, result: 'Pass - 97% of SLA', result_kind: 'warn' },
]

describe('TransactionSlaScoreboard', () => {
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

  it('filters by SLA result and emits selected transaction changes', async () => {
    const onSelect = vi.fn()
    await act(async () => {
      root.render(<TransactionSlaScoreboard rows={rows} onSelectAction={onSelect} />)
    })

    expect(container.textContent).toContain('Checkout')
    expect(container.querySelector('.run-report-sla-fail')?.textContent).toBe('Fail')

    await act(async () => {
      const select = container.querySelector<HTMLSelectElement>('select[aria-label="Filter by SLA result"]')
      if (select) {
        select.value = 'warn'
        select.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(container.textContent).toContain('Login')
    expect(container.textContent).not.toContain('Checkout')

    await act(async () => {
      container.querySelector('tbody tr')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onSelect).toHaveBeenCalledWith('Login')
  })
})
