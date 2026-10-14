// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { RecordingEnvironment } from '../types/recordingEnvironment'
import type { ScriptDefinition } from '../types/scripts'
import RecordingEnvironmentReview from './RecordingEnvironmentReview'

const receipt: RecordingEnvironment = { version: 1, observation_source: 'local_recorder', engine: 'chromium', device_profile: 'ipad_air', profile_browser_family: 'webkit', emulated: true, applied_context: { viewport_width: 820, viewport_height: 1180, device_scale_factor: 2, mobile: true, has_touch: true }, physical_device_qualification: 'unverified' }
const script: ScriptDefinition = { name: 'Recorded journey', version: '1', protocol: 'browser', config: { base_url: '', browser: { engine: 'webkit', device_profile: 'ipad_air' } }, actions: [], metadata: { import: { source: 'browser_recorder', preserved: { recording_environment: receipt } } } }

describe('recording environment review', () => {
  let container: HTMLDivElement, root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(() => { act(() => root.unmount()); container.remove() })
  const render = (props: Parameters<typeof RecordingEnvironmentReview>[0]) => act(() => { root.render(<RecordingEnvironmentReview {...props} />) })
  const field = (label: string) => [...container.querySelectorAll('dl > div')].find(row => row.querySelector('dt')?.textContent === label)?.querySelector('dd')?.textContent

  it('distinguishes recorded Chromium, emulated iPad intent and WebKit replay while retaining unverified hardware status', () => {
    render({ script })
    expect(field('Recorded engine')).toBe('Chromium')
    expect(field('Emulated preset')).toBe('iPad Air')
    expect(field('Applied viewport')).toBe('820 × 1180')
    expect(field('Configured replay engine')).toBe('WebKit')
    expect(field('Physical Chrome/iPad qualification')).toBe('Unverified')
    expect(container.textContent).toContain('differs')
    expect(container.textContent).toContain('Historical recording receipt')
    const changed = { ...script, config: { base_url: '', browser: { engine: 'chromium' } } }
    render({ script: changed })
    expect(field('Configured replay engine')).toBe('Chromium')
    expect(field('Recorded engine')).toBe('Chromium')
    expect(container.textContent).not.toContain('differs')
    expect(field('Physical Chrome/iPad qualification')).toBe('Unverified')
    expect(script.metadata?.import?.preserved?.recording_environment).toBe(receipt)
  })

  // RA-18: one line says what was recorded; the full receipt opens from it,
  // and an engine mismatch stays in view.
  it('folds the receipt behind a one-line summary and keeps a mismatch visible', () => {
    render({ script })
    const details = container.querySelector('details')!
    expect(details.open).toBe(false)
    expect(container.querySelector('h4')?.textContent).toBe('Recording environment')
    expect(details.querySelector('summary')?.textContent).toBe('Chromium · iPad Air · 820 × 1180 · reported by the recorder')
    const warning = container.querySelector('.recording-environment-warning')!
    expect(warning.textContent).toContain('differs')
    expect(details.contains(warning)).toBe(false)
    render({ environment: { ...receipt, observation_source: 'agent_reported', emulated: false, device_profile: '', profile_browser_family: '', applied_context: {} }, agentReported: true, live: true })
    expect(container.querySelector('summary')?.textContent).toBe('Chromium · no device emulation · reported by the Recording Agent')
    render({ environment: { version: 99 } })
    expect(container.querySelector('summary')?.textContent).toBe('Receipt could not be read')
  })

  it('qualifies live agent reports and does not label them as controller observation', () => {
    render({ environment: receipt, agentReported: true, live: true })
    expect(field('Observation source')).toBe('Recording agent reported')
    expect(container.textContent).toContain('controller did not independently observe')
    expect(container.textContent).toContain('Recording launch receipt')
  })

  it('keeps optional applied booleans distinct from explicit false and omits browser replay comparisons on HTTP outputs', () => {
    render({ script: { ...script, protocol: 'http', config: { base_url: '' } }, environment: { ...receipt, emulated: false, device_profile: '', profile_browser_family: '', applied_context: { mobile: false } } })
    expect(field('Applied mobile mode')).toBe('Disabled')
    expect(field('Applied touch mode')).toBe('Unavailable')
    expect(field('Configured replay engine')).toBe('Not applicable (HTTP output)')
    expect(field('Emulated preset')).toBe('None applied')
    expect(container.textContent).not.toContain('differs')
  })

  it('retains legacy preset as declared intent without inferring observations', () => {
    render({ script: { ...script, metadata: { import: { source: 'browser_recorder', preserved: { device_profile: 'ipad_air' } } } } })
    expect(field('Declared preset intent')).toBe('iPad Air')
    expect(field('Recorded engine')).toBe('Unavailable')
    expect(field('Applied mobile mode')).toBe('Unavailable')
    expect(field('Configured replay engine')).toBe('WebKit')
    expect(container.textContent).toContain('does not supply recorded engine')
  })

  it.each([null, [], { ...receipt, version: 2 }, { ...receipt, engine: { toString: null } }, { ...receipt, observation_source: 'private-source' }, { ...receipt, device_profile: 'private-preset' }, { ...receipt, physical_device_qualification: 'verified' }])('shows unavailable evidence for malformed receipt without raw claim disclosure', environment => {
    render({ environment, declaredPreset: 'private-declared-preset', showUnavailable: true })
    expect(field('Recorded engine')).toBe('Unavailable')
    expect(field('Physical Chrome/iPad qualification')).toBe('Unverified')
    expect(container.textContent).toContain('malformed or unsupported')
    expect(container.innerHTML).not.toContain('private-')
  })

  it('omits the panel for unrelated imports and does not echo unrecognized configured engines', () => {
    render({ script: { ...script, config: { base_url: '' }, metadata: { import: { source: 'har' } } } })
    expect(container.querySelector('section')).toBeNull()
    render({ script: { ...script, config: { base_url: '', browser: { engine: 'private-engine' } } } })
    expect(field('Configured replay engine')).toBe('Unavailable (not explicitly configured)')
    expect(container.innerHTML).not.toContain('private-engine')
  })
})
