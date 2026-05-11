import { useCallback, useMemo } from 'react'

import { BASE, request } from '../apiClient'

type ArchiveTransactionsParams = {
  limit?: number
  offset?: number
  action?: string
  group?: string
  success?: boolean
}

type ArchiveListParams = {
  limit?: number
  offset?: number
}

function buildQuery(params?: Record<string, string | number | boolean | undefined>) {
  const query = new URLSearchParams()

  Object.entries(params || {}).forEach(([key, value]) => {
    if (value !== undefined) {
      query.set(key, String(value))
    }
  })

  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

async function fetchBlob(path: string): Promise<Blob> {
  const res = await fetch(`${BASE}${path}`)
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(body.error || res.statusText)
  }
  return res.blob()
}

export function useTestApi() {
  const listTests = useCallback(
    (params?: { project_id?: string; folder_id?: string | null; application_id?: string; release_id?: string; scenario_id?: string } | string, folderId?: string | null) => {
      const queryParams = typeof params === 'string'
        ? { project_id: params, folder_id: folderId === undefined ? undefined : folderId || '' }
        : {
            application_id: params?.application_id,
            release_id: params?.release_id,
            scenario_id: params?.scenario_id,
            project_id: params?.project_id,
            folder_id: params?.folder_id === undefined ? undefined : params.folder_id || '',
          }
      return request<{ tests: any[]; count: number }>(`/tests${buildQuery(queryParams)}`)
    },
    [],
  )

  const getTest = useCallback((id: string) => request<any>(`/tests/${id}`), [])

  const createTest = useCallback(
    (data: any) => request<any>('/tests', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const estimateTest = useCallback(
    (data: any) => request<any>('/tests/estimate', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const startTest = useCallback(
    (id: string, options?: { override_capacity_shortfall?: boolean }) =>
      request<any>(`/tests/${id}/start`, {
        method: 'POST',
        body: options ? JSON.stringify(options) : undefined,
      }),
    [],
  )

  const stopTest = useCallback((id: string) => request<any>(`/tests/${id}/stop`, { method: 'POST' }), [])

  const abortTest = useCallback((id: string) => request<any>(`/tests/${id}/abort`, { method: 'POST' }), [])

  const deleteTest = useCallback((id: string) => request<any>(`/tests/${id}`, { method: 'DELETE' }), [])

  const moveTest = useCallback(
    (id: string, folderId: string | null) =>
      request<any>(`/tests/${id}/move`, { method: 'PATCH', body: JSON.stringify({ folder_id: folderId }) }),
    [],
  )

  const getMetrics = useCallback((id: string) => request<any>(`/tests/${id}/metrics`), [])

  const getTestArchiveManifest = useCallback((id: string) => request<any>(`/tests/${id}/archive/manifest`), [])

  const getTestArchiveTransactions = useCallback(
    (id: string, params?: ArchiveTransactionsParams) => {
      const suffix = buildQuery({
        limit: params?.limit,
        offset: params?.offset,
        action: params?.action,
        group: params?.group,
        success: params?.success,
      })
      return request<any>(`/tests/${id}/archive/transactions${suffix}`)
    },
    [],
  )

  const getTestArchiveResources = useCallback(
    (id: string, params?: ArchiveListParams) => {
      const suffix = buildQuery({ limit: params?.limit, offset: params?.offset })
      return request<any>(`/tests/${id}/archive/resources${suffix}`)
    },
    [],
  )

  const getTestArchiveSummaries = useCallback(
    (id: string, params?: ArchiveListParams) => {
      const suffix = buildQuery({ limit: params?.limit, offset: params?.offset })
      return request<any>(`/tests/${id}/archive/summaries${suffix}`)
    },
    [],
  )

  const getTestArchiveReport = useCallback((id: string) => request<any>(`/tests/${id}/archive/report`), [])

  const downloadTestArchive = useCallback((id: string) => fetchBlob(`/tests/${id}/archive`), [])

  const downloadTestArchiveArtifact = useCallback(
    (id: string, name: string) => fetchBlob(`/tests/${id}/archive/files/${encodeURIComponent(name)}`),
    [],
  )

  const scaleVUsers = useCallback(
    (testId: string, group: string, targetVUsers: number) =>
      request<any>(`/tests/${testId}/vusers`, {
        method: 'PATCH',
        body: JSON.stringify({ group, target_vusers: targetVUsers }),
      }),
    [],
  )

  const adjustDuration = useCallback(
    (testId: string, duration: string) =>
      request<any>(`/tests/${testId}/duration`, {
        method: 'PATCH',
        body: JSON.stringify({ duration }),
      }),
    [],
  )

  return useMemo(
    () => ({
      listTests,
      getTest,
      createTest,
      estimateTest,
      startTest,
      stopTest,
      abortTest,
      deleteTest,
      moveTest,
      getMetrics,
      getTestArchiveManifest,
      getTestArchiveTransactions,
      getTestArchiveResources,
      getTestArchiveSummaries,
      getTestArchiveReport,
      downloadTestArchive,
      downloadTestArchiveArtifact,
      scaleVUsers,
      adjustDuration,
    }),
    [
      listTests,
      getTest,
      createTest,
      estimateTest,
      startTest,
      stopTest,
      abortTest,
      deleteTest,
      moveTest,
      getMetrics,
      getTestArchiveManifest,
      getTestArchiveTransactions,
      getTestArchiveResources,
      getTestArchiveSummaries,
      getTestArchiveReport,
      downloadTestArchive,
      downloadTestArchiveArtifact,
      scaleVUsers,
      adjustDuration,
    ],
  )
}
