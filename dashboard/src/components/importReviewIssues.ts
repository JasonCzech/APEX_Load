import type { ImportReviewSection } from '../types/scriptImports'
import type { ScriptActionDefinition, ScriptDefinition, SourceReview } from '../types/scripts'

export type ImportFinding = { code: string; action_path?: string; source_ref?: string; detail: string; gate?: string; severity?: string; title?: string; evidence?: string[]; source_mapping_id?: string; source_file?: string }
export type IssueCategory = 'values' | 'bodies' | 'credentials' | 'runtime' | 'review'
export const issueCategories: Record<IssueCategory, string> = {
  values: 'Missing values & correlations', bodies: 'Protected request bodies', credentials: 'Credentials', runtime: 'Transport & runtime', review: 'Review decisions',
}
export type ImportIssue = { id: string; title: string; category: IssueCategory; blocking: boolean; findings: ImportFinding[] }
export type ActionEntry = { action: ScriptActionDefinition; path: string }
export function importActionEntries(script: ScriptDefinition): ActionEntry[] {
  const entries: ActionEntry[] = []
  function walk(actions: ScriptActionDefinition[], prefix: string, depth: number) {
    if (depth > 64) return
    actions.forEach((action, index) => {
      if (!action || typeof action !== 'object') return
      const path = `${prefix}[${index}]`
      entries.push({ action, path })
      if (Array.isArray(action.params?.requests)) walk(action.params.requests as ScriptActionDefinition[], `${path}.params.requests`, depth + 1)
      if (action.control) {
        for (const key of ['steps', 'else', 'default'] as const) walk(action.control[key] || [], `${path}.control.${key}`, depth + 1)
        action.control.cases?.forEach((branch, i) => walk(branch.steps || [], `${path}.control.cases[${i}].steps`, depth + 1))
        action.control.branches?.forEach((branch, i) => walk(branch.steps || [], `${path}.control.branches[${i}].steps`, depth + 1))
      }
    })
  }
  for (const phase of ['init', 'actions', 'teardown'] as const) walk(script[phase] || [], phase, 0)
  return entries
}
function strings(value: unknown): string[] { return Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : [] }
export function draftImportFindings(script: ScriptDefinition): ImportFinding[] {
  return importActionEntries(script).flatMap(({ action, path }) => {
    const meta = action.metadata || {}
    const blockers = [...strings(meta.execution_blockers), ...(typeof meta.execution_blocker === 'string' ? [meta.execution_blocker] : [])]
    const reviews = strings(meta.review_reasons)
    if (!blockers.length && !reviews.length && (meta.review_required === true || meta.promotion_blocker === true)) reviews.push('import_review_required')
    return [...blockers.map(code => ({ code, gate: 'execution' })), ...reviews.map(code => ({ code, gate: 'promotion' }))].map(f => ({ ...f, action_path: path, source_ref: meta.source_file ? `${meta.source_file}:${meta.source_line || ''}` : '', detail: '' }))
  })
}
export function findingCategory(code: string): IssueCategory {
  if (/^(missing_producer|conditional_producer):/.test(code)) return 'values'
  if (code === 'restored_body_required') return 'bodies'
  if (/secret|credential|proxy_password/.test(code)) return 'credentials'
  if (/tls|ssl|runtime|allocation/.test(code)) return 'runtime'
  return 'review'
}
// Titles for the LoadRunner web parity review codes (pkg/convert/enterprisearchive,
// pkg/convert/requestimport). Unlisted codes fall back to their words.
const LOADRUNNER_PARITY_ISSUE_TITLES: Record<string, string> = {
  html_navigation_requires_page_context: 'Replace HTML-mode click with an explicit request',
  html_form_requires_page_context: 'Verify form submitted without its page',
  request_body_variant_unsupported: 'Rebuild unsupported request body',
  request_body_file_missing: 'Add the missing request body file',
  parameter_helper_unsupported: 'Review the parameter helper options',
  request_argument_unsupported: 'Review unsupported request attributes',
  request_argument_review: 'Review request side effects',
  registration_scope_narrowed: 'Review narrowed extraction scope',
  registration_filter_unreproduced: 'Review extraction filter APEX cannot apply',
  registration_dfe_unsupported: 'Apply data-format extensions before extraction',
  text_check_scope_unsupported: 'Review header text check',
  text_check_scope_narrowed: 'Review narrowed text check',
  text_check_unsupported: 'Review text check APEX cannot express',
  web_set_user_credentials_conflict: 'Reconcile web_set_user credentials',
  integrated_auth_requires_configuration: 'Configure NTLM or Kerberos credentials',
  recorded_body_truncated: 'Re-record or supply the full request body',
  async_callbacks_not_executed: 'Rebuild async callbacks that do not run',
  async_conversation_unsupported: 'Review async conversation APEX cannot run',
}

