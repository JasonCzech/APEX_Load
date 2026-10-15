// @vitest-environment jsdom

import { act, StrictMode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { ScriptDebugBreakpoint } from '../types/replay'
import { useScriptReplayDebuggerController } from './useScriptReplayDebuggerController'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type Controller = ReturnType<typeof useScriptReplayDebuggerController>

const key = (scriptId: string) => `apexload.debug.breakpoints.${scriptId}`
const stored = (scriptId: string) => window.localStorage.getItem(key(scriptId))
const login: ScriptDebugBreakpoint = { phase: 'action', action_index: 0 }
const logout: ScriptDebugBreakpoint = { phase: 'teardown', action_index: 1, request_index: 0 }

describe('debugger breakpoints saved per script', () => {
  let container: HTMLDivElement
  let root: Root
  let controller: Controller

  function Harness({ scriptId }: { scriptId: string | null }) {
    controller = useScriptReplayDebuggerController({ browserReplaySession: null, debugActionRows: [], expandedReplayEvent: null, replayReport: null, replaying: false, scriptId })
    return null
  }
  // The dev server mounts the editor under StrictMode, which runs its effects twice.
  const render = (scriptId: string | null) => act(() => root.render(<StrictMode><Harness scriptId={scriptId} /></StrictMode>))

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    root = createRoot(container)
  })
  afterEach(() => {
    act(() => root.unmount())
    window.localStorage.clear()
  })

  it("keeps a script's breakpoints across a reload", () => {
    window.localStorage.setItem(key('script-a'), JSON.stringify([login]))
    render('script-a')
    expect(controller.debugBreakpoints).toEqual([login])
    expect(JSON.parse(stored('script-a')!)).toEqual([login])
  })

  it('gives each script its own breakpoints when the editor switches between them', () => {
    window.localStorage.setItem(key('script-a'), JSON.stringify([login]))
    render('script-a')

    // B has none saved: A's do not follow it there.
    render('script-b')
    expect(controller.debugBreakpoints).toEqual([])
    expect(JSON.parse(stored('script-b')!)).toEqual([])
    expect(JSON.parse(stored('script-a')!)).toEqual([login])

    act(() => controller.setDebugBreakpoints([logout]))
    render('script-a')
    expect(controller.debugBreakpoints).toEqual([login])
    expect(JSON.parse(stored('script-b')!)).toEqual([logout])
  })

  it("keeps a new draft's breakpoints when it is first saved, and none for the next new draft", () => {
    render(null)
    act(() => controller.setDebugBreakpoints([login, { ...logout, once: true }]))
    render('script-new')
    expect(controller.debugBreakpoints).toEqual([login, { ...logout, once: true }])
    // A run-to-here breakpoint is not kept for later.
    expect(JSON.parse(stored('script-new')!)).toEqual([login])

    render(null)
    expect(controller.debugBreakpoints).toEqual([])
  })
})
