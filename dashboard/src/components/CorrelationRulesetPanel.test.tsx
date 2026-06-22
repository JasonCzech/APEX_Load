// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CorrelationRulesetPanel from './CorrelationRulesetPanel'
import { CorrelationRuleset } from '../types'

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

let container: HTMLElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})
afterEach(() => {
  act(() => root.unmount())
  container.remove()
  vi.clearAllMocks()
})

async function flush() {
  await act(async () => { await Promise.resolve(); await Promise.resolve() })
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

function setInput(placeholder: string, value: string) {
  const input = Array.from(container.querySelectorAll('input')).find(i => i.getAttribute('placeholder') === placeholder) as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('CorrelationRulesetPanel', () => {
  it('lists rulesets and discovers one from a script', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.discoverCorrelationRulesetFromScript.mockResolvedValue(ruleset({ id: 'r2' }))

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()

    expect(container.textContent).toContain('Checkout correlations')
    expect(container.textContent).toContain('1 rule')

    setInput('Discover from script id…', 's7')
    await act(async () => { findButton('Discover from script')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(apiMock.discoverCorrelationRulesetFromScript).toHaveBeenCalledWith(expect.objectContaining({ script_id: 's7' }))
  })

  it('previews then applies a ruleset to a script', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.applyCorrelationRulesetToScript
      .mockResolvedValueOnce({ ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: false, diffs: [{ kind: 'extractor_added', after: 'login' }] })
      .mockResolvedValueOnce({ ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: true })

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()

    // Expand the ruleset to reveal the apply form.
    const title = container.querySelector('.pp-row-title-button') as HTMLButtonElement
    await act(async () => { title.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    setInput('Apply to script id…', 's9')
    await act(async () => { findButton('Preview')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(apiMock.applyCorrelationRulesetToScript).toHaveBeenCalledWith('r1', { script_id: 's9', apply: false })
    expect(container.textContent).toContain('1 rule matched')

    await act(async () => { findButton('Apply')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    expect(apiMock.applyCorrelationRulesetToScript).toHaveBeenCalledWith('r1', { script_id: 's9', apply: true })
    expect(container.textContent).toContain('applied 1 rule')
  })

  it('keeps the typed script id and preview when the apply input is re-focused', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.applyCorrelationRulesetToScript
      .mockResolvedValueOnce({ ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: false, diffs: [{ kind: 'extractor_added', after: 'login' }] })

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()
    const title = container.querySelector('.pp-row-title-button') as HTMLButtonElement
    await act(async () => { title.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    setInput('Apply to script id…', 's9')
    await act(async () => { findButton('Preview')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()
    expect(container.textContent).toContain('1 rule matched')

    // Click back into the same field. onFocus must NOT wipe the typed id/preview.
    const input = Array.from(container.querySelectorAll('input')).find(i => i.getAttribute('placeholder') === 'Apply to script id…') as HTMLInputElement
    await act(async () => { input.dispatchEvent(new FocusEvent('focusin', { bubbles: true })) })
    await flush()
    expect(input.value).toBe('s9')
    expect(container.textContent).toContain('1 rule matched')
    expect(findButton('Apply')!.disabled).toBe(false)
  })

  it('disables Apply once the script id is edited until a fresh Preview', async () => {
    apiMock.listCorrelationRulesets.mockResolvedValue({ correlation_rulesets: [ruleset({})], count: 1 })
    apiMock.applyCorrelationRulesetToScript
      .mockResolvedValueOnce({ ruleset_id: 'r1', script_id: 's9', matched_rules: 1, applied: false, diffs: [{ kind: 'extractor_added', after: 'login' }] })

    await act(async () => { root.render(<CorrelationRulesetPanel projectId="proj-a" canManage />) })
    await flush()
    const title = container.querySelector('.pp-row-title-button') as HTMLButtonElement
    await act(async () => { title.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()

    // Preview s9 → Apply becomes enabled and the diff is shown.
    setInput('Apply to script id…', 's9')
    await act(async () => { findButton('Preview')!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()
    expect(findButton('Apply')!.disabled).toBe(false)
    expect(container.textContent).toContain('1 rule matched')

    // Edit the script id without re-previewing → Apply must disable and the stale
    // preview must clear, so Apply can never commit against an unpreviewed script.
    setInput('Apply to script id…', 's10')
    await flush()
    expect(findButton('Apply')!.disabled).toBe(true)
    expect(container.textContent).not.toContain('1 rule matched')
  })
})
