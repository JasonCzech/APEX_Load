import { describe, expect, it } from 'vitest'
import type { MetricsHistoryPoint, RunArchiveReport, TestRunManifest, TestSummary } from '../../types'
import { executionDate, executionFacts, executionLoadPoints, recordedNumber } from './executionPresentation'

const test: TestSummary = { id: 'run', name: 'Run', status: 'FAILED', duration: '20m', total_vusers: 10, groups: [], created_at: '', error: 'Controller restarted' }

describe('execution evidence presentation', () => {
  it.each([undefined, '', 'invalid', '0001-01-01T00:00:00Z'])('renders missing or invalid dates as unavailable: %s', value => {
    expect(executionDate(value)).toBe('Not recorded')
  })
  it.each([undefined, NaN, Infinity, -1])('does not turn unavailable values into zero: %s', value => {
    expect(recordedNumber(value)).toBe('Not recorded')
  })
  it('preserves a recorded zero', () => expect(recordedNumber(0)).toBe('0'))
  it('does not infer peak load, elapsed time or successful completion from the final sample', () => {
    expect(executionFacts(test)).toMatchObject({ peak: 'Not recorded', elapsed: 'Not recorded', reason: 'Controller restarted', immutable: false })
    expect(executionFacts({ ...test, started_at: '2026-09-10T12:00:00Z', ended_at: '2026-09-10T11:00:00Z' }).elapsed).toBe('Not recorded')
  })
  it('uses archived timing and failure details before mutable test values', () => {
    const report = { manifest: { error: 'Archived failure', provenance: { started_at: '2026-09-10T12:00:00Z' }, completed_at: '2026-09-10T12:04:37Z' }, overview: { peak_active_vusers: 8 } } as RunArchiveReport
    expect(executionFacts(test, report)).toMatchObject({ reason: 'Archived failure', elapsed: '4m 37s', peak: '8' })
  })
  it('uses original configuration only with explicit immutable provenance', () => {
    const manifest = { schema_version: 'v1', test_id: 'run', test_name: 'Run', status: 'FAILED', source: 'archive', generated_at: '', identity: {}, current_configuration: {}, load_generators: { execution_settings: {} }, initial_configuration: { total_vusers: 5, duration: '10m', groups: [] }, initial_configuration_provenance: { source: 'archive_provenance', immutable: true } } as TestRunManifest
    expect(executionFacts(test, null, manifest)).toMatchObject({ immutable: true, vusers: 5, duration: '10m' })
    expect(executionFacts(test, null, { ...manifest, initial_configuration_provenance: undefined })).toMatchObject({ immutable: false, vusers: 10, duration: '20m' })
  })
  it('does not label mutable fallback values as immutable when original fields are absent', () => {
    const manifest = { initial_configuration: {}, initial_configuration_provenance: { immutable: true } } as TestRunManifest
    expect(executionFacts({ ...test, groups: [{ name: 'Changed group', vusers: 10, script_id: 'changed', script_name: 'Changed script', protocol: 'http' }] }, null, manifest))
      .toMatchObject({ immutable: true, duration: undefined, vusers: undefined, groups: [] })
  })
  it('preserves irregular elapsed timestamps, orders samples and rejects invalid chart data', () => {
    const point = (timestampMs: number, vusers = 2, resourceSampleOnly = 0) => ({ timestampMs, vusers, resourceSampleOnly }) as MetricsHistoryPoint
    const start = Date.parse('2026-09-10T12:00:00Z')
    const history = [point(start + 60000), point(Infinity), point(1e20), point(start), point(start + 1000), point(start, -1), point(start, 2, 1)]
    expect(executionLoadPoints(history).map(p => p.timestampMs)).toEqual([start, start + 1000, start + 60000])
    expect(history[0].timestampMs).toBe(start + 60000)
  })

})
