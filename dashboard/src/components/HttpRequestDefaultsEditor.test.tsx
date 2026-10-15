// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { HTTPConfig, ScriptActionDefinition } from '../types/scripts'
import HttpRequestDefaultsEditor, {
  SocketCompatibilityPanel,
} from './HttpRequestDefaultsEditor'
import {
  countHTTPDefaultHeaders,
  countPinnedCookies,
  countHTTPDefaultHeaderErrors,
  countPinnedCookieErrors,
  parseBulkCookies,
  parseBulkHeaders,
} from './httpRequestDefaultsModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function click(element: Element | null) {
  if (!element) throw new Error('element not found')
  element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
}

function changeTextarea(textarea: HTMLTextAreaElement | null, value: string) {
  if (!textarea) throw new Error('textarea not found')
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
  setter?.call(textarea, value)
  textarea.dispatchEvent(new Event('input', { bubbles: true }))
}

function changeSelect(select: HTMLSelectElement | null, value: string) {
  if (!select) throw new Error('select not found')
  const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

describe('HttpRequestDefaultsEditor', () => {
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

  function render(
    value: HTTPConfig,
    section: 'request' | 'session' = 'request',
    actions?: ScriptActionDefinition[],
  ) {
    const onChange = vi.fn()
    const onAddRuntimeAction = vi.fn()
    act(() => {
      root.render(
        <HttpRequestDefaultsEditor
          actions={actions || [
            { name: 'Orders', type: 'GET', target: 'https://orders.example.test/api', headers: { 'X-Action': 'yes' } },
            { name: 'Billing', type: 'POST', target: 'https://billing.example.test/api' },
          ]}
          onAddRuntimeAction={onAddRuntimeAction}
          onChange={onChange}
          section={section}
          value={value}
        />,
      )
    })
    return { onAddRuntimeAction, onChange }
  }

  it('promotes legacy headers into the ordered list when editing', () => {
    const { onChange } = render({ default_headers: { 'X-Legacy': 'one' } })

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Add header')) || null))

    expect(onChange).toHaveBeenLastCalledWith({
      default_header_list: [
        { name: 'X-Legacy', value: 'one' },
        { name: '', value: '', enabled: true },
      ],
      default_headers: undefined,
    })
  })

  it('bulk-imports ordered and repeated header arguments', () => {
    const { onChange } = render({})

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Paste headers') || null))
    act(() => changeTextarea(
      container.querySelector('.http-bulk-editor textarea'),
      'X-Route: blue\n-H "X-Route: green"\nAccept: application/json',
    ))
    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Import headers') || null))

    expect(onChange).toHaveBeenLastCalledWith({
      default_header_list: [
        { enabled: true, name: 'X-Route', value: 'blue' },
        { enabled: true, name: 'X-Route', value: 'green' },
        { enabled: true, name: 'Accept', value: 'application/json' },
      ],
      default_headers: undefined,
    })
  })

  it('bulk-import errors identify locations without echoing values', () => {
    const canary = 'sensitive-bulk-value'
    const result = parseBulkHeaders(`not-a-header ${canary}`)
    expect(result.errors).toEqual(['Line 1: enter a valid “Name: Value” header.'])
    expect(result.errors.join(' ')).not.toContain(canary)
  })

  it('reports transport-owned and typed-setting conflicts without disclosing values', () => {
    const secret = 'credential-canary-never-show'
    render({
      version: '2',
      default_header_list: [
        { name: 'Content-Length', value: secret },
        { name: 'User-Agent', value: secret },
      ],
    })

    expect(container.textContent).toContain('owned by the HTTP transport')
    expect(container.textContent).toContain('duplicates a typed HTTP setting')
    expect(container.textContent).not.toContain(secret)
  })

  it('focuses the first invalid header row when validation findings are shown', () => {
    render({
      default_header_list: [
        { name: 'X-Valid', value: 'one' },
        { name: 'Bad Header', value: 'two' },
      ],
    })

    expect(document.activeElement).toBe(container.querySelector('input[aria-label="Header 2 name"]'))
  })

  it('edits pinned cookies separately and inserts native cleanup actions', () => {
    const { onAddRuntimeAction } = render({
      default_header_list: [
        { name: 'Cookie', value: 'dark=true; route=blue' },
        { name: 'X-Tenant', value: 'one' },
      ],
      cookies_enabled: true,
    }, 'session')

    expect(container.querySelector<HTMLInputElement>('input[aria-label="Pinned cookie 1 name"]')?.value).toBe('dark')
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Pinned cookie 2 name"]')?.value).toBe('route')

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Clear simulated cache') || null))
    expect(onAddRuntimeAction).toHaveBeenCalledWith('cache_clear')
  })

  it('bulk-imports semicolon and Cookie-header pinned cookies', () => {
    expect(parseBulkCookies('Cookie: dark=blue; feature={{secret.preview}}\nroute=isolated')).toEqual({
      entries: [
        { name: 'dark', value: 'blue' },
        { name: 'feature', value: '{{secret.preview}}' },
        { name: 'route', value: 'isolated' },
      ],
      errors: [],
    })
  })

  it('accepts curl header argument variants and reports an empty Cookie header', () => {
    expect(parseBulkHeaders('--header=X-One: one\n-H"X-Two: two"\n-HX-Three: three')).toEqual({
      entries: [
        { enabled: true, name: 'X-One', value: 'one' },
        { enabled: true, name: 'X-Two', value: 'two' },
        { enabled: true, name: 'X-Three', value: 'three' },
      ],
      errors: [],
    })
    expect(parseBulkCookies('Cookie:')).toEqual({
      entries: [],
      errors: ['Enter at least one valid “name=value” cookie.'],
    })
  })

  it('keeps a newly added blank pinned-cookie row available for editing', () => {
    const { onChange } = render({}, 'session')

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Add cookie')) || null))

    expect(onChange).toHaveBeenLastCalledWith({
      default_header_list: [{ name: 'Cookie', value: '=', enabled: true }],
      default_headers: undefined,
    })
  })

  it('preserves a masked pinned-cookie sentinel when another row is added', () => {
    const { onChange } = render({
      default_header_list: [{ name: 'Cookie', value: '[REDACTED]' }],
    }, 'session')

    expect(countPinnedCookieErrors({
      default_header_list: [{ name: 'Cookie', value: '[REDACTED]' }],
    })).toBe(0)
    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Add cookie')) || null))

    expect(onChange).toHaveBeenLastCalledWith({
      default_header_list: [
        { name: 'Cookie', value: '[REDACTED]', enabled: true },
        { name: 'Cookie', value: '=', enabled: true },
      ],
      default_headers: undefined,
    })
  })

  it('preserves a structured masked cookie by name when another row is added', () => {
    const { onChange } = render({
      default_header_list: [{ name: 'Cookie', value: 'dark-route=[REDACTED]' }],
    }, 'session')

    expect(countPinnedCookies({
      default_header_list: [{ name: 'Cookie', value: 'dark-route=[REDACTED]' }],
    })).toBe(1)
    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Add cookie')) || null))

    expect(onChange).toHaveBeenLastCalledWith({
      default_header_list: [
        { name: 'Cookie', value: 'dark-route=[REDACTED]', enabled: true },
        { name: 'Cookie', value: '=', enabled: true },
      ],
      default_headers: undefined,
    })
  })

  it('treats cookie names as case-sensitive', () => {
    expect(countPinnedCookieErrors({
      default_header_list: [
        { name: 'Cookie', value: 'SID=upper' },
        { name: 'Cookie', value: 'sid=lower' },
      ],
    })).toBe(0)
  })

  it('masks sensitive values in the effective request preview', () => {
    const secret = 'super-secret-bearer'
    render({
      default_header_list: [
        { name: 'Authorization', value: `Bearer ${secret}` },
        { name: 'Accept', value: 'application/json' },
      ],
    })

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Show preview') || null))
    expect(container.textContent).toContain('••••••••')
    expect(container.textContent).toContain('application/json')
    expect(container.textContent).not.toContain(secret)
  })

  it('shows recorded session and anti-forgery headers without a credential warning', () => {
    render({
      default_header_list: [
        { name: 'X-CSRF-Token', value: 'recorded-csrf-52' },
        { name: 'X-Auth-Token', value: 'operator-token-63' },
      ],
    })

    expect(container.querySelector<HTMLInputElement>('input[aria-label="Header 1 value"]')?.type).toBe('text')
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Header 2 value"]')?.type).toBe('password')
    expect(container.textContent).not.toContain('X-CSRF-Token may contain a credential')
    expect(container.textContent).toContain('X-Auth-Token may contain a credential')
    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Show preview') || null))
    expect(container.textContent).toContain('recorded-csrf-52')
    expect(container.textContent).not.toContain('operator-token-63')
  })

  it('shows only the winning authored default when it overrides a typed header', () => {
    render({
      user_agent: 'typed-agent',
      default_header_list: [{ name: 'user-agent', value: 'authored-agent' }],
    })

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Show preview') || null))
    const previewRows = Array.from(container.querySelectorAll('[aria-label="Effective request headers"] [role="listitem"]'))
      .filter(row => row.querySelector('code')?.textContent?.toLowerCase() === 'user-agent')
    expect(previewRows).toHaveLength(1)
    expect(previewRows[0].textContent).toContain('authored-agent')
    expect(previewRows[0].textContent).not.toContain('typed-agent')
    expect(container.querySelector('.http-preview-suppressed')?.textContent).toContain('Typed setting')
    expect(container.querySelector('.http-preview-suppressed')?.textContent).toContain('Script default')
  })

  it('includes masked typed authentication in the effective preview', () => {
    render({
      auth: { type: 'bearer', token: '[REDACTED]' },
    })

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Show preview') || null))
    const authorizationRow = Array.from(container.querySelectorAll('.http-effective-preview-list [role="listitem"]'))
      .find(row => row.querySelector('code')?.textContent === 'Authorization')
    expect(authorizationRow?.textContent).toContain('••••••••')
    expect(authorizationRow?.textContent).not.toContain('generated from typed authentication')
  })

  it('labels each effective preview header with its winning origin', () => {
    render({
      user_agent: 'typed-agent',
      default_header_list: [{ name: 'X-Script', value: 'one' }],
    })

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Show preview') || null))
    expect(container.textContent).toContain('Typed setting')
    expect(container.textContent).toContain('Script default')
    expect(container.textContent).toContain('Action override')
  })

  it('mirrors disabled ordered-row suppression and shows request context', () => {
    render({
      version: '2',
      user_agent: 'typed-agent',
      default_headers: { 'User-Agent': 'compatibility-agent' },
      default_header_list: [{ name: 'user-agent', value: '', enabled: false }],
    }, 'request', [{
      name: 'Disabled action header',
      type: 'GET',
      target: 'https://example.test/orders',
    }])

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Show preview') || null))
    const effectiveNames = Array.from(container.querySelectorAll('[aria-label="Effective request headers"] code'))
      .map(node => node.textContent?.toLowerCase())
    expect(effectiveNames).not.toContain('user-agent')
    expect(container.querySelector('.http-preview-request-context')?.textContent).toContain('https://example.test/orders')
    expect(container.querySelector('.http-preview-request-context')?.textContent).toContain('HTTP/2')
    expect(container.querySelector('.http-preview-suppressed')?.textContent).toContain('Disabled default row')
    expect(container.querySelector('.http-preview-suppressed')?.textContent).toContain('Compatibility default')
  })

  it('shows when a disabled action row suppresses an inherited default', () => {
    render({
      default_header_list: [{ name: 'X-Feature', value: 'script-default' }],
    }, 'request', [{
      name: 'Suppress feature',
      type: 'GET',
      target: '/orders',
      headers: { 'X-Feature': 'compatibility-action' },
      header_list: [{ name: 'x-feature', value: '', enabled: false }],
    }])

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Show preview') || null))
    const effectiveNames = Array.from(container.querySelectorAll('[aria-label="Effective request headers"] code'))
      .map(node => node.textContent?.toLowerCase())
    expect(effectiveNames).not.toContain('x-feature')
    expect(container.querySelector('.http-preview-suppressed')?.textContent).toContain('Disabled action row')
    expect(container.querySelector('.http-preview-suppressed')?.textContent).toContain('Action compatibility map')
  })

  it('excludes invalid rows from the effective preview and explains that execution is blocked', () => {
    render({
      user_agent: 'typed-agent',
      default_header_list: [
        { name: '', value: 'unfinished' },
        { name: 'Content-Length', value: '12' },
      ],
    })

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Show preview') || null))
    const effectiveNames = Array.from(container.querySelectorAll('[aria-label="Effective request headers"] code'))
      .map(node => node.textContent)
    expect(effectiveNames).toContain('User-Agent')
    expect(effectiveNames).not.toContain('')
    expect(effectiveNames).not.toContain('Content-Length')
    expect(container.querySelector('[role="note"]')?.textContent).toContain('2 header validation errors prevent')
  })

  it('offers grouped HTTP sequence child requests in the preview', () => {
    render({}, 'request', [{
      name: 'Checkout sequence',
      type: 'http_sequence',
      target: '',
      params: {
        requests: [
          { name: 'Load cart', method: 'GET', target: '/cart', headers: { 'X-Step': 'cart' } },
          { name: 'Submit order', method: 'POST', target: '/orders', headers: { 'X-Step': 'order' } },
        ],
      },
    }])

    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Show preview') || null))
    const options = Array.from(container.querySelectorAll<HTMLSelectElement>('.http-effective-preview select option'))
      .map(option => option.textContent)
    expect(options).toEqual(['Load cart', 'Submit order'])
    expect(container.textContent).toContain('/cart')
    expect(container.textContent).toContain('X-Step')
  })

  it('clamps the selected request when the focused script has fewer actions', () => {
    const initialActions: ScriptActionDefinition[] = [
      { name: 'First', type: 'GET', target: '/first' },
      { name: 'Second', type: 'GET', target: '/second' },
      { name: 'Third', type: 'GET', target: '/third' },
    ]
    render({}, 'request', initialActions)
    act(() => click(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Show preview') || null))
    const requestSelect = container.querySelector<HTMLSelectElement>('.http-effective-preview select')
    act(() => changeSelect(requestSelect, '2'))
    expect(container.querySelector('.http-preview-request-context')?.textContent).toContain('/third')

    render({}, 'request', [{ name: 'Only request', type: 'GET', target: '/only' }])

    expect(container.querySelector<HTMLSelectElement>('.http-effective-preview select')?.value).toBe('0')
    expect(container.querySelector('.http-preview-request-context')?.textContent).toContain('/only')
  })

  it('counts request and pinned-cookie errors for non-color tab badges', () => {
    const value: HTTPConfig = {
      version: '2',
      default_header_list: [
        { name: 'Connection', value: 'close' },
        { name: 'Cookie', value: 'duplicate=one' },
        { name: 'Cookie', value: 'duplicate=two' },
      ],
    }
    expect(countHTTPDefaultHeaderErrors(value)).toBe(1)
    expect(countPinnedCookieErrors(value)).toBe(2)
  })

  it('does not double-count compatibility-map names shadowed by ordered rows', () => {
    const value: HTTPConfig = {
      default_headers: { 'X-Tenant': 'legacy', 'X-Map-Only': 'one' },
      default_header_list: [
        { name: 'x-tenant', value: 'ordered' },
        { name: 'Cookie', value: 'dark=true; route=blue' },
      ],
    }
    expect(countHTTPDefaultHeaders(value)).toBe(2)
    expect(countPinnedCookies(value)).toBe(2)
  })

  it('shows imported socket options as mapped or review-only without making them editable', () => {
    act(() => {
      root.render(
        <SocketCompatibilityPanel
          additionalAttributes={{
            'web_set_sockets_option.MAX_CONNECTIONS_PER_HOST': '12',
            'web_set_sockets_option.UNSUPPORTED_FLAG': 'legacy',
          }}
          originLabel="Imported script"
        />,
      )
    })

    expect(container.textContent).toContain('Mapped to typed setting')
    expect(container.textContent).toContain('Review required')
    expect(container.querySelector('input')).toBeNull()
  })
})
