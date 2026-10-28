// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestExecutionScenarioEditor from './TestExecutionScenarioEditor'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listScripts: vi.fn(),
  getScenarioValidationContext: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
  }),
}))

vi.mock('./CreateTestModal', () => ({
  default: ({ mode, projectId, scripts, initialScenario, profileManaged, onCreated }: { mode?: string, projectId?: string, scripts: Array<{ id: string }>, initialScenario?: { name?: string }, profileManaged?: boolean, onCreated: (test: { id: string }) => void }) => (
    <div data-testid="create-test-modal">
      <span>mode:{mode}</span>
      <span>profile:{profileManaged ? 'managed' : 'none'}</span>
      <span>project:{projectId || 'none'}</span>
      <span>scripts:{scripts.length}</span>
      <span>scenario:{initialScenario?.name || 'none'}</span>
      <button onClick={() => onCreated({ id: 'revised-test-1' })}>create-copy</button>
    </div>
  ),
}))

describe('TestExecutionScenarioEditor', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.listScripts.mockResolvedValue({
      scripts: [{ id: 'script-1', name: 'Checkout Script', protocol: 'http' }],
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('bridges queued test editing into a revised queued-run modal and selects the new test after create', async () => {
    const onClose = vi.fn()
    const onSelectTest = vi.fn()

    await act(async () => {
      root.render(
        <TestExecutionScenarioEditor
          open
          test={{
            id: 'test-1',
            name: 'Queued Checkout Run',
            status: 'QUEUED',
            project_id: 'project-2',
            total_vusers: 10,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              ramp_up: '30s',
              initialization: 'at_runtime',
            }],
          } as never}
          onClose={onClose}
          onSelectTest={onSelectTest}
        />,
      )
    })

    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledTimes(1)
    expect(apiMock.listScripts).toHaveBeenCalledWith('project-2')
    expect(container.textContent).toContain('mode:revise-queued-run')
    expect(container.textContent).toContain('project:project-2')
    expect(container.textContent).toContain('scripts:1')
    expect(container.textContent).toContain('scenario:Queued Checkout Run')
    expect(container.textContent).toContain('profile:none')

    const createButton = Array.from(container.querySelectorAll('button')).find((button) => button.textContent?.includes('create-copy'))
    expect(createButton).toBeTruthy()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(onSelectTest).toHaveBeenCalledWith('revised-test-1')
    // Parent admission must see the owned completion before close retires it.
    expect(onSelectTest.mock.invocationCallOrder[0]).toBeLessThan(onClose.mock.invocationCallOrder[0])
  })

  it('tells the revise dialog when the run replays a TPS traffic profile', async () => {
    const run = (runtimeTarget: 'tps' | 'vusers') => ({
      id: 'test-profile', name: 'Profile run', status: 'QUEUED', project_id: 'project-2', total_vusers: 5, duration: '10m', created_at: '2026-05-12T20:00:00Z', scenario_mode: runtimeTarget === 'tps' ? 'goal_oriented' : 'vuser_driven',
      groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 5, initialization: 'at_runtime' }],
      traffic_profile: { version: 1, runtime_target: runtimeTarget, mappings: [{ series_key: 'checkout', group_name: 'Checkout', peak_target: 12, points: [{ offset_ms: 0, target: 12 }] }] },
    }) as never
    await act(async () => root.render(<TestExecutionScenarioEditor open test={run('tps')} onClose={vi.fn()} onSelectTest={vi.fn()} />))
    await flushPromises()
    expect(container.textContent).toContain('profile:managed')
    // A VUser profile sets no throughput: the goal settings do not apply to it.
    await act(async () => root.render(<TestExecutionScenarioEditor open test={run('vusers')} onClose={vi.fn()} onSelectTest={vi.fn()} />))
    await flushPromises()
    expect(container.textContent).toContain('profile:none')
  })

  it('shows a failed script load with Retry instead of an editor without scripts', async () => {
    const onClose = vi.fn()
    apiMock.listScripts.mockRejectedValueOnce(new Error('controller unavailable'))
    await act(async () => root.render(<TestExecutionScenarioEditor open test={queuedRun()} onClose={onClose} onSelectTest={vi.fn()} />))
    await flushPromises()

    expect(document.body.querySelector('[data-testid="create-test-modal"]')).toBeNull()
    expect(document.body.querySelector('[role="alert"]')?.textContent).toContain('Scripts could not be loaded')

    await act(async () => buttonNamed('Retry').dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledTimes(2)
    expect(document.body.querySelector('[role="alert"]')).toBeNull()
    expect(container.textContent).toContain('scripts:1')
    expect(onClose).not.toHaveBeenCalled()
  })

  it('keeps the failure on screen while a retry runs and closes from it', async () => {
    const onClose = vi.fn()
    let rejectRetry: (cause: Error) => void = () => undefined
    apiMock.listScripts
      .mockRejectedValueOnce(new Error('controller unavailable'))
      .mockReturnValueOnce(new Promise((_, reject) => { rejectRetry = reject }))
    await act(async () => root.render(<TestExecutionScenarioEditor open test={queuedRun()} onClose={onClose} onSelectTest={vi.fn()} />))
    await flushPromises()

    await act(async () => buttonNamed('Retry').dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(buttonNamed('Retrying…').disabled).toBe(true)
    expect(document.body.querySelector('[data-testid="create-test-modal"]')).toBeNull()

    await act(async () => rejectRetry(new Error('still unavailable')))
    await flushPromises()
    expect(buttonNamed('Retry').disabled).toBe(false)

    await act(async () => buttonNamed('Close').dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not open the editor with an empty script list while scripts load', async () => {
    let resolveScripts: (response: { scripts: Array<{ id: string }> }) => void = () => undefined
    apiMock.listScripts.mockReturnValueOnce(new Promise(resolve => { resolveScripts = resolve }))
    await act(async () => root.render(<TestExecutionScenarioEditor open test={queuedRun()} onClose={vi.fn()} onSelectTest={vi.fn()} />))
    await flushPromises()

    expect(container.querySelector('[data-testid="create-test-modal"]')).toBeNull()
    expect(container.textContent).toContain('Loading scenario editor…')

    await act(async () => resolveScripts({ scripts: [{ id: 'script-1' }, { id: 'script-2' }] }))
    await flushPromises()
    expect(container.textContent).toContain('scripts:2')
  })

  it('marks held scenario validation stale with named reasons on focus when scripts, policy, capabilities, approvals, or secrets change, while ignoring read failures (AUTH-B1)', async () => {
    const context = (overrides?: Partial<Record<'scripts' | 'capabilities' | 'policy' | 'approvals' | 'secret_references', string>>) => ({
      version: 1,
      revision: `svctx1:${JSON.stringify(overrides ?? {})}`,
      components: {
        scripts: overrides?.scripts ?? 'scripts-a',
        capabilities: overrides?.capabilities ?? 'cap-a',
        policy: overrides?.policy ?? 'policy-a',
        approvals: overrides?.approvals ?? 'approvals-a',
        secret_references: overrides?.secret_references ?? 'secrets-a',
      },
    })
    let clock = 1_000_000
    const now = vi.spyOn(Date, 'now').mockImplementation(() => clock)
    try {
      apiMock.getScenarioValidationContext.mockResolvedValue(context())
      await act(async () => root.render(<TestExecutionScenarioEditor open test={queuedRun()} onClose={vi.fn()} onSelectTest={vi.fn()} />))
      await flushPromises()
      expect(container.querySelector('.scenario-validation-stale-notice')).toBeNull()

      // A transient read failure or null context on focus never marks held validation stale.
      apiMock.getScenarioValidationContext.mockRejectedValueOnce(new Error('503 Service Unavailable'))
      clock += 6_000
      await act(async () => { window.dispatchEvent(new Event('focus')) })
      await flushPromises()
      expect(container.querySelector('.scenario-validation-stale-notice')).toBeNull()

      apiMock.getScenarioValidationContext.mockResolvedValueOnce(null)
      clock += 12_000
      await act(async () => { window.dispatchEvent(new Event('focus')) })
      await flushPromises()
      expect(container.querySelector('.scenario-validation-stale-notice')).toBeNull()

      // A changed constituent script, policy, and secret reference reports the exact named causes.
      apiMock.getScenarioValidationContext.mockResolvedValueOnce(context({
        scripts: 'scripts-b',
        policy: 'policy-b',
        secret_references: 'secrets-b',
      }))
      clock += 24_000
      await act(async () => { window.dispatchEvent(new Event('focus')) })
      await flushPromises()
      const notice = container.querySelector('.scenario-validation-stale-notice')?.textContent || ''
      expect(notice).toContain('Validation results no longer apply: constituent scripts, project or domain policy, project secrets changed since the check. Validate load model again.')

      // Rechecking adopts the updated context and clears the stale notice.
      apiMock.getScenarioValidationContext.mockResolvedValueOnce(context({
        scripts: 'scripts-b',
        policy: 'policy-b',
        secret_references: 'secrets-b',
      }))
      await act(async () => buttonNamed('Recheck validation').dispatchEvent(new MouseEvent('click', { bubbles: true })))
      await flushPromises()
      expect(container.querySelector('.scenario-validation-stale-notice')).toBeNull()
    } finally {
      now.mockRestore()
    }
  })
})

function queuedRun() {
  return {
    id: 'test-1', name: 'Queued Checkout Run', status: 'QUEUED', project_id: 'project-2', total_vusers: 10, duration: '5m', created_at: '2026-05-12T20:00:00Z',
    groups: [{ name: 'Checkout', script_id: 'script-1', vusers: 10, initialization: 'at_runtime' }],
  } as never
}

function buttonNamed(name: string): HTMLButtonElement {
  const button = Array.from(document.body.querySelectorAll('button')).find(candidate => candidate.textContent === name)
  if (!button) {
    throw new Error(`no button named ${name}`)
  }
  return button
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
