import { useCallback, useEffect, useRef, useState } from 'react'
import type { TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'

interface QueuedRunScope {
  run: TestSummary | null
  routeId?: string
  projectId?: string
  applicationId?: string
  releaseId?: string
  resolvedStatus: string
  canManageTests: boolean
  canSelectRun: boolean
  active: boolean
  prototypeMode: boolean
}

// A route/socket status alone cannot admit a source. The canonical run and
// current owning project must agree, and a started run can never be revised.
export function queuedRunRevisionScope(input: QueuedRunScope): string {
  const run = input.run
  if (!input.active || input.prototypeMode || !input.canManageTests || !input.canSelectRun || !input.projectId || !input.routeId || !run || run.id !== input.routeId || run.project_id !== input.projectId || run.status !== 'QUEUED' || input.resolvedStatus !== 'QUEUED' || run.started_at || run.ended_at || !run.created_at) return ''
  return JSON.stringify([run.id, run.project_id, run.created_at, input.applicationId ?? null, input.releaseId ?? null])
}

type RequestToken = { generation: number }
type RevisionOwner = { scope: string; request: RequestToken; snapshot: TestSummary }
type ScopedRequest = { scope: string; request: RequestToken }
type ScopedError = ScopedRequest & { message: string }

export function useQueuedRunRevision(scope: string, source: TestSummary | null, readTest: (id: string) => Promise<TestSummary>, onSelectRun?: (id: string) => void) {
  const [owner, setOwner] = useState<RevisionOwner | null>(null)
  const [pending, setPending] = useState<ScopedRequest | null>(null)
  const [error, setError] = useState<ScopedError | null>(null)
  const scopeRef = useRef(scope)
  const requestRef = useRef<RequestToken | null>(null)
  const nextGenerationRef = useRef(0)
  const mountedRef = useRef(false)
  // Every observed ownership change retires the old request, even if navigation
  // later returns to the same run before that request completes.
  if (scopeRef.current !== scope) {
    scopeRef.current = scope
    requestRef.current = null
  }

  // Visibility is fenced synchronously below, before this cleanup can run.
  useEffect(() => {
    setOwner(current => current?.scope === scope && current.request === requestRef.current ? current : null)
    setPending(current => current?.scope === scope && current.request === requestRef.current ? current : null)
    setError(current => current?.scope === scope && current.request === requestRef.current ? current : null)
  }, [scope])
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      requestRef.current = null
    }
  }, [])

  const clear = useCallback(() => {
    requestRef.current = null
    setOwner(null)
    setPending(null)
    setError(null)
  }, [])
  const close = useCallback(() => {
    const request = owner?.request ?? pending?.request
    if (!request || scopeRef.current !== scope || requestRef.current !== request) return
    clear()
  }, [clear, owner, pending, scope])
  const open = useCallback(async () => {
    if (!mountedRef.current || !scope || !source || !onSelectRun || scopeRef.current !== scope) return
    const identity = { id: source.id, project: source.project_id, created: source.created_at }
    const request = { generation: ++nextGenerationRef.current }
    requestRef.current = request
    setOwner(null)
    setError(null)
    setPending({ scope, request })
    try {
      const current = await readTest(identity.id)
      if (requestRef.current !== request || scopeRef.current !== scope) return
      if (current.id !== identity.id || current.project_id !== identity.project || current.created_at !== identity.created || current.status !== 'QUEUED' || current.started_at || current.ended_at) throw new Error('The queued source changed or has started. Reload the run before creating a revised copy.')
      setOwner({ scope, request, snapshot: structuredClone(current) })
    } catch (failure) {
      if (requestRef.current === request && scopeRef.current === scope) setError({ scope, request, message: getErrorMessage(failure, 'Unable to load the queued source for revision.') })
    } finally {
      if (requestRef.current === request && scopeRef.current === scope) setPending(null)
    }
  }, [onSelectRun, readTest, scope, source])
  const created = useCallback((id: string) => {
    if (!id || !owner || scopeRef.current !== owner.scope || requestRef.current !== owner.request) return
    clear()
    onSelectRun?.(id)
  }, [clear, onSelectRun, owner])

  return { incarnation: owner?.scope === scope && owner.request === requestRef.current ? owner.request.generation : undefined, snapshot: owner?.scope === scope && owner.request === requestRef.current ? owner.snapshot : null, loading: Boolean(scope && pending?.scope === scope && pending.request === requestRef.current), error: error?.scope === scope && error.request === requestRef.current ? error.message : '', open, close, created }
}
