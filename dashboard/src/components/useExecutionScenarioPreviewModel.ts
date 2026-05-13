import { useEffect, useMemo, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { buildQueuedScenarioRows, QueuedScenarioRow, ScenarioScriptDetail } from './testExecutionScenarioModel'

interface ExecutionScenarioPreviewModel {
  queuedScenarioRows: QueuedScenarioRow[]
  scriptDetailsLoading: boolean
  scriptDetailsError: string
}

export function useExecutionScenarioPreviewModel(test: TestSummary | null): ExecutionScenarioPreviewModel {
  const api = useApi()
  const [scriptDetails, setScriptDetails] = useState<Record<string, ScenarioScriptDetail>>({})
  const [scriptDetailsLoading, setScriptDetailsLoading] = useState(false)
  const [scriptDetailsError, setScriptDetailsError] = useState('')

  const scriptIdsKey = useMemo(() => {
    if (!test?.groups?.length) return ''
    const ids = [...new Set(test.groups.map((group) => group.script_id).filter(Boolean))].sort()
    return ids.join('|')
  }, [test?.groups])

  useEffect(() => {
    if (!scriptIdsKey) {
      setScriptDetails({})
      setScriptDetailsError('')
      setScriptDetailsLoading(false)
      return
    }

    const scriptIds = scriptIdsKey.split('|').filter(Boolean)
    let cancelled = false

    const loadScriptDetails = async () => {
      setScriptDetailsLoading(true)
      setScriptDetailsError('')
      try {
        const results = await Promise.all(scriptIds.map(async (scriptId) => {
          const detail = await api.getScript(scriptId)
          return [scriptId, detail as ScenarioScriptDetail] as const
        }))
        if (cancelled) return
        setScriptDetails(Object.fromEntries(results))
      } catch (error: unknown) {
        if (cancelled) return
        setScriptDetailsError(getErrorMessage(error, 'Unable to load script details'))
      } finally {
        if (!cancelled) {
          setScriptDetailsLoading(false)
        }
      }
    }

    loadScriptDetails()

    return () => {
      cancelled = true
    }
  }, [api, scriptIdsKey])

  const queuedScenarioRows = useMemo(() => buildQueuedScenarioRows(test, scriptDetails), [scriptDetails, test])

  return {
    queuedScenarioRows,
    scriptDetailsLoading,
    scriptDetailsError,
  }
}