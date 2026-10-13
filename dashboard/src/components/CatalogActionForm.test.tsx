// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptActionDefinition } from '../types/scripts'
import CatalogActionForm from './CatalogActionForm'
import { LIVE_TABLE_SPEC } from './catalogActionFormFixture'
import { setCachedDslActionCatalog, type DslActionCatalog, type DslCatalogAction } from './dslActionCatalog'
import type { LiveTableFormContext } from './catalogActionFormModel'

const requestMock = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request: requestMock }))

const KAFKA_PRODUCE: DslCatalogAction = {
  type: 'kafka_produce', protocols: ['kafka'], summary: 'Produces a record.',
  fields: [
    { name: 'target', kind: 'string', doc: 'Topic.' },
    { name: 'body', kind: 'string' },
    { name: 'headers', kind: 'object' },
    { name: 'params.partition', kind: 'int' },
  ],
}
const catalog = (actions: DslCatalogAction[]): DslActionCatalog => ({ protocols: ['http', 'kafka'], common_fields: [], actions })

type Action = ScriptActionDefinition
let container: HTMLDivElement
let root: Root
let current: Action

function Harness({ initial, context }: { initial: Action; context?: LiveTableFormContext }) {
  const [action, setAction] = useState(initial)
  current = action
  return <CatalogActionForm action={action} index={0} context={context} onOpenSource={() => undefined} onUpdate={(_index, field, value) => setAction(previous => {
    const next = { ...previous }
    if (value === undefined) delete (next as Record<string, unknown>)[field]
    else (next as Record<string, unknown>)[field] = value
    return next
  })} />
}

const render = async (initial: Record<string, unknown>, context?: LiveTableFormContext) => {
  await act(async () => { root.render(<Harness initial={{ name: 'step', target: '', ...initial } as unknown as Action} context={context} />) })
}
const FIELD = 'input:not([type="radio"]):not([type="checkbox"]), select, textarea'
const field = (label: string) => Array.from(container.querySelectorAll('label'))
  .find(item => item.querySelector('span')?.textContent?.replace(' (required)', '') === label && item.querySelector(FIELD))
  ?.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(FIELD) || null
const button = (text: string) => Array.from(container.querySelectorAll('button')).find(item => item.textContent === text)!
function type(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(input), 'value')!.set!
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}
function pick(select: HTMLSelectElement, value: string) {
  select.value = value
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  setCachedDslActionCatalog(catalog([LIVE_TABLE_SPEC, KAFKA_PRODUCE]))
  requestMock.mockReset()
})
afterEach(async () => {
  await act(async () => { root.unmount() })
  container.remove()
  setCachedDslActionCatalog(null)
})

