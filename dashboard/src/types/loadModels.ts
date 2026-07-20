/** One phase in a scenario's staged ramp/hold timeline. */
export interface LoadStageDraft {
  name?: string
  type: 'ramp_up' | 'hold' | 'ramp_down' | string
  duration: string
  target_vusers?: number
  target_tps?: number
}
