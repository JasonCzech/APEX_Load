import type { RecordingAppliedContext, RecordingEngine, RecordingEnvironment } from '../types/recordingEnvironment'
import type { ScriptDefinition } from '../types/scripts'

const ENGINES: Record<RecordingEngine, string> = { chromium: 'Chromium', webkit: 'WebKit', firefox: 'Firefox', chrome: 'Chrome' }
const PRESETS: Record<string, { label: string; family: RecordingEngine }> = {
  desktop_1080p: { label: 'Desktop 1080p', family: 'chromium' },
  galaxy_s24: { label: 'Galaxy S24', family: 'chromium' },
  ipad_air: { label: 'iPad Air', family: 'webkit' },
  iphone_15_pro: { label: 'iPhone 15 Pro', family: 'webkit' },
  iphone_se: { label: 'iPhone SE', family: 'webkit' },
  pixel_8: { label: 'Pixel 8', family: 'chromium' },
}
const own = (value: object, key: string) => Object.prototype.hasOwnProperty.call(value, key)
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)
export const recordingEngine = (value: unknown): RecordingEngine | undefined => typeof value === 'string' && value.length <= 16 && own(ENGINES, value) ? value as RecordingEngine : undefined
export const recordingEngineLabel = (value: RecordingEngine) => ENGINES[value]
export const recordingPresetLabel = (value: unknown): string | undefined => typeof value === 'string' && value.length <= 64 && own(PRESETS, value) ? PRESETS[value].label : undefined

/** Fixed-schema reads only: no traversal, serialization or display of arbitrary claims. */
export function normalizeRecordingEnvironment(value: unknown, agentReported = false): RecordingEnvironment | null {
  if (!object(value) || value.version !== 1 || !recordingEngine(value.engine) || typeof value.emulated !== 'boolean' || value.physical_device_qualification !== 'unverified') return null
  if (value.observation_source !== 'local_recorder' && value.observation_source !== 'agent_reported') return null
  const receipt: RecordingEnvironment = { version: 1, observation_source: agentReported ? 'agent_reported' : value.observation_source, engine: recordingEngine(value.engine)!, emulated: value.emulated, physical_device_qualification: 'unverified' }
  if (own(value, 'device_profile')) {
    if (typeof value.device_profile !== 'string' || (value.device_profile !== '' && !recordingPresetLabel(value.device_profile))) return null
    if (value.device_profile) receipt.device_profile = value.device_profile
  }
  if (own(value, 'profile_browser_family')) {
    if (value.profile_browser_family !== '' && !recordingEngine(value.profile_browser_family)) return null
    if (value.profile_browser_family) receipt.profile_browser_family = recordingEngine(value.profile_browser_family)
  }
  if (!receipt.emulated && (receipt.device_profile || receipt.profile_browser_family)) return null
  if (receipt.device_profile && receipt.profile_browser_family && PRESETS[receipt.device_profile].family !== receipt.profile_browser_family) return null
  if (own(value, 'applied_context') && value.applied_context != null) {
    const input = value.applied_context
    if (!object(input)) return null
    const context: RecordingAppliedContext = {}
    const width = own(input, 'viewport_width'), height = own(input, 'viewport_height')
    if (width !== height) return null
    if (width) {
      if (typeof input.viewport_width !== 'number' || typeof input.viewport_height !== 'number' || !Number.isInteger(input.viewport_width) || !Number.isInteger(input.viewport_height) || input.viewport_width <= 0 || input.viewport_height <= 0 || input.viewport_width > 8192 || input.viewport_height > 8192 || input.viewport_width * input.viewport_height > 16 * 1024 * 1024) return null
      context.viewport_width = input.viewport_width
      context.viewport_height = input.viewport_height
    }
    if (own(input, 'device_scale_factor')) {
      if (typeof input.device_scale_factor !== 'number' || !Number.isFinite(input.device_scale_factor) || input.device_scale_factor <= 0 || input.device_scale_factor > 4) return null
      context.device_scale_factor = input.device_scale_factor
    }
    if (context.viewport_width !== undefined && context.viewport_height !== undefined && context.device_scale_factor !== undefined && context.viewport_width * context.viewport_height * context.device_scale_factor ** 2 > 32 * 1024 * 1024) return null
    for (const field of ['mobile', 'has_touch'] as const) {
      if (!own(input, field)) continue
      if (typeof input[field] !== 'boolean') return null
      context[field] = input[field]
    }
    if (Object.keys(context).length) receipt.applied_context = context
  }
  return receipt
}

export function recordingEnvironmentFingerprint(value: unknown, agentReported = false): string {
  const receipt = normalizeRecordingEnvironment(value, agentReported)
  return JSON.stringify(receipt ? ['receipt', receipt] : [value === undefined ? 'unavailable' : 'malformed'])
}

export function scriptRecordingEnvironment(script?: ScriptDefinition): unknown {
  return script?.metadata?.import?.preserved?.recording_environment
}

export function legacyRecordingPreset(script?: ScriptDefinition): unknown {
  return script?.metadata?.import?.preserved?.device_profile ?? script?.config?.browser?.device_profile
}
