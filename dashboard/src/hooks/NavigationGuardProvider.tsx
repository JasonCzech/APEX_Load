import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import UnsavedChangesModal from '../components/UnsavedChangesModal'

export interface NavigationGuard {
  id: string
  label: string
  isDirty: () => boolean
  save: () => Promise<boolean> | boolean
  discard: () => void
}

interface RegisteredNavigationGuard extends NavigationGuard {
  dirty: boolean
  sequence: number
}

type NavigationGuardRegistrar = (guard: NavigationGuard, dirty: boolean) => () => void

interface NavigationGuardRunner {
  hasDirtyGuard: () => boolean
  runGuarded: (proceed: () => void) => void
}

interface PendingNavigation {
  guard: RegisteredNavigationGuard
  proceed: () => void
}

const NavigationGuardRegisterContext = createContext<NavigationGuardRegistrar>(() => () => undefined)
const NavigationGuardRunnerContext = createContext<NavigationGuardRunner>({
  hasDirtyGuard: () => false,
  runGuarded: (proceed) => proceed(),
})

function firstDirtyGuard(guards: Map<string, RegisteredNavigationGuard>): RegisteredNavigationGuard | null {
  return [...guards.values()]
    .filter(guard => guard.dirty || guard.isDirty())
    .sort((left, right) => left.sequence - right.sequence)[0] || null
}

export function NavigationGuardProvider({ children }: { children: ReactNode }) {
  const guardsRef = useRef<Map<string, RegisteredNavigationGuard>>(new Map())
  const sequenceRef = useRef(0)
  const [version, setVersion] = useState(0)
  const [pending, setPending] = useState<PendingNavigation | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const bumpVersion = useCallback(() => setVersion(current => current + 1), [])

  const registerGuard = useMemo<NavigationGuardRegistrar>(() => (
    (guard, dirty) => {
      const existing = guardsRef.current.get(guard.id)
      guardsRef.current.set(guard.id, {
        ...guard,
        dirty,
        sequence: existing?.sequence ?? ++sequenceRef.current,
      })
      bumpVersion()
      return () => {
        const existingGuard = guardsRef.current.get(guard.id)
        if (existingGuard?.id !== guard.id) return
        guardsRef.current.delete(guard.id)
        bumpVersion()
      }
    }
  ), [bumpVersion])

  const hasDirtyGuard = useCallback(() => Boolean(firstDirtyGuard(guardsRef.current)), [])

  const runGuarded = useCallback((proceed: () => void) => {
    if (pending) return
    const guard = firstDirtyGuard(guardsRef.current)
    if (!guard) {
      proceed()
      return
    }
    setError(null)
    setPending({ guard, proceed })
  }, [pending])

  useEffect(() => {
    if (!hasDirtyGuard()) return undefined
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!hasDirtyGuard()) return
      event.preventDefault()
      event.returnValue = 'unsaved changes'
    }
    window.addEventListener('beforeunload', handleBeforeUnload)
    return () => {
      window.removeEventListener('beforeunload', handleBeforeUnload)
    }
  }, [hasDirtyGuard, version])

  const runner = useMemo<NavigationGuardRunner>(() => ({
    hasDirtyGuard,
    runGuarded,
  }), [hasDirtyGuard, runGuarded])

  const handleCancel = useCallback(() => {
    if (saving) return
    setPending(null)
    setError(null)
  }, [saving])

  const handleDiscard = useCallback(() => {
    if (!pending || saving) return
    pending.guard.discard()
    const proceed = pending.proceed
    setPending(null)
    setError(null)
    proceed()
  }, [pending, saving])

  const handleSave = useCallback(async () => {
    if (!pending || saving) return
    setSaving(true)
    setError(null)
    try {
      const saved = await pending.guard.save()
      if (!saved) {
        setError('Save did not complete. Fix validation errors, then try again.')
        return
      }
      const proceed = pending.proceed
      setPending(null)
      setError(null)
      proceed()
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Unable to save changes.')
    } finally {
      setSaving(false)
    }
  }, [pending, saving])

  return (
    <NavigationGuardRegisterContext.Provider value={registerGuard}>
      <NavigationGuardRunnerContext.Provider value={runner}>
        {children}
        {pending && (
          <UnsavedChangesModal
            label={pending.guard.label}
            saving={saving}
            error={error}
            onSave={() => { void handleSave() }}
            onDiscard={handleDiscard}
            onCancel={handleCancel}
          />
        )}
      </NavigationGuardRunnerContext.Provider>
    </NavigationGuardRegisterContext.Provider>
  )
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
