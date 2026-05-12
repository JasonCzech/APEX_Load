import { useEffect, useRef, useState, useCallback } from 'react'
import { WSMessage } from '../types'
import { wsUrl } from './apiClient'

interface UseWebSocketOptions {
  testId?: string
  onMessage?: (msg: WSMessage) => void
}

export function useWebSocket({ testId, onMessage }: UseWebSocketOptions = {}) {
  const [connected, setConnected] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<number | null>(null)
  const shouldReconnectRef = useRef(true)
  const isPageVisibleRef = useRef(
    typeof document === 'undefined' ? true : document.visibilityState === 'visible',
  )
  const onMessageRef = useRef(onMessage)
  onMessageRef.current = onMessage

  const connect = useCallback(() => {
    if (!shouldReconnectRef.current || !isPageVisibleRef.current) return

    const path = testId ? `/api/v1/tests/${testId}/stream` : '/api/v1/ws'
    const url = wsUrl(path)

    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onopen = () => {
      setConnected(true)
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
      setConnected(false)
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
  }, [testId])

  useEffect(() => {
    shouldReconnectRef.current = true
    connect()

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
  }, [connect])

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
        setConnected(false)
        return
      }

      if (shouldReconnectRef.current && !wsRef.current) {
        connect()
      }
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [connect])

  return { connected }
}
