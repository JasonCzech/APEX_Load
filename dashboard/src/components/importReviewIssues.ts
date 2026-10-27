import type { ImportReviewSection } from '../types/scriptImports'
import type { ReplayPreflight, ScriptActionDefinition, ScriptDefinition, SourceReview } from '../types/scripts'
import { countLabel } from '../utils/numberFormat'
import { replayBlockingKinds } from './replayPreflightUtils'
import { analyzeVariableReadiness } from './variableFlow'

export type ImportFinding = { code: string; action_path?: string; source_ref?: string; detail: string; gate?: string; severity?: string; title?: string; evidence?: string[]; source_mapping_id?: string; source_file?: string }
export type IssueCategory = 'values' | 'bodies' | 'credentials' | 'runtime' | 'review'
export const issueCategories: Record<IssueCategory, string> = {
  values: 'Missing values & correlations', bodies: 'Protected request bodies', credentials: 'Credentials', runtime: 'Transport & runtime', review: 'Review decisions',
}
// One severity scheme for every import count. A blocker refuses replay; a
// warning lets replay run but it may send a placeholder or need credentials;
// a note is a review decision with no effect on replay (converter warnings,
// runtime differences, promotion-only review codes).
export type ImportIssueSeverity = 'blocker' | 'warning' | 'note'
// `blocking` is severity === 'blocker'.
export type ImportIssue = { id: string; title: string; category: IssueCategory; severity: ImportIssueSeverity; blocking: boolean; findings: ImportFinding[] }
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
function sortedCodes(values: string[]): string[] { return [...new Set(values.map(value => value.trim()).filter(Boolean))].sort() }
// One step's own gates at path (`init[0]`, `actions[2].params.requests[1]`),
// read as the server's FindingsFromScript reads them
// (pkg/convert/importreview/review.go).
export function actionImportFindings(action: ScriptActionDefinition, path: string): ImportFinding[] {
  const meta = action.metadata || {}
  const review = meta.import_review && typeof meta.import_review === 'object' && !Array.isArray(meta.import_review) ? meta.import_review as Record<string, unknown> : {}
  const located = meta.source_file ? `${meta.source_file}:${meta.source_line || ''}` : ''
  const blockers = sortedCodes([...strings(meta.execution_blockers), ...(typeof meta.execution_blocker === 'string' ? [meta.execution_blocker] : [])])
  const reviews = sortedCodes([...strings(meta.review_reasons), ...(typeof meta.review_gate === 'string' ? [meta.review_gate] : [])])
  if (!reviews.length && (meta.review_required === true || meta.promotion_blocker === true)) reviews.push('import_review_required')
  return [
    ...blockers.map(code => ({ code, gate: 'execution', source_ref: strings(review.source_refs)[0] || located })),
    ...reviews.map(code => ({ code, gate: 'promotion', source_ref: located })),
  ].map(f => ({ ...f, action_path: path, detail: f.code === 'request_body_xml_json_content_type_mismatch' ? "The request body is XML, but the Content-Type header declares JSON. Update this request's Content-Type header to the XML media type expected by the service, then re-import and validate." : '' }))
}
// The draft's own gates, so the list before an inspection answers is the
// list the inspection returns.
export function draftImportFindings(script: ScriptDefinition): ImportFinding[] {
  return importActionEntries(script).flatMap(({ action, path }) => actionImportFindings(action, path))
}
// A protocol configuration reference (base URL, auth, default headers) to a
// value no step can supply: `config_reference:<name>`. Import review never
// stamps it on a step; it comes from replay readiness (importReviewFindings).
const CONFIG_CODE = 'config_reference:'
/**
 * Whether an issue is a configuration reference. It leads with its
 * configuration finding, and a step's finding for the same value folded into
 * it is evidence only: no step repair can supply configuration.
 */
