import type { ScriptDefinition } from '../types/scripts'
import {
  legacyRecordingPreset, normalizeRecordingEnvironment, recordingEngine, recordingEngineLabel,
  recordingPresetLabel, scriptRecordingEnvironment,
} from './recordingEnvironmentModel'
import './RecordingEnvironmentReview.css'

export default function RecordingEnvironmentReview({ environment, declaredPreset, script, agentReported = false, live = false, showUnavailable = false }: {
  environment?: unknown
  declaredPreset?: unknown
  script?: ScriptDefinition
  agentReported?: boolean
  live?: boolean
  showUnavailable?: boolean
}) {
  const input = environment === undefined ? scriptRecordingEnvironment(script) : environment
  const receipt = normalizeRecordingEnvironment(input, agentReported)
  const requested = declaredPreset ?? legacyRecordingPreset(script)
  const hasRecordingProvenance = script?.metadata?.import?.source === 'browser_recorder'
  if (!showUnavailable && input === undefined && !requested && !hasRecordingProvenance) return null
  const replayEngine = script?.protocol === 'browser' ? recordingEngine(script.config?.browser?.engine) : undefined
  const context = receipt?.applied_context
  const booleanLabel = (value?: boolean) => value === undefined ? 'Unavailable' : value ? 'Enabled' : 'Disabled'
  const mismatch = receipt && replayEngine && receipt.engine !== replayEngine
  const replayLabel = script ? script.protocol === 'browser' ? replayEngine ? recordingEngineLabel(replayEngine) : 'Unavailable (not explicitly configured)' : script.protocol === 'http' ? 'Not applicable (HTTP output)' : 'Not applicable (non-browser output)' : 'Unavailable during recording'
  // One line says what was recorded; the full receipt opens from it.
  const summary = receipt
    ? [
        recordingEngineLabel(receipt.engine),
        receipt.emulated ? recordingPresetLabel(receipt.device_profile) || 'custom device emulation' : 'no device emulation',
        context?.viewport_width !== undefined ? `${context.viewport_width} × ${context.viewport_height}` : '',
        receipt.observation_source === 'agent_reported' ? 'reported by the Recording Agent' : 'reported by the recorder',
      ].filter(Boolean).join(' · ')
    : input === undefined ? 'Not reported' : 'Receipt could not be read'
  return <section className="recording-environment-review" aria-label="Recording environment">
    <h4>Recording environment</h4>
    <details>
      <summary className="recording-environment-summary">{summary}</summary>
      <p>{live ? 'Recording launch receipt.' : 'Historical recording receipt.'} This records workstation browser emulation. It does not establish physical-device compatibility or successful replay.</p>
      {!receipt && <p role="status">{input === undefined ? 'Recording observations unavailable for this legacy or unreported recording.' : 'Recording receipt is malformed or unsupported; observations are unavailable.'}</p>}
      <dl>
        <div><dt>Recorded engine</dt><dd>{receipt ? recordingEngineLabel(receipt.engine) : 'Unavailable'}</dd></div>
        <div><dt>Observation source</dt><dd>{receipt ? receipt.observation_source === 'agent_reported' ? 'Recording agent reported' : 'Local recorder reported' : 'Unavailable'}</dd></div>
        <div><dt>Emulated preset</dt><dd>{receipt ? receipt.emulated ? recordingPresetLabel(receipt.device_profile) || 'Custom workstation emulation' : 'None applied' : 'Unavailable'}</dd></div>
        {!receipt && requested != null && <div><dt>Declared preset intent</dt><dd>{recordingPresetLabel(requested) || 'Unrecognized preset'}</dd></div>}
        <div><dt>Preset browser family</dt><dd>{receipt?.profile_browser_family ? recordingEngineLabel(receipt.profile_browser_family) : 'Unavailable'}</dd></div>
        <div><dt>Applied viewport</dt><dd>{context?.viewport_width !== undefined ? `${context.viewport_width} × ${context.viewport_height}` : 'Unavailable'}</dd></div>
        <div><dt>Applied device scale factor</dt><dd>{context?.device_scale_factor ?? 'Unavailable'}</dd></div>
        <div><dt>Applied mobile mode</dt><dd>{booleanLabel(context?.mobile)}</dd></div>
        <div><dt>Applied touch mode</dt><dd>{booleanLabel(context?.has_touch)}</dd></div>
        <div><dt>Configured replay engine</dt><dd>{replayLabel}</dd></div>
        <div><dt>Physical Chrome/iPad qualification</dt><dd>Unverified</dd></div>
      </dl>
      {receipt?.observation_source === 'agent_reported' && <p>The controller did not independently observe the agent browser runtime.</p>}
      {!receipt && requested != null && <p>A declared preset describes intent; it does not supply recorded engine or applied context evidence.</p>}
    </details>
    {mismatch && <p className="recording-environment-warning" role="status">Recorded engine {recordingEngineLabel(receipt.engine)} differs from configured replay engine {recordingEngineLabel(replayEngine)}. Review replay configuration and verify this output.</p>}
  </section>
}
