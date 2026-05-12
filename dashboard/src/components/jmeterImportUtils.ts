export interface ConvertStats {
  thread_groups: number
  samplers: number
  converted_actions: number
  headers: number
  variables: number
  csv_sources: number
  assertions: number
  timers: number
  unsupported: number
}

export interface SupportMatrix {
  supported: string[]
  partial: string[]
  unsupported: string[]
  notes?: string[]
}

export interface ImportResult {
  scripts: any[]
  script?: any
  scenario_draft?: any
  warnings: string[]
  unsupported_elements: string[]
  assumptions: string[]
  stats: ConvertStats
  summary: any
}

export function normalizeImportResult(response: any): ImportResult {
  return {
    scripts: Array.isArray(response?.scripts) ? response.scripts : [],
    script: response?.script,
    scenario_draft: response?.scenario_draft,
    warnings: Array.isArray(response?.warnings) ? response.warnings : [],
    unsupported_elements: Array.isArray(response?.unsupported_elements) ? response.unsupported_elements : [],
    assumptions: Array.isArray(response?.assumptions) ? response.assumptions : [],
    stats: response?.stats || {
      thread_groups: 0,
      samplers: 0,
      converted_actions: 0,
      headers: 0,
      variables: 0,
      csv_sources: 0,
      assertions: 0,
      timers: 0,
      unsupported: 0,
    },
    summary: response?.summary || {},
  }
}