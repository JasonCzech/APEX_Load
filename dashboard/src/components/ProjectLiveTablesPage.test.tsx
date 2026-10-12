// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ProjectLiveTablesPage from './ProjectLiveTablesPage'
import { ApiError } from '../hooks/apiClient'
import type { ProjectLiveTableView } from '../types/projectLiveTables'

const { api } = vi.hoisted(() => ({ api: { listProjectLiveTables: vi.fn(), getProjectLiveTable: vi.fn(), listDataPools: vi.fn(), createProjectLiveTable: vi.fn(), getProjectLiveTableRows: vi.fn(), mutateProjectLiveTable: vi.fn(), changeProjectLiveTableLifecycle: vi.fn(), exportProjectLiveTable: vi.fn() } }))
vi.mock('../hooks/useApi', () => ({ useApi: () => api }))
const table: ProjectLiveTableView = { id: 'table', project_id: 'project', name: 'Shared work', profile: 'native_queue', columns: ['id', 'note'], state: 'active', config_revision: '2', instance_id: 'instance', stats: { revision: '9007199254740993', counts: { available: 1, leased: 0 }, capacity: { max_rows: 10, max_bytes: 1024 } }, attachments: [], permissions: { can_view: true, can_mutate: true, can_admin: true } }
const preview = { columns: table.columns, revision: table.stats!.revision, total: 1, rows: [{ id: 'row', version: '9007199254740995', values: { id: 'private-value', note: '' }, state: 'available' }] }
let container: HTMLDivElement; let root: Root
beforeEach(() => { vi.clearAllMocks(); api.listProjectLiveTables.mockResolvedValue({ tables: [table] }); api.getProjectLiveTable.mockResolvedValue(table); api.listDataPools.mockResolvedValue({ data_pools: [] }); api.getProjectLiveTableRows.mockResolvedValue(preview); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })
const button = (label: string) => [...container.querySelectorAll('button')].find(element => element.textContent === label)!
async function click(label: string) { await act(async () => button(label).click()) }
async function open() {
  await act(async () => root.render(<ProjectLiveTablesPage projectId="project" />))
  await act(async () => { const select = container.querySelector('select')!; select.value = 'table'; select.dispatchEvent(new Event('change', { bubbles: true })) })
}
function changeInput(element: HTMLInputElement, value: string) { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(element, value); element.dispatchEvent(new Event('input', { bubbles: true })) }

it('requires deliberate preview and preserves exact revisions in conditional edits', async () => {
  api.mutateProjectLiveTable.mockResolvedValue({ result: { status: 'condition_failed', revision: table.stats!.revision }, replayed: false })
  await open()
  expect(api.getProjectLiveTableRows).not.toHaveBeenCalled()
  expect(container.textContent).not.toContain('private-value')
  await click('Preview up to 100 rows')
  expect(api.getProjectLiveTableRows).toHaveBeenCalledWith('project', 'table', 'instance', '9007199254740993', 0)
  await click('Edit row')
  const field = container.querySelector<HTMLInputElement>('input[aria-label="Value for id"]')!
  await act(async () => changeInput(field, 'my-draft'))
  await click('Apply row change')
  expect(api.mutateProjectLiveTable).toHaveBeenCalledWith('project', 'table', expect.objectContaining({ expected_revision: '9007199254740993', record_id: 'row', expected_version: '9007199254740995', values: { id: 'my-draft', note: '' } }))
  expect(container.querySelector<HTMLInputElement>('input[aria-label="Value for id"]')!.value).toBe('my-draft')
  expect(container.textContent).toContain('Your draft is preserved')
})

it('retries an uncertain command with its original key, timestamp and body', async () => {
  api.mutateProjectLiveTable.mockRejectedValueOnce(new TypeError('Network disconnected')).mockResolvedValueOnce({ result: { status: 'ok', revision: '9007199254740994' }, replayed: true })
  await open()
  await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Include id"]')!.click())
  await click('Append row')
  const first = api.mutateProjectLiveTable.mock.calls[0][2]
  expect(first.idempotency_key).toMatch(/^[\da-f-]{36}$/)
  expect(button('Append row').matches(':disabled')).toBe(true)
  await click('Retry same command')
  expect(api.mutateProjectLiveTable.mock.calls[1][2]).toEqual(first)
  expect(container.textContent).toContain('Original outcome recovered')
})

