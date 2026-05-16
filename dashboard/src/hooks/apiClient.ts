const DEFAULT_LOCAL_API_ORIGIN = 'http://127.0.0.1:8080'

type ApiEnvironment = {
  VITE_API_ORIGIN?: string
  DEV?: boolean
}

type ApiErrorPayload = {
  error?: string
  code?: string
  [key: string]: unknown
}

function trimTrailingSlash(value: string): string {
  return value.replace(/\/+$/, '')
}

function getApiEnvironment(): ApiEnvironment {
  return (import.meta as ImportMeta & { env?: ApiEnvironment }).env ?? {}
}

function isApiErrorPayload(value: unknown): value is ApiErrorPayload {
  return typeof value === 'object' && value !== null
}

function resolveApiOrigin(): string {
  const env = getApiEnvironment()
  const envOrigin = env.VITE_API_ORIGIN
  if (envOrigin && envOrigin.trim()) {
    return trimTrailingSlash(envOrigin.trim())
  }
  if (typeof window !== 'undefined' && window.location.protocol === 'file:') {
    return DEFAULT_LOCAL_API_ORIGIN
  }
  if (
    typeof window !== 'undefined' &&
    (env.DEV || window.location.port === '3000') &&
    (window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1')
  ) {
    return DEFAULT_LOCAL_API_ORIGIN
  }
  if (typeof window !== 'undefined' && window.location.origin) {
    return trimTrailingSlash(window.location.origin)
  }
  return DEFAULT_LOCAL_API_ORIGIN
}

export const API_ORIGIN = resolveApiOrigin()
export const BASE = `${API_ORIGIN}/api/v1`

export function wsUrl(path: string): string {
  const wsOrigin = API_ORIGIN.replace(/^http:/, 'ws:').replace(/^https:/, 'wss:')
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  return `${wsOrigin}${normalizedPath}`
}

async function parseJsonResponse(res: Response): Promise<unknown> {
  const contentType = (res.headers.get('content-type') || '').toLowerCase()
  if (contentType.includes('application/json')) {
    return res.json()
  }
  const text = await res.text()
  const snippet = text.trim().slice(0, 180)
  throw new ApiError(
    `Expected JSON response from API but received ${contentType || 'non-JSON content'}${snippet ? `: ${snippet}` : ''}`,
    res.status,
    { content_type: contentType, body: snippet },
  )
}

export class ApiError extends Error {
  status: number
  code?: string
  payload?: unknown

  constructor(message: string, status: number, payload?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = isApiErrorPayload(payload) && typeof payload.code === 'string' ? payload.code : undefined
    this.payload = payload
  }
}

export async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...options?.headers },
    ...options,
  })
  if (!res.ok) {
    const body = await parseJsonResponse(res).catch((): ApiErrorPayload => ({ error: res.statusText }))
    const message = isApiErrorPayload(body) && typeof body.error === 'string' && body.error
      ? body.error
      : res.statusText
    throw new ApiError(message, res.status, body)
  }
  return await parseJsonResponse(res) as T
}
