import type { PartialRuntimeSettingsOverride, RuntimeSettingsOverride } from '../../types/runtimeSettings'
import type { ScriptConfig } from '../../types/scripts'
import type { ContinueOnErrorResolution } from '../../types/testConfiguration'
import {
  JAVA_VUSER_OVERRIDE_FIELDS,
  RUNTIME_OVERRIDE_SCHEMA,
  effectiveRuntimeValue,
  resolveRuntimeOverrideFields,
  resolveRuntimeRowsValue,
} from '../runtimeSettingsModel'
import {
  CANONICAL_RUNTIME_GROUPS,
  getCanonicalGroupForPath,
  type CanonicalRuntimeGroupId,
  type RuntimeValueProvenance,
} from './canonicalRuntimeTaxonomy'
import { REDACTED_SECRET, formatOverrideValue } from './scenarioOverrideValues'

// The read-only "effective runtime settings" summary shared by the queued
// scenario preview, the test manifest, the run report and the archive. It
// resolves every path through the stored override rows exactly as execution
// applies them (resolveRuntimeRowsValue: group row over the scenario/run row; a
// partial row supplies only the paths override_fields lists, a v1 row supplies
// every section it carries whole), then the script's own value when the
// surface knows the script. Values arrive masked by the API (maskRuntimeSettings)
// and are never shown raw: the "[REDACTED]" mask reads as a stored secret, a
// {{secret.NAME}} reference reads as a reference, and credential leaves are
// never echoed.

/** A stored override row as the API returns it (masked), v1 or partial. */
export type StoredRuntimeRow = RuntimeSettingsOverride | PartialRuntimeSettingsOverride | null | undefined

export interface EffectiveRuntimeGroupSource {
  /** Group name, shown as the block heading. */
  name: string
  protocol?: string
  /** The group's own stored override row. */
  row?: StoredRuntimeRow
  /**
   * The group's effective iteration count (TestGroup.iterations): the root and group rows merged
   * with the compact groups[].iterations, or the script cap an iteration-terminated run keeps.
   * It is a resolved value, not a layer, so it never decides provenance on its own.
   */
  iterations?: number
  /** The compact groups[].iterations the group itself set (TestGroup.iteration_override). */
  iterationOverride?: number
  /** Whether iterationOverride is authoritative (absent on rows stored before it existed). */
  iterationOverrideKnown?: boolean
  /** How the API resolved continue-on-error for this group, when it reports it. */
  continueOnError?: ContinueOnErrorResolution
  /**
   * The schedule's termination mode, when the surface knows it (the queued preview). A duration
   * schedule removes a closed-model script's per-VUser iteration cap at launch.
   */
  scheduleTerminationMode?: string
  /** The script's config, when the surface has it (the queued preview's library reference). */
  scriptConfig?: ScriptConfig
}

export type EffectiveRuntimeValueKind =
  | 'value'
  /** A layer supplies the path with no value: the engine default applies. */
  | 'cleared'
  /** The API's "[REDACTED]" mask, or a controller-owned runtime secret reference. */
  | 'stored_secret'
  /** A credential leaf that carries a value; never echoed. */
  | 'hidden'
  /** A {{secret.NAME}} project-secret reference. */
  | 'secret_reference'
  /** A section a layer replaces whole (v1 rows, or a partial row listing the section). */
  | 'section'

export interface EffectiveRuntimeEntry {
  path: string
  label: string
  kind: EffectiveRuntimeValueKind
  /** Display text; never a raw secret value. */
  text: string
  /** The referenced secret's name, for kind secret_reference. */
  secretName?: string
  /** The layer that supplies the value; absent when only the resolved value is known (legacy rows). */
  provenance?: RuntimeValueProvenance
}

export type EffectiveRuntimeCategoryId = CanonicalRuntimeGroupId | 'other'

export interface EffectiveRuntimeCategory {
  id: EffectiveRuntimeCategoryId
  label: string
  entries: EffectiveRuntimeEntry[]
}

export interface EffectiveRuntimeSummary {
  categories: EffectiveRuntimeCategory[]
  /** Entries an override layer (group or scenario) supplies. */
  overrideCount: number
  /** Whether script values were known when the summary was resolved. */
  scriptKnown: boolean
}

const OTHER_CATEGORY_LABEL = 'Other runtime settings'

