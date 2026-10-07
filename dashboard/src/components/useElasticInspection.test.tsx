// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { useElasticInspection } from './useElasticInspection'
import type { ElasticInspection } from '../types/elastic'

const api = vi.hoisted(() => ({ inspectELKConnection: vi.fn(), getELKInspection: vi.fn(), cancelELKInspection: vi.fn() }))
vi.mock('../hooks/api/useELKConnectionsApi', () => ({ useELKConnectionsApi: () => api }))
let root: Root
let container: HTMLElement
let inspection: ReturnType<typeof useElasticInspection>
const operation = (id: string, status: string): ElasticInspection => ({ id, connection_id: 'connection', connection_revision: 1, kind: 'preview', status, created_at: '', expires_at: '' })
function Harness({ projectId = 'project' }: { projectId?: string }) { inspection = useElasticInspection(projectId); return <span>{inspection.operation?.id}</span> }
beforeEach(async () => {
  vi.useFakeTimers(); vi.clearAllMocks(); api.cancelELKInspection.mockResolvedValue(undefined)
  container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container)
  await act(async () => root.render(<Harness />))
})
afterEach(() => { act(() => root.unmount()); container.remove(); vi.useRealTimers() })

it('keeps asynchronous inspections busy until the saved operation completes', async () => {
  api.inspectELKConnection.mockResolvedValue(operation('preview', 'pending'))
  api.getELKInspection.mockResolvedValue(operation('preview', 'complete'))
  await act(async () => inspection.start('connection', { kind: 'preview', expected_revision: 1 }))
  expect(inspection.busy).toBe(true)
  await act(async () => { await vi.advanceTimersByTimeAsync(1500) })
  expect(inspection.operation?.status).toBe('complete')
  expect(inspection.busy).toBe(false)
})

it('prevents an old poll from replacing a newly selected inspection', async () => {
  let finishOld!: (value: ElasticInspection) => void
  api.inspectELKConnection.mockResolvedValueOnce(operation('old', 'pending')).mockResolvedValueOnce(operation('new', 'complete'))
  api.getELKInspection.mockImplementation(() => new Promise(resolve => { finishOld = resolve }))
  await act(async () => inspection.start('connection', { kind: 'preview' }))
  await act(async () => { await vi.advanceTimersByTimeAsync(1500) })
  await act(async () => inspection.start('connection', { kind: 'discover_fields' }))
  await act(async () => finishOld(operation('old', 'complete')))
  expect(inspection.operation?.id).toBe('new')
})

it('does not replace a new inspection when cancellation of the old one finishes', async () => {
  let finishCancel!: () => void
  api.inspectELKConnection.mockResolvedValueOnce(operation('old', 'pending')).mockResolvedValueOnce(operation('new', 'complete'))
  api.cancelELKInspection.mockImplementation(() => new Promise<void>(resolve => { finishCancel = resolve }))
  await act(async () => inspection.start('connection', { kind: 'preview' }))
  await act(async () => { void inspection.cancel() })
  await act(async () => inspection.start('connection', { kind: 'discover_fields' }))
  await act(async () => finishCancel())
  expect(inspection.operation?.id).toBe('new')
  expect(inspection.operation?.status).toBe('complete')
})

it('does not publish a stale cancellation failure after a new inspection starts', async () => {
  let rejectCancel!: (error: Error) => void
  api.inspectELKConnection.mockResolvedValueOnce(operation('old', 'pending')).mockResolvedValueOnce(operation('new', 'complete'))
  api.cancelELKInspection.mockImplementationOnce(() => new Promise<void>((_, reject) => { rejectCancel = reject }))
  await act(async () => inspection.start('connection', { kind: 'preview' }))
  await act(async () => { void inspection.cancel() })
  await act(async () => inspection.start('connection', { kind: 'discover_fields' }))
  await act(async () => rejectCancel(new Error('old cancellation failed')))
  expect(inspection.error).toBe('')
  expect(inspection.operation?.id).toBe('new')
})

it('clears in-flight results and cancels a known pending operation when its authoring scope changes', async () => {
  let finish!: (value: ElasticInspection) => void
  api.inspectELKConnection.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  await act(async () => { void inspection.start('connection', { kind: 'preview' }) })
  expect(inspection.busy).toBe(true)
  await act(async () => inspection.clear())
  await act(async () => finish(operation('obsolete', 'complete')))
  expect(inspection.operation).toBeUndefined()
  expect(inspection.busy).toBe(false)
  api.inspectELKConnection.mockResolvedValueOnce(operation('pending', 'pending'))
  await act(async () => inspection.start('connection', { kind: 'preview' }))
  await act(async () => inspection.clear())
  expect(api.cancelELKInspection).toHaveBeenCalledWith('project', 'connection', 'pending')
  expect(inspection.operation).toBeUndefined()
})

it('cancels a pending operation in its original project when the project changes', async () => {
  api.inspectELKConnection.mockResolvedValue(operation('pending', 'pending'))
  await act(async () => inspection.start('connection', { kind: 'preview' }))
  await act(async () => root.render(<Harness projectId="next-project" />))
  expect(api.cancelELKInspection).toHaveBeenCalledWith('project', 'connection', 'pending')
  expect(api.cancelELKInspection).not.toHaveBeenCalledWith('next-project', 'connection', 'pending')
  expect(inspection.operation).toBeUndefined()
  expect(inspection.busy).toBe(false)
})

it('cancels owned pending work when the authoring view unmounts', async () => {
  api.inspectELKConnection.mockResolvedValue(operation('pending', 'running'))
  await act(async () => inspection.start('connection', { kind: 'preview' }))
  await act(async () => root.render(null))
  expect(api.cancelELKInspection).toHaveBeenCalledWith('project', 'connection', 'pending')
})

it('retains cancellation ownership after a status poll fails', async () => {
  api.inspectELKConnection.mockResolvedValue(operation('pending', 'pending'))
  api.getELKInspection.mockRejectedValue(new Error('status unavailable'))
  await act(async () => inspection.start('connection', { kind: 'preview' }))
  await act(async () => { await vi.advanceTimersByTimeAsync(1500) })
  expect(inspection.error).toContain('status unavailable')
  expect(inspection.operation).toBeUndefined()
  await act(async () => inspection.clear())
  expect(api.cancelELKInspection).toHaveBeenCalledWith('project', 'connection', 'pending')
})

it('cancels a stale pending creation result after its authoring scope changes', async () => {
  let finish!: (value: ElasticInspection) => void
  api.inspectELKConnection.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  await act(async () => { void inspection.start('connection', { kind: 'preview' }) })
  await act(async () => root.render(<Harness projectId="next-project" />))
  await act(async () => finish(operation('obsolete', 'pending')))
  expect(api.cancelELKInspection).toHaveBeenCalledWith('project', 'connection', 'obsolete')
  expect(inspection.operation).toBeUndefined()
  expect(inspection.busy).toBe(false)
})
