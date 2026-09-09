import { describe, expect, it } from 'vitest'
import type { RunArchiveReport } from '../types'
import { runOutcomeLabels } from './runOutcomeLabels'

describe('run outcome headline', () => {
  it('does not infer workload completion from absent termination evidence', () => {
    expect(runOutcomeLabels()).toEqual({ execution: 'Unknown termination reason', elapsed: 'Unknown' })
  })
  it('keeps observed elapsed time separate from planned duration', () => {
    const manifest = { termination_reason: 'operator_stopped', completed_at: '2026-09-09T10:04:37Z', provenance: { started_at: '2026-09-09T10:00:00Z', duration: '20m' } } as RunArchiveReport['manifest']
    expect(runOutcomeLabels(manifest)).toEqual({ execution: 'Stopped by operator', elapsed: '277s' })
    expect(runOutcomeLabels({ ...manifest, completed_at: '2026-09-09T09:00:00Z' }).elapsed).toBe('Unknown')
  })
})
