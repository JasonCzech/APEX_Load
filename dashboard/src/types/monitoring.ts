/** Contracts for project-scoped infrastructure monitoring collectors. */
export interface MonitoringCollector {
  id: string
  project_id: string
  name: string
  description?: string
  health: string
  last_seen_at?: string
  allowed_metric_packs: string[]
  created_at: string
  updated_at: string
}

export interface MonitoringCollectorListResponse {
  collectors: MonitoringCollector[]
  count: number
  limit?: number
  offset?: number
}

export interface MonitoringCollectorTokenResponse {
  collector: MonitoringCollector
  token: string
}

export interface CollectorMetricPack {
  pack: string
  timestamp: string
  samples: Record<string, number>
}

export interface CollectorMetricSnapshot {
  collector_id: string
  project_id?: string
  health: string
  received_at?: string
  age_seconds?: number
  stale: boolean
  pending?: boolean
  packs: CollectorMetricPack[]
}