export const isConfigReferenceIssue = (issue: Pick<ImportIssue, 'id'>) => issue.id.startsWith(CONFIG_CODE)
/** What a configuration reference means for replay, worded once for the import review and the editor. */
export function configReferenceText(name: string): string {
  return `Protocol configuration references ${name}. Configuration is applied before any step runs, so replay refuses it. Replace the reference with a literal configuration value.`
}
export function findingCategory(code: string): IssueCategory {
  // Configuration is a runtime setting, whatever the value is called.
  if (code.startsWith(CONFIG_CODE)) return 'runtime'
  if (/^(missing_producer|conditional_producer):/.test(code)) return 'values'
  if (code === 'restored_body_required') return 'bodies'
  if (/secret|credential|proxy_password/.test(code)) return 'credentials'
  if (/tls|ssl|runtime|allocation|http3|http_version/.test(code)) return 'runtime'
  return 'review'
}
// Titles for the LoadRunner web parity review codes (pkg/convert/enterprisearchive,
// pkg/convert/requestimport). Unlisted codes fall back to their words.
const LOADRUNNER_PARITY_ISSUE_TITLES: Record<string, string> = {
  html_navigation_requires_page_context: 'Replace HTML-mode click with an explicit request',
  html_form_requires_page_context: 'Verify form submitted without its page',
  request_body_variant_unsupported: 'Rebuild unsupported request body',
  request_body_file_missing: 'Add the missing request body file',
  request_body_xml_json_content_type_mismatch: 'Body is XML but Content-Type declares JSON',
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
  websocket_callbacks_not_executed: 'Rebuild WebSocket callbacks that do not run',
  websocket_attribute_unsupported: 'Review WebSocket connect attributes',
  websocket_step_unsupported: 'Review WebSocket step APEX cannot run',
  recorded_async_conversation: 'Review recorded background conversation',
  javascript_web_script_unsupported: 'Fix the JavaScript or re-record the script in C',
}
// HTTP/3 import review codes (pkg/convert/requestimport, pkg/convert/sample).
const HTTP3_ISSUE_TITLES: Record<string, string> = {
  curl_http3_race_not_modeled: 'Choose an HTTP version for curl --http3',
  http3_requires_https: 'Use an https:// URL for HTTP/3',
  http3_with_proxy: 'Remove the proxy or choose another HTTP version',
  http3_requires_tls13: 'Allow TLS 1.3 or choose another HTTP version',
  http3_draft_token: 'Choose final HTTP/3 for a draft capture',
  raw_http_version_review_required: 'Choose the HTTP version for a raw request',
}

