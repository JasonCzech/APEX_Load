import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent } from '../../types/replay'
import { replayFrameForEvent } from './replaySourceFrame'

const ev = (o: Partial<BrowserReplayEvent>): BrowserReplayEvent => ({ phase: 'action', action_name: 'A', action_type: 'request', target: '', success: true, started_at: '', completed_at: '', duration_ms: 1, ...o })

describe('replayFrameForEvent', () => {
  it('treats an omitted action_index as the first action', () => {
    expect(replayFrameForEvent(ev({ phase: 'init' }))).toMatchObject({ phase: 'init', action_index: 0 })
  })
  it('falls back to requests[N] for sequence requests without a step path', () => {
    expect(replayFrameForEvent(ev({ action_index: 3, request_index: 2 }))?.step_path).toEqual(['requests[2]'])
    expect(replayFrameForEvent(ev({ action_index: 3, request_index: 2, step_path: ['then[0]'] }))?.step_path).toEqual(['then[0]'])
  })
  it('rejects session-level and negative locations', () => {
    expect(replayFrameForEvent(ev({ phase: 'finalize' }))).toBeNull()
    expect(replayFrameForEvent(ev({ action_index: -1 }))).toBeNull()
  })
})
