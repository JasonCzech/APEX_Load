// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { type StoredScript } from '../types'
import MoveScriptsDialog from './MoveScriptsDialog'

const api = { listFolders: vi.fn(), moveScript: vi.fn() }
vi.mock('../hooks/useApi', () => ({ useApi: () => api }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const script = (id: string, folder_id: string | null = null) => ({ id, name: id, folder_id, project_id: 'p' } as StoredScript)
const folders = [
  { id: 'a', name: 'APIs', category: 'scripts', project_id: 'p' },
  { id: 'b', name: 'Checkout', parent_id: 'a', category: 'scripts', project_id: 'p' },
  { id: 'foreign', name: 'Foreign', category: 'scripts', project_id: 'other' },
  { id: 'results', name: 'Results', category: 'results', project_id: 'p' },
]
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => { resolve = done })
  return { promise, resolve }
}

describe('MoveScriptsDialog', () => {
  let root: Root
  let container: HTMLDivElement
  const onClose = vi.fn()
  const onResult = vi.fn()
  const onBusy = vi.fn()
  const onFoldersLoaded = vi.fn()
  beforeEach(() => {
    vi.resetAllMocks()
    api.listFolders.mockResolvedValue({ folders })
    api.moveScript.mockResolvedValue({})
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(() => { act(() => root.unmount()); container.remove() })
  const render = async (scripts = [script('one')], projectId = 'p') => {
    await act(async () => root.render(<MoveScriptsDialog key={projectId} projectId={projectId} scripts={scripts} onClose={onClose} onResult={onResult} onBusy={onBusy} onFoldersLoaded={onFoldersLoaded} />))
  }
  const button = (name: string) => {
    const result = [...document.querySelectorAll('button')].find(node => node.textContent === name)
    expect(result, `button ${name}`).toBeDefined()
    return result as HTMLButtonElement
  }
  const choose = async (value: string) => {
    await act(async () => {
      const select = document.querySelector('select')!
      select.value = value
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
  }
  const click = async (name: string) => { await act(async () => button(name).click()) }

  it('loads same-project script paths, skips a no-op, and moves a single script', async () => {
    await render()
    expect(api.listFolders).toHaveBeenCalledWith('p', 'scripts')
    expect(document.body.textContent).toContain('APIs / Checkout')
    expect(document.body.textContent).not.toContain('Foreign')
    expect(document.body.textContent).not.toContain('Results')
    expect(button('Move').disabled).toBe(true)
    await choose('b')
    await click('Move')
    expect(api.moveScript).toHaveBeenCalledExactlyOnceWith('one', 'b')
    expect(onResult).toHaveBeenCalledWith(['one'], [], 'b', 'Moved 1; unchanged 0; failed 0.')
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('preselects a shared folder and sends null for Unfiled', async () => {
    await render([script('one', 'a'), script('two', 'a')])
    expect(document.querySelector('select')?.value).toBe('a')
    expect(button('Move').disabled).toBe(true)
    await choose('')
    await click('Move')
    expect(api.moveScript.mock.calls).toEqual([['one', null], ['two', null]])
  })

  it('requires a destination for mixed folders and skips already assigned scripts', async () => {
    await render([script('one', 'a'), script('two', 'b')])
    expect(button('Move').disabled).toBe(true)
    await choose('a')
    await click('Move')
    expect(api.moveScript).toHaveBeenCalledExactlyOnceWith('two', 'a')
    expect(onResult).toHaveBeenCalledWith(['two'], ['one'], 'a', 'Moved 1; unchanged 1; failed 0.')
  })

  it('keeps individual errors and retries only failed moves', async () => {
    api.moveScript.mockImplementation(async (id: string) => { if (id === 'two') throw new Error('Locked by another change') })
    await render([script('one'), script('two')])
    await choose('b')
    await click('Move')
    expect(document.body.textContent).toContain('two: Locked by another change')
    expect(onClose).not.toHaveBeenCalled()
    expect(onResult).toHaveBeenCalledWith(['one'], [], 'b', 'Moved 1; unchanged 0; failed 1.')
    api.moveScript.mockResolvedValue({})
    await click('Retry failed moves')
    expect(api.moveScript.mock.calls).toEqual([['one', 'b'], ['two', 'b'], ['two', 'b']])
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('blocks submission during folder loading and permits retry after failure', async () => {
    const pending = deferred<{ folders: typeof folders }>()
    api.listFolders.mockReturnValueOnce(pending.promise)
    await render([script('one', 'a')])
    expect(button('Move').disabled).toBe(true)
    expect(document.body.textContent).toContain('Loading script folders')
    await act(async () => pending.resolve({ folders: [] }))
    expect(document.body.textContent).toContain('selected folder is unavailable')
    expect(button('Move').disabled).toBe(true)
    await choose('')
    expect(button('Move').disabled).toBe(false)
  })

  it('shows folder errors without allowing an accidental move to root', async () => {
    api.listFolders.mockRejectedValueOnce(new Error('Folders unavailable'))
    await render([script('one', 'a')])
    expect(document.body.textContent).toContain('Folders unavailable')
    expect(button('Move').disabled).toBe(true)
    await click('Retry folders')
    await choose('b')
    await click('Move')
    expect(api.moveScript).toHaveBeenCalledExactlyOnceWith('one', 'b')
  })

  it('cancels without mutation and fences duplicate clicks and Escape while moving', async () => {
    await render()
    await click('Cancel')
    expect(api.moveScript).not.toHaveBeenCalled()
    onClose.mockClear()
    const pending = deferred<object>()
    api.moveScript.mockReturnValue(pending.promise)
    await choose('a')
    await act(async () => { button('Move').click(); button('Move').click() })
    expect(button('Cancel').disabled).toBe(true)
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(onClose).not.toHaveBeenCalled()
    expect(api.moveScript).toHaveBeenCalledTimes(1)
    await act(async () => pending.resolve({}))
    expect(onClose).toHaveBeenCalledOnce()
  })

  it('ignores late results and stops queued moves when project scope changes', async () => {
    const pending = deferred<object>()
    api.moveScript.mockReturnValue(pending.promise)
    await render(Array.from({ length: 9 }, (_, index) => script(`script-${index}`)))
    await choose('a')
    await click('Move')
    expect(api.moveScript).toHaveBeenCalledTimes(6)
    await render([script('new')], 'new-project')
    await act(async () => pending.resolve({}))
    expect(api.moveScript).toHaveBeenCalledTimes(6)
    expect(onResult).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  })
})
