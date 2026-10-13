// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AuthorInsertDialog, type InsertSharedData } from './AuthorInsertDialog'
import fixture from './catalogActionFormFixture.json'
import { setCachedDslActionCatalog, type DslCatalogAction } from './dslActionCatalog'
import type { DataAliasUsage } from './scriptDataUsageModel'

let container: HTMLDivElement
let root: Root
const usage: DataAliasUsage[] = [{ alias: 'orders', columns: ['id'], operations: ['read'], nativeOperations: [], mutates: false, kinds: [], readsEachIteration: false, suppliedByScript: false, steps: [] }]
const sharedData: InsertSharedData = {
  usage,
  bindings: new Map([['orders', [{ scenarioId: 's', scenarioName: 'Checkout', binding: { alias: 'orders', pool_id: '', access_mode: 'live', selection_strategy: 'manual' }, choice: 'run', kind: 'vts_columns' }]]]),
  context: alias => ({ alias, columns: ['id'], taken: new Set(['row', 'orders']), names: new Set(), earlier: { record_id: [], record_version: [], lease: [] }, variables: new Set() }),
}

beforeEach(() => {
  setCachedDslActionCatalog({ protocols: ['http'], common_fields: [], actions: [{ type: 'GET', protocols: ['http'], summary: 'A GET request', fields: [{ name: 'target', kind: 'string', required: true }] }, fixture as unknown as DslCatalogAction] })
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})
afterEach(async () => {
  await act(async () => root.unmount())
  container.remove()
  setCachedDslActionCatalog(null)
})

async function render(handlers: { onInsert?: () => void; onInsertMany?: () => void } = {}) {
  await act(async () => root.render(<AuthorInsertDialog protocol="http" seed={{ name: 'Action_1', type: 'GET', target: '' }} phase="action" position="end" canInsertInside={false}
    onPlan={() => undefined} onInsert={handlers.onInsert || vi.fn()} onInsertMany={handlers.onInsertMany || vi.fn()} onClose={() => undefined} library={null} sharedData={sharedData} />))
}
const dialog = () => document.body.querySelector('[role="dialog"]')!
const select = async (value: string) => {
  const type = dialog().querySelector<HTMLSelectElement>('select[aria-label="Action type"]')!
  await act(async () => { type.value = value; type.dispatchEvent(new Event('change', { bubbles: true })) })
}
const field = (name: string) => [...dialog().querySelectorAll('label')].find(label => label.textContent?.startsWith(name))!.querySelector('input')!
const type = async (input: HTMLInputElement, value: string) => act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
const click = async (name: string) => act(async () => [...dialog().querySelectorAll('button')].find(button => button.textContent === name)!.click())

describe('AuthorInsertDialog shared data', () => {
  it('lists table operations as plain verbs, marking the table kind each needs', async () => {
    await render()
    const groups = [...dialog().querySelectorAll('optgroup')].map(group => group.label)
    expect(groups).toEqual(['Requests', 'Shared data: workflows', 'Shared data: read', 'Shared data: change', 'Shared data: take work', 'Shared data: columns'])
    const labels = [...dialog().querySelectorAll('option')].map(option => option.textContent)
    expect(labels).toContain('Lease a row (needs a work queue table)')
    expect(labels).toContain('Lease, process, acknowledge (needs a work queue table)')
    expect(labels).not.toContain('Live table')
  })

  it('inserts an operation with its name, a free result prefix and safe defaults', async () => {
    const onInsert = vi.fn()
    await render({ onInsert })
    await select('live_table:read')
    expect(dialog().textContent).toContain('Most scripts need no table steps')
    expect(field('Action name').value).toBe('Read a row by ID or position (orders)')
    expect(field('Save results as').value).toBe('row_2')
    await type(field('Save results as'), 'orders')
    await click('Insert action')
    expect(dialog().querySelector('[role="alert"]')!.textContent).toBe('Another step or data source already uses the name orders. Choose another.')
    await type(field('Save results as'), 'order_row')
    await click('Insert action')
    expect(onInsert).toHaveBeenCalledWith({ name: 'Read a row by ID or position (orders)', type: 'live_table', target: '', params: { alias: 'orders', operation: 'read', result_prefix: 'order_row', position: 1, on_empty: 'error' } })
  })

  it('inserts a linked template, saying which kind of table it needs and where the alias is bound otherwise', async () => {
    const onInsertMany = vi.fn()
    await render({ onInsertMany })
    await select('template:lease_ack')
    expect(dialog().textContent).toContain('Needs a work queue table. orders is a shared columns table in Checkout.')
    // A stray space does not hide where the alias is bound.
    await type(field('Table alias'), 'orders ')
    expect(dialog().textContent).toContain('orders is a shared columns table in Checkout.')
    expect(dialog().textContent).toContain('Inserts 3 steps: Lease a row from orders, When a row was leased, Acknowledge the leased row. Your own steps go before “Acknowledge the leased row”.')
    await type(field('Table alias'), '1bad')
    await click('Insert 3 steps')
    expect(dialog().querySelector('[role="alert"]')!.textContent).toContain('Enter the table alias')
    await type(field('Table alias'), 'jobs')
    await click('Insert 3 steps')
    const [actions, label, spot] = onInsertMany.mock.lastCall!
    expect([label, spot]).toEqual(['Lease, process, acknowledge', { name: 'Acknowledge the leased row', position: 'before' }])
    expect(actions[0].params.alias).toBe('jobs')
    expect(actions[1].control.steps[0].params.alias).toBe('jobs')
  })
})
