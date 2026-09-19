import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'

export type ImportFinding = { code: string; action_path?: string; source_ref?: string; detail: string; gate?: string; severity?: string }
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
export function issueTitle(code: string): string {
  if (code.startsWith('missing_producer:')) return `Missing value: ${code.slice(17)}`
  if (code.startsWith('conditional_producer:')) return `Check producer order: ${code.slice(21)}`
  return ({ 'diagnostic:credential_bindings': 'Configure unresolved credential bindings', 'diagnostic:validation': 'Resolve draft validation checks', 'diagnostic:execution': 'Review execution prerequisites', 'diagnostic:runtime_settings_parity': 'Review runtime behavior differences', 'diagnostic:runtime_settings_migration_readiness': 'Review runtime migration coverage', restored_body_required: 'Restore protected request body', encrypted_proxy_password: 'Replace encrypted proxy password', legacy_tls_replacement: 'Choose supported TLS settings', import_review_required: 'Review imported behavior' } as Record<string, string>)[code] || code.replace(/_/g, ' ')
}
export function groupImportFindings(findings: ImportFinding[], script: ScriptDefinition): ImportIssue[] {
  const actions = new Map(importActionEntries(script).map(entry => [entry.path, entry.action]))
  const grouped = new Map<string, ImportIssue>()
  for (const finding of findings) {
    // The generic body review is evidence of the same required body restoration.
    if (finding.code === 'import_review_required' && findings.some(f => f.code === 'restored_body_required' && f.action_path === finding.action_path)) continue
    const category = findingCategory(finding.code)
    const path = finding.action_path || ''
    const blocking = finding.gate === 'execution' || finding.severity === 'blocker' || /^(missing_producer:|restored_body_required$|encrypted_proxy_password$|legacy_tls_replacement$)/.test(finding.code)
    const action = actions.get(path)
    // Keep branch scopes and parameter namespaces separate. Grouping never applies a bulk repair.
    const controlIndex = path.lastIndexOf('.control.')
    const scope = controlIndex < 0 ? 'script' : path.slice(0, path.lastIndexOf('['))
    const executable = JSON.stringify(action ? { target: action.target, body: action.body, headers: action.headers, params: action.params } : {})
    const variable = finding.code.split(':').slice(1).join(':')
    const escaped = variable.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const namespace = variable && new RegExp(`\\{\\{\\s*param\\.${escaped}\\s*\\}\\}`).test(executable) ? 'param' : 'var'
    const id = category === 'values' ? `${finding.code}:${scope}:${namespace}` : `${finding.code}:${path}`
    const issue = grouped.get(id) || { id, title: issueTitle(finding.code), category, blocking, findings: [] }
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