export function issueTitle(code: string): string {
  if (code.startsWith('missing_producer:')) return `Missing value: ${code.slice(17)}`
  if (code.startsWith('conditional_producer:')) return `Check producer order: ${code.slice(21)}`
  return ({ 'diagnostic:warnings': 'Review conversion warnings', 'diagnostic:credential_bindings': 'Configure unresolved credential bindings', 'diagnostic:validation': 'Resolve draft validation checks', 'diagnostic:execution': 'Review execution prerequisites', 'diagnostic:runtime_settings_parity': 'Review runtime behavior differences', 'diagnostic:runtime_settings_migration_readiness': 'Review runtime migration coverage', restored_body_required: 'Restore protected request body', encrypted_proxy_password: 'Replace encrypted proxy password', legacy_tls_replacement: 'Choose supported TLS settings', import_review_required: 'Review imported behavior', ...LOADRUNNER_PARITY_ISSUE_TITLES } as Record<string, string>)[code] || (code.startsWith('diagnostic:') ? 'Review import requirement' : code.replace(/_/g, ' '))
}
export function groupImportFindings(findings: ImportFinding[], script: ScriptDefinition): ImportIssue[] {
  const actions = new Map(importActionEntries(script).map(entry => [entry.path, entry.action]))
  const grouped = new Map<string, ImportIssue>()
  for (const finding of findings) {
    // The generic body review is evidence of the same required body restoration.
    if (finding.code === 'import_review_required' && findings.some(f => f.code === 'restored_body_required' && f.action_path === finding.action_path)) continue
    const category = findingCategory(finding.code)
    const path = finding.action_path || ''
    // A missing producer blocks only when the server gates it: replay sends most placeholders with a warning.
    const blocking = finding.gate === 'execution' || finding.severity === 'blocker' || /^(restored_body_required|encrypted_proxy_password|legacy_tls_replacement)$/.test(finding.code)
    const action = actions.get(path)
    // Keep branch scopes and parameter namespaces separate. Grouping never applies a bulk repair.
    const controlIndex = path.lastIndexOf('.control.')
    const scope = controlIndex < 0 ? 'script' : path.slice(0, path.lastIndexOf('['))
    const executable = JSON.stringify(action ? { target: action.target, body: action.body, headers: action.headers, params: action.params } : {})
    const variable = finding.code.split(':').slice(1).join(':')
    const escaped = variable.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const namespace = variable && new RegExp(`\\{\\{\\s*param\\.${escaped}\\s*\\}\\}`).test(executable) ? 'param' : 'var'
    const id = category === 'values' ? `${finding.code}:${scope}:${namespace}` : `${finding.code}:${path}`
    const issue = grouped.get(id) || { id, title: finding.title || issueTitle(finding.code), category, blocking, findings: [] }
    issue.blocking ||= blocking
    if (!issue.findings.some(f => f.code === finding.code && f.action_path === path)) issue.findings.push(finding)
    grouped.set(id, issue)
  }
  return [...grouped.values()].sort((a, b) => Number(b.blocking) - Number(a.blocking) || b.findings.length - a.findings.length || a.title.localeCompare(b.title))
}
export function issueRepairOptions(code: string): { value: string; label: string }[] {
  if (/^(missing_producer|conditional_producer):/.test(code)) return [
    { value: 'bind_variable', label: 'Bind existing value' }, { value: 'add_parameter', label: 'Add parameter' }, { value: 'add_extractor', label: 'Configure response extractor' },
  ]
  if (code === 'restored_body_required') return [{ value: 'restore_body', label: 'Supply restored request body' }]
  if (code === 'encrypted_proxy_password') return [{ value: 'replace_proxy_secret', label: 'Choose proxy secret binding' }]
  if (code === 'legacy_tls_replacement') return [{ value: 'select_tls', label: 'Select supported TLS' }]
  return []
}

