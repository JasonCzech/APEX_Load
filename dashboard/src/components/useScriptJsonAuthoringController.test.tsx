// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { formatScriptJson } from './scriptEditorJsonModel'
import {
  useJsonParameterMenu,
  useScriptJsonDraftController,
  useScriptJsonNavigationController,
} from './useScriptJsonAuthoringController'

// Counts the O(script) passes the controllers ask the JSON model for, so that
// re-introducing a per-keystroke serialization or dropping a memo fails here.
const workCounts = vi.hoisted(() => ({ format: 0, outline: 0, parse: 0, reserialize: 0 }))

vi.mock('./scriptEditorJsonModel', async importOriginal => {
  const actual = await importOriginal<typeof import('./scriptEditorJsonModel')>()
  return {
    ...actual,
    formatScriptJson: (...args: Parameters<typeof actual.formatScriptJson>) => {
      workCounts.format += 1
      return actual.formatScriptJson(...args)
    },
    buildScriptOutlineSummary: (...args: Parameters<typeof actual.buildScriptOutlineSummary>) => {
      workCounts.outline += 1
      return actual.buildScriptOutlineSummary(...args)
    },
    parseScriptJsonDraft: (...args: Parameters<typeof actual.parseScriptJsonDraft>) => {
      workCounts.parse += 1
      return actual.parseScriptJsonDraft(...args)
    },
    // updateScriptJsonText re-serializes the whole structured script to decide
    // whether the pane is dirty, so a keystroke handler must not reach for it.
    updateScriptJsonText: (...args: Parameters<typeof actual.updateScriptJsonText>) => {
      workCounts.reserialize += 1
      return actual.updateScriptJsonText(...args)
    },
  }
})

