// Native file-drop routing for the desktop shell.
//
// Wails exposes a SINGLE global window.runtime.OnFileDrop callback, so we
// register it once and route the dropped absolute paths to whichever registered
// drop zone contains the drop point. Drop zones opt in with the
// `--wails-drop-target: drop` CSS marker (set by consumers) so Wails recognizes
// them. In the browser this hook is inert.

import { useEffect } from 'react'

import { getWailsEventRuntime } from './wailsRuntime'

type DropHandler = (paths: string[]) => void

interface DropZone {
  element: HTMLElement
  handler: DropHandler
}

interface FileDropRuntime {
  OnFileDrop?: (callback: (x: number, y: number, paths: string[]) => void, useDropTarget: boolean) => void
  OnFileDropOff?: () => void
}

const zones = new Set<DropZone>()
let registered = false

function ensureGlobalListener() {
  if (registered) {
    return
  }
  const runtime = getWailsEventRuntime() as (FileDropRuntime | undefined)
  if (!runtime?.OnFileDrop) {
    return
  }
  registered = true
  runtime.OnFileDrop((x, y, paths) => {
    if (!paths || paths.length === 0) {
      return
    }
    const target = typeof document !== 'undefined' ? document.elementFromPoint(x, y) : null
    if (!target) {
      return
    }
    // Innermost-wins: walk up from the drop target and dispatch to the first
    // ancestor that is a registered zone, so nested/overlapping zones resolve to
    // the most specific one rather than insertion order.
    let node: Element | null = target
    while (node) {
      for (const zone of zones) {
        if (zone.element === node) {
          zone.handler(paths)
          return
        }
      }
      node = node.parentElement
    }
  }, true)
}

function teardownGlobalListener() {
  if (!registered || zones.size > 0) {
    return
  }
  const runtime = getWailsEventRuntime() as (FileDropRuntime | undefined)
  runtime?.OnFileDropOff?.()
  registered = false
}

// registerDropZone wires an element to receive native dropped paths. Returns an
// unregister function. Exported for tests.
export function registerDropZone(element: HTMLElement, handler: DropHandler): () => void {
  const zone: DropZone = { element, handler }
  zones.add(zone)
  ensureGlobalListener()
  return () => {
    zones.delete(zone)
    teardownGlobalListener()
  }
}

// useDesktopFileDrop registers a ref'd element as a native drop target. The
// element must also carry the `--wails-drop-target: drop` CSS marker.
export function useDesktopFileDrop(
  ref: React.RefObject<HTMLElement | null>,
  onPaths: DropHandler,
  enabled: boolean,
) {
  useEffect(() => {
    if (!enabled || !ref.current) {
      return undefined
    }
    return registerDropZone(ref.current, onPaths)
  }, [ref, onPaths, enabled])
}
