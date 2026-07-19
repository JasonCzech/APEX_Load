import {
  API_ORIGIN,
  API_ORIGIN_CHANGED_EVENT,
  apiFetchFromOrigin,
  requestFromOrigin,
  trimTrailingSlash,
  wsUrl,
} from '../apiClient'
import { type BrowserRecorderAvailability } from './useBrowserRecorderApi'

export const BROWSER_RECORDER_BRIDGE_CHANGED_EVENT = 'apexload:browser-recorder-bridge-changed'
const BRIDGE_STORAGE_PREFIX = 'apexload.browserRecorderBridge.'
const BRIDGE_TOKEN_HEADER = 'X-APEXLoad-Bridge-Token'
const BRIDGE_SESSION_HEADER = 'X-APEXLoad-Bridge-Session'

export type BrowserRecorderBridgeConnection = {
  origin: string
  token: string
  session_id: string
  paired_origin?: string
  paired_at: string
  pairing_expires_at?: string
  session_expires_at?: string
}

export type BrowserRecorderBridgeStatus = {
  paired: boolean
  origin?: string
  api_origin: string
  started_at?: string
  pairing_expires_at?: string
  session_id?: string
  session_expires_at?: string
  availability: BrowserRecorderAvailability
  active?: { id: string; name: string; status: string } | null
}

export type BrowserRecorderBridgeStatusEnvelope = {
  bridge: BrowserRecorderBridgeStatus
}

export type BrowserRecorderBridgeHealth = {
  status: string
  version?: string
  environment?: string
  build_time?: string
}

function storage(): Storage | undefined {
  try {
    return typeof window !== 'undefined' ? window.localStorage : undefined
  } catch {
    return undefined
  }
}

function storageKey(remoteOrigin = API_ORIGIN): string {
  return `${BRIDGE_STORAGE_PREFIX}${trimTrailingSlash(remoteOrigin)}`
}

function normalizeBridgeOrigin(origin: string): string {
  return trimTrailingSlash(origin.trim())
}

export function readBrowserRecorderBridge(remoteOrigin = API_ORIGIN): BrowserRecorderBridgeConnection | null {
  const raw = storage()?.getItem(storageKey(remoteOrigin))
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as BrowserRecorderBridgeConnection
    if (!parsed.origin || !parsed.token || !parsed.session_id) return null
    return { ...parsed, origin: normalizeBridgeOrigin(parsed.origin) }
  } catch {
    return null
  }
}

export function writeBrowserRecorderBridge(connection: BrowserRecorderBridgeConnection, remoteOrigin = API_ORIGIN): void {
  storage()?.setItem(storageKey(remoteOrigin), JSON.stringify({
    ...connection,
    origin: normalizeBridgeOrigin(connection.origin),
  }))
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(BROWSER_RECORDER_BRIDGE_CHANGED_EVENT, { detail: connection }))
  }
}

export function clearBrowserRecorderBridge(remoteOrigin = API_ORIGIN): void {
  storage()?.removeItem(storageKey(remoteOrigin))
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent(BROWSER_RECORDER_BRIDGE_CHANGED_EVENT))
  }
}

function withBridgeToken(connection: BrowserRecorderBridgeConnection, options?: RequestInit): RequestInit {
  const headers = new Headers(options?.headers)
  headers.set(BRIDGE_TOKEN_HEADER, connection.token)
  headers.set(BRIDGE_SESSION_HEADER, connection.session_id)
  return { ...options, headers }
}

export function bridgeRequest<T>(connection: BrowserRecorderBridgeConnection, path: string, options?: RequestInit): Promise<T> {
  return requestFromOrigin<T>(connection.origin, path, withBridgeToken(connection, options))
}

export function bridgeFetch(connection: BrowserRecorderBridgeConnection, path: string, options?: RequestInit): Promise<Response> {
  return apiFetchFromOrigin(connection.origin, path, withBridgeToken(connection, options))
}

export async function pairBrowserRecorderBridge(
  origin: string,
  token: string,
  remoteOrigin = API_ORIGIN,
): Promise<BrowserRecorderBridgeConnection & { status: BrowserRecorderBridgeStatus }> {
  const normalized = normalizeBridgeOrigin(origin)
  const res = await requestFromOrigin<BrowserRecorderBridgeStatusEnvelope>(normalized, '/bridge/pair', {
    method: 'POST',
    body: JSON.stringify({ token: token.trim() }),
  })
  if (!res.bridge.session_id) {
    throw new Error('Bridge paired, but did not return a WebSocket session id')
  }
  const connection: BrowserRecorderBridgeConnection = {
    origin: normalized,
    token: token.trim(),
    session_id: res.bridge.session_id,
    paired_origin: res.bridge.origin,
    paired_at: new Date().toISOString(),
    pairing_expires_at: res.bridge.pairing_expires_at,
    session_expires_at: res.bridge.session_expires_at,
  }
  writeBrowserRecorderBridge(connection, remoteOrigin)
  return { ...connection, status: res.bridge }
}

export function verifyBrowserRecorderBridgeAgent(origin: string): Promise<BrowserRecorderBridgeHealth> {
  return requestFromOrigin<BrowserRecorderBridgeHealth>(normalizeBridgeOrigin(origin), '/system/health', {
    method: 'GET',
  })
}

export function bridgeWsUrl(connection: BrowserRecorderBridgeConnection, path: string): string {
  const separator = path.includes('?') ? '&' : '?'
  return wsUrl(`${path}${separator}bridge_session_id=${encodeURIComponent(connection.session_id)}`, connection.origin, false)
}

export function subscribeBrowserRecorderBridgeChanges(callback: () => void): () => void {
  if (typeof window === 'undefined') return () => {}
  const handle = () => callback()
  const handleStorage = (event: StorageEvent) => {
    if (!event.key || event.key.startsWith(BRIDGE_STORAGE_PREFIX)) {
      callback()
    }
  }
  window.addEventListener(BROWSER_RECORDER_BRIDGE_CHANGED_EVENT, handle)
  window.addEventListener(API_ORIGIN_CHANGED_EVENT, handle)
  window.addEventListener('storage', handleStorage)
  return () => {
    window.removeEventListener(BROWSER_RECORDER_BRIDGE_CHANGED_EVENT, handle)
    window.removeEventListener(API_ORIGIN_CHANGED_EVENT, handle)
    window.removeEventListener('storage', handleStorage)
  }
}
