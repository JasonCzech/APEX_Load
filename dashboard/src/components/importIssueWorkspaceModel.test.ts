import { expect, it } from 'vitest'
import { groupImportWorkspaceIssues, importDiagnosticTitle } from './importIssueWorkspaceModel'

it('titles normalized diagnostics by the problem before the sampler', () => {
  expect(importDiagnosticTitle({ code: 'import_missing_variables', element: 'SPLIT_PAY_BALANCE', detail: 'Declare or explicitly bind unresolved variables: client_id, client_secret' }))
    .toBe('Unresolved variables: client_id, client_secret in SPLIT_PAY_BALANCE')
  expect(importDiagnosticTitle({ code: 'jmeter_timestamp_timezone_required', element: 'SPLIT_PAY_BALANCE' })).toBe('Timestamp timezone policy required in SPLIT_PAY_BALANCE')
  expect(importDiagnosticTitle({ code: 'unsupported_auth_scheme', element: 'Login' })).toBe('Unsupported auth scheme in Login')
  expect(importDiagnosticTitle({ code: 'jmeter_diagnostic:JMeter CSV export', element: 'JMeter CSV export' })).toBe('JMeter CSV export')
  expect(importDiagnosticTitle({ code: 'jmeter_warning:View Results Tree is enabled', element: 'JMeter source' })).toBe('Source warning')
})

it('explains the HAR storage policy in sentences instead of routing it to credentials', () => {
  const [issue] = groupImportWorkspaceIssues([
    { id: 'a', severity: 'info', title: 'HAR Artifact Governance', description: 'The stored HAR keeps the recorded values, so it can contain credentials, cookies and tokens. Treat it as sensitive.' },
    { id: 'b', severity: 'info', title: 'HAR Artifact Governance', description: 'Retention period: 7 days.' },
  ])
  expect(issue.title).toBe('Review HAR storage policy')
  expect(issue.targetTab).toBeUndefined()
  expect(issue.occurrences).toHaveLength(2)
  expect(issue.impact).toContain('does not change replay')
})
