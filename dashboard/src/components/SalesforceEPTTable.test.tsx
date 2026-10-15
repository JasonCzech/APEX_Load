// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import SalesforceEPTTable from './SalesforceEPTTable'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('SalesforceEPTTable', () => {
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

  it('shows each step’s EPT beside its own time', () => {
    act(() => root.render(<SalesforceEPTTable rows={[
      { step: 'Open account', samples: 2, avg_ept_ms: 1500, avg_step_ms: 2000, avg_difference_ms: 500 },
      { step: 'Search', samples: 1, avg_ept_ms: 800, avg_step_ms: 750, avg_difference_ms: -50 },
    ]} />))
    const cells = Array.from(container.querySelectorAll('tbody tr')).map(row => Array.from(row.querySelectorAll('td')).map(cell => cell.textContent))
    expect(cells).toEqual([
      ['Open account', '2', '1500.0 ms', '2000.0 ms', '+500.0 ms'],
      ['Search', '1', '800.0 ms', '750.0 ms', '-50.0 ms'],
    ])
  })

  it('renders nothing without readings', () => {
    act(() => root.render(<SalesforceEPTTable rows={[]} />))
    expect(container.innerHTML).toBe('')
  })
})
