import { useLayoutEffect, useRef, useState } from 'react'
import type { TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

type RunHistory =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'success'; runs: TestSummary[] }

// A failed read is not a successful empty result. Keep one request owner per
// scenario, and discard all owners when the workspace changes (even A→B→A).
// Explicit refreshes bypass successful cache entries, but share pending reads.
export function useScenarioRunHistory(projectId: string | undefined) {
  const scope = useRef({ projectId, entries: {} as Record<string, RunHistory>, requests: new Map<string, object>() })
  if (scope.current.projectId !== projectId) scope.current = { projectId, entries: {}, requests: new Map() }
  const [snapshot, setSnapshot] = useState({ scope: scope.current, entries: scope.current.entries })
  useLayoutEffect(() => {
    const owner = scope.current
    setSnapshot({ scope: owner, entries: owner.entries })
    return () => owner.requests.clear()
  }, [projectId])

  const load = async (id: string, request: (id: string) => Promise<{ tests?: TestSummary[] }>, force = false) => {
    const owner = scope.current
    const existing = owner.entries[id]
    if (existing?.status === 'loading' || (!force && existing?.status === 'success')) return
    const token = {}
    owner.requests.set(id, token)
    const publish = (entry: RunHistory) => {
      if (scope.current !== owner || owner.requests.get(id) !== token) return
      owner.entries = { ...owner.entries, [id]: entry }
      setSnapshot({ scope: owner, entries: owner.entries })
    }
    publish({ status: 'loading' })
    try {
      const response = await request(id)
      const runs = [...(response.tests || [])].sort((a, b) => (
        new Date(b.start_time || b.created_at || 0).getTime() - new Date(a.start_time || a.created_at || 0).getTime()
      ))
      publish({ status: 'success', runs })
    } catch (error) {
      publish({ status: 'error', message: getErrorMessage(error, 'Scenario runs could not be loaded') })
    }
  }

  return { entries: snapshot.scope === scope.current ? snapshot.entries : {}, load }
}
