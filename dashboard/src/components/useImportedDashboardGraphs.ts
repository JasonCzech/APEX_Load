import { useEffect, useState } from 'react'

import { ImportedDashboardResult, MetricsHistoryPoint, TestSummary } from '../types'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { getErrorMessage } from '../utils/errorUtils'
import { LIVE_PROMETHEUS_REFRESH_STATUSES, usePrometheusQueryWindow } from './prometheusQueryWindow'

const EMPTY_DASHBOARDS: ImportedDashboardResult[] = []

// useImportedDashboardGraphs is the sibling of usePrometheusGraphs for imported Grafana
// dashboards. It shares usePrometheusQueryWindow so both poll on the exact same
// quantized window (otherwise the two graph sets tear against each other on the shared
// cursor), but has its own endpoint, response shape, and disabled semantics.
export function useImportedDashboardGraphs(test: TestSummary | null, filteredData: MetricsHistoryPoint[], projectId?: string) {
  const { getImportedDashboardGraphs } = useSystemApi()
  const [dashboards, setDashboards] = useState<ImportedDashboardResult[]>(EMPTY_DASHBOARDS)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const { windowStartMs, windowEndMs, windowStepSeconds } = usePrometheusQueryWindow(test, filteredData)
  const testStatus = test?.status
	const testId = test?.id
	const scopedProjectId = projectId || test?.project_id || 'unassigned'

  useEffect(() => {
    if (windowStartMs === null || windowEndMs === null || windowStepSeconds === null) {
      setDashboards(EMPTY_DASHBOARDS)
      setLoading(false)
      setError('')
      return
    }

		let cancelled = false
		let inFlight = false
    let timer: ReturnType<typeof window.setInterval> | undefined

		const load = async (showLoading: boolean) => {
			if (inFlight) return
			inFlight = true
      if (showLoading) setLoading(true)
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
        setDashboards(enabledAndConfigured ? response.dashboards : EMPTY_DASHBOARDS)
        setError('')
      } catch (err) {
        if (cancelled) return
        setDashboards(EMPTY_DASHBOARDS)
        setError(getErrorMessage(err, 'Imported dashboards unavailable.'))
			} finally {
				inFlight = false
        if (!cancelled) setLoading(false)
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
	}, [getImportedDashboardGraphs, windowStartMs, windowEndMs, windowStepSeconds, testStatus, testId, scopedProjectId])

  return {
    importedDashboards: dashboards,
		importedDashboardsLoading: loading,
    importedDashboardsError: error,
  }
}
