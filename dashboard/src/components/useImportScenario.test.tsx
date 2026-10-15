// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { StoredScript } from '../types'
import { addRecent } from '../desktop/desktopRecents'
import { useImportScenario } from './useImportScenario'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = { createScenarioDraft: vi.fn() }
vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))
vi.mock('../desktop/desktopRecents', () => ({ addRecent: vi.fn().mockResolvedValue(undefined) }))

const script = (id: string) => ({ id, name: `Request ${id}`, protocol: 'http', version: '1.0', created_at: '2026-10-14T00:00:00Z' }) as StoredScript

let scenario: ReturnType<typeof useImportScenario>
function Harness({ projectId }: { projectId?: string }) {
  scenario = useImportScenario(projectId)
  return null
}

let container: HTMLDivElement
let root: Root
beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  apiMock.createScenarioDraft.mockReset()
  window.location.hash = '#/scripts'
})
afterEach(async () => {
  await act(async () => root.unmount())
  container.remove()
})

describe('useImportScenario', () => {
  it('opens the draft it already made when leaving the import was cancelled', async () => {
    apiMock.createScenarioDraft.mockResolvedValueOnce({ id: 'draft-1' })
    await act(async () => root.render(<Harness projectId="project-1" />))
    const saved = [script('a'), script('b')]
    await act(async () => scenario.create('Orders', saved))
    expect(window.location.hash).toContain('draftId=draft-1')
    // The desktop app lists the draft under Recent.
    expect(addRecent).toHaveBeenCalledWith(expect.objectContaining({ kind: 'scenario', id: 'draft-1', name: 'Orders' }))
    // An unsaved-work guard kept the user on the import.
    window.location.hash = '#/scripts'
    await act(async () => scenario.create('Orders', saved))
    expect(apiMock.createScenarioDraft).toHaveBeenCalledTimes(1)
    expect(window.location.hash).toContain('draftId=draft-1')
    // A different set of saved scripts gets its own draft.
    apiMock.createScenarioDraft.mockResolvedValueOnce({ id: 'draft-2' })
    await act(async () => scenario.create('Orders', [...saved, script('c')]))
    expect(apiMock.createScenarioDraft).toHaveBeenCalledTimes(2)
    expect(window.location.hash).toContain('draftId=draft-2')
  })

  it('keeps a failure with the saved scripts it was for', async () => {
    apiMock.createScenarioDraft.mockRejectedValueOnce(new Error('network down'))
    await act(async () => root.render(<Harness projectId="project-1" />))
    const first = [script('a')]
    await act(async () => scenario.create('Orders', first))
    expect(scenario.errorFor(first)).toContain('network down')
    // A later save confirms other scripts without the earlier failure.
    expect(scenario.errorFor([script('a'), script('b')])).toBe('')
    await act(async () => scenario.reset())
    expect(scenario.errorFor(first)).toBe('')
  })

  it('is not offered without a project', async () => {
    await act(async () => root.render(<Harness />))
    expect(scenario.available).toBe(false)
    await act(async () => scenario.create('Orders', [script('a')]))
    expect(apiMock.createScenarioDraft).not.toHaveBeenCalled()
  })
})
