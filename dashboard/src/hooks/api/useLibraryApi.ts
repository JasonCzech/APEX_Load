import { useCallback, useMemo } from 'react'

import {
  DesktopStatusResponse,
  LibraryCatalogPageOptions,
  LibraryCatalogResponse,
  LibraryDiffResponse,
  LibraryKind,
  LibraryRemote,
  LibraryRemoteCatalogResponse,
  LibraryRemoteListResponse,
  LibraryRemoteRequest,
  LibraryTransferRequest,
  LibraryTransferResponse,
} from '../../types/library'
import { apiFetch, request } from '../apiClient'

function catalogPath(kind: LibraryKind, projectId?: string, page?: LibraryCatalogPageOptions) {
  const params = new URLSearchParams({ kind })
  if (projectId?.trim()) {
    params.set('project_id', projectId.trim())
  }
  if (page?.limit !== undefined) {
    params.set('limit', String(page.limit))
  }
  if (page?.offset !== undefined) {
    params.set('offset', String(page.offset))
  }
  return `/library/catalog?${params.toString()}`
}

function remoteCatalogPath(remoteId: string, kind: LibraryKind, projectId?: string, page?: LibraryCatalogPageOptions) {
  const params = new URLSearchParams({ kind })
  if (projectId?.trim()) {
    params.set('project_id', projectId.trim())
  }
  if (page?.limit !== undefined) {
    params.set('limit', String(page.limit))
  }
  if (page?.offset !== undefined) {
    params.set('offset', String(page.offset))
  }
  return `/library/remotes/${encodeURIComponent(remoteId)}/catalog?${params.toString()}`
}

async function transferRequest(path: string, body: LibraryTransferRequest): Promise<LibraryTransferResponse> {
  const response = await apiFetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok && response.status !== 409) {
    const message = typeof payload === 'object' && payload !== null && 'error' in payload && typeof payload.error === 'string'
      ? payload.error
      : response.statusText
    throw new Error(message)
  }
  return payload as LibraryTransferResponse
}

export function useLibraryApi() {
  const listRemotes = useCallback(
    () => request<LibraryRemoteListResponse>('/library/remotes'),
    [],
  )

  const createRemote = useCallback(
    (body: LibraryRemoteRequest) => request<LibraryRemote>('/library/remotes', { method: 'POST', body: JSON.stringify(body) }),
    [],
  )

  const updateRemote = useCallback(
    (remoteId: string, body: LibraryRemoteRequest) =>
      request<LibraryRemote>(`/library/remotes/${encodeURIComponent(remoteId)}`, { method: 'PUT', body: JSON.stringify(body) }),
    [],
  )

  const deleteRemote = useCallback(
    (remoteId: string) => request<{ deleted: string }>(`/library/remotes/${encodeURIComponent(remoteId)}`, { method: 'DELETE' }),
    [],
  )

  const probeRemote = useCallback(
    (remoteId: string) => request<{ status: string; error?: string }>(`/library/remotes/${encodeURIComponent(remoteId)}/probe`, { method: 'POST' }),
    [],
  )

  const localCatalog = useCallback(
    (kind: LibraryKind, projectId?: string, page?: LibraryCatalogPageOptions) =>
      request<LibraryCatalogResponse>(catalogPath(kind, projectId, page), { signal: page?.signal }),
    [],
  )

  const remoteWorkspaces = useCallback(
    (remoteId: string, kind: 'projects' | 'applications' | 'releases', parentId = '', signal?: AbortSignal) =>
      request<{ items: { id: string; name: string }[] }>(`/system/library/remotes/${encodeURIComponent(remoteId)}/workspaces?${new URLSearchParams({ kind, parent_id: parentId })}`, { signal }),
    [],
  )

  const remoteCatalog = useCallback(
    (remoteId: string, kind: LibraryKind, projectId?: string, page?: LibraryCatalogPageOptions) =>
      request<LibraryRemoteCatalogResponse>(remoteCatalogPath(remoteId, kind, projectId, page), { signal: page?.signal }),
    [],
  )

  const pullRemoteItem = useCallback(
    (remoteId: string, body: LibraryTransferRequest) =>
      transferRequest(`/library/remotes/${encodeURIComponent(remoteId)}/pull`, body),
    [],
  )

  const pushLocalItem = useCallback(
    (remoteId: string, body: LibraryTransferRequest) =>
      transferRequest(`/library/remotes/${encodeURIComponent(remoteId)}/push`, body),
    [],
  )

  const compareItems = useCallback(
    (remoteId: string, body: LibraryTransferRequest) =>
      request<LibraryDiffResponse>(`/library/remotes/${encodeURIComponent(remoteId)}/diff`, {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    [],
  )

  const getDesktopStatus = useCallback(
    () => request<DesktopStatusResponse>('/desktop/status'),
    [],
  )

  return useMemo(
    () => ({
      listRemotes,
      createRemote,
      updateRemote,
      deleteRemote,
      probeRemote,
      localCatalog,
      remoteCatalog,
      remoteWorkspaces,
      pullRemoteItem,
      pushLocalItem,
      compareItems,
      getDesktopStatus,
    }),
    [
      listRemotes,
      createRemote,
      updateRemote,
      deleteRemote,
      probeRemote,
      localCatalog,
      remoteCatalog,
      remoteWorkspaces,
      pullRemoteItem,
      pushLocalItem,
      compareItems,
      getDesktopStatus,
    ],
  )
}