export function issueTitle(code: string): string {
  if (code.startsWith('missing_producer:')) return `Missing value: ${code.slice(17)}`
  if (code.startsWith('conditional_producer:')) return `Check producer order: ${code.slice(21)}`
  if (code.startsWith(CONFIG_CODE)) return `Replace configuration reference: ${code.slice(CONFIG_CODE.length)}`
  return ({ 'diagnostic:warnings': 'Review conversion warnings', 'diagnostic:credential_bindings': 'Configure unresolved credential bindings', 'diagnostic:validation': 'Resolve draft validation checks', 'diagnostic:execution': 'Review execution prerequisites', 'diagnostic:runtime_settings_parity': 'Review runtime behavior differences', 'diagnostic:runtime_settings_migration_readiness': 'Review runtime migration coverage', restored_body_required: 'Restore protected request body', encrypted_proxy_password: 'Replace encrypted proxy password', legacy_tls_replacement: 'Choose supported TLS settings', import_review_required: 'Review imported behavior', ...LOADRUNNER_PARITY_ISSUE_TITLES, ...HTTP3_ISSUE_TITLES } as Record<string, string>)[code] || (code.startsWith('diagnostic:') ? 'Review import requirement' : code.replace(/_/g, ' '))
}
// Converter warnings are raw messages. Known ones get a title saying what to
// do; the rest keep their message, cut at a word. The full message stays in
// the finding's detail either way.
const WARNING_ACTION_TITLES: Array<[RegExp, string]> = [
  [/\bweb_set_sockets_option\b[^;]*\b(?:SSL|TLS)/i, 'Choose supported TLS settings'],
  [/^(?:Legacy SSL version|Automatic SSL\/TLS selection).*replaced with TLS 1\.3/i, 'Review TLS migration'],
  [/\bweb_reg_save_param\w*\b/i, 'Review the response extraction'],
  [/\bweb_reg_find\b/i, 'Review the text check'],
]
const WARNING_TITLE_LENGTH = 100
export function warningIssueTitle(detail: string): string {
  const message = detail.replace(/^line\s+\d+:\s*/i, '').trim()
  for (const [pattern, title] of WARNING_ACTION_TITLES) if (pattern.test(message)) return title
  if (message.length <= WARNING_TITLE_LENGTH) return message
  const cut = message.slice(0, WARNING_TITLE_LENGTH)
  const space = cut.lastIndexOf(' ')
  return `${(space > WARNING_TITLE_LENGTH / 2 ? cut.slice(0, space) : cut).replace(/[\s,;:.-]+$/, '')}…`
}
// Where an issue applies, for its title: a source location, else the action.
function findingLocation(finding: ImportFinding, actions: Map<string, ScriptActionDefinition>): string {
  const ref = (finding.source_ref || '').replace(/:$/, '')
  const unmappedLine = /^Line (\d+) · /.exec(ref)
  if (unmappedLine) return `line ${unmappedLine[1]}`
  return ref || (finding.action_path && actions.get(finding.action_path)?.name) || ''
}
const WARNING_CODE = 'diagnostic:warnings:'
// A converter warning as written, so the line a section item carries does not
// keep it from matching the gate that covers it.
const warningKey = (detail: string) => detail.replace(/^line\s+\d+:\s*/i, '').trim().replace(/\s+/g, ' ')
// Converter warnings a located gate already reports. The converter stamps them
// on the gate action as metadata.covered_warnings; the metadata survives a
// repair that clears the gate, so the warning stays covered afterwards too.
export function coveredWarnings(script: ScriptDefinition): Set<string> {
  const covered = new Set<string>()
  for (const { action } of importActionEntries(script)) for (const warning of strings(action.metadata?.covered_warnings)) covered.add(warningKey(warning))
  return covered
}
const ARCHIVE_CODE = 'diagnostic:archive:'
function issueSeverity(issue: Pick<ImportIssue, 'blocking' | 'category' | 'findings'>): ImportIssueSeverity {
  if (issue.blocking) return 'blocker'
  // An archive-level fidelity blocker holds promotion and load-test launch,
  // never a replay: a warning, as the editor lists the same report entry.
  if (issue.findings[0]?.code.startsWith(ARCHIVE_CODE)) return 'warning'
  // Converter warnings and review sections are notes whatever their text names.
  if (issue.findings[0]?.code.startsWith('diagnostic:')) return 'note'
  return issue.category === 'values' || issue.category === 'credentials' ? 'warning' : 'note'
}
export function groupImportFindings(findings: ImportFinding[], script: ScriptDefinition, options: { coveredWarnings?: ReadonlySet<string> } = {}): ImportIssue[] {
  const actions = new Map(importActionEntries(script).map(entry => [entry.path, entry.action]))
  const covered = options.coveredWarnings ?? coveredWarnings(script)
  const grouped = new Map<string, ImportIssue>()
  // A configuration reference blocks its value before any step runs, so the
  // value is one problem, as the editor lists one row per blocking value: a
  // step's own finding for the same value is part of it, not a second one.
  const configNames = new Set(findings.flatMap(finding => finding.code.startsWith(CONFIG_CODE) ? [finding.code.slice(CONFIG_CODE.length)] : []))
  for (const finding of findings) {
    // The generic body review is evidence of the same required body restoration.
    if (finding.code === 'import_review_required' && findings.some(f => f.code === 'restored_body_required' && f.action_path === finding.action_path)) continue
    // A converter warning a located gate covers is that gate's finding, not a second note.
    if (finding.code.startsWith(WARNING_CODE) && covered.has(warningKey(finding.detail || finding.code.slice(WARNING_CODE.length)))) continue
    const category = findingCategory(finding.code)
    const path = finding.action_path || ''
    // A missing producer blocks only when the server gates it: replay sends most placeholders with a warning.
    const blocking = finding.gate === 'execution' || finding.severity === 'blocker' || /^(restored_body_required|encrypted_proxy_password|legacy_tls_replacement)$/.test(finding.code)
    const action = actions.get(path)
    // Keep branch scopes and parameter namespaces separate. Grouping never applies a bulk repair.
    // A value the server gates is one problem script-wide, wherever its
    // consumers sit, as the editor and its start gate count it: each branch
    // consumer is one of its occurrences.
    const controlIndex = path.lastIndexOf('.control.')
    const scope = controlIndex < 0 || (category === 'values' && blocking) ? 'script' : path.slice(0, path.lastIndexOf('['))
    const executable = JSON.stringify(action ? { target: action.target, body: action.body, headers: action.headers, params: action.params } : {})
    const variable = finding.code.split(':').slice(1).join(':')
    const escaped = variable.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const namespace = variable && new RegExp(`\\{\\{\\s*param\\.${escaped}\\s*\\}\\}`).test(executable) ? 'param' : 'var'
    const config = finding.code.startsWith(CONFIG_CODE) || (category === 'values' && configNames.has(variable))
    const id = config ? `${CONFIG_CODE}${variable}` : category === 'values' ? `${finding.code}:${scope}:${namespace}` : `${finding.code}:${path}`
    const issue = grouped.get(id) || (config
      ? { id, title: issueTitle(id), category: 'runtime', severity: 'note', blocking, findings: [] }
      : { id, title: finding.title || issueTitle(finding.code), category, severity: 'note', blocking, findings: [] })
    issue.blocking ||= blocking
    // The configuration finding leads its issue, so the issue reads (and
    // offers no step repair) as the configuration change it needs.
    if (finding.code.startsWith(CONFIG_CODE)) issue.findings.unshift(finding)
    else if (!issue.findings.some(f => f.code === finding.code && f.action_path === path)) issue.findings.push(finding)
    grouped.set(id, issue)
  }
  // "<what to do> — <where>", so issues with the same fix at different places
  // read apart. Issues spanning several places show their count instead.
  for (const issue of grouped.values()) {
    issue.severity = issueSeverity(issue)
    const locations = new Set(issue.findings.map(finding => findingLocation(finding, actions)).filter(Boolean))
    // A configuration reference is script-wide, wherever a step also uses the value.
    if (locations.size === 1 && !issue.id.startsWith(CONFIG_CODE)) issue.title = `${issue.title} — ${[...locations][0]}`
  }
  return [...grouped.values()].sort((a, b) => Number(b.blocking) - Number(a.blocking) || b.findings.length - a.findings.length || a.title.localeCompare(b.title))
}
// What a finding means for replay and how it is resolved, worded once for the
// import review and the script editor. A code with nothing specific to say
// reads as its detail, '' without one.
export function repairImpactText(code: string, detail = ''): string {
  if (code.startsWith('diagnostic:') && /^(?:Legacy SSL version|Automatic SSL\/TLS selection).*replaced with TLS 1\.3/i.test(detail)) return 'The source SSL/TLS selection was imported using a TLS 1.3 default. Review the current draft range below. Replay requires an endpoint compatible with that range; the original warning records the import-time migration.'
  const category = findingCategory(code)
  if (category === 'values') return code.startsWith('conditional_') ? 'A possible producer exists, but its order or execution path needs review. It must run before the selected request.' : 'This value has no reachable producer. Bind an existing value, add test data, or extract it from a preceding response.'
  if (category === 'bodies') return 'The original payload could not be restored. Supply the body below; previews keep its contents protected.'
  if (code === 'legacy_tls_replacement') return 'The source uses legacy TLS behavior. Choose a supported replacement and preview the migration.'
  if (/web_reg_find.*Search=ALL/i.test(detail)) return 'The converted check can miss text outside the main response body. Verify the intended assertion before replay.'
  if (category === 'runtime' && code.startsWith('diagnostic:')) return 'Some imported settings need an explicit choice or have no editable equivalent. Review their source values and replay consequences below.'
  return detail
}
export function isTLSReviewFinding(finding: ImportFinding | undefined): boolean {
  return finding?.code === 'legacy_tls_replacement' || (!!finding?.code.startsWith('diagnostic:') && (/\bweb_set_sockets_option\b[^;]*\b(?:SSL|TLS)/i.test(finding.detail) || /^(?:Legacy SSL version|Automatic SSL\/TLS selection).*replaced with TLS 1\.3/i.test(finding.detail)))
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
// The repairs a saved script accepts (pkg/api script_repairs.go): restoring a
// protected body is import-only, so a saved script imports its source again.
export function storedRepairOptions(code: string): { value: string; label: string }[] {
  return issueRepairOptions(code).filter(option => option.value !== 'restore_body')
}

// One guided repair as both repair routes take it.
export type RepairOperation = { type: string; [key: string]: unknown }
// What the repair form collects: the chosen operation and its value.
export type RepairFields = { kind: string; value: string; responsePath: string; extractorType: string }
// The operation for kind at finding, built the same way for the import
// review and the script editor. A dependency repair names the value its
// finding lacks; TLS defaults to 1.2 up to 1.3.
export function buildRepairOperation(kind: string, finding: Pick<ImportFinding, 'code'> | undefined, fields: Omit<RepairFields, 'kind'>): RepairOperation {
  const variable = finding?.code.split(':').slice(1).join(':') || ''
  const { value, responsePath, extractorType } = fields
  switch (kind) {
    case 'restore_body': return { type: kind, body: value }
    case 'replace_proxy_secret': return { type: kind, secret_name: value }
    case 'bind_variable': return { type: kind, producer: value }
    case 'add_parameter': return { type: kind, parameter: { name: variable, type: 'list', values: value.split('\n'), update: 'once', selection: 'sequential' } }
    case 'add_extractor': return { type: kind, response_path: responsePath, extractor: { name: variable, type: extractorType, source: 'body', expression: value, on_miss: 'fail' } }
    default: return { type: 'select_tls', tls_min: value || '1.2', tls_max: '1.3' }
  }
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
        title: narrowerAssertion ? 'Text check searches a narrower response scope' : warning ? warningIssueTitle(detail) : section.key.startsWith('runtime_') ? issueTitle(`diagnostic:${section.key}`) : section.title || issueTitle(`diagnostic:${section.key}`),
        detail, evidence: warning ? [] : [...(section.items || []), ...(section === parity && coverage ? [coverage.summary, ...(coverage.items || [])] : [])],
        gate: section.severity === 'blocker' ? 'execution' : 'promotion',
        source_ref: mapping ? `${mapping.source_file}:${line![1]}` : line ? `Line ${line[1]} · source file not identified` : undefined,
        source_file: mapping?.source_file, source_mapping_id: mapping?.id, action_path: mapping?.target_path,
      }
    })
  })
}

