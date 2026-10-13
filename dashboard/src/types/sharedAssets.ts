/** One scenario's binding of a data pool or project live table. */
export interface SharedAssetScenarioUse {
  id: string
  name: string
  alias: string
  archived?: boolean
}

/** One script that uses a shared asset. */
export interface SharedAssetScriptUse {
  id: string
  name: string
  archived?: boolean
}

/** Where a correlation rule set was learned, and the scripts it was applied to (copies). */
export interface SharedAssetRulesetUse {
  learned_from?: SharedAssetScriptUse
  applied_to: SharedAssetScriptUse[]
}

/** The kinds a usage read can ask for (?include=). */
export type SharedAssetUsageKind = 'data_pools' | 'live_tables' | 'object_repositories' | 'correlation_rulesets'

/**
 * What uses each of a project's shared assets, keyed by asset id
 * (GET /projects/{id}/shared-asset-usage). Only the kinds asked for are present.
 */
export interface SharedAssetUsage {
  project_id: string
  data_pools?: Record<string, SharedAssetScenarioUse[]>
  live_tables?: Record<string, SharedAssetScenarioUse[]>
  object_repositories?: Record<string, SharedAssetScriptUse[]>
  correlation_rulesets?: Record<string, SharedAssetRulesetUse>
}
