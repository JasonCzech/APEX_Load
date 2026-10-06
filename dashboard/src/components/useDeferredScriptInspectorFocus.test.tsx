// @vitest-environment jsdom
import { act, useRef } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useDeferredScriptInspectorFocus, type ScriptInspectorFocusOwner } from './useDeferredScriptInspectorFocus'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let requestFocus: ReturnType<typeof useDeferredScriptInspectorFocus>['requestFocus']
function Harness({ owner }: { owner: ScriptInspectorFocusOwner }) {
  const rootRef = useRef<HTMLDivElement>(null)
  const controller = useDeferredScriptInspectorFocus(rootRef)
  controller.updateOwner(owner)
  requestFocus = controller.requestFocus
  return <div ref={rootRef}><button data-origin>Outline</button><section className="author-main-action"><input data-destination /><input data-other /></section></div>
}

describe('deferred script inspector focus ownership', () => {
  let container: HTMLDivElement
  let root: Root
  let owner: ScriptInspectorFocusOwner
  let frames: Map<number, FrameRequestCallback>
  let requestFrame: ReturnType<typeof vi.spyOn>
  let cancelFrame: ReturnType<typeof vi.spyOn>
  beforeEach(async () => {
    container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container)
    owner = { scriptId: 'one', projectId: 'project', folderId: 'folder', initialScript: {}, path: 'script.actions[0]', action: {} }
    frames = new Map(); let id = 0
    requestFrame = vi.spyOn(globalThis, 'requestAnimationFrame').mockImplementation(callback => { const next = ++id; frames.set(next, callback); return next })
    cancelFrame = vi.spyOn(globalThis, 'cancelAnimationFrame').mockImplementation(frame => { frames.delete(frame) })
    await act(async () => { root.render(<Harness owner={owner} />) })
  })
  afterEach(async () => { await act(async () => { root.unmount() }); container.remove(); requestFrame.mockRestore(); cancelFrame.mockRestore() })
  const destination = () => container.querySelector<HTMLInputElement>('[data-destination]')!
  const origin = () => container.querySelector<HTMLButtonElement>('[data-origin]')!
  const schedule = () => requestFocus(owner, panel => panel.querySelector('[data-destination]'))
  const flush = async () => { await act(async () => { const callbacks = [...frames.values()]; frames.clear(); callbacks.forEach(callback => callback(1)) }) }

  it('focuses the requested field when navigation still owns focus', async () => {
    origin().focus(); schedule(); await flush(); expect(document.activeElement).toBe(destination())
  })
  it('preserves a later operator focus even after returning to the origin', async () => {
    origin().focus(); schedule(); destination().focus(); origin().focus(); await flush(); expect(document.activeElement).toBe(origin())
  })
  it('allows a removed issue origin to leave body before its destination receives focus', async () => {
    origin().focus(); schedule(); origin().remove(); expect(document.activeElement).toBe(document.body)
    await flush(); expect(document.activeElement).toBe(destination())
  })
  it.each(['scriptId', 'projectId', 'folderId', 'initialScript', 'path', 'action'] as const)('refuses a stale %s owner', async field => {
    origin().focus(); schedule()
    const changed = { ...owner, [field]: field === 'initialScript' || field === 'action' ? {} : 'different' }
    await act(async () => { root.render(<Harness owner={changed} />) })
    await flush(); expect(document.activeElement).toBe(origin())
  })
  it('rejects superseded callbacks even when a cancelled callback is already retained', async () => {
    origin().focus(); schedule(); const stale = [...frames.values()][0]
    requestFocus(owner, panel => panel.querySelector('[data-other]'))
    await act(async () => { stale(1) })
    expect(document.activeElement).toBe(origin())
    await flush(); expect(document.activeElement).toBe(container.querySelector('[data-other]'))
  })
  it('cannot focus after its editor is unmounted', async () => {
    origin().focus(); schedule(); const stale = [...frames.values()][0]
    await act(async () => { root.unmount() }); root = createRoot(container)
    await act(async () => { stale(1) }); expect(document.activeElement).toBe(document.body)
  })
})
