import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError } from '../hooks/apiClient'
import { useApi } from '../hooks/useApi'
import { type StoredScript } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { scenarioDraftBuilderHash } from './importScenario'
import { scenarioAuthoringDraftFromScripts } from './scenarioAuthoringDraft'

// One scenario from a saved import batch: a group per script, saved as a
// project draft and opened in the Scenario Builder to shape the load. A draft
// can hold scripts whose inputs are still missing, which a saved scenario
// refuses; the builder saves the scenario once they can run. Every importer's
// save confirmation offers it.
export function useImportScenario(projectId: string | undefined) {
  const api = useApi()
  const [busy, setBusy] = useState(false)
  // An error belongs to the saved scripts it was for: a later save shows its
  // own confirmation, without an earlier batch's failure.
  const [failure, setFailure] = useState<{ key: string; message: string } | null>(null)
  const mountedRef = useRef(true)
  const busyRef = useRef(false)
  // The draft made for a set of saved scripts. When leaving the import is
  // cancelled (it still holds unsaved drafts), the next click opens that draft
  // again instead of saving another.
  const createdRef = useRef<{ key: string; draftId: string } | null>(null)
  useEffect(() => {
    mountedRef.current = true
    return () => { mountedRef.current = false }
  }, [])
  const create = useCallback(async (name: string, scripts: StoredScript[]) => {
    if (!projectId || scripts.length === 0 || busyRef.current) return
    const key = importScenarioKey(projectId, scripts)
    if (createdRef.current?.key === key) {
      window.location.hash = scenarioDraftBuilderHash(createdRef.current.draftId, projectId)
      return
    }
    busyRef.current = true
    setBusy(true)
    setFailure(null)
    try {
      const draft = await api.createScenarioDraft(projectId, { name, document: scenarioAuthoringDraftFromScripts(projectId, name, scripts) })
      createdRef.current = { key, draftId: draft.id }
      if (!mountedRef.current) return
      window.location.hash = scenarioDraftBuilderHash(draft.id, projectId)
    } catch (cause) {
      if (mountedRef.current) {
        setFailure({
          key,
          message: cause instanceof ApiError && cause.status === 403
            ? 'Your role cannot create scenarios in this project. Ask a Project Administrator to create one from these scripts.'
            : getErrorMessage(cause, 'Unable to create the scenario.'),
        })
      }
    } finally {
      busyRef.current = false
      if (mountedRef.current) setBusy(false)
    }
  }, [api, projectId])
  const errorFor = useCallback((scripts: readonly StoredScript[]) => (
    projectId && failure?.key === importScenarioKey(projectId, scripts) ? failure.message : ''
  ), [failure, projectId])
  const reset = useCallback(() => setFailure(null), [])
  return { available: Boolean(projectId), create, busy, errorFor, reset }
}

function importScenarioKey(projectId: string, scripts: readonly StoredScript[]): string {
  return [projectId, ...scripts.map(script => script.id)].join('\n')
}

// The name a scenario from an import takes: the source's own name when there
// is one, else the one saved script's, else a count.
export function importScenarioName(sourceName: string | undefined, scripts: readonly StoredScript[]): string {
  const base = (sourceName || '').replace(/\.[^./\\]+$/, '').trim()
  if (base) return base
  if (scripts.length === 1) return scripts[0].name
  return `${scripts.length} imported scripts`
}
