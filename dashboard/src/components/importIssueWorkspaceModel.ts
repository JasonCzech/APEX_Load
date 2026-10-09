import type { IssueDetail, IssueDetailSeverity } from './IssueDetailsDialog'
import type { ImportReviewTab } from './ImportReviewTabs'

export interface ImportWorkspaceIssue extends IssueDetail {
  identity?: string
  targetTab?: ImportReviewTab
  actionLabel?: string
  impact?: string
  resolution?: string
}

export interface GroupedImportWorkspaceIssue extends ImportWorkspaceIssue {
  occurrences: ImportWorkspaceIssue[]
}

const severityOrder: Record<IssueDetailSeverity, number> = { blocker: 0, error: 1, warning: 2, info: 3 }

const responseContentSourceReasons = new Set([
  'response_content_truncated', 'unsupported_response_content_encoding', 'invalid_response_content_encoding',
  'response_content_decode_limit', 'response_content_decode_canceled', 'malformed_response_content_provenance',
  'response_content_provenance_unavailable', 'invalid_har_content_storage', 'unsupported_har_content_storage',
  'har_content_storage_limit',
])

function diagnosticCode(issue: ImportWorkspaceIssue) {
  return /\b([a-z][a-z0-9]+(?:_[a-z0-9]+){2,})\b/i.exec(issue.description || '')?.[1]?.toLowerCase() || ''
}

function issuePresentation(issue: ImportWorkspaceIssue): ImportWorkspaceIssue {
  const evidence = `${issue.title} ${issue.description || ''} ${issue.source || ''}`.toLowerCase()
  if ((evidence.match(/[a-z][a-z0-9]*(?:_[a-z0-9]+)+/g) || []).some(code => responseContentSourceReasons.has(code))) return {
    ...issue,
    title: 'Review unavailable response content',
    targetTab: 'source',
    actionLabel: 'Inspect source evidence',
    impact: 'The captured response is incomplete or its decoded content cannot be verified. It cannot supply faithful response correlations.',
    resolution: 'Inspect the captured source, then provide a complete supported capture or replace the affected behavior and its source gate. A review note cannot clear this execution blocker.',
  }
  if (evidence.includes('jmeter_unbounded_thread_group')) return { ...issue, title: 'Set a finite JMeter termination rule', targetTab: 'runtime', actionLabel: 'Edit runtime settings' }
  if (evidence.includes('source_order_unavailable') || /headers as a map.*order was synthesized/.test(evidence)) return {
    ...issue,
    title: 'Confirm synthesized source order',
    targetTab: 'source',
    actionLabel: 'Inspect source evidence',
    impact: 'The source did not specify a stable order. The draft chose one, so confirm that the order does not change request behavior.',
    resolution: 'Compare the highlighted source location with the draft. If the order is harmless, mark this note reviewed; otherwise correct the draft before replay.',
  }
  if (/imported credential.*retained in the runnable destination/.test(evidence)) return {
    ...issue,
    title: 'Review preserved credential',
    targetTab: 'credentials',
    actionLabel: 'Review credential handling',
    impact: 'The generated request still uses the imported credential value. Confirm that retaining it is intentional before replay.',
    resolution: 'Review the preserved value in Credentials. Keep it for exact replay, or bind an approved project secret where available.',
  }
  if (/no reusable correlation values were detected/.test(evidence)) return {
    ...issue,
    title: 'No reusable correlation values detected',
    impact: 'The draft does not extract and reuse a response value. Later requests may need an extractor if they depend on dynamic data.',
    resolution: 'If the source flow expects a returned value, add an extractor in the Visual Editor after saving. Otherwise, record that no correlation is needed.',
  }
  if (/^har artifact governance$/i.test(issue.title.trim())) return {
    ...issue,
    title: 'Review HAR storage policy',
    impact: 'The stored HAR keeps the recorded traffic, including any credentials, cookies and tokens it captured. This policy does not change replay.',
    resolution: 'Confirm the stored HAR may be kept under this policy, then mark this note reviewed.',
  }
  if (/credential|secret|authorization|token/.test(evidence)) return { ...issue, targetTab: 'credentials', actionLabel: 'Review credentials' }
  if (/parameter|variable|csv|data source|extractor|correlation/.test(evidence)) return { ...issue, targetTab: 'data', actionLabel: 'Configure data & parameters' }
  if (/runtime|thread group|duration|iteration|timer|timestamp|tls|sandbox/.test(evidence)) return { ...issue, targetTab: 'runtime', actionLabel: 'Review runtime settings' }
  if (/source|mapping|provenance|unsupported/.test(evidence)) return { ...issue, targetTab: 'source', actionLabel: 'Inspect source evidence' }
  return issue
}

