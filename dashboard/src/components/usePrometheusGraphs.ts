import { useEffect, useState } from 'react'

import { MetricsHistoryPoint, PrometheusGraph, TestSummary } from '../types'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { getErrorMessage } from '../utils/errorUtils'
import { LIVE_PROMETHEUS_REFRESH_STATUSES, usePrometheusQueryWindow } from './prometheusQueryWindow'

export function usePrometheusGraphs(test: TestSummary | null, filteredData: MetricsHistoryPoint[]) {
  const { getPrometheusGraphs } = useSystemApi()
  const [graphs, setGraphs] = useState<PrometheusGraph[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const { windowStartMs, windowEndMs, windowStepSeconds } = usePrometheusQueryWindow(test, filteredData)
  const testStatus = test?.status

  useEffect(() => {
    if (windowStartMs === null || windowEndMs === null || windowStepSeconds === null) {
      setGraphs([])
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
        const response = await getPrometheusGraphs({
          start: new Date(windowStartMs).toISOString(),
          end: new Date(windowEndMs).toISOString(),
          step_seconds: windowStepSeconds,
        })
        if (cancelled) return
        const enabledAndConfigured = response.enabled && response.configured
        setGraphs(enabledAndConfigured ? response.graphs : [])
        setError('')
      } catch (err) {
        if (cancelled) return
        setGraphs([])
        setError(getErrorMessage(err, 'Prometheus metrics unavailable.'))
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
  }, [getPrometheusGraphs, windowStartMs, windowEndMs, windowStepSeconds, testStatus])

	return { prometheusGraphs: graphs, prometheusLoading: loading, prometheusError: error }
}
