import { createContext, useContext, useEffect, useMemo, useRef } from 'react'

export interface NavigationGuard {
  id: string
  label: string
  isDirty: () => boolean
  save: () => Promise<boolean> | boolean
  discard: () => void
  /** False when the work cannot be saved from the leave prompt (e.g. an import review awaiting decisions). */
  canSave?: boolean
}

export interface RegisteredNavigationGuard extends NavigationGuard {
  dirty: boolean
  sequence: number
}

export type NavigationGuardRegistrar = (guard: NavigationGuard, dirty: boolean) => () => void

export interface NavigationGuardRunner {
  hasDirtyGuard: () => boolean
  runGuarded: (proceed: () => void, retainedGuardIds?: readonly string[]) => void
  /**
   * Marks a registered guard clean immediately. A guard's rendered `dirty`
   * snapshot only updates after the next render, so an editor that has just
   * saved and then navigates in the same handler must clear it explicitly.
   */
  markClean: (guardId: string) => void
}

export interface PendingNavigation {
  retainedGuardIds?: readonly string[]
  guard: RegisteredNavigationGuard
  proceed: () => void
}

export const NavigationGuardRegisterContext = createContext<NavigationGuardRegistrar>(() => () => undefined)
export const NavigationGuardRunnerContext = createContext<NavigationGuardRunner>({
  hasDirtyGuard: () => false,
  runGuarded: (proceed) => proceed(),
  markClean: () => undefined,
})

export function firstDirtyGuard(guards: Map<string, RegisteredNavigationGuard>, retainedGuardIds: readonly string[] = []): RegisteredNavigationGuard | null {
  return [...guards.values()]
    .filter(guard => !retainedGuardIds.includes(guard.id) && (guard.dirty || guard.isDirty()))
    .sort((left, right) => right.sequence - left.sequence)[0] || null
}

export function useNavigationGuard(guard: NavigationGuard | null | undefined, dirty: boolean) {
  const registerGuard = useContext(NavigationGuardRegisterContext)

  useEffect(() => {
    if (!guard) return undefined
    return registerGuard(guard, dirty)
  }, [dirty, guard, registerGuard])
}

export function useNavigationGuardRunner() {
  return useContext(NavigationGuardRunnerContext)
}

/**
 * Guards work that only lives in this page (an import queue, review decisions)
 * and cannot be saved from the leave prompt: leaving asks to discard it.
 * isDirty, when given, reads the work's live state, which can change before
 * the page renders again (a draft saved and navigated away from in one turn).
 */
export function useUnsavedWorkGuard(id: string, label: string, dirty: boolean, discard: () => void, isDirty?: () => boolean) {
  const discardRef = useRef(discard)
  discardRef.current = discard
  const dirtyRef = useRef(dirty)
  dirtyRef.current = dirty
  const isDirtyRef = useRef(isDirty)
  isDirtyRef.current = isDirty
  const guard = useMemo<NavigationGuard>(() => ({
    id,
    label,
    canSave: false,
    isDirty: () => isDirtyRef.current ? isDirtyRef.current() : dirtyRef.current,
    save: () => false,
    discard: () => discardRef.current(),
  }), [id, label])
  useNavigationGuard(guard, dirty)
}
