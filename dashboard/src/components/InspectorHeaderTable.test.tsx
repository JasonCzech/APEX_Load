// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScriptActionDefinition } from '../types/scripts'
import { InspectorFormFieldTable, InspectorHeaderTable } from './InspectorHeaderTable'
import { MASKED_VALUE, maskedFieldNote, sourcesUnavailableNote } from './inspectorHeadersModel'

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

const csp = `default-src 'self'; ${'script-src *.example.test; '.repeat(80)}`

const responseHeaders = {
  server: 'edge',
  'Content-Security-Policy-Report-Only': csp,
  'Set-Cookie': 'a=1; secure\nb=; Max-Age=0',
  'Content-Type': 'text/html',
}

const rowNames = () => [...container.querySelectorAll('tbody th[scope="row"]')].map(cell => cell.textContent)
const button = (text: string) => [...container.querySelectorAll('button')].find(entry => entry.textContent === text)

describe('InspectorHeaderTable', () => {
  it('shows key headers first and toggles to every header', () => {
    act(() => root.render(<InspectorHeaderTable direction="response" headers={responseHeaders} label="Response headers" copyKeyPrefix="0:response-headers" copiedKey={null} onCopy={() => undefined} />))
    const table = container.querySelector('table')
    expect(table?.getAttribute('aria-label')).toBe('Response headers')
    expect([...container.querySelectorAll('thead th')].map(cell => cell.textContent)).toEqual(['Header', 'Value', 'Copy'])
    expect(rowNames()).toEqual(['Content-Type', 'Set-Cookie', 'Set-Cookie'])
    const toggle = button('Show all 5')
    expect(toggle?.getAttribute('aria-expanded')).toBe('false')
    // The toggle names the table it expands.
    expect(toggle?.getAttribute('aria-controls')).toBe(table?.id)
    expect(table?.id).toBeTruthy()
    act(() => toggle?.click())
    expect(rowNames()).toEqual(['Content-Type', 'Set-Cookie', 'Set-Cookie', 'server', 'Content-Security-Policy-Report-Only'])
    expect(button('Show key headers')?.getAttribute('aria-expanded')).toBe('true')
  })

  it('keeps a long value whole in the tooltip and copies it whole', () => {
    const onCopy = vi.fn()
    act(() => root.render(<InspectorHeaderTable direction="response" headers={{ 'Content-Security-Policy': csp }} label="Response headers" copyKeyPrefix="0:rh" copiedKey="0:rh:0" onCopy={onCopy} />))
    expect(container.querySelector('.inspector-table-value')?.getAttribute('title')).toBe(csp)
    const copy = container.querySelector<HTMLButtonElement>('.inspector-table-copy')
    expect(copy?.textContent).toBe('Copied')
    // The copied state is in the accessible name too.
    expect(copy?.getAttribute('aria-label')).toBe('Copied Content-Security-Policy header')
    act(() => copy?.click())
    expect(onCopy).toHaveBeenCalledWith('0:rh:0', csp)
    expect(button('Show all 1')).toBeUndefined()
  })

  it('adds Comes from for request headers and masks a secret-sourced value', () => {
    const onCopy = vi.fn()
    const action = { name: 'Login', type: 'POST', target: '/login', headers: { 'X-Api-Key': '{{secret.API_KEY}}', Accept: '*/*' } } as ScriptActionDefinition
    act(() => root.render(<InspectorHeaderTable
      direction="request"
      headers={{ 'X-Api-Key': 'live-key-value', Accept: '*/*', Cookie: 'sid=•••' }}
      sources={{ view: 'wire', authored: { action } }}
      label="Request headers sent on the wire"
      copyKeyPrefix="0:qh"
      copiedKey={null}
      onCopy={onCopy}
    />))
    expect([...container.querySelectorAll('thead th')].map(cell => cell.textContent)).toEqual(['Header', 'Value', 'Comes from', 'Copy'])
    act(() => button('Show all 3')?.click())
    expect([...container.querySelectorAll('.inspector-table-source')].map(cell => cell.textContent)).toEqual(['Cookie jar', 'Secret', 'Literal'])
    expect(container.textContent).not.toContain('live-key-value')
    expect(container.innerHTML).not.toContain('live-key-value')
    // The masked row offers no Copy (not a disabled one): only the word, and
    // its bullets are hidden from assistive technology behind "Masked".
    const maskedRow = [...container.querySelectorAll('tbody tr')].find(row => row.querySelector('th')?.textContent === 'X-Api-Key')!
    expect(maskedRow.classList.contains('is-masked')).toBe(true)
    expect(maskedRow.querySelector('button')).toBeNull()
    expect(maskedRow.querySelector('.inspector-table-masked-note')?.textContent).toBe('Masked')
    const value = maskedRow.querySelector('.inspector-table-value')!
    expect(value.classList.contains('is-masked')).toBe(true)
    expect(value.querySelector('[aria-hidden="true"]')?.textContent).toBe(MASKED_VALUE)
    expect(value.querySelector('.sr-only')?.textContent).toBe('Masked')
    expect(container.querySelectorAll('.inspector-table-copy')).toHaveLength(2)
  })

  it('leaves Comes from out, and the note to the inspector, when the authored step is not at hand', () => {
    act(() => root.render(<InspectorHeaderTable
      direction="request"
      headers={{ Cookie: 'sid=1', Accept: '*/*' }}
      sources={{ view: 'wire', authored: null }}
      label="Request headers sent on the wire"
      copyKeyPrefix="0:qh"
      copiedKey={null}
      onCopy={() => undefined}
    />))
    expect([...container.querySelectorAll('thead th')].map(cell => cell.textContent)).toEqual(['Header', 'Value', 'Copy'])
    expect(container.querySelectorAll('.inspector-table-source')).toHaveLength(0)
    expect(container.textContent).not.toContain('Cookie jar')
    // The inspector says why once per view; the table repeats nothing.
    expect(container.querySelector('.inspector-table-note')).toBeNull()
  })

  it('words the sources note by its reason', () => {
    expect(sourcesUnavailableNote(false)).toBe('Sources are unavailable because the script changed since this run.')
    expect(sourcesUnavailableNote(true)).toBe('Sources are unknown for this step.')
  })

  it('says when no headers were captured', () => {
    act(() => root.render(<InspectorHeaderTable direction="request" headers={{}} label="Request headers" copyKeyPrefix="x" copiedKey={null} onCopy={() => undefined} />))
    expect(container.querySelector('table')).toBeNull()
    expect(container.textContent).toBe('No request headers captured.')
  })
})

