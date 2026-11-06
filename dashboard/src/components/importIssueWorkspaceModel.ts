import type { IssueDetail, IssueDetailSeverity } from './IssueDetailsDialog'
import type { ImportReviewTab } from './ImportReviewTabs'

export interface ImportWorkspaceIssue extends IssueDetail {
  identity?: string
  /** The normalized diagnostic code, when the importer reported one. */
  code?: string
  /** Where in the draft the diagnostic applies, such as scripts[1].actions[0]. */
  location?: string
  targetTab?: ImportReviewTab
  actionLabel?: string
  impact?: string
  resolution?: string
  /** Nothing available in this release clears the finding, so no fix is offered. */
  unclearable?: boolean
}

/**
 * A resolution the importer hosting the review offers for one issue, outside
 * the review tabs (a source option, a batch repair). run, when present,
 * replaces the tab button; resolution replaces the issue's resolution text.
 */
export interface ImportIssueAction {
  label?: string
  run?: () => void
  resolution?: string
  /** The importer says nothing clears this finding yet: no fix button is shown. */
  unclearable?: boolean
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

// The converter's detail for codes it has no sentence for. It names the code
// only, so the review explains those codes itself.
const GENERIC_DIAGNOSTIC_DETAIL = /^resolve imported behavior before replay:/i

const MESSAGING_REPLAY_UNAVAILABLE = 'JMeter messaging replay is not available yet. The converted script is preserved for review, but it cannot run until APEX has verified its Kafka and Event Hubs clients against the JMeter plugins.'

// Code-specific explanations for gates a review mark cannot clear: each says
// what clears it, or that nothing in the review does. A server-provided
// detail or resolution wins over this copy.
const diagnosticPresentations: Record<string, Pick<ImportWorkspaceIssue, 'targetTab' | 'actionLabel' | 'impact' | 'resolution' | 'unclearable'>> = {
  jmeter_timestamp_timezone_required: {
    // The policy is a conversion option, not a runtime setting.
    targetTab: undefined,
    actionLabel: undefined,
    impact: 'The source builds a timestamp from the load generator\'s local clock but labels it UTC with a literal Z suffix, so the value it sends depends on where the test runs. Replay stays blocked until you choose how the timestamp is produced.',
    resolution: 'Open Source & conversion options and choose a Timestamp policy: normalize to UTC, or keep the source wall clock and name its IANA timezone. Then select Inspect & review again. Changing the policy replaces this review with a new inspection.',
  },
  jmeter_kafka_oauth_credentials_required: {
    impact: 'The Kafka OAuth client ID and client secret were moved to project-secret references. Replay stays blocked until you confirm which project secrets supply them.',
    resolution: 'Create the client ID and client secret in Project Secrets, then apply the repair "Bind Kafka credential references" for this group in Reviewed repairs for all scripts.',
  },
  jmeter_kafka_nonstandard_oauth_options: {
    impact: 'The Kafka OAuth settings use option names the standard Kafka client does not recognize, so the importer cannot tell how the JMeter plugin applied them.',
    resolution: 'Apply the repair "Normalize Kafka OAuth option names" for this group in Reviewed repairs for all scripts. If the repair is refused, correct the option names in the source and import it again.',
  },
  jmeter_eventhubs_credentials_required: {
    impact: 'The Event Hubs connection string was moved to a project-secret reference. Replay stays blocked until you confirm which project secret holds it.',
    resolution: 'Create a project secret whose value is the connection string, in the form Endpoint=sb://<namespace>/;SharedAccessKeyName=<name>;SharedAccessKey=<key>, optionally followed by ;EntityPath=<hub> naming this script\'s event hub. Then apply the repair "Bind Event Hubs credential reference" for this group in Reviewed repairs for all scripts.',
  },
  jmeter_reference_context_confirmation_required: {
    targetTab: undefined,
    actionLabel: undefined,
    impact: 'The messaging schedule and payload times depend on the timezone the JMeter host ran in and on JMeter property overrides (-J options). If either differs from what the draft assumes, the replayed load or timestamps differ from the source.',
    resolution: 'Apply the repair "Confirm the reference timezone and no property overrides" in Reviewed repairs for all scripts, choosing UTC or the IANA timezone the JMeter host ran in. A plan converted with time mode utc sends true UTC time, so confirm UTC for it. If the source used property overrides, correct the plan and import it again.',
  },
  jmeter_messaging_compatibility_unverified: {
    targetTab: undefined,
    actionLabel: undefined,
    impact: MESSAGING_REPLAY_UNAVAILABLE,
    resolution: 'Nothing in this review clears this gate. Save the scripts to keep them for review; no repair or review mark makes them runnable today.',
    unclearable: true,
  },
  jmeter_kafka_batch_allocation_parity_unverified: {
    targetTab: undefined,
    actionLabel: undefined,
    impact: `APEX has not verified that its Kafka producer batches and partitions messages the way the JMeter Kafka plugin does, so throughput and partition spread could differ. ${MESSAGING_REPLAY_UNAVAILABLE}`,
    resolution: 'Nothing in this review clears this gate. Save the scripts to keep them for review; no repair or review mark makes them runnable today.',
    unclearable: true,
  },
}

/** The review's own resolution for a code, in the words of the controls that clear it. */
export function importReviewResolution(code: string): string | undefined {
  return diagnosticPresentations[code]?.resolution
}

function codePresentation(issue: ImportWorkspaceIssue, evidence: string): ImportWorkspaceIssue | null {
  const code = issue.code && diagnosticPresentations[issue.code]
    ? issue.code
    : Object.keys(diagnosticPresentations).find(candidate => evidence.includes(candidate))
  if (!code) return null
  const presentation = diagnosticPresentations[code]
  const serverDetail = issue.description && !GENERIC_DIAGNOSTIC_DETAIL.test(issue.description.trim())
  return {
    ...issue,
    code,
    targetTab: 'targetTab' in presentation ? presentation.targetTab : /credential/.test(code) ? 'credentials' : undefined,
    actionLabel: 'actionLabel' in presentation ? presentation.actionLabel : /credential/.test(code) ? 'Review credentials' : undefined,
    impact: issue.impact || (serverDetail ? undefined : presentation.impact),
    resolution: issue.resolution || presentation.resolution,
    unclearable: issue.unclearable ?? presentation.unclearable,
  }
}

function issuePresentation(issue: ImportWorkspaceIssue): ImportWorkspaceIssue {
  const evidence = `${issue.title} ${issue.description || ''} ${issue.source || ''}`.toLowerCase()
  const coded = codePresentation(issue, evidence)
  if (coded) return coded
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
  if (isNoReusableCorrelationNote(evidence)) return {
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
  // The JMeter review-gate titles match the API's guidance table
  // (importreview.JMeterReviewCodeGuidance), so the issue and its repair
  // suggestion read the same.
  jmeter_timestamp_timezone_required: 'Choose how JMeter timestamps are evaluated',
  jmeter_kafka_oauth_credentials_required: 'Bind the Kafka OAuth client credentials',
  jmeter_kafka_nonstandard_oauth_options: 'Rename the Kafka OAuth options',
  jmeter_eventhubs_credentials_required: 'Bind the Event Hubs connection string',
  jmeter_reference_context_confirmation_required: 'Confirm the JMeter reference timezone',
  jmeter_messaging_compatibility_unverified: 'JMeter messaging replay is not available yet',
  jmeter_kafka_batch_allocation_parity_unverified: 'Kafka batching parity is not verified yet',
  jmeter_invalid_header_value: 'Invalid header value',
  jmeter_review_traversal_failed: 'Review traversal failed',
}

// isNoReusableCorrelationNote tells the importers' "no correlation found" note,
// which is information rather than a problem.
export function isNoReusableCorrelationNote(note: string): boolean {
  return /no reusable correlation values were detected/i.test(note)
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
