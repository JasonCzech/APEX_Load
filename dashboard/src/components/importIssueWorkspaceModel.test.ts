import { expect, it } from 'vitest'
import { groupImportWorkspaceIssues, importDiagnosticTitle } from './importIssueWorkspaceModel'

it('titles normalized diagnostics by the problem before the sampler', () => {
  expect(importDiagnosticTitle({ code: 'import_missing_variables', element: 'SPLIT_PAY_BALANCE', detail: 'Declare or explicitly bind unresolved variables: client_id, client_secret' }))
    .toBe('Unresolved variables: client_id, client_secret in SPLIT_PAY_BALANCE')
  expect(importDiagnosticTitle({ code: 'jmeter_timestamp_timezone_required', element: 'SPLIT_PAY_BALANCE' })).toBe('Choose how JMeter timestamps are evaluated in SPLIT_PAY_BALANCE')
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

it('sends the timestamp policy blocker to its conversion option instead of runtime settings', () => {
  const [issue] = groupImportWorkspaceIssues([{
    id: 'ts', identity: 'ts', severity: 'blocker', code: 'jmeter_timestamp_timezone_required',
    title: importDiagnosticTitle({ code: 'jmeter_timestamp_timezone_required', element: 'Balance' }),
    description: 'Select a timestamp policy: UTC normalization or a source timezone and acknowledge the literal Z suffix.',
    source: 'plan.jmx:7 · actions[0]',
  }])
  expect(issue.targetTab).toBeUndefined()
  expect(issue.actionLabel).toBeUndefined()
  expect(issue.resolution).toContain('Source & conversion options')
  expect(issue.resolution).toContain('Inspect & review again')
  // The converter's own sentence is kept as the explanation.
  expect(issue.impact).toBeUndefined()
})

it('explains every JMeter messaging gate in plain language and says what clears it', () => {
  const codes = [
    'jmeter_eventhubs_credentials_required',
    'jmeter_kafka_oauth_credentials_required',
    'jmeter_kafka_nonstandard_oauth_options',
    'jmeter_reference_context_confirmation_required',
    'jmeter_messaging_compatibility_unverified',
    'jmeter_kafka_batch_allocation_parity_unverified',
  ]
  const grouped = groupImportWorkspaceIssues(codes.map(code => ({
    id: code, identity: code, severity: 'blocker' as const, code,
    title: importDiagnosticTitle({ code, element: 'Producer' }),
    description: `Resolve imported behavior before replay: ${code}`,
    source: 'plan.jmx#scripts[1].actions[0]#Producer · scripts[1].actions[0]',
  })))
  const byCode = Object.fromEntries(grouped.map(issue => [issue.code, issue]))
  for (const code of codes) {
    const issue = byCode[code]
    expect(issue.title).not.toContain('_')
    expect(issue.impact).toBeTruthy()
    expect(issue.impact).not.toContain(code)
    expect(issue.resolution).toBeTruthy()
  }
  expect(byCode.jmeter_messaging_compatibility_unverified.title).toBe('JMeter messaging replay is not available yet in Producer')
  // The titles are the API guidance table's, so the issue and its suggestion match.
  expect(byCode.jmeter_eventhubs_credentials_required.title).toBe('Bind the Event Hubs connection string in Producer')
  expect(byCode.jmeter_messaging_compatibility_unverified.unclearable).toBe(true)
  expect(byCode.jmeter_kafka_batch_allocation_parity_unverified.unclearable).toBe(true)
  expect(byCode.jmeter_eventhubs_credentials_required.unclearable).toBeFalsy()
  expect(byCode.jmeter_messaging_compatibility_unverified.impact).toContain('JMeter messaging replay is not available yet')
  expect(byCode.jmeter_messaging_compatibility_unverified.impact).toContain('preserved for review')
  expect(byCode.jmeter_kafka_batch_allocation_parity_unverified.impact).toContain('JMeter messaging replay is not available yet')
  expect(byCode.jmeter_messaging_compatibility_unverified.resolution).toContain('Nothing in this review clears this gate')
  expect(byCode.jmeter_reference_context_confirmation_required.resolution).toContain('Confirm the reference timezone and no property overrides')
  expect(byCode.jmeter_reference_context_confirmation_required.resolution).toContain('UTC or the IANA timezone')
  expect(byCode.jmeter_reference_context_confirmation_required.resolution).toContain('converted with time mode utc sends true UTC time, so confirm UTC for it')
  expect(byCode.jmeter_reference_context_confirmation_required.targetTab).toBeUndefined()
  expect(byCode.jmeter_eventhubs_credentials_required.resolution).toContain('Endpoint=sb://<namespace>/;SharedAccessKeyName=<name>;SharedAccessKey=<key>')
  expect(byCode.jmeter_eventhubs_credentials_required.resolution).toContain(';EntityPath=<hub>')
  expect(byCode.jmeter_kafka_oauth_credentials_required.targetTab).toBe('credentials')
})

it('prefers the server detail and resolution over the review copy', () => {
  const [issue] = groupImportWorkspaceIssues([{
    id: 'eh', identity: 'eh', severity: 'blocker', code: 'jmeter_eventhubs_credentials_required',
    title: 'Bind the Event Hubs connection string in Producer',
    description: 'Bind the project secret that holds the connection string.',
    resolution: 'Apply the bind repair.',
  }])
  expect(issue.impact).toBeUndefined()
  expect(issue.resolution).toBe('Apply the bind repair.')
})
