import { describe, expect, it } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { draftImportFindings, findingCategory, groupImportFindings, issueRepairOptions, issueTitle, reviewSectionFindings, warningIssueTitle } from './importReviewIssues'
const script = { name: 'Review', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [] } as ScriptDefinition

describe('import issue queue', () => {
  it('groups repeated consumers without conflating namespaces or control branches', () => {
    const s = { ...script, actions: [
      { name: 'one', type: 'POST', target: '/', body: '{{var.token}}' },
      { name: 'two', type: 'POST', target: '/', body: '{{var.token}}' },
      { name: 'parameter', type: 'POST', target: '/', body: '{{param.token}}' },
      { name: 'branch', type: 'control_if', target: '', control: { steps: [{ name: 'nested', type: 'POST', target: '/', body: '{{var.token}}' }] } },
    ] } as ScriptDefinition
    const findings = ['actions[0]', 'actions[1]', 'actions[2]', 'actions[3].control.steps[0]'].map(action_path => ({ code: 'missing_producer:token', action_path, detail: '', gate: 'execution' }))
    const groups = groupImportFindings([...findings, findings[0]], s)
    expect(groups).toHaveLength(3)
    expect(groups.map(g => g.findings.length).sort()).toEqual([1, 1, 2])
    expect(groups[0].title).toBe('Missing value: token')
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
