// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ResponseSelection from './ResponseSelection'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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
  window.getSelection()?.removeAllRanges()
})

const BODY = '{"token":"abc123","user":"Ada"}'

function handlers() {
  return { onContains: vi.fn(() => true), onSave: vi.fn(() => true), onCorrelate: vi.fn(), onStatus: vi.fn(() => true), laterUses: vi.fn(() => 2) }
}

async function render(props: Partial<Parameters<typeof ResponseSelection>[0]> & ReturnType<typeof handlers>, shown?: string) {
  await act(async () => root.render(<ResponseSelection body={BODY} status={200} disabledReason="" {...props}><pre id="shown">{shown ?? props.body ?? BODY}</pre></ResponseSelection>))
}

/** Selects `text` in the shown response and releases the mouse, as a reader would. */
async function selectText(text: string) {
  const node = container.querySelector('#shown')!.firstChild!
  const start = node.textContent!.indexOf(text)
  const range = document.createRange()
  range.setStart(node, start)
  range.setEnd(node, start + text.length)
  window.getSelection()!.removeAllRanges()
  window.getSelection()!.addRange(range)
  await act(async () => container.querySelector('#shown')!.dispatchEvent(new MouseEvent('mouseup', { bubbles: true })))
}
const button = (name: string) => [...container.querySelectorAll('button')].find(item => item.textContent?.startsWith(name)) as HTMLButtonElement

describe('ResponseSelection', () => {
  it('turns the status into a check', async () => {
    const spy = handlers()
    await render(spy)
    await act(async () => button('Check that the status is 200').click())
    expect(spy.onStatus).toHaveBeenCalledWith(200)
    expect(container.querySelector('[role="status"]')!.textContent).toBe('Added the check “Status is 200”.')
  })

  it('offers a check, a saved value and later uses for selected text', async () => {
    const spy = handlers()
    await render(spy)
    await selectText('abc123')
    expect(container.querySelector('[role="group"]')!.getAttribute('aria-label')).toBe('Use the selected text')
    expect(spy.laterUses).toHaveBeenCalledWith('abc123')
    expect(button('Use this value in later steps').textContent).toBe('Use this value in later steps (2)')
    await act(async () => button('Check that the response contains this').click())
    expect(spy.onContains).toHaveBeenCalledWith('abc123')
    expect(container.querySelector('[role="group"]')).toBeNull()
    await selectText('abc123')
    await act(async () => button('Save this as a value').click())
    expect(spy.onSave).toHaveBeenCalledWith({ text: 'abc123', start: 10, end: 16, boundaries: { left: ':"', right: '"' } })
    await selectText('abc123')
    await act(async () => button('Use this value in later steps').click())
    expect(spy.onCorrelate).toHaveBeenCalledWith({ text: 'abc123', start: 10, end: 16, boundaries: { left: ':"', right: '"' } })
  })

  it('says when the step already has the check', async () => {
    const spy = { ...handlers(), onStatus: vi.fn(() => false), onContains: vi.fn(() => false) }
    await render(spy)
    await act(async () => button('Check that the status is 200').click())
    expect(container.querySelector('[role="status"]')!.textContent).toBe('The step already checks that the status is 200.')
    await selectText('abc123')
    await act(async () => button('Check that the response contains this').click())
    expect(container.querySelector('[role="status"]')!.textContent).toBe('The step already checks that the response contains it.')
  })

  it('saves no value at the end of a body the replay kept only part of', async () => {
    const spy = handlers()
    await render({ ...spy, body: 'id:x-42', bodyState: 'partial' })
    await selectText('x-42')
    expect(button('Save this as a value').disabled).toBe(true)
    expect(container.textContent).toContain('No boundaries around it find exactly this text, so it cannot be saved.')
    expect(button('Check that the response contains this').disabled).toBe(false)
  })

  it('saves no value from a redacted body', async () => {
    const spy = handlers()
    await render({ ...spy, bodyState: 'redacted' })
    await selectText('abc123')
    expect(button('Save this as a value').disabled).toBe(true)
    expect(button('Use this value in later steps').disabled).toBe(true)
    expect(container.textContent).toContain('The replay redacted parts of this response, so a value cannot be saved from it.')
  })

  it('cannot use a value no later step sends', async () => {
    const spy = { ...handlers(), laterUses: vi.fn(() => 0) }
    await render(spy)
    await selectText('abc123')
    expect(button('Use this value in later steps').disabled).toBe(true)
    expect(button('Save this as a value').disabled).toBe(false)
  })

  it('asks for the raw text when the selection is not in the response as sent', async () => {
    const spy = handlers()
    await render(spy, '{\n  "token": "abc123"\n}')
    await selectText('"token": "abc123"')
    expect(container.textContent).toContain('not in the response as it was sent')
    expect(button('Check that the response contains this').disabled).toBe(true)
    expect(button('Save this as a value').disabled).toBe(true)
  })

  it('adds nothing from a replay older than the script', async () => {
    const spy = handlers()
    await render({ ...spy, disabledReason: 'The script changed since this replay.' })
    expect(button('Check that the status is 200').disabled).toBe(true)
    await selectText('abc123')
    expect(container.textContent).toContain('The script changed since this replay.')
    expect(button('Check that the response contains this')).toBeUndefined()
  })

  it('ignores a selection outside the response', async () => {
    const spy = handlers()
    await render(spy)
    const outside = document.createElement('p')
    outside.textContent = 'elsewhere'
    document.body.appendChild(outside)
    const range = document.createRange()
    range.selectNodeContents(outside)
    window.getSelection()!.addRange(range)
    await act(async () => container.querySelector('#shown')!.dispatchEvent(new MouseEvent('mouseup', { bubbles: true })))
    expect(container.querySelector('[role="group"]')).toBeNull()
    outside.remove()
  })
})
