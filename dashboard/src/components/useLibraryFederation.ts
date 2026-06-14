import { useCallback, useEffect, useMemo, useState } from 'react'

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

function transferProjectMessage(projectMapping: string, remote?: LibraryRemote) {
  if (projectMapping.trim() || remote?.default_project_id) return ''
  return 'Set a project mapping before transferring assets.'
}

type UseLibraryFederationOptions = {
  kind: LibraryKind
  projectId?: string
  onRefreshLocal?: () => void | Promise<void>
}

export type UseLibraryFederationResult = ReturnType<typeof useLibraryFederation>

export function useLibraryFederation({ kind, projectId, onRefreshLocal }: UseLibraryFederationOptions) {
  const libraryApi = useLibraryApi()
  const [remotes, setRemotes] = useState<LibraryRemote[]>([])
  const [selectedRemoteId, setSelectedRemoteId] = useState('')
  const [sourceFilter, setSourceFilter] = useState<LibrarySourceFilter>('all')
  const [projectMapping, setProjectMapping] = useState(projectId || '')
  const [includeDependencies, setIncludeDependencies] = useState(true)
  const [localItems, setLocalItems] = useState<LibraryCatalogItem[]>([])
  const [remoteItems, setRemoteItems] = useState<LibraryCatalogItem[]>([])
  const [loading, setLoading] = useState(false)
  const [actionMessage, setActionMessage] = useState('')
  const [actionError, setActionError] = useState('')
  const [diffMessage, setDiffMessage] = useState('')

  const selectedRemote = useMemo(
    () => remotes.find(remote => remote.id === selectedRemoteId),
    [remotes, selectedRemoteId],
  )

  const effectiveProjectID = projectMapping.trim() || selectedRemote?.default_project_id || ''
  const projectBlockMessage = transferProjectMessage(projectMapping, selectedRemote)
  const credentialsReady = remoteCredentialReady(selectedRemote)

  const loadRemotes = useCallback(async () => {
    const response = await libraryApi.listRemotes()
    const nextRemotes = response.remotes || []
    setRemotes(nextRemotes)
    setSelectedRemoteId(current => {
      if (current && nextRemotes.some(remote => remote.id === current)) return current
      return nextRemotes[0]?.id || ''
    })
  }, [libraryApi])

  const loadLocalCatalog = useCallback(async () => {
    const response = await libraryApi.localCatalog(kind, effectiveProjectID)
    setLocalItems(response.items || [])
  }, [effectiveProjectID, kind, libraryApi])

  const loadRemoteCatalog = useCallback(async () => {
    if (!selectedRemoteId) {
      setRemoteItems([])
      return
    }
    const response = await libraryApi.remoteCatalog(selectedRemoteId, kind, effectiveProjectID)
    setRemoteItems(response.catalog.items || [])
  }, [effectiveProjectID, kind, libraryApi, selectedRemoteId])

  const refreshCatalogs = useCallback(async () => {
    setLoading(true)
    setActionError('')
    try {
      await Promise.all([loadLocalCatalog(), loadRemoteCatalog()])
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to load library catalogs'))
    } finally {
      setLoading(false)
    }
  }, [loadLocalCatalog, loadRemoteCatalog])

  useEffect(() => {
    void loadRemotes().catch(error => {
      setActionError(getErrorMessage(error, 'Unable to load Remote Workspaces'))
    })
  }, [loadRemotes])

  useEffect(() => {
    void refreshCatalogs()
  }, [refreshCatalogs])

  useEffect(() => {
    if (projectId && !projectMapping) {
      setProjectMapping(projectId)
    }
  }, [projectId, projectMapping])

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

  const refreshAfterTransfer = useCallback(async () => {
    await refreshCatalogs()
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
    try {
      const response = await libraryApi.pullRemoteItem(selectedRemoteId, {
        kind: item.kind,
        id: item.remote_item_id || item.id,
        remote_id: item.remote_item_id || item.id,
        project_id: effectiveProjectID,
        include_dependencies: includeDependencies,
      })
      setActionMessage(libraryTransferSummary('pull', response))
      await refreshAfterTransfer()
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to pull remote item'))
    }
  }, [actionBlocked, actionBlockedReason, effectiveProjectID, includeDependencies, libraryApi, refreshAfterTransfer, selectedRemoteId])

  const pushLocalItem = useCallback(async (item: LibraryCatalogItem) => {
    if (actionBlocked) {
      setActionError(actionBlockedReason)
      return
    }
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
    try {
      const response = await libraryApi.pushLocalItem(selectedRemoteId, {
        kind: item.kind,
        id: item.id,
        local_id: item.id,
        project_id: effectiveProjectID,
        include_dependencies: includeDependencies,
      })
      setActionMessage(libraryTransferSummary('push', response))
      await refreshAfterTransfer()
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to push local item'))
    }
  }, [actionBlocked, actionBlockedReason, effectiveProjectID, includeDependencies, libraryApi, refreshAfterTransfer, selectedRemoteId])

  const compareItems = useCallback(async (localItemID: string, remoteItem: LibraryCatalogItem) => {
    if (!selectedRemoteId) return
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
    try {
      const diff = await libraryApi.compareItems(selectedRemoteId, {
        kind: remoteItem.kind,
        id: remoteItem.remote_item_id || remoteItem.id,
        local_id: localItemID,
        remote_id: remoteItem.remote_item_id || remoteItem.id,
        project_id: effectiveProjectID,
      })
      setDiffMessage(`${diff.kind} ${diff.status}: local ${shortLibraryHash(diff.local_hash)} / host ${shortLibraryHash(diff.remote_hash)}`)
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to compare items'))
    }
  }, [effectiveProjectID, libraryApi, selectedRemoteId])

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
