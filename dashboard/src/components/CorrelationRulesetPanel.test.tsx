// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CorrelationRulesetPanel from './CorrelationRulesetPanel'
import { CorrelationRuleset } from '../types'
import { ApiError } from '../hooks/apiClient'
import { NavigationGuardRegisterContext, type NavigationGuard } from '../hooks/NavigationGuardProvider'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listCorrelationRulesets: vi.fn(),
  getCorrelationRuleset: vi.fn(),
  createCorrelationRuleset: vi.fn(),
  updateCorrelationRuleset: vi.fn(),
  deleteCorrelationRuleset: vi.fn(),
  discoverCorrelationRulesetFromScript: vi.fn(),
  applyCorrelationRulesetToScript: vi.fn(),
}

vi.mock('../hooks/api/useCorrelationRulesetApi', () => ({
  useCorrelationRulesetApi: () => apiMock,
}))

const scriptApiMock = { listScripts: vi.fn() }
vi.mock('../hooks/api/useScriptApi', () => ({
  useScriptApi: () => scriptApiMock,
}))
const projectScripts = ['s1', 's2', 's7', 's9', 's10', 'script-b', 'script-draft'].map(id => ({ id, name: `Recorded ${id}`, protocol: 'web' }))

let container: HTMLElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  scriptApiMock.listScripts.mockResolvedValue({ scripts: projectScripts, count: projectScripts.length })
})
afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.clearAllMocks()
})

async function flush() {
  await act(async () => { await Promise.resolve(); await Promise.resolve() })
}

async function waitForDestructiveDialog() {
  // The modal is lazy-loaded. Wait for its input rather than a fixed number of
  // event-loop turns, which can finish before the module loads under suite load.
  await vi.waitFor(async () => {
    await act(async () => { await new Promise(resolve => window.setTimeout(resolve, 0)) })
    expect(container.querySelector('input[placeholder="Operational reason"]')).not.toBeNull()
  }, { timeout: 3000, interval: 20 })
}

function ruleset(overrides: Partial<CorrelationRuleset>): CorrelationRuleset {
  return {
    id: 'r1', project_id: 'proj-a', name: 'Checkout correlations', source: 'script:s1',
    rules: [{ name: 'csrf', source: 'response_body', extract_type: 'boundary', expression: 'name="csrf" value="(.+?)"', confidence: 0.9 }],
    created_at: '', updated_at: '', ...overrides,
  }
}

function findButton(text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(b => b.textContent?.trim() === text) as HTMLButtonElement | undefined
}

function scriptSelect(label = 'Script'): HTMLSelectElement | undefined {
  const picker = Array.from(container.querySelectorAll('.script-picker')).find(element => element.querySelector('span')?.textContent === label)
  return picker?.querySelector('select') ?? undefined
}

