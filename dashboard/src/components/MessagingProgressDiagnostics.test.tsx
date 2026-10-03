// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import type { MessagingProgressSnapshot } from '../types'
import MessagingProgressDiagnostics from './MessagingProgressDiagnostics'
import { capturedSequenceLag, checkpointTiming } from './messagingProgress'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
function fixture(): MessagingProgressSnapshot {
  const now = '2026-10-03T00:00:00Z'
  const position = { state: 'observed' as const, sequence: '9007199254740993', provider_offset: '2:-1:-1', observed_at: now, received_at: now, observation_age_ms: 10 }
  return { version: 1, lifecycle: 'running', captured_at: now, rows: [{ origin: 'peer', group: 'workers', protocol: 'eventhubs', mode: 'processor', source_id: 'source', source: '<hub>', subscription: 'consumer', session_id: 'session', partition: '0', generation: 'generation', revision: '1', capture_revision: '1', captured_at: now, updated_at: now, expected_partitions: 1, inventory_complete: true, ownership: 'owned', ownership_verified_at: now, ownership_verified_age_ms: 10, acquired: 1, lost: 0, delivered: 1, rejected: 0, redelivered: 0, delivered_position: { ...position }, checkpoint_position: { ...position }, tail_position: { ...position, sequence: '9007199254740995' }, checkpoint: { attempts: 0, committed: 0, failed: 0, fenced: 0, cancelled_before_write: 0, unconfirmed: 0, retry_attempts: 0, in_flight: 0, duration: { metric: 'checkpoint', population: 'checkpoint_operation', eligible: 0, distribution_ms: { count: 0, sum: 0, min: 0, max: 0, buckets: [] } }, last_completed_at: now } }] }
}
it('calculates exact source-capture lag and distinguishes observed zero', () => {
  const snapshot = fixture(); const row = snapshot.rows[0]
  expect(capturedSequenceLag(snapshot, row, 'delivered')).toBe('2')
  row.tail_position.sequence = row.checkpoint_position.sequence
  expect(capturedSequenceLag(snapshot, row, 'checkpoint')).toBe('0')
  row.tail_position.sequence = '9007199254740992'
  expect(capturedSequenceLag(snapshot, row, 'checkpoint')).toBe('Unavailable')
})
it('does not score missing, stale, incomplete, unverified or unconfirmed positions', () => {
  const changes: Array<(snapshot: MessagingProgressSnapshot) => void> = [
    s => { s.incomplete = true }, s => { s.rows[0].incomplete = true },
    s => { s.rows[0].inventory_complete = false }, s => { s.rows[0].ownership = 'unverified' },
    s => { s.rows[0].tail_position.observation_age_ms = 30001 },
    s => { s.rows[0].checkpoint_position.observation_age_ms = undefined },
    s => { s.rows[0].checkpoint_position.state = 'unconfirmed' },
    s => { s.rows[0].checkpoint.in_flight = 1 }, s => { s.rows[0].tail_position.sequence = '01' },
    s => { s.rows[0].tail_position.sequence = '9223372036854775808' },
  ]
  for (const change of changes) { const s = fixture(); change(s); expect(capturedSequenceLag(s, s.rows[0], 'checkpoint')).toBe('Unavailable') }
})
it('renders exact cursors, checkpoint outcomes and safely escaped identities', async () => {
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    const snapshot = fixture()
    await act(async () => root.render(<MessagingProgressDiagnostics snapshot={snapshot} />))
    expect(container.textContent).toContain('9007199254740993')
    expect(container.textContent).toContain('transit time is unqualified')
    expect(container.textContent).toContain('0 attempts; 0 committed')
    expect(container.textContent).toContain('provider offset 2:-1:-1')
    expect(container.textContent).toContain('Unavailable (0 eligible operations; 0 observed)')
    expect(container.querySelector('hub')).toBeNull()
    snapshot.incomplete = true; snapshot.dropped_rows = 3
    await act(async () => root.render(<MessagingProgressDiagnostics snapshot={snapshot} />))
    expect(container.textContent).toContain('Processor coverage incomplete')
    expect(container.textContent).toContain('At least 3 rows omitted')
    expect(container.textContent).toContain('Unavailable')
    snapshot.rows = []
    await act(async () => root.render(<MessagingProgressDiagnostics snapshot={snapshot} />))
    expect(container.textContent).toContain('No partition ownership observations captured.')
  } finally { act(() => root.unmount()) }
})

it('keeps missing checkpoint duration separate from measured zero', () => {
  const duration = fixture().rows[0].checkpoint.duration
  expect(checkpointTiming(duration)).toContain('Unavailable')
  duration.eligible = 2
  expect(checkpointTiming(duration)).toContain('2 eligible operations; 0 observed')
  duration.distribution_ms = { count: 1, sum: 0, min: 0, max: 0, buckets: [1] }
  expect(checkpointTiming(duration)).toBe('0.000 ms mean; 1/2 operations observed')
})
