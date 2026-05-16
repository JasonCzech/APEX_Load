import { useEffect, useRef, useState, useCallback } from 'react'
import { WSMessage } from '../types'
import { wsUrl } from './apiClient'

export type WebSocketConnectionState = 'connecting' | 'connected' | 'disconnected'

interface UseWebSocketOptions {
  testId?: string
  projectId?: string
  enabled?: boolean
  onMessage?: (msg: WSMessage) => void
}

export function useWebSocket({ testId, projectId, enabled = true, onMessage }: UseWebSocketOptions = {}) {
  const [connectionState, setConnectionState] = useState<WebSocketConnectionState>(enabled ? 'connecting' : 'disconnected')
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<number | null>(null)
  const shouldReconnectRef = useRef(true)
  const isPageVisibleRef = useRef(
    typeof document === 'undefined' ? true : document.visibilityState === 'visible',
  )
  const onMessageRef = useRef(onMessage)
  onMessageRef.current = onMessage

  const connect = useCallback(() => {
    if (!enabled || !shouldReconnectRef.current || !isPageVisibleRef.current) return

    const query = !testId && projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''
    const path = testId ? `/api/v1/tests/${testId}/stream` : `/api/v1/ws${query}`
    const url = wsUrl(path)

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
  }, [enabled, projectId, testId])

  useEffect(() => {
    shouldReconnectRef.current = true
    if (enabled) {
      setConnectionState('connecting')
      connect()
    } else {
      setConnectionState('disconnected')
    }

    return () => {
      shouldReconnectRef.current = false
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
