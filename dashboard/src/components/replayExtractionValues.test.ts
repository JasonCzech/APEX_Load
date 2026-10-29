import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../types/replay'
import { appliedReplayExtractions } from './replayExtractionValues'
import { mergeReplayVariables, buildReplayVariableDiffs } from './scriptEditorUtils'
import { variablesAt } from './debuggerExperience'

describe('streamed replay extraction history', () => {
  it('applies child values in order followed by parent values, and never applies rejected values', () => {
    const event: BrowserReplayEvent = {
      phase: 'action', action_name: 'stream', action_type: 'request', target: '/', success: false,
      started_at: '', completed_at: '', duration_ms: 1,
      streamed_children: [
        { action_name: 'first', success: true, extractions: { token: 'first', one: '1' } },
        { action_name: 'second', success: false, extractions: { token: 'second' }, unapplied_extractions: { rejected: 'secret' } },
      ],
      extractions: { token: 'parent' }, unapplied_extractions: { rejected_parent: 'secret' },
    }
    const expected = { token: 'parent', one: '1' }
    expect(appliedReplayExtractions(event)).toEqual(expected)
    expect(mergeReplayVariables({ token: 'seed' }, [event])).toEqual(expected)
    expect(variablesAt([event], { token: 'seed' }, 0)).toEqual(expected)
    expect(buildReplayVariableDiffs({ token: 'seed' }, [event])).toEqual([{ token: { previous: 'seed', next: 'parent' }, one: { previous: undefined, next: '1' } }])
  })

  it('retains prototype-like variable names and their previous values', () => {
    const seed = JSON.parse('{"__proto__":"before","constructor":"old"}') as Record<string, string>
    const event: BrowserReplayEvent = {
      phase: 'action', action_name: 'stream', action_type: 'request', target: '/', success: true,
      started_at: '', completed_at: '', duration_ms: 1,
      streamed_children: [{ action_name: 'child', success: true, extractions: JSON.parse('{"__proto__":"after","constructor":"new"}') as Record<string, string> }],
    }
    const [diff] = buildReplayVariableDiffs(seed, [event])
    expect(Object.keys(diff).sort()).toEqual(['__proto__', 'constructor'])
    expect(diff['__proto__']).toEqual({ previous: 'before', next: 'after' })
    expect(diff.constructor).toEqual({ previous: 'old', next: 'new' })
  })
})
