import { type Dispatch, type SetStateAction, useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useLibraryApi } from '../hooks/api/useLibraryApi'
import {
  LibraryCatalogPageOptions,
  LibraryCatalogResponse,
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
const LIBRARY_CATALOG_PAGE_SIZE = 100
const LIBRARY_CATALOG_MAX_OFFSET = 10_000
const LIBRARY_CATALOG_MAX_ITEMS = LIBRARY_CATALOG_MAX_OFFSET + LIBRARY_CATALOG_PAGE_SIZE
const LIBRARY_CATALOG_MAX_PAGES = 250

type LibraryCatalogPageLoader = (page: LibraryCatalogPageOptions) => Promise<LibraryCatalogResponse>

function catalogItemIdentity(item: LibraryCatalogItem) {
  const sourceItemID = item.source_item_id?.trim() || item.remote_item_id?.trim() || item.id.trim()
  return `${item.kind}\u0000${item.source_instance_id?.trim() || ''}\u0000${sourceItemID}`
}

function catalogLoadAborted(signal: AbortSignal) {
  if (signal.aborted) throw new Error('Library catalog load was cancelled')
}

async function loadCompleteCatalog(loadPage: LibraryCatalogPageLoader, signal: AbortSignal) {
  const itemsByIdentity = new Map<string, LibraryCatalogItem>()
  let offset = 0
  let expectedTotal: number | undefined

  for (let pageNumber = 0; pageNumber < LIBRARY_CATALOG_MAX_PAGES; pageNumber += 1) {
    catalogLoadAborted(signal)
    if (offset > LIBRARY_CATALOG_MAX_OFFSET) {
      throw new Error(`Library catalog exceeds the supported ${LIBRARY_CATALOG_MAX_ITEMS}-item window`)
    }

    const response = await loadPage({ limit: LIBRARY_CATALOG_PAGE_SIZE, offset, signal })
    catalogLoadAborted(signal)
    const items = response.items || []
    const responseOffset = response.offset ?? offset
    if (!Number.isInteger(responseOffset) || responseOffset !== offset) {
      throw new Error(`Library catalog returned offset ${responseOffset}; expected ${offset}`)
    }
    if (response.count !== undefined && response.count !== items.length) {
      throw new Error(`Library catalog returned ${items.length} items but reported count ${response.count}`)
    }
    if (response.total !== undefined) {
      if (!Number.isInteger(response.total) || response.total < 0) {
        throw new Error('Library catalog returned an invalid total')
      }
      if (response.total > LIBRARY_CATALOG_MAX_ITEMS) {
        throw new Error(`Library catalog exceeds the supported ${LIBRARY_CATALOG_MAX_ITEMS}-item window`)
      }
      if (expectedTotal !== undefined && expectedTotal !== response.total) {
        throw new Error(`Library catalog total changed from ${expectedTotal} to ${response.total} during pagination`)
      }
      expectedTotal = response.total
    }

    items.forEach(item => itemsByIdentity.set(catalogItemIdentity(item), item))
    const nextOffset = responseOffset + items.length
    if (expectedTotal !== undefined && nextOffset > expectedTotal) {
      throw new Error(`Library catalog advanced to ${nextOffset} past its reported total ${expectedTotal}`)
    }
    const totalHasMore = expectedTotal === undefined ? undefined : nextOffset < expectedTotal
    if (response.has_more !== undefined && totalHasMore !== undefined && response.has_more !== totalHasMore) {
      throw new Error('Library catalog returned inconsistent pagination metadata')
    }
    const hasMore = response.has_more ?? totalHasMore ?? false
    if (!hasMore) return Array.from(itemsByIdentity.values())
    if (items.length === 0) {
      throw new Error(`Library catalog pagination did not advance from offset ${offset}`)
    }
    if (nextOffset > LIBRARY_CATALOG_MAX_OFFSET) {
      throw new Error(`Library catalog exceeds the supported ${LIBRARY_CATALOG_MAX_ITEMS}-item window`)
    }
    offset = nextOffset
  }

  throw new Error(`Library catalog exceeded the ${LIBRARY_CATALOG_MAX_PAGES}-page safety limit`)
}

function transferProjectMessage(projectMapping: string, remote?: LibraryRemote) {
  const localProjectID = projectMapping.trim()
  if (!localProjectID) return 'Select a local project before transferring assets.'
  if (remote?.project_mappings?.[localProjectID]) return ''
  return `Ask an administrator to map local project ${localProjectID} for this Remote Workspace.`
}

type UseLibraryFederationOptions = {
  kind: LibraryKind
  projectId?: string
  applicationId?: string
  releaseId?: string
  onRefreshLocal?: () => void | Promise<void>
  requireProjectScope?: boolean
}

export type LibraryResultDestination = {
  applicationId?: string
  releaseId?: string
}

export type UseLibraryFederationResult = ReturnType<typeof useLibraryFederation>

export function useLibraryFederation({ kind, projectId, applicationId, releaseId, onRefreshLocal, requireProjectScope = false }: UseLibraryFederationOptions) {
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
  const catalogAbortControllerRef = useRef<AbortController | null>(null)

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

  const effectiveProjectID = projectMapping.trim() || projectId?.trim() || ''
  const mappedRemoteProjectID = selectedRemote?.project_mappings?.[effectiveProjectID] || ''
  const catalogScopeReady = !requireProjectScope || Boolean(projectId?.trim() || projectMapping.trim())
  const catalogScopeKey = `${kind}\u0000${catalogScopeReady ? 'ready' : 'pending'}\u0000${effectiveProjectID}\u0000${mappedRemoteProjectID}\u0000${applicationId || ''}\u0000${releaseId || ''}\u0000${selectedRemoteId}`
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
    catalogAbortControllerRef.current?.abort()
    const abortController = new AbortController()
    catalogAbortControllerRef.current = abortController
    setActionError('')
    if (!catalogScopeReady || (requireProjectScope && !effectiveProjectID)) {
      catalogAbortControllerRef.current = null
      if (catalogScopeKeyRef.current === requestScopeKey) {
        setCatalogState({ scopeKey: requestScopeKey, localItems: [], remoteItems: [] })
        setLoadingScopeKey('')
      }
      return
    }
    setLoadingScopeKey(requestScopeKey)
    try {
      const [localItems, remoteItems] = await Promise.all([
        loadCompleteCatalog(
          page => libraryApi.localCatalog(kind, effectiveProjectID, page),
          abortController.signal,
        ),
        selectedRemoteId && mappedRemoteProjectID
          ? loadCompleteCatalog(
              async page => (await libraryApi.remoteCatalog(selectedRemoteId, kind, effectiveProjectID, page)).catalog,
              abortController.signal,
            )
          : Promise.resolve([]),
      ])
      if (
        !mountedRef.current
        || abortController.signal.aborted
        || requestGeneration !== catalogRequestGenerationRef.current
        || catalogScopeKeyRef.current !== requestScopeKey
      ) return
      setCatalogState({
        scopeKey: requestScopeKey,
        localItems,
        remoteItems,
      })
    } catch (error) {
      const requestIsCurrent = (
        mountedRef.current
        && !abortController.signal.aborted
        && requestGeneration === catalogRequestGenerationRef.current
        && catalogScopeKeyRef.current === requestScopeKey
      )
      if (requestIsCurrent) {
        abortController.abort()
        setActionError(getErrorMessage(error, 'Unable to load library catalogs'))
      }
    } finally {
      if (
        mountedRef.current
        && requestGeneration === catalogRequestGenerationRef.current
        && catalogScopeKeyRef.current === requestScopeKey
      ) {
        if (catalogAbortControllerRef.current === abortController) {
          catalogAbortControllerRef.current = null
        }
        setLoadingScopeKey(current => current === requestScopeKey ? '' : current)
      }
    }
  }, [catalogScopeKey, catalogScopeReady, effectiveProjectID, kind, libraryApi, mappedRemoteProjectID, requireProjectScope, selectedRemoteId])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      remotesRequestGenerationRef.current += 1
      catalogRequestGenerationRef.current += 1
      catalogAbortControllerRef.current?.abort()
      catalogAbortControllerRef.current = null
    }
  }, [])

  useEffect(() => {
    catalogRequestGenerationRef.current += 1
    catalogAbortControllerRef.current?.abort()
    catalogAbortControllerRef.current = null
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

  const baseActionBlockedReason = !selectedRemoteId
    ? 'Select a Remote Workspace first.'
    : !credentialsReady
      ? 'Configure remote credentials in Settings first.'
      : projectBlockMessage

  const transferBlockReasonForItem = useCallback((item?: LibraryCatalogItem, destination?: LibraryResultDestination) => {
    if (baseActionBlockedReason) return baseActionBlockedReason
    if (kind !== 'result') return ''

    const localApplicationID = destination?.applicationId?.trim()
      || applicationId?.trim()
      || item?.application_id?.trim()
      || ''
    const localReleaseID = destination?.releaseId?.trim()
      || releaseId?.trim()
      || item?.release_id?.trim()
      || ''
    if (localApplicationID && !selectedRemote?.application_mappings?.[localApplicationID]) {
      return `Ask an administrator to map local application ${localApplicationID} for this Remote Workspace.`
    }
    if (localReleaseID && !selectedRemote?.release_mappings?.[localReleaseID]) {
      return `Ask an administrator to map local release ${localReleaseID} for this Remote Workspace.`
    }
    return ''
  }, [applicationId, baseActionBlockedReason, kind, releaseId, selectedRemote?.application_mappings, selectedRemote?.release_mappings])

  const actionBlockedReason = transferBlockReasonForItem()
  const actionBlocked = Boolean(actionBlockedReason)

  const refreshAfterTransfer = useCallback(async (actionScopeKey: string) => {
    await refreshCatalogs()
    if (!mountedRef.current || catalogScopeKeyRef.current !== actionScopeKey) return
    await onRefreshLocal?.()
  }, [onRefreshLocal, refreshCatalogs])

  const pullRemoteItem = useCallback(async (item: LibraryCatalogItem, destination?: LibraryResultDestination) => {
    const transferBlockReason = transferBlockReasonForItem(item, destination)
    if (transferBlockReason) {
      setActionError(transferBlockReason)
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
        application_id: destination?.applicationId || applicationId,
        release_id: destination?.releaseId || releaseId,
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
  }, [applicationId, catalogScopeKey, effectiveProjectID, includeDependencies, libraryApi, refreshAfterTransfer, releaseId, selectedRemoteId, transferBlockReasonForItem])

  const pushLocalItem = useCallback(async (item: LibraryCatalogItem) => {
    const transferBlockReason = transferBlockReasonForItem(item, {
      applicationId: item.application_id || applicationId,
      releaseId: item.release_id || releaseId,
    })
    if (transferBlockReason) {
      setActionError(transferBlockReason)
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
  }, [applicationId, catalogScopeKey, effectiveProjectID, includeDependencies, libraryApi, refreshAfterTransfer, releaseId, selectedRemoteId, transferBlockReasonForItem])

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

  const refreshVisibleLibraries = useCallback(async () => {
    await Promise.all([refreshCatalogs(), onRefreshLocal?.()])
  }, [onRefreshLocal, refreshCatalogs])

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
    transferBlockReasonForItem,
    refreshCatalogs: refreshVisibleLibraries,
    pullRemoteItem,
    pushLocalItem,
    compareItems,
    hostItemForLocal,
    syncStatusForLocal,
    remoteItemHref,
  }
}
