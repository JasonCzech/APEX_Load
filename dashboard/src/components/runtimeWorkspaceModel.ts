import type { ScriptConfig } from '../types/scripts'
import type { RuntimeSettingsSectionId } from './scriptEditorRuntimeSettingsModel'

export const RUNTIME_WORKSPACE_CATEGORIES: { group: string; id: RuntimeSettingsSectionId; label: string; fields: (keyof ScriptConfig)[] }[] = [
  { group: 'Execution & timing', id: 'script_config', label: 'Script Configuration', fields: ['base_url'] },
  { group: 'Execution & timing', id: 'runlogic', label: 'Run Logic', fields: ['run_logic', 'pacing'] },
  { group: 'Execution & timing', id: 'pacing', label: 'Iteration Pacing', fields: ['pacing'] },
  { group: 'Execution & timing', id: 'think', label: 'Think Time', fields: ['think_time'] },
  { group: 'Requests & connections', id: 'http', label: 'HTTP Transport', fields: ['http'] },
  { group: 'Session & browser identity', id: 'browser_emulation', label: 'Browser Emulation', fields: ['browser_emulation'] },
  { group: 'Errors & diagnostics', id: 'errors', label: 'Error Handling', fields: ['error_handling', 'results'] },
  { group: 'Errors & diagnostics', id: 'log', label: 'Logging', fields: ['log'] },
  { group: 'Protocol settings', id: 'soap', label: 'SOAP Settings', fields: ['soap'] },
  { group: 'Protocol settings', id: 'java_http', label: 'Java over HTTP', fields: ['java_http'] },
  { group: 'Protocol settings', id: 'java_vuser', label: 'Java VM', fields: ['java_vuser'] },
  { group: 'Protocol settings', id: 'jms', label: 'JMS Runtime', fields: ['jms'] },
  { group: 'Protocol settings', id: 'eventhubs', label: 'Event Hubs Runtime', fields: ['eventhubs'] },
  { group: 'Protocol settings', id: 'websocket', label: 'WebSocket Runtime', fields: ['websocket'] },
  { group: 'Protocol settings', id: 'grpc', label: 'gRPC Runtime', fields: ['grpc'] },
  { group: 'Protocol settings', id: 'database', label: 'Database Runtime', fields: ['database'] },
  { group: 'Protocol settings', id: 'network', label: 'Network Runtime', fields: ['network'] },
  { group: 'Imported compatibility', id: 'web_runtime', label: 'Web HTTP/HTML Compatibility', fields: ['web_runtime'] },
  { group: 'Imported compatibility', id: 'imported_source', label: 'Imported Source Runtime Settings', fields: [] },
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
  const pacing = config.pacing
  if (pacing?.type === 'random_delay' && (pacing.max_delay_ms ?? 0) < (pacing.min_delay_ms ?? 0)) issues.push('Maximum pacing delay must be at least the minimum delay.')
  if (pacing?.type === 'random_interval' && (pacing.max_interval_ms ?? 0) < (pacing.min_interval_ms ?? 0)) issues.push('Maximum pacing interval must be at least the minimum interval.')
  const think = config.think_time
  if (!think?.ignore && ['uniform', 'gaussian', 'normal'].includes(think?.type || '') && (think?.max_ms ?? 0) > 0 && (think?.max_ms ?? 0) < (think?.min_ms ?? 0)) issues.push('Maximum think time must be at least the minimum think time.')
  if (think?.type === 'random_percentage' && ((think.min_percent ?? 0) < 0 || (think.max_percent ?? 0) > 10000 || (think.max_percent ?? 0) < (think.min_percent ?? 0))) issues.push('Think-time percentages must satisfy 0 ≤ minimum ≤ maximum ≤ 10000.')
  return issues
}

export function effectiveThinkTimeLabel(config: ScriptConfig): string {
  const think = config.think_time
  if (!think || think.ignore || think.type === 'none') return 'No think-time delay'
  const base = think?.type === 'random_percentage' ? `${think.min_percent ?? 0}–${think.max_percent ?? 0}% of recorded action delays` : think?.type === 'recorded' ? 'Recorded action delays' : think?.type === 'uniform' ? `${(think.min_ms ?? 0) / 1000}–${(think.max_ms ?? 0) / 1000} seconds` : `${think?.type || 'fixed'} ${((think?.mean_ms ?? 0) / 1000)} seconds`
  return `${base} × ${think?.multiplier || 1}${think?.limit_ms ? `, capped at ${think.limit_ms / 1000} seconds` : ''}`
}

export const RUNTIME_SEARCH_TERMS: Partial<Record<RuntimeSettingsSectionId, string>> = {
  http: 'proxy timeout redirect cookies headers cache connection tls certificate socket keep alive http2',
  think: 'delay distribution mean gaussian uniform recorded multiplier limit ignore',
  pacing: 'rate tps interval random delay',
  runlogic: 'iterations global transactions arrival open closed max in flight rate cap',
  errors: 'continue fail retry backoff handling',
  log: 'diagnostic capture verbosity level',
  browser_emulation: 'user agent language device browser cache session identity',
  imported_source: 'block scheduling action sequence order preserved source',
}
