// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScriptActionDefinition } from '../types/scripts'
import { HTTPActionVersionFields } from './HTTPActionVersionFields'
import { ActionEditor } from './ScriptEditorActionEditor'
import { setHTTPActionVersion } from './httpActionVersionModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root, container: HTMLDivElement
const noop = () => {}
const action = (patch: Partial<ScriptActionDefinition> = {}): ScriptActionDefinition => ({ name: 'Read', type: 'GET', target: '/read', ...patch })
const select = () => container.querySelector<HTMLSelectElement>('select[aria-invalid]')!
const change = (value: string) => act(() => { select().value = value; select().dispatchEvent(new Event('change', { bubbles: true })) })

beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })

function renderEditor(input: ScriptActionDefinition, protocol: string, scriptHTTPVersion?: string) {
  act(() => root.render(<ActionEditor inspector action={input} index={0} parameters={[]} onUpdate={noop}
    onAddExtractor={noop} onUpdateExtractor={noop} onRemoveExtractor={noop} onAddAssertion={noop} onUpdateAssertion={noop}
    onRemoveAssertion={noop} onAddHeader={noop} onUpdateHeader={noop} onRemoveHeader={noop} onAddMetadata={noop}
    onUpdateMetadata={noop} onRemoveMetadata={noop} onBeginParameterEdit={noop} methodColor={() => '#000'}
    protocol={protocol} scriptHTTPVersion={scriptHTTPVersion} isBrowserScript={protocol === 'browser'} isJmsScript={protocol.startsWith('jms')}
    isSoapScript={protocol === 'soap'} issues={[]} />))
}

