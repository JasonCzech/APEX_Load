// @vitest-environment node
import { describe, expect, it } from 'vitest'

import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { analyzeVariableReadiness, protocolFamily, stepOutputs, templateReferences, type StepOutputs, type VariableWrite } from './variableFlow'

interface ReadinessCase {
  name: string
  script: ScriptDefinition
  seeded?: Record<string, string>
  // param_blocking and config_blocking are empty unless a case lists them.
  expected: { missing: string[]; blocking: string[]; late_produced: string[]; param_blocking?: string[]; config_blocking?: string[] }
}

// The Go golden corpus pkg/dsl/varflow reads (TestAnalyzeGolden). It is read
// at run time, so the dashboard build needs no copy of it.
async function readinessCases(): Promise<ReadinessCase[]> {
  // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
  const { readFileSync } = await import('node:fs')
  return JSON.parse(readFileSync(new URL('../../../pkg/dsl/varflow/testdata/readiness_cases.json', import.meta.url), 'utf8') as string)
}

const cases = await readinessCases()

describe('analyzeVariableReadiness', () => {
  it('reads the shared corpus', () => {
    expect(cases.length).toBeGreaterThan(20)
  })

  it.each(cases.map(testCase => [testCase.name, testCase] as const))('matches the Go analyzer: %s', (_name, testCase) => {
    const result = analyzeVariableReadiness(testCase.script, testCase.seeded)
    expect({ missing: result.missing, blocking: result.blocking, late_produced: result.lateProduced, param_blocking: result.paramBlocking, config_blocking: result.configBlocking })
      .toEqual({ param_blocking: [], config_blocking: [], ...testCase.expected })
  })

  it('keeps a scalar beside a save-all array of the same name, as the runtime does', () => {
    const script: ScriptDefinition = { name: 'c', version: '1', protocol: 'http', config: { base_url: '' }, actions: [
      { name: 'scalar', type: 'source_parameter', target: '', params: { function: 'lr_save_string', args: ['x', 'Ids'] } },
      { name: 'eval', type: 'source_json', target: '', params: { function: 'lr_eval_json', json_object: 'doc', buffer: '{"ids":[1]}' } },
      { name: 'array', type: 'source_json', target: '', params: { function: 'lr_json_get_values', json_object: 'doc', value_param: 'Ids', select_all: 'Yes' } },
      { name: 'use', type: 'request', method: 'GET', target: '/{{var.Ids}}/{{var.ids}}/{{var.ids_1}}/{{var.Ids_count}}' },
    ] }
    expect(analyzeVariableReadiness(script).missing).toEqual([])
  })

  it('walks every nesting level validation accepts', () => {
    let nested: ScriptActionDefinition = { name: 'deep', type: 'request', method: 'GET', target: '/{{var.deep}}' }
    for (let depth = 0; depth < 63; depth += 1) nested = { name: `loop ${depth}`, type: 'control_loop', target: '', control: { count: 1, steps: [nested] } }
    const script: ScriptDefinition = { name: 'deep', version: '1', protocol: 'http', config: { base_url: '' }, actions: [nested] }
    expect(analyzeVariableReadiness(script).missing).toEqual(['deep'])
  })

  it('treats an undecodable base64 body as text only', () => {
    const script: ScriptDefinition = { name: 'b', version: '1', protocol: 'http', config: { base_url: '' }, actions: [
      { name: 'bad', type: 'request', method: 'POST', target: '/', body: '{{var.raw}}', body_encoding: 'base64' },
      // Line breaks inside base64 are ignored, as Go's decoder ignores them.
      { name: 'wrapped', type: 'request', method: 'POST', target: '/', body: btoa('{"id":"{{var.wrapped}}"}').replace(/(.{8})/g, '$1\r\n'), body_encoding: ' BASE64 ' },
    ] }
    expect(analyzeVariableReadiness(script).missing).toEqual(['raw', 'wrapped'])
  })

  it('reads a raw-JSON native_javascript step whose params is not an object without throwing', () => {
    // The JSON pane only parses a draft, so params can be any JSON value.
    const native = (params: unknown) => ({ name: 'native', type: 'native_javascript', target: '', params } as unknown as ScriptActionDefinition)
    for (const params of ['x', 5, true]) {
      const script: ScriptDefinition = { name: 'n', version: '1', protocol: 'http', config: { base_url: '' }, actions: [
        native(params),
        { name: 'sequence', type: 'http_sequence', target: '/', params: { requests: [native(params), { name: 'use', type: 'GET', method: 'GET', target: '/{{var.after}}' }] } },
      ] }
      expect(analyzeVariableReadiness(script)).toEqual({ missing: ['after'], blocking: [], lateProduced: [], paramBlocking: [], configBlocking: [] })
    }
  })

  it('reads no metadata from a raw-JSON draft whose metadata is not an object', () => {
    // The JSON pane only parses a draft; the server would not decode this metadata at all.
    const step = (type: string, metadata: unknown) => ({ name: type, type, target: 'orders', metadata } as unknown as ScriptActionDefinition)
    for (const metadata of ['{{var.trace_id}} {{param.region}}', ['{{param.region}}'], 5]) {
      const script: ScriptDefinition = { name: 'm', version: '1', protocol: 'kafka', config: { base_url: '' }, actions: [step('kafka_produce', metadata), step('kafka_consume', metadata)] }
      expect(analyzeVariableReadiness(script)).toEqual({ missing: [], blocking: [], lateProduced: [], paramBlocking: [], configBlocking: [] })
    }
  })

  it('ignores http_sequence children that do not decode as actions', () => {
    const script: ScriptDefinition = { name: 's', version: '1', protocol: 'http', config: { base_url: '' }, actions: [
      { name: 'sequence', type: 'http_sequence', target: '/', params: { requests: [{ name: 'a', type: 'GET', target: '/{{var.child}}' }, 'not an action'] } },
    ] }
    expect(analyzeVariableReadiness(script).missing).toEqual([])
  })
})

