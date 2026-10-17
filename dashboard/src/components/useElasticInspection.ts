import { useCallback, useEffect, useRef, useState } from 'react'
import { useELKConnectionsApi } from '../hooks/api/useELKConnectionsApi'
import type { ElasticInspection, ElasticInspectionInput } from '../types/elastic'

export function useElasticInspection(projectId?: string) {
  const api = useELKConnectionsApi()
  const [operation, setOperation] = useState<ElasticInspection>()
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const requestController = useRef<AbortController | undefined>(undefined)
  const generation = useRef(0)
  // Keep ownership independent of the rendered result: a failed status read
  // must not lose the ID needed to cancel work in its original project.
  const ownedOperation = useRef<{ projectId: string; operation: ElasticInspection } | undefined>(undefined)
  const invalidateRequests = useCallback(() => {
    generation.current++
    requestController.current?.abort()
  }, [])
  const cancelOwnedOperation = useCallback(() => {
    const previous = ownedOperation.current
    ownedOperation.current = undefined
    if (previous && ['pending', 'queued', 'running'].includes(previous.operation.status)) {
      void api.cancelELKInspection(previous.projectId, previous.operation.connection_id, previous.operation.id).catch(() => {})
    }
  }, [api])
  const clear = useCallback(() => {
    invalidateRequests(); cancelOwnedOperation()
    setOperation(undefined); setError(''); setBusy(false)
  }, [invalidateRequests, cancelOwnedOperation])
  useEffect(() => {
    clear()
    return () => { invalidateRequests(); cancelOwnedOperation() }
  }, [projectId, clear, invalidateRequests, cancelOwnedOperation])
  useEffect(() => {
    if (!projectId || !operation || !['pending', 'queued', 'running'].includes(operation.status)) return
    const controller = new AbortController()
    const version = generation.current
    const timer = setTimeout(() => {
      void api.getELKInspection(projectId, operation.connection_id, operation.id, { signal: controller.signal }).then(value => { if (!controller.signal.aborted && generation.current === version) { ownedOperation.current = { projectId, operation: value }; setOperation(value) } }).catch(err => { if (!controller.signal.aborted && generation.current === version) { setError(String(err)); setOperation(undefined) } })
    }, 1500)
    return () => { clearTimeout(timer); controller.abort() }
  }, [api, projectId, operation])
  const start = useCallback(async (connectionId: string, input: ElasticInspectionInput) => {
    if (!projectId) return
    clear()
    const version = generation.current
    const controller = new AbortController(); requestController.current = controller
    setBusy(true); setError(''); setOperation(undefined)
    try {
      const result = await api.inspectELKConnection(projectId, connectionId, input, { signal: controller.signal })
      if (!controller.signal.aborted && generation.current === version) { ownedOperation.current = { projectId, operation: result }; setOperation(result) }
      else if (['pending', 'queued', 'running'].includes(result.status)) { void api.cancelELKInspection(projectId, result.connection_id, result.id).catch(() => {}) }
    } catch (err) { if (!controller.signal.aborted && generation.current === version) setError(String(err)) } finally { if (!controller.signal.aborted && generation.current === version) setBusy(false) }
  }, [api, projectId, clear])
  const cancel = useCallback(async () => {
    if (!projectId || !operation) return
    const version = generation.current
    try {
      await api.cancelELKInspection(projectId, operation.connection_id, operation.id)
      if (generation.current !== version) return
      if (ownedOperation.current?.operation.id === operation.id) ownedOperation.current.operation.status = 'cancelled'
      setOperation(current => current?.id === operation.id && current.connection_id === operation.connection_id ? { ...current, status: 'cancelled' } : current)
    } catch (err) { if (generation.current === version) setError(String(err)) }
  }, [api, projectId, operation])
  return { operation, error, busy: busy || Boolean(operation && ['pending', 'queued', 'running'].includes(operation.status)), start, cancel, clear }
}
