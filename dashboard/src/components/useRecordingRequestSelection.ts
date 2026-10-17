import { useCallback, useLayoutEffect, useRef, useState, type SetStateAction } from 'react'
import type { RecorderRequestRecord as RecordedRequest, RecorderSession as RecordingSession } from '../hooks/api/useRecorderApi'

type RequestDraft = Pick<Required<RecordedRequest>, 'step_name' | 'transaction_name' | 'note' | 'think_time_ms' | 'excluded'>

function draftFor(request: RecordedRequest | null): RequestDraft {
  return {
    step_name: request?.step_name || '', transaction_name: request?.transaction_name || '',
    note: request?.note || '', think_time_ms: request?.think_time_ms || 0, excluded: !!request?.excluded,
  }
}

export interface RecordingRequestUpdate {
  scope: { key: string }
  sessionId: string
  seq: number
  selection: number
  draft: number
  excludedEdit: number
  mutation: object
}

// Selection and edits have separate generations: a completed request may update
// its session cache without owning either the current selection or newer typing.
export function useRecordingRequestSelection(scopeKey: string) {
  const scope = useRef({ key: scopeKey })
  if (scope.current.key !== scopeKey) scope.current = { key: scopeKey }
  const [state, setState] = useState({
    scope: scope.current,
    session: null as RecordingSession | null,
    request: null as RecordedRequest | null,
    draft: draftFor(null), selection: 0, edit: 0, excludedEdit: 0,
  })
  const current = useRef(state)
  const latestUpdates = useRef(new Map<string, object>())
  const pendingUpdates = useRef(new Map<string, object>())
  const [, setPendingRevision] = useState(0)
  const mounted = useRef(true)
  const publish = useCallback((next: typeof state) => {
    current.current = next
    setState(next)
  }, [])
  const clearSelection = useCallback((sessionId?: string) => {
    const previous = current.current
    if (sessionId && previous.session?.id !== sessionId) return
    publish({ scope: scope.current, session: null, request: null, draft: draftFor(null), selection: previous.selection + 1, edit: previous.edit + 1, excludedEdit: previous.excludedEdit + 1 })
  }, [publish])
  useLayoutEffect(clearSelection, [scopeKey, clearSelection])
  useLayoutEffect(() => {
    mounted.current = true
    const latest = latestUpdates.current
    const pending = pendingUpdates.current
    return () => { mounted.current = false; latest.clear(); pending.clear() }
  }, [])

  const selectSession = async (session: RecordingSession, load: (id: string) => Promise<RecordingSession>) => {
    const previous = current.current
    if (previous.session?.id === session.id) { clearSelection(); return }
    const owner = { scope: scope.current, selection: previous.selection + 1 }
    publish({ ...previous, ...owner, session, request: null, draft: draftFor(null), edit: previous.edit + 1 })
    if (session.requests?.length) return
    try {
      const full = await load(session.id)
      if (!mounted.current || scope.current !== owner.scope || current.current.selection !== owner.selection) return
      publish({ ...current.current, session: { ...full, requests: full.requests || [] } })
    } catch {
      // The selected summary stays visible; an older failure cannot select it
      // again after the user has selected another session or closed the detail.
    }
  }

  const selectRequest = (request: RecordedRequest | null) => {
    const previous = current.current
    publish({ ...previous, request, draft: draftFor(request), selection: previous.selection + 1, edit: previous.edit + 1 })
  }
  const setRequestDraft = (next: SetStateAction<RequestDraft>) => {
    const previous = current.current
    const draft = typeof next === 'function' ? next(previous.draft) : next
    publish({ ...previous, draft, edit: previous.edit + 1,
      excludedEdit: previous.excludedEdit + Number(draft.excluded !== previous.draft.excluded) })
  }
  const beginRequestUpdate = (sessionId: string, seq: number): RecordingRequestUpdate | null => {
    const key = JSON.stringify([scope.current.key, sessionId, seq])
    // Serialize writes to one recorded request, not just their UI responses:
    // otherwise an older HTTP request can arrive last and win in storage.
    if (pendingUpdates.current.has(key)) return null
    const mutation = {}
    latestUpdates.current.set(key, mutation)
    pendingUpdates.current.set(key, mutation)
    setPendingRevision(value => value + 1)
    return { scope: scope.current, sessionId, seq, selection: current.current.selection,
      draft: current.current.edit, excludedEdit: current.current.excludedEdit, mutation }
  }
  const isRequestUpdateCurrent = (update: RecordingRequestUpdate) => scope.current === update.scope
    && latestUpdates.current.get(JSON.stringify([update.scope.key, update.sessionId, update.seq])) === update.mutation
  const isRequestUpdating = (sessionId: string, seq: number) => pendingUpdates.current.has(JSON.stringify([scope.current.key, sessionId, seq]))
  const finishRequestUpdate = (update: RecordingRequestUpdate) => {
    const key = JSON.stringify([update.scope.key, update.sessionId, update.seq])
    if (pendingUpdates.current.get(key) !== update.mutation) return
    pendingUpdates.current.delete(key)
    latestUpdates.current.delete(key)
    setPendingRevision(value => value + 1)
  }
  const acceptRequestUpdate = (update: RecordingRequestUpdate, request: RecordedRequest, excludedOnly = false) => {
    if (!isRequestUpdateCurrent(update) || request.seq !== update.seq) return false
    const previous = current.current
    if (previous.session?.id !== update.sessionId) return true
    const session = { ...previous.session, requests: previous.session.requests?.map(item => item.seq === request.seq ? request : item) }
    const ownsSelection = previous.request?.seq === request.seq && previous.selection === update.selection
    let draft = previous.draft
    if (ownsSelection) {
      if (excludedOnly) {
        // Excluding a row updates that field without discarding other unsaved
        // edits, including edits already present when Exclude was pressed.
        if (previous.excludedEdit === update.excludedEdit) draft = { ...draft, excluded: !!request.excluded }
      } else if (previous.edit === update.draft) draft = draftFor(request)
    }
    publish({ ...previous, session, request: ownsSelection ? request : previous.request, draft })
    return true
  }

  const visible = state.scope === scope.current
  return {
    selectedSession: visible ? state.session : null, selectedRequest: visible ? state.request : null,
    requestDraft: state.draft, setRequestDraft, selectSession, selectRequest, clearSelection,
    beginRequestUpdate, acceptRequestUpdate, isRequestUpdateCurrent, isRequestUpdating, finishRequestUpdate,
  }
}