/** Paths a summary shows from the script when the surface knows the script and no override covers them. */
export const SCRIPT_KEY_PATHS = [
  'base_url',
  'http.version',
  'http.request_timeout_ms',
  'http.connect_timeout_ms',
  'http.tls.min_version',
  'http.tls.max_version',
  'soap.version',
  'soap.fault_handling.fail_on_fault',
  'soap.ws_security.username_token.password_type',
  'run_logic.iterations',
  'think_time.type',
  'pacing.type',
  'error_handling.continue_on_error',
  'log.level',
] as const

const FIELD_LABELS: Readonly<Record<string, string>> = {
  base_url: 'Base URL',
  'http.version': 'HTTP version',
  'http.request_timeout_ms': 'Request timeout (ms)',
  'http.connect_timeout_ms': 'Connect timeout (ms)',
  'http.tls': 'TLS settings',
  'http.tls.min_version': 'TLS min version',
  'http.tls.max_version': 'TLS max version',
  soap: 'SOAP settings',
  'soap.version': 'SOAP version',
  'soap.fault_handling.fail_on_fault': 'Fail on SOAP fault',
  'soap.ws_security': 'WS-Security',
  'soap.ws_security.username_token.username': 'WS-Security username',
  'soap.ws_security.username_token.password': 'WS-Security password',
  'soap.ws_security.username_token.password_type': 'WS-Security password type',
  'soap.ws_security.username_token.nonce': 'WS-Security nonce',
  'soap.ws_security.username_token.created': 'WS-Security created timestamp',
  'soap.ws_security.timestamp.ttl_seconds': 'WS-Security timestamp TTL (seconds)',
  'run_logic.iterations': 'Iterations',
  'log.level': 'Log level',
  // Every field the debugger's session overrides editor offers has a name here, so the
  // debugger and the read-only views (preview, manifest, run report, archive) agree.
  'http.user_agent': 'User agent',
  'think_time.type': 'Think time type',
  'think_time.mean_ms': 'Think time mean (ms)',
  'think_time.min_ms': 'Think time min (ms)',
  'think_time.max_ms': 'Think time max (ms)',
  'think_time.multiplier': 'Think time multiplier',
  'think_time.limit_ms': 'Think time limit (ms)',
  'http.tls_timeout_ms': 'TLS handshake timeout (ms)',
  'http.idle_timeout_ms': 'Idle timeout (ms)',
  'http.max_conns_per_host': 'Max connections per host',
  'http.keep_alive': 'Keep-alive',
  'http.tls.insecure_skip_verify': 'Skip TLS verification',
  'http.proxy_mode': 'Proxy mode',
  'http.proxy.url': 'Proxy URL',
  'http.proxy.pac_url': 'PAC URL',
  'http.proxy.username': 'Proxy username',
  'http.proxy.password': 'Proxy password',
  'http.auth.type': 'Auth type',
  'http.auth.username': 'Auth username',
  'http.auth.password': 'Auth password',
  'http.auth.token': 'Auth token',
  'http.auth.domain': 'Auth domain',
  'http.max_redirects': 'Max redirects',
  'http.cookies_enabled': 'Cookies',
  'error_handling.max_retries': 'Max retries',
  'error_handling.retry_delay_ms': 'Retry delay (ms)',
  'error_handling.retry_backoff': 'Retry backoff',
  'error_handling.snapshot_on_error': 'Snapshot on error',
  'log.on_error_only': 'Extended log only on errors',
  'log.max_response_bytes': 'Max logged response (bytes)',
  // The group editor's own names for the paths it edits, so the read-only views match it.
  'error_handling.continue_on_error': 'Continue on error',
  'error_handling.fail_transaction_on_error_message': 'Fail transaction on error message',
  'error_handling.honor_retry_after': 'Honor Retry-After',
  'error_handling.retry_after_max_ms': 'Retry-After cap (ms)',
  'error_handling.retry_on': 'Retry on',
  'pacing.type': 'Pacing',
  'pacing.arrival_process': 'Arrival process',
  'pacing.burstiness': 'Burstiness',
  'pacing.delay_ms': 'Pacing delay (ms)',
  'pacing.interval_ms': 'Pacing interval (ms)',
  'pacing.min_delay_ms': 'Min pacing delay (ms)',
  'pacing.max_delay_ms': 'Max pacing delay (ms)',
  'pacing.min_interval_ms': 'Min pacing interval (ms)',
  'pacing.max_interval_ms': 'Max pacing interval (ms)',
  'pacing.quota.adaptive_aimd': 'Adaptive AIMD throttling',
  'pacing.rate_stages': 'Rate stages',
  'pacing.seed': 'Arrival seed',
  'pacing.tps': 'Target TPS',
  'run_logic.action_sequence': 'Action sequence',
  'run_logic.auto_transactions': 'Auto-transactions',
  'run_logic.block_scheduling': 'Block scheduling',
  'run_logic.blocks': 'Run logic blocks',
  'run_logic.global_iterations': 'Global iterations',
  'run_logic.max_in_flight': 'Maximum in-flight iterations',
  'run_logic.max_transactions': 'Max transactions',
  'run_logic.termination_mode': 'Termination mode',
  'run_logic.workload_model': 'Workload model',
  'think_time.distribution': 'Think time distribution',
  'think_time.std_ms': 'Think time std dev (ms)',
  'http.auth.hosts': 'Send credentials only to hosts',
  'http.cache_enabled': 'HTTP cache',
  'http.clear_cookies_per_iteration': 'Clear learned cookies each iteration',
  'http.max_idle_conns': 'Max idle connections',
  'http.proxy.no_proxy': 'No-proxy hosts',
  'http.tls.ca_cert_file': 'CA cert file',
  'http.tls.client_cert_file': 'Client cert file',
  'http.tls.client_key_file': 'Client key file',
  'http.traceparent': 'Trace context',
  'http.traceparent_sampled': 'Trace sampling',
  'browser.capture_screenshots': 'Capture screenshots',
  'browser.device_profile': 'Device profile',
  'browser.download_resources': 'Download resources',
  'browser.engine': 'Browser engine',
  'browser.headless': 'Headless',
  'browser.orientation': 'Orientation',
  'browser.quic': 'HTTP/3 (QUIC)',
  'browser.slow_mo_ms': 'Slow-mo (ms)',
  'browser.system_language': 'Emulated system language',
  'browser.touch_mode': 'Touch mode',
  'browser.user_agent': 'Browser user agent',
  'browser.viewport.height': 'Viewport height',
  'browser.viewport.width': 'Viewport width',
  'browser_emulation.browser_type': 'Browser type',
  'browser_emulation.browser_version': 'Browser version',
  'browser_emulation.cache.clear_cache_for_new_user': 'Clear cache for a new user',
  'browser_emulation.custom_user_agent': 'Custom User-Agent',
  'browser_emulation.language': 'Browser language',
  'browser_emulation.mode': 'Browser emulation mode',
  'browser_emulation.platform': 'Browser platform',
}

