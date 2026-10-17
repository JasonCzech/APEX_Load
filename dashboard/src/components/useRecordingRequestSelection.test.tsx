// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useRecordingRequestSelection } from './useRecordingRequestSelection'
import type { RecorderRequestRecord as RecordedRequest, RecorderSession as RecordingSession } from '../hooks/api/useRecorderApi'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function request(name: string, seq = 1): RecordedRequest {
  return { seq, timestamp: '', method: 'GET', url: `https://example.test/${name}`, path: `/${name}`, host: 'example.test', status_code: 200, response_time_ms: 1, content_type: '', body: '', think_time_ms: 0, is_static: false, step_name: name }
}
function session(name: string, requests?: RecordedRequest[]): RecordingSession {
  return { id: name, name, status: 'stopped', started_at: '', filters: { exclude_static: true }, requests }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

describe('recording request ownership', () => {
  let root: Root
  let container: HTMLDivElement
  let selection: ReturnType<typeof useRecordingRequestSelection>
  function Harness({ scope = 'project-a:recorder-a' }: { scope?: string }) {
    selection = useRecordingRequestSelection(scope)
    return null
  }
  beforeEach(async () => {
    container = document.createElement('div')
    root = createRoot(container)
    await act(async () => root.render(<Harness />))
  })
  afterEach(async () => { await act(async () => root.unmount()) })
  const begin = (id: string, seq: number) => {
    let update!: NonNullable<ReturnType<typeof selection.beginRequestUpdate>>
    act(() => { update = selection.beginRequestUpdate(id, seq)! })
    return update
  }
  const select = async (name: string, seq = 1) => {
    const item = request(name, seq)
    await act(async () => { await selection.selectSession(session(name, [item]), async () => { throw new Error('cached') }); selection.selectRequest(item) })
  }

  it.each(['save', 'exclude'] as const)('keeps a delayed %s in its original session even when both requests have sequence 1', async kind => {
    await select('A')
    const update = begin('A', 1)
    await select('B')
    await act(async () => selection.acceptRequestUpdate(update, { ...request('A saved'), excluded: true }, kind === 'exclude'))
    expect(selection.selectedSession?.id).toBe('B')
    expect(selection.selectedRequest?.step_name).toBe('B')
    expect(selection.requestDraft).toMatchObject({ step_name: 'B', excluded: false })
  })

  it('keeps a newer request selection while updating the original cached row', async () => {
    const a = request('A')
    const b = request('B', 2)
    await act(async () => { await selection.selectSession(session('A', [a, b]), async () => session('A')); selection.selectRequest(a) })
    const update = begin('A', 1)
    await act(async () => selection.selectRequest(b))
    await act(async () => selection.acceptRequestUpdate(update, request('A saved')))
    expect(selection.requestDraft.step_name).toBe('B')
    expect(selection.selectedSession?.requests?.[0].step_name).toBe('A saved')
  })

  it('preserves typing after Save starts and accepts the next save as the baseline', async () => {
    await select('A')
    const first = begin('A', 1)
    await act(async () => selection.setRequestDraft(draft => ({ ...draft, step_name: 'newer draft', note: 'keep this' })))
    await act(async () => selection.acceptRequestUpdate(first, request('first response')))
    expect(selection.selectedSession?.requests?.[0].step_name).toBe('first response')
    expect(selection.requestDraft).toMatchObject({ step_name: 'newer draft', note: 'keep this' })
    await act(async () => selection.finishRequestUpdate(first))
    const second = begin('A', 1)
    await act(async () => selection.acceptRequestUpdate(second, { ...request('newer draft'), note: 'keep this' }))
    expect(selection.requestDraft.step_name).toBe('newer draft')
  })

  it('Exclude changes only its field and preserves unsaved names and notes', async () => {
    await select('A')
    await act(async () => selection.setRequestDraft(draft => ({ ...draft, step_name: 'unsaved', note: 'before' })))
    const update = begin('A', 1)
    await act(async () => selection.setRequestDraft(draft => ({ ...draft, note: 'after' })))
    await act(async () => selection.acceptRequestUpdate(update, { ...request('A'), excluded: true }, true))
    expect(selection.requestDraft).toMatchObject({ step_name: 'unsaved', note: 'after', excluded: true })
  })

  it('preserves a newer inclusion choice even when it returns to the original value', async () => {
    await select('A')
    const update = begin('A', 1)
    await act(async () => selection.setRequestDraft(draft => ({ ...draft, excluded: true })))
    await act(async () => selection.setRequestDraft(draft => ({ ...draft, excluded: false })))
    await act(async () => selection.acceptRequestUpdate(update, { ...request('A'), excluded: true }, true))
    expect(selection.selectedSession?.requests?.[0].excluded).toBe(true)
    expect(selection.requestDraft.excluded).toBe(false)
  })

  it('serializes mutations while keeping the newer draft editable', async () => {
    await select('A')
    let older!: ReturnType<typeof begin>
    await act(async () => { older = begin('A', 1) })
    await act(async () => selection.setRequestDraft(draft => ({ ...draft, step_name: 'newer' })))
    expect(selection.beginRequestUpdate('A', 1)).toBeNull()
    expect(selection.isRequestUpdating('A', 1)).toBe(true)
    await act(async () => {
      selection.acceptRequestUpdate(older, request('old'))
      selection.finishRequestUpdate(older)
    })
    expect(selection.requestDraft.step_name).toBe('newer')
    expect(selection.isRequestUpdating('A', 1)).toBe(false)
    let newer!: ReturnType<typeof begin>
    await act(async () => { newer = begin('A', 1) })
    await act(async () => {
      selection.acceptRequestUpdate(newer, request('newer'))
      selection.finishRequestUpdate(newer)
    })
    let accepted = true
    await act(async () => { accepted = selection.acceptRequestUpdate(older, request('late old')) })
    expect(accepted).toBe(false)
    expect(selection.selectedRequest?.step_name).toBe('newer')
    expect(selection.selectedSession?.requests?.[0].step_name).toBe('newer')
    await act(async () => selection.selectRequest(selection.selectedSession!.requests![0]))
    expect(selection.requestDraft.step_name).toBe('newer')
  })

  it('keeps an in-flight request serialized across a project round trip', async () => {
    await select('A')
    const old = begin('A', 1)
    await act(async () => root.render(<Harness scope="project-b:recorder-a" />))
    await act(async () => root.render(<Harness />))
    await select('A')
    expect(selection.beginRequestUpdate('A', 1)).toBeNull()
    await act(async () => {
      expect(selection.acceptRequestUpdate(old, request('old response'))).toBe(false)
      selection.finishRequestUpdate(old)
    })
    expect(selection.isRequestUpdating('A', 1)).toBe(false)
    expect(selection.requestDraft.step_name).toBe('A')
  })

  it('does not clear a newer selection when an earlier session deletion completes', async () => {
    await select('A')
    await select('B')
    await act(async () => selection.clearSelection('A'))
    expect(selection.selectedSession?.id).toBe('B')
    await act(async () => selection.clearSelection('B'))
    expect(selection.selectedSession).toBeNull()
  })

  it.each(['resolve', 'reject'] as const)('ignores an older detail %s after a newer cached selection', async outcome => {
    const pending = deferred<RecordingSession>()
    await act(async () => { void selection.selectSession(session('A'), () => pending.promise) })
    await select('B')
    await act(async () => outcome === 'resolve' ? pending.resolve(session('A', [request('A')])) : pending.reject(new Error('offline')))
    expect(selection.selectedSession?.id).toBe('B')
    expect(selection.requestDraft.step_name).toBe('B')
  })

  it('accepts the latest fetched selection when both fetches finish out of order', async () => {
    const a = deferred<RecordingSession>()
    const b = deferred<RecordingSession>()
    await act(async () => { void selection.selectSession(session('A'), () => a.promise) })
    await act(async () => { void selection.selectSession(session('B'), () => b.promise) })
    await act(async () => b.resolve(session('B', [request('B')])) )
    await act(async () => a.resolve(session('A', [request('A')])) )
    expect(selection.selectedSession?.requests?.[0].step_name).toBe('B')
  })

  it.each(['deselect', 'project', 'endpoint'] as const)('invalidates pending details and saves on %s', async change => {
    await select('saved')
    const update = begin('saved', 1)
    const pending = deferred<RecordingSession>()
    await act(async () => { void selection.selectSession(session('A'), () => pending.promise) })
    await act(async () => {
      if (change === 'deselect') await selection.selectSession(session('A'), () => pending.promise)
      else root.render(<Harness scope={change === 'project' ? 'project-b:recorder-a' : 'project-a:recorder-b'} />)
    })
    await act(async () => {
      pending.resolve(session('A', [request('A')]))
      selection.acceptRequestUpdate(update, request('late save'))
    })
    expect(selection.selectedSession).toBeNull()
    expect(selection.selectedRequest).toBeNull()
  })
})