it('retains an initial forbidden response that may follow a committed command', async () => {
  api.mutateProjectLiveTable.mockRejectedValueOnce(new ApiError('Access changed', 403)).mockResolvedValueOnce({ result: { status: 'ok', revision: table.stats!.revision }, replayed: true })
  await open()
  await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Include id"]')!.click())
  await click('Append row')
  const command = api.mutateProjectLiveTable.mock.calls[0][2]
  expect(button('Append row').matches(':disabled')).toBe(true)
  await click('Retry same command')
  expect(api.mutateProjectLiveTable.mock.calls[1][2]).toEqual(command)
})

it.each([429, 403, 409])('retains an uncertain identity after a retry receives HTTP %i', async status => {
  api.mutateProjectLiveTable.mockRejectedValueOnce(new TypeError('Disconnected')).mockRejectedValueOnce(new ApiError('Retry unavailable', status)).mockResolvedValueOnce({ result: { status: 'ok', revision: table.stats!.revision }, replayed: true })
  await open()
  await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Include id"]')!.click())
  await click('Append row'); await click('Retry same command')
  expect(button('Append row').matches(':disabled')).toBe(true)
  await click('Retry same command')
  expect(api.mutateProjectLiveTable.mock.calls.map(call => call[2])).toEqual(Array(3).fill(api.mutateProjectLiveTable.mock.calls[0][2]))
})

it('retains reconciliation through a metadata authorization outage and requires current write permission', async () => {
  vi.useFakeTimers()
  try {
    api.mutateProjectLiveTable.mockRejectedValueOnce(new TypeError('Disconnected')).mockResolvedValueOnce({ result: { status: 'ok', revision: table.stats!.revision }, replayed: true })
    await open()
    await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Include id"]')!.click())
    await click('Append row')
    const command = api.mutateProjectLiveTable.mock.calls[0][2]
    api.getProjectLiveTable.mockRejectedValueOnce(new ApiError('Access unavailable', 403)).mockResolvedValueOnce({ ...table, permissions: { ...table.permissions, can_mutate: false } })
    await click('Refresh table')
    expect(button('Retry same command')).toBeUndefined()
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(button('Retry same command').disabled).toBe(true)
    await click('Refresh table')
    expect(button('Append row').matches(':disabled')).toBe(true)
    await click('Retry same command')
    expect(api.mutateProjectLiveTable.mock.calls[1][2]).toEqual(command)
  } finally { vi.useRealTimers() }
})

it('requires an explicit current preview and acknowledgment before clearing an expired unknown command', async () => {
  api.mutateProjectLiveTable.mockRejectedValueOnce(new TypeError('Disconnected')).mockRejectedValueOnce(new ApiError('Retry window expired', 410))
  await open()
  await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Include id"]')!.click())
  await click('Append row'); await click('Retry same command')
  expect(button('Retry same command').disabled).toBe(true)
  await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Confirm reconciliation review"]')!.click())
  expect(button('Clear pending command after review').disabled).toBe(true)
  await click('Preview up to 100 rows')
  expect(button('Clear pending command after review').disabled).toBe(true)
  await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Confirm reconciliation review"]')!.click())
  expect(button('Clear pending command after review').disabled).toBe(false)
  await click('Clear pending command after review')
  expect(api.mutateProjectLiveTable).toHaveBeenCalledTimes(2)
  expect(container.textContent).toContain('No new command was sent')
  expect(button('Retry same command')).toBeUndefined()
})

it.each(['preview', 'hide', 'revision', 'instance', 'outage'])('requires a fresh expiry acknowledgment after %s changes the reviewed preview', async change => {
  vi.useFakeTimers()
  try {
    api.mutateProjectLiveTable.mockRejectedValueOnce(new TypeError('Disconnected')).mockRejectedValueOnce(new ApiError('Retry window expired', 410))
    await open()
    await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Include id"]')!.click())
    await click('Append row'); await click('Retry same command'); await click('Preview up to 100 rows')
    await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Confirm reconciliation review"]')!.click())
    expect(button('Clear pending command after review').disabled).toBe(false)
    if (change === 'preview') await click('Preview up to 100 rows')
    else if (change === 'hide') { await click('Hide row values'); await click('Preview up to 100 rows') }
    else {
      if (change === 'outage') api.getProjectLiveTable.mockRejectedValueOnce(new Error('Metadata unavailable'))
      else api.getProjectLiveTable.mockResolvedValue({ ...table, ...(change === 'instance' ? { instance_id: 'new-instance' } : { stats: { ...table.stats, revision: '9007199254740994' } }) })
      await click('Refresh table')
      if (change === 'outage') await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
      if (change === 'revision') api.getProjectLiveTableRows.mockResolvedValue({ ...preview, revision: '9007199254740994' })
      await click('Preview up to 100 rows')
    }
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Confirm reconciliation review"]')!.checked).toBe(false)
    expect(button('Clear pending command after review').disabled).toBe(true)
    expect(api.mutateProjectLiveTable).toHaveBeenCalledTimes(2)
  } finally { vi.useRealTimers() }
})

