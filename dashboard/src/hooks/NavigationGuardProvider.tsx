import { createContext, useContext, useEffect } from 'react'

export interface NavigationGuard {
  id: string
  label: string
  isDirty: () => boolean
  save: () => Promise<boolean> | boolean
  discard: () => void
}

export interface RegisteredNavigationGuard extends NavigationGuard {
  dirty: boolean
  sequence: number
}

export type NavigationGuardRegistrar = (guard: NavigationGuard, dirty: boolean) => () => void

export interface NavigationGuardRunner {
  hasDirtyGuard: () => boolean
  runGuarded: (proceed: () => void) => void
}

export interface PendingNavigation {
  guard: RegisteredNavigationGuard
  proceed: () => void
}

export const NavigationGuardRegisterContext = createContext<NavigationGuardRegistrar>(() => () => undefined)
export const NavigationGuardRunnerContext = createContext<NavigationGuardRunner>({
  hasDirtyGuard: () => false,
  runGuarded: (proceed) => proceed(),
})

export function firstDirtyGuard(guards: Map<string, RegisteredNavigationGuard>): RegisteredNavigationGuard | null {
  return [...guards.values()]
    .filter(guard => guard.dirty || guard.isDirty())
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
