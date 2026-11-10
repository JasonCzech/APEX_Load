import { useEffect, useRef, useState } from 'react'
import { request } from '../../hooks/apiClient'
import type { RunArchiveReport } from '../../types'
import type { RunArchiveTimeAnatomy } from '../../types/timeAnatomy'
import { getErrorMessage } from '../../utils/errorUtils'

export type AnatomyLoad =
  | { status: 'loading' }
  | { status: 'ready'; anatomy?: RunArchiveTimeAnatomy }
  | { status: 'error'; message: string }

/** Reports the page already holds; each stands in for a fetch of the run its manifest names. */
export type AnatomyReportSeeds = readonly (RunArchiveReport | null | undefined)[]

const NO_SEEDS: AnatomyReportSeeds = []

const fetchReport = (id: string, signal: AbortSignal) =>
  request<RunArchiveReport>(`/tests/${encodeURIComponent(id)}/archive/report?include_release_trend=false`, { signal })

/**
 * Each run's `time_anatomy`, fetched once per run id while `enabled` and kept
 * for the life of the workspace (only the anatomy, not the whole report).
 * A fetch aborted before it finished is dropped, and a failed one is retried
 * the next time it is enabled, or when `attempt` changes (a Retry button).
 */
export function useAnatomyReports(ids: readonly string[], enabled: boolean, seeds: AnatomyReportSeeds = NO_SEEDS, attempt = 0): Record<string, AnatomyLoad> {
  const [cache, setCache] = useState<Record<string, AnatomyLoad>>({})
  const cacheRef = useRef(cache)
  cacheRef.current = cache
  // Ids with a fetch in flight. A 'loading' cache entry can outlive its
  // fetch (an effect re-run aborts it before the queued cleanup update
  // lands), so this, not the cache, says what is already being fetched.
  const inFlight = useRef(new Set<string>())
  const seedById = new Map(seeds.flatMap(report => (report?.manifest?.test_id ? [[report.manifest.test_id, report] as const] : [])))
  const seededKey = ids.filter(id => seedById.has(id)).join('\n')
  const idsKey = ids.join('\n')

  useEffect(() => {
    if (!enabled) return
    const seeded = new Set(seededKey ? seededKey.split('\n') : [])
    const wanted = (idsKey ? idsKey.split('\n') : []).filter(id => !seeded.has(id) && !inFlight.current.has(id) && cacheRef.current[id]?.status !== 'ready')
    if (!wanted.length) return
    const controller = new AbortController()
    const fetching = inFlight.current
    for (const id of wanted) fetching.add(id)
    setCache(old => ({ ...old, ...Object.fromEntries(wanted.map(id => [id, { status: 'loading' } as AnatomyLoad])) }))
    for (const id of wanted) {
      fetchReport(id, controller.signal).then(
        report => { if (!controller.signal.aborted) setCache(old => ({ ...old, [id]: { status: 'ready', anatomy: report.time_anatomy } })) },
        (error: unknown) => { if (!controller.signal.aborted) setCache(old => ({ ...old, [id]: { status: 'error', message: getErrorMessage(error, 'The report could not be loaded') } })) },
      ).finally(() => { if (!controller.signal.aborted) fetching.delete(id) })
    }
    return () => {
      controller.abort()
      // Synchronously, so the next run (same commit) refetches what this one dropped.
      for (const id of wanted) fetching.delete(id)
      setCache(old => Object.fromEntries(Object.entries(old).filter(([id, load]) => !(wanted.includes(id) && load.status === 'loading'))))
    }
  }, [enabled, idsKey, seededKey, attempt])

  const out: Record<string, AnatomyLoad> = {}
  for (const id of ids) {
    const seed = seedById.get(id)
    if (seed) out[id] = { status: 'ready', anatomy: seed.time_anatomy }
    else if (cache[id]) out[id] = cache[id]
  }
  return out
}
