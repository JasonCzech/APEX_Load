import type { ScriptConfig } from '../types/scripts'
import type { RuntimeSettingsSectionId } from './scriptEditorRuntimeSettingsModel'
import type { CanonicalRuntimeGroupId } from './runtime-settings/canonicalRuntimeTaxonomy'

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
    logic?.iterations ? config.run_logic?.workload_model === 'open' ? `${logic.iterations} × assigned workers total arrivals` : `${logic.iterations} iterations per virtual user` : '',
    logic?.global_iterations ? `${logic.global_iterations} shared iterations` : '',
    logic?.max_transactions ? `${logic.max_transactions} transactions` : '',
    'Duration set by the scenario or run',
  ].filter(Boolean)
}

export function runtimeTimingIssues(config: ScriptConfig): string[] {
  const issues: string[] = []
  if (config.run_logic?.workload_model === 'open' && (config.pacing?.type !== 'tps' || !Number.isFinite(config.pacing.tps) || (config.pacing.tps ?? 0) <= 0)) issues.push('Open arrival-rate requires TPS pacing and a target greater than zero.')
  if (
    config.run_logic?.termination_mode === 'iterations' &&
    ((config.run_logic?.iterations ?? 0) <= 0 ||
      (config.run_logic?.workload_model !== undefined &&
        config.run_logic.workload_model !== '' &&
        config.run_logic.workload_model !== 'closed') ||
      (config.run_logic?.global_iterations ?? 0) > 0 ||
      (config.run_logic?.max_transactions ?? 0) > 0)
  ) {
    issues.push('Iteration termination requires positive per-user iterations, closed workload, and no shared execution limits.')
  }
  const pacing = config.pacing
  if (pacing?.type === 'random_delay' && (pacing.max_delay_ms ?? 0) < (pacing.min_delay_ms ?? 0)) issues.push('Maximum pacing delay must be at least the minimum delay.')
  if (pacing?.type === 'random_interval' && (pacing.max_interval_ms ?? 0) < (pacing.min_interval_ms ?? 0)) issues.push('Maximum pacing interval must be at least the minimum interval.')
  const think = config.think_time
  const thinkDist = (think as { distribution?: string } | undefined)?.distribution
  if (!think?.ignore && (['uniform', 'gaussian', 'normal', 'exponential'].includes(think?.type || '') || thinkDist === 'exponential') && (think?.max_ms ?? 0) > 0 && (think?.max_ms ?? 0) < (think?.min_ms ?? 0)) issues.push('Maximum think time must be at least the minimum think time.')
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
}
