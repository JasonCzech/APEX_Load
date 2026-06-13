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
  bridge?: BrowserRecorderBridgeConnection | null
}

// Coalesce rapid (re)connect triggers — e.g. the projectId settling from
// undefined to the resolved active project during initial load — into a single
// socket, so the connection indicator doesn't flap through connecting/disconnected.
const CONNECT_DEBOUNCE_MS = 150

function pageVisible() {
  return typeof document === 'undefined' || document.visibilityState === 'visible'
}

export function useWebSocket({ testId, projectId, enabled = true, onMessage, bridge }: UseWebSocketOptions = {}) {
  const [connectionState, setConnectionState] = useState<WebSocketConnectionState>(
    enabled && pageVisible() ? 'connecting' : 'disconnected',
  )
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<number | null>(null)
  const shouldReconnectRef = useRef(true)
  const isPageVisibleRef = useRef(pageVisible())
  const onMessageRef = useRef(onMessage)
  onMessageRef.current = onMessage

  const connect = useCallback(() => {
    // The page can become hidden (or the hook disabled) between scheduling and
    // firing — reflect that as disconnected rather than leaving a stale
    // "connecting" the socket will never resolve.
    if (!enabled || !shouldReconnectRef.current || !isPageVisibleRef.current) {
      setConnectionState('disconnected')
      return
    }

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
      if (!shouldReconnectRef.current || !isPageVisibleRef.current) return
      reconnectTimer.current = window.setTimeout(() => {
        if (shouldReconnectRef.current && isPageVisibleRef.current) connect()
      }, 3000)
    }

    ws.onerror = () => {
      ws.close()
    }
  }, [bridge, enabled, projectId, testId])

  useEffect(() => {
    shouldReconnectRef.current = true

    // Only advertise "connecting" when we are actually going to open a socket.
    // A hidden tab waits for the visibilitychange handler to connect, so showing
    // "connecting" there would stick forever.
    const willConnect = enabled && isPageVisibleRef.current
    setConnectionState(willConnect ? 'connecting' : 'disconnected')

    let debounceTimer: number | null = null
    if (willConnect) {
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

  useEffect(() => {
    if (typeof document === 'undefined') return

    const handleVisibilityChange = () => {
      isPageVisibleRef.current = document.visibilityState === 'visible'

      if (!isPageVisibleRef.current) {
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
        setConnectionState('disconnected')
        return
      }

      if (enabled && shouldReconnectRef.current && !wsRef.current) {
        connect()
      }
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [connect, enabled])

  return { connected: connectionState === 'connected', connectionState }
}
