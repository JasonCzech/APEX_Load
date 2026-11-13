// Fixed option values the scenario and group override editor offers. Each list
// is what pkg/dsl/validate.go (and catalog_soap_validation.go) accepts for the
// path; scenarioOverrideOptions.parity.test.ts reads pkg/dsl/config_catalog.go
// so a list cannot drift from the backend contract.

export interface OverrideOption {
  value: string
  label: string
}

export const THINK_TIME_TYPE_OPTIONS: readonly OverrideOption[] = [
  { value: 'none', label: 'None' },
  { value: 'fixed', label: 'Fixed' },
  { value: 'recorded', label: 'Replay think time as recorded' },
  { value: 'random_percentage', label: 'Random percentage of recorded think time' },
  { value: 'gaussian', label: 'Gaussian' },
  { value: 'uniform', label: 'Uniform' },
  { value: 'exponential', label: 'Exponential' },
]

export const THINK_TIME_DISTRIBUTION_OPTIONS: readonly OverrideOption[] = [
  { value: 'uniform', label: 'Uniform' },
  { value: 'exponential', label: 'Exponential' },
]

export const PACING_TYPE_OPTIONS: readonly OverrideOption[] = [
  { value: 'none', label: 'None' },
  { value: 'fixed_delay', label: 'Fixed delay' },
  { value: 'random_delay', label: 'Random delay' },
  { value: 'fixed_interval', label: 'Fixed interval' },
  { value: 'random_interval', label: 'Random interval' },
  { value: 'tps', label: 'Target TPS' },
  { value: 'quota', label: 'Quota only (no iteration pacing)' },
]

export const ARRIVAL_PROCESS_OPTIONS: readonly OverrideOption[] = [
  { value: 'uniform', label: 'Uniform' },
  { value: 'poisson', label: 'Poisson' },
  { value: 'gamma', label: 'Gamma' },
]

export const TERMINATION_MODE_OPTIONS: readonly OverrideOption[] = [
  { value: 'duration', label: 'Duration' },
  { value: 'iterations', label: 'Iterations' },
]

export const RETRY_BACKOFF_OPTIONS: readonly OverrideOption[] = [
  { value: 'fixed', label: 'Fixed' },
  { value: 'exponential', label: 'Exponential' },
  { value: 'retry_after', label: 'Server Retry-After' },
]

/** pkg/dsl validateErrorHandlingResourceIssues: retry_on entries. */
export const RETRY_ON_OPTIONS: readonly OverrideOption[] = [
  { value: '429', label: '429 Too Many Requests' },
  { value: '5xx', label: '5xx server errors' },
  { value: 'timeout', label: 'Timeouts' },
  { value: 'network', label: 'Network errors' },
]

export const TLS_VERSION_OPTIONS: readonly OverrideOption[] = [
  { value: '1.0', label: '1.0' },
  { value: '1.1', label: '1.1' },
  { value: '1.2', label: '1.2' },
  { value: '1.3', label: '1.3' },
]

export const SOAP_VERSION_OPTIONS: readonly OverrideOption[] = [
  { value: '1.1', label: 'SOAP 1.1' },
  { value: '1.2', label: 'SOAP 1.2' },
]

export const SOAP_PASSWORD_TYPE_OPTIONS: readonly OverrideOption[] = [
  { value: 'text', label: 'Text' },
  { value: 'digest', label: 'Digest' },
]

/** pkg/dsl validate: config.log.max_response_bytes must be between 0 and 1048576. */
export const LOG_MAX_RESPONSE_BYTES = 1048576
/** pkg/dsl validate: pacing.burstiness above 0 and at most 1000 for gamma arrivals. */
export const MAX_ARRIVAL_BURSTINESS = 1000

/** The select options by DSL config path, for the catalog parity test. */
export const SCENARIO_OVERRIDE_SELECT_OPTIONS: Readonly<Record<string, readonly OverrideOption[]>> = {
  'config.think_time.type': THINK_TIME_TYPE_OPTIONS,
  'config.think_time.distribution': THINK_TIME_DISTRIBUTION_OPTIONS,
  'config.pacing.type': PACING_TYPE_OPTIONS,
  'config.pacing.arrival_process': ARRIVAL_PROCESS_OPTIONS,
  'config.run_logic.termination_mode': TERMINATION_MODE_OPTIONS,
  'config.error_handling.retry_backoff': RETRY_BACKOFF_OPTIONS,
  'config.error_handling.retry_on': RETRY_ON_OPTIONS,
  'config.http.tls.min_version': TLS_VERSION_OPTIONS,
  'config.http.tls.max_version': TLS_VERSION_OPTIONS,
  'config.soap.version': SOAP_VERSION_OPTIONS,
  'config.soap.ws_security.username_token.password_type': SOAP_PASSWORD_TYPE_OPTIONS,
}
