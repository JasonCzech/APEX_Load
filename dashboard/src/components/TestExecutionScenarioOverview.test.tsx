// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import TestExecutionScenarioOverview from './TestExecutionScenarioOverview'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('TestExecutionScenarioOverview', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('renders multi-group summary chips', async () => {
    await act(async () => {
      root.render(
        <TestExecutionScenarioOverview
          test={{
            groups: [
              { name: 'Checkout', script_name: 'Checkout Script', script_id: 'script-1', protocol: 'http', vusers: 10 },
              { name: 'Search', script_name: 'Search Script', script_id: 'script-2', protocol: 'http', vusers: 5 },
            ],
          } as never}
          isMultiGroup
          shakeoutFailed={false}
          groupColors={['red', 'blue']}
        />,
      )
    })

    expect(container.textContent).toContain('Checkout')
    expect(container.textContent).toContain('Checkout Script · 10 VUsers')
    expect(container.textContent).toContain('Search Script · 5 VUsers')
  })

  it('renders the shakeout failure banner when the shakeout fails', async () => {
    await act(async () => {
      root.render(
        <TestExecutionScenarioOverview
          test={{
            error: 'Shakeout failed on login action',
            groups: [],
          } as never}
          isMultiGroup={false}
          shakeoutFailed
          groupColors={[]}
        />,
      )
    })

    expect(container.textContent).toContain('Script shakeout failed')
    expect(container.textContent).toContain('Shakeout failed on login action')
  })
})