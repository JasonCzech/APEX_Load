import { describe, expect, it } from 'vitest'
import type { ReplayPreflight, ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { actionImportFindings, buildRepairOperation, coveredWarnings, draftImportFindings, findingCategory, groupImportFindings, importActionEntries, importReviewFindings, isConfigReferenceIssue, issueRepairOptions, issueTitle, readinessCounts, readinessSummaryText, repairImpactText, reviewSectionFindings, storedRepairOptions, unresolvedIssueCount, warningIssueTitle, type ImportFinding, type ImportIssue } from './importReviewIssues'
import { buildLocalReplayPreflight } from './replayPreflightUtils'
import { replayReadinessIssues } from './replayReadinessIssues'
import { replayBlockers, replayGateAction, replayStartGate } from './replayStartGate'
const script = { name: 'Review', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [] } as ScriptDefinition

describe('import issue queue', () => {
  it('groups repeated consumers without conflating namespaces, and control branches only for a value the server gates', () => {
    const s = { ...script, actions: [
      { name: 'one', type: 'POST', target: '/', body: '{{var.token}}' },
      { name: 'two', type: 'POST', target: '/', body: '{{var.token}}' },
      { name: 'parameter', type: 'POST', target: '/', body: '{{param.token}}' },
      { name: 'branch', type: 'control_if', target: '', control: { steps: [{ name: 'nested', type: 'POST', target: '/', body: '{{var.token}}' }] } },
    ] } as ScriptDefinition
    const findings = ['actions[0]', 'actions[1]', 'actions[2]', 'actions[3].control.steps[0]'].map(action_path => ({ code: 'missing_producer:token', action_path, detail: '', gate: 'execution' }))
    // A gated value is one problem script-wide, as the editor counts it: the
    // branch consumer is one more occurrence of the {{var.token}} issue.
    const groups = groupImportFindings([...findings, findings[0]], s)
    expect(groups).toHaveLength(2)
    expect(groups.map(g => g.findings.length).sort()).toEqual([1, 3])
    expect(groups[0].title).toBe('Missing value: token')
    // A value replay only sends as placeholder text keeps its branches apart.
    const ungated = groupImportFindings(findings.map(finding => ({ ...finding, gate: 'promotion' })), s)
    expect(ungated).toHaveLength(3)
    expect(ungated.map(g => g.findings.length).sort()).toEqual([1, 1, 2])
  })
  it('counts a gated value consumed in two control scopes once in the import review and the start gate', () => {
    // An undeclared {{param.*}} refuses replay, so the editor lists sid as a blocking value.
    const s = { ...script, actions: [
      { name: 'Confirm', type: 'POST', method: 'POST', target: '/confirm/{{param.sid}}', metadata: { execution_blockers: ['missing_producer:sid'] } },
      { name: 'Retry', type: 'control_loop', target: '', control: { count: 2, steps: [
        { name: 'Poll', type: 'GET', method: 'GET', target: '/poll/{{param.sid}}', metadata: { execution_blockers: ['missing_producer:sid'] } },
      ] } },
    ] } as ScriptDefinition
    const issues = groupImportFindings(draftImportFindings(s), s)
    const blockers = replayBlockers(replayReadinessIssues(buildLocalReplayPreflight(s), [], new Map(), s), [])
    expect({ wizard: readinessCounts(issues).blockers, startGate: blockers.length }).toEqual({ wizard: 1, startGate: 1 })
    expect(replayGateAction(replayStartGate({ replaying: false, saving: false, hasName: true, actionCount: 2, jsonInvalid: false, secretStatus: 'known', blockers, storedGateIssues: [] }), { hasReport: false, readyTitle: '' }).label).toBe('Resolve 1 blocker')
    // Each consumer stays an occurrence of its own, for a repair of each.
    expect(issues[0].findings.map(finding => finding.action_path)).toEqual(['actions[0]', 'actions[1].control.steps[0]'])
  })
  it('blocks a missing producer only when the server gates execution', () => {
    const groups = groupImportFindings([
      { code: 'missing_producer:storeId', action_path: 'actions[0]', detail: '', gate: 'promotion', severity: 'warning' },
      { code: 'missing_producer:document', action_path: 'actions[1]', detail: '', gate: 'execution', severity: 'blocker' },
    ], script)
    expect(Object.fromEntries(groups.map(g => [g.title, g.blocking]))).toEqual({ 'Missing value: storeId': false, 'Missing value: document': true })
  })
  it('merges generic body evidence but retains independent bodies and blockers', () => {
    const findings = ['actions[0]', 'actions[1]'].flatMap(action_path => [
      { code: 'restored_body_required', action_path, detail: '', gate: 'execution' },
      { code: 'import_review_required', action_path, detail: '', gate: 'promotion' },
    ])
    const groups = groupImportFindings(findings, script)
    expect(groups).toHaveLength(2)
    expect(groups.every(g => g.blocking)).toBe(true)
  })
  it('reads nested request metadata while preserving review-only severity', () => {
    const s = { ...script, actions: [{ name: 'sequence', type: 'http_sequence', target: '', params: { requests: [{ name: 'request', metadata: { execution_blockers: ['missing_producer:token'] } }] } }, { name: 'review', type: 'source_review', target: '', metadata: { review_reasons: ['conditional_producer:later'] } }] } as ScriptDefinition
    const findings = draftImportFindings(s)
    expect(findings[0].action_path).toBe('actions[0].params.requests[0]')
    expect(groupImportFindings(findings, s).find(g => g.title.includes('later'))?.blocking).toBe(false)
  })
  it('keeps findings inside parallel branches discoverable', () => {
    const s = { ...script, actions: [{ name: 'parallel', type: 'control_parallel', target: '', control: { branches: [{ name: 'one', steps: [{ name: 'nested', type: 'request', target: '/', metadata: { execution_blockers: ['missing_producer:branchValue'] } }] }] } }] } as ScriptDefinition
    expect(draftImportFindings(s)[0].action_path).toBe('actions[0].control.branches[0].steps[0]')
  })
  it('only offers repairs that match the finding', () => {
    expect(issueRepairOptions('missing_producer:tstamp').map(o => o.value)).toEqual(['bind_variable', 'add_parameter', 'add_extractor'])
    expect(issueRepairOptions('restored_body_required').map(o => o.value)).toEqual(['restore_body'])
    expect(issueRepairOptions('legacy_tls_replacement').map(o => o.value)).toEqual(['select_tls'])
    expect(issueRepairOptions('unknown_unsupported')).toEqual([])
  })
  it('offers a saved script every repair but restoring a protected body', () => {
    for (const code of ['missing_producer:tstamp', 'conditional_producer:tstamp', 'encrypted_proxy_password', 'legacy_tls_replacement']) {
      expect(storedRepairOptions(code)).toEqual(issueRepairOptions(code))
    }
    expect(storedRepairOptions('restored_body_required')).toEqual([])
    expect(storedRepairOptions('unknown_unsupported')).toEqual([])
  })
  it('builds the operation each repair sends, the same for the import review and the editor', () => {
    const fields = { value: '', responsePath: '', extractorType: 'jsonpath' }
    const value = { code: 'missing_producer:c_token' }
    expect(buildRepairOperation('select_tls', { code: 'legacy_tls_replacement' }, fields)).toEqual({ type: 'select_tls', tls_min: '1.2', tls_max: '1.3' })
    expect(buildRepairOperation('select_tls', { code: 'legacy_tls_replacement' }, { ...fields, value: '1.3' })).toEqual({ type: 'select_tls', tls_min: '1.3', tls_max: '1.3' })
    // An operation the form does not know falls back to TLS, as it always has.
    expect(buildRepairOperation('', undefined, fields)).toEqual({ type: 'select_tls', tls_min: '1.2', tls_max: '1.3' })
    expect(buildRepairOperation('restore_body', { code: 'restored_body_required' }, { ...fields, value: '{"a":1}' })).toEqual({ type: 'restore_body', body: '{"a":1}' })
    expect(buildRepairOperation('replace_proxy_secret', { code: 'encrypted_proxy_password' }, { ...fields, value: 'PROXY_PASSWORD' })).toEqual({ type: 'replace_proxy_secret', secret_name: 'PROXY_PASSWORD' })
    expect(buildRepairOperation('bind_variable', value, { ...fields, value: 'existingToken' })).toEqual({ type: 'bind_variable', producer: 'existingToken' })
    expect(buildRepairOperation('add_parameter', value, { ...fields, value: 'one\ntwo' })).toEqual({
      type: 'add_parameter', parameter: { name: 'c_token', type: 'list', values: ['one', 'two'], update: 'once', selection: 'sequential' },
    })
    expect(buildRepairOperation('add_extractor', { code: 'conditional_producer:ns:token' }, { value: '$.token', responsePath: 'actions[0]', extractorType: 'regex' })).toEqual({
      type: 'add_extractor', response_path: 'actions[0]', extractor: { name: 'ns:token', type: 'regex', source: 'body', expression: '$.token', on_miss: 'fail' },
    })
  })
  it('reads one step\'s gates as the draft reads them', () => {
    const s = { ...script, init: [{ name: 'Socket option', type: 'source_review', target: 'web_set_sockets_option', metadata: { execution_blocker: 'legacy_tls_replacement', review_reasons: ['import_review_required'], source_file: 'vuser_init.c', source_line: 3 } }],
      actions: [{ name: 'sequence', type: 'http_sequence', target: '', params: { requests: [{ name: 'child', type: 'POST', target: '/', metadata: { execution_blockers: ['missing_producer:token'], import_review: { source_refs: ['Action.c:9'] } } }] } }] } as ScriptDefinition
    for (const { action, path } of importActionEntries(s)) {
      expect(actionImportFindings(action, path)).toEqual(draftImportFindings(s).filter(finding => finding.action_path === path))
    }
    expect(actionImportFindings(s.init![0], 'init[0]')).toEqual([
      { code: 'legacy_tls_replacement', gate: 'execution', source_ref: 'vuser_init.c:3', action_path: 'init[0]', detail: '' },
      { code: 'import_review_required', gate: 'promotion', source_ref: 'vuser_init.c:3', action_path: 'init[0]', detail: '' },
    ])
    expect(actionImportFindings((s.actions[0].params!.requests as ScriptActionDefinition[])[0], 'actions[0].params.requests[0]')[0]).toMatchObject({ code: 'missing_producer:token', source_ref: 'Action.c:9' })
  })
  it('identifies the Content-Type header to fix without misclassifying malformed JSON', () => {
    const s = { ...script, actions: [{ name: 'SOAP request', type: 'POST', target: '/', metadata: {
      execution_blocker: 'request_body_xml_json_content_type_mismatch', source_file: 'Action.c', source_line: 7,
    } }] } as ScriptDefinition
    const findings = draftImportFindings(s)
    expect(findings[0].detail).toContain('The request body is XML, but the Content-Type header declares JSON.')
    expect(findings[0].detail).toContain("Update this request's Content-Type header")
    expect(findings[0].detail).toContain('then re-import and validate')
    expect(groupImportFindings(findings, s)[0]).toMatchObject({
      title: 'Body is XML but Content-Type declares JSON — Action.c:7', category: 'review', blocking: true,
    })
    expect(issueRepairOptions(findings[0].code)).toEqual([])
    expect(issueTitle('credential_json_malformed')).not.toContain('XML')
  })
})

it('preserves warning details and only focuses an unambiguous source mapping', async () => {
  const { reviewSectionFindings, issueScope } = await import('./importReviewIssues')
  const sections = [{ key: 'warnings', title: 'Conversion warnings', severity: 'warning', summary: 'Review warnings', items: ['line 28: web_reg_find Search=ALL checks the main response body only', 'Unknown source construct'] }]
  const mapping = { id: 'assertion', source_file: 'Action.c', start_line: 28, end_line: 30, target_path: 'actions[1]', status: 'converted' }
  const findings = reviewSectionFindings(sections, { mappings: [mapping] })
  expect(findings[0]).toMatchObject({ title: 'Text check searches a narrower response scope', source_mapping_id: 'assertion', source_ref: 'Action.c:28', action_path: 'actions[1]' })
  expect(findings[1].detail).toBe('Unknown source construct')
  expect(reviewSectionFindings([{ ...sections[0], items: [...sections[0].items].reverse() }])[0].code).toBe(findings[1].code)
  expect(groupImportFindings(findings, script)).toHaveLength(2)
  expect(issueScope(groupImportFindings([findings[1]], script)[0])).toBe('Script-wide')
  const ambiguous = reviewSectionFindings(sections, { mappings: [mapping, { ...mapping, id: 'other', source_file: 'Other.c' }] })
  expect(ambiguous[0].source_mapping_id).toBeUndefined()
  expect(ambiguous[0].action_path).toBeUndefined()
  expect(reviewSectionFindings([])).toEqual([])
})

it('retains runtime evidence and summarizes changed paths without exposing values', async () => {
  const { reviewSectionFindings, changedRepairPaths } = await import('./importReviewIssues')
  const findings = reviewSectionFindings([{ key: 'runtime_settings_parity', title: 'Runtime differences', summary: 'One setting differs', severity: 'warning', items: ['Partial: HTTP / buffer size'] }])
  expect(findings[0].evidence).toEqual(['Partial: HTTP / buffer size'])
  expect(changedRepairPaths({ actions: [{ body: 'secret-before' }], config: { n: 0 } }, { actions: [{ body: 'secret-after' }], config: { n: 1 } })).toEqual(['actions[0].body', 'config.n'])
  expect(changedRepairPaths({}, {})).toEqual([])
  expect(changedRepairPaths(null, { body: 'secret' })).toEqual(['Script'])
})

it('leaves out of the changed paths the records the server rewrites after every repair', async () => {
  const { changedRepairPaths } = await import('./importReviewIssues')
  // What evaluateRepairedScript and the candidate store restamp, beside the
  // protocol report the import wrote, which no repair rewrites.
  const bookkeeping = (stamp: 'before' | 'after') => ({
    fidelity_report: { promotion_status: stamp === 'after' ? 'promotable' : 'blocked', blockers: stamp === 'after' ? [] : ['legacy_tls_replacement'], secret_gated: 0 },
    replay_preflight: { valid: stamp === 'after', issues: stamp === 'after' ? [] : ['init[0] "tls": execution blocker legacy_tls_replacement'] },
    protocol_compat_report: { review_gated_steps: 1 },
    guided_repair_candidate: stamp === 'after',
    server_import_attestation_v1: `protected:${stamp}`,
  })
  const before = {
    config: { http: {} },
    init: [{ name: 'tls', type: 'source_review', metadata: { execution_blocker: 'legacy_tls_replacement' } }],
    metadata: { import: { source: 'enterprise_script_archive', preserved: { ...bookkeeping('before'), source_files: ['vuser_init.c'] } } },
  }
  const after = {
    config: { http: { tls: { min_version: '1.2', max_version: '1.3' } } },
    init: [{ name: 'tls', type: 'source_review', metadata: { migration_change: 'Operator selected TLS 1.2–1.3' } }],
    metadata: { import: { source: 'enterprise_script_archive', preserved: { ...bookkeeping('after'), source_files: ['vuser_init.c', 'Action.c'] } } },
  }
  // The executable, config and step changes stay listed, and so does any
  // other preserved import record.
  expect(changedRepairPaths(before, after)).toEqual([
    'config.http.tls',
    'init[0].metadata.execution_blocker',
    'init[0].metadata.migration_change',
    'metadata.import.preserved.source_files[1]',
  ])
  // A repair that only restamps the bookkeeping lists nothing, also for a
  // draft that had no import metadata before.
  expect(changedRepairPaths({ ...before, metadata: { import: { preserved: bookkeeping('before') } } }, { ...before, metadata: { import: { preserved: bookkeeping('after') } } })).toEqual([])
  expect(changedRepairPaths({ actions: [] }, { actions: [], metadata: { import: { preserved: bookkeeping('after') } } })).toEqual([])
  expect(changedRepairPaths({ actions: [], metadata: { import: { preserved: bookkeeping('before') } } }, { actions: [] })).toEqual([])
  // The protocol report is left out defensively: a difference in it, which no
  // repair makes, is still not listed as a repair's change.
  expect(changedRepairPaths(
    { actions: [], metadata: { import: { preserved: { protocol_compat_report: { review_gated_steps: 1 } } } } },
    { actions: [], metadata: { import: { preserved: { protocol_compat_report: { review_gated_steps: 0 } } } } },
  )).toEqual([])
  // The same names anywhere else are the script's own fields.
  expect(changedRepairPaths(
    { config: { fidelity_report: 1 }, actions: [{ metadata: { replay_preflight: 'a' } }], metadata: { replay_preflight: 'a' } },
    { config: { fidelity_report: 2 }, actions: [{ metadata: { replay_preflight: 'b' } }], metadata: { replay_preflight: 'b' } },
  )).toEqual(['config.fidelity_report', 'actions[0].metadata.replay_preflight', 'metadata.replay_preflight'])
})

 it('consolidates runtime evidence and duplicate warnings without hiding blocking coverage', async () => {
  const { reviewSectionFindings } = await import('./importReviewIssues')
  const parity = { title: 'Runtime parity', key: 'runtime_settings_parity', severity: 'warning', summary: 'Partial parity', items: ['Buffer'] }
  const coverage = { title: 'Runtime coverage', key: 'runtime_settings_migration_readiness', severity: 'warning', summary: 'Preserved settings', items: ['Timeout'] }
  const warnings = { title: 'Warnings', key: 'warnings', severity: 'warning', summary: '', items: ['line 2: web_reg_find Search=ALL', 'line 2: web_reg_find Search=ALL'] }
  const findings = reviewSectionFindings([parity, coverage, warnings])
  expect(findings).toHaveLength(2)
  expect(findings[0].evidence).toEqual(['Buffer', 'Preserved settings', 'Timeout'])
  expect(reviewSectionFindings([parity, { ...coverage, severity: 'blocker' }])).toHaveLength(2)
  const mapping = { id: 'assertion', function: 'web_reg_find', source_file: 'Action.c', start_line: 2, end_line: 2, status: 'contributor' }
  expect(reviewSectionFindings([warnings], { mappings: [mapping, { ...mapping, id: 'request', function: 'web_url', source_file: 'Other.c' }] })[0].source_mapping_id).toBe('assertion')
 })

describe('repairImpactText', () => {
  it('says what a finding means for replay once, for the import review and the editor', () => {
    expect(repairImpactText('missing_producer:token')).toBe('This value has no reachable producer. Bind an existing value, add test data, or extract it from a preceding response.')
    expect(repairImpactText('conditional_producer:token')).toBe('A possible producer exists, but its order or execution path needs review. It must run before the selected request.')
    expect(repairImpactText('restored_body_required')).toBe('The original payload could not be restored. Supply the body below; previews keep its contents protected.')
    expect(repairImpactText('legacy_tls_replacement')).toBe('The source uses legacy TLS behavior. Choose a supported replacement and preview the migration.')
    expect(repairImpactText('diagnostic:runtime_settings_parity')).toBe('Some imported settings need an explicit choice or have no editable equivalent. Review their source values and replay consequences below.')
    expect(repairImpactText('diagnostic:warnings:x', 'line 4: web_reg_find("Text=ok", "Search=ALL")')).toBe('The converted check can miss text outside the main response body. Verify the intended assertion before replay.')
    // A code with nothing specific to say reads as its detail, or not at all.
    expect(repairImpactText('encrypted_proxy_password', 'Proxy password is encrypted.')).toBe('Proxy password is encrypted.')
    expect(repairImpactText('import_review_required')).toBe('')
  })
})

describe('HTTP/3 import review codes', () => {
  it('titles and files them under transport and runtime', () => {
    for (const [code, title] of [
      ['curl_http3_race_not_modeled', 'Choose an HTTP version for curl --http3'],
      ['http3_requires_https', 'Use an https:// URL for HTTP/3'],
      ['http3_with_proxy', 'Remove the proxy or choose another HTTP version'],
      ['http3_requires_tls13', 'Allow TLS 1.3 or choose another HTTP version'],
      ['http3_draft_token', 'Choose final HTTP/3 for a draft capture'],
    ]) {
      expect(issueTitle(code)).toBe(title)
      expect(findingCategory(code)).toBe('runtime')
    }
  })
})

describe('import issue titles', () => {
  it('say what to do for known converter warnings and keep the message as detail', () => {
    const [finding] = reviewSectionFindings([{ key: 'warnings', title: 'Warnings', severity: 'warning', summary: '', items: [
      'line 15: web_set_sockets_option SSL_VERSION=SSL3 is unsupported; APEX TLS defaults remain unchanged and review is required',
    ] }])
    expect(finding.title).toBe('Choose supported TLS settings')
    expect(finding.detail).toContain('SSL_VERSION=SSL3 is unsupported')
  })
  it('cut other long warnings at a word, never mid-word', () => {
    const message = `${'Converted request keeps a recorded header '.repeat(4)}that APEX sends unchanged`
    const title = warningIssueTitle(`line 3: ${message}`)
    expect(title.endsWith('…')).toBe(true)
    expect(title.length).toBeLessThanOrEqual(101)
    const kept = title.slice(0, -1)
    expect(message.startsWith(kept)).toBe(true)
    expect(message[kept.length]).toBe(' ')
    expect(warningIssueTitle('manual review recommended')).toBe('manual review recommended')
  })
  it('name the one place an issue applies, so the same fix in two places reads apart', () => {
    const s = { ...script, actions: [
      { name: 'login', type: 'POST', target: '/' },
      { name: 'search', type: 'GET', target: '/' },
    ] } as ScriptDefinition
    const groups = groupImportFindings([
      { code: 'legacy_tls_replacement', action_path: 'actions[0]', source_ref: 'vuser_init.c:15', detail: '' },
      { code: 'legacy_tls_replacement', action_path: 'actions[1]', source_ref: 'Action.c:', detail: '' },
      { code: 'import_review_required', action_path: 'actions[1]', detail: '' },
    ], s)
    expect(groups.map(group => group.title).sort()).toEqual([
      'Choose supported TLS settings — Action.c',
      'Choose supported TLS settings — vuser_init.c:15',
      'Review imported behavior — search',
    ])
  })
})

// The converter's warning for one legacy TLS call, as the gate it stamps covers it.
const TLS_WARNING = 'web_set_sockets_option SSL_VERSION="2&3" is unsupported; APEX TLS defaults remain unchanged and review is required'
function tlsDraft(gated: boolean) {
  const gate = (line: number) => ({
    name: `web_set_sockets_option_${line}`, type: 'source_review', target: '',
    metadata: { source_file: 'vuser_init.c', source_line: line, covered_warnings: [TLS_WARNING], ...(gated ? { execution_blocker: 'legacy_tls_replacement' } : { migration_change: 'Operator selected TLS 1.2–1.3; intentional migration, not exact LR parity.' }) },
  })
  return { ...script, init: [gate(3), { name: 'login', type: 'GET', target: '/login' }, gate(5)], actions: [
    { name: 'store', type: 'GET', target: '/stores/{{var.C_StoreId}}', metadata: { source_file: 'Action.c', source_line: 2, review_reasons: ['conditional_producer:C_StoreId'], review_required: true } },
    { name: 'stores', type: 'GET', target: '/stores', extractors: [{ name: 'C_StoreId', source: 'body', type: 'regex', expression: 'id=(\\w+)' }] },
  ] } as ScriptDefinition
}
const tlsSections = [
  { key: 'warnings', title: 'Conversion Warnings', severity: 'warning', summary: '', items: [TLS_WARNING, TLS_WARNING, 'line 9: web_reg_find Search=ALL checks the main response body only'] },
  { key: 'runtime_settings_parity', title: 'Runtime Settings Parity', severity: 'warning', summary: 'One setting differs', items: ['Partial: Logging'] },
  { key: 'detected_protocol', title: 'Detected Source Protocol', severity: 'warning', summary: 'Web HTTP/HTML', items: [] },
  { key: 'preserved_metadata', title: 'Preserved Source Metadata', severity: 'info', summary: 'Kept', items: [] },
]
// The inspection's findings for a draft: the server reads the same gates.
const inspectionOf = (draft: ScriptDefinition, readiness: { replay_ready: boolean; unresolved_bindings?: number; validation_issues?: string[] }) => ({
  findings: draftImportFindings(draft).map(f => ({ ...f, severity: f.gate === 'execution' ? 'blocker' : 'warning', source_ref: f.gate === 'execution' ? f.source_ref : undefined })),
  readiness,
})

describe('import readiness', () => {
  it('drops a converter warning its located gate covers, before and after the repair clears the gate', () => {
    for (const gated of [true, false]) {
      const draft = tlsDraft(gated)
      const issues = groupImportFindings(importReviewFindings({ script: draft, reviewSections: tlsSections, warnings: [TLS_WARNING] }), draft)
      expect(issues.filter(issue => issue.title.startsWith('Choose supported TLS settings')).map(issue => issue.severity)).toEqual(gated ? ['blocker', 'blocker'] : [])
      expect(issues.some(issue => issue.findings.some(f => f.detail === TLS_WARNING))).toBe(false)
      expect(issues.map(issue => issue.title)).toContain('Text check searches a narrower response scope — line 9')
    }
    expect(coveredWarnings(tlsDraft(false))).toEqual(new Set([TLS_WARNING]))
    // Without the converter's link the warning stays a note of its own.
    const unlinked = groupImportFindings(reviewSectionFindings([tlsSections[0]]), script)
    expect(unlinked.map(issue => [issue.title, issue.severity])).toContainEqual(['Choose supported TLS settings', 'note'])
  })

  it('maps every issue to one severity: blocker, warning or note', () => {
    const draft = { ...script, actions: [
      { name: 'one', type: 'GET', target: '/{{var.token}}' },
      { name: 'two', type: 'GET', target: '/{{var.order}}' },
      { name: 'three', type: 'GET', target: '/' },
    ] } as ScriptDefinition
    const issues = groupImportFindings([
      { code: 'legacy_tls_replacement', action_path: 'actions[2]', detail: '', gate: 'execution' },
      { code: 'missing_producer:token', action_path: 'actions[0]', detail: '', gate: 'promotion' },
      { code: 'conditional_producer:order', action_path: 'actions[1]', detail: '', gate: 'execution' },
      { code: 'web_set_user_credentials_conflict', action_path: 'actions[2]', detail: '', gate: 'promotion' },
      { code: 'import_review_required', action_path: 'actions[2]', detail: '', gate: 'promotion' },
      ...reviewSectionFindings(tlsSections.slice(0, 2)),
    ], draft, { coveredWarnings: new Set() })
    const severity = (title: string) => issues.find(issue => issue.title.startsWith(title))?.severity
    expect(severity('Choose supported TLS settings — three')).toBe('blocker')
    expect(severity('Check producer order: order')).toBe('blocker')
    expect(severity('Missing value: token')).toBe('warning')
    expect(severity('Reconcile web_set_user credentials')).toBe('warning')
    expect(severity('Review imported behavior')).toBe('note')
    expect(severity('Review runtime behavior differences')).toBe('note')
    expect(severity('Text check searches a narrower response scope')).toBe('note')
    expect(issues.find(issue => issue.findings[0].code.startsWith('diagnostic:warnings:') && issue.title === 'Choose supported TLS settings')?.severity).toBe('note')
    expect(issues.every(issue => issue.blocking === (issue.severity === 'blocker'))).toBe(true)
  })

  it('counts each severity once and moves reviewed warnings and notes out of their counts', () => {
    const issue = (id: string, severity: ImportIssue['severity']): ImportIssue => ({ id, title: id, category: 'review', severity, blocking: severity === 'blocker', findings: [] })
    const issues = [issue('a', 'blocker'), issue('b', 'blocker'), issue('c', 'warning'), issue('d', 'warning'), issue('e', 'note'), issue('f', 'note')]
    expect(readinessCounts(issues)).toEqual({ blockers: 2, warnings: 2, notes: 2, reviewed: 0 })
    // A review mark never settles a blocker.
    const counts = readinessCounts(issues, new Set(['a', 'c', 'f']))
    expect(counts).toEqual({ blockers: 2, warnings: 1, notes: 1, reviewed: 2 })
    expect(unresolvedIssueCount(counts)).toBe(4)
    expect(readinessSummaryText(counts)).toBe('2 block replay · 1 warning · 1 review note · 2 reviewed')
    expect(readinessSummaryText(readinessCounts([issue('a', 'blocker'), issue('c', 'warning'), issue('d', 'warning')]))).toBe('1 blocks replay · 2 warnings · 0 review notes')
    expect(readinessSummaryText(readinessCounts([]))).toBe('0 block replay · 0 warnings · 0 review notes')
  })

  it('lists the same issues with or without the inspection', () => {
    const draft = tlsDraft(true)
    const preflight = {
      valid: false, missing_secrets: ['API_TOKEN'], missing_variables: ['C_StoreId'],
      checks: [{ id: 'validation', label: 'Script validation', status: 'pass' }, { id: 'review_gates', label: 'Review gates', status: 'fail' }],
    }
    const context = { script: draft, preflight, reviewSections: [...tlsSections, { key: 'unsupported_elements', title: 'Unsupported Source Elements', severity: 'blocker', summary: 'Not converted', items: ['web_set_max_html_param_len'] }], warnings: [TLS_WARNING], fidelityBlockers: ['legacy_tls_replacement at init[0]', 'legacy_tls_replacement at init[2]'] }
    const summary = (issues: ImportIssue[]) => issues.map(issue => `${issue.severity}:${issue.id}`).sort()
    const provisional = groupImportFindings(importReviewFindings(context), draft)
    const inspected = groupImportFindings(importReviewFindings({ ...context, inspection: inspectionOf(draft, { replay_ready: false, unresolved_bindings: 1 }) }), draft)
    expect(summary(inspected)).toEqual(summary(provisional))
    expect(readinessCounts(provisional)).toEqual({ blockers: 3, warnings: 1, notes: 3, reviewed: 0 })
    // The stamped preflight's missing values are not listed again beside the draft's own value findings.
    expect(provisional.map(issue => issue.title).filter(title => title.includes('C_StoreId'))).toEqual(['Check producer order: C_StoreId — Action.c:2'])
    // Unsupported elements are reported by the gates' blockers already: a note.
    expect(provisional.find(issue => issue.id.startsWith('diagnostic:unsupported_elements'))?.severity).toBe('note')
    expect(provisional.some(issue => issue.id.startsWith('diagnostic:detected_protocol') || issue.id.startsWith('diagnostic:preserved_metadata'))).toBe(false)
  })

  it('blocks on review sections and execution prerequisites only when replay is refused for no other reason', () => {
    const draft = { ...script, actions: [{ name: 'home', type: 'GET', target: '/' }] } as ScriptDefinition
    const sections = [{ key: 'unsupported_elements', title: 'Unsupported Source Elements', severity: 'blocker', summary: 'Not converted', items: ['web_set_max_html_param_len'] }]
    const blockers = (input: Partial<Parameters<typeof importReviewFindings>[0]>) => groupImportFindings(importReviewFindings({ script: draft, reviewSections: sections, ...input }), draft).filter(issue => issue.blocking).map(issue => issue.id.split(':').slice(0, 2).join(':'))
    // Replay runs: the unsupported element is listed as a note.
    expect(blockers({})).toEqual([])
    expect(blockers({ inspection: { findings: [], readiness: { replay_ready: true } } })).toEqual([])
    // Replay is refused (here by the runtime migration policy) and nothing else says why.
    expect(blockers({ executionGated: true })).toEqual(['diagnostic:unsupported_elements'])
    expect(blockers({ executionGated: true, reviewSections: [] })).toEqual(['diagnostic:execution'])
    // A failed validation check names itself, from the inspection or else the stamped preflight.
    const failedValidation = { valid: false, checks: [{ id: 'validation', label: 'Script validation', status: 'fail', detail: '1 validation issue' }] }
    expect(blockers({ preflight: failedValidation, reviewSections: [] })).toEqual(['diagnostic:validation'])
    expect(blockers({ preflight: failedValidation, reviewSections: [], inspection: { findings: [], readiness: { replay_ready: false, validation_issues: ['script protocol is required'] } } })).toEqual(['diagnostic:validation'])
    // Archive sentences the action findings do not name stay listed, as warnings
    // (they hold promotion, not replay); unresolved credentials are one finding.
    expect(blockers({ reviewSections: [], fidelityBlockers: ['credential_bindings_required', 'One or more C lifecycle source files were missing, ambiguous, or incompletely parsed.'], inspection: { findings: [], readiness: { replay_ready: false, unresolved_bindings: 2 } } }).sort()).toEqual(['diagnostic:credential_bindings'])
  })

  it('counts unresolved credentials from the inspection once it answers, not from a preflight stamped without the project\'s secrets', () => {
    const draft = { ...script, actions: [{ name: 'home', type: 'GET', target: '/' }] } as ScriptDefinition
    // A repair restamps the preflight without the project's secret resolver,
    // so a project secret the repair binds reads as missing there; the
    // resolver-backed readiness omits a zero count.
    const preflight = { valid: true, missing_secrets: ['proxy_pw'], checks: [{ id: 'secrets', label: 'Secret bindings', status: 'warn' }] }
    const inspected = groupImportFindings(importReviewFindings({ script: draft, preflight, inspection: { findings: [], readiness: { replay_ready: true } } }), draft)
    expect(inspected.some(issue => issue.id.startsWith('diagnostic:credential_bindings'))).toBe(false)
    expect(readinessCounts(inspected).blockers).toBe(0)
    // Before an inspection answers, the stamped preflight is all there is.
    const provisional = groupImportFindings(importReviewFindings({ script: draft, preflight }), draft)
    expect(provisional.filter(issue => issue.blocking).map(issue => issue.id)).toEqual(['diagnostic:credential_bindings:'])
  })

  it('counts an archive fidelity sentence as a warning, as the editor counts the same report entry, never as a replay blocker', () => {
    const draft = { ...script, actions: [{ name: 'home', type: 'GET', target: '/' }] } as ScriptDefinition
    const trailing = 'One or more response contributors or one-shot headers occur after the final request and cannot be bound faithfully.'
    const issues = groupImportFindings(importReviewFindings({ script: draft, fidelityBlockers: [trailing], inspection: { findings: [], readiness: { replay_ready: true, promotion_ready: false } } }), draft)
    expect(issues.map(issue => ({ id: issue.id, severity: issue.severity, blocking: issue.blocking, detail: issue.findings[0].detail }))).toEqual([{ id: 'diagnostic:archive:0:', severity: 'warning', blocking: false, detail: trailing }])
    const counts = readinessCounts(issues)
    expect(counts).toEqual({ blockers: 0, warnings: 1, notes: 0, reviewed: 0 })
    expect(readinessSummaryText(counts)).toBe('0 block replay · 1 warning · 0 review notes')
    // Without the inspection too, and a review mark settles it like any warning.
    const provisional = groupImportFindings(importReviewFindings({ script: draft, fidelityBlockers: [trailing] }), draft)
    expect(readinessCounts(provisional)).toEqual(counts)
    expect(readinessCounts(provisional, new Set([provisional[0].id]))).toEqual({ blockers: 0, warnings: 0, notes: 0, reviewed: 1 })
    // A refused replay the sentence does not explain still blocks, for the reason the server gives.
    const refused = groupImportFindings(importReviewFindings({ script: draft, fidelityBlockers: [trailing], executionGated: true }), draft)
    expect(refused.filter(issue => issue.blocking).map(issue => issue.id)).toEqual(['diagnostic:execution:'])
    expect(refused.find(issue => issue.id.startsWith('diagnostic:archive:'))?.severity).toBe('warning')
  })
})

// Synthetic enterprise drafts and the server's findings and preflight for each
// (pkg/internal/importreview TestReadinessParityFixtureIsFresh writes them).
async function readinessParityCases(): Promise<Array<{ name: string; script: ScriptDefinition; findings: ImportFinding[] | null; preflight: ReplayPreflight }>> {
  // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
  const { readFileSync } = await import('node:fs')
  return JSON.parse(readFileSync(new URL('../../../pkg/internal/importreview/testdata/readiness_parity.json', import.meta.url), 'utf8') as string)
}

describe('protocol configuration references', () => {
  // An imported web_set_user's user in the auth settings, and a default
  // header whose value a request also uses with no producer.
  const configured = {
    ...script,
    config: { base_url: 'https://example.test', http: { auth: { type: 'basic', username: '{{var.pUser}}' }, default_headers: { 'X-Tenant': '{{var.c_tenant}}' } } },
    parameters: [{ name: 'pUser', type: 'list', values: ['perf_user_01'], update: 'once' }],
    actions: [{ name: 'tenant', type: 'GET', method: 'GET', target: '/tenants/{{var.c_tenant}}', metadata: { source_file: 'Action.c', source_line: 3, review_reasons: ['missing_producer:c_tenant'], review_required: true, promotion_blocker: true } }],
  } as ScriptDefinition

  it('lists each one as a script-wide configuration blocker with no step repair, folding a step\'s use of the same value into it', () => {
    const preflight = { ...buildLocalReplayPreflight(configured), valid: false }
    expect(preflight.config_blocking_variables).toEqual(['c_tenant', 'pUser'])
    const issues = groupImportFindings(importReviewFindings({ script: configured, preflight }), configured)
    expect(issues.map(issue => [issue.id, issue.title, issue.category, issue.severity, issue.findings.map(finding => finding.code)])).toEqual([
      ['config_reference:c_tenant', 'Replace configuration reference: c_tenant', 'runtime', 'blocker', ['config_reference:c_tenant', 'missing_producer:c_tenant']],
      ['config_reference:pUser', 'Replace configuration reference: pUser', 'runtime', 'blocker', ['config_reference:pUser']],
    ])
    expect(issues[1].findings[0]).toMatchObject({ gate: 'execution', detail: 'Protocol configuration references pUser. Configuration is applied before any step runs, so replay refuses it. Replace the reference with a literal configuration value.' })
    expect(issues[1].findings[0].action_path).toBeUndefined()
    expect(issueRepairOptions(issues[0].findings[0].code)).toEqual([])
    // Both read as configuration issues, the one with a step's finding too.
    expect(issues.map(isConfigReferenceIssue)).toEqual([true, true])
    expect(groupImportFindings([{ code: 'missing_producer:c_tenant', action_path: 'actions[0]', detail: '', gate: 'execution' }], configured).map(isConfigReferenceIssue)).toEqual([false])
    // The configuration blockers explain the refusal; no generic prerequisite is added.
    expect(readinessCounts(issues)).toEqual({ blockers: 2, warnings: 0, notes: 0, reviewed: 0 })
    // The editor words the same value the same way.
    expect(replayReadinessIssues(preflight).find(issue => issue.id === 'readiness:blocking:pUser')?.description).toBe(issues[1].findings[0].detail)
  })

  it('reads them from the stamped preflight, from the draft when an older preflight does not list them, and from the draft without a preflight', () => {
    const local = buildLocalReplayPreflight(configured)
    const listed = { valid: false, blocking_variables: ['c_tenant', 'pUser'], config_blocking_variables: ['pUser'] }
    const codes = (preflight?: Parameters<typeof importReviewFindings>[0]['preflight']) => importReviewFindings({ script: configured, preflight }).map(finding => finding.code).filter(code => code.startsWith('config_reference:'))
    // The preflight's own list wins over the draft's.
    expect(codes(listed)).toEqual(['config_reference:pUser'])
    // An older controller's preflight lists the blocking values without saying why.
    const older = { ...local, valid: false, config_blocking_variables: undefined, param_blocking_variables: undefined }
    expect(codes(older)).toEqual(['config_reference:c_tenant', 'config_reference:pUser'])
    expect(codes(undefined)).toEqual(['config_reference:c_tenant', 'config_reference:pUser'])
    // A preflight that blocks nothing names nothing.
    expect(codes({ valid: true })).toEqual([])
  })
})

describe('import and editor readiness parity', () => {
  it('reads the draft gates as the server reads them', async () => {
    const identity = (finding: ImportFinding) => `${finding.action_path}|${finding.code}|${finding.gate}|${finding.gate === 'execution' ? finding.source_ref || '' : ''}`
    for (const parity of await readinessParityCases()) {
      expect({ name: parity.name, findings: draftImportFindings(parity.script).map(identity).sort() }).toEqual({ name: parity.name, findings: (parity.findings || []).map(identity).sort() })
    }
  })

  it('counts the blockers and warnings the editor lists for the same draft', async () => {
    const cases = await readinessParityCases()
    expect(cases.length).toBeGreaterThan(0)
    // The corpus covers a configuration reference and a gate with two codes.
    expect(cases.some(parity => parity.preflight.config_blocking_variables?.length)).toBe(true)
    expect(cases.some(parity => (parity.findings || []).filter((finding, _, all) => finding.gate === 'execution' && all.some(other => other !== finding && other.gate === 'execution' && other.action_path === finding.action_path)).length > 0)).toBe(true)
    for (const parity of cases) {
      // The import review reads the draft with the preflight the server stamps on it.
      const imported = readinessCounts(groupImportFindings(importReviewFindings({ script: parity.script, preflight: parity.preflight }), parity.script))
      const editor = replayReadinessIssues(buildLocalReplayPreflight(parity.script))
      expect({ name: parity.name, blockers: imported.blockers, warnings: imported.warnings }).toEqual({
        name: parity.name,
        blockers: editor.filter(issue => issue.severity === 'blocker').length,
        warnings: editor.filter(issue => issue.severity === 'warning').length,
      })
    }
  })
})
