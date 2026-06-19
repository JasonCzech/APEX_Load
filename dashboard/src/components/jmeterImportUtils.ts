import { type ScriptActionDefinition, type ScriptDefinition, type ScriptExtractorDefinition } from '../types/scripts'
import { type JMeterSupportMatrixEntry, type SupportMatrix } from '../types/scriptImports'

export interface SupportMatrixSection {
  key: 'supported' | 'partial' | 'unsupported'
  title: string
  itemLabel: string
  items: string[]
  preview: string
}

export type CoverageCategoryKey = 'mapped' | 'review_required' | 'adapter_required' | 'unsupported'

export interface CoverageCategorySection {
  key: CoverageCategoryKey
  title: string
  hint: string
  items: JMeterSupportMatrixEntry[]
  preview: string
}

const COVERAGE_CATEGORY_ORDER: ReadonlyArray<{ key: CoverageCategoryKey; title: string; hint: string }> = [
  { key: 'mapped', title: 'Mapped', hint: 'Converted into executable APEX Load actions.' },
  { key: 'review_required', title: 'Review required', hint: 'Imported but needs manual review before it behaves correctly.' },
  { key: 'adapter_required', title: 'Adapter required', hint: 'Has a native equivalent, but needs an operator-supplied adapter or provider before running.' },
  { key: 'unsupported', title: 'Not supported', hint: 'No native conversion path; preserved for reference.' },
]

// coverageCategoryForExecutionStatus maps a structured entry's execution_status
// onto the four reporting categories surfaced to the user.
function coverageCategoryForExecutionStatus(executionStatus: string): CoverageCategoryKey {
  switch (executionStatus) {
    case 'executable':
      return 'mapped'
    case 'review_required':
      return 'review_required'
    case 'adapter_required':
      return 'adapter_required'
    default:
      return 'unsupported'
  }
}

// summarizeSupportMatrixEntries groups structured support-matrix entries into the
// mapped / review_required / adapter_required / unsupported coverage categories.
// Empty categories are omitted so the UI only renders what is present.
export function summarizeSupportMatrixEntries(entries?: JMeterSupportMatrixEntry[] | null): CoverageCategorySection[] {
  if (!Array.isArray(entries) || entries.length === 0) {
    return []
  }
  const grouped = new Map<CoverageCategoryKey, JMeterSupportMatrixEntry[]>()
  for (const entry of entries) {
    const key = coverageCategoryForExecutionStatus(entry.execution_status)
    const bucket = grouped.get(key) ?? []
    bucket.push(entry)
    grouped.set(key, bucket)
  }
  const sections: CoverageCategorySection[] = []
  for (const category of COVERAGE_CATEGORY_ORDER) {
    const items = grouped.get(category.key) ?? []
    if (items.length === 0) continue
    sections.push({
      key: category.key,
      title: category.title,
      hint: category.hint,
      items,
      preview: items.slice(0, 3).map(item => item.construct).join(' • '),
    })
  }
  return sections
}

export interface ImportExtractorSummary {
  scriptName: string
  actionName: string
  extractorName: string
  extractorType: string
  expression: string
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

export function summarizeImportExtractors(scripts: ScriptDefinition[]): ImportExtractorSummary[] {
  if (!Array.isArray(scripts) || scripts.length === 0) {
    return []
  }

  const summaries: ImportExtractorSummary[] = []
  for (const script of scripts) {
    const scriptName = typeof script?.name === 'string' && script.name ? script.name : 'Imported Script'
    const actions: ScriptActionDefinition[] = Array.isArray(script?.actions) ? script.actions : []
    for (const action of actions) {
      const actionName = typeof action?.name === 'string' && action.name ? action.name : 'Unnamed Action'
      const extractors: ScriptExtractorDefinition[] = Array.isArray(action?.extractors) ? action.extractors : []
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