const diagnosticKindTitles: Record<string, string> = {
  import_missing_variables: 'Unresolved variables',
  import_credential_preserved: 'Imported credential kept in the script',
  import_review_required: 'Imported behavior needs review',
  jmeter_timestamp_timezone_required: 'Timestamp timezone policy required',
  jmeter_invalid_header_value: 'Invalid header value',
  jmeter_review_traversal_failed: 'Review traversal failed',
}

// Titles a normalized import diagnostic by what is wrong, then where. The
// element alone is often a sampler name that says nothing about the problem.
export function importDiagnosticTitle(item: { code?: string; element: string; detail?: string }): string {
  const code = item.code || ''
  // These codes embed the element, which already names the kind of finding.
  if (!code || code.startsWith('jmeter_diagnostic:')) return item.element
  if (code.startsWith('jmeter_warning:')) return 'Source warning'
  const words = code.replace(/^(?:import|jmeter)_/, '').replace(/_/g, ' ')
  const kind = diagnosticKindTitles[code] || `${words.charAt(0).toUpperCase()}${words.slice(1)}`
  const variables = code === 'import_missing_variables' ? /:\s*([^:]+)$/.exec(item.detail || '')?.[1]?.trim() : ''
  const subject = variables ? `${kind}: ${variables}` : kind
  return item.element && !/^(?:JMeter (?:source|script)|imported credential)$/i.test(item.element) ? `${subject} in ${item.element}` : subject
}

export function groupImportWorkspaceIssues(issues: ImportWorkspaceIssue[]): GroupedImportWorkspaceIssue[] {
  const groups = new Map<string, GroupedImportWorkspaceIssue>()
  const evidenceOwners = new Map<string, string>()
  for (const rawIssue of issues) {
    const issue = issuePresentation(rawIssue)
    const evidence = issue.description?.trim().replace(/\s+/g, ' ').toLowerCase()
    const evidenceKey = evidence && !issue.identity ? `${issue.severity}:${issue.targetTab || ''}:${evidence}` : ''
    const key = issue.identity || (evidenceKey && evidenceOwners.get(evidenceKey)) || `${issue.title.trim().toLowerCase()}::${diagnosticCode(issue)}`
    const current = groups.get(key)
    if (!current) {
      groups.set(key, { ...issue, occurrences: [issue] })
      if (evidenceKey) evidenceOwners.set(evidenceKey, key)
      continue
    }
    const hasSourceLocation = /\bline\s*:?\s*\d+\b/i.test(issue.description || '')
    const genericSource = (source?: string) => !source || /^(?:Import conversion|\d+ import diagnostic)/i.test(source)
    if (!current.occurrences.some(existing => existing.description === issue.description && (
      existing.source === issue.source || (hasSourceLocation && genericSource(existing.source) && genericSource(issue.source))
    ))) current.occurrences.push(issue)
    if (severityOrder[issue.severity] < severityOrder[current.severity]) current.severity = issue.severity
    if (!current.targetTab && issue.targetTab) current.targetTab = issue.targetTab
    if (!current.actionLabel && issue.actionLabel) current.actionLabel = issue.actionLabel
    if (evidenceKey) evidenceOwners.set(evidenceKey, key)
  }
  return [...groups.values()].sort((left, right) => severityOrder[left.severity] - severityOrder[right.severity] || left.title.localeCompare(right.title))
}
