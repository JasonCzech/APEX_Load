// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import AuraActionRollupTable from './AuraActionRollupTable'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('AuraActionRollupTable', () => {
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

  it('lists each transaction’s Aura actions with their calls, time and errors', () => {
    act(() => root.render(<AuraActionRollupTable rows={[
      { transaction: 'Search', name: 'AccountSearch.find', apex: true, count: 2, total_ms: 220, errors: 1 },
      { transaction: '', name: 'serviceComponent://RecordGvpController/ACTION$getRecord', count: 1, total_ms: 50 },
    ]} />))
    const cells = Array.from(container.querySelectorAll('tbody tr')).map(row => Array.from(row.querySelectorAll('td')).map(cell => cell.textContent))
    expect(cells).toEqual([
      ['Search', 'AccountSearch.find (Apex)', '2', '220.0 ms', '1'],
      ['Outside transactions', 'serviceComponent://RecordGvpController/ACTION$getRecord', '1', '50.0 ms', '0'],
    ])
  })

  it('renders nothing without actions', () => {
    act(() => root.render(<AuraActionRollupTable rows={[]} />))
    expect(container.innerHTML).toBe('')
  })
})