describe('CatalogActionForm', () => {
  it('waits for the catalog, says so when it cannot load, and retries', async () => {
    setCachedDslActionCatalog(null)
    let fail!: (error: Error) => void
    requestMock.mockReturnValueOnce(new Promise((_resolve, reject) => { fail = reject }))
    await render({ type: 'live_table', params: { alias: 'orders', operation: 'read', position: 1 } })
    expect(container.textContent).toContain('Loading this action’s fields')
    expect(container.querySelector('select')).toBeNull()
    await act(async () => { fail(new Error('offline')) })
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Couldn’t load')
    requestMock.mockResolvedValueOnce(catalog([LIVE_TABLE_SPEC]))
    await act(async () => { button('Try again').click() })
    expect(field('Operation')).not.toBeNull()
  })

  it('edits a live-table read by plain fields and choices, keeping the keys it did not touch in order', async () => {
    await render({ type: 'live_table', params: { alias: 'orders', operation: 'read', position: 1, result_prefix: 'row' } })
    const operation = field('Operation') as HTMLSelectElement
    expect(operation.selectedOptions[0].textContent).toBe('Read a row by ID or position')
    expect(Array.from(operation.querySelectorAll('optgroup')).map(group => group.label)).toEqual(['Read', 'Change', 'Take work', 'Columns'])
    expect(container.textContent).toContain('It does not change the table.')
    // The row is found by position: the record ID field is not offered.
    expect(field('Position')).not.toBeNull()
    expect(field('Record ID')).toBeNull()
    expect(field('Headers')).toBeNull()
    await act(async () => { type(field('Position') as HTMLInputElement, '4') })
    expect(Object.keys(current.params!)).toEqual(['alias', 'operation', 'position', 'result_prefix'])
    expect(current.params).toMatchObject({ position: 4 })

    const byRecord = Array.from(container.querySelectorAll<HTMLInputElement>('fieldset.catalog-choice input'))[0]
    await act(async () => { byRecord.click() })
    expect(current.params).toEqual({ alias: 'orders', operation: 'read', result_prefix: 'row' })
    expect(field('Record ID')).not.toBeNull()
  })

  it('asks before an operation switch removes fields, and keeps the action on Cancel', async () => {
    await render({ type: 'live_table', params: { alias: 'orders', operation: 'read', position: 1, columns: ['status'] } })
    await act(async () => { pick(field('Operation') as HTMLSelectElement, 'push') })
    const prompt = container.querySelector('[role="alertdialog"]')!
    expect(prompt.textContent).toContain('Add a row does not take Position, Columns. Switching removes them.')
    await act(async () => { button('Cancel').click() })
    expect(current.params).toEqual({ alias: 'orders', operation: 'read', position: 1, columns: ['status'] })
    await act(async () => { pick(field('Operation') as HTMLSelectElement, 'push') })
    await act(async () => { button('Switch').click() })
    expect(current.params).toEqual({ alias: 'orders', operation: 'push' })
    expect(container.querySelector('.catalog-values')).not.toBeNull()
  })

  it('lists settings the operation does not take, and removes them on request', async () => {
    await render({ type: 'live_table', params: { alias: 'orders', operation: 'next', position: 2 } })
    const stray = container.querySelector('[role="group"][aria-label="Not recognised"]')!
    expect(stray.textContent).toContain('Not used by “Read the next row”')
    await act(async () => { stray.querySelector<HTMLButtonElement>('button[aria-label="Remove position"]')!.click() })
    expect(current.params).toEqual({ alias: 'orders', operation: 'next' })
  })

  it('keeps a 64-bit increment as a decimal string, never a rounded number', async () => {
    await render({ type: 'live_table', params: { alias: 'orders', operation: 'increment', position: 1, columns: ['count'], delta: '1' } })
    const add = field('Add') as HTMLInputElement
    await act(async () => { type(add, '9007199254740993') })
    expect(current.params).toMatchObject({ delta: '9007199254740993' })
    await act(async () => { type(add, '1.5') })
    expect(container.textContent).toContain('Enter a whole number')
    expect(current.params).toMatchObject({ delta: '9007199254740993' })
  })

  it('suggests earlier outputs for a reference and plain labels for a policy', async () => {
    const context: LiveTableFormContext = { aliases: ['orders'], columnsByAlias: { orders: ['status'] }, outputs: { record_id: ['{{var.row.record_id}}'], record_version: ['{{var.row.record_version}}'], lease: ['job'] } }
    await render({ type: 'live_table', params: { alias: 'orders', operation: 'ack' } }, context)
    const lease = field('Lease') as HTMLInputElement
    expect(Array.from(document.getElementById(lease.getAttribute('list')!)?.querySelectorAll('option') || []).map(option => option.getAttribute('value'))).toEqual(['job'])
    await act(async () => { type(lease, 'job') })
    expect(current.params).toEqual({ alias: 'orders', operation: 'ack', lease_ref: 'job' })
  })

  it('offers plain labels for an empty-table policy', async () => {
    await render({ type: 'live_table', params: { alias: 'orders', operation: 'pop' } })
    const policy = Array.from(container.querySelectorAll('fieldset')).find(item => item.querySelector('legend')?.textContent === 'When no row is found')!
    expect(Array.from(policy.querySelectorAll('label')).map(item => item.textContent)).toEqual(['Default', 'End the iteration with an error', 'Skip and continue'])
    await act(async () => { policy.querySelectorAll<HTMLInputElement>('input')[2].click() })
    expect(current.params).toEqual({ alias: 'orders', operation: 'pop', on_empty: 'skip' })
  })

  it('does not guess a form when the controller describes no operations', async () => {
    setCachedDslActionCatalog(catalog([{ ...LIVE_TABLE_SPEC, variants: undefined, discriminator: undefined }]))
    await render({ type: 'live_table', params: { alias: 'orders', operation: 'read', position: 1 } })
    expect(container.textContent).toContain('needs a newer controller')
    expect(container.querySelector('select, input')).toBeNull()
    expect(button('Edit in Source')).toBeTruthy()
  })

  it('renders any other catalog action from its fields', async () => {
    await render({ type: 'kafka_produce', target: 'orders', params: { partition: 2 } })
    expect((field('Target') as HTMLInputElement).value).toBe('orders')
    const headers = field('Headers') as HTMLTextAreaElement
    await act(async () => { type(headers, '{"trace":"1"}') })
    await act(async () => { headers.dispatchEvent(new FocusEvent('blur', { bubbles: true })); headers.dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    expect(current.headers).toEqual({ trace: '1' })
    await act(async () => { type(headers, '[1]') })
    await act(async () => { headers.dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    expect(container.textContent).toContain('Enter a JSON object')
    await act(async () => { type(field('Partition') as HTMLInputElement, '') })
    expect(current.params).toEqual({})
  })

  it('keeps a message body’s line breaks, and a cleared target a string', async () => {
    await render({ type: 'kafka_produce', target: 'orders', body: '{\n  "id": 1\n}' })
    const body = field('Body') as HTMLTextAreaElement
    expect(body.tagName).toBe('TEXTAREA')
    await act(async () => { type(body, '{\n  "id": 2\n}') })
    expect(current.body).toBe('{\n  "id": 2\n}')
    await act(async () => { type(field('Target') as HTMLInputElement, '') })
    expect(current.target).toBe('')
  })

  it('takes only column names a live table accepts', async () => {
    await render({ type: 'live_table', params: { alias: 'orders', operation: 'next' } })
    const add = container.querySelector<HTMLInputElement>('input[aria-label="Add a column to Columns"]')!
    await act(async () => { type(add, '1st') })
    expect(container.textContent).toContain('Start with a letter or underscore')
    expect(button('Add').disabled).toBe(true)
    await act(async () => { type(add, 'status') })
    await act(async () => { button('Add').click() })
    expect(current.params).toEqual({ alias: 'orders', operation: 'next', columns: ['status'] })
  })

  it('asks before switching away from an operation it does not know', async () => {
    await render({ type: 'live_table', params: { alias: 'orders', operation: 'peek', position: 2 } })
    expect(container.textContent).toContain('is not one this controller knows')
    await act(async () => { pick(field('Operation') as HTMLSelectElement, 'push') })
    expect(container.querySelector('[role="alertdialog"]')?.textContent).toContain('does not take Position')
    expect(current.params).toEqual({ alias: 'orders', operation: 'peek', position: 2 })
  })

  it('sends an unknown type to Source', async () => {
    await render({ type: 'teleport' })
    expect(container.textContent).toContain('does not describe the teleport action')
  })
})
