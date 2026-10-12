import { useCallback, useEffect, useRef, useState } from 'react'

import type { LiveTableOperationStats } from '../types/dataPools'
import type { ProjectLiveTableRunReference } from '../types/projectLiveTables'
import { request } from './apiClient'
import { useWebSocket } from './useWebSocket'

// A run's live tables, kept current by value-free WebSocket hints with a
// 2-second fallback refresh. Shared by the run page's panel and Command Center.

export interface LiveTableSummary extends LiveTableOperationStats {
  id: string
  alias: string
  profile: string
  durability: string
  state: string
  revision?: number
  counts?: Partial<Record<'seeded' | 'pushed' | 'available' | 'leased' | 'acknowledged' | 'popped' | 'quarantined' | 'discarded' | 'delivery_attempts', number>>
  data_bytes?: number
  finalization?: { state?: string; mode?: string; output_pool_id?: string; reason?: string }
}

export interface LiveTablesSnapshot { testId: string; tables: LiveTableSummary[]; projectTables: ProjectLiveTableRunReference[]; updatedAt?: string }

/** The API path of a run's live tables. */
export const liveTablesPath = (testId: string) => `/tests/${encodeURIComponent(testId)}/live-tables`

/** A table's state in plain words. */
export function liveTableStateLabel(table: LiveTableSummary): string {
  if (table.state === 'recovering') return 'Recovering table state'
  if (table.state === 'closed') return 'Closed'
  if (table.state === 'unavailable') return 'Unavailable on this controller'
  return table.counts?.available === 0 ? 'Empty — waiting for producers' : 'Active'
}

export function useLiveTablesSnapshot(testId: string, enabled = true) {
  const [snapshot, setSnapshot] = useState<LiveTablesSnapshot | null>(null)
  const [error, setError] = useState('')
  const refreshRequest = useRef<(() => void) | null>(null)
  const retry = useCallback(() => refreshRequest.current?.(), [])
  const sequence = useRef<{ testId: string; value: number } | null>(null)
  const { connectionState } = useWebSocket({
    testId,
    enabled,
    onOpen: () => { sequence.current = null; retry() },
    onMessage: message => {
      if (message.type !== 'live_tables_changed' || message.test_id !== testId) return
      const previous = sequence.current
      if (typeof message.event_index === 'number') {
        // Every hint causes an authoritative fetch, including a sequence gap
        // or reset after adoption. Never apply telemetry as table state.
        if (previous?.testId === testId && message.event_index === previous.value) return
        sequence.current = { testId, value: message.event_index }
      }
      retry()
    },
  })
  useEffect(() => {
    if (!enabled) return undefined
    let pending: AbortController | null = null
    let timer: ReturnType<typeof setTimeout>
    let disposed = false
    let offline = false
    let queued = false
    const load = async () => {
      if (disposed || offline) return
      // A busy run can emit hints faster than this request completes. Finish
      // the authoritative read, then coalesce all missed hints into one read.
      if (pending) { queued = true; return }
      clearTimeout(timer)
      const controller = new AbortController()
      pending = controller
      try {
        const result = await request<{ tables: LiveTableSummary[]; project_tables?: ProjectLiveTableRunReference[]; updated_at?: string }>(liveTablesPath(testId), { signal: controller.signal })
        if (controller.signal.aborted) return
        setSnapshot({ testId, tables: result.tables || [], projectTables: result.project_tables || [], updatedAt: result.updated_at }); setError('')
      } catch {
        if (controller.signal.aborted) return
        setSnapshot(null); setError('Live table status is unavailable. Previously displayed row values have been cleared.')
      } finally {
        pending = null
        if (!disposed && !offline) {
          if (queued) { queued = false; void load() }
          else timer = setTimeout(() => void load(), 2000)
        }
      }
    }
    const goOffline = () => { offline = true; queued = false; pending?.abort(); clearTimeout(timer); setSnapshot(null); setError('Offline. Reconnect to refresh live table status.') }
    const goOnline = () => { offline = false; void load() }
    const visible = () => { if (document.visibilityState === 'visible') retry() }
    const refresh = () => { void load() }
    refreshRequest.current = refresh
    window.addEventListener('offline', goOffline); window.addEventListener('online', goOnline); document.addEventListener('visibilitychange', visible)
    void load()
    return () => { disposed = true; pending?.abort(); clearTimeout(timer); if (refreshRequest.current === refresh) refreshRequest.current = null; window.removeEventListener('offline', goOffline); window.removeEventListener('online', goOnline); document.removeEventListener('visibilitychange', visible) }
  }, [enabled, testId, retry])
  const current = snapshot?.testId === testId ? snapshot : null
  return { current, error, connectionState, retry }
}