const LABEL_WORDS: Readonly<Record<string, string>> = {
  http: 'HTTP', https: 'HTTPS', tls: 'TLS', soap: 'SOAP', url: 'URL', urls: 'URLs', ws: 'WS', ms: '(ms)',
  ip: 'IP', ips: 'IPs', jdk: 'JDK', jvm: 'JVM', vm: 'VM', dns: 'DNS', html: 'HTML', quic: 'QUIC', pac: 'PAC',
  ca: 'CA', id: 'ID', tps: 'TPS', tpm: 'TPM', rpm: 'RPM', ttl: 'TTL', mime: 'MIME', vusers: 'VUsers', csv: 'CSV',
  har: 'HAR', jwt: 'JWT', aimd: 'AIMD', ept: 'EPT', xbootclasspath: 'Xbootclasspath',
}

const SECTION_WORDS: Readonly<Record<string, string>> = {
  env: 'Environment variable',
  java_vuser: 'Java',
  browser: 'Browser',
  web_runtime: 'Web runtime',
}

function humanize(segment: string): string[] {
  return segment.split('_').filter(Boolean).map(word => LABEL_WORDS[word.toLowerCase()] ?? word.toLowerCase())
}

/** Whether a path has a curated name rather than one built from its segments. */
export function hasCuratedRuntimeFieldLabel(path: string): boolean {
  return Object.prototype.hasOwnProperty.call(FIELD_LABELS, path)
}

