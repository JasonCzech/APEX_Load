import { describe, expect, it } from 'vitest'
import { normalizeRecordingEnvironment, recordingEnvironmentFingerprint, recordingPresetLabel } from './recordingEnvironmentModel'
import type { RecordingEnvironment } from '../types/recordingEnvironment'

const receipt: RecordingEnvironment = { version: 1, observation_source: 'local_recorder', engine: 'chromium', device_profile: 'ipad_air', profile_browser_family: 'webkit', emulated: true, applied_context: { viewport_width: 820, viewport_height: 1180, device_scale_factor: 2, mobile: true, has_touch: true }, physical_device_qualification: 'unverified' }

describe('recording environment admission', () => {
  it('preserves explicit false versus missing applied options and qualifies agent reports without mutating source', () => {
    const source = { ...receipt, applied_context: { mobile: false, has_touch: false } }
    expect(normalizeRecordingEnvironment(source, true)).toEqual({ ...source, observation_source: 'agent_reported' })
    expect(source.observation_source).toBe('local_recorder')
    expect(normalizeRecordingEnvironment({ ...receipt, applied_context: null })?.applied_context).toBeUndefined()
    expect(normalizeRecordingEnvironment({ ...receipt, applied_context: {} })?.applied_context).toBeUndefined()
  })

  it.each([
    undefined, null, [], 'private receipt', { ...receipt, version: 2 }, { ...receipt, engine: 'private-engine' },
    { ...receipt, observation_source: 'physical_device' }, { ...receipt, emulated: 'true' }, { ...receipt, emulated: false },
    { ...receipt, physical_device_qualification: 'verified' }, { ...receipt, device_profile: 'private-device' },
    { ...receipt, profile_browser_family: 'chromium' }, { ...receipt, applied_context: [] },
    ...[{ viewport_width: 820 }, { viewport_width: -1, viewport_height: 1 }, { viewport_width: 1.2, viewport_height: 1 }, { viewport_width: 8193, viewport_height: 1 }, { viewport_width: 8192, viewport_height: 8192 }, { viewport_width: 4096, viewport_height: 4096, device_scale_factor: 2 }, { device_scale_factor: 0 }, { device_scale_factor: 4.1 }, { device_scale_factor: Infinity }, { device_scale_factor: NaN }, { mobile: 'true' }, { has_touch: null }].map(applied_context => ({ ...receipt, applied_context })),
  ])('rejects malformed or unsupported receipt without normalizing claims into facts', value => {
    expect(normalizeRecordingEnvironment(value)).toBeNull()
  })

  it('accepts the exact combined device-pixel boundary', () => {
    expect(normalizeRecordingEnvironment({ ...receipt, applied_context: { viewport_width: 4096, viewport_height: 2048, device_scale_factor: 2 } })).not.toBeNull()
  })

  it('reads only fixed fields and ignores arbitrarily large unrelated claim structures', () => {
    const input = { ...receipt, arbitrary: { private: new Array(100_000).fill('private-value') } }
    Object.defineProperty(input, 'other', { enumerable: true, get: () => { throw new Error('arbitrary field read') } })
    const admitted = normalizeRecordingEnvironment(input)
    expect(admitted).toEqual(receipt)
    expect(JSON.stringify(admitted)).not.toContain('private')
    expect(recordingPresetLabel('__proto__')).toBeUndefined()
  })

  it('fingerprints canonical facts, not wire ordering or undisplayed values, and detects changed observations', () => {
    const reordered = { ...receipt, physical_device_qualification: receipt.physical_device_qualification, applied_context: { has_touch: true, mobile: true, device_scale_factor: 2, viewport_height: 1180, viewport_width: 820 }, extra: 'private-value' }
    expect(recordingEnvironmentFingerprint(reordered)).toBe(recordingEnvironmentFingerprint(receipt))
    expect(recordingEnvironmentFingerprint({ ...receipt, engine: 'webkit' })).not.toBe(recordingEnvironmentFingerprint(receipt))
    expect(recordingEnvironmentFingerprint(receipt, true)).not.toBe(recordingEnvironmentFingerprint(receipt))
    expect(recordingEnvironmentFingerprint(undefined)).not.toBe(recordingEnvironmentFingerprint({ ...receipt, version: 2 }))
    expect(recordingEnvironmentFingerprint({ ...receipt, engine: 'private-engine' })).not.toContain('private-engine')
  })
})
