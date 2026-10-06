import { describe, expect, it } from 'vitest'
import { decodeBrowserResourcePage } from './browserResourcePageModel'
import { resourcePageFixture } from './browserResourcePage.testFixtures'
const expected = { testID: 'run-one', projectID: 'project-one' }
const decode = (value: unknown) => decodeBrowserResourcePage(value, expected)

describe('archive browser resource page admission', () => {
  it('owns the approved page and preserves native/legacy zero, false and nanoseconds', () => {
    const raw = resourcePageFixture(); const page = decode(raw)
    expect(page.items[0].timestamp).toBe('2026-10-05T12:00:00.123456789Z')
    expect(page.items[0]).toMatchObject({ status_code: 0, duration_ms: 0, bytes_received: 0, browser_evidence: { epoch_start_ms: 0, service_worker: false, bytes: { response_encoded_body: 0 } } })
    raw.items[0].browser_evidence!.phases![0].end_ms = 100
    expect(page.items[0].browser_evidence!.phases![0].end_ms).toBe(0)
  })
  it.each(['version', 'test_id', 'project_id', 'source_format', 'source_revision', 'source_integrity', 'source_timestamp_precision', 'population', 'capture_coverage', 'items'])('requires %s', key => {
    const page = resourcePageFixture() as unknown as Record<string, unknown>; delete page[key]; expect(() => decode(page)).toThrow()
  })
  it.each([
    ['test_id', 'other-run'], ['test_id', ''], ['project_id', 'other-project'], ['project_id', ''], ['version', 2], ['source_format', 'csv'],
    ['source_revision', 'A'.repeat(64)], ['source_revision', 'a'.repeat(63)], ['source_integrity', 'verified'], ['source_timestamp_precision', 'microsecond'],
    ['next_cursor', ''], ['next_cursor', 'path/escape'], ['next_cursor', 'a.'.repeat(3000)], ['future', true], ['items', null],
  ])('rejects invalid or foreign %s', (key, value) => { expect(() => decode({ ...resourcePageFixture(), [key]: value })).toThrow() })
  it.each(['availability', 'transaction_records', 'retained_entry_observations', 'matched_retained_observations'])('requires population %s', key => {
    const page = resourcePageFixture(); delete (page.population as unknown as Record<string, unknown>)[key]; expect(() => decode(page)).toThrow()
  })
  it('keeps missing projection distinct from observed zero and filter zero', () => {
    const page = resourcePageFixture(); delete page.next_cursor; page.items = []
    page.population = { availability: 'unavailable', transaction_records: 2, retained_entry_observations: null, matched_retained_observations: null }
    expect(decode(page).population.retained_entry_observations).toBeNull()
    page.population = { availability: 'retained_observations', transaction_records: 2, retained_entry_observations: 0, matched_retained_observations: 0 }
    expect(decode(page).population.retained_entry_observations).toBe(0)
    page.population.retained_entry_observations = 80
    expect(decode(page).population.matched_retained_observations).toBe(0)
  })
  it.each([
    { retained_entry_observations: null }, { matched_retained_observations: null }, { retained_entry_observations: -1 },
    { transaction_records: 2_000_001 }, { retained_entry_observations: 2_000_001 }, { matched_retained_observations: 81 }, { matched_retained_observations: 0 },
  ])('refuses misleading population %#', fields => { const page = resourcePageFixture(); Object.assign(page.population, fields); expect(() => decode(page)).toThrow() })
  it('refuses invented capture coverage and unavailable populated rows', () => {
    const page = resourcePageFixture(); page.capture_coverage.availability = 'complete' as never; expect(() => decode(page)).toThrow()
    const unavailable = resourcePageFixture(); unavailable.population = { availability: 'unavailable', transaction_records: 2, retained_entry_observations: null, matched_retained_observations: null }
    expect(() => decode(unavailable)).toThrow()
  })
  it('refuses duplicate locators and overlarge pages', () => {
    const page = resourcePageFixture(); page.items.push(structuredClone(page.items[0])); expect(() => decode(page)).toThrow()
    page.items = Array.from({ length: 101 }, (_, i) => ({ ...page.items[0], locator: { ...page.items[0].locator, resource_ordinal: i + 1 } })); page.population.matched_retained_observations = 101; page.population.retained_entry_observations = 101
    expect(() => decode(page)).toThrow()
  })
  it.each([
    ['timestamp', null], ['timestamp', '2026-02-30T00:00:00Z'], ['timestamp', '0001-01-01T00:00:00Z'], ['timestamp_precision', 'mixed'],
    ['status_code', 1000], ['duration_ms', -1], ['duration_ms', Infinity], ['bytes_received', 2 ** 50 + 1],
    ['failure_kind', 'unknown'], ['url', 'x'.repeat(2049)], ['action_name', 'bad\nlabel'], ['action_name', 'bad\0label'], ['future', true], ['evidence_state', 'healthy'],
  ])('refuses invalid row %s', (key, value) => { const page = resourcePageFixture(); Object.assign(page.items[0], { [key]: value }); expect(() => decode(page)).toThrow() })
  it('normalizes optional pointer nulls to owned absence and retains explicit unavailable reasons', () => {
    const page = resourcePageFixture(); const row = page.items[0] as unknown as Record<string, unknown>
    Object.assign(row, { timestamp: null, timestamp_precision: 'unavailable', status_code: null, duration_ms: null, bytes_received: null, browser_evidence: null, evidence_state: 'malformed' })
    const owned = decode(page).items[0]
    for (const key of ['timestamp', 'status_code', 'duration_ms', 'bytes_received', 'browser_evidence']) expect(owned).not.toHaveProperty(key)
    expect(owned.evidence_state).toBe('malformed'); expect(row.timestamp).toBeNull()
    row.evidence_state = 'available'; expect(() => decode(page)).toThrow()
  })
  it('refuses noncanonical HMAC signatures while admitting exact cursor shape', () => {
    const page = resourcePageFixture(); page.next_cursor = `payload.${'A'.repeat(42)}B`; expect(() => decode(page)).toThrow()
    page.next_cursor = `payload.${'A'.repeat(42)}8`; expect(decode(page).next_cursor).toBe(page.next_cursor)
  })
  it('preserves legacy rows without fabricating native evidence', () => {
    const page = resourcePageFixture(); const row = page.items[0]; delete row.browser_evidence; row.evidence_state = 'not_captured'
    delete row.timestamp; row.timestamp_precision = 'unavailable'; delete row.duration_ms; delete row.status_code; delete row.bytes_received
    expect(decode(page).items[0]).not.toHaveProperty('duration_ms')
    row.evidence_state = 'malformed'; expect(decode(page).items[0].evidence_state).toBe('malformed')
    row.evidence_state = 'unsupported_version'; expect(decode(page).items[0].evidence_state).toBe('unsupported_version')
    row.evidence_state = 'available'; expect(() => decode(page)).toThrow()
  })
  it('refuses malformed or future native evidence inside an available row', () => {
    const page = resourcePageFixture(); page.items[0].browser_evidence!.request_id = 'foreign-request'; expect(() => decode(page)).toThrow()
    page.items[0].browser_evidence!.version = 3 as never; expect(() => decode(page)).toThrow()
  })
})
