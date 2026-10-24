import { describe, expect, it } from 'vitest'
import type { ReplayPreflight, ScriptDefinition } from '../types/scripts'
import { draftImportFindings, groupImportFindings, readinessCounts, repairImpactText } from './importReviewIssues'
import { buildLocalReplayPreflight, templateVariableNames } from './replayPreflightUtils'
import { distinctBlockerCodes, headerIssueText, openingEditorFocus, readinessIssueText, replayReadinessIssues, replayRefusalSummary, reviewGateBreadcrumb, storedScriptImpactText } from './replayReadinessIssues'

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
    // A blocker says what it means as the import review does, not the gate's raw words.
    expect(issues[0].description).toBe('The source uses legacy TLS behavior. Choose a supported replacement and preview the migration. Debug and test runs refuse this script until it is resolved.')
    expect(issues[0]).toMatchObject({ codes: ['legacy_tls_replacement'], actionPath: 'init[2]' })
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
    const issues = replayReadinessIssues(preflight({ issues: [
      'teardown[0] "Say \\"bye\\"": execution blocker encrypted_proxy_password, bruno_missing_variables (a, b) blocks replay until the imported behavior is resolved',
    ] }))
    // One problem per code, as the import review counts them; a comma inside
    // a code's detail does not split it.
    expect(issues.map(issue => [issue.title, issue.codes, issue.location])).toEqual([
      ['Replace encrypted proxy password', ['encrypted_proxy_password'], { phase: 'teardown', actionIndex: 0 }],
      ['Bruno missing variables', ['bruno_missing_variables'], { phase: 'teardown', actionIndex: 0 }],
    ])
    expect(issues[1].description).toBe('Execution blocker bruno_missing_variables (a, b) blocks replay until the imported behavior is resolved')
  })

  it('counts a gate with several codes as one blocker per code, as the import review does', () => {
    const message = 'actions[2] "Submit": execution blocker restored_body_required, request_body_xml_json_content_type_mismatch blocks replay until the imported behavior is resolved'
    const issues = replayReadinessIssues(preflight({ issues: [message] }))
    expect(issues.map(issue => [issue.id, issue.severity, issue.title, issue.codes, issue.actionPath])).toEqual([
      [`readiness:issue:${message.toLowerCase()}#restored_body_required`, 'blocker', 'Restore protected request body', ['restored_body_required'], 'actions[2]'],
      [`readiness:issue:${message.toLowerCase()}#request_body_xml_json_content_type_mismatch`, 'blocker', 'Body is XML but Content-Type declares JSON', ['request_body_xml_json_content_type_mismatch'], 'actions[2]'],
    ])
    expect(issues.every(issue => issue.location?.phase === 'action' && issue.location.actionIndex === 2)).toBe(true)
    // The same draft in the import review: two blockers there too.
    const script = { name: 'Two codes', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [
      { name: 'Home', type: 'GET', target: '/' },
      { name: 'Cart', type: 'GET', target: '/cart' },
      { name: 'Submit', type: 'POST', method: 'POST', target: '/submit', metadata: { execution_blockers: ['restored_body_required', 'request_body_xml_json_content_type_mismatch'] } },
    ] } as ScriptDefinition
    const imported = readinessCounts(groupImportFindings(draftImportFindings(script), script))
    const editor = replayReadinessIssues(buildLocalReplayPreflight(script)).filter(issue => issue.severity === 'blocker')
    expect({ imported: imported.blockers, editor: editor.length }).toEqual({ imported: 2, editor: 2 })
    // The import review folds a generic review into a body restoration on the same step.
    const folded = replayReadinessIssues(preflight({ issues: ['actions[2] "Submit": execution blocker import_review_required, restored_body_required blocks replay until the imported behavior is resolved'] }))
    expect(folded.map(issue => issue.codes)).toEqual([['restored_body_required']])
    // The same rule, for a step's own codes (the repair panel).
    expect(distinctBlockerCodes(['import_review_required', 'restored_body_required'])).toEqual(['restored_body_required'])
    expect(distinctBlockerCodes(['import_review_required', 'legacy_tls_replacement'])).toEqual(['import_review_required', 'legacy_tls_replacement'])
  })

  it('tells a saved script to import its source again to restore a protected body, not to supply it below', () => {
    const reimport = 'The original request body could not be restored from the source. A protected request body can be restored only while importing: import the source again to restore it.'
    const [issue] = replayReadinessIssues(preflight({ issues: ['actions[2] "Submit": execution blocker restored_body_required blocks replay until the imported behavior is resolved'] }))
    expect(issue.title).toBe('Restore protected request body')
    expect(issue.description).toBe(`${reimport} Debug and test runs refuse this script until it is resolved.`)
    expect(issue.description).not.toContain('Supply the body below')
    // The same words for a nested request, after its path.
    expect(readinessIssueText('actions[1].params.requests[0] "Pay": execution blocker restored_body_required blocks replay until the imported behavior is resolved').description)
      .toBe(`actions[1].params.requests[0] "Pay": ${reimport} Debug and test runs refuse this script until it is resolved.`)
    // The import review keeps its body form; every other code reads as it does there.
    expect(storedScriptImpactText('restored_body_required')).toBe(reimport)
    expect(repairImpactText('restored_body_required')).toContain('Supply the body below')
    for (const code of ['legacy_tls_replacement', 'missing_producer:token', 'conditional_producer:token', 'encrypted_proxy_password', 'bruno_missing_variables']) {
      expect(storedScriptImpactText(code)).toBe(repairImpactText(code))
    }
    expect(storedScriptImpactText('request_body_xml_json_content_type_mismatch', 'Update the header.')).toBe('Update the header.')
  })

  it('tells an unsaved draft opened from the import review to return there for a protected body', () => {
    const review = 'The original request body could not be restored from the source. A protected request body can be restored only while importing: return to the import review to supply the protected body.'
    const message = 'actions[2] "Submit": execution blocker restored_body_required blocks replay until the imported behavior is resolved'
    const [issue] = replayReadinessIssues(preflight({ issues: [message] }), [], new Map(), undefined, 'import-review')
    expect(issue.title).toBe('Restore protected request body')
    expect(issue.description).toBe(`${review} Debug and test runs refuse this script until it is resolved.`)
    expect(issue.description).not.toContain('import the source again')
    // Each code of a gate with several reads the same way, as does a nested request after its path.
    const twoCodes = replayReadinessIssues(preflight({ issues: ['actions[2] "Submit": execution blocker restored_body_required, request_body_xml_json_content_type_mismatch blocks replay until the imported behavior is resolved'] }), [], new Map(), undefined, 'import-review')
    expect(twoCodes.find(item => item.codes?.[0] === 'restored_body_required')?.description).toBe(`${review} Debug and test runs refuse this script until it is resolved.`)
    expect(readinessIssueText('actions[1].params.requests[0] "Pay": execution blocker restored_body_required blocks replay until the imported behavior is resolved', 'import-review').description)
      .toBe(`actions[1].params.requests[0] "Pay": ${review} Debug and test runs refuse this script until it is resolved.`)
    expect(storedScriptImpactText('restored_body_required', '', 'import-review')).toBe(review)
    // Every other code reads as it does for a saved script.
    for (const code of ['legacy_tls_replacement', 'missing_producer:token', 'encrypted_proxy_password']) {
      expect(storedScriptImpactText(code, '', 'import-review')).toBe(storedScriptImpactText(code))
    }
  })

  it('locates a gate on a nested step by the step path the outline gives it, given the script', () => {
    const script = { name: 'Nested', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [
      { name: 'Checkout', type: 'http_sequence', target: '', params: { requests: [{ name: 'Cart', type: 'GET', target: '/cart' }, { name: 'Pay', type: 'POST', target: '/pay' }] } },
      { name: 'Retry', type: 'control_loop', target: '', control: { steps: [{ name: 'Poll', type: 'GET', target: '/poll' }] } },
    ] } as ScriptDefinition
    const facts = preflight({
      issues: [
        'actions[0].params.requests[1] "Pay": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved',
        'actions[1].control.steps[0] "Poll": execution blocker missing_producer:sid blocks replay until the imported behavior is resolved',
      ],
      blocking_variables: ['sid'],
    })
    const issues = replayReadinessIssues(facts, [], new Map(), script)
    expect(issues.map(issue => [issue.id.split(':')[1], issue.location, issue.actionPath])).toEqual([
      ['issue', { phase: 'action', actionIndex: 0, stepPath: ['requests[1]'] }, 'actions[0].params.requests[1]'],
      ['blocking', { phase: 'action', actionIndex: 1, stepPath: ['loop[0]'] }, 'actions[1].control.steps[0]'],
    ])
    // Without the script, or for a path the script does not have, only the action.
    expect(replayReadinessIssues(facts)[0].location).toEqual({ phase: 'action', actionIndex: 0 })
    expect(replayReadinessIssues(preflight({ issues: ['actions[0].params.requests[7] "Gone": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved'] }), [], new Map(), script)[0].location).toEqual({ phase: 'action', actionIndex: 0 })
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

  it('names the codes and step a guided repair addresses, a nested request included', () => {
    const nested = 'actions[1].params.requests[0] "Pay": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved'
    const [gate] = replayReadinessIssues(preflight({ issues: [nested] }))
    expect(gate).toMatchObject({ codes: ['legacy_tls_replacement'], actionPath: 'actions[1].params.requests[0]', location: { phase: 'action', actionIndex: 1 } })
    // The nested request's own path stays in view before what the blocker means.
    expect(gate.description).toBe('actions[1].params.requests[0] "Pay": The source uses legacy TLS behavior. Choose a supported replacement and preview the migration. Debug and test runs refuse this script until it is resolved.')
    // A gate that is not an execution blocker carries no codes to repair.
    const [review] = replayReadinessIssues(preflight({ issues: ['actions[0] "Pay": review-required action blocks replay until review metadata is removed'] }))
    expect(review.codes).toBeUndefined()
    expect(review.actionPath).toBeUndefined()
  })

  it('keeps the gate\'s own words for a code with nothing specific to say', () => {
    const [gate] = replayReadinessIssues(preflight({ issues: ['actions[0] "Pay": execution blocker bruno_missing_variables (a, b) blocks replay until the imported behavior is resolved'] }))
    expect(gate.description).toBe('Execution blocker bruno_missing_variables (a, b) blocks replay until the imported behavior is resolved')
  })

  it('gives a gated value row the dependency code and step of its first gate', () => {
    const [value] = replayReadinessIssues(preflight({
      issues: ['actions[2] "Cart": execution blocker missing_producer:region blocks replay until the imported behavior is resolved'],
      missing_variables: ['region'],
      blocking_variables: ['region'],
    }))
    expect(value).toMatchObject({ id: 'readiness:blocking:region', codes: ['missing_producer:region'], actionPath: 'actions[2]', location: { phase: 'action', actionIndex: 2 } })
  })

  it('keeps a configuration reference off the step a gate on the same name sits on', () => {
    const issues = replayReadinessIssues(preflight({
      issues: ['actions[2] "Cart": execution blocker missing_producer:host blocks replay until the imported behavior is resolved'],
      missing_variables: ['host'],
      blocking_variables: ['host'],
      config_blocking_variables: ['host'],
    }))
    // One problem, as the importer groups it, replaced in Runtime Settings:
    // no step location, codes or path for a step repair to open.
    expect(issues).toHaveLength(1)
    expect(issues[0]).toMatchObject({ id: 'readiness:blocking:host', title: 'Replace configuration reference: host', config: true })
    expect(issues[0].location).toBeUndefined()
    expect(issues[0].codes).toBeUndefined()
    expect(issues[0].actionPath).toBeUndefined()
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

  it('keeps the preflight\'s own words on a static issue, so the editor can tell when its own check lists it', () => {
    const [missing, gate] = replayReadinessIssues(preflight({ issues: [' actions[2] "Login": missing target ', 'init[0] "web_set_sockets_option_0": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved'] }))
    expect(missing).toMatchObject({ title: 'Missing target', location: { phase: 'action', actionIndex: 2 }, message: 'actions[2] "Login": missing target' })
    // A gate is a problem only the preflight knows.
    expect(gate.message).toBeUndefined()
  })

  it('skips messages the editor already lists from server validation', () => {
    const issues = replayReadinessIssues(preflight({ issues: ['Script name is required.', 'teardown[0] "Logout": blocked'] }), ['script name is  required.'])
    expect(issues.map(issue => [issue.title, issue.location?.phase])).toEqual([['Blocked', 'teardown']])
  })

  it('titles a value some step sets too late as the importer titles producer order', () => {
    const issues = replayReadinessIssues(preflight({
      missing_variables: ['C_StoreId', 'c_cart_body', 'c_promo'],
      blocking_variables: ['c_cart_body'],
      placeholder_variables: ['C_StoreId', 'c_promo'],
      late_produced_variables: ['C_StoreId', 'c_cart_body'],
    }))
    expect(issues.map(issue => [issue.severity, issue.title, issue.variable])).toEqual([
      ['blocker', 'Check producer order: c_cart_body', 'c_cart_body'],
      ['warning', 'Check producer order: C_StoreId', 'C_StoreId'],
      ['warning', 'Missing value: c_promo', 'c_promo'],
    ])
    expect(issues[0].description).toBe('Producer ordering or conditional execution for c_cart_body requires review. A step sets it, but not before every use, and replay refuses to run without it.')
    expect(issues[1].description).toBe('Producer ordering or conditional execution for C_StoreId requires review. A step sets it, but not before every use, so replay can send the placeholder text.')
    expect(issues[2].description).toBe('Nothing provides it before use, so replay sends the placeholder text.')
  })

  it('lists a gated blocking value once, at its first gated step, as the importer groups it', () => {
    const gate = (path: string, codes: string) => `${path} "step": execution blocker ${codes} blocks replay until the imported behavior is resolved`
    const issues = replayReadinessIssues(preflight({
      issues: [
        gate('actions[1]', 'conditional_producer:c_cart_body'),
        gate('actions[3]', 'conditional_producer:c_cart_body'),
        gate('actions[2]', 'missing_producer:region'),
        // A stamped gate for a value replay no longer misses still blocks,
        // until a save re-evaluates it.
        gate('actions[4]', 'missing_producer:stale'),
        gate('init[0]', 'missing_producer:region, legacy_tls_replacement'),
      ],
      missing_variables: ['c_cart_body', 'region'],
      blocking_variables: ['c_cart_body', 'region'],
      late_produced_variables: ['c_cart_body'],
    }))
    // The init gate's value is the value row's; its other code is its own blocker.
    expect(issues.map(issue => [issue.title, issue.location])).toEqual([
      ['Save to update the import review for stale', { phase: 'action', actionIndex: 4 }],
      ['Choose supported TLS settings', { phase: 'init', actionIndex: 0 }],
      ['Check producer order: c_cart_body', { phase: 'action', actionIndex: 1 }],
      ['Unresolved value: region', { phase: 'action', actionIndex: 2 }],
    ])
  })

  it('lists the review codes for a value the draft now supplies once, as the save that re-evaluates them', () => {
    const script = { name: 'Supplied', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [
      { name: 'Confirm', type: 'POST', target: '/confirm' },
      { name: 'Retry', type: 'control_loop', target: '', control: { steps: [{ name: 'Poll', type: 'GET', target: '/poll' }] } },
    ] } as ScriptDefinition
    const gate = (path: string, name: string) => `${path} "${name}": execution blocker missing_producer:sid blocks replay until the imported behavior is resolved`
    const issues = [gate('actions[0]', 'Confirm'), gate('actions[1].control.steps[0]', 'Poll')]
    const supplied = { issues, missing_variables: [], blocking_variables: [], placeholder_variables: [] }
    expect(replayReadinessIssues(preflight(supplied), [], new Map(), script)).toEqual([{
      id: 'readiness:saved-review:sid',
      severity: 'blocker',
      title: 'Save to update the import review for sid',
      description: 'The draft now supplies sid, but 2 steps still carry the import review codes that name it missing. Saving re-evaluates their review codes. Debug and test runs refuse this script until it is resolved.',
      location: { phase: 'action', actionIndex: 0 },
      codes: ['missing_producer:sid'],
      actionPath: 'actions[0]',
      variable: 'sid',
      clearedBySave: true,
    }])
    expect(replayReadinessIssues(preflight({ ...supplied, issues: [issues[1]] }))[0].description).toBe('The draft now supplies sid, but a step still carries the import review code that names it missing. Saving re-evaluates the step\'s review code. Debug and test runs refuse this script until it is resolved.')
    // A value replay still misses is the missing value it was, at each gate.
    const blockers = (facts: Partial<ReplayPreflight>) => replayReadinessIssues(preflight(facts)).filter(issue => issue.severity === 'blocker').map(issue => issue.title)
    expect(blockers({ ...supplied, missing_variables: ['sid'], placeholder_variables: ['sid'] })).toEqual(['Missing value: sid', 'Missing value: sid'])
    // A preflight that reports no values cannot say the draft supplies one.
    expect(blockers({ issues })).toEqual(['Missing value: sid', 'Missing value: sid'])
  })

  it('words a value only a parameter or a literal configuration value clears by that fix, before producer order', () => {
    const facts = {
      missing_variables: ['host', 'jmeter_global_counter', 'region', 'token'],
      blocking_variables: ['host', 'jmeter_global_counter', 'region', 'token'],
      late_produced_variables: ['host', 'region'],
      param_blocking_variables: ['jmeter_global_counter', 'region'],
      config_blocking_variables: ['host'],
    }
    const rows = (issues: ReturnType<typeof replayReadinessIssues>) => issues.map(issue => [issue.title, issue.description])
    const configured = 'Protocol configuration references host. Configuration is applied before any step runs, so replay refuses it. Replace the reference with a literal configuration value.'
    // Only a declared parameter resolves {{param.*}}: a data source alone publishes data.<source>.<column>.
    const parameter = (name: string) => `No parameter supplies ${name}, and replay refuses to run with an unresolved parameter. Declare a script parameter named ${name} (it can read its values from a data source).`
    const generic = 'Replay refuses to run until a parameter, an extractor or a session variable provides it.'
    // Only a configuration reference is marked as one: Runtime Settings, not a step, holds it.
    expect(replayReadinessIssues(preflight(facts)).map(issue => [issue.variable, issue.config === true])).toEqual([['host', true], ['jmeter_global_counter', false], ['region', false], ['token', false]])
    expect(rows(replayReadinessIssues(preflight(facts)))).toEqual([
      ['Replace configuration reference: host', configured],
      ['Unresolved value: jmeter_global_counter', parameter('jmeter_global_counter')],
      ['Unresolved value: region', parameter('region')],
      ['Unresolved value: token', generic],
    ])
    // A server preflight does not list them, so they come from the script.
    const script: ScriptDefinition = { name: 's', version: '1', protocol: 'http', config: { base_url: 'https://{{var.host}}' }, actions: [
      { name: 'early', type: 'GET', method: 'GET', target: 'https://bootstrap.example.test/{{var.host}}/{{var.region}}/{{param.region}}/{{param.jmeter_global_counter}}' },
      { name: 'save', type: 'source_parameter', target: '', params: { function: 'lr_save_string', args: ['eu', 'region'] } },
      { name: 'save host', type: 'source_parameter', target: '', params: { function: 'lr_save_string', args: ['api.example.test', 'host'] } },
      { name: 'relative', type: 'GET', method: 'GET', target: '/orders' },
    ] }
    const serverFacts = { ...facts, param_blocking_variables: undefined, config_blocking_variables: undefined }
    expect(rows(replayReadinessIssues(preflight(serverFacts), [], new Map(), script))).toEqual(rows(replayReadinessIssues(preflight(facts))))
    // Without either, an older controller's rows keep their wording.
    expect(rows(replayReadinessIssues(preflight(serverFacts)))).toEqual([
      ['Check producer order: host', 'Producer ordering or conditional execution for host requires review. A step sets it, but not before every use, and replay refuses to run without it.'],
      ['Unresolved value: jmeter_global_counter', generic],
      ['Check producer order: region', 'Producer ordering or conditional execution for region requires review. A step sets it, but not before every use, and replay refuses to run without it.'],
      ['Unresolved value: token', generic],
    ])
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

describe('readinessIssueText', () => {
  it('titles a gate by what to do about it, and leaves other text alone', () => {
    expect(readinessIssueText('init[0] "web_set_sockets_option_15": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved').title).toBe('Choose supported TLS settings')
    expect(readinessIssueText('Script name is required.')).toEqual({ title: 'Script name is required.' })
  })
})

describe('reviewGateBreadcrumb', () => {
  it('names the phase and the step, or its number when the step is unnamed', () => {
    expect(reviewGateBreadcrumb('init[0] "web_set_sockets_option_15": execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved')).toBe('Init › web_set_sockets_option_15')
    expect(reviewGateBreadcrumb('teardown[1] "Say \\"bye\\"": execution blocker import_review_required blocks replay until the imported behavior is resolved')).toBe('Teardown › Say "bye"')
    expect(reviewGateBreadcrumb('actions[3]: execution blocker legacy_tls_replacement blocks replay until the imported behavior is resolved')).toBe('Actions › Action 4')
    expect(reviewGateBreadcrumb('Script name is required.')).toBeUndefined()
  })
})

describe('replayRefusalSummary', () => {
  const blocker = (title: string, where?: string) => ({ title, where })
  const where = (item: { where?: string }) => item.where

  it('names up to three blockers with where they sit and counts the rest', () => {
    expect(replayRefusalSummary([blocker('Choose supported TLS settings', 'Init › web_set_sockets_option_15'), blocker('Choose supported TLS settings', 'Init › web_set_sockets_option_22')], where))
      .toBe('Debug did not start: 2 problems block replay — Choose supported TLS settings (Init › web_set_sockets_option_15); Choose supported TLS settings (Init › web_set_sockets_option_22).')
    expect(replayRefusalSummary(['a', 'b', 'c', 'd', 'e'].map(title => blocker(`Fix ${title}`)), where))
      .toBe('Debug did not start: 5 problems block replay — Fix a; Fix b; Fix c; +2 more.')
    expect(replayRefusalSummary([blocker('Configure project secret: TOKEN')]))
      .toBe('Debug did not start: 1 problem blocks replay — Configure project secret: TOKEN.')
    expect(replayRefusalSummary([])).toBe('Debug did not start.')
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
