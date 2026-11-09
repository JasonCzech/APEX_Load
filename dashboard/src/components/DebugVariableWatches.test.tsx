// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { DebugVariableWatches } from './DebuggerSessionTools'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('does not treat an absent pinned prototype-like name as a changed value', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const props = { events: [], seed: {}, selected: null, onSelect: () => {}, mode: 'retained' as const }
  try {
    await act(async () => root.render(<DebugVariableWatches {...props} latest={JSON.parse('{"constructor":"value"}')} />))
    const pin = host.querySelector<HTMLButtonElement>('button[aria-label="Pin constructor"]')!
    expect(pin).toBeTruthy()
    await act(async () => pin.click())
    await act(async () => root.render(<DebugVariableWatches {...props} latest={{}} />))
    expect(host.textContent).toContain('Not available')
    const changedOnly = Array.from(host.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))[0]
    await act(async () => changedOnly.click())
    expect(host.querySelector('button[aria-label="Pin constructor"]')).toBeNull()
  } finally {
    await act(async () => root.unmount())
  }
})

it('shows all authored variables and script parameters in the watch table while protecting sensitive parameter values', async () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const script = {
    name: 'Checkout',
    version: '1.0',
    protocol: 'http',
    config: { base_url: 'https://example.test' },
    parameters: [
      { name: 'region', type: 'list', values: ['us-east', 'us-west'], update: 'each_iteration' },
      { name: 'api_secret_password', type: 'list', values: ['raw-secret-123'], update: 'once' },
      { name: 'seq', type: 'counter', start: 10 },
    ],
    actions: [
      { name: 'Fetch cart', type: 'request', method: 'GET', target: '/cart', extractors: [{ name: 'cart_id', source: 'body', type: 'jsonpath', expression: '$.id' }] },
    ],
  }
  try {
    await act(async () => root.render(
      <DebugVariableWatches
        script={script}
        events={[]}
        seed={{ seeded_var: 'seed-val' }}
        latest={{ extracted_var: 'live-val' }}
        selected={null}
        onSelect={() => {}}
        mode="retained"
        completed
      />,
    ))
    const table = host.querySelector<HTMLTableElement>('table.debug-watch-table')
    expect(table?.getAttribute('data-table-pagination')).toBe('off')
    expect(host.querySelector('.debug-watch-panel')?.getAttribute('data-table-pagination')).toBe('off')
    const rows = Array.from(host.querySelectorAll('table.debug-watch-table tbody tr'))
    const text = rows.map(r => r.textContent)
    expect(text.some(t => t?.includes('extracted_var') && t.includes('live-val'))).toBe(true)
    expect(text.some(t => t?.includes('seeded_var') && t.includes('seed-val'))).toBe(true)
    expect(text.some(t => t?.includes('cart_id') && t.includes('Authored in Fetch cart'))).toBe(true)
    expect(text.some(t => t?.includes('param.region') && t.includes('us-east'))).toBe(true)
    expect(text.some(t => t?.includes('param.seq') && t.includes('10'))).toBe(true)
    expect(text.some(t => t?.includes('param.api_secret_password') && t.includes('[REDACTED]'))).toBe(true)
    expect(host.textContent).not.toContain('raw-secret-123')

    const typeGroup = host.querySelector('[role="group"][aria-label="Filter by parameter or variable type"]')
    expect(typeGroup).toBeTruthy()
    const chipByLabel = (label: string) =>
      Array.from(typeGroup!.querySelectorAll<HTMLButtonElement>('button')).find(btn => btn.textContent === label)!

    expect(chipByLabel('All types').getAttribute('aria-pressed')).toBe('true')

    // Select 'list' type
    await act(async () => chipByLabel('list').click())
    expect(chipByLabel('list').getAttribute('aria-pressed')).toBe('true')
    expect(chipByLabel('All types').getAttribute('aria-pressed')).toBe('false')
    let filteredNames = Array.from(host.querySelectorAll('table.debug-watch-table tbody tr th[scope="row"]')).map(th => th.textContent)
    expect(filteredNames.some(t => t?.includes('param.region'))).toBe(true)
    expect(filteredNames.some(t => t?.includes('param.api_secret_password'))).toBe(true)
    expect(filteredNames.some(t => t?.includes('param.seq'))).toBe(false)
    expect(filteredNames.some(t => t?.includes('cart_id'))).toBe(false)

    // Multi-select 'counter' in addition to 'list'
    await act(async () => chipByLabel('counter').click())
    expect(chipByLabel('list').getAttribute('aria-pressed')).toBe('true')
    expect(chipByLabel('counter').getAttribute('aria-pressed')).toBe('true')
    filteredNames = Array.from(host.querySelectorAll('table.debug-watch-table tbody tr th[scope="row"]')).map(th => th.textContent)
    expect(filteredNames.some(t => t?.includes('param.region'))).toBe(true)
    expect(filteredNames.some(t => t?.includes('param.seq'))).toBe(true)
    expect(filteredNames.some(t => t?.includes('cart_id'))).toBe(false)

    // Reset with 'All types'
    await act(async () => chipByLabel('All types').click())
    expect(chipByLabel('All types').getAttribute('aria-pressed')).toBe('true')
    expect(host.querySelectorAll('table.debug-watch-table tbody tr')).toHaveLength(6)
  } finally {
    await act(async () => root.unmount())
  }
})
