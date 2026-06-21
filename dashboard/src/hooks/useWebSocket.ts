import { useEffect, useRef, useState, useCallback } from 'react'
import { WSMessage } from '../types'
import { wsUrl } from './apiClient'
import { bridgeWsUrl, type BrowserRecorderBridgeConnection } from './api/browserRecorderBridge'

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
const RECONNECT_DELAY_MS = 3000

export function useWebSocket({ testId, projectId, enabled = true, onMessage, onOpen, bridge }: UseWebSocketOptions = {}) {
  const [connectionState, setConnectionState] = useState<WebSocketConnectionState>(
    enabled ? 'connecting' : 'disconnected',
  )
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<number | null>(null)
  const shouldReconnectRef = useRef(true)
  const onMessageRef = useRef(onMessage)
  onMessageRef.current = onMessage
  const onOpenRef = useRef(onOpen)
  onOpenRef.current = onOpen

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

    const query = !testId && projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''
    const path = testId ? `/api/v1/tests/${testId}/stream` : `/api/v1/ws${query}`
    const url = bridge && !testId ? bridgeWsUrl(bridge, path) : wsUrl(path)

    setConnectionState('connecting')
    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onopen = () => {
      setConnectionState('connected')
      if (reconnectTimer.current) {
        clearTimeout(reconnectTimer.current)
        reconnectTimer.current = null
      }
      onOpenRef.current?.()
    }

    ws.onmessage = (event) => {
      try {
        const msg: WSMessage = JSON.parse(event.data)
        onMessageRef.current?.(msg)
      } catch {
        // ignore malformed messages
      }
    }

    ws.onclose = () => {
      setConnectionState('disconnected')
      if (wsRef.current === ws) {
        wsRef.current = null
      }
      if (!shouldReconnectRef.current) return
      reconnectTimer.current = window.setTimeout(() => {
        if (shouldReconnectRef.current) connect()
      }, RECONNECT_DELAY_MS)
    }

    ws.onerror = () => {
      ws.close()
    }
  }, [bridge, enabled, projectId, testId])

  useEffect(() => {
    shouldReconnectRef.current = true
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
