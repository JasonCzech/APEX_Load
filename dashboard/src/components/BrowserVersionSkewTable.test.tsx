// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import BrowserVersionSkewTable from './BrowserVersionSkewTable'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('BrowserVersionSkewTable', () => {
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

  it('lists each mismatch with what the browser was compared with', () => {
    act(() => root.render(<BrowserVersionSkewTable rows={[
      { source: 'driver', actual: '152.0.7900.1', expected: '149.0.7827.55', sessions: 4 },
      { source: 'recording', actual: '152.0.7900.1', expected: '148.0.7000.2', sessions: 4 },
    ]} />))
    const cells = Array.from(container.querySelectorAll('tbody tr')).map(row => Array.from(row.querySelectorAll('td')).map(cell => cell.textContent))
    expect(cells).toEqual([
      ['The Playwright driver’s tested browser', '152.0.7900.1', '149.0.7827.55', '4'],
      ['The recording’s browser', '152.0.7900.1', '148.0.7000.2', '4'],
    ])
  })

  it('renders nothing without mismatches', () => {
    act(() => root.render(<BrowserVersionSkewTable rows={[]} />))
    expect(container.innerHTML).toBe('')
  })
})
