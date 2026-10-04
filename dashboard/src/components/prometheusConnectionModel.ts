import type { PrometheusConnection, PrometheusConnectionInput, PrometheusParameter, PrometheusQuery, PrometheusSelection } from '../types/prometheus'

export const prometheusSecretFields = ['token_secret', 'username_secret', 'password_secret', 'client_secret_secret', 'client_certificate_secret', 'client_key_secret'] as const
export const emptyPrometheusQuery = (index = 1): PrometheusQuery => ({ id: `query_${index}`, name: '', expression: '', unit: '', semantics: 'gauge', allowed_labels: ['job', 'instance'], min_series: 1 })
export const emptyPrometheusSelection = (): PrometheusSelection => ({ queries: [], step_seconds: 15, settle_seconds: 120, targets: false, alerts: false })
export function prometheusDraft(saved?: PrometheusConnection | null): PrometheusConnectionInput {
  return saved ? { name: saved.name, description: saved.description || '', enabled: saved.enabled, collection_mode: saved.collection_mode, collector_agent_id: saved.collector_agent_id || '', config: { ...saved.config }, revision: saved.revision } : { name: '', description: '', enabled: false, collection_mode: 'direct', config: { endpoint: '', backend_profile: 'prometheus', auth_type: 'none' } }
}
export function prometheusSameIdentity(draft: PrometheusConnectionInput, saved?: PrometheusConnection | null): boolean {
  return Boolean(saved && draft.config.endpoint === saved.config.endpoint && (draft.config.context_path || '') === (saved.config.context_path || '') && Boolean(draft.config.mtls) === Boolean(saved.config.mtls) && draft.config.auth_type === saved.config.auth_type && draft.config.backend_profile === saved.config.backend_profile && draft.config.client_id === saved.config.client_id && draft.config.azure_tenant_id === saved.config.azure_tenant_id && draft.config.azure_credential_mode === saved.config.azure_credential_mode && draft.collection_mode === saved.collection_mode && (draft.collector_agent_id || '') === (saved.collector_agent_id || ''))
}
export function prometheusMissing(draft: PrometheusConnectionInput, saved?: PrometheusConnection | null): string[] {
  const missing: string[] = []
  const same = prometheusSameIdentity(draft, saved)
  if (!draft.config.endpoint.trim()) missing.push('Prometheus endpoint')
  if (draft.collection_mode === 'collector' && !draft.collector_agent_id?.trim()) missing.push('Assigned collector')
  if (draft.config.auth_type === 'bearer' && !draft.token_secret?.trim() && !(same && saved?.has_token)) missing.push('Bearer token secret reference')
  if (draft.config.auth_type === 'basic') {
    if (!draft.username_secret?.trim() && !(same && saved?.has_username)) missing.push('Username secret reference')
    if (!draft.password_secret?.trim() && !(same && saved?.has_password)) missing.push('Password secret reference')
  }
  if (draft.config.auth_type === 'azure' && !draft.config.azure_credential_mode) missing.push('Azure credential mode')
  if (draft.config.auth_type === 'azure' && ['service_principal', 'workload_identity'].includes(draft.config.azure_credential_mode || '')) {
    if (!draft.config.azure_tenant_id?.trim()) missing.push('Azure tenant ID')
    if (!draft.config.client_id?.trim()) missing.push('Azure client ID')
    if (draft.config.azure_credential_mode === 'service_principal' && !draft.client_secret_secret?.trim() && !(same && saved?.has_client_secret)) missing.push('Azure client secret reference')
  }
  if (draft.config.mtls) {
    if (!draft.client_certificate_secret?.trim() && !(same && saved?.has_client_certificate)) missing.push('Client certificate secret reference')
    if (!draft.client_key_secret?.trim() && !(same && saved?.has_client_key)) missing.push('Client key secret reference')
  }
  return missing
}
export function prometheusCanEnable(draft: PrometheusConnectionInput, saved?: PrometheusConnection | null): boolean {
  return Boolean(saved && saved.verified_revision === saved.revision && saved.last_check_status === 'connected' && prometheusSameIdentity(draft, saved) && JSON.stringify(draft.config) === JSON.stringify(saved.config) && !prometheusSecretFields.some(key => draft[key]?.trim()))
}
export function prometheusQueryValid(query: PrometheusQuery): boolean {
  const identity = /^[A-Za-z][A-Za-z0-9_.-]{0,127}$/
  const label = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/
  const parameters = Object.entries(query.parameters || {})
  const expression = [query.expression, query.freshness_expression || '', query.terminal_expression || ''].join(' ')
  const tokens = Array.from(expression.matchAll(/\{\{param\.([A-Za-z][A-Za-z0-9_]*)\}\}/g), match => match[1])
  return Boolean(identity.test(query.id) && identity.test(query.name) && query.expression.trim() && query.unit.trim() && query.unit.length <= 64 && Number.isInteger(query.min_series) && query.min_series >= 1 && query.min_series <= 200 && query.allowed_labels.length <= 12 && query.allowed_labels.every(value => label.test(value)) && new Set(query.allowed_labels).size === query.allowed_labels.length && parameters.length <= 16 && parameters.every(([name, parameter]) => /^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(name) && prometheusParameterValid(parameter) && tokens.includes(name)) && tokens.every(name => Object.prototype.hasOwnProperty.call(query.parameters || {}, name)))
}

export function prometheusParameterValid(parameter: PrometheusParameter): boolean {
  if (new TextEncoder().encode(parameter.value).length > 256 || /[\r\n\0]/.test(parameter.value)) return false
  if (parameter.type === 'label_value') return true
  if (parameter.type === 'timestamp') {
    if (!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(parameter.value)) return false
    const time = Date.parse(parameter.value)
    return Number.isFinite(time) && time >= 0 && new Date(time).toISOString().slice(0, 19) === parameter.value.slice(0, 19)
  }
  if (parameter.type !== 'duration') return false
  const units = { y: 31536000000, w: 604800000, d: 86400000, h: 3600000, m: 60000, s: 1000, ms: 1 }
  const names = Object.keys(units)
  let matched = '', milliseconds = 0, previous = -1
  for (const match of parameter.value.matchAll(/(\d+)(ms|y|w|d|h|m|s)/g)) {
    const unit = match[2] as keyof typeof units, order = names.indexOf(unit)
    if (order <= previous) return false
    previous = order; matched += match[0]; milliseconds += Number(match[1]) * units[unit]
  }
  return matched === parameter.value && milliseconds > 0 && milliseconds <= 86400000
}
