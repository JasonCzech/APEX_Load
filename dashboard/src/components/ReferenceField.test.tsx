// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import ReferenceField, { ReferenceSearch } from './ReferenceField'
import { ReferenceFieldContext, type ReferenceFieldContextValue } from './referenceFieldContext'
import { buildReferenceScope } from './referenceScopeModel'

const script = {
  name: 's', protocol: 'http', config: {},
  parameters: [{ name: 'user', type: 'list', values: ['alice'] }],
  data_sources: [{ name: 'cards', type: 'csv', columns: ['number'] }],
  actions: [
    { name: 'Search', type: 'GET', target: '/search', extractors: [{ name: 'order_id', type: 'json', expression: '$.id' }] },
    { name: 'Pay', type: 'POST', target: '/pay' },
  ],
} as unknown as ScriptDefinition
const scope = buildReferenceScope(script, { phase: 'action', actionIndex: 1 })

let container: HTMLDivElement
let root: Root
let current = ''

function Harness({ initial, context }: { initial: string; context: Partial<ReferenceFieldContextValue> }) {
  const [value, setValue] = useState(initial)
  current = value
  return <ReferenceFieldContext.Provider value={{ scope, ...context }}>
    <ReferenceField value={value} onChange={setValue} field={['target']} label="Target" inputProps={{ 'aria-label': 'Target' }} />
  </ReferenceFieldContext.Provider>
}

const render = async (initial: string, context: Partial<ReferenceFieldContextValue> = {}) => {
  await act(async () => { root.render(<Harness initial={initial} context={context} />) })
}
const input = () => container.querySelector<HTMLInputElement>('.reference-field-control input')!
function type(element: HTMLInputElement, value: string, caret = value.length) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  setter.call(element, value)
  element.setSelectionRange(caret, caret)
  element.dispatchEvent(new Event('input', { bubbles: true }))
}
const key = (element: Element, name: string) => element.dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true }))
const options = () => Array.from(container.querySelectorAll('[role="option"]')).map(option => option.querySelector('code')?.textContent)

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation(callback => { callback(0); return 0 })
})
afterEach(async () => {
  await act(async () => { root.unmount() })
  container.remove()
  vi.restoreAllMocks()
})

