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

  it('starts from an initial draft and keeps a type the list does not offer', async () => {
    await act(async () => {
      root.render(<ExtractorTester event={makeEvent('{"csrf":"tok-987","other":"x"}')} actionIndex={2} onAddExtractor={vi.fn()}
        initialDraft={{ name: 'c_csrf', source: '', type: 'regex', expression: '"(?:csrf|other)":"([^"]+)"' }} />)
    })
    const selects = container.querySelectorAll<HTMLSelectElement>('.extractor-tester-controls select')
    expect(selects[0].value).toBe('body')
    expect(selects[1].value).toBe('regex')
    expect((container.querySelector('.extractor-tester-name input') as HTMLInputElement).value).toBe('c_csrf')
    expect((container.querySelector('.extractor-tester-expression') as HTMLInputElement).value).toBe('"(?:csrf|other)":"([^"]+)"')
    expect(container.querySelector('.extractor-tester-count')?.textContent).toBe('2 matches')

    await act(async () => { root.unmount() })
    root = createRoot(container)
    await act(async () => {
      root.render(<ExtractorTester event={makeEvent('{}')} actionIndex={2} onAddExtractor={vi.fn()}
        initialDraft={{ name: 'token', source: 'header', type: 'header', expression: 'X-Token' }} />)
    })
    const [source, type] = Array.from(container.querySelectorAll<HTMLSelectElement>('.extractor-tester-controls select'))
    expect(source.value).toBe('header')
    expect(type.value).toBe('header')
    expect(Array.from(type.options).map(option => option.value)).toContain('header')
    // A header lookup is previewed as the engine runs it: the named header's value.
    expect(container.querySelector('.extractor-tester-result')?.textContent).toBe('No matches in this response.')
  })

  it('seeds a header extractor without a type as the engine reads it: a direct lookup of the header value', async () => {
    const event = makeEvent('{}')
    event.snapshot = { ...event.snapshot, response_headers: { 'Content-Type': 'text/html', 'x-csrf-token': 'tok-1', 'X-CSRF-Token-Expiry': '60' } }
    await act(async () => {
      root.render(<ExtractorTester event={event} actionIndex={2} onAddExtractor={vi.fn()}
        initialDraft={{ name: 'csrf', source: 'header', type: '', expression: 'X-CSRF-Token' }} />)
    })
    const [, type] = Array.from(container.querySelectorAll<HTMLSelectElement>('.extractor-tester-controls select'))
    expect(type.value).toBe('header')
    expect(type.selectedOptions[0].textContent).toBe('Header value')
    // The value, not the name; a longer header that starts with the name is another header.
    expect(container.querySelector('.extractor-tester-count')?.textContent).toBe('1 match')
    expect(Array.from(container.querySelectorAll('.extractor-tester-matches code')).map(node => node.textContent)).toEqual(['tok-1'])
  })

  it('updates the seeded extractor in place under its own name instead of adding a copy', async () => {
    const onAddExtractor = vi.fn()
    const onUpdateExtractor = vi.fn((): true | string => true)
    await act(async () => {
      root.render(<ExtractorTester event={makeEvent('{"csrf":"tok-987"}')} actionIndex={2} onAddExtractor={onAddExtractor} onUpdateExtractor={onUpdateExtractor}
        initialDraft={{ name: 'c_csrf', source: 'body', type: 'regex', expression: 'nope=(\\w+)' }} />)
    })
    const buttons = () => Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
    expect(buttons().map(button => button.textContent)).toEqual(['Update extractor c_csrf'])
    expect((container.querySelector('.extractor-tester-name input') as HTMLInputElement).readOnly).toBe(true)
    await act(async () => { setInput(container.querySelector('.extractor-tester-expression') as HTMLInputElement, 'csrf":"([^"]+)"') })
    expect(container.querySelector('.extractor-tester-count')?.textContent).toBe('1 match')
    await act(async () => { buttons()[0].click() })
    expect(onUpdateExtractor).toHaveBeenCalledWith({ source: 'body', type: 'regex', expression: 'csrf":"([^"]+)"' })
    expect(onAddExtractor).not.toHaveBeenCalled()
    expect(container.querySelector('.extractor-tester-added')?.textContent).toBe('Updated “c_csrf” on this step. Replay to check it.')

    // A step that changed since the replay is not updated, and the tester says so.
    onUpdateExtractor.mockReturnValueOnce('the step changed since this replay.')
    await act(async () => { setInput(container.querySelector('.extractor-tester-expression') as HTMLInputElement, 'csrf') })
    expect(container.querySelector('.extractor-tester-added')).toBeNull()
    await act(async () => { buttons()[0].click() })
    expect(container.querySelector('.extractor-tester-actions .extractor-tester-error')?.textContent).toBe('Could not update “c_csrf”: the step changed since this replay.')
    // Whatever blocked it is the reason shown, such as unapplied JSON edits.
    onUpdateExtractor.mockReturnValueOnce('apply or discard the JSON edits first.')
    await act(async () => { buttons()[0].click() })
    expect(container.querySelector('.extractor-tester-actions .extractor-tester-error')?.textContent).toBe('Could not update “c_csrf”: apply or discard the JSON edits first.')
  })

  it('tells the caller when the user changes a field', async () => {
    const onDraftChange = vi.fn()
    await act(async () => {
      root.render(<ExtractorTester event={makeEvent('{"csrf":"tok-987"}')} actionIndex={0} onAddExtractor={vi.fn()} onDraftChange={onDraftChange} />)
    })
    expect(onDraftChange).not.toHaveBeenCalled()
    await act(async () => { setInput(container.querySelector('.extractor-tester-expression') as HTMLInputElement, 'csrf') })
    expect(onDraftChange).toHaveBeenCalledTimes(1)
    await act(async () => { setInput(container.querySelector('.extractor-tester-name input') as HTMLInputElement, 'c_csrf') })
    expect(onDraftChange).toHaveBeenCalledTimes(2)
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
      setter.call(typeDropdown, 'xpath')
      typeDropdown.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const expression = container.querySelector('.extractor-tester-expression') as HTMLInputElement
    await act(async () => { setInput(expression, '//input/@value') })
    expect(container.textContent).toContain('No live preview for xpath')
    const addButton = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Add as extractor') as HTMLButtonElement
    expect(addButton.disabled).toBe(false)
    void typeSelect
  })

  it('evaluates an engine (Go) regex with inline flags as the replay does, and names Go syntax it cannot test', async () => {
    // A real Salesforce extractor: the leading (?i) is Go syntax JS RegExp rejects ("Invalid group").
    const cshc = String.raw`(?i)(?:^|[?&{,\"\s]|&amp;)cshc[\"']?\s*[:=]\s*[\"']?([^\"&',}<\s]+)`
    await act(async () => {
      root.render(<ExtractorTester event={makeEvent('{"CSHC":"Zx9-AbC_1"}')} actionIndex={2} onAddExtractor={vi.fn()} initialDraft={{ name: 'c_cshc', source: 'body', type: 'regex', expression: cshc }} />)
    })
    expect(container.querySelector('.extractor-tester-error')).toBeNull()
    expect(container.querySelector('.extractor-tester-count')?.textContent).toBe('1 match')
    expect(container.querySelector('.extractor-tester-matches code')?.textContent).toBe('Zx9-AbC_1')

    const expression = container.querySelector('.extractor-tester-expression') as HTMLInputElement
    await act(async () => { setInput(expression, String.raw`\p{Lu}+`) })
    expect(container.querySelector('.extractor-tester-error')).toBeNull()
    expect(container.querySelector('.extractor-tester-result')?.textContent).toBe('This expression uses Go regex syntax the browser can’t test (Unicode character classes (\\p…)). It’s evaluated at replay.')
    // Syntax the engine rejects too is still an error.
    await act(async () => { setInput(expression, 'a(?=b)') })
    expect(container.querySelector('.extractor-tester-error')?.textContent).toBe('Invalid regular expression for the engine (Go): lookahead and lookbehind are not supported.')
  })
})
