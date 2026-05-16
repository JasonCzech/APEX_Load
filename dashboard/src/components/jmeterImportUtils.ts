import { type ImportedAction, type ImportedExtractor, type ImportedScript, type ImportReviewSection, type JMeterImportResult as ImportResult, type SupportMatrix } from '../types/scriptImports'

export interface SupportMatrixSection {
  key: 'supported' | 'partial' | 'unsupported'
  title: string
  itemLabel: string
  items: string[]
  preview: string
}

export interface ImportExtractorSummary {
  scriptName: string
  actionName: string
  extractorName: string
  extractorType: string
  expression: string
}

function isObjectRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function normalizeReviewSections(value: unknown): ImportReviewSection[] {
  if (!Array.isArray(value)) {
    return []
  }
  return value
    .filter(isObjectRecord)
    .map(section => ({
      key: typeof section.key === 'string' ? section.key : '',
      title: typeof section.title === 'string' ? section.title : '',
      severity: typeof section.severity === 'string' ? section.severity : 'info',
      summary: typeof section.summary === 'string' ? section.summary : '',
      items: Array.isArray(section.items) ? section.items.filter((item): item is string => typeof item === 'string') : [],
    }))
    .filter(section => section.key && section.title)
}

function normalizeSharedImportFields(response: unknown): Pick<ImportResult, 'scripts' | 'script' | 'scenario_draft' | 'warnings' | 'unsupported_elements' | 'assumptions' | 'review_sections' | 'correlations'> {
  const payload = isObjectRecord(response) ? response : {}
  const script = payload.script as ImportedScript | undefined
  const scripts = Array.isArray(payload.scripts)
    ? payload.scripts as ImportedScript[]
    : (script ? [script] : [])

  return {
    scripts,
    script,
    scenario_draft: payload.scenario_draft as ImportResult['scenario_draft'] | undefined,
    warnings: Array.isArray(payload.warnings) ? payload.warnings as string[] : [],
    unsupported_elements: Array.isArray(payload.unsupported_elements) ? payload.unsupported_elements as string[] : [],
    assumptions: Array.isArray(payload.assumptions) ? payload.assumptions as string[] : [],
    review_sections: normalizeReviewSections(payload.review_sections),
    correlations: Array.isArray(payload.correlations) ? payload.correlations : [],
  }
}

export function normalizeImportResult(response: unknown): ImportResult {
  const payload = isObjectRecord(response) ? response : {}
  const statsPayload = isObjectRecord(payload.stats) ? payload.stats : {}
  const summaryPayload = isObjectRecord(payload.summary) ? payload.summary : {}

  return {
    ...normalizeSharedImportFields(response),
    stats: {
      thread_groups: 0,
      samplers: 0,
      converted_actions: 0,
      headers: 0,
      variables: 0,
      csv_sources: 0,
      assertions: 0,
      timers: 0,
      unsupported: 0,
      ...statsPayload,
    },
    summary: {
      thread_group_count: 0,
      sampler_count: 0,
      ...summaryPayload,
    },
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

export function summarizeImportExtractors(scripts: ImportedScript[]): ImportExtractorSummary[] {
  if (!Array.isArray(scripts) || scripts.length === 0) {
    return []
  }

  const summaries: ImportExtractorSummary[] = []
  for (const script of scripts) {
    const scriptName = typeof script?.name === 'string' && script.name ? script.name : 'Imported Script'
    const actions: ImportedAction[] = Array.isArray(script?.actions) ? script.actions : []
    for (const action of actions) {
      const actionName = typeof action?.name === 'string' && action.name ? action.name : 'Unnamed Action'
      const extractors: ImportedExtractor[] = Array.isArray(action?.extractors) ? action.extractors : []
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