describe('ReferenceField', () => {
  it('inserts a picked reference at the caret, grouped by where values come from', async () => {
    await render('/orders/')
    await act(async () => { input().setSelectionRange(8, 8); input().dispatchEvent(new KeyboardEvent('keyup', { key: 'End', bubbles: true })) })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Insert a reference into Target"]')!.click() })
    expect(Array.from(container.querySelectorAll('.reference-picker-group')).map(group => group.textContent)).toEqual(['Parameters', 'Data', 'Earlier steps', 'Environment and run', 'Functions'])
    const search = container.querySelector<HTMLInputElement>('input[aria-label="Find a reference"]')!
    await act(async () => { type(search, 'order') })
    expect(options()).toEqual(['{{var.order_id}}'])
    await act(async () => { key(search, 'Enter') })
    expect(current).toBe('/orders/{{var.order_id}}')
    expect(container.querySelector('.reference-picker')).toBeNull()
  })

  it('lists references in place after {{ and completes the one chosen', async () => {
    await render('')
    await act(async () => { type(input(), '?u={{par') })
    expect(options()).toEqual(['{{param.user}}'])
    await act(async () => { key(input(), 'Enter') })
    expect(current).toBe('?u={{param.user}}')
    await act(async () => { type(input(), '?u={{param.user}}&c={{data') })
    expect(options()).toEqual(['{{data.cards.number}}'])
    await act(async () => { key(input(), 'Escape') })
    expect(container.querySelector('.reference-picker')).toBeNull()
  })

  it('flags a misspelled reference before any replay, with fixes', async () => {
    const addExtractor = vi.fn()
    const createParameter = vi.fn(() => 'ordr_id')
    await render('/orders/{{var.ordr_id}}', { onAddExtractor: addExtractor, onCreateParameter: createParameter })
    const warning = container.querySelector('.reference-warning')!
    expect(warning.textContent).toContain('{{var.ordr_id}} is not set by an earlier step')
    expect(input().getAttribute('aria-invalid')).toBe('true')
    await act(async () => { Array.from(warning.querySelectorAll('button')).find(button => button.textContent === 'Add an extractor')!.click() })
    expect(addExtractor).toHaveBeenCalledWith('ordr_id')
    await act(async () => { Array.from(warning.querySelectorAll('button')).find(button => button.textContent === 'Use {{var.order_id}}')!.click() })
    expect(current).toBe('/orders/{{var.order_id}}')
    expect(container.querySelector('.reference-warning')).toBeNull()
  })

  it('makes a misspelled variable a parameter under the name it gets', async () => {
    const createParameter = vi.fn(() => 'promo')
    await render('{{var.Promo}}', { onCreateParameter: createParameter })
    await act(async () => { Array.from(container.querySelectorAll<HTMLButtonElement>('.reference-warning button')).find(button => button.textContent === 'Make it a parameter')!.click() })
    expect(createParameter).toHaveBeenCalledWith('Promo')
    expect(current).toBe('{{param.promo}}')
  })

  it('makes the field, or the selected part, a parameter', async () => {
    const makeParameter = vi.fn()
    await render('/orders/12345', { onMakeParameter: makeParameter })
    await act(async () => { input().setSelectionRange(8, 13); input().dispatchEvent(new KeyboardEvent('keyup', { key: 'Shift', bubbles: true })) })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Insert a reference into Target"]')!.click() })
    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Make the selected text a parameter')!.click() })
    expect(makeParameter).toHaveBeenCalledWith({ field: ['target'], value: '/orders/12345', from: 8, to: 13 })
  })

  it('offers no parameter for a value the script holds as a number', async () => {
    await act(async () => { root.render(<ReferenceFieldContext.Provider value={{ scope, onMakeParameter: vi.fn() }}>
      <ReferenceField value="200" onChange={() => {}} field={['assertions', 0, 'expected']} label="Status" makesParameter={false} />
    </ReferenceFieldContext.Provider>) })
    await act(async () => { container.querySelector<HTMLButtonElement>('button[aria-label="Insert a reference into Status"]')!.click() })
    expect(container.querySelector('.reference-picker')).not.toBeNull()
    expect(container.querySelector('.reference-make-parameter')).toBeNull()
  })

  it('inserts from the outputs panel into the field as it is now, not as it was on focus', async () => {
    let handle: ((expression: string) => void) | null = null
    const release = vi.fn()
    await render('/pay/', { registerInsertTarget: insert => { handle = insert }, releaseInsertTarget: release })
    await act(async () => { input().focus() })
    await act(async () => { type(input(), '/pay/abc'); input().dispatchEvent(new KeyboardEvent('keyup', { key: 'c', bubbles: true })) })
    await act(async () => { handle!('var.order_id') })
    expect(current).toBe('/pay/abc{{var.order_id}}')
    await act(async () => { root.render(<div />) })
    expect(release).toHaveBeenCalledWith(handle)
  })

  it('replaces the whole reference when one is picked from inside it', async () => {
    await render('')
    await act(async () => { type(input(), '{{var.orer_id}}', 8) })
    expect(options()).toEqual(['{{var.order_id}}'])
    await act(async () => { key(input(), 'Enter') })
    expect(current).toBe('{{var.order_id}}')
  })

  it('closes the body’s picker on a click outside it and its button', async () => {
    const close = vi.fn()
    await act(async () => { root.render(<ReferenceFieldContext.Provider value={{ scope }}><span><button type="button">Insert reference</button><ReferenceSearch label="body" onPick={() => undefined} onClose={close} /></span></ReferenceFieldContext.Provider>) })
    await act(async () => { container.querySelector('button')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) })
    expect(close).not.toHaveBeenCalled()
    await act(async () => { document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })) })
    expect(close).toHaveBeenCalled()
  })

  it('stays a plain field without a scope', async () => {
    await act(async () => { root.render(<ReferenceField value="x" onChange={() => undefined} field={['target']} label="Target" />) })
    expect(container.querySelector('.reference-insert')).toBeNull()
  })
})
