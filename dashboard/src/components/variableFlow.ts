import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'

/**
 * A hand port of pkg/dsl/varflow: which variables a script step reads and
 * publishes, and whether each reference has a value where it executes. Server
 * replay readiness and import dependency review run the Go original, so the
 * local preflight reports the same missing, blocking and late-produced names
 * without a round trip. pkg/dsl/varflow/testdata/readiness_cases.json holds
 * both to the same answers (variableFlow.test.ts); change them together.
 */

/** One variable a step publishes. `lower` adds the lowercase alias the runtime saves; `array` publishes NAME_1..n and NAME_count instead of NAME. */
export interface VariableWrite {
  name: string
  lower?: boolean
  array?: boolean
}

/**
 * One step's effect on runtime variables (varflow.Outputs). When `known` is
 * set, `readTexts` are the only template text the runtime resolves; otherwise
 * every field except metadata may be read, plus the metadata a Kafka or JMS
 * send delivers and any {{param.*}} in a protocol action's metadata outside
 * its import-evidence keys (metadataKeyIsEvidence), which replay keeps as
 * recorded and never resolves or refuses.
 * `noReads` marks steps the runtime only logs or uses literally.
 */
export interface StepOutputs {
  writes?: VariableWrite[]
  removes?: string[]
  prefixes?: string[]
  noReads?: boolean
  readTexts?: string[]
  known?: boolean
}

export interface TemplateReference {
  namespace: 'var' | 'param'
  name: string
}

/** The source-neutral readiness of a script, as the server preflight reports it. */
export interface VariableReadiness {
  /** References with no value where they execute, sorted. */
  missing: string[]
  /** The missing names replay cannot carry as placeholder text. */
  blocking: string[]
  /**
   * Missing {{var.*}} names some step does write, only not on every path
   * before the use. A write inside a control_parallel branch counts only for
   * uses that branch (or one nested in it) can reach.
   */
  lateProduced: string[]
  /** Blocking names an undeclared {{param.*}} use blocks: only a declared script parameter clears them. */
  paramBlocking: string[]
  /** Blocking names a protocol configuration reference blocks: no step can supply them. */
  configBlocking: string[]
}

type Params = Record<string, unknown>

