// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { ImportTransportPanel } from './ImportTransportPanel'

const applyMock = vi.fn()

vi.mock('../hooks/useApi', () => ({
  useApi: () => ({ applyImportTransport: applyMock }),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const script = {
  name: 'Captured', version: '1', protocol: 'http', config: { base_url: 'https://app.example.test' },
  actions: [
    { name: 'home', type: 'GET', method: 'GET', target: '/', metadata: { http_version_capture: { version: 1, request_state: 'available', response_state: 'available', request_version: '3' } } },
    { name: 'batch', type: 'http_sequence', target: '', params: { requests: [{ name: 'child', type: 'GET', method: 'GET', target: '/child' }] } },
    { name: 'race', type: 'GET', method: 'GET', target: 'https://api.example.test/orders', metadata: { execution_blockers: ['curl_http3_race_not_modeled'], http_version_rewrite_proposal: { version: 1, policy: 'auto+h3' } } },
  ],
} as unknown as ScriptDefinition

describe('ImportTransportPanel', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    applyMock.mockReset()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const render = async (props: Partial<Parameters<typeof ImportTransportPanel>[0]> = {}) => {
    const onApplied = props.onApplied ?? vi.fn()
    await act(async () => {
      root.render(<ImportTransportPanel script={script} diagnostics={[{ code: 'origin_advertises_h3', location: 'https://app.example.test' }]} onApplied={onApplied} {...props} />)
    })
    return onApplied
  }
  const select = (origin: string) => container.querySelector<HTMLSelectElement>(`select[aria-label="HTTP version for ${origin}"]`)!
  const choose = async (origin: string, value: string) => {
    await act(async () => {
      select(origin).value = value
      select(origin).dispatchEvent(new Event('change', { bubbles: true }))
    })
  }
  const button = (label: string) => Array.from(container.querySelectorAll('button')).find(candidate => candidate.textContent?.startsWith(label))

  it('lists one row per origin with observations, advertisement and the curl proposal', async () => {
    await render()
    const rows = Array.from(container.querySelectorAll('tbody tr')).map(row => row.textContent)
    expect(rows).toHaveLength(2)
    expect(rows[0]).toContain('https://api.example.test')
    expect(rows[0]).toContain('Suggested for curl --http3: Upgrade to HTTP/3 when advertised (approximate)')
    expect(rows[0]).toContain('1 request needs an explicit HTTP version')
    expect(rows[1]).toContain('https://app.example.test')
    expect(rows[1]).toContain('h3 1')
    expect(rows[1]).toContain('Yes (Alt-Svc)')
    expect(container.querySelector('[aria-label="Transport preview"]')).toBeNull()
  })

  it('previews an origin edit across nested requests and applies it', async () => {
    const updated = { ...script, name: 'Edited' }
    applyMock.mockResolvedValue({ script: updated, changes: [{ path: 'actions[0]' }, { path: 'actions[1].params.requests[0]' }], import_draft_expires_at: '2026-10-10T12:00:00Z' })
    const onApplied = await render()
    await choose('https://app.example.test', 'auto+h3')
    const preview = container.querySelector('[aria-label="Transport preview"]')!
    expect(preview.textContent).toContain('Preview: 2 requests change')
    expect(preview.textContent).toContain('actions[1].params.requests[0] child: Inherit the script setting → Upgrade to HTTP/3 when advertised')
    await act(async () => { button('Apply to 2 requests')!.click() })
    expect(applyMock).toHaveBeenCalledWith({ script, edits: [
      { path: 'actions[0]', origin: 'https://app.example.test', from: '', to: 'auto+h3' },
      { path: 'actions[1].params.requests[0]', origin: 'https://app.example.test', from: '', to: 'auto+h3' },
    ] })
    expect(onApplied).toHaveBeenCalledWith(updated, '2026-10-10T12:00:00Z')
    expect(container.querySelector('[role="status"]')?.textContent).toBe('Applied HTTP version choices to 2 requests.')
  })

  it('says which gate a choice resolves, and discards staged choices', async () => {
    await render()
    await choose('https://api.example.test', '2')
    expect(container.querySelector('[aria-label="Transport preview"]')?.textContent).toContain('Preview: 1 request changes')
    expect(container.querySelector('[aria-label="Transport preview"]')?.textContent).toContain('(resolves the curl --http3 gate)')
    await act(async () => { button('Discard')!.click() })
    expect(container.querySelector('[aria-label="Transport preview"]')).toBeNull()
    expect(select('https://api.example.test').value).toBe('')
  })

  it('reports a refused apply without clearing the choice', async () => {
    applyMock.mockRejectedValue(new Error('the import draft changed since the transport review; refresh it and preview the edit again'))
    const onApplied = await render()
    await choose('https://app.example.test', '3')
    await act(async () => { button('Apply to 2 requests')!.click() })
    expect(onApplied).not.toHaveBeenCalled()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('refresh it and preview the edit again')
    expect(container.querySelector('[aria-label="Transport preview"]')).not.toBeNull()
  })

  it('offers exact HTTP/3 only for https origins', async () => {
    const cleartext = { ...script, config: { base_url: 'http://plain.example.test' }, actions: [{ name: 'home', type: 'GET', method: 'GET', target: '/' }] } as unknown as ScriptDefinition
    await render({ script: cleartext })
    const exact = Array.from(select('http://plain.example.test').options).find(option => option.value === '3')!
    expect(exact.disabled).toBe(true)
    expect(exact.textContent).toBe('Require HTTP/3 (no fallback) (https only)')
    expect(Array.from(select('http://plain.example.test').options).find(option => option.value === 'auto+h3')!.disabled).toBe(false)
    act(() => root.unmount())
    root = createRoot(container)
    await render()
    expect(Array.from(select('https://api.example.test').options).find(option => option.value === '3')!.disabled).toBe(false)
  })

  it('locks every control while disabled', async () => {
    await render({ disabled: true })
    expect(Array.from(container.querySelectorAll('select')).every(element => element.disabled)).toBe(true)
  })
})
