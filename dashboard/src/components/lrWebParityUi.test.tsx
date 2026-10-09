// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { headersAndBodySurface } from './extractorSurface'
import { issueTitle } from './importReviewIssues'
import { collectAuthoringIssues } from './scriptAuthoringAssist'
import AuthHostsField from './AuthHostsField'
import { parseAuthHosts } from './authHosts'
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
    expect(issueTitle('some_future_code')).toBe('some future code')
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
    expect(headersAndBodySurface({ 'X-Ref': 'abc', 'Set-Cookie': 'sid=1' })).toBe('X-Ref: abc\r\nSet-Cookie: sid=1\r\n\r\n')
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
