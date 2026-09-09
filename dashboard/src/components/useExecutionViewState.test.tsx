// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { useExecutionViewState } from './useExecutionViewState'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function ViewStateHarness() {
  const model = useExecutionViewState()

  return (
    <div>
      <span data-testid="chart">{model.expandedChart || 'none'}</span>
      <span data-testid="script">{model.expandedScenarioScript || 'none'}</span>
      <button data-testid="set-chart" onClick={() => model.onExpandedChartChange('latency')}>chart</button>
      <button data-testid="clear-chart" onClick={() => model.onExpandedChartChange(null)}>clear-chart</button>
      <button data-testid="set-script" onClick={() => model.onExpandedScenarioScriptChange('Checkout')}>script</button>
      <button data-testid="clear-script" onClick={() => model.onExpandedScenarioScriptChange(null)}>clear-script</button>
    </div>
  )
}

describe('useExecutionViewState', () => {
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

  it('tracks expanded chart and queued script state independently', async () => {
    await act(async () => {
      root.render(<ViewStateHarness />)
    })

    expect(readTestId('chart')).toBe('none')
    expect(readTestId('script')).toBe('none')

    await click('set-chart')
    await click('set-script')

    expect(readTestId('chart')).toBe('latency')
    expect(readTestId('script')).toBe('Checkout')

    await click('clear-chart')
    await click('clear-script')

    expect(readTestId('chart')).toBe('none')
    expect(readTestId('script')).toBe('none')
  })

  async function click(testId: string) {
    await act(async () => {
      container.querySelector(`[data-testid="${testId}"]`)?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})