describe('request HTTP version fields', () => {
  it('writes the HTTP/3 policies and names their execution requirement', () => {
    const onUpdate = vi.fn()
    act(() => root.render(<HTTPActionVersionFields action={action()} index={2} protocol="http" scriptVersion="" onUpdate={onUpdate} />))
    expect(Array.from(select().options).map(option => option.value)).toEqual(['inherit', 'auto', '1.1', '2', 'auto+h3', '3'])
    change('3')
    expect(onUpdate).toHaveBeenLastCalledWith(2, 'http_version', '3')
    act(() => root.render(<HTTPActionVersionFields action={action({ http_version: 'auto+h3' })} index={2} protocol="http" scriptVersion="" onUpdate={onUpdate} />))
    expect(select().value).toBe('auto+h3')
    expect(container.textContent).toContain('runs need HTTP/3 execution enabled')
    // An inherited HTTP/3 script policy carries the same note.
    act(() => root.render(<HTTPActionVersionFields action={action()} index={2} protocol="http" scriptVersion="3" onUpdate={onUpdate} />))
    expect(select().selectedOptions[0].textContent).toContain('HTTP/3 required (no fallback)')
    expect(container.textContent).toContain('runs need HTTP/3 execution enabled')
    act(() => root.render(<HTTPActionVersionFields action={action({ http_version: '1.1' })} index={2} protocol="http" scriptVersion="3" onUpdate={onUpdate} />))
    expect(container.textContent).not.toContain('runs need HTTP/3 execution enabled')
  })

  it('reports exact HTTP/3 conflicts with the request URL and script settings at the field', () => {
    const issues = () => Array.from(container.querySelectorAll('.http3-policy-issue')).map(issue => issue.textContent)
    act(() => root.render(<HTTPActionVersionFields action={action({ http_version: '3' })} index={0} protocol="http" scriptVersion=""
      scriptContext={{ base_url: 'http://shop.example', http: { tls: { max_version: '1.2' } } }} onUpdate={noop} />))
    expect(issues()).toEqual([expect.stringContaining('only https origins'), expect.stringContaining('requires TLS 1.3')])
    // An absolute https target overrides the base URL; auto+h3 keeps http:// on TCP.
    act(() => root.render(<HTTPActionVersionFields action={action({ http_version: '3', target: 'https://api.example/read' })} index={0} protocol="http" scriptVersion=""
      scriptContext={{ base_url: 'http://shop.example' }} onUpdate={noop} />))
    expect(issues()).toEqual([])
    act(() => root.render(<HTTPActionVersionFields action={action({ http_version: 'auto+h3' })} index={0} protocol="http" scriptVersion=""
      scriptContext={{ base_url: 'http://shop.example' }} onUpdate={noop} />))
    expect(issues()).toEqual([])
  })

  it.each(['http', 'http/1.1', 'http/2', 'soap', 'java_http'])('shows accessible leaf controls in %s and inherits the passed script default', protocol => {
    renderEditor(action(), protocol, '2')
    const region = container.querySelector('[aria-label="Request HTTP version policy"]')!
    expect(region).not.toBeNull()
    expect(region.querySelector('label')?.textContent).toBe('Request HTTP version')
    expect(select().value).toBe('inherit')
    expect(select().selectedOptions[0].textContent).toContain('HTTP/2 required (no fallback)')
    expect(region.textContent).toContain('Authored policy: Inherit script setting')
  })

  it('offers the policy for a legacy request with a supported explicit method', () => {
    renderEditor(action({ type: 'request', method: 'POST' }), 'http', '2')
    expect(select().value).toBe('inherit')
  })

  it.each(['browser', 'grpc', 'database', 'network', 'websocket', 'kafka', 'java_vuser'])('hides the policy in the ActionEditor fallback for %s', protocol => {
    renderEditor(action(), protocol, '2')
    expect(container.querySelector('[aria-label="Request HTTP version policy"]')).toBeNull()
  })

  it.each(['http_sequence', 'http_oauth2_token', 'cookie_clear', 'control_if', 'source_review'])('hides the leaf policy on %s', type => {
    renderEditor(action({ type }), 'http', '2')
    expect(container.querySelector('[aria-label="Request HTTP version policy"]')).toBeNull()
  })

  it.each([null, '', 'HTTP/2', 2, {}, []].map(value => ({ value })))('keeps invalid authored $value visible without mutating it', ({ value }) => {
    const input = { ...action(), http_version: value } as ScriptActionDefinition
    const onUpdate = vi.fn()
    act(() => root.render(<HTTPActionVersionFields action={input} index={1} protocol="http" scriptVersion="2" onUpdate={onUpdate} />))
    expect(select().value).toBe('invalid')
    expect(select().getAttribute('aria-invalid')).toBe('true')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('original value is preserved')
    expect(onUpdate).not.toHaveBeenCalled()
    expect(input.http_version).toBe(value)
    change('auto')
    expect(onUpdate).toHaveBeenCalledWith(1, 'http_version', 'auto')
  })

  it('deletes an override for Inherit and restores the actual inherited policy while preserving the request', () => {
    const input = { ...action({ http_version: '1.1', body_encoding: 'base64', body: 'YQ==', timeout_ms: 1200 }),
      vendor_extension: { keep: true }, metadata: { 'har.request.http_version': 'HTTP/2', 'har.response.http_version': 'HTTP/1.1' } }
    function Harness() {
      const [current, setCurrent] = useState<ScriptActionDefinition>(input)
      return <><HTTPActionVersionFields action={current} index={0} protocol="http" scriptVersion="2" onUpdate={(_index, _field, value) => setCurrent(previous => setHTTPActionVersion(previous, value as ScriptActionDefinition['http_version']))} />
        <output>{JSON.stringify(current)}</output></>
    }
    act(() => root.render(<Harness />))
    expect(select().value).toBe('1.1')
    expect(container.textContent).toContain('Captured request HTTP versionHTTP/2')
    expect(container.textContent).toContain('Captured response HTTP versionHTTP/1.1')
    change('inherit')
    expect(select().value).toBe('inherit')
    expect(select().getAttribute('aria-invalid')).toBe('false')
    const saved = JSON.parse(container.querySelector('output')!.textContent!)
    expect(saved).not.toHaveProperty('http_version')
    expect(saved.vendor_extension).toEqual({ keep: true })
    expect(saved.body_encoding).toBe('base64')
    expect(saved.metadata).toEqual(input.metadata)
    expect(container.textContent).toContain('Authored policy: Inherit script setting (HTTP/2 required (no fallback))')
    change('auto')
    expect(JSON.parse(container.querySelector('output')!.textContent!).http_version).toBe('auto')
    expect(container.textContent).toContain('Authored policy: Auto-negotiate')
  })

  it('qualifies bounded recorder versions as separate proxy-leg observations', () => {
    renderEditor(action({ metadata: { http_version_capture: { version: 1, request_state: 'available', response_state: 'available', proxy: true, provenance: 'proxy_legs', request_version: '2', response_version: '1.1' } } }), 'http', '2')
    expect(container.textContent).toContain('Captured request HTTP version (client to proxy)HTTP/2')
    expect(container.textContent).toContain('Captured response HTTP version (proxy to origin)HTTP/1.1')
    expect(container.textContent).toContain('observations describe different connections')
    expect(container.textContent).toContain('Authored policy: Inherit script setting (HTTP/2 required')
    renderEditor(action({ metadata: { http_version_capture: { version: 1, request_state: 'unsupported', response_state: 'malformed', proxy: false, provenance: 'request' } } }), 'http')
    expect(container.textContent).toContain('Unsupported captured HTTP version (source value not retained)')
    expect(container.textContent).toContain('Malformed captured HTTP version (source value not retained)')
  })

  it('labels alias defaults and global overrides accurately without exposing an unrecognized observation', () => {
    renderEditor(action({ metadata: { 'har.request.http_version': 'untrusted-observation', 'har.response.http_version': 'HTTP/2' } }), 'http/1.1')
    expect(select().selectedOptions[0].textContent).toContain('HTTP/1.1 required')
    expect(container.textContent).toContain('Unrecognized captured version')
    expect(container.textContent).not.toContain('untrusted-observation')
    renderEditor(action(), 'http/2', '1.1')
    expect(select().selectedOptions[0].textContent).toContain('HTTP/1.1 required')
    renderEditor(action(), 'http/2', '')
    expect(select().selectedOptions[0].textContent).toContain('HTTP/2 required')
    renderEditor(action(), 'http', 'auto')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('script HTTP version is unsupported')
  })
})
