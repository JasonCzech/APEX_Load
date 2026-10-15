// Native file-drop routing for the desktop shell.
//
// Wails exposes a SINGLE global window.runtime.OnFileDrop callback, so we
// register it once and route the dropped absolute paths to whichever registered
// drop zone contains the drop point. Drop zones opt in with the
// `--wails-drop-target: drop` CSS marker (set by consumers) so Wails recognizes
// them. In the browser this hook is inert.

import { useEffect } from 'react'

import { installExternalDropGuard } from './externalDropGuard'
import { getWailsEventRuntime } from './wailsRuntime'

type DropHandler = (paths: string[]) => void

interface DropZone {
  element: HTMLElement
  handler: DropHandler
}

interface FileDropRuntime {
  OnFileDrop?: (callback: (x: number, y: number, paths: string[]) => void, useDropTarget: boolean) => void
}

const zones = new Set<DropZone>()
let registeredRuntime: FileDropRuntime | undefined

// installDesktopDropHandling runs when the desktop page loads, before any
// screen renders (the engine's start-up and start-up failure screens
// included). Nothing dropped on the window may replace the page: Windows'
// webview accepts external drops (desktop/dragdrop.go). A file goes to the
// router, whose Wails page listeners (added only once OnFileDrop is first
// called) cancel its default; a link or text goes to the external drop guard.
// The page never removes it; the returned function is for tests.
export function installDesktopDropHandling(target: Window = window): () => void {
  ensureGlobalListener()
  return installExternalDropGuard(target)
}

function ensureGlobalListener() {
  const runtime = getWailsEventRuntime() as (FileDropRuntime | undefined)
  if (!runtime?.OnFileDrop || registeredRuntime === runtime) {
    return
  }
  registeredRuntime = runtime
  runtime.OnFileDrop((x, y, dropped) => {
    // Windows reports an item that is not a file (an attachment dragged out of
    // a mail client, an image from a browser) as an empty path. It has nothing
    // to read, so the drop is what remains.
    const paths = (dropped ?? []).filter(path => typeof path === 'string' && path.trim() !== '')
    if (paths.length === 0) {
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

// registerDropZone wires an element to receive native dropped paths. Returns an
// unregister function. Exported for tests.
export function registerDropZone(element: HTMLElement, handler: DropHandler): () => void {
  const zone: DropZone = { element, handler }
  zones.add(zone)
  ensureGlobalListener()
  return () => {
    zones.delete(zone)
    // Keep the Wails listener for the renderer lifetime. OnFileDropOff maps to
    // EventsOff("wails:file-drop"), which removes every listener for the shared
    // event — including the Go listener that grants dropped paths to the secure
    // file bridge. The global callback is harmless while `zones` is empty.
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
