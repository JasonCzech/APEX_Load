// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ExtractorTester } from './ExtractorTester'
import type { ScriptReplayActionEvent } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function makeEvent(responseBody: string): ScriptReplayActionEvent {
  return {
    phase: 'action',
    action_index: 2,
    action_name: 'Login',
    action_type: 'POST',
    target: '/login',
    started_at: '', completed_at: '', duration_ms: 1, success: true,
    snapshot: { response_body: responseBody, response_headers: { 'Content-Type': 'application/json' } },
  } as ScriptReplayActionEvent
}

function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('ExtractorTester', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => { root.unmount() })
    container.remove()
    vi.clearAllMocks()
  })

  it('previews regex matches against the response and adds the extractor to the action', async () => {
    const onAddExtractor = vi.fn()
    await act(async () => {
      root.render(<ExtractorTester event={makeEvent('{"csrf":"tok-987"}')} actionIndex={2} onAddExtractor={onAddExtractor} />)
    })

    const expression = container.querySelector('.extractor-tester-expression') as HTMLInputElement
    await act(async () => { setInput(expression, 'csrf":"([^"]+)"') })
    expect(container.querySelector('.extractor-tester-matches code')?.textContent).toBe('tok-987')

    const nameInput = container.querySelector('.extractor-tester-name input') as HTMLInputElement
    await act(async () => { setInput(nameInput, 'csrf') })

    const addButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Add as extractor')!
    await act(async () => { addButton.dispatchEvent(new MouseEvent('click', { bubbles: true })) })

    expect(onAddExtractor).toHaveBeenCalledTimes(1)
    expect(onAddExtractor).toHaveBeenCalledWith(2, { name: 'csrf', source: 'body', type: 'regex', expression: 'csrf":"([^"]+)"' })
    expect(container.querySelector('.extractor-tester-added')?.textContent).toContain('csrf')
  })

  it('disables Add and explains when there is no editable action', async () => {
    const onAddExtractor = vi.fn()
    await act(async () => {
      root.render(<ExtractorTester event={makeEvent('{}')} actionIndex={null} onAddExtractor={onAddExtractor} />)
    })
    const expression = container.querySelector('.extractor-tester-expression') as HTMLInputElement
    await act(async () => { setInput(expression, 'x') })
    const addButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Add as extractor') as HTMLButtonElement
    expect(addButton.disabled).toBe(true)
    expect(container.textContent).toContain('Select an action step')
  })

  it('marks non-previewable types but still allows adding them', async () => {
    const onAddExtractor = vi.fn()
    await act(async () => {
      root.render(<ExtractorTester event={makeEvent('<html/>')} actionIndex={0} onAddExtractor={onAddExtractor} />)
    })
    const typeSelect = container.querySelector('.extractor-tester-controls select:nth-of-type(1)') as HTMLSelectElement
    // second control is the type select
    const selects = container.querySelectorAll('.extractor-tester-controls select')
    const typeDropdown = selects[1] as HTMLSelectElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!
      setter.call(typeDropdown, 'boundary')
      typeDropdown.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const expression = container.querySelector('.extractor-tester-expression') as HTMLInputElement
    await act(async () => { setInput(expression, 'LB::RB') })
    expect(container.textContent).toContain('No live preview for boundary')
    const addButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Add as extractor') as HTMLButtonElement
    expect(addButton.disabled).toBe(false)
    void typeSelect
  })
})
