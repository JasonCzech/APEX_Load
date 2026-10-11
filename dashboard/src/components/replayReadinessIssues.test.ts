import { describe, expect, it } from 'vitest'
import type { ReplayPreflight } from '../types/scripts'
import { templateVariableNames } from './replayPreflightUtils'
import { replayReadinessIssues } from './replayReadinessIssues'

const preflight = (overrides: Partial<ReplayPreflight>): ReplayPreflight => ({ valid: false, action_count: 3, expected_action_count: 3, data_source_count: 0, parameter_count: 0, ...overrides })

describe('replayReadinessIssues', () => {
  it('lists each gate, missing value and secret the preflight reports', () => {
    const issues = replayReadinessIssues(preflight({
      issues: ['init[2] "web_set_sockets_option_15": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved'],
      missing_variables: ['c_storeid', 'token'],
      blocking_variables: ['token'],
      placeholder_variables: ['c_storeid'],
      missing_secrets: ['PROXY_PASSWORD'],
      warnings: ['variable(s) with no seed or preceding extractor are sent as placeholder text: c_storeid', 'configure project secret(s) before replay: PROXY_PASSWORD', 'Parameter p references missing data source d.'],
    }))
    expect(issues.map(issue => [issue.severity, issue.title])).toEqual([
      ['blocker', 'Choose supported TLS settings'],
      ['blocker', 'Unresolved value: token'],
      ['warning', 'Missing value: c_storeid'],
      ['blocker', 'Configure project secret: PROXY_PASSWORD'],
      ['warning', 'Parameter p references missing data source d.'],
    ])
    expect(issues[0].location).toEqual({ phase: 'init', actionIndex: 2 })
    expect(issues[0].description).toBe('Execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved')
    expect(issues[1].variable).toBe('token')
    expect(issues[3].secret).toBe('PROXY_PASSWORD')
  })

  it('maps actions[] paths to the action phase and keeps a nested request path in view', () => {
    const message = 'actions[4].params.requests[1] "Pay": review-required action blocks replay until review metadata is removed'
    const [gate] = replayReadinessIssues(preflight({ issues: [message] }))
    expect(gate.location).toEqual({ phase: 'action', actionIndex: 4 })
    expect(gate.title).toBe('Review-required action blocks replay until review metadata is removed')
    expect(gate.description).toBe(message)
  })

  it('titles each execution blocker code, splitting only between codes', () => {
    const [gate] = replayReadinessIssues(preflight({ issues: [
      'teardown[0] "Say \\"bye\\"": execution blocker encrypted_proxy_password, bruno_missing_variables (a, b) blocks replay until the imported behavior is resolved',
    ] }))
    expect(gate.location).toEqual({ phase: 'teardown', actionIndex: 0 })
    expect(gate.title).toBe('Replace encrypted proxy password · Bruno missing variables')
  })

  it('titles a gate on an unnamed action, as the local preflight words it', () => {
    const [gate] = replayReadinessIssues(preflight({ issues: ['init[3]: execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved'] }))
    expect(gate).toMatchObject({ title: 'Choose supported TLS settings', location: { phase: 'init', actionIndex: 3 } })
  })

  it('lists data-source problems as blockers once, and as warnings for an older controller', () => {
    const message = 'file data source "users" requires an import file binding before replay'
    const current = replayReadinessIssues(preflight({ data_source_issues: [message], warnings: [message] }))
    expect(current.map(issue => [issue.severity, issue.title])).toEqual([['blocker', message]])
    const older = replayReadinessIssues(preflight({ warnings: [message] }))
    expect(older.map(issue => [issue.severity, issue.title])).toEqual([['warning', message]])
  })

  it('keeps messages that are not review gates as they are', () => {
    const [issue] = replayReadinessIssues(preflight({ issues: ['Script name is required.'] }))
    expect(issue.title).toBe('Script name is required.')
    expect(issue.description).toBeUndefined()
    expect(issue.location).toBeUndefined()
  })

  it('skips messages the editor already lists from server validation', () => {
    const issues = replayReadinessIssues(preflight({ issues: ['Script name is required.', 'teardown[0] "Logout": blocked'] }), ['script name is  required.'])
    expect(issues.map(issue => [issue.title, issue.location?.phase])).toEqual([['Blocked', 'teardown']])
  })

  it('treats an older controller\'s unsplit missing values as placeholders', () => {
    const issues = replayReadinessIssues(preflight({ missing_variables: ['a', 'b'] }))
    expect(issues.map(issue => [issue.severity, issue.title])).toEqual([['warning', 'Missing value: a'], ['warning', 'Missing value: b']])
  })

  it('reports nothing for a ready preflight or none at all', () => {
    expect(replayReadinessIssues(preflight({ valid: true }))).toEqual([])
    expect(replayReadinessIssues(null)).toEqual([])
  })
})

describe('templateVariableNames', () => {
  it('collects template uses of a name, not longer names, secrets or plain text', () => {
    expect([...templateVariableNames('{"target":"/store/{{var.c_storeid}}/cart","body":"id={{ data.order }}"}')]).toEqual(['c_storeid', 'order'])
    expect(templateVariableNames('{"target":"/store/{{var.c_storeid_2}}"}').has('c_storeid')).toBe(false)
    expect(templateVariableNames('{"name":"c_storeid"}').size).toBe(0)
    expect(templateVariableNames('{"token":"{{secret.c_storeid}}"}').size).toBe(0)
    expect(templateVariableNames('{"x":"{{var.a.b}}"}').has('a.b')).toBe(true)
  })
})
