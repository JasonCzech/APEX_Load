import type { TestSummary } from '../types'

type ChronologyRun = Partial<Pick<TestSummary, 'ended_at' | 'start_time' | 'created_at' | 'ref_key'>> & {
  id: string
  /** When the caller knows the completion time from another source (a trend point). */
  completed_at?: string
}

function time(value?: string) {
  const parsed = Date.parse(value || '')
  return Number.isFinite(parsed) ? parsed : 0
}

/** The numeric part of a run reference (LOC-RUN-42 → 42); references count up as runs are created. */
function referenceNumber(ref?: string) {
  const match = /(\d+)\s*$/.exec(ref || '')
  return match ? Number(match[1]) : -1
}

/**
 * The one oldest-to-newest order for completed runs, shared by every view that
 * names a "latest" or "previous" run. Runs that finish in the same second (two
 * runs started together) are ordered by start, creation and run reference, so
 * the landing page and the release trend agree on which run came first.
 */
export function compareRunsChronologically(a: ChronologyRun, b: ChronologyRun): number {
  const completedAt = (run: ChronologyRun) => run.completed_at || run.ended_at || run.start_time || run.created_at
  return time(completedAt(a)) - time(completedAt(b))
    || time(a.start_time) - time(b.start_time)
    || time(a.created_at) - time(b.created_at)
    || referenceNumber(a.ref_key) - referenceNumber(b.ref_key)
    || a.id.localeCompare(b.id)
}
