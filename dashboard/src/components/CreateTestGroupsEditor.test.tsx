// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestGroupsEditor from './CreateTestGroupsEditor'
import { clampVUsersForRole, validateAggregateVUserDemand } from './observerGroupModel'
import { type LoadStageDraft, type ScenarioDraftGroup, type StoredScript } from '../types'

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

  it('clears observer role when its browser script is replaced by a non-browser script', async () => {
    const onUpdateGroup = vi.fn()
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[draftGroup({ scriptId: 'script-browser', role: 'observer' })]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })

    const scriptSelect = Array.from(container.querySelectorAll('select')).find(select => (
      Array.from(select.options).some(option => option.value === 'script-http')
    ))
    expect(scriptSelect).toBeTruthy()
    if (!scriptSelect) return
    await act(async () => {
      setSelectValue(scriptSelect, 'script-http')
    })

    expect(onUpdateGroup).toHaveBeenCalledWith(0, { scriptId: 'script-http', role: 'load' })
  })

  it('enforces the observer boundary while preserving the load-group maximum', async () => {
    const onUpdateGroup = vi.fn()
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[draftGroup({ scriptId: 'script-browser', role: 'observer', vusers: 10 })]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })

    const observerVusers = container.querySelector<HTMLInputElement>('input[type="number"]')
    expect(observerVusers?.max).toBe('10')
    expect(container.textContent).toContain('Observer canaries are capped at 10 VUsers.')
    expect(observerVusers).toBeTruthy()
    if (!observerVusers) return
    await act(async () => {
      setInputValue(observerVusers, '11')
    })
    expect(onUpdateGroup).toHaveBeenCalledWith(0, { vusers: 10 })

    onUpdateGroup.mockClear()
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[draftGroup({ scriptId: 'script-browser', role: 'load', vusers: 1000000 })]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })
    expect(container.querySelector<HTMLInputElement>('input[type="number"]')?.max).toBe('1000000')

    await act(async () => {
      setSelectValue(roleSelect(), 'observer')
    })
    expect(onUpdateGroup).toHaveBeenCalledWith(0, { role: 'observer', vusers: 10 })
  })

  // Guards the seam, not one value: the observer role switch has two consumers
  // (this editor and TestHub) and both must land on exactly what the shared
  // clampVUsersForRole invariant says -- for the group VUsers *and* for every
  // load-stage target, since the observer envelope is validated against the
  // group's peak demand (effectiveGroupVUsers). Expectations are derived from
  // the shared helper, so re-deriving the rule locally in either consumer, or
  // clamping only one half of the demand, fails this test.
  it('switches to the observer role through the shared VUser clamp invariant', async () => {
    for (const vusers of [0, 7.5, 10, 25, 1_000_000]) {
      for (const stages of [undefined, stageSchedule(vusers)]) {
        const onUpdateGroup = vi.fn()
        await act(async () => {
          root.render(
            <CreateTestGroupsEditor
              scenarioMode="vuser_driven"
              groups={[draftGroup({ scriptId: 'script-browser', role: 'load', vusers, ...(stages ? { stages } : {}) })]}
              scripts={scripts}
              onAddGroup={vi.fn()}
              onUpdateGroup={onUpdateGroup}
              onRemoveGroup={vi.fn()}
            />,
          )
        })

        await act(async () => {
          setSelectValue(roleSelect(), 'observer')
        })

        expect(onUpdateGroup).toHaveBeenCalledTimes(1)
        const patch = onUpdateGroup.mock.calls[0][1] as Partial<ScenarioDraftGroup>
        expect(patch.role).toBe('observer')
        // Whether the editor emits a vusers patch or leaves the value alone, the
        // group it hands back must satisfy the shared observer envelope.
        expect(patch.vusers ?? vusers).toBe(clampVUsersForRole(vusers, 'observer', 1))
        // A staged group's peak demand is its stage targets, so those must be
        // clamped by the same helper or the draft becomes unsaveable here.
        expect((patch.stages ?? stages)?.map(stage => stage.target_vusers)).toEqual(
          stages?.map(stage => (
            stage.target_vusers === undefined
              ? undefined
              : clampVUsersForRole(stage.target_vusers, 'observer')
          )),
        )
      }
    }
  })

  // A staged group's VUsers value is not free: validateAggregateVUserDemand
  // rejects anything other than the stage peak, and this surface renders no
  // stage editor. An editable field that accepts exactly one value is a trap,
  // so it is presented the way the run-group editor presents it: read-only,
  // annotated with the peak that owns it.
  it('locks the VUsers field to the peak stage target for staged groups', async () => {
    const onUpdateGroup = vi.fn()
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[draftGroup({ scriptId: 'script-browser', vusers: 100, stages: rampedStageSchedule() })]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })

    const vusersInput = container.querySelector<HTMLInputElement>('input[type="number"]')
    expect(vusersInput?.value).toBe('100')
    expect(vusersInput?.disabled).toBe(true)
    expect(container.textContent).toContain('fixed at its peak stage target of 100 VUsers')
  })

  // The lock must not become its own trap: a group whose VUsers already
  // disagrees with its stage peak is rejected by the shared validator, so the
  // field stays editable until the invariant it enforces actually holds.
  it('keeps the VUsers field editable while a staged group disagrees with its peak', async () => {
    const onUpdateGroup = vi.fn()
    const stages = rampedStageSchedule()
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[draftGroup({ scriptId: 'script-browser', vusers: 40, stages })]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })
    expect(validateAggregateVUserDemand([{ vusers: 40, stages }])).toContain('peak stage target of 100')

    const vusersInput = container.querySelector<HTMLInputElement>('input[type="number"]')
    expect(vusersInput?.disabled).toBe(false)
    expect(container.textContent).toContain('must match its peak stage target of 100 VUsers')
    expect(vusersInput).toBeTruthy()
    if (!vusersInput) return
    await act(async () => {
      setInputValue(vusersInput, '100')
    })
    expect(onUpdateGroup).toHaveBeenCalledWith(0, { vusers: 100 })
  })

  // The observer clamp rewrites the whole stage schedule, not just its peak,
  // and this surface renders no stage editor -- so the switch back to load must
  // undo the editor's own clamp. Otherwise a mis-click permanently flattens a
  // 50 -> 100 ramp into a flat 10 and still validates, which is worse than the
  // loud rejection the clamp was introduced to remove.
  it('restores the pre-observer stage schedule and VUsers when the role returns to load', async () => {
    const onUpdateGroup = vi.fn()
    const stages = rampedStageSchedule()
    const group = draftGroup({ scriptId: 'script-browser', role: 'load', vusers: 100, stages })
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[group]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })

    await act(async () => {
      setSelectValue(roleSelect(), 'observer')
    })
    const clamped = { ...group, ...(onUpdateGroup.mock.calls[0][1] as Partial<ScenarioDraftGroup>) }
    expect(clamped.vusers).toBe(10)
    expect(clamped.stages?.map(stage => stage.target_vusers)).toEqual([10, 10, undefined])

    onUpdateGroup.mockClear()
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[clamped]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })
    await act(async () => {
      setSelectValue(roleSelect(), 'load')
    })

    const restored = { ...clamped, ...(onUpdateGroup.mock.calls[0][1] as Partial<ScenarioDraftGroup>) }
    expect(restored.role).toBe('load')
    expect(restored.vusers).toBe(100)
    expect(restored.stages?.map(stage => stage.target_vusers)).toEqual([50, 100, undefined])
    expect(validateAggregateVUserDemand([restored])).toBeUndefined()
  })

  // Restoring must undo the editor's clamp, never a deliberate edit made while
  // the group was an observer.
  it('leaves a deliberately edited observer group alone when the role returns to load', async () => {
    const onUpdateGroup = vi.fn()
    const group = draftGroup({ scriptId: 'script-browser', role: 'load', vusers: 100, stages: rampedStageSchedule() })
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[group]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })
    await act(async () => {
      setSelectValue(roleSelect(), 'observer')
    })

    const edited = {
      ...group,
      ...(onUpdateGroup.mock.calls[0][1] as Partial<ScenarioDraftGroup>),
      vusers: 4,
      stages: stageSchedule(4),
    }
    onUpdateGroup.mockClear()
    await act(async () => {
      root.render(
        <CreateTestGroupsEditor
          scenarioMode="vuser_driven"
          groups={[edited]}
          scripts={scripts}
          onAddGroup={vi.fn()}
          onUpdateGroup={onUpdateGroup}
          onRemoveGroup={vi.fn()}
        />,
      )
    })
    await act(async () => {
      setSelectValue(roleSelect(), 'load')
    })

    expect(onUpdateGroup).toHaveBeenCalledWith(0, { role: 'load' })
  })
})

function rampedStageSchedule(): LoadStageDraft[] {
  return [
    { type: 'ramp_up', duration: '1m', target_vusers: 50 },
    { type: 'hold', duration: '5m', target_vusers: 100 },
    { type: 'ramp_down', duration: '1m' },
  ]
}

function stageSchedule(peak: number): LoadStageDraft[] {
  return [
    { type: 'ramp_up', duration: '1m', target_vusers: peak },
    { type: 'hold', duration: '5m', target_vusers: peak },
    { type: 'ramp_down', duration: '1m' },
  ]
}

function setSelectValue(select: HTMLSelectElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('change', { bubbles: true }))
}
