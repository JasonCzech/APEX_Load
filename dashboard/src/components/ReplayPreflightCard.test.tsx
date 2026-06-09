// @vitest-environment jsdom

import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import ReplayPreflightCard from './ReplayPreflightCard'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ReplayPreflightCard', () => {
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

  it('surfaces expected replay action count when control flow expands fixed loops', () => {
    act(() => {
      root.render(<ReplayPreflightCard preflight={{
        valid: true,
        action_count: 4,
        expected_action_count: 5,
        data_source_count: 0,
        parameter_count: 0,
        browser_required: true,
        checks: [],
      }} />)
    })

    expect(container.textContent).toContain('4 actions')
    expect(container.textContent).toContain('5 expected')
    expect(container.textContent).toContain('Browser runtime')
  })
})