// Review sections the issue queue lists, with or without an inspection
// (pkg/api handlers_import_enterprise.go builds them; conversion_warnings is
// the older warnings key). Detected protocol, review artifacts, preserved
// metadata, browser assumptions and the vendor guidance packs stay in the
// converter assessments only.
export const IMPORT_NOTE_SECTIONS: ReadonlySet<string> = new Set([
  'warnings', 'conversion_warnings', 'unsupported_elements', 'runtime_settings_parity', 'runtime_settings_migration_readiness',
  'salesforce_review', 'mulesoft_review', 'agentforce_review',
])
// Whether an archive fidelity blocker restates a finding listed already, given
// the codes of the findings listed: unresolved credentials are the bindings
// finding, a step gate ("<code> at <path>", or the bare code a repair writes)
// is its action finding, and the lr_unmask sentence is the required body
// restoration. The import review and the script editor leave those out.
export function fidelityBlockerCoveredByGate(detail: string, codes: ReadonlySet<string>): boolean {
  return detail === 'credential_bindings_required'
    || [...codes].some(code => detail.includes(code))
    || (codes.has('restored_body_required') && /lr_unmask|secret.gated/i.test(detail))
}
export type ImportReadiness = { replay_ready: boolean; promotion_ready?: boolean; blocking_codes?: string[]; validation_issues?: string[]; unresolved_bindings?: number }
export type ImportReviewInput = {
  script: ScriptDefinition
  // The protected candidate's inspection, only when it describes this script.
  inspection?: { findings: ImportFinding[]; readiness: ImportReadiness } | null
  preflight?: Pick<ReplayPreflight, 'valid' | 'checks' | 'missing_secrets' | 'blocking_variables' | 'param_blocking_variables' | 'config_blocking_variables'> | null
  fidelityBlockers?: readonly string[]
  reviewSections?: readonly ImportReviewSection[]
  warnings?: readonly string[]
  sourceReview?: SourceReview
  // The protocol or the runtime migration policy refuses executable replay.
  executionGated?: boolean
}
// Every finding the import review lists for one script. The policy is the
// same whether or not the inspection has answered: the action findings come
// from the inspection or, until then, from the draft's own gates (the same
// list), and every other finding from the archive, the review sections and
// the readiness the inspection or else the stamped preflight reports.
// Protocol configuration references come from the preflight's blocking facts,
// as the editor lists them (the draft's own analysis without a preflight).
export function importReviewFindings(input: ImportReviewInput): ImportFinding[] {
  const base = input.inspection?.findings ?? draftImportFindings(input.script)
  const codes = new Set(base.map(f => f.code))
  const readiness = input.inspection?.readiness
  const preflight = input.preflight
  const extra: ImportFinding[] = []
  // Older/unprotected imports still expose archive-level gates. Keep them visible
  // without rendering another copy of action-level findings; unresolved
  // credentials are the bindings finding below. The server folds what is left
  // into promotion and load-test launch only, so each is a warning: replay is
  // refused for the gates the findings name, and for nothing said here.
  for (const [index, detail] of (input.fidelityBlockers || []).entries()) {
    if (fidelityBlockerCoveredByGate(detail, codes)) continue
    extra.push({ code: `${ARCHIVE_CODE}${index}`, detail, gate: 'promotion', severity: 'warning' })
  }
  // A configuration reference blocks replay before any step runs, and no step
  // or guided repair supplies it, so no step carries it as a gate: it is one
  // script-wide blocker per value, as the editor lists it.
  const configNames = preflight ? replayBlockingKinds(preflight, input.script).config : new Set(analyzeVariableReadiness(input.script).configBlocking)
  for (const name of [...configNames].sort()) extra.push({ code: `${CONFIG_CODE}${name}`, detail: configReferenceText(name), gate: 'execution' })
  // The inspection's readiness resolves secrets against the project and omits
  // a zero count; a preflight stamped without that resolver lists every
  // project secret as missing, so it counts only until an inspection answers.
  const bindings = readiness ? (readiness.unresolved_bindings ?? 0) : (preflight?.missing_secrets?.length || 0)
  if (bindings) extra.push({ code: 'diagnostic:credential_bindings', detail: `${bindings} credential requirements remain unresolved. Review project secret bindings and protected body requirements. Counts may include several requirements for one request.`, gate: 'execution' })
  const failedValidation = preflight?.checks?.find(check => check.id === 'validation' && check.status === 'fail')
  const validation = readiness ? readiness.validation_issues || [] : failedValidation ? [failedValidation.detail || 'Script validation failed.'] : []
  if (validation.length) extra.push({ code: 'diagnostic:validation', detail: validation.join('\n'), gate: 'execution' })
  const replayBlocked = Boolean(input.executionGated || preflight?.valid === false || preflight?.checks?.some(check => check.status === 'fail') || (readiness && !readiness.replay_ready))
  // A blocker-severity section (unsupported elements, a blocked runtime
  // setting) refuses nothing itself: it blocks replay here only when replay is
  // refused and nothing else says why. Otherwise it stays listed as a note.
  const explained = [...base, ...extra].some(f => f.gate === 'execution')
  const sections = (input.reviewSections || []).filter(section => IMPORT_NOTE_SECTIONS.has(section.key))
  const warningFallback = input.warnings?.length ? [{ key: 'warnings', title: 'Conversion warnings', severity: 'warning', summary: '', items: [...input.warnings] }] : []
  for (const finding of reviewSectionFindings([...sections, ...warningFallback], input.sourceReview)) {
    extra.push(finding.gate === 'execution' && (explained || !replayBlocked) ? { ...finding, gate: 'promotion' } : finding)
  }
  if (replayBlocked && ![...base, ...extra].some(f => f.gate === 'execution')) extra.push({ code: 'diagnostic:execution', detail: 'Additional protocol or execution checks need attention. Inspect conversion details and the related review sections before replay.', gate: 'execution' })
  return [...base, ...extra]
}

