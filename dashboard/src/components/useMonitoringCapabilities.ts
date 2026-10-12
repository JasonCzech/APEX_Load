import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { JsonSuccessResponseBody } from '../types/generated/openapi'

export type MonitoringCapabilities = JsonSuccessResponseBody<'handleAnalysisCapabilities', '200'>

/** A project's analysis and run-monitoring capabilities; undefined until they load. */
export function useMonitoringCapabilities(projectId?: string) {
  const [capabilities, setCapabilities] = useState<MonitoringCapabilities>()
  useEffect(() => { const c = new AbortController(); setCapabilities(undefined); if (projectId) void request<MonitoringCapabilities>(`/analysis/capabilities?project_id=${encodeURIComponent(projectId)}`, { signal: c.signal }).then(caps => { if (!c.signal.aborted) setCapabilities(caps) }).catch(() => {}); return () => c.abort() }, [projectId])
  return capabilities
}

/** Whether run-bound monitoring is on for a project (false until known). */
export function useMonitoringEnabled(projectId?: string) { return useMonitoringCapabilities(projectId)?.monitoring_enabled === true }