/** A sentence-case label for a canonical override path. */
export function effectiveRuntimeFieldLabel(path: string): string {
  const known = FIELD_LABELS[path]
  if (known) return known
  const [root, ...rest] = path.split('.')
  if (root === 'env' && rest.length > 0) return `Environment variable ${rest.join('.')}`
  const words = [...(SECTION_WORDS[root] ? [SECTION_WORDS[root]] : humanize(root))]
  // A map key (a header name, an attribute) is kept verbatim.
  rest.forEach((segment, index) => {
    if (index > 0 && mapKind(path.split('.').slice(0, index + 1).join('.'))) words.push(segment)
    else words.push(...humanize(segment))
  })
  const text = words.join(' ')
  return text.charAt(0).toUpperCase() + text.slice(1)
}

// ---------------------------------------------------------------------------
// Path shapes, from the generated override schema.

type PathKind = { kind: 'struct'; fields: readonly string[] } | { kind: 'map' } | { kind: 'leaf' }

const structFieldCache = new Map<string, Map<string, string>>()

function structFields(name: string): Map<string, string> {
  const cached = structFieldCache.get(name)
  if (cached) return cached
  const fields = new Map<string, string>()
  for (const entry of (RUNTIME_OVERRIDE_SCHEMA[name] ?? '').split(' ')) {
    if (!entry) continue
    const [field, kind = ''] = entry.split(':')
    fields.set(field, kind)
  }
  structFieldCache.set(name, fields)
  return fields
}

function pathKind(path: string): PathKind {
  if (path === 'env') return { kind: 'map' }
  if (path === 'java_vuser') return { kind: 'struct', fields: JAVA_VUSER_OVERRIDE_FIELDS }
  if (path.startsWith('env.') || path.startsWith('java_vuser.') || path === 'redaction_rules') return { kind: 'leaf' }
  let struct = 'ScriptConfig'
  const segments = path.split('.')
  for (let index = 0; index < segments.length; index++) {
    const kind = structFields(struct).get(segments[index])
    if (kind === undefined || kind === '') return { kind: 'leaf' }
    if (kind === '*') return index === segments.length - 1 ? { kind: 'map' } : { kind: 'leaf' }
    if (index === segments.length - 1) return { kind: 'struct', fields: [...structFields(kind).keys()] }
    struct = kind
  }
  return { kind: 'leaf' }
}