describe('stepOutputs', () => {
  const parameter = (fn: string, args?: unknown, extra: Record<string, unknown> = {}): ScriptActionDefinition => ({ name: fn, type: 'source_parameter', target: '', params: { function: fn, ...(args === undefined ? {} : { args }), ...extra } })
  const sourceJSON = (params: Record<string, unknown>): ScriptActionDefinition => ({ name: 'json', type: 'source_json', target: '', params })
  const x = (name: string): VariableWrite => ({ name, lower: true })
  const known = (reads?: string[], ...writes: VariableWrite[]): StepOutputs => ({ known: true, ...(reads ? { readTexts: reads } : {}), ...(writes.length ? { writes } : {}) })

  // The rows of TestStepOutputsMatchesRuntimeProducers (pkg/dsl/varflow/outputs_test.go).
  it.each<[string, ScriptActionDefinition, StepOutputs]>([
    ['lr_save_string reads its value and writes args[1]', parameter('lr_save_string', ['{{var.input}}', 'Copy']), known(['{{var.input}}'], x('Copy'))],
    ['lr_save_int formats scalar args', parameter('lr_save_int', [1, 'Result']), known(['1'], x('Result'))],
    ['lr_save_string needs two args', parameter('lr_save_string', ['only']), known()],
    ['random source array reads only the count', parameter('lr_save_string', ['{{var.ids}}', 'Picked'], { source_array: { name: ' IDs ', selection: 'random' } }), known(['{{var.ids_count}}'], x('Picked'))],
    ['non-random source array reads nothing', parameter('lr_save_string', ['{{var.ids}}', 'Picked'], { source_array: { name: 'ids', selection: 'sequential' } }), known(undefined, x('Picked'))],
    ['lr_save_var reads the buffer and writes args[3]', parameter('lr_save_var', ['{{var.buffer}}', '5', '0', 'Part']), known(['{{var.buffer}}'], x('Part'))],
    ['lr_save_var needs four args', parameter('lr_save_var', ['{{var.buffer}}', '5', 'Part']), known()],
    ['lr_param_sprintf reads the format and values', parameter('lr_param_sprintf', ['Formatted', '%s-%s', '{{var.a}}', '{{var.b}}']), known(['%s-%s', '{{var.a}}', '{{var.b}}'], x('Formatted'))],
    ['lr_param_sprintf needs two args', parameter('lr_param_sprintf', ['Formatted']), known()],
    ['lr_param_increment writes args[0]', parameter('lr_param_increment', ['Counter', '2']), known(undefined, x('Counter'))],
    ['lr_param_increment without args', parameter('lr_param_increment'), known()],
    ['lr_free_parameter removes args[0]', parameter('lr_free_parameter', ['Value']), { known: true, removes: ['Value'] }],
    ['lr_save_timestamp default name', parameter('lr_save_timestamp'), known(undefined, x('timestamp'))],
    ['lr_save_timestamp first non-empty arg', parameter('lr_save_timestamp', ['  ', 'Stamp', 'DIGITS=10']), known(undefined, x('Stamp'))],
    ['lr_save_timestamp scalar arg', parameter('lr_save_timestamp', 'Stamp'), known(undefined, x('Stamp'))],
    ['lr_save_datetime one arg', parameter('lr_save_datetime', ['Today']), known(undefined, x('Today'))],
    ['lr_save_datetime last arg and format read', parameter('lr_save_datetime', ['{{var.format}}', 'DATE_NOW', 'Out']), known(['{{var.format}}'], x('Out'))],
    ['lr_save_datetime blank destination', parameter('lr_save_datetime', ['%Y', '']), known(['%Y'])],
    ['lr_save_datetime default name', parameter('lr_save_datetime'), known(undefined, x('datetime'))],
    ['lr_whoami', parameter('lr_whoami'), known(undefined, x('vuser_id'), x('vuser_group'))],
    ['lr_get_host_name default', parameter('lr_get_host_name'), known(undefined, x('host_name'))],
    ['lr_get_master_host_name named', parameter('lr_get_master_host_name', ['Master']), known(undefined, x('Master'))],
    ['lr_get_vuser_ip default', parameter('lr_get_vuser_ip'), known(undefined, x('vuser_ip'))],
    ['web_convert_param save_param and source_string', parameter('web_convert_param', ['Ignored', 'SourceString={{var.other}}'], { save_param: ' c_Token_URL ', source_string: '{{var.token}}' }), known(['{{var.token}}'], x('c_Token_URL'))],
    ['web_convert_param args fallback', parameter('web_convert_param', ['Encoded', 'SourceString={{var.token}}', 'TargetEncoding=URL']), known(['{{var.token}}'], x('Encoded'))],
    ['web_convert_param blank name reads nothing', parameter('web_convert_param', [' ', 'SourceString={{var.token}}']), known()],
    ['web_save_timestamp_param', parameter('web_save_timestamp_param', ['Tstamp']), known(undefined, x('Tstamp'))],
    ['web_save_timestamp_param without args', parameter('web_save_timestamp_param'), known()],
    ['web_save_param_length', parameter('web_save_param_length', [' C_Body ', 'Base=Hexadecimal']), known(undefined, x('C_Body_Length'))],
    ['web_save_param_length without args', parameter('web_save_param_length'), known()],
    ['web_convert_date_param rewrites in place', parameter('web_convert_date_param', ['Date', 'SourceFormat=%Y', 'TargetFormat=%y']), known()],
    ['named fallback', parameter('legacy_parameter', [' ', '{{var.input}}'], { save_param: 'Result' }), known(['{{var.input}}'], x('Result'))],
    ['blank named fallback key is skipped', parameter('legacy_parameter', ['ready'], { save_param: '  ', parameter: 'Result' }), known(['ready'], x('Result'))],
    ['call_args SaveParam fallback', parameter('lr_save_param_regexp', ['{{var.body}}'], { call_args: [{ name: 'SaveParam', value: 'Matched' }] }), known(['{{var.body}}'], x('Matched'))],
    ['blank call_args SaveParam writes nothing', parameter('lr_save_param_regexp', ['{{var.body}}'], { call_args: [{ name: 'SaveParam', value: ' ' }, { name: 'param', value: 'Later' }] }), known()],
    ['positional lr_save_searched_string publishes nothing', parameter('lr_save_searched_string', ['{{var.body}}', 'LB=a', 'Found']), known()],
    ['function from metadata', { name: 'm', type: 'source_parameter', target: '', params: { args: ['ready', 'Copy'] }, metadata: { source_function: 'lr_save_string' } }, known(['ready'], x('Copy'))],
    ['whitespace function', parameter(' lr_save_string ', ['ready', 'Copy']), known(['ready'], x('Copy'))],
    ['legacy loadrunner_parameter type', { name: 'l', type: 'loadrunner_parameter', target: '', params: { function: 'lr_save_string', args: ['ready', 'Copy'] } }, known(['ready'], x('Copy'))],
    ['lr_eval_json reads the buffer', sourceJSON({ function: 'lr_eval_json', json_object: 'doc', buffer: '{{var.body}}' }), known(['{{var.body}}'])],
    ['lr_json_get_values SelectAll array', sourceJSON({ function: 'lr_json_get_values', json_object: 'doc', value_param: ' C_OfferIds ', query: '$.ids[*]', select_all: ' yes ' }), known(['$.ids[*]'], { name: 'C_OfferIds', lower: true, array: true })],
    ['lr_json_get_values scalar despite save_all', sourceJSON({ function: 'lr_json_get_values', json_object: 'doc', value_param: 'C_Name', query: '$.name', save_all: true, select_all: '' }), known(['$.name'], x('C_Name'))],
    ['lr_json_get_values legacy args', sourceJSON({ function: 'lr_json_get_values', args: ['JsonObject=doc', 'ValueParam=Picked', 'QueryString=$.items[{{var.index}}]', 'SelectAll=Yes'] }), known(['$.items[{{var.index}}]'], { name: 'Picked', lower: true, array: true })],
    ['lr_json_get_values without a value param writes nothing', sourceJSON({ function: 'lr_json_get_values', json_object: 'doc', query: '$.name' }), known(['$.name'])],
    ['lr_json_stringify', sourceJSON({ function: 'lr_json_stringify', json_object: 'doc', output_param: 'Text' }), known(undefined, x('Text'))],
    ['lr_json_stringify without a document writes nothing', sourceJSON({ function: 'lr_json_stringify', output_param: 'Text' }), known()],
    ['other JSON functions fail and stay unknown', sourceJSON({ function: 'lr_json_find', json_object: 'doc' }), {}],
    ['source_secret save param', { name: 's', type: 'source_secret', target: '', params: { function: 'lr_get_secret', save_param: 'Token', args: ['{{var.ignored}}', 'Last'] } }, known(undefined, x('Token'))],
    ['source_secret last arg', { name: 's', type: 'source_secret', target: '', params: { function: 'lr_get_secret', args: ['vault', 'Last'] } }, known(undefined, x('Last'))],
    ['source_secret function name', { name: 's', type: 'loadrunner_secret', target: '', params: { function: 'lr_unmask' } }, known(undefined, x('unmask'))],
    ['source_message status', { name: 'm', type: 'source_message', target: '', params: { format: 'done', status_message: 'true' } }, { writes: [{ name: 'vuser_status' }] }],
    ['source_message without status', { name: 'm', type: 'source_message', target: '', params: { format: 'done', status_message: false } }, {}],
    ['oauth default', { name: 'o', type: 'http_oauth2_token', target: '' }, { writes: [x('access_token'), x('access_token_token_type'), x('access_token_instance_url'), x('access_token_expires_at')] }],
    ['oauth save_as', { name: 'o', type: 'http_oauth2_token', target: '', params: { save_as: ' Orders ' } }, { writes: [x('Orders'), x('Orders_token_type'), x('Orders_instance_url'), x('Orders_expires_at')] }],
    ['browser evaluate result', { name: 'b', type: 'browser_evaluate_js', target: 'document.title' }, { writes: [{ name: 'result' }] }],
    ['native javascript reads inputs only', { name: 'n', type: 'native_javascript', target: '', params: { source: '{{var.not_read}}', inputs: { order: '{{var.order}}' }, result_prefix: ' calc ' } }, { known: true, readTexts: ['{"order":"{{var.order}}"}'], prefixes: ['calc'] }],
    ['live table prefix', { name: 'l', type: 'live_table', target: '', params: { result_prefix: ' job ' } }, { prefixes: ['job'] }],
    ['live table without prefix', { name: 'l', type: 'live_table', target: '', params: { result_prefix: ' ' } }, {}],
    ['source_review is log only', { name: 'r', type: 'source_review', target: '', params: { args: ['{{var.x}}'] } }, { noReads: true }],
    ['source_external is log only', { name: 'e', type: 'source_external', target: '' }, { noReads: true }],
    ['custom_call is log only', { name: 'c', type: 'custom_call', target: '' }, { noReads: true }],
    ['source_database is log only', { name: 'd', type: 'loadrunner_database', target: '' }, { noReads: true }],
    ['source_transaction uses its args literally', { name: 't', type: 'source_transaction', target: '', params: { function: 'lr_start_transaction', args: ['{{var.x}}'] } }, { noReads: true }],
    ['source_timer uses its args literally', { name: 't', type: 'loadrunner_timer', target: '', params: { args: ['{{var.x}}'] } }, { noReads: true }],
    ['source_metric only logs', { name: 'm', type: 'source_metric', target: '', params: { args: ['m', '{{var.x}}'] } }, { noReads: true }],
    ['source_runtime uses its args literally', { name: 'r', type: 'source_runtime', target: '', params: { function: 'lr_exit', args: ['{{var.x}}'] } }, { noReads: true }],
    ['rendezvous uses its key literally', { name: 'r', type: 'rendezvous', target: '', params: { key: '{{var.x}}' } }, { noReads: true }],
    ['requests are not modeled', { name: 'r', type: 'request', target: '/{{var.x}}' }, {}],
  ])('%s', (_name, action, want) => {
    expect(stepOutputs(action)).toEqual(want)
  })

  it('formats args as the runtime does', () => {
    const args = [0, -1, 1.5, 123456, 1000000, 1234567, 123456789, 1e21, 0.0001, 0.00001, 2.5e-5, true, null, [1, 'a', [2]], { b: 1, a: 'x' }]
    expect(stepOutputs(parameter('lr_param_sprintf', ['Out', ...args])).readTexts).toEqual([
      '0', '-1', '1.5', '123456', '1e+06', '1.234567e+06', '1.23456789e+08', '1e+21', '0.0001', '1e-05', '2.5e-05', 'true', '<nil>', '[1 a [2]]', 'map[a:x b:1]',
    ])
  })
})

describe('templateReferences', () => {
  it('reads var and param references once each, in order, and no other namespace', () => {
    expect(templateReferences('{{ var.a }}/{{param.b}}/{{var.a}}/{{secret.c}}/{{data.d}}/{{ var.e}}')).toEqual([
      { namespace: 'var', name: 'a' },
      { namespace: 'param', name: 'b' },
    ])
  })
})

describe('protocolFamily', () => {
  it('maps HTTP versions and JMS providers onto their family', () => {
    expect(['http', 'http/1.1', ' http/2 ', 'http/3', 'jms-solace', 'browser', 'HTTP'].map(protocolFamily)).toEqual(['http', 'http', 'http', 'http', 'jms', 'browser', 'HTTP'])
  })
})
