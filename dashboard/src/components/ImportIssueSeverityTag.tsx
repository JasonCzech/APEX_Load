import type { IssueDetailSeverity } from './IssueDetailsDialog'
import './ImportIssueSeverityTag.css'

type Severity = IssueDetailSeverity | 'resolved'

const labels: Record<Severity, string> = {
  blocker: 'Blocker',
  error: 'Error',
  warning: 'Warning',
  info: 'Review note',
  resolved: 'Resolved',
}

export default function ImportIssueSeverityTag({ severity }: { severity: Severity }) {
  return <span className={`import-issue-severity-tag ${severity}`}>{labels[severity]}</span>
}
