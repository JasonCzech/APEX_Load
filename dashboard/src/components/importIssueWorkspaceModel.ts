import type { IssueDetail, IssueDetailSeverity } from './IssueDetailsDialog'
import type { ImportReviewTab } from './ImportReviewTabs'

export interface ImportWorkspaceIssue extends IssueDetail {
  targetTab?: ImportReviewTab
  actionLabel?: string
}

export interface GroupedImportWorkspaceIssue extends ImportWorkspaceIssue {
  occurrences: ImportWorkspaceIssue[]
}

const severityOrder: Record<IssueDetailSeverity, number> = { blocker: 0, error: 1, warning: 2, info: 3 }

function diagnosticCode(issue: ImportWorkspaceIssue) {
  return /\b([a-z][a-z0-9]+(?:_[a-z0-9]+){2,})\b/i.exec(issue.description || '')?.[1]?.toLowerCase() || ''
}

function issuePresentation(issue: ImportWorkspaceIssue): ImportWorkspaceIssue {
  const evidence = `${issue.title} ${issue.description || ''} ${issue.source || ''}`.toLowerCase()
  if (evidence.includes('jmeter_unbounded_thread_group')) return { ...issue, title: 'Set a finite JMeter termination rule', targetTab: 'runtime', actionLabel: 'Edit runtime settings' }
  if (evidence.includes('source_order_unavailable')) return { ...issue, title: 'Confirm synthesized source order', targetTab: 'source', actionLabel: 'Inspect source mapping' }
  if (/credential|secret|authorization|token/.test(evidence)) return { ...issue, targetTab: 'credentials', actionLabel: 'Configure credentials' }
  if (/parameter|variable|csv|data source|extractor|correlation/.test(evidence)) return { ...issue, targetTab: 'data', actionLabel: 'Configure data & parameters' }
  if (/runtime|thread group|duration|iteration|timer|timestamp|tls|sandbox/.test(evidence)) return { ...issue, targetTab: 'runtime', actionLabel: 'Review runtime settings' }
  if (/source|mapping|provenance|unsupported/.test(evidence)) return { ...issue, targetTab: 'source', actionLabel: 'Inspect source evidence' }
  return issue
}

export function groupImportWorkspaceIssues(issues: ImportWorkspaceIssue[]): GroupedImportWorkspaceIssue[] {
  const groups = new Map<string, GroupedImportWorkspaceIssue>()
  for (const rawIssue of issues) {
    const issue = issuePresentation(rawIssue)
    const key = `${issue.title.trim().toLowerCase()}::${diagnosticCode(issue)}`
    const current = groups.get(key)
    if (!current) {
      groups.set(key, { ...issue, occurrences: [issue] })
      continue
    }
    current.occurrences.push(issue)
    if (severityOrder[issue.severity] < severityOrder[current.severity]) current.severity = issue.severity
    if (!current.targetTab && issue.targetTab) current.targetTab = issue.targetTab
    if (!current.actionLabel && issue.actionLabel) current.actionLabel = issue.actionLabel
  }
  return [...groups.values()].sort((left, right) => severityOrder[left.severity] - severityOrder[right.severity] || left.title.localeCompare(right.title))
}
