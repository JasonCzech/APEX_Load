import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState, type ReactNode } from 'react'

import { ApiError } from '../hooks/apiClient'
import { type DataPoolImportResult } from '../hooks/api/useScriptApi'
import { useApi } from '../hooks/useApi'
import type { Project, SharedDataPool, SharedDataPoolRow } from '../types'
import { downloadBlob } from '../utils/fileUtils'
import { getErrorMessage } from '../utils/errorUtils'
import SettingsConfirmDialog from './SettingsConfirmDialog'
import SharedAssetUsedBy from './SharedAssetUsedBy'
import { useSharedAssetUsage } from './useSharedAssetUsage'
import { clearSettingsBreadcrumb, readSettingsBreadcrumb, writeSettingsBreadcrumb } from './settingsBreadcrumbs'
import { formatDateTime } from './settingsPageFormatters'
import DataRowGrid, { type DataGridRow } from './ux/DataRowGrid'
import { orderedGridColumns, rowsToCSV } from './ux/dataRowGridModel'

const DATA_POOL_ROW_PAGE_SIZE = 100
// The engine's per-data-source column cap, which the server enforces too.
const DATA_POOL_MAX_COLUMNS = 4096
// Row statuses that runs skip (isActiveDataPoolRowStatus on the server).
const INACTIVE_DATA_POOL_ROW_STATUSES = new Set(['consumed', 'used', 'disabled', 'inactive', 'exhausted'])

/** A typed pool name or description not yet added, for Settings' Save and Discard. */
export interface DataPoolsDraftController {
  save: () => Promise<boolean>
  discard: () => void
}

interface Props {
  activeProject: Project | null | undefined
  /** Projects a pool can be created in, to name the one it lands in. */
  projects: Project[]
  /** A pool to open, from a link. */
  preferredPoolId?: string
  /** Whether the viewer may create, change and delete pools and rows. */
  canManage: boolean
  /** Shown in place of the create form's project line when no project is active. */
  noProjectHint?: ReactNode
  /** The draft in the create form: typed (dirty) and whether it can be added (ready). */
  onDraftChange?: (draft: { dirty: boolean; ready: boolean }) => void
  /** A load or list error while no pool is open; without it the error shows here. */
  onListError?: (message: string) => void
}

/**
 * A project's shared data pools: create and delete pools, and see, edit,
 * import and export their rows. Shared assets › Test data shows it; what can
 * be changed follows the viewer's role.
 */
/** A pool row's count of the scenarios that bind it. */
function usedByCount(count: number): string {
  return count === 0 ? 'Not bound by a scenario' : `Used by ${count} ${count === 1 ? 'scenario' : 'scenarios'}`
}

