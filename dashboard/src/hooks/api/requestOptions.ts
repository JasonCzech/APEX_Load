import type { ApiRequestOptions } from '../apiClient'

// ApiCallOptions is the per-call transport surface a domain-hook caller may
// supply. apiClient already implements cancellation, deadlines and header
// merging; domain hooks historically swallowed all of it, so every consumer
// hand-rolled unmount cancellation (or simply leaked in-flight requests).
// Domain hooks accept this as a trailing optional argument and hand it to
// apiClient unchanged.
export type ApiCallOptions = {
  signal?: AbortSignal | null
  timeoutMs?: number
  headers?: HeadersInit
  credentials?: RequestCredentials
  cache?: RequestCache
  keepalive?: boolean
}

const CALL_OPTION_KEYS = [
  'signal',
  'timeoutMs',
  'headers',
  'credentials',
  'cache',
  'keepalive',
] as const

// Trailing optional parameters are easy to hit accidentally — `ids.map(getTest)`
// passes the array index as the second argument. Only object literals are
// treated as call options so such calls stay no-ops instead of corrupting the
// request init.
function isCallOptions(value: unknown): value is ApiCallOptions {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function mergeHeaderInits(base?: HeadersInit, extra?: HeadersInit): Headers | undefined {
  if (base === undefined && extra === undefined) return undefined
  const merged = new Headers(base)
  new Headers(extra).forEach((value, key) => {
    merged.set(key, value)
  })
  return merged
}

// withCallOptions layers caller-supplied transport options onto the init a
// domain hook already built. Caller headers merge with (and win over) the
// hook's own headers; every other field is copied verbatim. Returns `init`
// untouched when there is nothing to apply so existing call sites keep their
// exact previous behaviour.
export function withCallOptions(
  init?: ApiRequestOptions,
  options?: ApiCallOptions,
): ApiRequestOptions | undefined {
  if (!isCallOptions(options)) return init
  const merged: ApiRequestOptions = { ...(init ?? {}) }
  let applied = false
  for (const key of CALL_OPTION_KEYS) {
    const value = options[key]
    if (value === undefined) continue
    applied = true
    if (key === 'headers') continue
    // Each key is a distinct optional field of ApiRequestOptions; the loop is
    // the only place the union has to be reassembled.
    ;(merged as Record<string, unknown>)[key] = value
  }
  if (options.headers !== undefined) {
    merged.headers = mergeHeaderInits(init?.headers, options.headers)
  }
  if (!applied) return init
  return merged
}
