/** Historical recorder receipt; no field qualifies a physical device. */
export type RecordingEngine = 'chromium' | 'webkit' | 'firefox' | 'chrome'
export type RecordingObservationSource = 'local_recorder' | 'agent_reported'
export interface RecordingAppliedContext {
  viewport_width?: number
  viewport_height?: number
  device_scale_factor?: number
  mobile?: boolean
  has_touch?: boolean
}
export interface RecordingEnvironment {
  version: 1
  observation_source: RecordingObservationSource
  engine: RecordingEngine
  device_profile?: string
  profile_browser_family?: RecordingEngine
  emulated: boolean
  applied_context?: RecordingAppliedContext
  physical_device_qualification: 'unverified'
}
