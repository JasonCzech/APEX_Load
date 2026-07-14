import { type Dispatch, type SetStateAction, useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useLibraryApi } from '../hooks/api/useLibraryApi'
import {
  LibraryCatalogItem,
  LibraryKind,
  LibraryRemote,
  LibrarySourceFilter,
} from '../types/library'
import { getErrorMessage } from '../utils/errorUtils'
import {
  hostLibraryHref,
  libraryTransferSummary,
  remoteCredentialReady,
  shortLibraryHash,
} from './libraryFederationUtils'

export type LibraryAssetSourceKind = 'Local' | 'Remote'

export const LIBRARY_SOURCE_OPTIONS: Array<{ value: LibrarySourceFilter; label: string }> = [
  { value: 'all', label: 'All' },
  { value: 'local', label: 'Local' },
  { value: 'host', label: 'Remote Workspace' },
]

const EMPTY_LIBRARY_CATALOG_ITEMS: LibraryCatalogItem[] = []

function transferProjectMessage(projectMapping: string, remote?: LibraryRemote) {
  if (projectMapping.trim() || remote?.default_project_id) return ''
  return 'Set a project mapping before transferring assets.'
}

type UseLibraryFederationOptions = {
  kind: LibraryKind
  projectId?: string
  onRefreshLocal?: () => void | Promise<void>
  requireProjectScope?: boolean
}

export type UseLibraryFederationResult = ReturnType<typeof useLibraryFederation>

