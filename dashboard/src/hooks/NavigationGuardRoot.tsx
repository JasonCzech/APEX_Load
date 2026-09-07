import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'

import UnsavedChangesModal from '../components/UnsavedChangesModal'
import {
  NavigationGuardRegisterContext,
  NavigationGuardRunnerContext,
  firstDirtyGuard,
  type NavigationGuardRegistrar,
  type NavigationGuardRunner,
  type PendingNavigation,
  type RegisteredNavigationGuard,
} from './NavigationGuardProvider'

export function NavigationGuardProvider({ children }: { children: ReactNode }) {
  const guardsRef = useRef<Map<string, RegisteredNavigationGuard>>(new Map())
  const sequenceRef = useRef(0)
  const [version, setVersion] = useState(0)
  const [pending, setPending] = useState<PendingNavigation | null>(null)
  const pendingRef = useRef<PendingNavigation | null>(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const bumpVersion = useCallback(() => setVersion(current => current + 1), [])
  const registerGuard = useMemo<NavigationGuardRegistrar>(() => ((guard, dirty) => {
    const existing = guardsRef.current.get(guard.id)
    guardsRef.current.set(guard.id, { ...guard, dirty, sequence: existing?.sequence ?? ++sequenceRef.current })
    bumpVersion()
    return () => {
      const existingGuard = guardsRef.current.get(guard.id)
      if (existingGuard?.id !== guard.id) return
      guardsRef.current.delete(guard.id)
      bumpVersion()
    }
  }), [bumpVersion])
  const hasDirtyGuard = useCallback(() => Boolean(firstDirtyGuard(guardsRef.current)), [])
  const runGuarded = useCallback((proceed: () => void, retainedGuardIds: readonly string[] = []) => {
    if (pendingRef.current) return
    const guard = firstDirtyGuard(guardsRef.current, retainedGuardIds)
    if (!guard) { proceed(); return }
    setError(null)
    const next = { guard, proceed, retainedGuardIds }
    pendingRef.current = next
    setPending(next)
  }, [])
  useEffect(() => {
    if (!hasDirtyGuard()) return undefined
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!hasDirtyGuard()) return
      event.preventDefault()
      event.returnValue = 'unsaved changes'
    }
    window.addEventListener('beforeunload', handleBeforeUnload)
    return () => window.removeEventListener('beforeunload', handleBeforeUnload)
  }, [hasDirtyGuard, version])
  const runner = useMemo<NavigationGuardRunner>(() => ({ hasDirtyGuard, runGuarded }), [hasDirtyGuard, runGuarded])
  const handleCancel = useCallback(() => {
    if (!saving) {
      pendingRef.current = null
      setPending(null)
      setError(null)
    }
  }, [saving])
  const continueAfterResolvedGuard = useCallback((resolved: PendingNavigation) => {
    // A single page can publish multiple independent editors. Remove the guard
    // just resolved before inspecting the rest; its React dirty prop is updated
    // on the next render, and leaving the stale registration in the map would
    // either prompt twice or let another dirty editor be skipped.
    guardsRef.current.delete(resolved.guard.id)
    const nextGuard = firstDirtyGuard(guardsRef.current, resolved.retainedGuardIds)
    if (nextGuard) {
      const next = { guard: nextGuard, proceed: resolved.proceed, retainedGuardIds: resolved.retainedGuardIds }
      pendingRef.current = next
      setPending(next)
      setError(null)
      bumpVersion()
      return
    }
    pendingRef.current = null
    setPending(null)
    setError(null)
    bumpVersion()
    resolved.proceed()
  }, [bumpVersion])
  const handleDiscard = useCallback(() => {
    if (!pending || saving) return
    pending.guard.discard()
    continueAfterResolvedGuard(pending)
  }, [continueAfterResolvedGuard, pending, saving])
  const handleSave = useCallback(async () => {
    if (!pending || saving) return
    setSaving(true); setError(null)
    try {
      const saved = await pending.guard.save()
      if (!saved) { setError('Save did not complete. Fix validation errors, then try again.'); return }
      continueAfterResolvedGuard(pending)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Unable to save changes.')
    } finally { setSaving(false) }
  }, [continueAfterResolvedGuard, pending, saving])
  return (
    <NavigationGuardRegisterContext.Provider value={registerGuard}>
      <NavigationGuardRunnerContext.Provider value={runner}>
        {children}
        {pending && <UnsavedChangesModal label={pending.guard.label} saving={saving} error={error} onSave={() => { void handleSave() }} onDiscard={handleDiscard} onCancel={handleCancel} />}
      </NavigationGuardRunnerContext.Provider>
    </NavigationGuardRegisterContext.Provider>
  )
}