function mapKind(path: string): boolean {
  return pathKind(path).kind === 'map'
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function hasDisplayValue(value: unknown): boolean {
  if (value === undefined || value === null || value === '') return false
  if (Array.isArray(value)) return value.length > 0
  if (isPlainObject(value)) return Object.keys(value).length > 0
  return true
}

/**
 * The leaf paths a value at `path` carries: a struct walks its set fields, a
 * map lists its keys, anything else is one leaf. Nested structs with nothing
 * set drop out; the top path is kept when it carries nothing (an empty
 * section a v1 row applies whole).
 */
function leafPaths(path: string, value: unknown, top = true): string[] {
  const shape = pathKind(path)
  if (shape.kind === 'leaf') return top || hasDisplayValue(value) ? [path] : []
  const children: string[] = []
  if (isPlainObject(value)) {
    const keys = shape.kind === 'struct' ? shape.fields.filter(field => Object.prototype.hasOwnProperty.call(value, field)) : Object.keys(value)
    for (const key of keys) {
      const child = `${path}.${key}`
      if (shape.kind === 'map') {
        children.push(child)
        continue
      }
      children.push(...leafPaths(child, value[key], false))
    }
  }
  if (children.length > 0) return children
  return top ? [path] : []
}

// ---------------------------------------------------------------------------
// Value display.

const SECRET_REFERENCE = /^\{\{\s*secret\.\s*([A-Za-z0-9_.-]{1,249})\s*\}\}$/
/** Controller-owned references the server writes in place of a typed credential (pkg/api runtimeOverrideSecretPrefix). */
const RUNTIME_SECRET_PREFIX = 'APEX_RUNTIME_OVERRIDE_'
const CREDENTIAL_LEAF = /(password|passphrase|secret|token|api[_-]?key|authorization|credential|private[_-]?key|cookie)/i
/** A leaf that holds a credential; a leaf naming its kind (password_type) does not. */
function credentialLeaf(name: string): boolean {
  return CREDENTIAL_LEAF.test(name) && !/_(type|mode)$/i.test(name)
}
const MASK_TOKENS = [REDACTED_SECRET, '%5BREDACTED%5D', '%5bREDACTED%5d']

function containsMask(text: string): boolean {
  return MASK_TOKENS.some(token => text.includes(token))
}

function withoutMasks(text: string): string {
  return MASK_TOKENS.reduce((current, token) => current.split(token).join('***'), text)
}

function describeValue(path: string, value: unknown): Pick<EffectiveRuntimeEntry, 'kind' | 'text' | 'secretName'> {
  if (!hasDisplayValue(value)) {
    return { kind: 'cleared', text: 'Not set (engine default applies)' }
  }
  const leaf = path.slice(path.lastIndexOf('.') + 1)
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (trimmed === REDACTED_SECRET) return { kind: 'stored_secret', text: 'Stored secret (hidden)' }
    const reference = SECRET_REFERENCE.exec(trimmed)
    if (reference) {
      return reference[1].toUpperCase().startsWith(RUNTIME_SECRET_PREFIX)
        ? { kind: 'stored_secret', text: 'Stored secret (hidden)' }
        : { kind: 'secret_reference', text: 'Secret reference', secretName: reference[1] }
    }
    if (credentialLeaf(leaf) || (path.startsWith('env.') && credentialLeaf(path.slice('env.'.length)))) {
      return { kind: 'hidden', text: 'Set (hidden)' }
    }
    // A masked URL or header keeps its shape; the masked parts read as ***.
    const shown = formatOverrideValue(value)
    if (containsMask(shown)) {
      return /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed)
        ? { kind: 'value', text: withoutMasks(shown) }
        : { kind: 'stored_secret', text: 'Stored secret (hidden)' }
    }
    return { kind: 'value', text: shown }
  }
  if (Array.isArray(value)) {
    if (value.some(item => isPlainObject(item) || Array.isArray(item))) {
      return { kind: 'value', text: `${value.length} ${value.length === 1 ? 'entry' : 'entries'}` }
    }
    if (credentialLeaf(leaf)) return { kind: 'hidden', text: 'Set (hidden)' }
    const items = value.map(item => describeValue(`${path}.item`, item))
    if (items.some(item => item.kind === 'stored_secret' || item.kind === 'hidden')) {
      return { kind: 'value', text: items.map(item => (item.kind === 'value' ? item.text : '***')).join(', ') }
    }
    return { kind: 'value', text: items.map(item => item.secretName ? `{{secret.${item.secretName}}}` : item.text).join(', ') }
  }
  if (isPlainObject(value)) {
    const count = Object.keys(value).length
    return { kind: 'value', text: `${count} ${count === 1 ? 'field' : 'fields'} set` }
  }
  return { kind: 'value', text: formatOverrideValue(value) }
}

/** One value as the read-only views show it: masks, references and credentials never read as values. */
export function formatEffectiveRuntimeValue(path: string, value: unknown): string {
  const described = describeValue(path, value)
  return described.kind === 'secret_reference' ? `${described.text} {{secret.${described.secretName}}}` : described.text
}

// ---------------------------------------------------------------------------
// Resolution.

const LAYER_PROVENANCE: readonly RuntimeValueProvenance[] = ['group_override', 'scenario_default']

function rowFields(row: StoredRuntimeRow): string[] {
  return row ? resolveRuntimeOverrideFields(row as PartialRuntimeSettingsOverride) : []
}

/** The canonical group a whole section reads under (the taxonomy maps its fields, not the section). */
const SECTION_CATEGORIES: Readonly<Record<string, CanonicalRuntimeGroupId>> = {
  http: 'transport_connections',
  websocket: 'transport_connections',
  soap: 'protocol_behavior_session',
  web_runtime: 'protocol_behavior_session',
  browser_emulation: 'target_identity',
  run_logic: 'run_logic_pacing',
  pacing: 'run_logic_pacing',
  think_time: 'think_time',
  error_handling: 'errors_logging_diagnostics',
  log: 'errors_logging_diagnostics',
  results: 'errors_logging_diagnostics',
}