// Keep concrete diagnostics and location identities intact when moving them into the queue.
export function reviewSectionFindings(sections: ImportReviewSection[], source?: SourceReview): ImportFinding[] {
  const parity = sections.find(section => section.key === 'runtime_settings_parity' && section.severity !== 'info')
  const coverage = sections.find(section => section.key === 'runtime_settings_migration_readiness' && section.severity !== 'blocker')
  const seenWarnings = new Set<string>()
  return sections.flatMap(section => {
    if (parity && coverage === section) return []
    if (section.severity === 'info' || section.key === 'import_fidelity') return []
    const warning = ['warnings', 'conversion_warnings'].includes(section.key)
    const items = warning && section.items?.length ? section.items : [section.summary]
    return items.filter(detail => {
      if (!warning) return true
      if (seenWarnings.has(detail)) return false
      seenWarnings.add(detail); return true
    }).map(detail => {
      const line = /(?:^|\s)line\s+(\d+)\s*:/i.exec(detail)
      const lineCandidates = line ? (source?.mappings || []).filter(mapping => mapping.start_line <= Number(line[1]) && (mapping.end_line || mapping.start_line) >= Number(line[1])) : []
      const functionCandidates = lineCandidates.filter(mapping => mapping.function && detail.includes(mapping.function))
      const candidates = functionCandidates.length ? functionCandidates : lineCandidates
      // A bare line number is ambiguous across files. Never navigate to an arbitrary first match.
      const files = new Set(candidates.map(mapping => mapping.source_file))
      const mapping = files.size === 1 ? [...candidates].sort((a, b) => ((a.end_line || a.start_line) - a.start_line) - ((b.end_line || b.start_line) - b.start_line))[0] : undefined
      const narrowerAssertion = /web_reg_find.*Search=ALL/i.test(detail)
      return {
        code: warning ? `diagnostic:warnings:${detail}` : `diagnostic:${section.key}`,
        title: narrowerAssertion ? 'Text check searches a narrower response scope' : warning ? detail.replace(/^line\s+\d+:\s*/i, '').slice(0, 120) : section.key.startsWith('runtime_') ? issueTitle(`diagnostic:${section.key}`) : section.title || issueTitle(`diagnostic:${section.key}`),
        detail, evidence: warning ? [] : [...(section.items || []), ...(section === parity && coverage ? [coverage.summary, ...(coverage.items || [])] : [])],
        gate: section.severity === 'blocker' ? 'execution' : 'promotion',
        source_ref: mapping ? `${mapping.source_file}:${line![1]}` : line ? `Line ${line[1]} · source file not identified` : undefined,
        source_file: mapping?.source_file, source_mapping_id: mapping?.id, action_path: mapping?.target_path,
      }
    })
  })
}

export function issueScope(issue: ImportIssue): string {
  const locations = issue.findings.filter(finding => finding.action_path || finding.source_ref)
  return locations.length ? `${locations.length} affected ${locations.length === 1 ? 'location' : 'locations'}` : 'Script-wide'
}

// Report field paths only: repair values may contain protected payloads or credentials.
export function changedRepairPaths(before: unknown, after: unknown): string[] {
  const paths: string[] = []
  const walk = (a: unknown, b: unknown, path: string, depth: number) => {
    if (Object.is(a, b) || paths.length >= 100) return
    if (depth >= 16 || a == null || b == null || typeof a !== 'object' || typeof b !== 'object' || Array.isArray(a) !== Array.isArray(b)) { paths.push(path || 'Script'); return }
    const left = a as Record<string, unknown>; const right = b as Record<string, unknown>
    for (const key of new Set([...Object.keys(left), ...Object.keys(right)])) {
      if (key === 'server_import_attestation_v1') continue
      walk(left[key], right[key], Array.isArray(a) ? `${path}[${key}]` : path ? `${path}.${key}` : key, depth + 1)
    }
  }
  walk(before, after, '', 0)
  return paths
}
