// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import AuraParityTable from './AuraParityTable'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('AuraParityTable', () => {
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

  it('sets each transaction’s browser Aura calls beside the HTTP scripts’', () => {
    act(() => root.render(<AuraParityTable rows={[{
      transaction: 'Open account', browser_instances: 2, http_instances: 2,
      browser_requests: 2, http_requests: 3, browser_actions_per_request: 2, http_actions_per_request: 1.3,
      browser_bytes_per_request: 9000, http_bytes_per_request: 4000,
      actions: [
        { name: 'AccountSearch.find', browser: 3, http: 1, flagged: true },
        { name: 'RecordGvpController/ACTION$getRecord', browser: 1, http: 1 },
      ],
    }]} />))
    expect(container.querySelector('h5')?.textContent).toBe('Open account')
    const cells = Array.from(container.querySelectorAll('tbody tr')).map(row => Array.from(row.querySelectorAll('td')).map(cell => cell.textContent))
    expect(cells).toEqual([
      ['AccountSearch.find', '3.0', '1.0', 'Yes'],
      ['RecordGvpController/ACTION$getRecord', '1.0', '1.0', ''],
    ])
    expect(container.textContent).toContain('2.0 requests per instance (2 instances)')
  })

  it('renders nothing without both sides', () => {
    act(() => root.render(<AuraParityTable rows={[]} />))
    expect(container.innerHTML).toBe('')
  })
})
