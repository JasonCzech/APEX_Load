// @vitest-environment jsdom

import { act, StrictMode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { useAsyncTask } from './useAsyncTask'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type Deferred<T> = {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (error: Error) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

let taskApi: ReturnType<typeof useAsyncTask<string>> | null = null

function Probe() {
  taskApi = useAsyncTask<string>()
  return <span>{taskApi.pending ? 'pending' : 'idle'}:{taskApi.value || 'empty'}:{taskApi.error || 'ok'}</span>
}

describe('useAsyncTask', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    taskApi = null
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('keeps the newest result when an older task completes later', async () => {
    const older = deferred<string>()
    const newer = deferred<string>()

    await act(async () => root.render(<Probe />))
    let olderRun!: Promise<string>
    let newerRun!: Promise<string>
    await act(async () => {
      olderRun = taskApi!.run(() => older.promise)
      newerRun = taskApi!.run(() => newer.promise)
    })

    await act(async () => newer.resolve('newer'))
    await expect(newerRun).resolves.toBe('newer')
    expect(container.textContent).toBe('idle:newer:ok')

    await act(async () => older.resolve('older'))
    await expect(olderRun).resolves.toBe('older')
    expect(container.textContent).toBe('idle:newer:ok')
  })

  it('invalidates in-flight state updates when reset is called', async () => {
    const request = deferred<string>()
    await act(async () => root.render(<Probe />))

    let run!: Promise<string>
    await act(async () => {
      run = taskApi!.run(() => request.promise)
      taskApi!.reset('reset-value')
    })
    expect(container.textContent).toBe('idle:reset-value:ok')

    await act(async () => request.resolve('stale-value'))
    await expect(run).resolves.toBe('stale-value')
    expect(container.textContent).toBe('idle:reset-value:ok')
  })

  it('continues to accept results after Strict Mode replays effects', async () => {
    await act(async () => root.render(<StrictMode><Probe /></StrictMode>))

    await act(async () => {
      await taskApi!.run(async () => 'strict-result')
    })
    expect(container.textContent).toBe('idle:strict-result:ok')
  })
})
