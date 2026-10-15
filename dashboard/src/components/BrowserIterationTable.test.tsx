// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import BrowserIterationTable from './BrowserIterationTable'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('BrowserIterationTable', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('shows each transaction’s cold and warm times side by side', () => {
    act(() => root.render(<BrowserIterationTable rows={[
      { transaction: 'Open account', cold: { count: 4, errors: 1, avg_ms: 5200, p90_ms: 6100.25 }, warm: { count: 36, avg_ms: 900, p90_ms: 1200 } },
      { transaction: 'Save', warm: { count: 10, avg_ms: 300, p90_ms: 450 } },
    ]} />))
    const cells = Array.from(container.querySelectorAll('tbody tr')).map(row => Array.from(row.querySelectorAll('td')).map(cell => cell.textContent))
    expect(cells).toEqual([
      ['Open account', '4', '5200.0 ms', '6100.3 ms', '36', '900.0 ms', '1200.0 ms', '1 / 0'],
      ['Save', '0', '—', '—', '10', '300.0 ms', '450.0 ms', '0 / 0'],
    ])
  })

  it('renders nothing without browser iterations', () => {
    act(() => root.render(<BrowserIterationTable rows={[]} />))
    expect(container.innerHTML).toBe('')
  })
})
