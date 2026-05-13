import { useCallback, useState } from 'react'

import { useWebSocket } from '../hooks/useWebSocket'
import { MetricsHistoryPoint, TestStatus, WSMessage } from '../types'

export const EXECUTION_STREAM_HISTORY_LIMIT = 300

function shouldStreamExecutionMetrics(status?: TestStatus): boolean {
  return status === 'RUNNING' || status === 'INITIALIZING' || status === 'PAUSED' || status === 'STOPPING'
}

export function useExecutionMetricsStream(testId: string, status?: TestStatus) {
  const [localMetricsHistory, setLocalMetricsHistory] = useState<MetricsHistoryPoint[]>([])

  const handleTestWSMessage = useCallback((msg: WSMessage) => {
    if (msg.type !== 'metrics_update' || !msg.data || !msg.timestamp) {
      return
    }

    const point: MetricsHistoryPoint = {
      time: new Date(msg.timestamp).toLocaleTimeString(),
      vusers: msg.data.active_vusers,
      tps: Math.round(msg.data.tps * 10) / 10,
      avgMs: Math.round(msg.data.avg_response_ms * 10) / 10,
      errorPct: Math.round(msg.data.error_pct * 100) / 100,
      p50: Math.round(msg.data.p50_ms * 10) / 10,
      p90: Math.round(msg.data.p90_ms * 10) / 10,
      p99: Math.round(msg.data.p99_ms * 10) / 10,
    }

    if (msg.data.groups) {
      for (const [name, groupMetrics] of Object.entries(msg.data.groups)) {
        point[`${name}_tps`] = Math.round(groupMetrics.tps * 10) / 10
        point[`${name}_avgMs`] = Math.round(groupMetrics.avg_response_ms * 10) / 10
        point[`${name}_vusers`] = groupMetrics.vusers
      }
    }

    if (msg.data.resources) {
      const resources = msg.data.resources
      point.cpuProcess = Math.round(resources.cpu_process_pct * 10) / 10
      point.cpuFree = Math.round(resources.cpu_free_pct * 10) / 10
      point.memProcess = Math.round(resources.mem_process_mb * 10) / 10
      point.memFree = Math.round(resources.mem_free_mb * 10) / 10
      point.netSent = Math.round(resources.net_sent_kbps * 10) / 10
      point.netRecv = Math.round(resources.net_recv_kbps * 10) / 10
      point.diskRead = Math.round(resources.disk_read_kbps * 10) / 10
      point.diskWrite = Math.round(resources.disk_write_kbps * 10) / 10
    }

    setLocalMetricsHistory((prev) => [...prev, point].slice(-EXECUTION_STREAM_HISTORY_LIMIT))
  }, [])

  useWebSocket({
    testId,
    enabled: shouldStreamExecutionMetrics(status),
    onMessage: handleTestWSMessage,
  })

  return { localMetricsHistory }
}