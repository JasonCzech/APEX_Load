import type { ScriptConfig } from '../types/scripts'
import type { RuntimeSettingsSectionId } from './scriptEditorRuntimeSettingsModel'
import type { CanonicalRuntimeGroupId } from './runtime-settings/canonicalRuntimeTaxonomy'

/** The engine caps an exponential think-time draw at this multiple of max_ms. */
export const EXPONENTIAL_THINK_TIME_CAP_FACTOR = 4

/**
 * Longest exponential think time before Limit (s), in ms, mirroring
 * engine computeThinkTime: the draw is capped at max_ms × 4, then the
 * multiplier (<= 0 means 1) is applied, then limit_ms. Returns 0 when
 * max_ms is unset (no cap).
 */
export function exponentialThinkTimeStopMs(think: { max_ms?: number; multiplier?: number } | undefined): number {
  const maxMs = think?.max_ms ?? 0
  if (!(maxMs > 0)) return 0
  const multiplier = typeof think?.multiplier === 'number' && Number.isFinite(think.multiplier) && think.multiplier > 0 ? think.multiplier : 1
  return maxMs * EXPONENTIAL_THINK_TIME_CAP_FACTOR * multiplier
}

/**
 * Runtime spelling of run_logic.workload_model, mirroring
 * dsl.CanonicalWorkloadModel: trimmed, lowercased, empty = "closed". The
 * engine treats "Open" or " open " as the open arrival-rate model.
 */
export function canonicalWorkloadModel(value: string | undefined | null): string {
  const normalized = (value ?? '').trim().toLowerCase()
  return normalized === '' ? 'closed' : normalized
}

export function isOpenWorkloadModel(config: Pick<ScriptConfig, 'run_logic'>): boolean {
  return canonicalWorkloadModel(config.run_logic?.workload_model) === 'open'
}

/**
 * Runtime spelling of pacing.type, mirroring dsl.CanonicalPacingType:
 * trimmed, lowercased, empty = "none". Stored "TPS" or " tps " is TPS pacing.
 */
export function canonicalPacingType(value: string | undefined | null): string {
  const normalized = (value ?? '').trim().toLowerCase()
  return normalized === '' ? 'none' : normalized
}

/**
 * Mirrors dsl.ValidateWorkloadSettingsIssues for open workloads: pacing must
 * canonicalize to tps, and either pacing.tps is finite and positive or at
 * least one rate stage has a positive target_tps or end_tps.
 */
export function openWorkloadPacingIsValid(pacing: ScriptConfig['pacing'] | undefined): boolean {
  if (canonicalPacingType(pacing?.type) !== 'tps') return false
  const tps = pacing?.tps
  if (typeof tps === 'number' && Number.isFinite(tps) && tps > 0) return true
  const stages = (pacing as { rate_stages?: { target_tps?: number; end_tps?: number | null }[] } | undefined)?.rate_stages
  return Array.isArray(stages) && stages.some(stage => (stage?.target_tps ?? 0) > 0 || (typeof stage?.end_tps === 'number' && stage.end_tps > 0))
}