// RE2's \s is [\t\n\f\r ]; JavaScript's would also match Unicode spaces.
const REFERENCE_PATTERN = /\{\{[\t\n\f\r ]*(var|param)\.([a-zA-Z0-9_.-]+)/g
// dsl.MaxScriptActionDepth: validation refuses a deeper tree (a top-level
// action has depth 1), so every script the server accepts is walked in full.
const MAX_ACTION_DEPTH = 64
const LEGACY_ACTION_TYPE_PREFIX = 'loadrunner_'
const HTTP_REQUEST_ACTION_TYPES = new Set(['request', 'http_request', 'soap_request', 'java_http_request'])
const HTTP_METHODS = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'])
// The protocol families whose adapter is, or sends through, the HTTP adapter.
const PLACEHOLDER_PROTOCOL_FAMILIES = new Set(['http', 'soap', 'java_http'])
// The steps handleEngineAction runs (the catalog's "*" specs and the HTTP
// OAuth token step) and the control blocks: none reaches a protocol adapter.
// The corpus carries a {{param.*}} in the metadata of each of them, so it
// holds this list to the Go catalog.
const ENGINE_ACTION_TYPES = new Set([
  'cache_clear', 'control_break', 'control_continue', 'control_if', 'control_loop', 'control_parallel', 'control_switch',
  'cookie_clear', 'cookie_set', 'custom_call', 'error_message', 'http_async_start', 'http_async_stop', 'http_connections_close',
  'http_oauth2_token', 'http_sync', 'live_table', 'native_javascript', 'rendezvous', 'review_required', 'source_database',
  'source_external', 'source_json', 'source_message', 'source_metric', 'source_parameter', 'source_review', 'source_runtime',
  'source_secret', 'source_timer', 'source_transaction', 'source_verification', 'source_xml',
])
// The send steps whose string metadata reaches the wire, by protocol family.
const METADATA_SENDING_ACTION_TYPES = new Map([
  ['kafka_produce', 'kafka'], ['kafka_request_reply', 'kafka'], ['jms_publish', 'jms'], ['jms_request_reply', 'jms'],
])
const METADATA_PROVENANCE_KEYS = new Set(['original_url', 'source_path', 'source_step_id', 'source_profile', 'source_uid', 'source_evidence'])

const isRecord = (value: unknown): value is Params => Boolean(value) && typeof value === 'object' && !Array.isArray(value)
const list = <T>(value: T[] | undefined | null): T[] => Array.isArray(value) ? value : []
const text = (value: unknown): string => typeof value === 'string' ? value : ''

/** dsl.CanonicalActionType: legacy loadrunner_* types run as their source_* equivalents. */
export function canonicalActionType(actionType: string): string {
  return actionType.startsWith(LEGACY_ACTION_TYPE_PREFIX) ? `source_${actionType.slice(LEGACY_ACTION_TYPE_PREFIX.length)}` : actionType
}

/** dsl.ProtocolFamily: http/1.1, http/2 and http/3 are HTTP; jms-* providers are JMS. */
export function protocolFamily(protocol: string): string {
  const trimmed = protocol.trim()
  if (trimmed === 'http/1.1' || trimmed === 'http/2' || trimmed === 'http/3') return 'http'
  if (trimmed.startsWith('jms-')) return 'jms'
  return trimmed
}

/** The {{var.*}} and {{param.*}} references in text, in order of first appearance and without duplicates. */
export function templateReferences(value: string): TemplateReference[] {
  const refs: TemplateReference[] = []
  const seen = new Set<string>()
  for (const match of value.matchAll(REFERENCE_PATTERN)) {
    const ref = { namespace: match[1] as TemplateReference['namespace'], name: match[2].trim() }
    const key = `${ref.namespace}\u0000${ref.name}`
    if (!ref.name || seen.has(key)) continue
    seen.add(key)
    refs.push(ref)
  }
  return refs
}

// fmt.Sprint of a JSON-decoded value, as the runtime formats source args.
function goSprint(value: unknown): string {
  if (typeof value === 'string') return value
  if (typeof value === 'boolean') return String(value)
  if (typeof value === 'number') return goFloat(value)
  if (value === null || value === undefined) return '<nil>'
  if (Array.isArray(value)) return `[${value.map(goSprint).join(' ')}]`
  if (isRecord(value)) return `map[${Object.keys(value).sort().map(key => `${key}:${goSprint(value[key])}`).join(' ')}]`
  return String(value)
}

// strconv's shortest %g: an exponent below -4 or from 6 up is written as
// d.ddde±XX, anything else as a plain decimal.
function goFloat(value: number): string {
  if (Number.isNaN(value)) return 'NaN'
  if (!Number.isFinite(value)) return value > 0 ? '+Inf' : '-Inf'
  if (value === 0) return Object.is(value, -0) ? '-0' : '0'
  const [mantissa, exponentText] = value.toExponential().split('e')
  const exponent = Number(exponentText)
  if (exponent < -4 || exponent >= 6) return `${mantissa}e${exponent < 0 ? '-' : '+'}${String(Math.abs(exponent)).padStart(2, '0')}`
  return String(value)
}

// actionParamString: a non-blank string, trimmed; undefined otherwise.
function paramString(params: Params | undefined, key: string): string | undefined {
  const value = params?.[key]
  if (typeof value !== 'string') return undefined
  const trimmed = value.trim()
  return trimmed ? trimmed : undefined
}

// actionParamBool: a bool, or a string strconv.ParseBool accepts.
function paramBool(params: Params | undefined, key: string): boolean {
  const value = params?.[key]
  if (typeof value === 'boolean') return value
  if (typeof value === 'string') return ['1', 't', 'T', 'TRUE', 'true', 'True'].includes(value.trim())
  return false
}

// firstEngineValue: the first non-blank value, trimmed.
function firstValue(fallback: string, values: string[]): string {
  return values.find(value => value.trim())?.trim() ?? fallback
}

// sourceActionFunction: params.function, else metadata.source_function, else the type.
function sourceFunction(action: ScriptActionDefinition): string {
  const fn = paramString(action.params, 'function')
  if (fn !== undefined) return fn
  const recorded = action.metadata?.source_function
  return typeof recorded === 'string' ? recorded : text(action.type)
}

// sourceActionArgs: a list or one scalar, each formatted and trimmed.
function sourceArgs(action: ScriptActionDefinition): string[] {
  const raw = action.params?.args
  if (raw === null || raw === undefined) return []
  return (Array.isArray(raw) ? raw : [raw]).map(value => goSprint(value).trim())
}

// structuredCallArgs: the object entries of params.call_args.
function callArgs(params: Params | undefined): Params[] {
  const raw = params?.call_args
  return Array.isArray(raw) ? raw.filter(isRecord) : []
}

// saveParamNameFromAction: a named param key, then a call_args entry. A
// call_args value is returned as written, so a blank one still ends the search.
function saveParamName(action: ScriptActionDefinition): string {
  for (const key of ['save_param', 'saveparam', 'save_param_name', 'param', 'parameter', 'name']) {
    const value = paramString(action.params, key)
    if (value !== undefined) return value
  }
  for (const entry of callArgs(action.params)) {
    if (['saveparam', 'save_param', 'param', 'parameter', 'name'].includes(text(entry.name).trim().toLowerCase())) return text(entry.value)
  }
  return ''
}

// sourceJSONParams: the importer's structured keys when json_object is set,
// else the Key=Value arguments of older imports.
function sourceJSONParams(action: ScriptActionDefinition): Record<string, string> {
  const params: Record<string, string> = {}
  for (const key of ['json_object', 'buffer', 'value_param', 'query', 'select_all', 'not_found', 'format', 'output_param']) {
    const value = action.params?.[key]
    if (typeof value === 'string') params[key] = value
  }
  if (params.json_object) return params
  const keys: Record<string, string> = { jsonobject: 'json_object', buffer: 'buffer', valueparam: 'value_param', querystring: 'query', selectall: 'select_all', notfound: 'not_found', format: 'format', outputparam: 'output_param' }
  for (const arg of sourceArgs(action)) {
    const separator = arg.indexOf('=')
    const key = separator < 0 ? undefined : keys[arg.slice(0, separator).trim().toLowerCase()]
    if (key) params[key] = arg.slice(separator + 1)
  }
  return params
}

// handleSourceParameterAction (pkg/engine/vuser_source_actions.go). Only the
// listed inputs are interpolated: snapshots and call_args are evidence.
function sourceParameterOutputs(action: ScriptActionDefinition): StepOutputs {
  const outputs: StepOutputs = { known: true }
  const args = sourceArgs(action)
  const write = (name: string) => {
    if (name.trim()) (outputs.writes ??= []).push({ name: name.trim(), lower: true })
  }
  const read = (...texts: string[]) => {
    if (texts.length) (outputs.readTexts ??= []).push(...texts)
  }
  switch (sourceFunction(action)) {
    case 'lr_save_string':
    case 'lr_save_int': {
      // Writes args[1]. A random source_array reads its lower(name)_count slot instead of the value.
      if (args.length < 2) break
      const array = action.params?.source_array
      if (isRecord(array)) {
        const name = text(array.name).trim().toLowerCase()
        if (name && text(array.selection) === 'random') read(`{{var.${name}_count}}`)
      } else {
        read(args[0])
      }
      write(args[1])
      break
    }
    case 'lr_save_var':
      if (args.length >= 4) { read(args[0]); write(args[3]) }
      break
    case 'lr_param_sprintf':
      if (args.length >= 2) { read(...args.slice(1)); write(args[0]) }
      break
    case 'lr_param_increment':
      // Its current value is a soft read that defaults to 0.
      if (args.length >= 1) write(args[0])
      break
    case 'lr_free_parameter':
      if (args.length >= 1) (outputs.removes ??= []).push(args[0])
      break
    case 'lr_save_timestamp':
      write(firstValue('timestamp', args))
      break
    case 'lr_save_datetime':
      // With more than one arg the last names the value and args[0] is the format.
      if (args.length > 1) { read(args[0]); write(args[args.length - 1]) } else write(firstValue('datetime', args))
      break
    case 'lr_whoami':
      write('vuser_id')
      write('vuser_group')
      break
    case 'lr_get_host_name':
    case 'lr_get_master_host_name':
      write(firstValue('host_name', args))
      break
    case 'lr_get_vuser_ip':
      write(firstValue('vuser_ip', args))
      break
    case 'web_convert_param': {
      // params.save_param, else args[0]; a blank name returns before the source is read.
      let name = paramString(action.params, 'save_param')
      if (name === undefined) name = args[0] ?? ''
      if (!name.trim()) break
      const source = paramString(action.params, 'source_string')
      if (source !== undefined) {
        read(source)
      } else {
        for (const arg of args.slice(1)) {
          const separator = arg.indexOf('=')
          if (separator >= 0 && arg.slice(0, separator).trim().toLowerCase() === 'sourcestring') { read(arg.slice(separator + 1)); break }
        }
      }
      write(name)
      break
    }
    case 'web_save_timestamp_param':
      if (args.length >= 1) write(args[0])
      break
    case 'web_save_param_length':
      // The length of args[0]'s value; an absent parameter has length 0.
      if (args.length >= 1) write(`${args[0].trim()}_Length`)
      break
    case 'web_convert_date_param':
      // Rewrites args[0] in place: no new name, a soft read of the current value.
      break
    default: {
      // The native fallback writes only a named SaveParam. Positional
      // lr_save_searched_string, lr_save_param_regexp and the like publish nothing.
      const name = saveParamName(action)
      if (!name.trim()) break
      const value = firstValue('', args)
      if (value) read(value)
      write(name)
    }
  }
  return outputs
}

// handleSourceJSONAction (pkg/engine/vuser_source_json.go). Any other JSON
// function fails its step, so its reads stay unknown.
function sourceJSONOutputs(action: ScriptActionDefinition): StepOutputs {
  const params = sourceJSONParams(action)
  switch (sourceFunction(action)) {
    case 'lr_eval_json':
      return { known: true, readTexts: [params.buffer ?? ''] }
    case 'lr_json_get_values': {
      // SelectAll=Yes publishes NAME_1..n and NAME_count; otherwise the first match, x+lc.
      const outputs: StepOutputs = { known: true, readTexts: [params.query ?? ''] }
      const name = (params.value_param ?? '').trim()
      if (!(params.json_object ?? '').trim() || !name) return outputs
      const array = (params.select_all ?? '').trim().toLowerCase() === 'yes'
      return { ...outputs, writes: [{ name, lower: true, ...(array ? { array } : {}) }] }
    }
    case 'lr_json_stringify': {
      const name = (params.output_param ?? '').trim()
      if (!(params.json_object ?? '').trim() || !name) return { known: true }
      return { known: true, writes: [{ name, lower: true }] }
    }
    default:
      return {}
  }
}

// handleSourceSecretAction: the named SaveParam, else the last arg, else the
// function without lr_. The saved value is a {{secret.*}} template.
function sourceSecretOutputs(action: ScriptActionDefinition): StepOutputs {
  const args = sourceArgs(action)
  let name = saveParamName(action)
  if (!name && args.length) name = args[args.length - 1]
  if (!name) name = sourceFunction(action).replace(/^lr_/, '')
  return name.trim() ? { known: true, writes: [{ name: name.trim(), lower: true }] } : { known: true }
}

/**
 * What the runtime reads and publishes for one leaf action (varflow.StepOutputs).
 * Extractors are not included; the walk registers them separately.
 */
export function stepOutputs(action: ScriptActionDefinition): StepOutputs {
  const params = action.params
  switch (canonicalActionType(text(action.type))) {
    case 'source_parameter':
      return sourceParameterOutputs(action)
    case 'source_json':
      return sourceJSONOutputs(action)
    case 'source_secret':
      return sourceSecretOutputs(action)
    case 'source_message':
      // status_message saves the rendered message as vuser_status, exact name only.
      if (paramBool(params, 'status_message')) return { writes: [{ name: 'vuser_status' }] }
      break
    case 'http_oauth2_token': {
      // The token family, x+lc; the handler interpolates every param.
      const name = paramString(params, 'save_as') ?? 'access_token'
      return { writes: ['', '_token_type', '_instance_url', '_expires_at'].map(suffix => ({ name: `${name}${suffix}`, lower: true })) }
    }
    case 'browser_evaluate_js':
      return { writes: [{ name: 'result' }] }
    case 'native_javascript': {
      // Only params.inputs reaches the resolver; outputs are <result_prefix>.<key>.
      const outputs: StepOutputs = { known: true }
      // A raw-JSON draft can hold any params value, so check it is an object first.
      if (isRecord(params) && params.inputs !== null && params.inputs !== undefined) outputs.readTexts = [JSON.stringify(params.inputs)]
      const prefix = text(params?.result_prefix).trim()
      if (prefix) outputs.prefixes = [prefix]
      return outputs
    }
    case 'live_table': {
      const prefix = text(params?.result_prefix).trim()
      if (prefix) return { prefixes: [prefix] }
      break
    }
    case 'source_review':
    case 'source_external':
    case 'custom_call':
    case 'source_database':
      // Log-only steps: nothing is interpolated.
      return { noReads: true }
    case 'source_transaction':
    case 'source_timer':
    case 'source_metric':
    case 'source_runtime':
    case 'rendezvous':
      // The runtime uses their name and args literally or only logs them.
      return { noReads: true }
  }
  return {}
}

// dsl.DecodeBodyEncoding: a base64 body as the bytes it stands for (each byte
// one character, enough for ASCII template references); null for a text body
// or one that does not decode.
function decodedBody(action: ScriptActionDefinition): string | null {
  if (text(action.body_encoding).trim().toLowerCase() !== 'base64') return null
  const encoded = text(action.body).trim().replace(/[\r\n]/g, '')
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(encoded)) return null
  try {
    return atob(encoded)
  } catch {
    return null
  }
}

// messaging.MetadataKeyIsProvenance: import provenance a send keeps off the
// wire. JMS still sends source_node_id and jmeter_* keys.
function metadataKeyIsProvenance(family: string, key: string): boolean {
  const normalized = key.trim().toLowerCase()
  if (METADATA_PROVENANCE_KEYS.has(normalized) || normalized.startsWith('asyncapi_')) return true
  return family === 'kafka' && (normalized === 'source_node_id' || normalized.startsWith('jmeter_'))
}

// messaging.MetadataKeyIsEvidence: provenance every send keeps off the wire.
// The runtime keeps it as recorded, so a reference there is never a read.
function metadataKeyIsEvidence(key: string): boolean {
  return metadataKeyIsProvenance('kafka', key) && metadataKeyIsProvenance('jms', key)
}

// sentMetadataTexts: Kafka copies each string metadata entry that is not
// provenance into the record headers, and JMS into the message properties.
function sentMetadataTexts(action: ScriptActionDefinition): string[] {
  const family = METADATA_SENDING_ACTION_TYPES.get(text(action.type).toLowerCase())
  if (!family || !isRecord(action.metadata)) return []
  return Object.entries(action.metadata).filter(([key, value]) => typeof value === 'string' && !metadataKeyIsProvenance(family, key)).map(([, value]) => value as string)
}

// executesThroughProtocol: engine steps and control blocks never reach a
// protocol adapter, and an http_sequence wrapper only annotates its aggregate
// result with its metadata; its children are protocol actions of their own.
function executesThroughProtocol(action: ScriptActionDefinition): boolean {
  const actionType = text(action.type)
  return actionType.trim().toLowerCase() !== 'http_sequence' && !ENGINE_ACTION_TYPES.has(canonicalActionType(actionType))
}

function actionReferences(action: ScriptActionDefinition, outputs: StepOutputs): TemplateReference[] {
  if (outputs.noReads) return []
  const refs = new Map<string, TemplateReference>()
  const collect = (value: string) => templateReferences(value).forEach(ref => refs.set(`${ref.namespace}\u0000${ref.name}`, ref))
  if (outputs.known) {
    list(outputs.readTexts).forEach(collect)
  } else {
    // The runtime interpolates metadata other than import evidence, but most
    // steps use it only to annotate results, so it is evidence apart from the
    // two uses below.
    collect(JSON.stringify({ ...action, metadata: undefined }))
    const body = decodedBody(action)
    if (body !== null) collect(body)
    sentMetadataTexts(action).forEach(collect)
    // Replay refuses a protocol action with an unresolved {{param.*}}
    // anywhere in it, metadata included, so a parameter use there is a read.
    // Import evidence is kept as recorded and exempt from the refusal.
    if (isRecord(action.metadata) && executesThroughProtocol(action)) {
      const refused = Object.fromEntries(Object.entries(action.metadata).filter(([key]) => !metadataKeyIsEvidence(key)))
      templateReferences(JSON.stringify(refused)).filter(ref => ref.namespace === 'param').forEach(ref => refs.set(`${ref.namespace}\u0000${ref.name}`, ref))
    }
  }
  return [...refs.values()]
}

interface FlowState {
  available: Set<string>
  saveAll: Set<string>
  /** Lowercased declared parameter names, shared by every branch. */
  parameterNames: Set<string>
  rowPrefixes: Set<string>
  dataSources: Set<string>
  /** live_table and native_javascript result prefixes; they outlive their block. */
  resultPrefixes: Set<string>
  /**
   * The available names the runtime merged as extractions: extractor outputs,
   * a browser evaluate result and a parallel join's own publications. Only
   * these leave a control_parallel branch; source steps and the OAuth token
   * step write variables directly.
   */
  extracted: Set<string>
}

interface ConfigReferences {
  baseURL: string[]
  httpTransport: string[]
  httpRequest: string[]
  soap: string[]
  javaHTTP: string[]
  protocol: string
}

const cloneState = (state: FlowState): FlowState => ({
  available: new Set(state.available),
  saveAll: new Set(state.saveAll),
  parameterNames: state.parameterNames,
  rowPrefixes: new Set(state.rowPrefixes),
  dataSources: state.dataSources,
  resultPrefixes: new Set(state.resultPrefixes),
  extracted: new Set(state.extracted),
})

const intersect = (left: Set<string>, right: Set<string>) => new Set([...left].filter(name => right.has(name)))

function intersectStates(target: FlowState, left: FlowState, right: FlowState): void {
  target.available = intersect(left.available, right.available)
  target.saveAll = intersect(left.saveAll, right.saveAll)
  target.resultPrefixes = intersect(left.resultPrefixes, right.resultPrefixes)
  target.extracted = intersect(left.extracted, right.extracted)
}

function replaceState(target: FlowState, source: FlowState): void {
  target.available = source.available
  target.saveAll = source.saveAll
  target.resultPrefixes = source.resultPrefixes
  target.extracted = source.extracted
}

// The runtime's lookup: exact variable names, parameters in any case, row and
// result prefixes, and name_N / name_count array slots.
function satisfiedVar(state: FlowState, name: string): boolean {
  for (const prefix of state.rowPrefixes) if (name.startsWith(`${prefix}.`)) return true
  for (const prefix of state.resultPrefixes) if (name.startsWith(`${prefix}.`)) return true
  if (state.available.has(name) || state.parameterNames.has(name.trim().toLowerCase())) return true
  for (const prefix of state.saveAll) {
    if (name === `${prefix}_count`) return true
    if (name.startsWith(`${prefix}_`) && /^\d+$/.test(name.slice(prefix.length + 1))) return true
  }
  return false
}

// A {{param.*}} reference resolves only from declared parameters.
function satisfied(state: FlowState, ref: TemplateReference): boolean {
  return ref.namespace === 'param' ? state.parameterNames.has(ref.name.trim().toLowerCase()) : satisfiedVar(state, ref.name)
}

// The {{var.*}} names in text, sorted.
function variableNames(value: string): string[] {
  return [...new Set(templateReferences(value).filter(ref => ref.namespace === 'var').map(ref => ref.name))].sort()
}

// configValueReferences: the {{var.*}} names in a configuration block's JSON.
const configVariableNames = (value: unknown) => variableNames(JSON.stringify(value ?? null) ?? '')

// scriptConfigReferences: the HTTP transport settings (also used by OAuth
// token requests), the per-request settings and the protocol-specific blocks.
function scriptConfigReferences(script: ScriptDefinition): ConfigReferences {
  const config = (isRecord(script.config) ? script.config : {}) as Params
  const http = isRecord(config.http) ? config.http : {}
  const { auth, default_headers, user_agent, accept_encoding, accept_language, ...transport } = http
  const webRuntime = isRecord(config.web_runtime) ? { ...config.web_runtime, additional_attributes: undefined } : config.web_runtime
  return {
    baseURL: variableNames(text(config.base_url)),
    httpTransport: configVariableNames(transport),
    // AdditionalAttributes is import evidence and never reaches the HTTP runtime.
    httpRequest: configVariableNames({ auth, default_headers, user_agent, accept_encoding, accept_language, web_runtime: webRuntime, browser_emulation: config.browser_emulation }),
    soap: configVariableNames(config.soap),
    javaHTTP: configVariableNames(config.java_http),
    protocol: text(script.protocol).trim().toLowerCase(),
  }
}

function actionUsesHTTPRequestConfig(action: ScriptActionDefinition): boolean {
  if (HTTP_REQUEST_ACTION_TYPES.has(canonicalActionType(text(action.type)).trim().toLowerCase())) return true
  return HTTP_METHODS.has(text(action.method).trim().toUpperCase() || text(action.type).trim().toUpperCase())
}

// Protocol configuration is flattened before init runs and never interpolated
// from parameters, seeds or extracted values, so each reference an action
// uses is unresolved wherever it is used.
function relevantConfigReferences(action: ScriptActionDefinition, refs: ConfigReferences): string[] {
  const actionType = canonicalActionType(text(action.type)).trim().toLowerCase()
  if (actionType === 'http_oauth2_token') return [...refs.httpTransport]
  if (!actionUsesHTTPRequestConfig(action)) return []
  const references = [...refs.httpTransport, ...refs.httpRequest]
  const target = text(action.target).trim().toLowerCase()
  if (!target.startsWith('http://') && !target.startsWith('https://')) references.push(...refs.baseURL)
  const family = protocolFamily(refs.protocol).trim().toLowerCase()
  if (family === 'soap' || actionType === 'soap_request') references.push(...refs.soap)
  if (family === 'java_http' || actionType === 'java_http_request') references.push(...refs.javaHTTP)
  return [...new Set(references)].sort()
}

// writesExtractions: the runtime merges a browser evaluate result as an
// extraction; native_javascript and live_table merge only their result
// prefixes, which a parallel join publishes on their own.
const writesExtractions = (action: ScriptActionDefinition) => canonicalActionType(text(action.type)) === 'browser_evaluate_js'

const emptyFlowState = (): FlowState => ({ available: new Set(), saveAll: new Set(), parameterNames: new Set(), rowPrefixes: new Set(), dataSources: new Set(), resultPrefixes: new Set(), extracted: new Set() })

// The HTTP adapter sends an unresolved {{var.*}} as its placeholder text and
// warns, and the SOAP and Java HTTP adapters send their requests through it;
// a JSON helper fails its step, and other protocols have no fallback.
function keepsVariablePlaceholders(action: ScriptActionDefinition, protocol: string): boolean {
  return PLACEHOLDER_PROTOCOL_FAMILIES.has(protocolFamily(protocol)) && canonicalActionType(text(action.type)) !== 'source_json'
}

// base64BodyVariables: Java HTTP decodes the stored body as base64 before
// sending it, so a placeholder there fails the step. A reference inside a
// body_encoding base64 body is re-encoded with the placeholder and sent.
function base64BodyVariables(action: ScriptActionDefinition, protocol: string): Set<string> {
  if (protocolFamily(protocol) !== 'java_http' || !executesThroughProtocol(action)) return new Set()
  return new Set(variableNames(text(action.body)))
}

// cloneControlShell: the control without its bodies, so only the shell's own
// condition and settings are read where the control executes.
function controlShell(action: ScriptActionDefinition): ScriptActionDefinition {
  const control = action.control
  return {
    ...action,
    extractors: undefined,
    control: control ? {
      ...control,
      steps: undefined,
      else: undefined,
      default: undefined,
      cases: Array.isArray(control.cases) ? control.cases.map(switchCase => ({ ...switchCase, steps: undefined })) : control.cases,
    } : control,
  }
}

// sequenceChildActions: the child requests decode as actions; a list that
// does not decode has no children.
function sequenceChildren(params: Params | undefined): ScriptActionDefinition[] {
  const requests = params?.requests
  if (requests === null || requests === undefined || !Array.isArray(requests)) return []
  if (!requests.every(request => request === null || isRecord(request))) return []
  return requests.map(request => (request || {}) as unknown as ScriptActionDefinition)
}

// sequenceWrapperOutputs: an http_sequence wrapper reads only the attribution
// the runtime interpolates for its aggregate result. Its metadata only
// annotates that result, so it is evidence as for any step that does not send it.
function sequenceWrapperOutputs(action: ScriptActionDefinition): StepOutputs {
  const attribution = isRecord(action.attribution) ? action.attribution : undefined
  return { known: true, readTexts: attribution ? [text(attribution.hierarchy_path), text(attribution.page_name)] : [] }
}

// postTestLoop: a condition-driven do_while loop skips its condition on the
// first pass, so the body runs before the condition is first read. A for_each
// or counted loop ignores do_while.
function postTestLoop(control: ScriptActionDefinition['control']): boolean {
  if (!control || control.for_each) return false
  return !(typeof control.count === 'number' && control.count > 0) && control.do_while === true
}

function loopRunsAtLeastOnce(control: ScriptActionDefinition['control']): boolean {
  if (!control || control.for_each) return false
  return (typeof control.count === 'number' && control.count > 0) || control.do_while === true
}

/**
 * varflow.Analyze: the references not supplied by replay seeds, script
 * parameters, or an output that executes earlier on the same path. Branches
 * keep only what every path provides, a loop body counts only when an
 * iteration is guaranteed, and an undeclared {{param.*}} always blocks.
 */
export function analyzeVariableReadiness(script: ScriptDefinition, seeded?: Record<string, string>): VariableReadiness {
  const config = scriptConfigReferences(script)
  const shellConfig: ConfigReferences = { baseURL: [], httpTransport: [], httpRequest: [], soap: [], javaHTTP: [], protocol: config.protocol }
  const missing = new Set<string>()
  const blocking = new Set<string>()
  const paramBlocking = new Set<string>()
  const configBlocking = new Set<string>()
  // The control_parallel branch being walked ('' outside any) and the scope
  // enclosing each branch. A branch runs on its own copy of the variables, so
  // what it writes is visible only within it and the branches nested in it.
  let scope = ''
  const scopeParents = new Map<string, string>()
  // Every {{var.*}} name that lacked a value at some use, with the scopes of
  // those uses, and every output any step publishes on any path, per scope,
  // to tell "produced too late" from "never produced".
  const unproven = new Map<string, Set<string>>()
  const written = new Map<string, FlowState>()
  const writtenIn = (key: string): FlowState => {
    let state = written.get(key)
    if (!state) written.set(key, state = emptyFlowState())
    return state
  }
  // Whether some step writes name where a use in this scope can see it: in
  // that scope or one enclosing it.
  const writtenWhereVisible = (name: string, useScope: string): boolean => {
    let key = useScope
    for (;;) {
      const state = written.get(key)
      if (state && satisfiedVar(state, name)) return true
      if (key === '') return false
      key = scopeParents.get(key) ?? ''
    }
  }

  const recordOutputs = (action: ScriptActionDefinition, outputs: StepOutputs, state: FlowState, refs: ConfigReferences) => {
    if (outputs.noReads) return
    const placeholder = keepsVariablePlaceholders(action, refs.protocol)
    const base64Body = base64BodyVariables(action, refs.protocol)
    for (const ref of actionReferences(action, outputs)) {
      if (satisfied(state, ref)) continue
      missing.add(ref.name)
      // An undeclared {{param.*}} always blocks, by product decision. The
      // runtime refuses a protocol action that carries one
      // (safeProtoExecuteStream); engine steps and control conditions would
      // run with the literal placeholder, which is never the intended value,
      // so the preflight refuses those too.
      if (ref.namespace === 'param') { blocking.add(ref.name); paramBlocking.add(ref.name); continue }
      let scopes = unproven.get(ref.name)
      if (!scopes) unproven.set(ref.name, scopes = new Set())
      scopes.add(scope)
      if (!placeholder || base64Body.has(ref.name)) blocking.add(ref.name)
    }
    for (const name of relevantConfigReferences(action, refs)) {
      missing.add(name)
      blocking.add(name)
      configBlocking.add(name)
    }
  }
  const record = (action: ScriptActionDefinition, state: FlowState, refs: ConfigReferences) => recordOutputs(action, stepOutputs(action), state, refs)

  // Extractor names are exact; mode=all publishes name_N and name_count instead of name.
  const register = (state: FlowState, extractors: ScriptActionDefinition['extractors']) => {
    const everWritten = writtenIn(scope)
    for (const extractor of list(extractors)) {
      const name = text(extractor?.name).trim()
      if (!name) continue
      if (text(extractor.mode).trim().toLowerCase() === 'all') {
        state.saveAll.add(name)
        state.available.delete(name)
        everWritten.saveAll.add(name)
      } else {
        state.available.add(name)
        state.extracted.add(name)
        state.saveAll.delete(name)
        everWritten.available.add(name)
      }
    }
  }

  // A step's own outputs, after its reads and extractors. Removals delete the
  // exact and lowercase names; a scalar write leaves an earlier array alone.
  // `extracted` marks writes the runtime merges as extractions.
  const apply = (state: FlowState, outputs: StepOutputs, extracted: boolean) => {
    const scopeWritten = writtenIn(scope)
    for (const name of list(outputs.removes)) {
      state.available.delete(name)
      state.available.delete(name.toLowerCase())
    }
    for (const write of list(outputs.writes)) {
      const name = write.name.trim()
      if (!name) continue
      const target = write.array ? state.saveAll : state.available
      const everWritten = write.array ? scopeWritten.saveAll : scopeWritten.available
      for (const alias of write.lower ? [name, name.toLowerCase()] : [name]) {
        target.add(alias)
        everWritten.add(alias)
        if (extracted && !write.array) state.extracted.add(alias)
      }
    }
    for (const prefix of list(outputs.prefixes)) {
      if (!prefix.trim()) continue
      state.resultPrefixes.add(prefix.trim())
      scopeWritten.resultPrefixes.add(prefix.trim())
    }
  }

  const walk = (actions: ScriptActionDefinition[] | undefined, prefix: string, state: FlowState, depth: number) => {
    if (depth > MAX_ACTION_DEPTH) return
    list(actions).forEach((action, index) => {
      if (!isRecord(action)) return
      const path = `${prefix}[${index}]`
      const control = action.control || undefined
      switch (text(action.type).trim().toLowerCase()) {
        case 'control_if': {
          record(controlShell(action), state, shellConfig)
          if (!control) return
          const thenState = cloneState(state)
          walk(control.steps, `${path}.control.steps`, thenState, depth + 1)
          // Without an else the branch may not run: only what was available before it stays guaranteed.
          if (!list(control.else).length) return
          const elseState = cloneState(state)
          walk(control.else, `${path}.control.else`, elseState, depth + 1)
          intersectStates(state, thenState, elseState)
          return
        }
        case 'control_switch': {
          record(controlShell(action), state, shellConfig)
          if (!control) return
          const branches = list(control.cases).map((switchCase, caseIndex) => {
            const caseState = cloneState(state)
            walk(switchCase?.steps, `${path}.control.cases[${caseIndex}].steps`, caseState, depth + 1)
            return caseState
          })
          if (list(control.default).length) {
            const defaultState = cloneState(state)
            walk(control.default, `${path}.control.default`, defaultState, depth + 1)
            branches.push(defaultState)
          } else {
            // No case is guaranteed to match.
            branches.push(cloneState(state))
          }
          const merged = branches[0]
          branches.slice(1).forEach(branch => intersectStates(merged, merged, branch))
          replaceState(state, merged)
          return
        }
        case 'control_loop': {
          const bodyState = cloneState(state)
          bodyState.available.add('loop_index')
          const forEach = control?.for_each
          if (forEach && state.dataSources.has(text(forEach.source).trim()) && text(forEach.var).trim()) bodyState.rowPrefixes.add(text(forEach.var).trim())
          // A post-test loop first reads its condition after the body has run once.
          const postTest = postTestLoop(control)
          if (!postTest) record(controlShell(action), bodyState, shellConfig)
          if (!control) return
          walk(control.steps, `${path}.control.steps`, bodyState, depth + 1)
          if (postTest) record(controlShell(action), bodyState, shellConfig)
          if (loopRunsAtLeastOnce(control)) {
            // The runtime restores loop locals on exit, including nested loops.
            if (!state.available.has('loop_index')) bodyState.available.delete('loop_index')
            replaceState(state, bodyState)
          }
          return
        }
        case 'control_parallel': {
          if (!control) return
          // Branches start alike, each on a copy of the variables with an
          // empty extraction ledger; the join publishes, as
          // parallel.<branch>.<name>, the ledger's names that are still set.
          const seed = cloneState(state)
          const enclosing = scope
          const enclosingWritten = writtenIn(enclosing)
          list(control.branches).forEach((branch, branchIndex) => {
            const branchPath = `${path}.control.branches[${branchIndex}]`
            const branchState = { ...cloneState(seed), extracted: new Set<string>() }
            scopeParents.set(branchPath, enclosing)
            scope = branchPath
            walk(branch?.steps, `${branchPath}.steps`, branchState, depth + 1)
            scope = enclosing
            const branchName = text(branch?.name)
            for (const name of branchState.extracted) {
              if (!branchState.available.has(name)) continue
              state.available.add(`parallel.${branchName}.${name}`)
              state.extracted.add(`parallel.${branchName}.${name}`)
              enclosingWritten.available.add(`parallel.${branchName}.${name}`)
            }
            for (const resultPrefix of branchState.resultPrefixes) {
              if (seed.resultPrefixes.has(resultPrefix)) continue
              state.resultPrefixes.add(`parallel.${branchName}.${resultPrefix}`)
              enclosingWritten.resultPrefixes.add(`parallel.${branchName}.${resultPrefix}`)
            }
          })
          return
        }
        case 'http_sequence':
          // Children run in order, each a request of its own. The wrapper
          // sends nothing: its target, headers, body and params are never
          // resolved (an LR transaction group copies its first request's
          // target there), and its own extractors never execute.
          recordOutputs(action, sequenceWrapperOutputs(action), state, shellConfig)
          sequenceChildren(action.params).forEach(child => {
            record(child, state, config)
            register(state, child.extractors)
          })
          return
        default: {
          const outputs = stepOutputs(action)
          recordOutputs(action, outputs, state, config)
          register(state, action.extractors)
          apply(state, outputs, writesExtractions(action))
        }
      }
    })
  }

  const parameterNames = new Set(list(script.parameters).map(parameter => text(parameter?.name).trim().toLowerCase()).filter(Boolean))
  const state: FlowState = {
    available: new Set(Object.keys(seeded || {}).map(name => name.trim()).filter(Boolean)),
    saveAll: new Set(),
    parameterNames,
    rowPrefixes: new Set(),
    dataSources: new Set(list(script.data_sources).map(source => text(source?.name))),
    resultPrefixes: new Set(),
    extracted: new Set(),
  }
  walk(script.init, 'init', state, 1)
  walk(script.actions, 'actions', state, 1)
  walk(script.teardown, 'teardown', state, 1)

  return {
    missing: [...missing].sort(),
    blocking: [...blocking].sort(),
    lateProduced: [...unproven].filter(([name, scopes]) => [...scopes].some(useScope => writtenWhereVisible(name, useScope))).map(([name]) => name).sort(),
    paramBlocking: [...paramBlocking].sort(),
    configBlocking: [...configBlocking].sort(),
  }
}