function categoryFor(path: string): EffectiveRuntimeCategoryId {
  const direct = getCanonicalGroupForPath(path) ?? SECTION_CATEGORIES[path]
  if (direct) return direct
  // A nested struct (http.quic) reads under the group that holds its fields.
  const holder = CANONICAL_RUNTIME_GROUPS.find(definition => definition.configPaths.some(field => field.startsWith(`${path}.`)))
  return holder?.id ?? 'other'
}

const ITERATIONS_PATH = 'run_logic.iterations'
const CONTINUE_ON_ERROR_PATH = 'error_handling.continue_on_error'

type ResolvedValue = { value: unknown; provenance?: RuntimeValueProvenance; text?: string }

/** The provenance of each continue-on-error source the API reports (continue_on_error_policy.go). */
const CONTINUE_ON_ERROR_PROVENANCE: Readonly<Record<string, RuntimeValueProvenance>> = {
  group: 'group_override',
  scenario: 'scenario_default',
  script: 'script',
  scenario_default: 'scenario_default',
}

/** Whether a row states continue-on-error: it supplies the path with a value (a section omitting it states nothing). */
function statedContinueOnError(row: StoredRuntimeRow): boolean | undefined {
  if (!row) return undefined
  const { index, value } = resolveRuntimeRowsValue(CONTINUE_ON_ERROR_PATH, [row])
  return index === 0 && typeof value === 'boolean' ? value : undefined
}

/**
 * Continue-on-error as execution resolves it: the API's resolution when the source carries it,
 * otherwise the same layering (resolveGroupContinueOnError) — the group or scenario row only when
 * it states the value, then the script, then the scenario's enabled-by-default policy.
 */
function resolveContinueOnError(group: EffectiveRuntimeGroupSource, scenarioRow: StoredRuntimeRow): ResolvedValue | undefined {
  const reported = group.continueOnError
  if (reported && typeof reported.effective === 'boolean') {
    return { value: reported.effective, provenance: CONTINUE_ON_ERROR_PROVENANCE[reported.source] }
  }
  const groupValue = statedContinueOnError(group.row)
  if (groupValue !== undefined) return { value: groupValue, provenance: 'group_override' }
  const scenarioValue = statedContinueOnError(scenarioRow)
  if (scenarioValue !== undefined) return { value: scenarioValue, provenance: 'scenario_default' }
  // Without the script, a layer that states nothing leaves the outcome unknown.
  if (!group.scriptConfig) return undefined
  const scriptValue = group.scriptConfig.error_handling?.continue_on_error
  if (typeof scriptValue === 'boolean') return { value: scriptValue, provenance: 'script' }
  return { value: true, provenance: 'scenario_default' }
}

/**
 * The effective runtime settings one group runs with, by canonical group:
 * every path the group row or the scenario/run row supplies (with the layer
 * that wins), the sections a layer replaces whole, and — when the script is
 * known — the key script values no override covers.
 */