export const RUNTIME_WORKSPACE_CATEGORIES: {
  canonicalGroupId: CanonicalRuntimeGroupId
  group: string
  id: RuntimeSettingsSectionId
  label: string
  fields: (keyof ScriptConfig)[]
}[] = [
  { canonicalGroupId: 'target_identity', group: 'Target & identity', id: 'script_config', label: 'Script Configuration', fields: ['base_url', 'native_services', 'jmeter', 'sampler_schedules'] },
  { canonicalGroupId: 'run_logic_pacing', group: 'Run logic & pacing', id: 'runlogic', label: 'Run Logic', fields: ['run_logic', 'pacing'] },
  { canonicalGroupId: 'run_logic_pacing', group: 'Run logic & pacing', id: 'pacing', label: 'Iteration Pacing', fields: ['pacing'] },
  { canonicalGroupId: 'think_time', group: 'Think time', id: 'think', label: 'Think Time', fields: ['think_time'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'http', label: 'HTTP Transport', fields: ['http'] },
  { canonicalGroupId: 'protocol_behavior_session', group: 'Protocol behavior & session', id: 'browser_emulation', label: 'Browser Emulation', fields: ['browser_emulation'] },
  { canonicalGroupId: 'errors_logging_diagnostics', group: 'Errors, logging & diagnostics', id: 'errors', label: 'Error Handling', fields: ['error_handling', 'results'] },
  { canonicalGroupId: 'errors_logging_diagnostics', group: 'Errors, logging & diagnostics', id: 'log', label: 'Logging', fields: ['log'] },
  { canonicalGroupId: 'protocol_behavior_session', group: 'Protocol behavior & session', id: 'soap', label: 'SOAP Settings', fields: ['soap'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'java_http', label: 'Java over HTTP', fields: ['java_http'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'java_vuser', label: 'Java VM', fields: ['java_vuser'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'jms', label: 'JMS Runtime', fields: ['jms'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'kafka', label: 'Kafka runtime', fields: ['kafka'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'eventhubs', label: 'Event Hubs Runtime', fields: ['eventhubs'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'websocket', label: 'WebSocket Runtime', fields: ['websocket'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'grpc', label: 'gRPC Runtime', fields: ['grpc'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'database', label: 'Database Runtime', fields: ['database'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'network', label: 'Network Runtime', fields: ['network'] },
  { canonicalGroupId: 'transport_connections', group: 'Transport & connections', id: 'appium', label: 'Appium Device', fields: ['appium'] },
  { canonicalGroupId: 'protocol_behavior_session', group: 'Protocol behavior & session', id: 'web_runtime', label: 'Web HTTP/HTML Compatibility', fields: ['web_runtime'] },
  { canonicalGroupId: 'target_identity', group: 'Target & identity', id: 'imported_source', label: 'Imported Source Runtime Settings', fields: [] },
]

export function runtimeStopConditions(config: ScriptConfig): string[] {
  const logic = config.run_logic
  return [
    logic?.iterations ? isOpenWorkloadModel(config) ? `${logic.iterations} × assigned workers total arrivals` : `${logic.iterations} iterations per virtual user` : '',
    logic?.global_iterations ? `${logic.global_iterations} shared iterations` : '',
    logic?.max_transactions ? `${logic.max_transactions} transactions` : '',
    'Duration set by the scenario or run',
  ].filter(Boolean)
}

export function runtimeTimingIssues(config: ScriptConfig): string[] {
  const issues: string[] = []
  if (isOpenWorkloadModel(config) && !openWorkloadPacingIsValid(config.pacing)) issues.push('Open arrival-rate requires TPS pacing and a target greater than zero.')
  if (
    config.run_logic?.termination_mode === 'iterations' &&
    ((config.run_logic?.iterations ?? 0) <= 0 ||
      canonicalWorkloadModel(config.run_logic?.workload_model) !== 'closed' ||
      (config.run_logic?.global_iterations ?? 0) > 0 ||
      (config.run_logic?.max_transactions ?? 0) > 0)
  ) {
    issues.push('Iteration termination requires positive per-user iterations, closed workload, and no shared execution limits.')
  }
  const pacing = config.pacing
  const pacingType = canonicalPacingType(pacing?.type)
  if (pacingType === 'random_delay' && (pacing?.max_delay_ms ?? 0) < (pacing?.min_delay_ms ?? 0)) issues.push('Maximum pacing delay must be at least the minimum delay.')
  if (pacingType === 'random_interval' && (pacing?.max_interval_ms ?? 0) < (pacing?.min_interval_ms ?? 0)) issues.push('Maximum pacing interval must be at least the minimum interval.')
  const arrivalProcess = (pacing as { arrival_process?: string } | undefined)?.arrival_process?.trim().toLowerCase()
  const burstiness = (pacing as { burstiness?: number } | undefined)?.burstiness
  if (arrivalProcess === 'gamma' && !(typeof burstiness === 'number' && Number.isFinite(burstiness) && burstiness > 0 && burstiness <= 1000)) issues.push('Gamma arrivals need a burstiness above 0 and at most 1000.')
  const think = config.think_time
  const thinkDist = (think as { distribution?: string } | undefined)?.distribution
  const thinkIsExponential = think?.type === 'exponential' || thinkDist === 'exponential'
  // DSL validation (validate.go validateThinkTime) rejects min_ms > max_ms for
  // every type whenever max_ms > 0, exponential included. The engine's 4 ×
  // max_ms exponential cap is explained as help text only.
  if (!think?.ignore && (['uniform', 'gaussian', 'normal'].includes(think?.type || '') || thinkIsExponential) && (think?.max_ms ?? 0) > 0 && (think?.max_ms ?? 0) < (think?.min_ms ?? 0)) issues.push(thinkIsExponential ? 'Cap base must be at least the minimum think time.' : 'Maximum think time must be at least the minimum think time.')
  if (think?.type === 'random_percentage' && ((think.min_percent ?? 0) < 0 || (think.max_percent ?? 0) > 10000 || (think.max_percent ?? 0) < (think.min_percent ?? 0))) issues.push('Think-time percentages must satisfy 0 ≤ minimum ≤ maximum ≤ 10000.')
  return issues
}

export function effectiveThinkTimeLabel(config: ScriptConfig): string {
  const think = config.think_time
  if (!think || think.ignore || think.type === 'none') return 'No think-time delay'
  const thinkDist = (think as { distribution?: string } | undefined)?.distribution
  const effectiveType = think.type === 'exponential' || thinkDist === 'exponential' ? 'exponential' : think.type
  const base = effectiveType === 'random_percentage' ? `${think.min_percent ?? 0}–${think.max_percent ?? 0}% of recorded action delays` : effectiveType === 'recorded' ? 'Recorded action delays' : effectiveType === 'uniform' ? `${(think.min_ms ?? 0) / 1000}–${(think.max_ms ?? 0) / 1000} seconds` : `${effectiveType || 'fixed'} ${((think?.mean_ms ?? 0) / 1000)} seconds`
  return `${base} × ${think?.multiplier || 1}${think?.limit_ms ? `, capped at ${think.limit_ms / 1000} seconds` : ''}`
}

export const RUNTIME_SEARCH_TERMS: Partial<Record<RuntimeSettingsSectionId, string>> = {
  http: 'proxy timeout redirect cookies headers cache connection tls certificate socket keep alive http2',
  think: 'delay distribution mean gaussian uniform recorded multiplier limit ignore',
  pacing: 'rate tps interval random delay',
  runlogic: 'iterations global transactions arrival open closed max in flight rate cap',
  errors: 'continue fail retry backoff handling transaction sla phase sla transaction_rules dns connect tls server download',
  log: 'diagnostic capture verbosity level',
  browser_emulation: 'user agent language device browser cache session identity',
  imported_source: 'block scheduling action sequence order preserved source',
  kafka: 'brokers bootstrap topic sasl oauth tls acks idempotence compression batching linger consumer group offset isolation schema registry serialization avro',
  jms: 'broker destination queue topic properties acknowledgement delivery tls',
  eventhubs: 'namespace hub partition consumer processor checkpoint owner level tail',
  grpc: 'reflection proto metadata authority plaintext',
  database: 'driver sqlite postgres dsn rows connections read only rollback',
  network: 'smtp from to recipients ldap base dn ftp tcp udp dns',
  java_vuser: 'jvm classpath startup action timeout parameter braces delimiters',
}