it('requires acknowledgment after a slow replacement preview finishes', async () => {
  api.mutateProjectLiveTable.mockRejectedValueOnce(new TypeError('Disconnected')).mockRejectedValueOnce(new ApiError('Retry window expired', 410))
  await open()
  await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Include id"]')!.click())
  await click('Append row'); await click('Retry same command'); await click('Preview up to 100 rows')
  await act(async () => container.querySelector<HTMLInputElement>('input[aria-label="Confirm reconciliation review"]')!.click())
  expect(button('Clear pending command after review').disabled).toBe(false)
  let resolve!: (value: typeof preview) => void
  api.getProjectLiveTableRows.mockImplementationOnce(() => new Promise(done => { resolve = done }))
  await click('Preview up to 100 rows')
  const acknowledgment = container.querySelector<HTMLInputElement>('input[aria-label="Confirm reconciliation review"]')!
  expect(acknowledgment.disabled).toBe(true)
  await act(async () => acknowledgment.click())
  await act(async () => resolve(preview))
  expect(acknowledgment.checked).toBe(false)
  expect(button('Clear pending command after review').disabled).toBe(true)
  await act(async () => acknowledgment.click())
  expect(button('Clear pending command after review').disabled).toBe(false)
})

it('clears a recovered polling error while retaining the operator draft and conflict', async () => {
  vi.useFakeTimers()
  try {
    api.mutateProjectLiveTable.mockResolvedValue({ result: { status: 'condition_failed', revision: table.stats!.revision }, replayed: false })
    await open(); await click('Preview up to 100 rows'); await click('Edit row')
    await click('Apply row change')
    api.getProjectLiveTable.mockRejectedValueOnce(new Error('Temporary metadata outage'))
    await click('Refresh table')
    expect(container.textContent).toContain('Temporary metadata outage')
    await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
    expect(container.textContent).not.toContain('Temporary metadata outage')
    expect(container.textContent).toContain('Your draft is preserved')
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Value for id"]')?.value).toBe('private-value')
  } finally { vi.useRealTimers() }
})

it('drops a slow raw preview when the authoritative permission changes', async () => {
  let resolve!: (value: typeof preview) => void
  api.getProjectLiveTableRows.mockImplementationOnce(() => new Promise(done => { resolve = done }))
  await open()
  await click('Preview up to 100 rows')
  api.getProjectLiveTable.mockResolvedValue({ ...table, permissions: { can_view: false, can_mutate: false, can_admin: false } })
  await click('Refresh table')
  await act(async () => resolve(preview))
  expect(container.textContent).not.toContain('private-value')
  expect(button('Preview up to 100 rows').disabled).toBe(true)
  expect(button('Append row')).toBeUndefined()
})

it('uses positions instead of native identities when editing shared columns', async () => {
  api.getProjectLiveTable.mockResolvedValue({ ...table, profile: 'vts_columns' })
  api.mutateProjectLiveTable.mockResolvedValue({ result: { status: 'ok', revision: '9007199254740994' } })
  await open(); await click('Preview up to 100 rows'); await click('Edit row'); await click('Apply row change')
  const command = api.mutateProjectLiveTable.mock.calls[0][2]
  expect(command.position).toBe(1)
  expect(command).not.toHaveProperty('record_id')
  expect(command).not.toHaveProperty('expected_version')
})

it.each([
  ['read access is revoked', { ...table, permissions: { can_view: false, can_mutate: false, can_admin: false } }],
  ['instance changes', { ...table, instance_id: 'replacement' }],
  ['revision advances', { ...table, stats: { ...table.stats, revision: '9007199254740994' } }],
] as const)('does not download a delayed export after %s', async (_reason, current) => {
  let resolve!: (value: typeof preview) => void
  api.exportProjectLiveTable.mockImplementationOnce(() => new Promise(done => { resolve = done }))
  const createURL = vi.fn()
  vi.stubGlobal('URL', class extends URL { static createObjectURL = createURL })
  try {
    await open(); await click('Export table')
    api.getProjectLiveTable.mockResolvedValue(current)
    await click('Refresh table')
    await act(async () => resolve(preview))
    expect(createURL).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Table access or revision changed while exporting')
  } finally { vi.unstubAllGlobals() }
})

