import { useCallback, useEffect, useRef, useState } from 'react'

import { useApi } from '../../hooks/useApi'
import type { RcaAnalysisResponse } from '../../types/rca'
import { getErrorMessage } from '../../utils/errorUtils'
import { normalizeRcaAssessment } from './rcaModel'

// A pending analysis is polled from every 2 s, backing off to every 15 s.
const POLL_MS = 2000
const POLL_MAX_MS = 15000

const pending = (state?: string) => state === 'queued' || state === 'running'

export interface RootCauseAnalysisModel {
  response: RcaAnalysisResponse | null
  loading: boolean
  error: string
  requesting: boolean
  requestError: string
  // request queues an analysis and resolves to it, or null when the
  // controller refused it (requestError says why).
  request: (body?: { enrich?: boolean; baseline_test_id?: string }) => Promise<RcaAnalysisResponse | null>
  reload: () => void
}

function normalized(response: RcaAnalysisResponse): RcaAnalysisResponse {
  return response.assessment ? { ...response, assessment: normalizeRcaAssessment(response.assessment) } : response
}

// useRootCauseAnalysis loads a run's root-cause analysis: the one the route
// names, or else the latest completed one. A queued or running analysis is
// polled until it ends.
export function useRootCauseAnalysis(testId: string, analysisId: string | undefined, enabled: boolean): RootCauseAnalysisModel {
  const api = useApi()
  const [response, setResponse] = useState<RcaAnalysisResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [requesting, setRequesting] = useState(false)
  const [requestError, setRequestError] = useState('')
  const [reloadKey, setReloadKey] = useState(0)
  const shownKey = useRef('')

  useEffect(() => {
    // A reload of the same analysis keeps it shown until the new copy
    // arrives; another run or analysis starts empty.
    const key = JSON.stringify([testId, analysisId || '', enabled])
    if (shownKey.current !== key) {
      shownKey.current = key
      setResponse(null)
      setRequestError('')
    }
    setError('')
    if (!enabled || !testId) {
      setLoading(false)
      return
    }
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let delay = POLL_MS
    const poll = (id: string) => {
      timer = setTimeout(() => void load(id, false), delay)
      delay = Math.min(POLL_MAX_MS, Math.round(delay * 1.5))
    }
    const load = async (id: string | undefined, first: boolean): Promise<void> => {
      if (first) setLoading(true)
      try {
        const next = normalized(id
          ? await api.getRootCauseAnalysis(testId, id, { signal: controller.signal })
          : await api.getRootCauseAssessment(testId, { signal: controller.signal }))
        if (controller.signal.aborted) return
        setResponse(next)
        setError('')
        if (pending(next.state) && next.analysis_id) poll(next.analysis_id)
      } catch (err) {
        if (controller.signal.aborted) return
        setError(getErrorMessage(err, 'Unable to load the root-cause analysis'))
        // A failed poll keeps polling, so a brief outage does not leave the
        // analysis shown as pending for good.
        if (!first && id) poll(id)
      } finally {
        if (first && !controller.signal.aborted) setLoading(false)
      }
    }
    void load(analysisId, true)
    return () => {
      controller.abort()
      if (timer) clearTimeout(timer)
    }
  }, [api, analysisId, enabled, reloadKey, testId])

  const request = useCallback(async (body: { enrich?: boolean; baseline_test_id?: string } = {}) => {
    setRequesting(true)
    setRequestError('')
    try {
      return normalized(await api.requestRootCauseAnalysis(testId, body))
    } catch (err) {
      setRequestError(getErrorMessage(err, 'Unable to start a root-cause analysis'))
      return null
    } finally {
      setRequesting(false)
    }
  }, [api, testId])

  const reload = useCallback(() => setReloadKey(key => key + 1), [])

  return { response, loading, error, requesting, requestError, request, reload }
}
