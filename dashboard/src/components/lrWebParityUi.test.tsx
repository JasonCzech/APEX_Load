// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { headersAndBodySurface, setCookieSurface } from './extractorSurface'
import { issueTitle } from './importReviewIssues'
import { collectAuthoringIssues } from './scriptAuthoringAssist'
import AuthHostsField from './AuthHostsField'
import { parseAuthHosts } from './authHosts'
import { ProtocolAssertionsSection, ProtocolExtractorsSection } from './scriptActionFields'
import { describeExtractorDFEs, describeExtractorScope } from './extractorScope'
import { describeHTMLStep, htmlStepOf } from './htmlSteps'
import { bodyEncodingLabel, knownBodyEncoding } from './bodyEncodings'
import type { ScriptDefinition } from '../types/scripts'

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root | undefined
let container: HTMLDivElement | undefined

function render(element: JSX.Element) {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  act(() => root!.render(element))
}

function authHostsInput(): HTMLInputElement {
  const input = container!.querySelector('input[aria-label="Auth hosts"]')
  if (!(input instanceof HTMLInputElement)) throw new Error('auth hosts input not rendered')
  return input
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
  act(() => {
    setter?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

afterEach(() => {
  act(() => root?.unmount())
  container?.remove()
  root = undefined
  container = undefined
})

describe('LoadRunner web parity UI', () => {
  it('titles the parity review codes', () => {
    expect(issueTitle('html_navigation_requires_page_context')).toBe('Replace HTML-mode click with an explicit request')
    expect(issueTitle('integrated_auth_requires_configuration')).toBe('Configure NTLM or Kerberos credentials')
    expect(issueTitle('registration_dfe_unsupported')).toBe('Apply data-format extensions before extraction')
    expect(issueTitle('request_body_file_missing')).toBe('Add the missing request body file')
    expect(issueTitle('async_callbacks_not_executed')).toBe('Rebuild async callbacks that do not run')
    expect(issueTitle('javascript_web_script_unsupported')).toBe('Fix the JavaScript or re-record the script in C')
    expect(issueTitle('websocket_callbacks_not_executed')).toBe('Rebuild WebSocket callbacks that do not run')
    expect(issueTitle('recorded_async_conversation')).toBe('Review recorded background conversation')
    expect(issueTitle('some_future_code')).toBe('some future code')
  })

  it('does not ask async conversation or WebSocket steps for a target', () => {
    const script = {
      name: 'async', protocol: 'http', config: { base_url: 'https://shop.test' },
      actions: [
        { name: 'Start poll', type: 'http_async_start', params: { id: 'poll', request: { name: 'poll', type: 'GET', method: 'GET', target: '/poll' } } },
        { name: 'Sync status', type: 'http_sync', params: { variable: 'status' } },
        { name: 'Stop poll', type: 'http_async_stop', params: { id: 'poll' } },
        { name: 'Connect', type: 'ws_connect', target: 'wss://shop.test/socket' },
        { name: 'Send', type: 'ws_send', body: 'hello' },
        { name: 'Close', type: 'ws_close' },
      ],
    } as unknown as ScriptDefinition
    const messages = collectAuthoringIssues(script).map(issue => issue.message)
    expect(messages.some(message => /target is required/i.test(message))).toBe(false)
  })

  it('does not ask cookie_set for a target', () => {
    const script = {
      name: 'cookies', protocol: 'http', config: { base_url: 'https://shop.test' },
      actions: [{ name: 'Set cookie sid', type: 'cookie_set', params: { name: 'sid', value: 'x' } }],
    } as unknown as ScriptDefinition
    const messages = collectAuthoringIssues(script).map(issue => issue.message)
    expect(messages.some(message => /target is required/i.test(message))).toBe(false)
  })

  it('shapes the headers-and-body surface like the runtime', () => {
    expect(headersAndBodySurface({ 'X-Ref': 'abc', 'Set-Cookie': 'sid=1\nlang=en' })).toBe('Set-Cookie: sid=1\r\nSet-Cookie: lang=en\r\nX-Ref: abc\r\n\r\n')
    expect(headersAndBodySurface(undefined)).toBe('\r\n')
  })

  it('parses auth hosts', () => {
    expect(parseAuthHosts(' shop.test, api.shop.test:8443 ,, ')).toEqual(['shop.test', 'api.shop.test:8443'])
    expect(parseAuthHosts('')).toEqual([])
  })

  it('edits auth hosts without losing a trailing comma while typing', () => {
    const changes: Array<string[] | undefined> = []
    function Harness() {
      const [hosts, setHosts] = useState<string[] | undefined>(['shop.test'])
      return <AuthHostsField hosts={hosts} onChange={next => { changes.push(next); setHosts(next) }} />
    }
    render(<Harness />)
    const input = authHostsInput()
    expect(input.value).toBe('shop.test')
    setInputValue(input, 'shop.test, ')
    expect(authHostsInput().value).toBe('shop.test, ')
    setInputValue(authHostsInput(), 'shop.test, api.shop.test')
    expect(changes[changes.length - 1]).toEqual(['shop.test', 'api.shop.test'])
    setInputValue(authHostsInput(), '')
    expect(changes[changes.length - 1]).toBeUndefined()
  })

  it('keeps unrelated auth fields when the hosts change', () => {
    const onChange = vi.fn()
    render(<AuthHostsField hosts={undefined} onChange={onChange} />)
    setInputValue(authHostsInput(), 'a.test')
    expect(onChange).toHaveBeenCalledWith(['a.test'])
  })
})

describe('request body encodings', () => {
  it('recognizes every encoding the runtime decodes, ignoring case and spaces', () => {
    expect(knownBodyEncoding(' C_ESCAPED ')?.value).toBe('c_escaped')
    expect(knownBodyEncoding('utf-16le')?.label).toBe('UTF-16LE text')
    expect(knownBodyEncoding('windows-1252')?.note).toContain('Windows-1252')
    expect(knownBodyEncoding('gzip')).toBeUndefined()
    expect(bodyEncodingLabel('base64')).toBe('Base64-encoded wire bytes')
    expect(bodyEncodingLabel('gzip')).toBe('Unknown encoding: gzip')
  })
})

describe('cookie extractor surface', () => {
  it('lists each Set-Cookie header line as the runtime does', () => {
    expect(setCookieSurface({ 'Set-Cookie': 'sid=1; Path=/\npref=dark', 'Content-Type': 'text/html' })).toBe('Set-Cookie: sid=1; Path=/\r\nSet-Cookie: pref=dark\r\n')
    expect(setCookieSurface(undefined)).toBe('')
  })
})

describe('HTTP extractor rows', () => {
  function selectFor(label: string) {
    return document.querySelector(`select[aria-label="${label}"]`) as HTMLSelectElement
  }
  function selectedLabel(label: string) {
    const select = selectFor(label)
    return select.options[select.selectedIndex]?.textContent
  }

  it('offers the source-specific types and keeps unsupported stored values visible', () => {
    const action = {
      name: 'a', type: 'http', method: 'GET', target: '/',
      extractors: [
        { name: 'final', source: 'url', type: '', expression: '' },
        { name: 'sid', source: 'cookies', type: 'boundary', expression: 'sid=|||;' },
        { name: 'legacy', source: 'cookie', type: 'regex', expression: 'sid=(.+)' },
      ],
    }
    render(<ProtocolExtractorsSection action={action as never} index={0} protocol="http" onAddExtractor={vi.fn()} onRemoveExtractor={vi.fn()} onUpdateExtractor={vi.fn()} />)
    expect(selectedLabel('Extractor 1 source')).toBe('Final URL')
    expect(selectedLabel('Extractor 1 type')).toBe('Whole value')
    expect(selectedLabel('Extractor 2 source')).toBe('Set-Cookie lines')
    expect([...selectFor('Extractor 2 type').options].map(option => option.value)).toEqual(['regex', 'boundary', 'regex_count', 'regex_ordinal'])
    expect(selectedLabel('Extractor 3 source')).toBe('Unsupported: cookie')
  })

  it('shows every type the validator accepts as supported', () => {
    const action = {
      name: 'a', type: 'http', method: 'GET', target: '/',
      extractors: [
        { name: 'location', source: 'header', type: 'header', expression: 'Location' },
        { name: 'id', source: 'body', type: 'json_path', expression: '$.id' },
        { name: 'second', source: 'body', type: 'regex_ordinal', expression: 'id=(\\d+)' },
        { name: 'hops', source: 'header', type: 'regex_count', expression: 'Via' },
      ],
    }
    render(<ProtocolExtractorsSection action={action as never} index={0} protocol="http" onAddExtractor={vi.fn()} onRemoveExtractor={vi.fn()} onUpdateExtractor={vi.fn()} />)
    expect(selectedLabel('Extractor 1 type')).toBe('Named header')
    expect(selectedLabel('Extractor 2 type')).toBe('JSONPath')
    expect(selectedLabel('Extractor 3 type')).toBe('Regex ordinal')
    expect(selectedLabel('Extractor 4 type')).toBe('Regex count')
  })
})

describe('extractor scope summary', () => {
  it('names the extra responses and filters', () => {
    expect(describeExtractorScope(undefined)).toBe('')
    expect(describeExtractorScope({ redirects: 'include', resources: 'include' })).toBe('Also searches redirect hops and resources')
    expect(describeExtractorScope({ redirects: 'exclude', resources: 'include', request_url: '*token*' })).toBe('Also searches resources; only URL *token*')
    expect(describeExtractorScope({ header_names: ['Location'] })).toBe('Final response only; only headers Location')
    expect(describeExtractorDFEs(['base64', 'json_to_xml'])).toBe('Decodes the body first: base64 → json_to_xml')
    expect(describeExtractorDFEs([])).toBe('')
  })
})

describe('HTTP text assertions', () => {
  it('edits the source and case of a text check', () => {
    const onUpdate = vi.fn()
    const action = { name: 'a', type: 'http', method: 'GET', target: '/', assertions: [{ type: 'text_matches', value: 'Order [0-9]+', source: 'headers', case_insensitive: true }] }
    render(<ProtocolAssertionsSection action={action as never} index={0} protocol="http" onAddAssertion={vi.fn()} onRemoveAssertion={vi.fn()} onUpdateAssertion={onUpdate} />)
    const type = document.querySelector('select[aria-label="Assertion 1 type"]') as HTMLSelectElement
    expect(type.options[type.selectedIndex].textContent).toBe('Text matches pattern')
    const source = document.querySelector('select[aria-label="Assertion 1 source"]') as HTMLSelectElement
    expect(source.value).toBe('headers')
    const ignoreCase = document.querySelector('input[aria-label="Assertion 1 ignore case"]') as HTMLInputElement
    expect(ignoreCase.checked).toBe(true)
    act(() => {
      source.value = 'all'
      source.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdate).toHaveBeenCalledWith(0, 0, 'source', 'all')
    act(() => { ignoreCase.click() })
    expect(onUpdate).toHaveBeenCalledWith(0, 0, 'case_insensitive', undefined)
    act(() => {
      type.value = 'status_code'
      type.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onUpdate).toHaveBeenCalledWith(0, 0, 'type', 'status_code')
    expect(onUpdate).toHaveBeenCalledWith(0, 0, 'source', undefined)
  })

  it('shows an explicit body source as Body', () => {
    const action = { name: 'a', type: 'http', method: 'GET', target: '/', assertions: [{ type: 'text_contains', value: 'Welcome', source: 'body' }] }
    render(<ProtocolAssertionsSection action={action as never} index={0} protocol="http" onAddAssertion={vi.fn()} onRemoveAssertion={vi.fn()} onUpdateAssertion={vi.fn()} />)
    const source = document.querySelector('select[aria-label="Assertion 1 source"]') as HTMLSelectElement
    expect(source.options[source.selectedIndex].textContent).toBe('Body')
  })
})

describe('HTML-mode steps', () => {
  it('describes what each step resolves on the page', () => {
    expect(htmlStepOf({ html_step: { kind: 'link', text: 'Flights' } })?.kind).toBe('link')
    expect(htmlStepOf({ resources: [] })).toBeUndefined()
    expect(describeHTMLStep({ kind: 'link', text: 'Next', ordinal: 2 })).toBe('Follows the link "Next" (match 2) on the current page.')
    expect(describeHTMLStep({ kind: 'image', alt: 'Go' })).toBe('Clicks the image with alt "Go" on the current page.')
    expect(describeHTMLStep({ kind: 'image_check', src: '/logo.png' })).toContain('no request is sent')
    expect(describeHTMLStep({ kind: 'form', fields: [{ name: 'user' }, { name: 'pass' }] })).toBe("Submits the form with fields user, pass, with the page's own hidden fields, URL and method.")
  })
})
