import { describe, expect, it } from 'vitest'

import { compareRunsChronologically } from './runChronology'

describe('compareRunsChronologically', () => {
  it('orders runs that finished in the same second by start, creation and reference', () => {
    const ended = '2026-10-08T10:06:00Z'
    const baseline = { id: 'ffff', ref_key: 'LOC-RUN-9', created_at: '2026-10-08T10:00:00Z', start_time: '2026-10-08T10:00:00Z', ended_at: ended }
    const candidate = { id: '0000', ref_key: 'LOC-RUN-10', created_at: '2026-10-08T10:00:00Z', start_time: '2026-10-08T10:00:00Z', ended_at: ended }
    // The run ID would put the candidate first; the reference keeps creation order.
    expect([candidate, baseline].sort(compareRunsChronologically).map(run => run.ref_key)).toEqual(['LOC-RUN-9', 'LOC-RUN-10'])
  })

  it('prefers an explicit completion time over the run record', () => {
    expect(compareRunsChronologically({ id: 'a', completed_at: '2026-10-08T10:00:00Z', ended_at: '2026-10-09T00:00:00Z' }, { id: 'b', ended_at: '2026-10-08T11:00:00Z' })).toBeLessThan(0)
  })
})