export type ImportReadinessCounts = { blockers: number; warnings: number; notes: number; reviewed: number }
// A review mark settles a warning or note for this session: it leaves its
// count for `reviewed`. It never settles a blocker.
export function readinessCounts(issues: readonly ImportIssue[], reviewedIds: ReadonlySet<string> = new Set()): ImportReadinessCounts {
  const counts = { blockers: 0, warnings: 0, notes: 0, reviewed: 0 }
  for (const issue of issues) {
    if (issue.severity === 'blocker') counts.blockers++
    else if (reviewedIds.has(issue.id)) counts.reviewed++
    else if (issue.severity === 'warning') counts.warnings++
    else counts.notes++
  }
  return counts
}
export const unresolvedIssueCount = (counts: ImportReadinessCounts) => counts.blockers + counts.warnings + counts.notes
export function readinessSummaryText(counts: ImportReadinessCounts): string {
  return [
    `${counts.blockers} ${counts.blockers === 1 ? 'blocks' : 'block'} replay`,
    countLabel(counts.warnings, 'warning'),
    countLabel(counts.notes, 'review note'),
    ...(counts.reviewed ? [`${counts.reviewed} reviewed`] : []),
  ].join(' · ')
}

export function issueScope(issue: ImportIssue): string {
  const locations = issue.findings.filter(finding => finding.action_path || finding.source_ref)
  return locations.length ? `${locations.length} affected ${locations.length === 1 ? 'location' : 'locations'}` : 'Script-wide'
}

