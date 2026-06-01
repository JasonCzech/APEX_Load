const DEFAULT_LOCAL_API_ORIGIN = 'http://127.0.0.1:8080'
export const DEV_VIEW_STORAGE_KEY = 'apexload.devViewMode'
export const DEV_VIEW_MODE_CHANGED_EVENT = 'apexload:dev-view-mode-changed'
const ACTIVE_PROJECT_STORAGE_KEY = 'apexload.activeProjectId'
const DEV_VIEW_HEADER = 'X-APEXLoad-Dev-View'
const DEV_PROJECT_HEADER = 'X-APEXLoad-Dev-Project-ID'

export type DevViewMode = 'admin' | 'user'

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

function getLocalStorage(): Storage | undefined {
  try {
    return typeof window !== 'undefined' ? window.localStorage : undefined
  } catch {
    return undefined
  }
}

function isApiErrorPayload(value: unknown): value is ApiErrorPayload {
  return typeof value === 'object' && value !== null
}

export function resolveApiOrigin(env: ApiEnvironment = getApiEnvironment(), location: ApiLocation | undefined = getWindowLocation()): string {
  const envOrigin = env.VITE_API_ORIGIN
  if (envOrigin && envOrigin.trim()) {
    return trimTrailingSlash(envOrigin.trim())
  }
  // Detect Wails desktop environment
  if (location?.protocol === 'wails:' || location?.hostname === 'wails.localhost') {
    return DEFAULT_LOCAL_API_ORIGIN
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

export function normalizeDevViewMode(value: unknown): DevViewMode {
  return value === 'user' ? 'user' : 'admin'
}

export function readStoredDevViewMode(): DevViewMode {
  return normalizeDevViewMode(getLocalStorage()?.getItem(DEV_VIEW_STORAGE_KEY))
}

export function writeStoredDevViewMode(mode: DevViewMode): void {
  const normalized = normalizeDevViewMode(mode)
  getLocalStorage()?.setItem(DEV_VIEW_STORAGE_KEY, normalized)
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(DEV_VIEW_MODE_CHANGED_EVENT, { detail: normalized }))
  }
}

export function buildDevPreviewHeaders(): Record<string, string> {
  const headers: Record<string, string> = {
    [DEV_VIEW_HEADER]: readStoredDevViewMode(),
  }
  const projectId = getLocalStorage()?.getItem(ACTIVE_PROJECT_STORAGE_KEY)?.trim()
  if (projectId) {
    headers[DEV_PROJECT_HEADER] = projectId
  }
  return headers
}

function mergeHeaders(headers?: HeadersInit, previewHeaders = buildDevPreviewHeaders()): Headers {
  const merged = new Headers(headers)
  Object.entries(previewHeaders).forEach(([key, value]) => {
    if (value) {
      merged.set(key, value)
    }
  })
  return merged
}

function apiUrl(pathOrURL: string): string {
  if (/^https?:\/\//i.test(pathOrURL)) {
    return pathOrURL
  }
  const path = pathOrURL.startsWith('/') ? pathOrURL : `/${pathOrURL}`
  return `${BASE}${path}`
}

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

export function apiFetch(pathOrURL: string, options?: RequestInit): Promise<Response> {
  return fetch(apiUrl(pathOrURL), {
    ...options,
    headers: mergeHeaders(options?.headers),
  })
}

export async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const headers = mergeHeaders(options?.headers)
  if (!headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const res = await apiFetch(path, {
    ...options,
    headers,
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