it('does not silently rebase a positional edit when background metadata advances', async () => {
  api.getProjectLiveTable.mockResolvedValue({ ...table, profile: 'vts_columns' })
  api.mutateProjectLiveTable.mockResolvedValue({ result: { status: 'condition_failed', revision: '9007199254740994' } })
  await open(); await click('Preview up to 100 rows'); await click('Edit row')
  api.getProjectLiveTable.mockResolvedValue({ ...table, profile: 'vts_columns', stats: { ...table.stats, revision: '9007199254740994' } })
  await click('Refresh table'); await click('Apply row change')
  expect(api.mutateProjectLiveTable.mock.calls[0][2].expected_revision).toBe('9007199254740993')
  api.getProjectLiveTableRows.mockResolvedValue({ ...preview, revision: '9007199254740994' })
  await click('Preview up to 100 rows'); await click('Use reviewed row version and keep my draft'); await click('Apply row change')
  expect(api.mutateProjectLiveTable.mock.calls[1][2].expected_revision).toBe('9007199254740994')
})


it('follows the returned row count when capacity limits shrink a preview page', async () => {
  api.getProjectLiveTableRows.mockImplementation((_project, _table, _instance, _revision, offset) => Promise.resolve({ ...preview, total: 5, rows: Array.from({ length: Math.min(2, 5 - offset) }, (_, index) => ({ ...preview.rows[0], id: `row-${offset + index}`, values: { id: `value-${offset + index}` } })) }))
  await open(); await click('Preview up to 100 rows')
  expect(container.textContent).toContain('1–2 of 5 rows')
  await click('Next rows')
  expect(api.getProjectLiveTableRows.mock.lastCall?.[4]).toBe(2)
  expect(container.textContent).toContain('3–4 of 5 rows')
  await click('Next rows')
  expect(api.getProjectLiveTableRows.mock.lastCall?.[4]).toBe(4)
  expect(container.textContent).toContain('5–5 of 5 rows')
  expect(button('Next rows').disabled).toBe(true)
  await click('Previous rows')
  expect(api.getProjectLiveTableRows.mock.lastCall?.[4]).toBe(2)
  await click('Previous rows')
  expect(api.getProjectLiveTableRows.mock.lastCall?.[4]).toBe(0)
  expect(button('Previous rows').disabled).toBe(true)
})


it('keeps a late preview hidden after Hide row values is selected', async () => {
  let resolve!: (value: typeof preview) => void
  api.getProjectLiveTableRows.mockImplementationOnce(() => new Promise(done => { resolve = done }))
  await open(); await click('Preview up to 100 rows'); await click('Hide row values')
  await act(async () => resolve(preview))
  expect(container.textContent).not.toContain('private-value')
  expect(container.querySelector('table')).toBeNull()
})

it('requires resume before new edits to a quiesced table', async () => {
  api.getProjectLiveTable.mockResolvedValue({ ...table, state: 'quiesced' })
  await open(); await click('Preview up to 100 rows')
  expect(button('Edit row').disabled).toBe(true)
  expect(button('Append row').matches(':disabled')).toBe(true)
  expect(container.textContent).toContain('Resume it before applying new row changes')
})


it('opens a scoped project table reference and follows another reference on the same page', async () => {
  const previousHash = window.location.hash
  try {
    window.location.hash = '#/repository?setting=live-tables&projectId=project&resourceId=table'
    await act(async () => root.render(<ProjectLiveTablesPage projectId="project" />))
    expect(api.getProjectLiveTable.mock.lastCall?.slice(0, 2)).toEqual(['project', 'table'])
    await act(async () => { window.location.hash = '#/repository?setting=live-tables&projectId=project&resourceId=another'; window.dispatchEvent(new HashChangeEvent('hashchange')) })
    expect(api.getProjectLiveTable.mock.lastCall?.slice(0, 2)).toEqual(['project', 'another'])
    await act(async () => { window.location.hash = '#/repository?setting=live-tables&projectId=foreign&resourceId=wrong-project'; window.dispatchEvent(new HashChangeEvent('hashchange')) })
    expect(api.getProjectLiveTable.mock.lastCall?.slice(0, 2)).toEqual(['project', 'another'])
  } finally { window.location.hash = previousHash }
})