const DataPoolsManager = forwardRef<DataPoolsDraftController, Props>(function DataPoolsManager({ activeProject, projects, preferredPoolId, canManage, noProjectHint, onDraftChange, onListError }, ref) {
  const api = useApi()
  const [confirmDeletePool, setConfirmDeletePool] = useState<SharedDataPool | null>(null)
  // The scenarios that bind each pool: a bound pool cannot be deleted.
  const { usage: assetUsage, error: assetUsageError, refresh: refreshAssetUsage } = useSharedAssetUsage(activeProject?.id, 'data_pools')
  const poolUses = (poolId: string) => assetUsage ? assetUsage.data_pools?.[poolId] || [] : undefined
  const [confirmDeleteRow, setConfirmDeleteRow] = useState<SharedDataPoolRow | null>(null)
  const [dataPools, setDataPools] = useState<SharedDataPool[]>([])
  const [selectedDataPoolId, setSelectedDataPoolId] = useState('')
  const [dataPoolRows, setDataPoolRows] = useState<SharedDataPoolRow[]>([])
  const [dataPoolName, setDataPoolName] = useState('')
  const [dataPoolDescription, setDataPoolDescription] = useState('')
  const [dataPoolProjectId, setDataPoolProjectId] = useState('')
  const [dataPoolActionError, setDataPoolActionError] = useState('')
  const [dataPoolActionNotice, setDataPoolActionNotice] = useState('')
  const [dataPoolRowsTotal, setDataPoolRowsTotal] = useState(0)
  const [dataPoolRowsNextCursor, setDataPoolRowsNextCursor] = useState<string | null>(null)
  const [dataPoolRowsHasMore, setDataPoolRowsHasMore] = useState(false)
  const [dataPoolRowsLoadBusy, setDataPoolRowsLoadBusy] = useState(false)
  const [dataPoolCatalogMutationBusy, setDataPoolCatalogMutationBusy] = useState(false)
  // The draft binds the uploaded file to the pool it was previewed against, so
  // a pool switch during the dry-run upload can never commit into another pool.
  const [dataPoolImportDraft, setDataPoolImportDraft] = useState<{ poolId: string; file: File; preview: DataPoolImportResult } | null>(null)
  const [dataPoolImportMode, setDataPoolImportMode] = useState<'append' | 'replace'>('append')
  const [dataPoolImportBusy, setDataPoolImportBusy] = useState(false)
  const [dataPoolExportBusy, setDataPoolExportBusy] = useState(false)
  // Concurrency token captured when the editor opens; looking it up at save
  // time would silently refresh past another writer's change.
  const [dataPoolRowDeleteBusy, setDataPoolRowDeleteBusy] = useState(false)
  const dataPoolImportInputRef = useRef<HTMLInputElement | null>(null)
  // Cell saves read the latest row version from here and run one at a time.
  const dataPoolRowsRef = useRef<SharedDataPoolRow[]>([])
  const dataPoolCellQueueRef = useRef<Promise<unknown>>(Promise.resolve())
  const selectedDataPoolIdRef = useRef('')
  const dataPoolCatalogMutationBusyRef = useRef(false)
  const dataPoolRowDeleteBusyRef = useRef(false)
  const dataPoolLoadGenerationRef = useRef(0)
  const dataPoolRowsLoadGenerationRef = useRef(0)
  const activeDataPoolProjectIdRef = useRef(activeProject?.id)
  activeDataPoolProjectIdRef.current = activeProject?.id
  dataPoolRowsRef.current = dataPoolRows
  const dataPoolCreateProjectId = dataPoolProjectId || activeProject?.id || ''
  const dataPoolCreateProject = useMemo(() => {
    if (!dataPoolCreateProjectId) return null
    return projects.find(candidate => candidate.id === dataPoolCreateProjectId)
      || (activeProject?.id === dataPoolCreateProjectId ? activeProject : null)
  }, [activeProject, dataPoolCreateProjectId, projects])

  const loadDataPools = useCallback(async (preferredId?: string): Promise<boolean> => {
    const generation = ++dataPoolLoadGenerationRef.current
    const projectId = activeProject?.id
    try {
      const response = await (preferredId ? api.listDataPools(projectId, true) : api.listDataPools(projectId))
      if (generation !== dataPoolLoadGenerationRef.current || projectId !== activeDataPoolProjectIdRef.current) return false
      const pools: SharedDataPool[] = response.data_pools || []
      setDataPools(pools)
      // Only ids from this response are selectable: a project switch must not
      // carry the previous project's selection into the new project's panel.
      const isMember = (id?: string) => Boolean(id && pools.some(pool => pool.id === id))
      const currentSelection = selectedDataPoolIdRef.current
      const nextSelected = preferredId ? (isMember(preferredId) ? preferredId : '') : isMember(currentSelection) ? currentSelection : (pools[0]?.id || '')
      // Row loading belongs to the selection effect, which carries the
      // cancellation guard; fetching here as well raced it with stale results.
      selectedDataPoolIdRef.current = nextSelected
      setSelectedDataPoolId(nextSelected)
      if (!nextSelected) {
        setDataPoolRows([])
        setDataPoolRowsTotal(0)
        setDataPoolRowsNextCursor(null)
        setDataPoolRowsHasMore(false)
      }
      setDataPoolActionError(preferredId && !isMember(preferredId) ? 'The requested data pool is not available in this project. Choose a pool below.' : '')
      return true
    } catch (error: unknown) {
      if (generation !== dataPoolLoadGenerationRef.current || projectId !== activeDataPoolProjectIdRef.current) return false
      setDataPoolActionError(getErrorMessage(error, 'Unable to load data pools'))
      return false
    }
  }, [activeProject?.id, api])

  useEffect(() => {
    // Never show or retain a selection from the previous project while the
    // newly scoped request is in flight.
    dataPoolLoadGenerationRef.current += 1
    selectedDataPoolIdRef.current = ''
    setDataPools([])
    setSelectedDataPoolId('')
    setDataPoolRows([])
    setDataPoolRowsTotal(0)
    setDataPoolRowsNextCursor(null)
    setDataPoolRowsHasMore(false)
    setDataPoolRowsLoadBusy(false)
    // A create committed just before a scope switch leaves its confirmation as
    // a breadcrumb, because the switch remounts this page (the route boundary
    // is keyed on the hash) and destroys the live notice. Surface it once;
    // otherwise start the newly scoped list with a clean slate.
    const breadcrumb = readSettingsBreadcrumb<string>('apexload.settings.dataPoolNotice')
    setDataPoolActionNotice(breadcrumb || '')
    void loadDataPools(preferredPoolId)
    return () => {
      dataPoolLoadGenerationRef.current += 1
    }
  }, [loadDataPools, preferredPoolId])

  useEffect(() => {
    let cancelled = false
    const generation = ++dataPoolRowsLoadGenerationRef.current
    selectedDataPoolIdRef.current = selectedDataPoolId
    setDataPoolImportDraft(null)
    setDataPoolImportMode('append')
    setDataPoolRows([])
    setDataPoolRowsTotal(0)
    setDataPoolRowsNextCursor(null)
    setDataPoolRowsHasMore(false)
    setDataPoolRowsLoadBusy(Boolean(selectedDataPoolId))
    const loadRows = async () => {
      if (!selectedDataPoolId) {
        setDataPoolRows([])
        setDataPoolRowsTotal(0)
        setDataPoolRowsNextCursor(null)
        setDataPoolRowsHasMore(false)
        return
      }
      try {
        const response = await api.getDataPool(selectedDataPoolId, { limit: DATA_POOL_ROW_PAGE_SIZE, cursor: '' })
        if (!cancelled && generation === dataPoolRowsLoadGenerationRef.current) {
          const rows = response.rows || []
          setDataPoolRows(rows)
          setDataPoolRowsTotal(response.total ?? rows.length)
          setDataPoolRowsNextCursor(response.next_cursor || null)
          setDataPoolRowsHasMore(Boolean(response.next_cursor) && (response.has_more ?? true))
        }
      } catch (error: unknown) {
        if (
          !cancelled
          && selectedDataPoolIdRef.current === selectedDataPoolId
          && generation === dataPoolRowsLoadGenerationRef.current
        ) {
          setDataPoolActionError(getErrorMessage(error, 'Unable to load data pool rows'))
        }
      } finally {
        if (!cancelled && generation === dataPoolRowsLoadGenerationRef.current) {
          setDataPoolRowsLoadBusy(false)
        }
      }
    }
    loadRows()
    return () => {
      cancelled = true
    }
  }, [api, selectedDataPoolId])

  const createDataPool = async (): Promise<boolean> => {
    const name = dataPoolName.trim()
    if (!name) return false
    const projectId = dataPoolProjectId || activeProject?.id
    // Without a project the server would file the pool under the installation's
    // default project, which is not where the operator is looking.
    if (!projectId) {
      setDataPoolActionError('Select a project before creating a data pool.')
      setDataPoolActionNotice('')
      return false
    }
    if (dataPoolCatalogMutationBusyRef.current) return false
    dataPoolCatalogMutationBusyRef.current = true
    setDataPoolCatalogMutationBusy(true)
    setDataPoolActionNotice('')
    try {
      const pool = await api.createDataPool({ name, description: dataPoolDescription.trim(), project_id: projectId })
      // Commit is complete. Clear the bound draft and publish the authoritative
      // row before attempting the best-effort list reconciliation.
      setDataPoolName('')
      setDataPoolDescription('')
      setDataPoolProjectId('')
      if (activeDataPoolProjectIdRef.current === projectId) {
        setDataPools(previous => [pool, ...previous.filter(candidate => candidate.id !== pool.id)])
        selectedDataPoolIdRef.current = pool.id
        setSelectedDataPoolId(pool.id)
      }
      const targetName = projects.find(candidate => candidate.id === projectId)?.name
        || (activeProject?.id === projectId ? activeProject.name : projectId)
      const reloaded = await loadDataPools(pool.id)
      const crossProject = activeDataPoolProjectIdRef.current !== projectId
      // Name the project in every outcome: a guard-save commits here first and
      // the workspace switch that follows remounts this page, so the message
      // is also written as a breadcrumb for the next mount to surface — the
      // pool list only shows the active project, and without this the pool
      // would vanish silently.
      const notice = crossProject
        ? `Created data pool "${pool.name}" in project "${targetName}". Switch to that project to manage it.`
        : reloaded
          ? `Created data pool "${pool.name}" in project "${targetName}".`
          : `Data pool "${pool.name}" was created in project "${targetName}", but the list could not be reloaded. The create succeeded—do not retry. Refresh the list to verify it.`
      writeSettingsBreadcrumb('apexload.settings.dataPoolNotice', notice)
      setDataPoolActionError('')
      setDataPoolActionNotice(notice)
      return true
    } catch (error: unknown) {
      if (activeDataPoolProjectIdRef.current === projectId) {
        setDataPoolActionError(getErrorMessage(error, 'Unable to create data pool'))
      }
      return false
    } finally {
      dataPoolCatalogMutationBusyRef.current = false
      setDataPoolCatalogMutationBusy(false)
    }
  }

  const deleteDataPool = async (pool: SharedDataPool) => {
    if (dataPoolCatalogMutationBusyRef.current) return
    dataPoolCatalogMutationBusyRef.current = true
    setDataPoolCatalogMutationBusy(true)
    const projectId = activeProject?.id
    setConfirmDeletePool(null)
    setDataPoolActionNotice('')
    // A delete supersedes any created-pool breadcrumb; without this a remount
    // within the freshness window would announce a pool that no longer exists.
    clearSettingsBreadcrumb('apexload.settings.dataPoolNotice')
    try {
      await api.deleteDataPool(pool.id)
      const reloaded = await loadDataPools('')
      if (activeDataPoolProjectIdRef.current !== projectId) return
      setDataPoolActionError('')
      setDataPoolActionNotice(reloaded
        ? `Deleted data pool "${pool.name}".`
        : `Data pool "${pool.name}" was deleted, but the list could not be reloaded. The delete succeeded—do not retry. Refresh the list to verify it.`)
    } catch (error: unknown) {
      if (activeDataPoolProjectIdRef.current === projectId) {
        setDataPoolActionError(getErrorMessage(error, 'Unable to delete data pool'))
        // A scenario may have bound it since the confirmation listed its users.
        refreshAssetUsage()
      }
    } finally {
      dataPoolCatalogMutationBusyRef.current = false
      setDataPoolCatalogMutationBusy(false)
    }
  }

  const selectedDataPool = dataPools.find(pool => pool.id === selectedDataPoolId) || null
  // The pool's stored order first, then columns only rows use; columns added
  // in the grid stay visible before any row fills them.
  const dataPoolGridColumns = orderedGridColumns(selectedDataPool?.columns, dataPoolRows.map(row => row.data || {}), true)
  const dataPoolGridRows: DataGridRow[] = dataPoolRows.map(row => {
    const status = (row.status || '').trim()
    return {
      id: row.id,
      values: row.data || {},
      status: INACTIVE_DATA_POOL_ROW_STATUSES.has(status.toLowerCase()) ? `${status}, skipped by runs` : undefined,
      details: `${status || 'active'} · version ${row.version} · updated ${formatDateTime(row.updated_at)}`,
    }
  })

  const replaceDataPoolRow = (updated: SharedDataPoolRow) => {
    dataPoolRowsRef.current = dataPoolRowsRef.current.map(row => row.id === updated.id ? updated : row)
    setDataPoolRows(current => current.map(row => row.id === updated.id ? updated : row))
  }

  // One cell is one row update carrying the version the row was loaded at.
  // Saves are queued so a second edit to the same row sends the version the
  // first one returned instead of conflicting with it.
  // Resolves true when saved, or why it wasn't; the grid shows that reason at
  // the cell, so a failed cell save doesn't also raise the page error.
  const commitDataPoolCell = (gridRow: DataGridRow, column: string, value: string): Promise<boolean | string> => {
    const poolId = selectedDataPoolId
    const save = async (): Promise<boolean | string> => {
      const row = dataPoolRowsRef.current.find(candidate => candidate.id === gridRow.id)
      if (!poolId || !row || selectedDataPoolIdRef.current !== poolId) return false
      setDataPoolActionNotice('')
      try {
        const updated = await api.updateDataPoolRow(poolId, row.id, { data: { ...(row.data || {}), [column]: value }, version: row.version })
        if (selectedDataPoolIdRef.current === poolId) {
          replaceDataPoolRow(updated)
          setDataPoolActionError('')
        }
        return true
      } catch (error: unknown) {
        if (selectedDataPoolIdRef.current !== poolId) return false
        const conflict = error instanceof ApiError && error.status === 409
        const deleted = error instanceof ApiError && error.status === 404
        if (conflict || deleted) {
          try {
            await refreshDataPoolRows(poolId)
          } catch {
            // The failed save stays the message; the list keeps its last state.
          }
        }
        if (deleted) {
          // The row and its cell are gone, so the page says what happened.
          setDataPoolActionError('The row you were editing was deleted, and the list has been reloaded.')
          return false
        }
        return conflict
          ? 'The row changed while you were editing, and the list now shows its latest values; Enter saves your value over them.'
          : getErrorMessage(error, 'The server refused the change.')
      }
    }
    const result = dataPoolCellQueueRef.current.then(save, save)
    dataPoolCellQueueRef.current = result.catch(() => undefined)
    return result
  }

  const addDataPoolRows = async (added: Record<string, string>[]): Promise<boolean> => {
    const poolId = selectedDataPoolId
    if (!poolId || added.length === 0) return false
    const single = added.length === 1
    setDataPoolActionNotice('')
    try {
      if (single) {
        await api.createDataPoolRow(poolId, { data: added[0] })
      } else {
        // Pasted rows go in as one append import: one request and one
        // transaction rather than a request per row.
        const columns = orderedGridColumns(dataPoolGridColumns, added)
        const file = new File([rowsToCSV(columns, added)], 'pasted-rows.csv', { type: 'text/csv' })
        await api.importDataPoolRows(poolId, file, { mode: 'append', delimiter: 'comma' })
      }
    } catch (error: unknown) {
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolActionError(getErrorMessage(error, single ? 'Unable to add the row' : 'Unable to add the pasted rows'))
      }
      return false
    }
    // The rows are saved. A failed reload must not invite adding them again.
    const outcome = single ? 'Row added' : `${added.length} rows added`
    try {
      const refreshed = await refreshDataPoolRows(poolId)
      if (!refreshed) return true
    } catch {
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolActionError('')
        setDataPoolActionNotice(`${outcome}, but the row list could not be reloaded. The save succeeded—do not add ${single ? 'it' : 'them'} again. Refresh the list to verify it.`)
      }
      return true
    }
    if (selectedDataPoolIdRef.current === poolId) {
      setDataPoolActionError('')
      setDataPoolActionNotice(`${outcome}.`)
    }
    return true
  }

  // Columns are the pool's stored order; adding or removing one replaces it.
  const saveDataPoolColumns = async (columns: string[], done: string, failed: string): Promise<boolean> => {
    const pool = selectedDataPool
    if (!pool) return false
    try {
      const updated = await api.updateDataPool(pool.id, { name: pool.name, columns, updated_at: pool.updated_at })
      setDataPools(current => current.map(candidate => candidate.id === updated.id ? updated : candidate))
      setDataPoolActionError('')
      setDataPoolActionNotice(done)
      return true
    } catch (error: unknown) {
      if (selectedDataPoolIdRef.current !== pool.id) return false
      if (error instanceof ApiError && error.status === 409) {
        try {
          await refreshDataPoolRows(pool.id)
        } catch {
          // Reported below either way.
        }
        setDataPoolActionError('This pool changed since it was loaded and has been reloaded. Try again.')
      } else {
        setDataPoolActionError(getErrorMessage(error, failed))
      }
      return false
    }
  }

  const addDataPoolColumn = (name: string) => saveDataPoolColumns([...dataPoolGridColumns, name], `Column ${name} added. Rows leave it unset until you fill it in.`, 'Unable to add the column')
  const removeDataPoolColumn = (name: string) => saveDataPoolColumns(dataPoolGridColumns.filter(column => column !== name), `Column ${name} removed.`, 'Unable to remove the column')

  const refreshDataPoolRows = async (poolId: string): Promise<boolean> => {
    // A late mutation from a pool the user has left must not invalidate the
    // active pool's generation or take ownership of its busy state.
    if (selectedDataPoolIdRef.current !== poolId) return false
    const generation = ++dataPoolRowsLoadGenerationRef.current
    // Reload as many rows as were showing, so a row the user reached through
    // "Load more" (and any value kept in its cells) doesn't vanish.
    const keep = Math.max(DATA_POOL_ROW_PAGE_SIZE, dataPoolRowsRef.current.length)
    setDataPoolRowsLoadBusy(true)
    try {
      let details = await api.getDataPool(poolId, { limit: DATA_POOL_ROW_PAGE_SIZE, cursor: '' })
      const stale = () => selectedDataPoolIdRef.current !== poolId || generation !== dataPoolRowsLoadGenerationRef.current
      if (stale()) return false
      const latest = details.data_pool
      const rows = [...(details.rows || [])]
      // Bounded: one extra page of slack, and stop when a page adds nothing.
      for (let pages = 1; rows.length < keep && details.next_cursor && (details.has_more ?? true) && pages <= Math.ceil(keep / DATA_POOL_ROW_PAGE_SIZE); pages++) {
        details = await api.getDataPool(poolId, { limit: DATA_POOL_ROW_PAGE_SIZE, cursor: details.next_cursor })
        if (stale()) return false
        const seen = new Set(rows.map(row => row.id))
        const fresh = (details.rows || []).filter(row => !seen.has(row.id))
        if (fresh.length === 0) break
        rows.push(...fresh)
      }
      // An import can change the pool's column order; take the server's copy.
      if (latest?.id === poolId) setDataPools(current => current.map(pool => pool.id === poolId ? latest : pool))
      setDataPoolRows(rows)
      setDataPoolRowsTotal(details.total ?? rows.length)
      setDataPoolRowsNextCursor(details.next_cursor || null)
      setDataPoolRowsHasMore(Boolean(details.next_cursor) && (details.has_more ?? true))
      return true
    } finally {
      if (selectedDataPoolIdRef.current === poolId && generation === dataPoolRowsLoadGenerationRef.current) {
        setDataPoolRowsLoadBusy(false)
      }
    }
  }

  const loadMoreDataPoolRows = async () => {
    const poolId = selectedDataPoolId
    if (!poolId || dataPoolRowsLoadBusy || !dataPoolRowsHasMore || !dataPoolRowsNextCursor) return
    const generation = dataPoolRowsLoadGenerationRef.current
    const cursor = dataPoolRowsNextCursor
    setDataPoolRowsLoadBusy(true)
    try {
      const details = await api.getDataPool(poolId, { limit: DATA_POOL_ROW_PAGE_SIZE, cursor })
      if (selectedDataPoolIdRef.current !== poolId || generation !== dataPoolRowsLoadGenerationRef.current) return
      const rows = details.rows || []
      setDataPoolRows(current => {
        const seen = new Set(current.map(row => row.id))
        return [...current, ...rows.filter(row => !seen.has(row.id))]
      })
      setDataPoolRowsTotal(details.total ?? dataPoolRowsTotal)
      setDataPoolRowsNextCursor(details.next_cursor || null)
      setDataPoolRowsHasMore(Boolean(details.next_cursor) && (details.has_more ?? true))
      setDataPoolActionError('')
    } catch (error: unknown) {
      if (selectedDataPoolIdRef.current === poolId && generation === dataPoolRowsLoadGenerationRef.current) {
        if (error instanceof ApiError && error.status === 409) {
          try {
            await refreshDataPoolRows(poolId)
            if (selectedDataPoolIdRef.current === poolId) {
              setDataPoolActionError('')
              setDataPoolActionNotice('Data pool rows changed while the next page was loading. The list was refreshed from the beginning.')
            }
          } catch {
            if (selectedDataPoolIdRef.current === poolId) {
              setDataPoolActionError('Data pool rows changed while the next page was loading, and the automatic refresh failed. Refresh the list before continuing.')
            }
          }
          return
        }
        setDataPoolActionError(getErrorMessage(error, 'Unable to load more data pool rows'))
      }
    } finally {
      if (selectedDataPoolIdRef.current === poolId && generation === dataPoolRowsLoadGenerationRef.current) {
        setDataPoolRowsLoadBusy(false)
      }
    }
  }

  const beginDataPoolImport = async (file: File) => {
    const poolId = selectedDataPoolId
    if (!poolId) return
    setDataPoolImportBusy(true)
    setDataPoolActionNotice('')
    try {
      const preview = await api.importDataPoolRows(poolId, file, { dryRun: true })
      if (selectedDataPoolIdRef.current !== poolId) return
      setDataPoolImportDraft({ poolId, file, preview })
      // append_warning means an append commit is known to be rejected. Start
      // on the safe replacement path while keeping both choices visible.
      setDataPoolImportMode(preview.mode === 'replace' || preview.append_warning ? 'replace' : 'append')
      setDataPoolActionError('')
    } catch (error: unknown) {
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolImportDraft(null)
        setDataPoolActionError(getErrorMessage(error, 'Unable to read the uploaded file'))
      }
    } finally {
      setDataPoolImportBusy(false)
    }
  }

  const cancelDataPoolImport = () => {
    setDataPoolImportDraft(null)
    setDataPoolImportMode('append')
  }

  const confirmDataPoolImport = async () => {
    const draft = dataPoolImportDraft
    if (!draft) return
    const mode = dataPoolImportMode
    const poolName = dataPools.find(candidate => candidate.id === draft.poolId)?.name || draft.poolId
    setDataPoolImportBusy(true)
    try {
      let result: DataPoolImportResult
      try {
        result = await api.importDataPoolRows(draft.poolId, draft.file, { mode })
      } catch (error: unknown) {
        if (selectedDataPoolIdRef.current === draft.poolId) {
          setDataPoolActionError(getErrorMessage(error, `Unable to import rows into "${poolName}"`))
        }
        return
      }
      if (selectedDataPoolIdRef.current === draft.poolId) {
        setDataPoolImportDraft(null)
        setDataPoolImportMode('append')
      }
      try {
        const refreshed = await refreshDataPoolRows(draft.poolId)
        if (!refreshed) return
      } catch {
        if (selectedDataPoolIdRef.current === draft.poolId) {
          setDataPoolActionError('')
          setDataPoolActionNotice(`Imported ${result.imported ?? 0} row(s) in ${mode} mode, but the row list could not be reloaded. The import succeeded—do not import the file again. Refresh the list to verify it.`)
        }
        return
      }
      // Only speak for the pool the user is still looking at; the import
      // itself is done either way.
      if (selectedDataPoolIdRef.current === draft.poolId) {
        setDataPoolActionError('')
        setDataPoolActionNotice(`Imported ${result.imported ?? 0} row(s) in ${mode} mode; the pool now holds ${result.total ?? 0} row(s).`)
      }
    } finally {
      setDataPoolImportBusy(false)
    }
  }

  const exportDataPool = async () => {
    const poolId = selectedDataPoolId
    if (!poolId) return
    const pool = dataPools.find(candidate => candidate.id === poolId)
    setDataPoolExportBusy(true)
    try {
      const { blob, filename } = await api.exportDataPoolRows(poolId)
      const safeName = (pool?.name || 'data-pool').trim().replace(/[^A-Za-z0-9_-]+/g, '-') || 'data-pool'
      const downloadName = filename || `${safeName}.apexload.csv`
      const outcome = await downloadBlob(blob, downloadName)
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolActionError('')
        if (outcome !== 'cancelled') {
          setDataPoolActionNotice(`Exported ${downloadName}.`)
        }
      }
    } catch (error: unknown) {
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolActionError(getErrorMessage(error, `Unable to export data pool "${pool?.name || poolId}"`))
      }
    } finally {
      setDataPoolExportBusy(false)
    }
  }


  const deleteDataPoolRow = async (row: SharedDataPoolRow) => {
    const poolId = row.pool_id
    if (!poolId || dataPoolRowDeleteBusyRef.current) return
    dataPoolRowDeleteBusyRef.current = true
    setDataPoolRowDeleteBusy(true)
    setConfirmDeleteRow(null)
    setDataPoolActionNotice('')
    try {
      try {
        await api.deleteDataPoolRow(poolId, row.id)
      } catch (error: unknown) {
        if (selectedDataPoolIdRef.current === poolId) {
          setDataPoolActionError(getErrorMessage(error, 'Unable to delete data row'))
        }
        return
      }
      try {
        const refreshed = await refreshDataPoolRows(poolId)
        if (!refreshed) return
      } catch {
        if (selectedDataPoolIdRef.current === poolId) {
          setDataPoolActionError('')
          setDataPoolActionNotice('Row deleted, but the row list could not be reloaded. The delete succeeded—do not retry. Refresh the list to verify it.')
        }
        return
      }
      if (selectedDataPoolIdRef.current === poolId) {
        setDataPoolActionError('')
        setDataPoolActionNotice('Row deleted.')
      }
    } finally {
      dataPoolRowDeleteBusyRef.current = false
      setDataPoolRowDeleteBusy(false)
    }
  }

  const listError = dataPoolActionError && !selectedDataPoolId ? dataPoolActionError : ''
  useEffect(() => { onListError?.(listError) }, [listError, onListError])
  const draftDirty = Boolean(dataPoolName.trim() || dataPoolDescription.trim())
  const draftReady = Boolean(dataPoolName.trim()) && Boolean(dataPoolCreateProjectId)
  useEffect(() => { onDraftChange?.({ dirty: draftDirty, ready: draftReady }) }, [draftDirty, draftReady, onDraftChange])
  useImperativeHandle(ref, () => ({
    save: async () => !draftDirty || createDataPool(),
    discard: () => {
      setDataPoolName('')
      setDataPoolDescription('')
      setDataPoolProjectId('')
    },
  }))

  return <>
    <div className="settings-group">
      {canManage ? <div className="settings-subsection">
        <div className="settings-manager-summary">
          <div>
            <span className="setting-label">Create a data pool</span>
            <span className="setting-desc">A data pool is a table of parameter values that scenarios bind by alias, so several scripts can share the same data.</span>
          </div>
        </div>
        <div className="inline-create-row" style={{ gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr) auto' }}>
          <input
            className="settings-input"
            value={dataPoolName}
            disabled={dataPoolCatalogMutationBusy}
            onChange={event => {
              const next = event.target.value
              if (!dataPoolName.trim() && !dataPoolDescription.trim() && next.trim()) setDataPoolProjectId(activeProject?.id || '')
              if (!next.trim() && !dataPoolDescription.trim()) setDataPoolProjectId('')
              setDataPoolName(next)
            }}
            placeholder="Pool name, e.g. Customer Accounts"
          />
          <input
            className="settings-input"
            value={dataPoolDescription}
            disabled={dataPoolCatalogMutationBusy}
            onChange={event => {
              const next = event.target.value
              if (!dataPoolName.trim() && !dataPoolDescription.trim() && next.trim()) setDataPoolProjectId(activeProject?.id || '')
              if (!next.trim() && !dataPoolName.trim()) setDataPoolProjectId('')
              setDataPoolDescription(next)
            }}
            placeholder="Optional description"
            aria-label="Pool description"
          />
          <button className="btn btn-secondary btn-sm" type="button" onClick={createDataPool} disabled={!dataPoolName.trim() || dataPoolCatalogMutationBusy || !dataPoolCreateProjectId}>
            Add pool
          </button>
        </div>
        {dataPoolCreateProjectId ? (
          <p className="new-test-inline-note">{`Creates in project "${dataPoolCreateProject?.name || dataPoolCreateProjectId}".`}</p>
        ) : noProjectHint ?? <p className="new-test-inline-note">Select a project to create data pools.</p>}
      </div> : <p className="settings-notice" role="note">You can view and export these pools. Editors and project admins can create and change them.</p>}

      <div className="settings-subsection">
        <div className="settings-manager-summary">
          <div>
            <span className="setting-label">Data pools</span>
            <span className="setting-desc">Select a pool to see and edit its rows.</span>
          </div>
          <strong>{`${dataPools.length} ${dataPools.length === 1 ? 'pool' : 'pools'}`}</strong>
        </div>
        {dataPools.length > 0 ? (
          <div className="settings-release-list">
            {dataPools.map(pool => (
              <div key={pool.id} className={`settings-release-row ${selectedDataPoolId === pool.id ? 'active' : ''}`}>
                <button
                  type="button"
                  className="settings-release-main"
                  onClick={() => {
                    setDataPoolActionNotice('')
                    clearSettingsBreadcrumb('apexload.settings.dataPoolNotice')
                    selectedDataPoolIdRef.current = pool.id
                    setSelectedDataPoolId(pool.id)
                  }}
                >
                  <span>{pool.name}</span>
                  <small>{pool.description || `Updated ${new Date(pool.updated_at).toLocaleDateString()}`}{poolUses(pool.id) && ` · ${usedByCount(poolUses(pool.id)!.length)}`}</small>
                </button>
                {canManage && <div className="settings-inline-actions">
                  <button className="btn btn-ghost" style={{ color: 'var(--danger-text)' }} type="button" disabled={dataPoolCatalogMutationBusy} onClick={() => { refreshAssetUsage(); setConfirmDeletePool(pool) }}>
                    Delete
                  </button>
                </div>}
              </div>
            ))}
          </div>
        ) : (
          <p className="new-test-inline-note">{canManage ? 'No data pools have been created yet. Use the creation form above to name a pool, then add or import its rows.' : 'No data pools exist in this project yet.'}</p>
        )}
      </div>

      {!onListError && listError && <p className="settings-error-inline" role="alert">{listError}</p>}
      {dataPoolActionNotice && <p className="settings-notice" role="status" aria-live="polite">{dataPoolActionNotice}</p>}
      {assetUsageError && <p className="new-test-inline-note" role="status">{assetUsageError}</p>}

      {selectedDataPoolId && (
        <div className="settings-subsection">
          <div className="settings-manager-summary">
            <div>
              <span className="setting-label">{selectedDataPool ? `Rows of ${selectedDataPool.name}` : 'Rows'}</span>
              <span className="setting-desc">{canManage ? 'Each column is a parameter that scripts read by name. Edit a cell and press Enter, or move to another cell, to save it.' : 'Each column is a parameter that scripts read by name.'}</span>
              {activeProject && poolUses(selectedDataPoolId) && <SharedAssetUsedBy kind="scenario" projectId={activeProject.id} uses={poolUses(selectedDataPoolId)!} none="No scenario binds this pool yet." />}
            </div>
            <strong>
              {dataPoolRowsTotal > dataPoolRows.length
                ? `Showing ${dataPoolRows.length} of ${dataPoolRowsTotal} rows`
                : `${dataPoolRowsTotal} ${dataPoolRowsTotal === 1 ? 'row' : 'rows'}`}
            </strong>
          </div>
          <div className="settings-inline-actions">
            <input
              ref={dataPoolImportInputRef}
              type="file"
              accept=".csv,.dat,.txt,.tsv"
              style={{ display: 'none' }}
              aria-label="Data pool CSV file"
              onChange={event => {
                const file = event.target.files?.[0]
                if (file) void beginDataPoolImport(file)
                event.target.value = ''
              }}
            />
            {canManage && <button className="btn btn-secondary" type="button" disabled={dataPoolImportBusy} onClick={() => dataPoolImportInputRef.current?.click()}>
              {dataPoolImportBusy && !dataPoolImportDraft ? 'Reading…' : 'Import CSV…'}
            </button>}
            <button className="btn btn-secondary" type="button" disabled={dataPoolExportBusy || dataPoolRowsTotal === 0} onClick={() => { void exportDataPool() }}>
              {dataPoolExportBusy ? 'Exporting…' : 'Export CSV'}
            </button>
          </div>
          {dataPoolActionError && <p className="settings-error-inline" role="alert">{dataPoolActionError.charAt(0).toUpperCase() + dataPoolActionError.slice(1)}</p>}
          {dataPoolImportDraft && dataPoolImportDraft.poolId === selectedDataPoolId && (
            <div className="settings-subsection" style={{ marginTop: 12 }}>
              <div className="settings-manager-summary">
                <div>
                  <span className="setting-label">Import preview</span>
                  <span className="setting-desc" role="status">
                    {dataPoolImportDraft.file.name} — {dataPoolImportDraft.preview.row_count ?? 0} row(s), columns: {(dataPoolImportDraft.preview.columns || []).join(', ') || 'none'}
                  </span>
                </div>
                <strong>Dry run</strong>
              </div>
              {(dataPoolImportDraft.preview.warnings || []).map(warning => (
                <p key={warning} className="new-test-inline-note">{warning}</p>
              ))}
              {dataPoolImportMode === 'replace' && dataPoolRowsTotal > 0 && (
                <p className="new-test-inline-note" role="alert">
                  Replace will first delete the {dataPoolRowsTotal} existing row(s) in this pool.
                </p>
              )}
              {dataPoolImportDraft.preview.append_warning && (
                <p className="new-test-inline-note" role="alert">{dataPoolImportDraft.preview.append_warning}</p>
              )}
              {(dataPoolImportDraft.preview.sample_rows || []).length > 0 && (
                <div className="script-parameter-table-wrap">
                  <table className="script-parameter-table">
                    <thead>
                      <tr>
                        {(dataPoolImportDraft.preview.columns || []).map(column => <th key={column}>{column}</th>)}
                      </tr>
                    </thead>
                    <tbody>
                      {(dataPoolImportDraft.preview.sample_rows || []).map((sample, index) => (
                        <tr key={index}>
                          {(dataPoolImportDraft.preview.columns || []).map(column => (
                            <td key={column} className="cell-mono">{sample[column]}</td>
                          ))}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              <div className="settings-inline-actions" style={{ marginTop: 8 }}>
                <select
                  className="settings-input"
                  value={dataPoolImportMode}
                  disabled={dataPoolImportBusy}
                  onChange={event => setDataPoolImportMode(event.target.value === 'replace' ? 'replace' : 'append')}
                  aria-label="Import mode"
                >
                  <option value="append">Append to existing rows</option>
                  <option value="replace">Replace all rows</option>
                </select>
                <button className="btn btn-primary" type="button" disabled={dataPoolImportBusy} onClick={() => { void confirmDataPoolImport() }}>
                  {dataPoolImportBusy
                    ? 'Importing…'
                    : dataPoolImportMode === 'replace'
                      ? `Replace with ${dataPoolImportDraft.preview.row_count ?? 0} row(s)`
                      : `Append ${dataPoolImportDraft.preview.row_count ?? 0} row(s)`}
                </button>
                <button className="btn btn-secondary" type="button" disabled={dataPoolImportBusy} onClick={cancelDataPoolImport}>
                  Cancel
                </button>
              </div>
            </div>
          )}

          <div style={{ marginTop: 16 }}>
            <DataRowGrid
              key={selectedDataPoolId}
              label={selectedDataPool ? `Rows of ${selectedDataPool.name}` : 'Data pool rows'}
              columns={dataPoolGridColumns}
              rows={dataPoolGridRows}
              disabled={dataPoolRowDeleteBusy || dataPoolImportBusy}
              detailsLabel="Status, version and last update"
              maxColumns={DATA_POOL_MAX_COLUMNS}
              emptyMessage={dataPoolRowsLoadBusy ? 'Loading rows…' : canManage ? 'This pool has no columns yet. Add a column for each parameter, or import a CSV file.' : 'This pool has no columns yet.'}
              // Without the role to change pools, the grid only shows them.
              onCellCommit={canManage ? commitDataPoolCell : undefined}
              onAddRows={canManage ? addDataPoolRows : undefined}
              onAddColumn={canManage ? addDataPoolColumn : undefined}
              // An unused column can go only once every row is loaded.
              onRemoveColumn={!canManage || dataPoolRowsHasMore ? undefined : removeDataPoolColumn}
              rowActions={!canManage ? undefined : (gridRow, rowNumber) => (
                <button
                  className="btn btn-sm btn-ghost"
                  style={{ color: 'var(--danger-text)' }}
                  type="button"
                  aria-label={`Delete row ${rowNumber}`}
                  disabled={dataPoolRowDeleteBusy}
                  onClick={() => setConfirmDeleteRow(dataPoolRows.find(row => row.id === gridRow.id) || null)}
                >
                  Delete
                </button>
              )}
            />
            {dataPoolRowsHasMore && dataPoolRowsNextCursor && (
              <div className="settings-inline-actions" style={{ marginTop: 12 }}>
                <button
                  className="btn btn-secondary"
                  type="button"
                  disabled={dataPoolRowsLoadBusy}
                  onClick={() => { void loadMoreDataPoolRows() }}
                >
                  {dataPoolRowsLoadBusy
                    ? 'Loading rows…'
                    : `Load more rows (${Math.max(dataPoolRowsTotal - dataPoolRows.length, 0)} remaining)`}
                </button>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
    <SettingsConfirmDialog
      open={Boolean(confirmDeletePool)}
      title="Delete data pool"
      confirmLabel="Delete data pool"
      confirmDisabled={dataPoolCatalogMutationBusy || Boolean(confirmDeletePool && poolUses(confirmDeletePool.id)?.length)}
      onCancel={() => setConfirmDeletePool(null)}
      onConfirm={() => {
        if (confirmDeletePool) void deleteDataPool(confirmDeletePool)
      }}
    >
      {confirmDeletePool && activeProject && poolUses(confirmDeletePool.id)?.length
        ? <>
          <p><strong>{confirmDeletePool.name}</strong> cannot be deleted while scenarios bind it. Open each scenario's Data step and remove the binding first.</p>
          <SharedAssetUsedBy kind="scenario" label="Bound by" projectId={activeProject.id} uses={poolUses(confirmDeletePool.id)!} none="" />
        </>
        : <>
          <p>Delete data pool <strong>{confirmDeletePool?.name}</strong>? {confirmDeletePool && poolUses(confirmDeletePool.id) ? 'No scenario binds it.' : 'Data pools bound to scenarios must be unbound first.'}</p>
          <p style={{ color: 'var(--danger-text)', fontWeight: 600 }}>This action cannot be undone.</p>
        </>}
    </SettingsConfirmDialog>

    <SettingsConfirmDialog
      open={Boolean(confirmDeleteRow)}
      title="Delete row"
      confirmLabel="Delete row"
      confirmDisabled={dataPoolRowDeleteBusy}
      onCancel={() => setConfirmDeleteRow(null)}
      onConfirm={() => {
        if (confirmDeleteRow) void deleteDataPoolRow(confirmDeleteRow)
      }}
    >
      <p>Permanently delete this data row?</p>
    </SettingsConfirmDialog>
  </>
})

export default DataPoolsManager
