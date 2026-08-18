import { describe, expect, it } from 'vitest'

import { isCompletedTestStatus, isLiveTestStatus, normalizeTestStatus, resolveTestStatus } from './testStatusUtils'
import type { TestSummary } from '../types'

describe('test status utilities', () => {
  it('normalizes status casing and common terminal aliases', () => {
    expect(normalizeTestStatus('failed')).toBe('FAILED')
    expect(normalizeTestStatus(' completed ')).toBe('COMPLETE')
    expect(normalizeTestStatus('cancelled')).toBe('ABORTED')
  })

  it('classifies live and completed test statuses after normalization', () => {
    expect(isLiveTestStatus('running')).toBe(true)
    expect(isLiveTestStatus('FAILED')).toBe(false)
    expect(isCompletedTestStatus('failed')).toBe(true)
    expect(isCompletedTestStatus('RUNNING')).toBe(false)
  })

  it('lets terminal persisted state beat stale non-terminal overrides', () => {
    expect(resolveTestStatus(buildTest('FAILED'), { run: 'RUNNING' })).toBe('FAILED')
    expect(resolveTestStatus(buildTest('COMPLETE'), { run: 'INITIALIZING' })).toBe('COMPLETE')
    expect(resolveTestStatus(buildTest('RUNNING'), { run: 'FAILED' })).toBe('FAILED')
    expect(resolveTestStatus(buildTest('QUEUED'), { run: 'RUNNING' })).toBe('RUNNING')
  })

  it('never lets a stale override roll the run backward through the lifecycle', () => {
    // A Stop issued on a paused run is known locally before its broadcast lands.
    expect(resolveTestStatus(buildTest('STOPPING'), { run: 'PAUSED' })).toBe('STOPPING')
    expect(resolveTestStatus(buildTest('STOPPING'), { run: 'RUNNING' })).toBe('STOPPING')
    expect(resolveTestStatus(buildTest('RUNNING'), { run: 'QUEUED' })).toBe('RUNNING')
    // Pause and resume move in both directions and must both be honoured.
    expect(resolveTestStatus(buildTest('RUNNING'), { run: 'PAUSED' })).toBe('PAUSED')
    expect(resolveTestStatus(buildTest('PAUSED'), { run: 'RUNNING' })).toBe('RUNNING')
    expect(resolveTestStatus(buildTest('PAUSED'), { run: 'STOPPING' })).toBe('STOPPING')
    // An unrecognized status has no comparable position; the override still wins.
    expect(resolveTestStatus(buildTest('STOPPING'), { run: 'MYSTERY' })).toBe('MYSTERY')
  })
})

function buildTest(status: TestSummary['status']): Pick<TestSummary, 'id' | 'status'> {
  return { id: 'run', status }
}
