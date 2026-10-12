import { describe, expect, it } from 'vitest'
import { pauseDurationNote, runActionConfirmationCopy } from './runActionConfirmation'

describe('run action confirmations', () => {
  it('says that a run makes up the time it spends paused', () => {
    expect(pauseDurationNote({ duration: '4m', termination_mode: 'duration' })).toBe('Paused time does not count against the 4m duration: the run makes it up after you resume, so it ends that much later.')
    expect(pauseDurationNote({ duration: '4m' })).toContain('the 4m duration')
    // A duration the controller stored after an adjustment reads as the page shows it.
    expect(pauseDurationNote({ duration: '1h30m0s' })).toContain('the 1h 30m duration')
    // A run to its iterations has no clock to spend, and an unreadable duration says nothing.
    expect(pauseDurationNote({ duration: '4m', termination_mode: 'iterations' })).toBe('')
    expect(pauseDurationNote({ duration: '' })).toBe('')
    expect(pauseDurationNote(undefined)).toBe('')
  })

  it('confirms a live load change like the run controls', () => {
    expect(runActionConfirmationCopy('apply-target')).toMatchObject({ title: 'Apply this load change?', confirmLabel: 'Apply change', tone: 'primary' })
  })
})
