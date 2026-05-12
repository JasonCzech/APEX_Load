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

export interface SupportMatrixSection {
  key: 'supported' | 'partial' | 'unsupported'
  title: string
  itemLabel: string
  items: string[]
  preview: string
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

export interface ImportExtractorSummary {
  scriptName: string
  actionName: string
  extractorName: string
  extractorType: string
  expression: string
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

export function summarizeSupportMatrix(matrix?: SupportMatrix | null): SupportMatrixSection[] {
  if (!matrix) {
    return []
  }

  const supported = Array.isArray(matrix.supported) ? matrix.supported : []
  const partial = Array.isArray(matrix.partial) ? matrix.partial : []
  const unsupported = Array.isArray(matrix.unsupported) ? matrix.unsupported : []

  return [
    {
      key: 'supported',
      title: 'Supported',
      itemLabel: 'constructs',
      items: supported,
      preview: supported.slice(0, 3).join(' • '),
    },
    {
      key: 'partial',
      title: 'Partial',
      itemLabel: 'behaviors',
      items: partial,
      preview: partial.slice(0, 2).join(' • '),
    },
    {
      key: 'unsupported',
      title: 'Unsupported',
      itemLabel: 'constructs',
      items: unsupported,
      preview: unsupported.slice(0, 2).join(' • '),
    },
  ]
}

export function summarizeImportExtractors(scripts: any[]): ImportExtractorSummary[] {
  if (!Array.isArray(scripts) || scripts.length === 0) {
    return []
  }

  const summaries: ImportExtractorSummary[] = []
  for (const script of scripts) {
    const scriptName = typeof script?.name === 'string' && script.name ? script.name : 'Imported Script'
    const actions = Array.isArray(script?.actions) ? script.actions : []
    for (const action of actions) {
      const actionName = typeof action?.name === 'string' && action.name ? action.name : 'Unnamed Action'
      const extractors = Array.isArray(action?.extractors) ? action.extractors : []
      for (const extractor of extractors) {
        if (!extractor?.name || !extractor?.type) {
          continue
        }
        summaries.push({
          scriptName,
          actionName,
          extractorName: extractor.name,
          extractorType: extractor.type,
          expression: typeof extractor.expression === 'string' ? extractor.expression : '',
        })
      }
    }
  }

  return summaries
}