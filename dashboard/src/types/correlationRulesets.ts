/** Contracts for persistent, reusable correlation rules. */
export interface CorrelationRule {
  name: string
  rule_id?: string
  source: string
  extract_type: string
  expression: string
  confidence?: number
}

export interface CorrelationRuleset {
  id: string
  project_id?: string
  name: string
  description?: string
  source?: string
  rules: CorrelationRule[]
  created_at: string
  updated_at: string
}

export interface CorrelationRulesetListResponse {
  correlation_rulesets: CorrelationRuleset[]
  count: number
  limit?: number
  offset?: number
}

export interface CorrelationRulesetApplyResponse {
  ruleset_id: string
  script_id: string
  matched_rules: number
  applied: boolean
  preview_token: string
  diffs?: Array<{ kind: string; path?: string }>
}
