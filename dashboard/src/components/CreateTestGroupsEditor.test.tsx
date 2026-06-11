// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestGroupsEditor from './CreateTestGroupsEditor'
import { type ScenarioDraftGroup, type StoredScript } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const scripts = [
  { id: 'script-http', name: 'Checkout API', protocol: 'http', version: '1.0.0', created_at: '2026-06-01T00:00:00Z' },
  { id: 'script-browser', name: 'Login Journey', protocol: 'browser', version: '1.0.0', created_at: '2026-06-01T00:00:00Z' },
] as StoredScript[]

function draftGroup(patch: Partial<ScenarioDraftGroup> = {}): ScenarioDraftGroup {
  return {
    name: '',
    scriptId: 'script-http',
    vusers: 10,
    rampUp: '',
    rampDown: '',
    initialization: 'at_runtime',
    ...patch,
  }
}

describe('CreateTestGroupsEditor', () => {
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

  function roleSelect(): HTMLSelectElement {
    const select = Array.from(container.querySelectorAll('select')).find(element =>
      Array.from(element.options).some(option => option.value === 'observer'))
    expect(select).toBeDefined()
    return select as HTMLSelectElement
  }

  it('gates the observer role to browser-protocol scripts', async () => {
    const onUpdateGroup = vi.fn()
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[draftGroup()]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })

    const select = roleSelect()
    expect(select.value).toBe('load')
    const observerOption = Array.from(select.options).find(option => option.value === 'observer')
    expect(observerOption?.disabled).toBe(true)
    expect(observerOption?.textContent).toContain('browser scripts only')
  })

  it('lets browser-script groups become observer canaries', async () => {
    const onUpdateGroup = vi.fn()
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[draftGroup({ scriptId: 'script-browser' })]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })

    const select = roleSelect()
    const observerOption = Array.from(select.options).find(option => option.value === 'observer')
    expect(observerOption?.disabled).toBe(false)

    await act(async () => {
      select.value = 'observer'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdateGroup).toHaveBeenCalledWith(0, { role: 'observer' })
  })

  it('shows the persisted observer role', async () => {
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[draftGroup({ scriptId: 'script-browser', role: 'observer' })]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={vi.fn()}
          onRemoveGroup={vi.fn()}
        />,
      )
    })
    expect(roleSelect().value).toBe('observer')
  })
})
