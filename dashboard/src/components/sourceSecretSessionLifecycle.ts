import { useCallback, useEffect, useMemo, useRef } from 'react'

type SourceSecretSession = {
  projectId: string
  sessionId: string
}

type DiscardSourceSecretSession = (request: {
  project_id: string
  source_secret_session_id: string
}) => Promise<unknown>

function sessionKey(session: SourceSecretSession): string {
  return `${session.projectId}\u0000${session.sessionId}`
}

/**
 * Owns protected source-secret sessions returned by importer conversions.
 *
 * A lane keeps its previous accepted session alive while a replacement request
 * is pending. Once the replacement is accepted, the previous session is
 * discarded. A late response is discarded only when it is not the active
 * session for any lane, preventing a stale request from destroying a session
 * that a newer response adopted.
 */
export function useSourceSecretSessionLifecycle(discardRequest: DiscardSourceSecretSession) {
  const discardRequestRef = useRef(discardRequest)
  discardRequestRef.current = discardRequest
  const activeByLaneRef = useRef(new Map<string, SourceSecretSession>())
  const discardedRef = useRef(new Set<string>())
  const discardInFlightRef = useRef(new Map<string, Promise<void>>())

  const isActive = useCallback((session: SourceSecretSession): boolean => {
    const key = sessionKey(session)
    return Array.from(activeByLaneRef.current.values()).some(active => sessionKey(active) === key)
  }, [])

  const discard = useCallback((session: SourceSecretSession): Promise<void> => {
    if (!session.projectId || !session.sessionId || isActive(session)) return Promise.resolve()
    const key = sessionKey(session)
    if (discardedRef.current.has(key)) return Promise.resolve()
    const existing = discardInFlightRef.current.get(key)
    if (existing) return existing

    const pending = Promise.resolve()
      .then(async () => {
        // A newer response may adopt the same protected session before this
        // queued cleanup reaches the API.
        if (isActive(session)) return false
        await discardRequestRef.current({
          project_id: session.projectId,
          source_secret_session_id: session.sessionId,
        })
        return true
      })
      .then((discarded) => {
        if (discarded) discardedRef.current.add(key)
      })
      .catch(() => {
        // Protected sessions expire server-side. Keep a failed discard
        // retryable at the next lifecycle boundary.
      })
      .finally(() => {
        discardInFlightRef.current.delete(key)
      })
    discardInFlightRef.current.set(key, pending)
    return pending
  }, [isActive])

  const replace = useCallback((
    lane: string,
    projectId: string | undefined,
    sessionId: string | undefined,
  ) => {
    const previous = activeByLaneRef.current.get(lane)
    const next = projectId && sessionId ? { projectId, sessionId } : null
    if (next) activeByLaneRef.current.set(lane, next)
    else activeByLaneRef.current.delete(lane)
    if (previous && (!next || sessionKey(previous) !== sessionKey(next))) {
      void discard(previous)
    }
  }, [discard])

  const discardUnaccepted = useCallback((
    projectId: string | undefined,
    sessionId: string | undefined,
  ) => {
    if (!projectId || !sessionId) return
    void discard({ projectId, sessionId })
  }, [discard])

  const clear = useCallback((lane?: string) => {
    const sessions = lane
      ? [activeByLaneRef.current.get(lane)].filter((session): session is SourceSecretSession => Boolean(session))
      : Array.from(activeByLaneRef.current.values())
    if (lane) activeByLaneRef.current.delete(lane)
    else activeByLaneRef.current.clear()
    sessions.forEach(session => { void discard(session) })
  }, [discard])

  const settle = useCallback((
    lane: string,
    projectId: string | undefined,
    sessionId: string | undefined,
  ) => {
    if (!projectId || !sessionId) return
    const active = activeByLaneRef.current.get(lane)
    if (!active || sessionKey(active) !== sessionKey({ projectId, sessionId })) return
    // A successful migration/discard has already consumed this one-shot
    // server session. Remove only the local owner; calling the discard API a
    // second time would turn a successful settlement into a noisy 404 retry.
    activeByLaneRef.current.delete(lane)
    discardedRef.current.add(sessionKey(active))
  }, [])

  useEffect(() => () => {
    const sessions = Array.from(activeByLaneRef.current.values())
    activeByLaneRef.current.clear()
    sessions.forEach(session => { void discard(session) })
  }, [discard])

  return useMemo(
    () => ({ clear, discardUnaccepted, replace, settle }),
    [clear, discardUnaccepted, replace, settle],
  )
}
