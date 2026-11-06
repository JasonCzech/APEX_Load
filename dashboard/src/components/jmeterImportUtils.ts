import { type ScriptActionDefinition, type ScriptDefinition, type ScriptExtractorDefinition } from '../types/scripts'
import { type JMeterScenarioDraft, type JMeterSupportMatrixEntry, type SupportMatrix } from '../types/scriptImports'
import { sourcePopulationFromScripts, sourcePopulationGroup, type ImportScenarioPopulation } from './scenarioAuthoringDraft'

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

/**
 * The thread-group load of a scenario draft, positional with the scripts it
 * would run. A save that leaves the draft out still lets the scenario created
 * later start from the source's threads, ramp-up and loops instead of one
 * virtual user. Groups match scripts by name, in order; a script the draft has
 * no group for (or a response with no draft) falls back to the load its
 * conversion preserved (metadata.import.preserved.source_population).
 */
export function jmeterScenarioPopulation(draft: JMeterScenarioDraft | null | undefined, scripts: ReadonlyArray<Pick<ScriptDefinition, 'name' | 'metadata'>>): ImportScenarioPopulation | undefined {
  if (!draft?.groups?.length) return sourcePopulationFromScripts(scripts)
  const used = new Set<number>()
  const groups = scripts.map(script => {
    const index = draft.groups.findIndex((group, candidate) => !used.has(candidate) && group.script_name === script.name)
    if (index < 0) return sourcePopulationGroup(script)
    used.add(index)
    const group = draft.groups[index]
    return { vusers: group.vusers, ramp_up: group.ramp_up, iterations: group.iterations, initialization: group.initialization }
  })
  if (groups.every(group => !group)) return undefined
  return {
    ...(draft.termination_mode === 'iterations' ? { termination_mode: 'iterations' as const } : {}),
    ...(draft.duration ? { duration: draft.duration } : {}),
    groups,
  }
}

/**
 * The JMeter Comments of a converted thread group
 * (metadata.import.preserved.source_comments) or of the Test Plan
 * (scenario_draft.metadata.source_comments): documentation only, shown as
 * read-only notes. '' when the source has none.
 */
export function jmeterSourceComments(owner: { metadata?: unknown } | null | undefined): string {
  const metadata = owner?.metadata as { source_comments?: unknown; import?: { preserved?: { source_comments?: unknown } } } | undefined
  const comment = metadata?.import?.preserved?.source_comments ?? metadata?.source_comments
  return typeof comment === 'string' ? comment.trim() : ''
}

/** The key a batch review's migrated secret name is looked up by: one credential of one script. */
export function batchSecretKey(scriptIndex: number, path: string): string {
  return `${scriptIndex}\u0000${path}`
}

const GENERATED_PARAMETER_TYPES = new Set(['counter', 'random_number', 'date_time'])

/**
 * Parameters the converter generated from JMeter config elements (counters,
 * random values, timestamps), for a response without
 * stats.generated_parameters. An older converter's variables count covers
 * User Defined Variables only, so these are counted separately. As the
 * converter counts them: a config element above the thread groups is attached
 * to every script it covers, so each one counts once per plan, by type, name
 * and source scope, not once per script.
 */
export function generatedParameterCount(scripts: readonly ScriptDefinition[]): number {
  const generated = new Set<string>()
  for (const script of scripts) {
    for (const parameter of script.parameters || []) {
      if (GENERATED_PARAMETER_TYPES.has(parameter.type)) generated.add(JSON.stringify([parameter.type, parameter.name, parameter.scope_id || '']))
    }
  }
  return generated.size
}
