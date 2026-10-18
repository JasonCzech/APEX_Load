import { useEffect, useState } from 'react'

import { MetricsHistoryPoint, PrometheusGraph, TestSummary } from '../types'
import { ApiError } from '../hooks/apiClient'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { getErrorMessage } from '../utils/errorUtils'
import { LIVE_PROMETHEUS_REFRESH_STATUSES, usePrometheusQueryWindow } from './prometheusQueryWindow'

// Platform graphs need global viewer access, which does not change within a
// session. After one 403 the graphs stay hidden instead of re-asking on every
// poll and showing the server's permission sentence.
let forbiddenForSession = false
const EMPTY_GRAPHS: PrometheusGraph[] = []
const EMPTY_STATE = { graphs: EMPTY_GRAPHS, loading: false, error: '', available: false }

export function resetPrometheusGraphsAccessForTests() {
  forbiddenForSession = false
}

export function usePrometheusGraphs(test: TestSummary | null, filteredData: MetricsHistoryPoint[]) {
  const { getPrometheusGraphs } = useSystemApi()
  const scope = JSON.stringify([test?.id, test?.project_id])
  const [state, setState] = useState({ scope, ...EMPTY_STATE })

  const { windowStartMs, windowEndMs, windowStepSeconds } = usePrometheusQueryWindow(test, filteredData)
  const testStatus = test?.status

  useEffect(() => {
    if (windowStartMs === null || windowEndMs === null || windowStepSeconds === null || forbiddenForSession) {
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
        const response = await getPrometheusGraphs({
          start: new Date(windowStartMs).toISOString(),
          end: new Date(windowEndMs).toISOString(),
          step_seconds: windowStepSeconds,
        })
        if (cancelled) return
        const available = Boolean(response.enabled && response.configured)
        setState({ scope, available, graphs: available ? response.graphs : EMPTY_GRAPHS, loading: false, error: '' })
      } catch (err) {
        if (cancelled) return
        if (err instanceof ApiError && err.status === 403) {
          forbiddenForSession = true
          setState({ scope, ...EMPTY_STATE })
          if (timer !== undefined) window.clearInterval(timer)
          timer = undefined
          return
        }
        // A refresh failure does not erase a confirmed configuration. It remains
        // a visible destination with an error instead of a disappearing tab.
        setState(previous => ({
          scope,
          graphs: EMPTY_GRAPHS,
          available: previous.scope === scope && previous.available,
          loading: false,
          error: getErrorMessage(err, 'Prometheus metrics unavailable.'),
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
  }, [getPrometheusGraphs, scope, windowStartMs, windowEndMs, windowStepSeconds, testStatus])

  // Mask the previous run synchronously; effects and responses settle later.
  const current = state.scope === scope ? state : EMPTY_STATE
  return {
    prometheusGraphs: current.graphs,
    prometheusLoading: current.loading,
    prometheusError: current.error,
    prometheusAvailable: current.available,
  }
}
