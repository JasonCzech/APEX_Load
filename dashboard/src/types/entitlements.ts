/** Contracts for edition limits and protocol-bundle consumption. */
export interface EntitlementsProtocolBundle {
  id: string
  display_name: string
  protocols?: string[]
  default?: string
  governance?: string
}

export interface EntitlementsResponse {
  edition: string
  limits: { max_concurrent_tests: number }
  protocol_bundles: { count: number; bundles: EntitlementsProtocolBundle[] }
}

export interface EntitlementLimitView {
  key: string
  label: string
  scope: string
  used: number
  limit: number
  remaining: number
  utilization: number
  unlimited: boolean
}

export interface EntitlementBundleConsumption {
  id: string
  display_name: string
  protocols: string[]
  run_count: number
  total_vusers: number
  vuser_seconds: number
}

export interface ProjectEntitlementView {
  project_id: string
  name?: string
  limits: EntitlementLimitView[]
}

export interface EntitlementsUsageResponse {
  concurrent_tests: { used: number; limit: number }
  service_accounts: number
  projects: number
  limits?: EntitlementLimitView[]
  protocol_bundles?: EntitlementBundleConsumption[]
  by_project?: ProjectEntitlementView[]
}

export interface ProjectEntitlementsUsageResponse {
  project_id: string
  name?: string
  limits: EntitlementLimitView[]
  protocol_bundles: EntitlementBundleConsumption[]
}
