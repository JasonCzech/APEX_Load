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

  it('shows the percentile distribution beside P95 and marks archives without it', async () => {
    await act(async () => {
      root.render(<TransactionSlaScoreboard rows={[{ ...rows[0], min_ms: 120, p50_ms: 310, p90_ms: 640, p99_ms: 980, max_ms: 1450 }, rows[1]]} />)
    })
    const headers = Array.from(container.querySelectorAll('.run-report-table thead th')).map(th => th.textContent?.replace(/[↑↓]/g, '').trim())
    // Units sit in the headers, as in the Action summary beside it; cells are numbers.
    expect(headers).toEqual(['Transaction', 'Count', 'Min (ms)', 'P50 (ms)', 'P90 (ms)', 'P95 (ms)', 'P99 (ms)', 'Max (ms)', 'SLA (ms)', 'Error rate', 'Result'])
    const checkout = Array.from(container.querySelectorAll('.run-report-table tbody tr')).find(row => row.textContent?.startsWith('Checkout'))!
    expect(Array.from(checkout.querySelectorAll('td')).slice(2, 8).map(cell => cell.textContent)).toEqual(['120', '310', '640', '750', '980', '1,450'])
    const search = Array.from(container.querySelectorAll('.run-report-table tbody tr')).find(row => row.textContent?.startsWith('Search'))!
    expect(search.querySelectorAll('td')[3].textContent).toBe('—')
  })

  it('marks percentiles that came from the duration sketch', async () => {
    await act(async () => {
      root.render(<TransactionSlaScoreboard rows={[{ ...rows[0], p50_ms: 310, p90_ms: 640, p99_ms: 980, percentile_quality: 'approximate' }, rows[1]]} />)
    })
    const [checkout, search] = ['Checkout', 'Search'].map(name => Array.from(container.querySelectorAll('.run-report-table tbody tr')).find(row => row.textContent?.startsWith(name))!)
    const marks = checkout.querySelectorAll('.percentile-quality-mark')
    expect(marks).toHaveLength(4)
    expect(marks[0].getAttribute('title')).toContain('within 0.5%')
    expect(checkout.querySelectorAll('td')[5].textContent).toContain('750')
    expect(search.querySelectorAll('.percentile-quality-mark')).toHaveLength(0)
    // A percentile the row lacks gets no mark.
    await act(async () => {
      root.render(<TransactionSlaScoreboard rows={[{ ...rows[0], percentile_quality: 'approximate' }]} />)
    })
    expect(container.querySelectorAll('.run-report-table tbody tr .percentile-quality-mark')).toHaveLength(1)
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
