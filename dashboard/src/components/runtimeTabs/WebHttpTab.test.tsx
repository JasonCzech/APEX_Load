// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import WebHttpTab from './WebHttpTab'
import { type WebRuntimeConfig } from '../../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

function render(web: WebRuntimeConfig | undefined, setWeb: (next: WebRuntimeConfig | undefined) => void) {
  act(() => {
    root.render(<WebHttpTab web={web} setWeb={setWeb} />)
  })
}

function findButton(text: string): HTMLButtonElement {
  const btn = Array.from(container.querySelectorAll('button')).find(b => (b.textContent || '').trim() === text)
  if (!btn) throw new Error(`button not found: ${text}`)
  return btn as HTMLButtonElement
}

function setTextarea(el: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')!.set!
  setter.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('WebHttpTab', () => {
  it('parses additional attributes into a key→value map', () => {
    const setWeb = vi.fn()
    render(undefined, setWeb)
    const textarea = Array.from(container.querySelectorAll('textarea')).find(
      t => (t as HTMLTextAreaElement).placeholder.includes('key=value'),
    ) as HTMLTextAreaElement
    expect(textarea).toBeTruthy()
    act(() => setTextarea(textarea, 'env=prod\nregion=us'))
    expect(setWeb).toHaveBeenCalledWith(expect.objectContaining({ additional_attributes: { env: 'prod', region: 'us' } }))
  })

  it('adds and removes a content check', () => {
    const setWeb = vi.fn()
    render(undefined, setWeb)
    act(() => findButton('Add content check').dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(setWeb).toHaveBeenCalledWith(expect.objectContaining({ content_checks: [{}] }))

    // with one present, Remove clears it back to an empty (undefined) section.
    setWeb.mockClear()
    render({ content_checks: [{ text: 'ok' }] }, setWeb)
    act(() => findButton('Remove').dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(setWeb).toHaveBeenCalledWith(undefined)
  })

  it('adds a weighted network profile', () => {
    const setWeb = vi.fn()
    render(undefined, setWeb)
    act(() => findButton('Add network profile').dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(setWeb).toHaveBeenCalledWith(expect.objectContaining({ network_profiles: [{ name: '', weight: 1 }] }))
  })

  it('collapses to undefined when the last field is cleared', () => {
    const setWeb = vi.fn()
    render({ source_ips: ['10.0.0.1'] }, setWeb)
    const sourceInput = Array.from(container.querySelectorAll('input[type="text"]')).find(
      i => (i as HTMLInputElement).placeholder.includes('10.0.0.1'),
    ) as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    setter.call(sourceInput, '')
    act(() => sourceInput.dispatchEvent(new Event('input', { bubbles: true })))
    expect(setWeb).toHaveBeenCalledWith(undefined)
  })

  it('collapses nested objects whose final value is cleared', () => {
    const setWeb = vi.fn()
    render({ network_simulation: { mode: 'preset' } }, setWeb)
    const networkMode = Array.from(container.querySelectorAll('label'))
      .find(label => label.querySelector('span')?.textContent === 'Mode')
      ?.querySelector('select') as HTMLSelectElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set
    setter?.call(networkMode, '')
    act(() => networkMode.dispatchEvent(new Event('change', { bubbles: true })))
    expect(setWeb).toHaveBeenCalledWith(undefined)
  })

  it('retains source-IP validation in the shared runtime editor', () => {
    render({ source_ips: ['999.10.10.10'] }, vi.fn())
    const sourceInput = Array.from(container.querySelectorAll('input[type="text"]')).find(
      input => (input as HTMLInputElement).placeholder.includes('10.0.0.1'),
    ) as HTMLInputElement
    expect(sourceInput.getAttribute('aria-invalid')).toBe('true')
    expect(container.textContent).toContain('Enter valid IPv4 or IPv6 addresses.')
  })
})

describe('WebHttpTab enum vocabulary', () => {
  // These option values are wire values the Go validator enforces
  // (pkg/dsl/validate.go validateWebRuntimeIssues). Emitting anything else made
  // the whole override save fail server-side, so lock the vocabularies here.
  const optionValues = (labelText: string): string[] => {
    const select = Array.from(container.querySelectorAll('label'))
      .find(label => (label.querySelector('span')?.textContent || '') === labelText)
      ?.querySelector('select') as HTMLSelectElement | undefined
    expect(select, `select ${labelText}`).toBeDefined()
    return Array.from(select!.options).map(option => option.value).filter(Boolean)
  }

  it('emits only validator-accepted wire values', () => {
    render({ content_checks: [{ text: 'ok' }] }, vi.fn())
    expect(optionValues('Filter mode')).toEqual(['none', 'allow_list', 'block_list'])
    // Two "Mode" selects render (network simulation, then streaming).
    const modeSelects = Array.from(container.querySelectorAll('label'))
      .filter(label => (label.querySelector('span')?.textContent || '') === 'Mode')
      .map(label => label.querySelector('select') as HTMLSelectElement)
    expect(modeSelects).toHaveLength(2)
    expect(Array.from(modeSelects[0].options).map(option => option.value).filter(Boolean)).toEqual(['max', 'preset', 'custom'])
    expect(Array.from(modeSelects[1].options).map(option => option.value).filter(Boolean)).toEqual(['adaptive', 'fixed'])
    expect(optionValues('Preset')).toEqual(['56k', 'dsl', 't1', '10mbps'])
    expect(optionValues('Bandwidth estimation')).toEqual(['rolling', 'instant'])
    expect(optionValues('Scope')).toEqual(['any_response', 'main_document', 'resource'])
  })
})
