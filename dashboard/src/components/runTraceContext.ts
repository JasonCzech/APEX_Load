export interface RunTraceContextSettings {
  enabled: boolean
  transaction_marker?: boolean
  origins?: string[]
}

export const MAX_TRACE_CONTEXT_ORIGINS = 32

// Mirrors the controller's origin rules: an http(s) origin with no
// credentials, path, query or fragment. The server validates again.
export function traceContextOriginError(origin: string): string | null {
  let parsed: URL
  try {
    parsed = new URL(origin)
  } catch {
    return `${origin} is not a URL`
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return `${origin} must use http or https`
  if (parsed.username || parsed.password) return `${origin} must not contain credentials`
  if ((parsed.pathname && parsed.pathname !== '/') || parsed.search || parsed.hash) return `${origin} must be an origin only, without a path, query or fragment`
  return null
}

export function parseTraceContextOrigins(text: string): string[] {
  const origins: string[] = []
  for (const line of text.split(/[\n,]/)) {
    const origin = line.trim().replace(/\/+$/, '')
    if (origin && !origins.includes(origin)) origins.push(origin)
  }
  return origins
}

export function traceContextErrors(value: RunTraceContextSettings | undefined): string[] {
  if (!value || (!value.enabled && !value.transaction_marker)) return []
  const origins = value.origins ?? []
  if (origins.length === 0) return ['Add at least one approved origin.']
  const errors = origins.map(traceContextOriginError).filter((error): error is string => Boolean(error))
  if (origins.length > MAX_TRACE_CONTEXT_ORIGINS) errors.push(`At most ${MAX_TRACE_CONTEXT_ORIGINS} approved origins are allowed.`)
  return errors
}

export function traceContextPayload(value: RunTraceContextSettings | undefined): RunTraceContextSettings | undefined {
  if (!value || (!value.enabled && !value.transaction_marker)) return undefined
  return { enabled: value.enabled, transaction_marker: Boolean(value.transaction_marker), origins: [...(value.origins ?? [])] }
}

export function traceContextSummary(value: RunTraceContextSettings | undefined): string {
  const payload = traceContextPayload(value)
  if (!payload) return 'Off for this test'
  const carriers = [payload.enabled ? 'traceparent' : '', payload.transaction_marker ? 'transaction markers' : ''].filter(Boolean).join(' and ')
  const origins = payload.origins ?? []
  return `${carriers} to ${origins.length} approved origin${origins.length === 1 ? '' : 's'}`
}
