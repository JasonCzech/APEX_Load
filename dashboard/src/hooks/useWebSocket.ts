import { useEffect, useRef, useState, useCallback } from 'react'
import { WSMessage } from '../types'

interface UseWebSocketOptions {
  testId?: string
  onMessage?: (msg: WSMessage) => void
}

export function useWebSocket({ testId, onMessage }: UseWebSocketOptions = {}) {
  const [connected, setConnected] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<number | null>(null)
  const onMessageRef = useRef(onMessage)
  onMessageRef.current = onMessage

  const connect = useCallback(() => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const host = window.location.host
    const path = testId ? `/api/v1/tests/${testId}/stream` : '/api/v1/ws'
    const url = `${protocol}//${host}${path}`

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
      wsRef.current = null
      reconnectTimer.current = window.setTimeout(connect, 3000)
    }

    ws.onerror = () => {
      ws.close()
    }
  }, [testId])

  useEffect(() => {
    connect()
    return () => {
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current)
      wsRef.current?.close()
    }
  }, [connect])

  return { connected }
}
