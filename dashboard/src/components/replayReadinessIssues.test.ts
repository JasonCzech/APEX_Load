import { describe, expect, it } from 'vitest'
import type { ReplayPreflight } from '../types/scripts'
import { templateVariableNames } from './replayPreflightUtils'
import { headerIssueText, openingEditorFocus, replayReadinessIssues } from './replayReadinessIssues'

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

  it('titles a secret by its display name and keeps the exact name in the description', () => {
    const issues = replayReadinessIssues(
      preflight({ missing_secrets: ['PARTNER_PASSWORD_A3CA62479DB786C5096A4AE7', 'USERNAME'] }),
      [],
      new Map([['PARTNER_PASSWORD_A3CA62479DB786C5096A4AE7', 'PARTNER_PASSWORD']]),
    )

    expect(issues.map(issue => [issue.title, issue.description, issue.secret])).toEqual([
      ['Configure project secret: PARTNER_PASSWORD', 'Replay stays blocked until the project secret PARTNER_PASSWORD_A3CA62479DB786C5096A4AE7 has a value.', 'PARTNER_PASSWORD_A3CA62479DB786C5096A4AE7'],
      ['Configure project secret: USERNAME', 'Replay stays blocked until the project secret has a value.', 'USERNAME'],
    ])
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

  it('names a header the request cannot send, in plain words, without its path', () => {
    const issues = replayReadinessIssues(preflight({ issues: [
      'actions[3] "Create session".header_list[7] "X-Bad": header value contains characters HTTP does not allow (http_header_invalid_value)',
      'actions[0] "Say \\"hi\\"".params.requests[1].headers[0] "X \\"q\\"": header name contains characters HTTP does not allow (http_header_invalid_header_name)',
      'actions[1] "Search".effective_headers[2] "Host": duplicate singleton header (http_header_duplicate_singleton_header)',
      'config.http.default_header_list[0] "X-Trace": header value contains characters HTTP does not allow (http_header_invalid_value)',
    ] }))
    expect(issues.map(issue => issue.title)).toEqual([
      'Header "X-Bad": value contains characters HTTP does not allow',
      'Header "X "q"": name contains characters HTTP does not allow',
      'Header "Host": duplicate singleton header',
      'Default header "X-Trace": value contains characters HTTP does not allow',
    ])
    expect(issues[0].location).toEqual({ phase: 'action', actionIndex: 3 })
    expect(issues[0].description).toBe('Replay sends headers exactly as written. Correct or remove it in this request\'s headers.')
    expect(issues[2].description).toContain('Runtime Settings › Request Defaults')
    expect(issues.every(issue => !issue.description?.includes('header_list'))).toBe(true)
    expect(headerIssueText('actions[0] "Pay": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved')).toBeNull()
  })

  it('names the headers an import left out, from the blocker detail', () => {
    const [single, both, comma, mixed] = replayReadinessIssues(preflight({ issues: [
      'actions[2] "Pay": execution blocker import_review_required (1 header left out: X-Bad) blocks replay until the imported behavior is resolved',
      'actions[4]: execution blocker import_review_required (2 headers left out: X-Bad, "X Pasted\\r\\nHeader") blocks replay until the imported behavior is resolved',
      'actions[5]: execution blocker import_review_required (1 header left out: "X, Y") blocks replay until the imported behavior is resolved',
      'actions[6]: execution blocker import_review_required (other review and 1 header left out: X-Bad) blocks replay until the imported behavior is resolved',
    ] }))
    expect(single.title).toBe('Header X-Bad left out on import')
    expect(single.description).toContain('Correct it in the source and import it again.')
    expect(both.title).toBe('Headers X-Bad, "X Pasted\\r\\nHeader" left out on import')
    expect(both.location).toEqual({ phase: 'action', actionIndex: 4 })
    // One header whose quoted name holds a comma is still one header.
    expect(comma.title).toBe('Header "X, Y" left out on import')
    // A hold that covers more than the headers keeps its general title, and
    // the names stay in its description.
    expect(mixed.title).toBe('Review imported behavior')
    expect(mixed.description).toContain('other review and 1 header left out: X-Bad')
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

describe('openingEditorFocus', () => {
  const row = (phase: 'init' | 'action' | 'teardown', actionIndex: number) => ({ phase, actionIndex })
  const gate = replayReadinessIssues(preflight({ issues: ['init[1] "gate": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved'] }))
  const rows = [row('init', 0), row('init', 1), row('action', 0), row('action', 1)]

  it('opens on the first action with an authoring error, as Author Problems lists them', () => {
    expect(openingEditorFocus([row('action', 1)], gate, rows)).toEqual({ phase: 'action', actionIndex: 1 })
  })

  it('then on the first replay gate, in any phase', () => {
    expect(openingEditorFocus([], gate, rows)).toEqual({ phase: 'init', actionIndex: 1 })
  })

  it('else on the first action, Actions before Init and Teardown', () => {
    expect(openingEditorFocus([], [], rows)).toEqual({ phase: 'action', actionIndex: 0 })
    expect(openingEditorFocus([], [], [row('teardown', 0)])).toEqual({ phase: 'teardown', actionIndex: 0 })
    expect(openingEditorFocus([], [], [])).toBeNull()
  })
})
