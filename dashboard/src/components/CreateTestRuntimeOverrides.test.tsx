// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestRuntimeOverrides from './CreateTestRuntimeOverrides'
import { ScenarioDraft } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const EMPTY_SETTINGS: ScenarioDraft['runtimeSettings'] = {
  log_level: '',
  auto_transactions: '',
}

describe('CreateTestRuntimeOverrides', () => {
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
    vi.clearAllMocks()
  })

  function tabButton(label: string): HTMLButtonElement {
    const button = Array.from(container.querySelectorAll('button[role="tab"]')).find(
      candidate => candidate.textContent?.trim() === label,
    ) as HTMLButtonElement | undefined
    expect(button, `tab ${label}`).toBeDefined()
    return button as HTMLButtonElement
  }

  it('mounts the shared runtime editor with the full tab set', async () => {
    const onReplace = vi.fn()
    await act(async () => {
      root.render(
        <CreateTestRuntimeOverrides
          runtimeSettings={EMPTY_SETTINGS}
          groupProtocols={['http']}
          onReplaceRuntimeSettings={onReplace}
        />,
      )
    })

    expect(container.textContent).toContain('Runtime Settings Overrides')
    for (const label of ['Summary', 'Think Time', 'Iteration Pacing', 'Run Logic', 'Error Handling', 'HTTP Transport', 'Browser Emulation', 'Web/HTTP']) {
      tabButton(label)
    }
    // Single settings object: no cross-group copy panel and no focus dropdown.
    expect(container.textContent).not.toContain('Apply to groups')
    expect(container.querySelector('select[aria-label="Focus group"]')).toBeNull()
  })

  it('shows every protocol-gated section when no group is selected yet', async () => {
    await act(async () => {
      root.render(
        <CreateTestRuntimeOverrides
          runtimeSettings={EMPTY_SETTINGS}
          groupProtocols={[]}
          onReplaceRuntimeSettings={vi.fn()}
        />,
      )
    })
    for (const label of ['HTTP Transport', 'Web/HTTP', 'Java Env']) {
      tabButton(label)
    }
  })

  it('replaces the whole runtime settings object when a field changes', async () => {
    const onReplace = vi.fn()

    function Harness() {
      const [runtimeSettings, setRuntimeSettings] = useState<ScenarioDraft['runtimeSettings']>(EMPTY_SETTINGS)
      return (
        <CreateTestRuntimeOverrides
          runtimeSettings={runtimeSettings}
          groupProtocols={['http']}
          onReplaceRuntimeSettings={next => {
            onReplace(next)
            setRuntimeSettings(next)
          }}
        />
      )
    }

    await act(async () => {
      root.render(<Harness />)
    })

    await act(async () => {
      tabButton('Iteration Pacing').click()
    })
    const pacingSelect = Array.from(container.querySelectorAll('select')).find(
      select => select.parentElement?.textContent?.includes('Pacing'),
    ) as HTMLSelectElement | undefined
    expect(pacingSelect).toBeDefined()

    await act(async () => {
      pacingSelect!.value = 'fixed_delay'
      pacingSelect!.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(onReplace).toHaveBeenCalled()
    const latest = onReplace.mock.calls[onReplace.mock.calls.length - 1]?.[0] as ScenarioDraft['runtimeSettings']
    expect(latest?.pacing?.type).toBe('fixed_delay')
  })
})
