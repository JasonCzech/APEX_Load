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
})

function buildTest(status: TestSummary['status']): Pick<TestSummary, 'id' | 'status'> {
  return { id: 'run', status }
}
