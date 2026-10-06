// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import BusinessKeyLookup from './BusinessKeyLookup'
import { parseBusinessKeyValues } from './businessKeyModel'

const mocks = vi.hoisted(() => {
  class ApiError extends Error {
    status: number
    constructor(message: string, status: number) {
      super(message)
      this.status = status
    }
  }
  return { request: vi.fn(), ApiError }
})
vi.mock('../../hooks/apiClient', () => ({ request: mocks.request, ApiError: mocks.ApiError }))

const mapping = { id: 'map-1', project_id: 'proj', kind: 'business_key_mapping', name: 'Order ID', revision: 2, deleted: false, definition: { extraction: 'order_id', source_id: 'elastic-1', field: 'order.id', label: 'Order ID' } }
const lookup = {
  mapping: { id: 'map-1', revision: 2, label: 'Order ID', extraction: 'order_id', source_id: 'elastic-1', field: 'order.id' },
  results: [
    { index: 0, requests: { total: 3, matches: [{ timestamp: '2026-10-05T12:00:01Z', group: 'shop', name: 'POST /pay', duration_ms: 820, success: false, status_code: 502, trace_id: 'abc', matched_by: 'digest' }] },
      logs: { status: 'found', total: 1, entries: [{ timestamp: '2026-10-05T12:00:02Z', trace_id: 'abc', message: 'payment failed' }] } },
    { index: 1, requests: { total: 0, matches: [] }, logs: { status: 'not_found', total: 0, entries: [] } },
  ],
  limitations: ['this run was archived before the mapping was approved, so its values are matched as archived'],
}

describe('BusinessKeyLookup', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mocks.request.mockReset()
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const routes = (overrides: Record<string, unknown> = {}) => mocks.request.mockImplementation(async (path: string, options?: { method?: string; body?: string }) => {
    for (const [prefix, value] of Object.entries(overrides)) {
      if (path.startsWith(prefix)) {
        if (value instanceof Error) throw value
        return typeof value === 'function' ? (value as (p: string, o?: unknown) => unknown)(path, options) : value
      }
    }
    if (path.startsWith('/analysis/business-key-mappings')) return [mapping, { ...mapping, id: 'gone', deleted: true }]
    if (path.startsWith('/projects/')) return [{ id: 'elastic-1', name: 'Order logs', pack: 'elastic-query' }, { id: 'prom-1', name: 'Metrics', pack: 'prometheus' }]
    if (path.includes('/business-keys/lookup')) return lookup
    throw new Error('unexpected ' + path)
  })

  const render = async (props: Partial<Parameters<typeof BusinessKeyLookup>[0]> = {}) => {
    await act(async () => {
      root.render(<BusinessKeyLookup testId="run-1" projectId="proj" canLookup canApprove={false} {...props} />)
    })
  }
  const button = (name: string) => Array.from(container.querySelectorAll('button')).find(b => b.textContent === name)!
  const type = async (element: HTMLTextAreaElement | HTMLInputElement, value: string) => {
    const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(element), 'value')!.set!
    await act(async () => {
      setter.call(element, value)
      element.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }

  it('splits values into distinct trimmed lines', () => {
    expect(parseBusinessKeyValues(' ORD-1 \n\nORD-2\nORD-1\n')).toEqual(['ORD-1', 'ORD-2'])
  })

  it('stays hidden where the project does not use business keys', async () => {
    routes({ '/analysis/business-key-mappings': new mocks.ApiError('analysis is not enabled for project', 409) })
    await render()
    expect(container.innerHTML).toBe('')
  })

  it('looks values up once and shows requests and logs per value', async () => {
    routes()
    await render()
    expect(container.querySelector('select')!.textContent).toBe('Order ID')
    const area = container.querySelector('textarea')!
    expect(area.getAttribute('autocomplete')).toBe('off')
    await type(area, 'ORD-1\nORD-404\nORD-1')
    await act(async () => button('Look up').click())
    const call = mocks.request.mock.calls.find(([path]) => String(path).includes('/business-keys/lookup'))!
    expect(call[0]).toBe('/tests/run-1/archive/business-keys/lookup')
    expect(JSON.parse(call[1].body)).toEqual({ mapping_id: 'map-1', values: ['ORD-1', 'ORD-404'] })
    const first = container.querySelector('[aria-label="Result for ORD-1"]')!
    expect(first.textContent).toContain('3 archived requests · Found in the logs (1)')
    expect(first.textContent).toContain('shop · POST /pay')
    expect(first.textContent).toContain('Failed (502)')
    expect(first.textContent).toContain('payment failed')
    expect(container.querySelector('[aria-label="Result for ORD-404"]')!.textContent).toContain('0 archived requests · Not found in the logs')
    expect(container.textContent).toContain('archived before the mapping was approved')

    // Another run starts empty.
    await render({ testId: 'run-2' })
    expect(container.querySelector('textarea')!.value).toBe('')
    expect(container.querySelector('[aria-label="Result for ORD-1"]')).toBeNull()
  })

  it('refuses more than twenty values before sending them', async () => {
    routes()
    await render()
    await type(container.querySelector('textarea')!, Array.from({ length: 21 }, (_, i) => `ORD-${i}`).join('\n'))
    expect(container.textContent).toContain('Look up at most 20 values at a time.')
    expect(button('Look up').disabled).toBe(true)
  })

  it('lets viewers see the mappings but not look values up', async () => {
    routes()
    await render({ canLookup: false })
    expect(container.querySelector('textarea')).toBeNull()
    expect(container.textContent).toContain('Editors can look business keys up.')
  })

  it('lets project admins approve and remove mappings on elastic-query sources', async () => {
    let saved: unknown
    routes({
      '/analysis/business-key-mappings/map-1': (path: string, options: { method?: string }) => { saved = { path, method: options?.method }; return {} },
      '/analysis/business-key-mappings?': (_path: string, options?: { method?: string; body?: string }) => {
        if (options?.method === 'POST') { saved = JSON.parse(options.body!); return {} }
        return [mapping]
      },
    })
    await render({ canApprove: true })
    const form = container.querySelector('.business-key-mapping-form')!
    const sourceSelect = form.querySelector('select')!
    expect(Array.from(sourceSelect.options).map(o => o.textContent)).toEqual(['Choose an Elasticsearch or OpenSearch source', 'Order logs'])
    const inputs = form.querySelectorAll('input')
    await type(inputs[0], 'Account')
    await type(inputs[1], 'account_id')
    await type(inputs[2], 'account.ban')
    await act(async () => {
      sourceSelect.value = 'elastic-1'
      sourceSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => button('Approve mapping').click())
    expect(saved).toEqual({ name: 'Account', definition: { extraction: 'account_id', source_id: 'elastic-1', field: 'account.ban', label: 'Account' } })
    await act(async () => button('Remove').click())
    expect(saved).toEqual({ path: '/analysis/business-key-mappings/map-1?project_id=proj&revision=2', method: 'DELETE' })
  })
})