describe('InspectorFormFieldTable', () => {
  it('lists form fields with the password masked and no Copy for it', () => {
    const onCopy = vi.fn()
    const action = { name: 'Login', type: 'POST', target: '/login', body: 'un={{param.p_username}}&pw={{secret.SF_PW}}' } as ScriptActionDefinition
    act(() => root.render(<InspectorFormFieldTable
      body="un=jane&pw=hunter2"
      form
      unmaskedCopies={['Copy full request', 'Copy as cURL']}
      sources={{ view: 'wire', authored: { action } }}
      label="Form fields sent on the wire"
      copyKeyPrefix="0:form"
      copiedKey={null}
      onCopy={onCopy}
    />))
    expect(container.querySelector('table')?.getAttribute('aria-label')).toBe('Form fields sent on the wire')
    expect([...container.querySelectorAll('thead th')].map(cell => cell.textContent)).toEqual(['Field', 'Value', 'Comes from', 'Copy'])
    expect(container.innerHTML).not.toContain('hunter2')
    // The note does not imply protection: the full-request copies keep the field.
    expect(container.querySelector('.inspector-table-note')?.textContent).toBe('1 field is hidden here. Copy full request and Copy as cURL include it.')
    const rows = [...container.querySelectorAll('tbody tr')]
    expect(rows.map(row => row.classList.contains('is-masked'))).toEqual([false, true])
    expect(rows[1].querySelector('button')).toBeNull()
    const copies = [...container.querySelectorAll<HTMLButtonElement>('.inspector-table-copy')]
    expect(copies).toHaveLength(1)
    act(() => copies[0].click())
    expect(onCopy).toHaveBeenCalledWith('0:form:0', 'jane')
    expect(onCopy).toHaveBeenCalledTimes(1)
  })

  it('counts several hidden fields and names only the copies on screen', () => {
    act(() => root.render(<InspectorFormFieldTable body="password=a&pw=b&x=1" form unmaskedCopies={['Copy as cURL']} sources={{ view: 'scripted' }} label="Form fields" copyKeyPrefix="x" copiedKey={null} onCopy={() => undefined} />))
    expect(container.querySelector('.inspector-table-note')?.textContent).toBe('2 fields are hidden here. Copy as cURL includes them.')
    // Without the authored step there is no Comes from column (and no note of the table's own).
    expect([...container.querySelectorAll('thead th')].map(cell => cell.textContent)).toEqual(['Field', 'Value', 'Copy'])
    expect(container.querySelectorAll('.inspector-table-note')).toHaveLength(1)
    act(() => root.render(<InspectorFormFieldTable body="pw=b" form sources={{ view: 'scripted' }} label="Form fields" copyKeyPrefix="x" copiedKey={null} onCopy={() => undefined} />))
    expect(container.querySelector('.inspector-table-note')?.textContent).toBe('1 field is hidden here.')
    expect(maskedFieldNote(1, ['Copy full request', 'Copy as cURL'])).toBe('1 field is hidden here. Copy full request and Copy as cURL include it.')
    expect(maskedFieldNote(3, ['Copy full request'])).toBe('3 fields are hidden here. Copy full request includes them.')
  })

  it('never shows a form body raw when no field could be read from it', () => {
    act(() => root.render(<InspectorFormFieldTable body="=hunter2" form sources={{ view: 'wire' }} label="Form fields" copyKeyPrefix="x" copiedKey={null} onCopy={() => undefined} />))
    expect(container.innerHTML).not.toContain('hunter2')
    expect(container.textContent).toBe('No form fields could be read from this body.')
  })

  it('renders nothing for a body that is not form-encoded', () => {
    act(() => root.render(<InspectorFormFieldTable body='{"a":1}' form={false} sources={{ view: 'wire' }} label="Form fields" copyKeyPrefix="x" copiedKey={null} onCopy={() => undefined} />))
    expect(container.innerHTML).toBe('')
  })
})