function script(name = 'controller-script'): ScriptDefinition {
  return {
    name,
    version: '1.0.0',
    protocol: 'http',
    config: {
      base_url: 'https://example.test',
      think_time: { type: 'fixed', mean_ms: 0, ignore: true },
      pacing: { type: 'none' },
      run_logic: { auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
    },
    actions: [{ name: 'Get user', type: 'GET', method: 'GET', target: '/users/{{param.user_id}}' }],
    parameters: [{ name: 'user_id', type: 'list', values: ['42'] }],
  }
}

function DraftHarness() {
  const [structured, setStructured] = useState(() => script())
  const [jsonText, setJsonText] = useState(() => formatScriptJson(structured))
  const [jsonDirty, setJsonDirty] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [invalidCount, setInvalidCount] = useState(0)
  const controller = useScriptJsonDraftController({
    jsonDirty,
    jsonText,
    script: structured,
    setError,
    setJsonDirty,
    setJsonText,
    setScript: setStructured,
    setSuccess,
    onInvalidDraft: () => setInvalidCount(count => count + 1),
  })
  return (
    <div>
      <textarea aria-label="JSON draft" value={jsonText} onChange={event => controller.changeJsonDraft(event.currentTarget.value)} />
      <span data-testid="status">{controller.status}</span>
      <span data-testid="draft-name">{controller.currentScript.name}</span>
      <span data-testid="structured-name">{structured.name}</span>
      <span data-testid="error">{error}</span>
      <span data-testid="success">{success}</span>
      <span data-testid="invalid-count">{invalidCount}</span>
      <button type="button" onClick={controller.applyJsonDraft}>Apply</button>
      <button type="button" onClick={controller.formatJsonDraft}>Format</button>
      <button type="button" onClick={controller.resetJsonDraft}>Reset</button>
      <button type="button" onClick={() => setStructured(script('structured-update'))}>Structured update</button>
    </div>
  )
}

function SelectionHarness() {
  const [draft, setDraft] = useState('')
  const controller = useJsonParameterMenu({
    parameters: [{ name: 'user_id', type: 'list', values: ['42'] }],
    onBeginParameterEdit: value => setDraft(JSON.stringify(value)),
    onChooseExistingParameter: value => setDraft(`existing:${value.selectedText}`),
    onReviewOccurrences: value => setDraft(`review:${value.selectedText}`),
  })
  return (
    <div>
      <button type="button" onClick={() => controller.openSelectionMenu({ start: 7, end: 15, selectedText: 'Acme Inc', x: 10, y: 20 })}>Select literal</button>
      <button type="button" onClick={() => controller.openSelectionMenu({ start: 1, end: 20, selectedText: '{{param.user_id}}', x: 10, y: 20 })}>Select token</button>
      {controller.selectionState && (
        <>
          <span data-testid="existing">{controller.selectionState.existingName || 'new'}</span>
          <button type="button" onClick={() => controller.openParameterEditor()}>Create</button>
        </>
      )}
      <output>{draft}</output>
    </div>
  )
}

function NavigationHarness() {
  const controller = useScriptJsonNavigationController({
    jsonText: formatScriptJson(script()),
    parameterCandidates: [],
    script: script(),
  })
  return (
    <div>
      <button type="button" onClick={() => controller.setSelectedOutlineParameterName('user_id')}>Select parameter</button>
      <button type="button" onClick={() => controller.requestJsonLineJump(42)}>Jump</button>
      <span data-testid="references">{controller.selectedParameterReferenceLines.join(',')}</span>
      <span data-testid="jump">{controller.jsonLineJumpRequest?.line || 0}</span>
    </div>
  )
}

const noopSetter = () => undefined

function CleanResolveHarness() {
  const [structured] = useState(() => script('structured-truth'))
  const [resolved, setResolved] = useState('')
  const controller = useScriptJsonDraftController({
    jsonDirty: false,
    jsonText: '{stale-invalid',
    script: structured,
    setError: noopSetter,
    setJsonDirty: noopSetter,
    setJsonText: noopSetter,
    setScript: noopSetter,
    setSuccess: noopSetter,
    onInvalidDraft: noopSetter,
  })
  const resolve = () => {
    try {
      setResolved(controller.resolveCurrentScript().name)
    } catch (error) {
      setResolved(`threw:${error instanceof Error ? error.message : 'unknown'}`)
    }
  }
  return (
    <div>
      <button type="button" onClick={resolve}>Resolve</button>
      <span data-testid="resolved">{resolved}</span>
    </div>
  )
}

function WorkCountHarness() {
  const [structured, setStructured] = useState(() => script())
  const [jsonText, setJsonText] = useState(() => formatScriptJson(structured))
  const [jsonDirty, setJsonDirty] = useState(false)
  const [tick, setTick] = useState(0)
  const draft = useScriptJsonDraftController({
    jsonDirty,
    jsonText,
    script: structured,
    setError: noopSetter,
    setJsonDirty,
    setJsonText,
    setScript: setStructured,
    setSuccess: noopSetter,
    onInvalidDraft: noopSetter,
  })
  const navigation = useScriptJsonNavigationController({
    jsonText,
    parameterCandidates: [],
    script: structured,
  })
  return (
    <div>
      <textarea aria-label="JSON draft" value={jsonText} onChange={event => draft.changeJsonDraft(event.currentTarget.value)} />
      <span data-testid="refs">{navigation.parameterReferenceCounts.get('user_id') || 0}</span>
      <span data-testid="account-refs">{navigation.parameterReferenceCounts.get('account_id') || 0}</span>
      <span data-testid="dirty">{jsonDirty ? 'dirty' : 'clean'}</span>
      <span data-testid="json-length">{draft.formattedScriptJson.length}</span>
      <span data-testid="tick">{tick}</span>
      <button type="button" onClick={() => setTick(value => value + 1)}>Tick</button>
      <button type="button" onClick={() => setStructured(current => ({ ...current, name: `${current.name}x` }))}>Rename</button>
      <button
        type="button"
        onClick={() => setStructured(current => ({
          ...current,
          actions: [{ ...current.actions[0], target: '/accounts/{{param.account_id}}' }],
        }))}
      >
        Retarget action
      </button>
    </div>
  )
}

describe('useScriptJsonAuthoringController', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('applies valid JSON, reports invalid drafts, and mirrors clean structured updates', async () => {
    await act(async () => root.render(<DraftHarness />))
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement
    expect(container.querySelector('[data-testid="status"]')?.textContent).toBe('synced')

    const nextText = formatScriptJson(script('raw-update'))
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(textarea, nextText)
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.querySelector('[data-testid="status"]')?.textContent).toBe('dirty')
    expect(container.querySelector('[data-testid="draft-name"]')?.textContent).toBe('raw-update')

    await act(async () => (container.querySelector('button') as HTMLButtonElement).click())
    expect(container.querySelector('[data-testid="structured-name"]')?.textContent).toBe('raw-update')
    expect(container.querySelector('[data-testid="status"]')?.textContent).toBe('synced')

    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(textarea, '{bad')
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.querySelector('[data-testid="status"]')?.textContent).toBe('invalid')
    await act(async () => (Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Apply') as HTMLButtonElement).click())
    expect(container.querySelector('[data-testid="error"]')?.textContent).toContain('Script JSON is invalid')
    expect(container.querySelector('[data-testid="invalid-count"]')?.textContent).toBe('1')

    await act(async () => (Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Reset') as HTMLButtonElement).click())
    await act(async () => (Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Structured update') as HTMLButtonElement).click())
    expect(textarea.value).toContain('structured-update')
  })

  it('routes a selection into a JSON parameter draft and recognizes an existing token', async () => {
    await act(async () => root.render(<SelectionHarness />))
    const buttons = () => Array.from(container.querySelectorAll('button'))
    await act(async () => (buttons().find(button => button.textContent === 'Select literal') as HTMLButtonElement).click())
    expect(container.querySelector('[data-testid="existing"]')?.textContent).toBe('new')
    await act(async () => (buttons().find(button => button.textContent === 'Create') as HTMLButtonElement).click())
    expect(container.querySelector('output')?.textContent).toContain('"requestedName":"acme_inc"')

    await act(async () => (buttons().find(button => button.textContent === 'Select token') as HTMLButtonElement).click())
    expect(container.querySelector('[data-testid="existing"]')?.textContent).toBe('user_id')
  })

  it('resolves the structured script without re-parsing while the JSON pane is clean', async () => {
    await act(async () => root.render(<CleanResolveHarness />))
    workCounts.parse = 0
    await act(async () => (container.querySelector('button') as HTMLButtonElement).click())
    expect(container.querySelector('[data-testid="resolved"]')?.textContent).toBe('structured-truth')
    expect(workCounts.parse).toBe(0)
  })

  it('does not repeat script serialization or outline work on renders that leave the script alone', async () => {
    await act(async () => root.render(<WorkCountHarness />))
    expect(container.querySelector('[data-testid="refs"]')?.textContent).toBe('1')
    const buttons = () => Array.from(container.querySelectorAll('button'))
    workCounts.format = 0
    workCounts.outline = 0
    workCounts.parse = 0
    workCounts.reserialize = 0

    await act(async () => (buttons().find(button => button.textContent === 'Tick') as HTMLButtonElement).click())
    expect(container.querySelector('[data-testid="tick"]')?.textContent).toBe('1')
    expect(workCounts).toEqual({ format: 0, outline: 0, parse: 0, reserialize: 0 })

    // A structured edit outside the phase action lists still has to reproject the
    // JSON pane, but must not rebuild the outline it cannot have changed.
    await act(async () => (buttons().find(button => button.textContent === 'Rename') as HTMLButtonElement).click())
    expect(workCounts).toEqual({ format: 1, outline: 0, parse: 0, reserialize: 0 })
    expect(container.querySelector('[data-testid="refs"]')?.textContent).toBe('1')
  })

  it('rebuilds the outline when the phase action lists actually change', async () => {
    await act(async () => root.render(<WorkCountHarness />))
    const buttons = () => Array.from(container.querySelectorAll('button'))
    workCounts.outline = 0

    await act(async () => (buttons().find(button => button.textContent === 'Retarget action') as HTMLButtonElement).click())
    expect(workCounts.outline).toBe(1)
    expect(container.querySelector('[data-testid="refs"]')?.textContent).toBe('0')
    expect(container.querySelector('[data-testid="account-refs"]')?.textContent).toBe('1')
  })

  it('marks the JSON pane dirty without re-serializing the script on every keystroke', async () => {
    await act(async () => root.render(<WorkCountHarness />))
    const textarea = container.querySelector('textarea') as HTMLTextAreaElement
    const clean = textarea.value
    workCounts.format = 0
    workCounts.outline = 0
    workCounts.reserialize = 0

    const type = async (value: string) => act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(textarea, value)
      textarea.dispatchEvent(new Event('input', { bubbles: true }))
    })

    await type(`${clean} `)
    expect(container.querySelector('[data-testid="dirty"]')?.textContent).toBe('dirty')
    await type(clean)
    expect(container.querySelector('[data-testid="dirty"]')?.textContent).toBe('clean')
    expect(workCounts.format).toBe(0)
    expect(workCounts.outline).toBe(0)
    expect(workCounts.reserialize).toBe(0)
  })

  it('coordinates parameter-reference and line-jump navigation', async () => {
    await act(async () => root.render(<NavigationHarness />))
    const buttons = Array.from(container.querySelectorAll('button'))
    await act(async () => buttons[0].click())
    expect(container.querySelector('[data-testid="references"]')?.textContent).not.toBe('')
    await act(async () => buttons[1].click())
    expect(container.querySelector('[data-testid="jump"]')?.textContent).toBe('42')
  })
})
