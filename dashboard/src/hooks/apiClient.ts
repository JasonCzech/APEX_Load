import { isWailsLocation } from '../desktop/desktopBridge'
import { readStoredString, writeStoredString } from '../utils/browserStorage'

const DEFAULT_LOCAL_API_ORIGIN = 'http://127.0.0.1:8080'
export const DEV_VIEW_STORAGE_KEY = 'apexload.devViewMode'
export const DEV_VIEW_MODE_CHANGED_EVENT = 'apexload:dev-view-mode-changed'
export const API_ORIGIN_CHANGED_EVENT = 'apexload:api-origin-changed'
const ACTIVE_PROJECT_STORAGE_KEY = 'apexload.activeProjectId'
const API_ORIGIN_STORAGE_KEY = 'apexload.apiOrigin'
const DEV_VIEW_HEADER = 'X-APEXLoad-Dev-View'
const DEV_PROJECT_HEADER = 'X-APEXLoad-Dev-Project-ID'

export type DevViewMode = 'admin' | 'user'

type ApiEnvironment = {
  VITE_API_ORIGIN?: string
  DEV?: boolean
}

type ApiLocation = Pick<Location, 'protocol' | 'hostname' | 'port' | 'origin'>
type WailsAppBridge = {
  GetAPIOrigin?: () => Promise<string>
}

type ApiErrorPayload = {
  error?: string
  code?: string
  [key: string]: unknown
}

export function trimTrailingSlash(value: string): string {
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

function getStoredApiOrigin(): string {
  return readStoredString(API_ORIGIN_STORAGE_KEY).trim()
}

function getWailsAppBridge(): WailsAppBridge | undefined {
  if (typeof window === 'undefined') return undefined
  const candidate = (window as unknown as { go?: { main?: { App?: WailsAppBridge } } }).go?.main?.App
  return candidate
}

export function resolveApiOrigin(env: ApiEnvironment = getApiEnvironment(), location: ApiLocation | undefined = getWindowLocation()): string {
  const envOrigin = env.VITE_API_ORIGIN
  if (envOrigin && envOrigin.trim()) {
    return trimTrailingSlash(envOrigin.trim())
  }
  // Detect Wails desktop environment
  if (isWailsLocation(location)) {
    return trimTrailingSlash(getStoredApiOrigin() || DEFAULT_LOCAL_API_ORIGIN)
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

export let API_ORIGIN = resolveApiOrigin()
export let BASE = `${API_ORIGIN}/api/v1`

export function setApiOrigin(origin: string): void {
  const normalized = trimTrailingSlash(origin.trim())
  if (!normalized) return
  API_ORIGIN = normalized
  BASE = `${API_ORIGIN}/api/v1`
  writeStoredString(API_ORIGIN_STORAGE_KEY, normalized)
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(API_ORIGIN_CHANGED_EVENT, { detail: normalized }))
  }
}

export async function initializeDesktopApiOrigin(): Promise<void> {
  const location = getWindowLocation()
  if (!isWailsLocation(location)) return
  const bridge = getWailsAppBridge()
  if (!bridge?.GetAPIOrigin) return
  try {
    const origin = await bridge.GetAPIOrigin()
    if (origin?.trim()) {
      setApiOrigin(origin)
    }
  } catch {
    // Keep the default local API origin when the Wails bridge is unavailable.
  }
}

export function normalizeDevViewMode(value: unknown): DevViewMode {
  return value === 'user' ? 'user' : 'admin'
}

export function readStoredDevViewMode(): DevViewMode {
  return normalizeDevViewMode(readStoredString(DEV_VIEW_STORAGE_KEY))
}

export function writeStoredDevViewMode(mode: DevViewMode): void {
  const normalized = normalizeDevViewMode(mode)
  writeStoredString(DEV_VIEW_STORAGE_KEY, normalized)
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(DEV_VIEW_MODE_CHANGED_EVENT, { detail: normalized }))
  }
}

export function buildDevPreviewHeaders(): Record<string, string> {
  const headers: Record<string, string> = {
    [DEV_VIEW_HEADER]: readStoredDevViewMode(),
  }
  const projectId = readStoredString(ACTIVE_PROJECT_STORAGE_KEY).trim()
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

function buildApiUrl(apiOrigin: string, pathOrURL: string): string {
  if (/^https?:\/\//i.test(pathOrURL)) {
    return pathOrURL
  }
  const path = pathOrURL.startsWith('/') ? pathOrURL : `/${pathOrURL}`
  return `${trimTrailingSlash(apiOrigin)}/api/v1${path}`
}

function apiUrl(pathOrURL: string): string {
  return buildApiUrl(API_ORIGIN, pathOrURL)
}

export function resolveApiResourceUrl(pathOrURL: string, apiOrigin = API_ORIGIN): string {
  if (/^https?:\/\//i.test(pathOrURL)) {
    return pathOrURL
  }
  const path = pathOrURL.startsWith('/') ? pathOrURL : `/${pathOrURL}`
  return `${trimTrailingSlash(apiOrigin)}${path}`
}

export function wsUrl(path: string, apiOrigin = API_ORIGIN): string {
  const wsOrigin = trimTrailingSlash(apiOrigin).replace(/^http:/, 'ws:').replace(/^https:/, 'wss:')
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  return `${wsOrigin}${normalizedPath}`
}

async function parseJsonResponse(res: Response): Promise<unknown> {
  const contentType = (res.headers.get('content-type') || '').toLowerCase()
  if (res.status === 204 || res.status === 205) {
    return undefined
  }
  const text = await res.text()
  if (!text.trim()) {
    return undefined
  }
  if (contentType.includes('application/json')) {
    return JSON.parse(text) as unknown
  }
  const snippet = text.trim().slice(0, 180)
  const isHTML = contentType.includes('text/html') || snippet.toLowerCase().startsWith('<!doctype html') || snippet.toLowerCase().startsWith('<html')
  const message = isHTML
    ? 'API endpoint returned the dashboard HTML instead of JSON. The backend route may be unavailable or the local dev proxy may be pointed at an older controller.'
    : `Expected JSON response from API but received ${contentType || 'non-JSON content'}`
  throw new ApiError(
    message,
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
    credentials: options?.credentials ?? 'include',
    headers: mergeHeaders(options?.headers),
  })
}

export function apiFetchFromOrigin(apiOrigin: string, pathOrURL: string, options?: RequestInit): Promise<Response> {
  return fetch(buildApiUrl(apiOrigin, pathOrURL), {
    ...options,
    credentials: options?.credentials ?? 'include',
    headers: mergeHeaders(options?.headers),
  })
}

export async function request<T>(path: string, options?: RequestInit): Promise<T> {
  return requestFromOrigin<T>(API_ORIGIN, path, options)
}

export async function requestFromOrigin<T>(apiOrigin: string, path: string, options?: RequestInit): Promise<T> {
  const headers = mergeHeaders(options?.headers)
  if (!headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const res = await apiFetchFromOrigin(apiOrigin, path, {
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