// The picker lists the project's scripts once its (debounced) search settles.
async function pickScript(id: string, label = 'Script') {
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
  await flush()
  const select = scriptSelect(label)
  expect(select).toBeTruthy()
  act(() => {
    select!.value = id
    select!.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

async function discoverFrom(id: string) {
  await act(async () => { findButton('Discover from a script')!.click() })
  await pickScript(id)
}

describe('CorrelationRulesetPanel', () => {
  it('does not let a slow prior-project response overwrite the current project', async () => {
    let resolveA!: (value: { correlation_rulesets: CorrelationRuleset[]; count: number }) => void
    let resolveB!: (value: { correlation_rulesets: CorrelationRuleset[]; count: number }) => void
    const responseA = new Promise<{ correlation_rulesets: CorrelationRuleset[]; count: number }>(resolve => { resolveA = resolve })
    const responseB = new Promise<{ correlation_rulesets: CorrelationRuleset[]; count: number }>(resolve => { resolveB = resolve })
    apiMock.listCorrelationRulesets.mockImplementation(({ project_id }: { project_id: string }) => (
      project_id === 'proj-a' ? responseA : responseB
    ))

    act(() => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()
    act(() => { root.render(<CorrelationRulesetPanel projectId="proj-b" canManage />) })
    await flush()

    await act(async () => {
      resolveB({ correlation_rulesets: [ruleset({ id: 'r-b', project_id: 'proj-b', name: 'Project B rules' })], count: 1 })
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Project B rules')

    await act(async () => {
      resolveA({ correlation_rulesets: [ruleset({ id: 'r-a', project_id: 'proj-a', name: 'Project A rules' })], count: 1 })
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Project B rules')
    expect(container.textContent).not.toContain('Project A rules')
  })

  it('lists rulesets and discovers one from a script', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.discoverCorrelationRulesetFromScript.mockResolvedValue(ruleset({ id: 'r2' }))

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()

    expect(container.textContent).toContain('Checkout correlations')
    expect(container.textContent).toContain('1 rule')

    expect(container.querySelector('.pp-create')).toBeNull()
    await discoverFrom('s7')
    expect(scriptApiMock.listScripts).toHaveBeenCalledWith('proj-a', undefined, { search: '', limit: 50 })
    await act(async () => { findButton('Discover rules')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(apiMock.discoverCorrelationRulesetFromScript).toHaveBeenCalledWith(expect.objectContaining({ script_id: 's7' }))
  })

  it('does not dispatch duplicate discovery mutations on rapid clicks', async () => {
    let resolveDiscover!: (value: CorrelationRuleset) => void
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.discoverCorrelationRulesetFromScript.mockReturnValue(new Promise(resolve => { resolveDiscover = resolve }))

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()
    await discoverFrom('s7')
    const discover = findButton('Discover rules')!
    act(() => {
      discover.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      discover.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.discoverCorrelationRulesetFromScript).toHaveBeenCalledTimes(1)
    await act(async () => {
      resolveDiscover(ruleset({ id: 'r2' }))
      await Promise.resolve()
    })
  })

  it('opens discovery in place of its button and returns focus to it on Cancel', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [], count: 0 })
    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()
    await act(async () => { findButton('Discover from a script')!.click() })
    expect(findButton('Discover from a script')).toBeUndefined()
    expect(document.activeElement?.closest('.pp-create')).not.toBeNull()

    await act(async () => { findButton('Cancel')!.click() })
    expect(container.querySelector('.pp-create')).toBeNull()
    expect(document.activeElement).toBe(findButton('Discover from a script'))
  })

  it('leaves no unsaved draft once an Apply has gone through', async () => {
    let registration: { guard: NavigationGuard; dirty: boolean } | undefined
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.applyCorrelationRulesetToScript
      .mockResolvedValueOnce({ ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: false, preview_token: 'preview-r1-s9', diffs: [] })
      .mockResolvedValueOnce({ ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: true, preview_token: 'applied-r1-s9' })
    await act(async () => {
      root.render(
        <NavigationGuardRegisterContext.Provider value={(guard, dirty) => {
          registration = { guard, dirty }
          return () => undefined
        }}>
          <CorrelationRulesetPanel projectId="proj-a" canManage />
        </NavigationGuardRegisterContext.Provider>,
      )
    })
    await flush()
    await act(async () => { (container.querySelector('.pp-row-title-button') as HTMLButtonElement).click() })
    await pickScript('s9', 'Apply to script')
    await act(async () => { findButton('Preview')!.click() })
    await flush()
    expect(registration?.dirty).toBe(true)

    await act(async () => { findButton('Apply')!.click() })
    await flush()
    expect(container.textContent).toContain('applied 1 rule')
    expect(registration?.dirty).toBe(false)
    // Its token described the script before the apply; a second Apply would only be refused as stale.
    expect(findButton('Apply')?.disabled).toBe(true)
    expect(findButton('Preview')?.disabled).toBe(false)
  })

  it('returns focus to the opener once a discovery completes', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [], count: 0 })
    apiMock.discoverCorrelationRulesetFromScript.mockResolvedValue(ruleset({ id: 'r2', name: 'Discovered' }))
    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()
    await discoverFrom('s7')
    const discover = findButton('Discover rules')!
    discover.focus()
    await act(async () => { discover.click() })
    await flush()
    expect(container.querySelector('.pp-create')).toBeNull()
    expect(document.activeElement).toBe(findButton('Discover from a script'))
  })

  it('previews then applies a ruleset to a script', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.applyCorrelationRulesetToScript
	  .mockResolvedValueOnce({ ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: false, preview_token: 'preview-r1-s9', diffs: [{ kind: 'extractor_added', path: '/actions/0/extractors' }] })
	  .mockResolvedValueOnce({ ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: true, preview_token: 'applied-r1-s9' })

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()

    // Expand the ruleset to reveal the apply form.
    const title = container.querySelector('.pp-row-title-button') as HTMLButtonElement
    await act(async () => { title.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    await pickScript('s9', 'Apply to script')
    await act(async () => { findButton('Preview')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

	expect(apiMock.applyCorrelationRulesetToScript).toHaveBeenCalledWith('r1', { script_id: 's9', apply: false, preview_token: undefined })
    expect(container.textContent).toContain('1 rule matched')

    await act(async () => { findButton('Apply')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

	expect(apiMock.applyCorrelationRulesetToScript).toHaveBeenCalledWith('r1', { script_id: 's9', apply: true, preview_token: 'preview-r1-s9' })
    expect(container.textContent).toContain('applied 1 rule')
  })

  it('keeps the chosen script and preview when the ruleset is collapsed and expanded again', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.applyCorrelationRulesetToScript
	  .mockResolvedValueOnce({ ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: false, preview_token: 'preview-r1-s9', diffs: [{ kind: 'extractor_added', path: '/actions/0/extractors' }] })

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()
    const title = container.querySelector('.pp-row-title-button') as HTMLButtonElement
    await act(async () => { title.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    await pickScript('s9', 'Apply to script')
    await act(async () => { findButton('Preview')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()
    expect(container.textContent).toContain('1 rule matched')

    // Collapsing unmounts the picker; reopening must show the same script, not a blank list over a hidden choice.
    await act(async () => { title.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(scriptSelect('Apply to script')).toBeUndefined()
    await act(async () => { title.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
    await flush()
    expect(scriptSelect('Apply to script')?.value).toBe('s9')
    expect(scriptSelect('Apply to script')?.selectedOptions[0]?.textContent).toBe('Recorded s9 · web')
    expect(container.textContent).toContain('1 rule matched')
    expect(findButton('Apply')!.disabled).toBe(false)
  })

  it('disables Apply once another script is chosen until a fresh Preview', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.applyCorrelationRulesetToScript
	  .mockResolvedValueOnce({ ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: false, preview_token: 'preview-r1-s9', diffs: [{ kind: 'extractor_added', path: '/actions/0/extractors' }] })

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()
    const title = container.querySelector('.pp-row-title-button') as HTMLButtonElement
    await act(async () => { title.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    // Preview s9 → Apply becomes enabled and the diff is shown.
    await pickScript('s9', 'Apply to script')
    await act(async () => { findButton('Preview')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()
    expect(findButton('Apply')!.disabled).toBe(false)
    expect(container.textContent).toContain('1 rule matched')

    // Choose another script without re-previewing → Apply must disable and the stale
    // preview must clear, so Apply can never commit against an unpreviewed script.
    await pickScript('s10', 'Apply to script')
    await flush()
    expect(findButton('Apply')!.disabled).toBe(true)
    expect(container.textContent).not.toContain('1 rule matched')
  })

  it('discards a preview response after switching to another ruleset', async () => {
    let resolvePreview!: (value: {
      ruleset_id: string
      script_id: string
      matched_rules: number
      applied: boolean
      preview_token: string
      diffs: { kind: string; path: string }[]
    }) => void
    apiMock.listCorrelationRulesets.mockResolvedValue({
      correlation_rulesets: [
        ruleset({ id: 'r1', name: 'First ruleset' }),
        ruleset({ id: 'r2', name: 'Second ruleset' }),
      ],
      count: 2,
    })
    apiMock.applyCorrelationRulesetToScript.mockReturnValue(new Promise(resolve => { resolvePreview = resolve }))

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()
    const titles = Array.from(container.querySelectorAll<HTMLButtonElement>('.pp-row-title-button'))
    await act(async () => { titles[0].dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await pickScript('s1', 'Apply to script')
    act(() => { findButton('Preview')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    await act(async () => { titles[1].dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    // The other ruleset's picker waits for the request: a change now would drop its answer.
    expect(scriptSelect('Apply to script')?.disabled).toBe(true)
    await act(async () => {
      resolvePreview({
        ruleset_id: 'r1',
        script_id: 's1',
        matched_rules: 1,
        applied: false,
        preview_token: 'preview-r1-s1',
        diffs: [{ kind: 'extractor_added', path: '/actions/0/extractors' }],
      })
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('1 rule matched')
    expect(findButton('Apply')!.disabled).toBe(true)
  })

  it('does not dispatch duplicate apply commits on rapid clicks', async () => {
    let resolveCommit!: (value: {
      ruleset_id: string
      script_id: string
      matched_rules: number
      applied: boolean
      preview_token: string
    }) => void
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.applyCorrelationRulesetToScript
      .mockResolvedValueOnce({
        ruleset_id: 'r1',
        script_id: 's9',
        matched_rules: 1,
        applied: false,
        preview_token: 'preview-r1-s9',
        diffs: [],
      })
      .mockReturnValueOnce(new Promise(resolve => { resolveCommit = resolve }))

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()
    const title = container.querySelector('.pp-row-title-button') as HTMLButtonElement
    await act(async () => { title.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await pickScript('s9', 'Apply to script')
    await act(async () => { findButton('Preview')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    const apply = findButton('Apply')!
    act(() => {
      apply.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      apply.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(apiMock.applyCorrelationRulesetToScript).toHaveBeenCalledTimes(2)

    await act(async () => {
      resolveCommit({
        ruleset_id: 'r1',
        script_id: 's9',
        matched_rules: 1,
        applied: true,
        preview_token: 'applied-r1-s9',
      })
      await Promise.resolve()
    })
  })

  it('keeps a discovered ruleset when the follow-up catalog refresh fails', async () => {
    apiMock.listCorrelationRulesets
      .mockResolvedValueOnce({ correlation_rulesets: [ruleset({})], count: 1 })
      .mockRejectedValueOnce(new ApiError('ruleset refresh unavailable', 503))
    apiMock.discoverCorrelationRulesetFromScript.mockResolvedValue(ruleset({ id: 'r2', name: 'Newly discovered' }))
    await act(async () => root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />))
    await flush()

    await discoverFrom('s7')
    await act(async () => findButton('Discover rules')?.click())
    await flush()

    expect(container.textContent).toContain('Checkout correlations')
    expect(container.textContent).toContain('Newly discovered')
    expect(container.textContent).toContain('ruleset refresh unavailable')
    expect(container.textContent).toContain('Retry rulesets')
    expect(apiMock.discoverCorrelationRulesetFromScript).toHaveBeenCalledTimes(1)
  })

  it('does not resurrect a deleted ruleset from a stale catalog response', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.deleteCorrelationRuleset.mockResolvedValue({ deleted: 'r1' })
    await act(async () => root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />))
    await flush()

    await act(async () => findButton('Delete')?.click())
    await waitForDestructiveDialog()
    const reason = container.querySelector<HTMLInputElement>('input[placeholder="Operational reason"]')
    const confirmation = container.querySelector<HTMLInputElement>('input[placeholder="Checkout correlations"]')
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    act(() => {
      setter?.call(reason, 'obsolete rules')
      reason?.dispatchEvent(new Event('input', { bubbles: true }))
      setter?.call(confirmation, 'Checkout correlations')
      confirmation?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => findButton('Delete ruleset')?.click())
    await flush()

    expect(apiMock.deleteCorrelationRuleset).toHaveBeenCalledWith('r1')
    expect(container.textContent).not.toContain('Checkout correlations')
    expect(container.textContent).toContain('No correlation rulesets yet')
  })

  it('clears an old preview token when re-previewing fails', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.applyCorrelationRulesetToScript
      .mockResolvedValueOnce({
        ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: false,
        preview_token: 'preview-r1-s9', diffs: [{ kind: 'extractor_added', path: '/actions/0/extractors' }],
      })
      .mockRejectedValueOnce(new ApiError('preview unavailable', 503))
    await act(async () => root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />))
    await flush()
    const title = container.querySelector('.pp-row-title-button') as HTMLButtonElement
    await act(async () => title.click())
    await pickScript('s9', 'Apply to script')
    await act(async () => findButton('Preview')?.click())
    await flush()
    expect(findButton('Apply')?.disabled).toBe(false)

    await act(async () => findButton('Preview')?.click())
    await flush()
    expect(container.textContent).toContain('preview unavailable')
    expect(findButton('Apply')?.disabled).toBe(true)
    expect(container.textContent).not.toContain('1 rule matched')
  })

  it('drops the apply draft of a ruleset once it is deleted', async () => {
    let registration: { guard: NavigationGuard; dirty: boolean } | undefined
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.deleteCorrelationRuleset.mockResolvedValue({ deleted: 'r1' })
    await act(async () => {
      root.render(
        <NavigationGuardRegisterContext.Provider value={(guard, dirty) => {
          registration = { guard, dirty }
          return () => undefined
        }}>
          <CorrelationRulesetPanel projectId="proj-a" canManage />
        </NavigationGuardRegisterContext.Provider>,
      )
    })
    await flush()
    await act(async () => { (container.querySelector('.pp-row-title-button') as HTMLButtonElement).click() })
    await pickScript('s9', 'Apply to script')
    expect(registration?.dirty).toBe(true)

    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [], count: 0 })
    await act(async () => findButton('Delete')?.click())
    await waitForDestructiveDialog()
    const reason = container.querySelector<HTMLInputElement>('input[placeholder="Operational reason"]')
    const confirmation = container.querySelector<HTMLInputElement>('input[placeholder="Checkout correlations"]')
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    act(() => {
      setter?.call(reason, 'obsolete rules')
      reason?.dispatchEvent(new Event('input', { bubbles: true }))
      setter?.call(confirmation, 'Checkout correlations')
      confirmation?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => findButton('Delete ruleset')?.click())
    await flush()

    expect(apiMock.deleteCorrelationRuleset).toHaveBeenCalledWith('r1')
    expect(registration?.dirty).toBe(false)
  })

  it('keeps destructive confirmation state when deletion fails', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.deleteCorrelationRuleset.mockRejectedValue(new ApiError('delete rejected', 409))
    await act(async () => root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />))
    await flush()
    await act(async () => findButton('Delete')?.click())
    await waitForDestructiveDialog()
    const reason = container.querySelector<HTMLInputElement>('input[placeholder="Operational reason"]')
    const confirmation = container.querySelector<HTMLInputElement>('input[placeholder="Checkout correlations"]')
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    act(() => {
      setter?.call(reason, 'obsolete rules')
      reason?.dispatchEvent(new Event('input', { bubbles: true }))
      setter?.call(confirmation, 'Checkout correlations')
      confirmation?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => findButton('Delete ruleset')?.click())
    await flush()

    expect(container.querySelector('[role="dialog"]')).not.toBeNull()
    expect(reason?.value).toBe('obsolete rules')
    expect(confirmation?.value).toBe('Checkout correlations')
    expect(container.textContent).toContain('delete rejected')
  })

  it('accepts a newer ruleset snapshot over an older mutation response', async () => {
    apiMock.listCorrelationRulesets
      .mockResolvedValueOnce({ correlation_rulesets: [ruleset({})], count: 1 })
      .mockResolvedValueOnce({
        correlation_rulesets: [
          ruleset({}),
          ruleset({ id: 'r2', name: 'Server-refined rules', updated_at: '2026-08-03T12:00:00Z' }),
        ],
        count: 2,
      })
    apiMock.discoverCorrelationRulesetFromScript.mockResolvedValue(
      ruleset({ id: 'r2', name: 'Initial discovery', updated_at: '2026-08-03T11:00:00Z' }),
    )
    await act(async () => root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />))
    await flush()
    await discoverFrom('s7')
    await act(async () => findButton('Discover rules')?.click())
    await flush()

    expect(container.textContent).toContain('Server-refined rules')
    expect(container.textContent).not.toContain('Initial discovery')
  })

  it('loads every ruleset page instead of truncating the catalog at 100', async () => {
    const firstPage = Array.from({ length: 100 }, (_, index) => ruleset({ id: `r-${index}`, name: `Ruleset ${index}` }))
    apiMock.listCorrelationRulesets
      .mockResolvedValueOnce({ correlation_rulesets: firstPage, count: 100, limit: 100, offset: 0 })
      .mockResolvedValueOnce({ correlation_rulesets: [ruleset({ id: 'r-100', name: 'Ruleset 100' })], count: 1, limit: 100, offset: 100 })

    await act(async () => root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />))
    await flush()

    expect(apiMock.listCorrelationRulesets).toHaveBeenNthCalledWith(1, { project_id: 'proj-a', limit: 100, offset: 0 })
    expect(apiMock.listCorrelationRulesets).toHaveBeenNthCalledWith(2, { project_id: 'proj-a', limit: 100, offset: 100 })
    expect(container.textContent).toContain('Ruleset 100')
  })

  it('does not add a discovered ruleset returned for another project', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [], count: 0 })
    apiMock.discoverCorrelationRulesetFromScript.mockResolvedValue(ruleset({ id: 'r-b', project_id: 'proj-b', name: 'Project B rules' }))
    await act(async () => root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />))
    await flush()
    await discoverFrom('script-b')
    await act(async () => findButton('Discover rules')?.click())
    await flush()

    expect(container.textContent).toContain('different project')
    expect(container.textContent).not.toContain('Project B rules')
    expect(scriptSelect()?.value).toBe('script-b')
  })

  it('registers discovery and apply edits with the global navigation guard', async () => {
    let registration: { guard: NavigationGuard; dirty: boolean } | undefined
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [], count: 0 })
    await act(async () => {
      root.render(
        <NavigationGuardRegisterContext.Provider value={(guard, dirty) => {
          registration = { guard, dirty }
          return () => undefined
        }}>
          <CorrelationRulesetPanel projectId="proj-a" canManage />
        </NavigationGuardRegisterContext.Provider>,
      )
    })
    await flush()
    await act(async () => { findButton('Discover from a script')!.click() })
    expect(registration?.dirty).toBe(false)
    await pickScript('script-draft')
    expect(registration?.dirty).toBe(true)

    act(() => registration?.guard.discard())
    expect(container.querySelector('.pp-create')).toBeNull()
    await act(async () => { findButton('Discover from a script')!.click() })
    expect(scriptSelect()?.value).toBe('')
  })
})
