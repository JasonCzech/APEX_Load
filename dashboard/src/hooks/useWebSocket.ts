import { useEffect, useRef, useState, useCallback } from 'react'
import { WSMessage } from '../types'
import { readDevPreviewContext, wsUrl } from './apiClient'
import { bridgeWsUrl, type BrowserRecorderBridgeConnection } from './api/browserRecorderBridge'
import { useDevView } from './useDevView'

export type WebSocketConnectionState = 'connecting' | 'connected' | 'disconnected'

interface UseWebSocketOptions {
  testId?: string
  projectId?: string
  enabled?: boolean
  onMessage?: (msg: WSMessage) => void
  // Fired every time a socket opens — initial connect and every reconnect.
  // Consumers use this to backfill any window missed while disconnected.
  onOpen?: () => void
  bridge?: BrowserRecorderBridgeConnection | null
}

// Coalesce rapid (re)connect triggers — e.g. the projectId settling from
// undefined to the resolved active project during initial load — into a single
// socket, so the connection indicator doesn't flap through connecting/disconnected.
const CONNECT_DEBOUNCE_MS = 150
const RECONNECT_BASE_DELAY_MS = 3000
const RECONNECT_MAX_DELAY_MS = 30000
// Spread the reconnect storm after a controller restart: without jitter every
// dashboard backs off on the identical schedule and retries in lockstep.
const RECONNECT_JITTER_MS = 1000

export function useWebSocket({ testId, projectId, enabled = true, onMessage, onOpen, bridge }: UseWebSocketOptions = {}) {
  const { mode: devViewMode } = useDevView()
  const socketPreviewMode = !testId && !bridge ? devViewMode : 'admin'
  const previewProjectId = socketPreviewMode === 'user' ? readDevPreviewContext().projectId : ''
  const [connectionState, setConnectionState] = useState<WebSocketConnectionState>(
    enabled ? 'connecting' : 'disconnected',
  )
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<number | null>(null)
  const reconnectAttempts = useRef(0)
  const shouldReconnectRef = useRef(true)
  const onMessageRef = useRef(onMessage)
  onMessageRef.current = onMessage
  const onOpenRef = useRef(onOpen)
  onOpenRef.current = onOpen
  const connectionScopeKey = [
    enabled ? 'enabled' : 'disabled',
    testId || '',
    projectId || '',
    socketPreviewMode,
    previewProjectId,
    bridge?.origin || '',
    bridge?.session_id || '',
    bridge?.token || '',
  ].join('\u0000')
  const connectionScopeRef = useRef({ key: connectionScopeKey, generation: 0 })
  if (connectionScopeRef.current.key !== connectionScopeKey) {
    connectionScopeRef.current = {
      key: connectionScopeKey,
      generation: connectionScopeRef.current.generation + 1,
    }
  }

  const connect = useCallback(() => {
    // The hook may have been disabled (or torn down) between scheduling and
    // firing — reflect that as disconnected rather than leaving a stale
    // "connecting" the socket will never resolve.
    if (!enabled || !shouldReconnectRef.current) {
      setConnectionState('disconnected')
      return
    }
    // A live socket already exists (e.g. a visibility-triggered reconnect raced
    // the backoff timer) — never open a second one.
    if (wsRef.current) return

    const params = new URLSearchParams()
    if (!testId && projectId) params.set('project_id', projectId)
    // Browsers cannot send the preview headers used by fetch. This hint only
    // narrows the simulated identity; the controller ignores it outside its
    // non-production development mode. Never send it to a recorder bridge.
    if (socketPreviewMode === 'user') {
      params.set('apexload_dev_view', 'user')
      if (previewProjectId) params.set('apexload_dev_project', previewProjectId)
    }
    const query = params.size ? `?${params.toString()}` : ''
    const path = testId ? `/api/v1/tests/${testId}/stream` : `/api/v1/ws${query}`
    const url = bridge && !testId ? bridgeWsUrl(bridge, path) : wsUrl(path)

    setConnectionState('connecting')
    const connectionGeneration = connectionScopeRef.current.generation
    const ws = new WebSocket(url)
    wsRef.current = ws
    const isCurrentConnection = () => connectionScopeRef.current.generation === connectionGeneration
      && wsRef.current === ws

    ws.onopen = () => {
      if (!isCurrentConnection()) return
      setConnectionState('connected')
      reconnectAttempts.current = 0
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current)
        reconnectTimer.current = null
      }
      onOpenRef.current?.()
    }

    ws.onmessage = (event) => {
      if (!isCurrentConnection()) return
      try {
        const msg: WSMessage = JSON.parse(event.data)
        onMessageRef.current?.(msg)
      } catch {
        // ignore malformed messages
      }
    }

    ws.onclose = () => {
      if (wsRef.current === ws) {
        wsRef.current = null
      }
      if (connectionScopeRef.current.generation !== connectionGeneration) return
      setConnectionState('disconnected')
      if (!shouldReconnectRef.current) return
      reconnectAttempts.current += 1
      const delay = Math.min(
        RECONNECT_BASE_DELAY_MS * 2 ** Math.min(reconnectAttempts.current - 1, 10),
        RECONNECT_MAX_DELAY_MS,
      )
      const jitter = Math.floor(Math.random() * RECONNECT_JITTER_MS)
      reconnectTimer.current = window.setTimeout(() => {
        if (shouldReconnectRef.current) connect()
      }, delay + jitter)
    }

    ws.onerror = () => {
      ws.close()
    }
  }, [bridge, enabled, previewProjectId, projectId, socketPreviewMode, testId])

  useEffect(() => {
    shouldReconnectRef.current = true
    reconnectAttempts.current = 0
    setConnectionState(enabled ? 'connecting' : 'disconnected')

    let debounceTimer: number | null = null
    if (enabled) {
      debounceTimer = window.setTimeout(connect, CONNECT_DEBOUNCE_MS)
    }

    return () => {
      shouldReconnectRef.current = false
      if (debounceTimer !== null) {
        clearTimeout(debounceTimer)
      }
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current)
        reconnectTimer.current = null
      }
      const ws = wsRef.current
      wsRef.current = null
      if (ws) {
        ws.onclose = null
        ws.onopen = null
        ws.onmessage = null
        ws.onerror = null
        ws.close()
      }
    }
  }, [connect, enabled])

  // The socket is intentionally kept open while the tab is in the background so
  // live metrics keep streaming (and the graphs don't develop a gap the length
  // of the time the tab was unfocused). The only thing we do on visibility
  // change is recover: a tab the browser froze/discarded — or a machine that
  // slept — may have had its socket closed and its reconnect timer throttled, so
  // when we become visible again, reconnect immediately instead of waiting out
  // the backoff.
  useEffect(() => {
    if (typeof document === 'undefined') return

    const handleVisibilityChange = () => {
      if (document.visibilityState !== 'visible') return
      if (!enabled || !shouldReconnectRef.current || wsRef.current) return
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current)
        reconnectTimer.current = null
      }
      connect()
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [connect, enabled])

  return { connected: connectionState === 'connected', connectionState }
}
