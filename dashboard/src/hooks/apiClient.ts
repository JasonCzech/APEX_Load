const DEFAULT_LOCAL_API_ORIGIN = 'http://127.0.0.1:8080'

type ApiEnvironment = {
  VITE_API_ORIGIN?: string
  DEV?: boolean
}

type ApiLocation = Pick<Location, 'protocol' | 'hostname' | 'port' | 'origin'>

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

function getWindowLocation(): ApiLocation | undefined {
  return typeof window !== 'undefined' ? window.location : undefined
}

function isApiErrorPayload(value: unknown): value is ApiErrorPayload {
  return typeof value === 'object' && value !== null
}

export function resolveApiOrigin(env: ApiEnvironment = getApiEnvironment(), location: ApiLocation | undefined = getWindowLocation()): string {
  const envOrigin = env.VITE_API_ORIGIN
  if (envOrigin && envOrigin.trim()) {
    return trimTrailingSlash(envOrigin.trim())
  }
  if (location?.protocol === 'file:') {
    return DEFAULT_LOCAL_API_ORIGIN
  }
  const isLocalHost = location?.hostname === 'localhost' || location?.hostname === '127.0.0.1'
  const hasUsableOrigin = !!location?.origin && location.origin !== 'null'
  if (
    isLocalHost &&
    hasUsableOrigin &&
    (env.DEV || location.port === '3000')
  ) {
    return trimTrailingSlash(location.origin)
  }
  if (hasUsableOrigin) {
    return trimTrailingSlash(location.origin)
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
