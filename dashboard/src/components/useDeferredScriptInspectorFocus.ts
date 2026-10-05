import { useCallback, useEffect, useRef, type RefObject } from 'react'

export interface ScriptInspectorFocusOwner {
  scriptId?: string | null
  projectId?: string | null
  folderId?: string | null
  initialScript?: object | null
  path: string
  action: object
}

function sameOwner(left: ScriptInspectorFocusOwner | null, right: ScriptInspectorFocusOwner): boolean {
  return left !== null && left.scriptId === right.scriptId && left.projectId === right.projectId
    && left.folderId === right.folderId && left.initialScript === right.initialScript
    && left.path === right.path && left.action === right.action
}

/** A deferred navigation focus must never take ownership from a later operator focus or document. */
export function useDeferredScriptInspectorFocus(root: RefObject<HTMLElement | null>) {
  const ownerRef = useRef<ScriptInspectorFocusOwner | null>(null)
  const pendingFrameRef = useRef<number | null>(null)
  const requestRef = useRef({ generation: 0 })
  const focusGenerationRef = useRef(0)
  useEffect(() => {
    const requests = requestRef.current
    const focusChanged = () => { focusGenerationRef.current++ }
    document.addEventListener('focusin', focusChanged, true)
    return () => {
      document.removeEventListener('focusin', focusChanged, true)
      requests.generation++
      ownerRef.current = null
      if (pendingFrameRef.current !== null) cancelAnimationFrame(pendingFrameRef.current)
    }
  }, [])
  const updateOwner = useCallback((owner: ScriptInspectorFocusOwner | null) => { ownerRef.current = owner }, [])
  const requestFocus = useCallback((owner: ScriptInspectorFocusOwner, locate: (panel: HTMLElement) => HTMLElement | null, scroll = false) => {
    if (pendingFrameRef.current !== null) cancelAnimationFrame(pendingFrameRef.current)
    const request = ++requestRef.current.generation
    const origin = document.activeElement
    const focusGeneration = focusGenerationRef.current
    pendingFrameRef.current = requestAnimationFrame(() => {
      if (request !== requestRef.current.generation) return
      pendingFrameRef.current = null
      if (!sameOwner(ownerRef.current, owner) || focusGeneration !== focusGenerationRef.current) return
      // Removing the selected issue dialog can leave body focused without a new operator focus.
      if (document.activeElement !== origin && (origin?.isConnected || document.activeElement !== document.body)) return
      const panel = root.current?.querySelector<HTMLElement>('.author-main-action')
      if (!panel || panel.closest('[hidden], [inert]')) return
      const field = locate(panel)
      field?.focus()
      if (scroll) field?.scrollIntoView({ block: 'nearest' })
    })
  }, [root])
  return { updateOwner, requestFocus }
}
