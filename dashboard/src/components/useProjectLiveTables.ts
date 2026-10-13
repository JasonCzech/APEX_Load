import { useEffect, useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { ProjectLiveTableView } from '../types/projectLiveTables'
import { getErrorMessage } from '../utils/errorUtils'

export interface ProjectLiveTablesState { tables: ProjectLiveTableView[]; loading: boolean; error?: string }

const NO_TABLES: ProjectLiveTableView[] = []

/** A project's live tables, loaded once while `enabled` (a binding uses one). */
export function useProjectLiveTables(projectId: string | undefined, enabled: boolean): ProjectLiveTablesState {
  const api = useApi()
  const [state, setState] = useState<ProjectLiveTablesState & { projectId?: string }>({ tables: [], loading: false })
  useEffect(() => {
    if (!enabled || !projectId) return undefined
    const abort = new AbortController()
    setState({ projectId, tables: [], loading: true })
    void api.listProjectLiveTables(projectId, { signal: abort.signal }).then(response => { if (!abort.signal.aborted) setState({ projectId, tables: response.tables, loading: false }) }).catch(cause => { if (!abort.signal.aborted) setState({ projectId, tables: [], loading: false, error: getErrorMessage(cause, 'Unable to load project live tables.') }) })
    return () => abort.abort()
  }, [api, projectId, enabled])
  return state.projectId === projectId && enabled ? state : { tables: NO_TABLES, loading: Boolean(enabled && projectId) }
}
