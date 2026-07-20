// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { formatScriptJson } from './scriptEditorJsonModel'
import {
  useJsonParameterMenu,
  useScriptJsonDraftController,
  useScriptJsonNavigationController,
} from './useScriptJsonAuthoringController'

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

  it('coordinates parameter-reference and line-jump navigation', async () => {
    await act(async () => root.render(<NavigationHarness />))
    const buttons = Array.from(container.querySelectorAll('button'))
    await act(async () => buttons[0].click())
    expect(container.querySelector('[data-testid="references"]')?.textContent).not.toBe('')
    await act(async () => buttons[1].click())
    expect(container.querySelector('[data-testid="jump"]')?.textContent).toBe('42')
  })
})
