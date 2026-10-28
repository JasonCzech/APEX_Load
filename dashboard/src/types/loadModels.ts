import type { CapacityAdvisory } from './capacity'
import type { AssignmentRecommendation } from './testConfiguration'

/** One phase in a scenario's staged ramp/hold timeline. */
export interface LoadStageDraft {
  name?: string
  type: 'ramp_up' | 'hold' | 'ramp_down' | string
  duration: string
  target_vusers?: number
  target_tps?: number
}

export interface LoadModelValidationItem {
  dimension: string
  group?: string
  status: 'ready' | 'needs_review' | 'blocked' | string
  summary: string
  required_rows?: number
  available_rows?: number
  target_vusers?: number
  target_tps?: number
}

export type ScenarioValidationContextComponent = 'scripts' | 'capabilities' | 'policy' | 'approvals' | 'secret_references'

export interface ScenarioValidationContext {
  version: number
  revision: string
  components: Record<ScenarioValidationContextComponent, string>
}

export interface LoadModelValidationReport {
  status: 'ready' | 'needs_review' | 'blocked' | string
  status_counts: Record<string, number>
  items: LoadModelValidationItem[]
  total_vusers: number
  peak_target_tps?: number
  capacity_advisory?: CapacityAdvisory
  assignment_recommendation?: AssignmentRecommendation
  validation_context?: ScenarioValidationContext
}