export function buildEffectiveRuntimeSummary(group: EffectiveRuntimeGroupSource, scenarioRow?: StoredRuntimeRow): EffectiveRuntimeSummary {
  const rows = [group.row ?? undefined, scenarioRow ?? undefined]
  const entries = new Map<string, EffectiveRuntimeEntry>()

  const resolveLayers = (path: string): ResolvedValue | undefined => {
    const { index, value } = resolveRuntimeRowsValue(path, rows)
    if (index >= 0) return { value, provenance: LAYER_PROVENANCE[index] }
    if (!group.scriptConfig) return undefined
    const script = effectiveRuntimeValue(path, { script: group.scriptConfig })
    return hasDisplayValue(script.value) ? script : undefined
  }

  // The script's per-VUser cap survives launch only when an iteration schedule, an
  // iteration-terminated script or an open workload model keeps it
  // (applyScheduleIterationAuthority); a duration schedule owns run length otherwise.
  const scheduleKeepsScriptCap = (): boolean => {
    if (group.scheduleTerminationMode?.trim().toLowerCase() === 'iterations') return true
    if (String(resolveLayers('run_logic.termination_mode')?.value ?? '').trim().toLowerCase() === 'iterations') return true
    return String(resolveLayers('run_logic.workload_model')?.value ?? '').trim().toLowerCase() === 'open'
  }

  const resolveIterations = (): ResolvedValue | undefined => {
    const effective = typeof group.iterations === 'number' ? group.iterations : undefined
    if (group.iterationOverrideKnown) {
      if (typeof group.iterationOverride === 'number') return { value: group.iterationOverride, provenance: 'group_override' }
      const fromRows = resolveRuntimeRowsValue(ITERATIONS_PATH, rows)
      if (fromRows.index >= 0) return { value: fromRows.value, provenance: LAYER_PROVENANCE[fromRows.index] }
      // No layer sets it: an effective count is the script cap an iteration-terminated run kept.
      if (effective !== undefined) return { value: effective, provenance: 'script' }
    } else if (effective !== undefined) {
      // A legacy row: the effective count is known, the layer that set it is not.
      const fromRows = resolveRuntimeRowsValue(ITERATIONS_PATH, rows)
      if (fromRows.index >= 0 && fromRows.value === effective) return { value: effective, provenance: LAYER_PROVENANCE[fromRows.index] }
      return { value: effective }
    }
    const resolved = resolveLayers(ITERATIONS_PATH)
    if (resolved?.provenance !== 'script' || scheduleKeepsScriptCap()) return resolved
    return { value: resolved.value, provenance: 'script', text: `Script cap ${formatOverrideValue(resolved.value)} (overridden by the schedule)` }
  }

  const resolvePath = (path: string): ResolvedValue | undefined => {
    if (path === ITERATIONS_PATH) return resolveIterations()
    if (path === CONTINUE_ON_ERROR_PATH) return resolveContinueOnError(group, scenarioRow ?? undefined)
    return resolveLayers(path)
  }

  const add = (path: string, onlyWhenOverridden = false) => {
    if (entries.has(path)) return
    const resolved = resolvePath(path)
    if (!resolved) return
    if (onlyWhenOverridden && resolved.provenance === 'script') return
    const described = describeValue(path, resolved.value)
    entries.set(path, {
      path,
      label: effectiveRuntimeFieldLabel(path),
      provenance: resolved.provenance,
      ...described,
      ...(resolved.text ? { text: resolved.text } : {}),
    })
  }

  // Sections a layer replaces whole come first, so their note leads the leaves.
  // A listed java_vuser overlays the leaves it sets rather than replacing the section.
  rows.forEach((row, rowIndex) => {
    for (const field of rowFields(row)) {
      if (field === 'java_vuser' || pathKind(field).kind !== 'struct') continue
      const winner = resolveRuntimeRowsValue(field, rows).index
      if (winner !== rowIndex || entries.has(field)) continue
      entries.set(field, {
        path: field,
        label: effectiveRuntimeFieldLabel(field),
        provenance: LAYER_PROVENANCE[rowIndex],
        kind: 'section',
        text: field === 'error_handling'
          ? "Replaced whole; the script's values for this section do not apply, except continue on error, which resolves on its own"
          : "Replaced whole; the script's values for this section do not apply",
      })
    }
  })
  if (group.scriptConfig) {
    for (const path of SCRIPT_KEY_PATHS) add(path)
  } else {
    add(ITERATIONS_PATH)
    add(CONTINUE_ON_ERROR_PATH)
  }
  rows.forEach(row => {
    for (const field of rowFields(row)) {
      const { value } = resolveRuntimeRowsValue(field, [row])
      for (const leaf of leafPaths(field, value)) {
        if (leaf === field && pathKind(field).kind === 'struct') continue
        add(leaf, true)
      }
    }
  })

  const byCategory = new Map<EffectiveRuntimeCategoryId, EffectiveRuntimeEntry[]>()
  for (const entry of entries.values()) {
    const id = categoryFor(entry.path)
    byCategory.set(id, [...(byCategory.get(id) ?? []), entry])
  }
  const categories: EffectiveRuntimeCategory[] = [
    ...CANONICAL_RUNTIME_GROUPS.map(definition => ({ id: definition.id as EffectiveRuntimeCategoryId, label: definition.label })),
    { id: 'other' as const, label: OTHER_CATEGORY_LABEL },
  ]
    .map(category => ({ ...category, entries: byCategory.get(category.id) ?? [] }))
    .filter(category => category.entries.length > 0)
  const overrideCount = [...entries.values()].filter(entry => entry.provenance === 'group_override' || entry.provenance === 'scenario_default').length
  return { categories, overrideCount, scriptKnown: Boolean(group.scriptConfig) }
}