export function useLibraryFederation({ kind, projectId, onRefreshLocal, requireProjectScope = false }: UseLibraryFederationOptions) {
  const libraryApi = useLibraryApi()
  const [remotes, setRemotes] = useState<LibraryRemote[]>([])
  const [selectedRemoteId, setSelectedRemoteId] = useState('')
  const [sourceFilter, setSourceFilter] = useState<LibrarySourceFilter>('all')
  const [projectMappingState, setProjectMappingState] = useState({ projectId, value: projectId || '' })
  const [includeDependencies, setIncludeDependencies] = useState(true)
  const [catalogState, setCatalogState] = useState<{
    scopeKey: string
    localItems: LibraryCatalogItem[]
    remoteItems: LibraryCatalogItem[]
  }>({ scopeKey: '', localItems: [], remoteItems: [] })
  const [loadingScopeKey, setLoadingScopeKey] = useState('')
  const [actionMessage, setActionMessage] = useState('')
  const [actionError, setActionError] = useState('')
  const [diffMessage, setDiffMessage] = useState('')
  const mountedRef = useRef(true)
  const remotesRequestGenerationRef = useRef(0)
  const catalogRequestGenerationRef = useRef(0)

  // A mapping entered by the user belongs to the project that was active when
  // it was entered. Deriving the next project's mapping during render avoids a
  // one-render window where project B could still issue requests for project A.
  const projectMapping = projectMappingState.projectId === projectId
    ? projectMappingState.value
    : projectId || ''
  const setProjectMapping = useCallback<Dispatch<SetStateAction<string>>>((nextValue) => {
    setProjectMappingState(current => {
      const currentValue = current.projectId === projectId ? current.value : projectId || ''
      return {
        projectId,
        value: typeof nextValue === 'function' ? nextValue(currentValue) : nextValue,
      }
    })
  }, [projectId])

  const selectedRemote = useMemo(
    () => remotes.find(remote => remote.id === selectedRemoteId),
    [remotes, selectedRemoteId],
  )

  const effectiveProjectID = projectMapping.trim() || selectedRemote?.default_project_id || ''
  const catalogScopeReady = !requireProjectScope || Boolean(projectId?.trim() || projectMapping.trim())
  const catalogScopeKey = `${kind}\u0000${catalogScopeReady ? 'ready' : 'pending'}\u0000${effectiveProjectID}\u0000${selectedRemoteId}`
  const catalogScopeKeyRef = useRef(catalogScopeKey)
  catalogScopeKeyRef.current = catalogScopeKey
  const localItems = catalogState.scopeKey === catalogScopeKey ? catalogState.localItems : EMPTY_LIBRARY_CATALOG_ITEMS
  const remoteItems = catalogState.scopeKey === catalogScopeKey ? catalogState.remoteItems : EMPTY_LIBRARY_CATALOG_ITEMS
  const loading = loadingScopeKey === catalogScopeKey
  const projectBlockMessage = catalogScopeReady
    ? transferProjectMessage(projectMapping, selectedRemote)
    : 'Set a project mapping before transferring assets.'
  const credentialsReady = remoteCredentialReady(selectedRemote)

  const loadRemotes = useCallback(async () => {
    const requestGeneration = ++remotesRequestGenerationRef.current
    const response = await libraryApi.listRemotes()
    if (!mountedRef.current || requestGeneration !== remotesRequestGenerationRef.current) return
    const nextRemotes = response.remotes || []
    setRemotes(nextRemotes)
    setSelectedRemoteId(current => {
      if (current && nextRemotes.some(remote => remote.id === current)) return current
      return nextRemotes[0]?.id || ''
    })
  }, [libraryApi])

  const refreshCatalogs = useCallback(async () => {
    const requestGeneration = ++catalogRequestGenerationRef.current
    const requestScopeKey = catalogScopeKey
    setActionError('')
    if (!catalogScopeReady || (requireProjectScope && !effectiveProjectID)) {
      if (catalogScopeKeyRef.current === requestScopeKey) {
        setCatalogState({ scopeKey: requestScopeKey, localItems: [], remoteItems: [] })
        setLoadingScopeKey('')
      }
      return
    }
    setLoadingScopeKey(requestScopeKey)
    try {
      const [localResponse, remoteResponse] = await Promise.all([
        libraryApi.localCatalog(kind, effectiveProjectID),
        selectedRemoteId
          ? libraryApi.remoteCatalog(selectedRemoteId, kind, effectiveProjectID)
          : Promise.resolve(null),
      ])
      if (
        !mountedRef.current
        || requestGeneration !== catalogRequestGenerationRef.current
        || catalogScopeKeyRef.current !== requestScopeKey
      ) return
      setCatalogState({
        scopeKey: requestScopeKey,
        localItems: localResponse.items || [],
        remoteItems: remoteResponse?.catalog.items || [],
      })
    } catch (error) {
      if (
        mountedRef.current
        && requestGeneration === catalogRequestGenerationRef.current
        && catalogScopeKeyRef.current === requestScopeKey
      ) {
        setActionError(getErrorMessage(error, 'Unable to load library catalogs'))
      }
    } finally {
      if (
        mountedRef.current
        && requestGeneration === catalogRequestGenerationRef.current
        && catalogScopeKeyRef.current === requestScopeKey
      ) {
        setLoadingScopeKey(current => current === requestScopeKey ? '' : current)
      }
    }
  }, [catalogScopeKey, catalogScopeReady, effectiveProjectID, kind, libraryApi, requireProjectScope, selectedRemoteId])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      remotesRequestGenerationRef.current += 1
      catalogRequestGenerationRef.current += 1
    }
  }, [])

  useEffect(() => {
    setProjectMappingState(current => {
      const nextValue = projectId || ''
      return current.projectId === projectId && current.value === nextValue
        ? current
        : { projectId, value: nextValue }
    })
    setCatalogState({ scopeKey: '', localItems: [], remoteItems: [] })
    setLoadingScopeKey('')
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
  }, [projectId])

  useEffect(() => {
    void loadRemotes().catch(error => {
      if (mountedRef.current) {
        setActionError(getErrorMessage(error, 'Unable to load Remote Workspaces'))
      }
    })
  }, [loadRemotes])

  useEffect(() => {
    void refreshCatalogs()
  }, [refreshCatalogs])

  useEffect(() => {
    setActionMessage('')
    setDiffMessage('')
  }, [catalogScopeKey])

  const localStatusById = useMemo(() => {
    const map = new Map<string, string>()
    remoteItems.forEach(item => {
      if (item.local_item_id) {
        map.set(item.local_item_id, item.sync_status || 'synced')
      }
    })
    return map
  }, [remoteItems])

  const hostByLocalID = useMemo(() => {
    const map = new Map<string, LibraryCatalogItem>()
    remoteItems.forEach(item => {
      if (item.local_item_id) {
        map.set(item.local_item_id, item)
      }
    })
    return map
  }, [remoteItems])

  const actionBlocked = !selectedRemoteId || !credentialsReady || Boolean(projectBlockMessage)
  const actionBlockedReason = !selectedRemoteId
    ? 'Select a Remote Workspace first.'
    : !credentialsReady
      ? 'Configure remote credentials in Settings first.'
      : projectBlockMessage

  const refreshAfterTransfer = useCallback(async (actionScopeKey: string) => {
    await refreshCatalogs()
    if (!mountedRef.current || catalogScopeKeyRef.current !== actionScopeKey) return
    await onRefreshLocal?.()
  }, [onRefreshLocal, refreshCatalogs])

  const pullRemoteItem = useCallback(async (item: LibraryCatalogItem) => {
    if (actionBlocked) {
      setActionError(actionBlockedReason)
      return
    }
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
    const actionScopeKey = catalogScopeKey
    try {
      const response = await libraryApi.pullRemoteItem(selectedRemoteId, {
        kind: item.kind,
        id: item.remote_item_id || item.id,
        remote_id: item.remote_item_id || item.id,
        project_id: effectiveProjectID,
        include_dependencies: includeDependencies,
      })
      if (!mountedRef.current || catalogScopeKeyRef.current !== actionScopeKey) return
      setActionMessage(libraryTransferSummary('pull', response))
      await refreshAfterTransfer(actionScopeKey)
    } catch (error) {
      if (mountedRef.current && catalogScopeKeyRef.current === actionScopeKey) {
        setActionError(getErrorMessage(error, 'Unable to pull remote item'))
      }
    }
  }, [actionBlocked, actionBlockedReason, catalogScopeKey, effectiveProjectID, includeDependencies, libraryApi, refreshAfterTransfer, selectedRemoteId])

  const pushLocalItem = useCallback(async (item: LibraryCatalogItem) => {
    if (actionBlocked) {
      setActionError(actionBlockedReason)
      return
    }
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
    const actionScopeKey = catalogScopeKey
    try {
      const response = await libraryApi.pushLocalItem(selectedRemoteId, {
        kind: item.kind,
        id: item.id,
        local_id: item.id,
        project_id: effectiveProjectID,
        include_dependencies: includeDependencies,
      })
      if (!mountedRef.current || catalogScopeKeyRef.current !== actionScopeKey) return
      setActionMessage(libraryTransferSummary('push', response))
      await refreshAfterTransfer(actionScopeKey)
    } catch (error) {
      if (mountedRef.current && catalogScopeKeyRef.current === actionScopeKey) {
        setActionError(getErrorMessage(error, 'Unable to push local item'))
      }
    }
  }, [actionBlocked, actionBlockedReason, catalogScopeKey, effectiveProjectID, includeDependencies, libraryApi, refreshAfterTransfer, selectedRemoteId])

  const compareItems = useCallback(async (localItemID: string, remoteItem: LibraryCatalogItem) => {
    if (!selectedRemoteId) return
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
    const actionScopeKey = catalogScopeKey
    try {
      const diff = await libraryApi.compareItems(selectedRemoteId, {
        kind: remoteItem.kind,
        id: remoteItem.remote_item_id || remoteItem.id,
        local_id: localItemID,
        remote_id: remoteItem.remote_item_id || remoteItem.id,
        project_id: effectiveProjectID,
      })
      if (!mountedRef.current || catalogScopeKeyRef.current !== actionScopeKey) return
      setDiffMessage(`${diff.kind} ${diff.status}: local ${shortLibraryHash(diff.local_hash)} / host ${shortLibraryHash(diff.remote_hash)}`)
    } catch (error) {
      if (mountedRef.current && catalogScopeKeyRef.current === actionScopeKey) {
        setActionError(getErrorMessage(error, 'Unable to compare items'))
      }
    }
  }, [catalogScopeKey, effectiveProjectID, libraryApi, selectedRemoteId])

  const hostItemForLocal = useCallback(
    (localId: string) => hostByLocalID.get(localId),
    [hostByLocalID],
  )

  const syncStatusForLocal = useCallback(
    (localId: string) => localStatusById.get(localId) || 'local only',
    [localStatusById],
  )

  const remoteItemHref = useCallback(
    (item: LibraryCatalogItem) => hostLibraryHref(selectedRemote, item),
    [selectedRemote],
  )

  return {
    remotes,
    selectedRemoteId,
    setSelectedRemoteId,
    sourceFilter,
    setSourceFilter,
    projectMapping,
    setProjectMapping,
    includeDependencies,
    setIncludeDependencies,
    localItems,
    remoteItems,
    loading,
    actionMessage,
    actionError,
    diffMessage,
    selectedRemote,
    credentialsReady,
    effectiveProjectID,
    actionBlocked,
    actionBlockedReason,
    refreshCatalogs,
    pullRemoteItem,
    pushLocalItem,
    compareItems,
    hostItemForLocal,
    syncStatusForLocal,
    remoteItemHref,
  }
}