// Under metadata.import.preserved, the records the server rewrites after every
// repair: its evaluation of the repaired script (fidelity report, replay
// preflight) and the candidate's own bookkeeping. They say what the repair
// did; they are not a change the repair makes. The protocol report is written
// at import and no repair rewrites it; it is left out only defensively, as a
// review record rather than a field a repair could change.
const REPAIR_BOOKKEEPING_PATH = 'metadata.import.preserved'
const REPAIR_BOOKKEEPING_KEYS = new Set(['fidelity_report', 'replay_preflight', 'protocol_compat_report', 'guided_repair_candidate', 'server_import_attestation_v1'])
// The objects on the way to it, which a draft may not have yet.
const REPAIR_BOOKKEEPING_CONTAINERS = new Set(['metadata', 'metadata.import', REPAIR_BOOKKEEPING_PATH])
const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value)

// Report field paths only: repair values may contain protected payloads or
// credentials. The server's bookkeeping is left out, so a repair that changes
// nothing else lists nothing.
export function changedRepairPaths(before: unknown, after: unknown): string[] {
  const paths: string[] = []
  const walk = (prior: unknown, next: unknown, path: string, depth: number) => {
    if (Object.is(prior, next) || paths.length >= 100) return
    // A side without the metadata that holds the bookkeeping compares as
    // empty, so only the fields beside it are listed.
    const container = REPAIR_BOOKKEEPING_CONTAINERS.has(path)
    const a = container && prior == null && isRecord(next) ? {} : prior
    const b = container && next == null && isRecord(prior) ? {} : next
    if (depth >= 16 || a == null || b == null || typeof a !== 'object' || typeof b !== 'object' || Array.isArray(a) !== Array.isArray(b)) { paths.push(path || 'Script'); return }
    const left = a as Record<string, unknown>; const right = b as Record<string, unknown>
    for (const key of new Set([...Object.keys(left), ...Object.keys(right)])) {
      if (key === 'server_import_attestation_v1' || (path === REPAIR_BOOKKEEPING_PATH && REPAIR_BOOKKEEPING_KEYS.has(key))) continue
      walk(left[key], right[key], Array.isArray(a) ? `${path}[${key}]` : path ? `${path}.${key}` : key, depth + 1)
    }
  }
  walk(before, after, '', 0)
  return paths
}
