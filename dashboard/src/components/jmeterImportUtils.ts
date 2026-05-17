import { type ScriptActionDefinition, type ScriptDefinition, type ScriptExtractorDefinition } from '../types/scripts'
import { type SupportMatrix } from '../types/scriptImports'

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
