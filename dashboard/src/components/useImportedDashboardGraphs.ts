import { useEffect, useState } from 'react'

import { ImportedDashboardResult, MetricsHistoryPoint, TestSummary } from '../types'
import { ApiError } from '../hooks/apiClient'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { getErrorMessage } from '../utils/errorUtils'
import { LIVE_PROMETHEUS_REFRESH_STATUSES, usePrometheusQueryWindow } from './prometheusQueryWindow'

const EMPTY_DASHBOARDS: ImportedDashboardResult[] = []
const EMPTY_STATE = { dashboards: EMPTY_DASHBOARDS, loading: false, error: '', available: false }

// Imported dashboards share the curated pack's quantized query window, but their
// availability comes from the run/project response, including frozen snapshots.
export function useImportedDashboardGraphs(test: TestSummary | null, filteredData: MetricsHistoryPoint[], projectId?: string) {
  const { getImportedDashboardGraphs } = useSystemApi()
  const testId = test?.id
  const scopedProjectId = projectId || test?.project_id || 'unassigned'
  const scope = JSON.stringify([testId, scopedProjectId])
  const [state, setState] = useState({ scope, ...EMPTY_STATE })

  const { windowStartMs, windowEndMs, windowStepSeconds } = usePrometheusQueryWindow(test, filteredData)
  const testStatus = test?.status

  useEffect(() => {
    if (windowStartMs === null || windowEndMs === null || windowStepSeconds === null) {
      setState({ scope, ...EMPTY_STATE })
      return
    }

    let cancelled = false
    let inFlight = false
    let timer: ReturnType<typeof window.setInterval> | undefined

    const load = async (showLoading: boolean) => {
      if (inFlight) return
      inFlight = true
      if (showLoading) setState(previous => ({ ...(previous.scope === scope ? previous : EMPTY_STATE), scope, loading: true }))
      try {
        const response = await getImportedDashboardGraphs({
          project_id: scopedProjectId,
          test_id: testId,
          start: new Date(windowStartMs).toISOString(),
          end: new Date(windowEndMs).toISOString(),
          step_seconds: windowStepSeconds,
        })
        if (cancelled) return
        const enabledAndConfigured = response.enabled && response.configured
        const dashboards = enabledAndConfigured ? response.dashboards : EMPTY_DASHBOARDS
        setState({ scope, dashboards, available: dashboards.some(dashboard => dashboard.panels.length > 0), loading: false, error: '' })
      } catch (err) {
        if (cancelled) return
        setState(previous => ({
          scope,
          dashboards: EMPTY_DASHBOARDS,
          available: !(err instanceof ApiError && err.status === 403) && previous.scope === scope && previous.available,
          loading: false,
          error: getErrorMessage(err, 'Imported dashboards unavailable.'),
        }))
      } finally {
        inFlight = false
      }
    }

    void load(true)
    if (testStatus && LIVE_PROMETHEUS_REFRESH_STATUSES.has(testStatus)) {
      timer = window.setInterval(() => {
        void load(false)
      }, 10_000)
    }

    return () => {
      cancelled = true
      if (timer !== undefined) window.clearInterval(timer)
    }
  }, [getImportedDashboardGraphs, scope, windowStartMs, windowEndMs, windowStepSeconds, testStatus, testId, scopedProjectId])

  const current = state.scope === scope ? state : EMPTY_STATE
  return {
    importedDashboards: current.dashboards,
    importedDashboardsLoading: current.loading,
    importedDashboardsError: current.error,
    importedDashboardsAvailable: current.available,
  }
}
