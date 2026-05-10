import { useCallback, useMemo } from 'react'

const BASE = '/api/v1'

export class ApiError extends Error {
  status: number
  code?: string
  payload?: any

  constructor(message: string, status: number, payload?: any) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = payload?.code
    this.payload = payload
  }
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json', ...options?.headers },
    ...options,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new ApiError(body.error || res.statusText, res.status, body)
  }
  return res.json()
}

export function useApi() {
  const listTests = useCallback(() => request<{ tests: any[]; count: number }>('/tests'), [])

  const getTest = useCallback((id: string) => request<any>(`/tests/${id}`), [])

  const createTest = useCallback(
    (data: any) =>
      request<any>('/tests', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const estimateTest = useCallback(
    (data: any) =>
      request<any>('/tests/estimate', { method: 'POST', body: JSON.stringify(data) }),
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

  const stopTest = useCallback(
    (id: string) => request<any>(`/tests/${id}/stop`, { method: 'POST' }),
    [],
  )

  const abortTest = useCallback(
    (id: string) => request<any>(`/tests/${id}/abort`, { method: 'POST' }),
    [],
  )

  const getMetrics = useCallback((id: string) => request<any>(`/tests/${id}/metrics`), [])

  const getTestArchiveManifest = useCallback(
    (id: string) => request<any>(`/tests/${id}/archive/manifest`),
    [],
  )

  const getTestArchiveTransactions = useCallback(
    (id: string, params?: { limit?: number; offset?: number; action?: string; group?: string; success?: boolean }) => {
      const query = new URLSearchParams()
      if (params?.limit !== undefined) query.set('limit', String(params.limit))
      if (params?.offset !== undefined) query.set('offset', String(params.offset))
      if (params?.action) query.set('action', params.action)
      if (params?.group) query.set('group', params.group)
      if (params?.success !== undefined) query.set('success', String(params.success))
      const suffix = query.toString() ? `?${query.toString()}` : ''
      return request<any>(`/tests/${id}/archive/transactions${suffix}`)
    },
    [],
  )

  const getTestArchiveResources = useCallback(
    (id: string, params?: { limit?: number; offset?: number }) => {
      const query = new URLSearchParams()
      if (params?.limit !== undefined) query.set('limit', String(params.limit))
      if (params?.offset !== undefined) query.set('offset', String(params.offset))
      const suffix = query.toString() ? `?${query.toString()}` : ''
      return request<any>(`/tests/${id}/archive/resources${suffix}`)
    },
    [],
  )

  const getTestArchiveSummaries = useCallback(
    (id: string, params?: { limit?: number; offset?: number }) => {
      const query = new URLSearchParams()
      if (params?.limit !== undefined) query.set('limit', String(params.limit))
      if (params?.offset !== undefined) query.set('offset', String(params.offset))
      const suffix = query.toString() ? `?${query.toString()}` : ''
      return request<any>(`/tests/${id}/archive/summaries${suffix}`)
    },
    [],
  )

  const getTestArchiveReport = useCallback(
    (id: string) => request<any>(`/tests/${id}/archive/report`),
    [],
  )

  const downloadTestArchive = useCallback(async (id: string) => {
    const res = await fetch(`${BASE}/tests/${id}/archive`)
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }))
      throw new Error(body.error || res.statusText)
    }
    return res.blob()
  }, [])

  const downloadTestArchiveArtifact = useCallback(async (id: string, name: string) => {
    const res = await fetch(`${BASE}/tests/${id}/archive/files/${encodeURIComponent(name)}`)
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }))
      throw new Error(body.error || res.statusText)
    }
    return res.blob()
  }, [])

  const listScripts = useCallback(() => request<{ scripts: any[]; count: number }>('/scripts'), [])

  const createScript = useCallback(
    (script: any) => request<any>('/scripts', { method: 'POST', body: JSON.stringify({ script }) }),
    [],
  )

  const getScript = useCallback(
    (id: string) => request<any>(`/scripts/${id}`),
    [],
  )

  const updateScript = useCallback(
    (id: string, script: any) =>
      request<any>(`/scripts/${id}`, { method: 'PUT', body: JSON.stringify({ script }) }),
    [],
  )

  const deleteScript = useCallback(
    (id: string) => request<any>(`/scripts/${id}`, { method: 'DELETE' }),
    [],
  )

  const validateScript = useCallback(
    (id: string) => request<any>(`/scripts/${id}/validate`, { method: 'POST' }),
    [],
  )

  const exportScript = useCallback(
    (id: string, outputDir?: string, filename?: string) =>
      request<any>(`/scripts/${id}/export`, {
        method: 'POST',
        body: JSON.stringify({ output_dir: outputDir, filename }),
      }),
    [],
  )

  const importLoadRunner = useCallback(async (file: File, options?: { name?: string; base_url?: string; include_assertions?: boolean }) => {
    const formData = new FormData()
    formData.append('file', file)
    if (options?.name) formData.append('name', options.name)
    if (options?.base_url) formData.append('base_url', options.base_url)
    if (options?.include_assertions) formData.append('include_assertions', 'true')

    const res = await fetch(`${BASE}/scripts/import/loadrunner`, {
      method: 'POST',
      body: formData,
    })
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }))
      throw new Error(body.error || res.statusText)
    }
    return res.json()
  }, [])

  const deleteTest = useCallback(
    (id: string) => request<any>(`/tests/${id}`, { method: 'DELETE' }),
    [],
  )

  const getHealth = useCallback(() => request<any>('/system/health'), [])

  const getSystemSettings = useCallback(() => request<any>('/system/settings'), [])

  const updateSystemSettings = useCallback(
    (settings: any) => request<any>('/system/settings', { method: 'PUT', body: JSON.stringify(settings) }),
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

  const startRecording = useCallback(
    (data: { name: string; exclude_static?: boolean; include_hosts?: string[]; exclude_hosts?: string[] }) =>
      request<any>('/recorder/start', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const stopRecording = useCallback(
    (id: string) => request<any>(`/recorder/${id}/stop`, { method: 'POST' }),
    [],
  )

  const getRecording = useCallback(
    (id: string) => request<any>(`/recorder/${id}`),
    [],
  )

  const listRecordings = useCallback(() => request<any>('/recorder'), [])

  const deleteRecording = useCallback(
    (id: string) => request<any>(`/recorder/${id}`, { method: 'DELETE' }),
    [],
  )

  const convertRecording = useCallback(
    (id: string, name?: string) =>
      request<any>(`/recorder/${id}/convert`, { method: 'POST', body: JSON.stringify({ name }) }),
    [],
  )

  const getRecorderStatus = useCallback(() => request<any>('/recorder/status'), [])

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
      getMetrics,
      getTestArchiveManifest,
      getTestArchiveTransactions,
      getTestArchiveResources,
      getTestArchiveSummaries,
      getTestArchiveReport,
      downloadTestArchive,
      downloadTestArchiveArtifact,
      listScripts,
      createScript,
      getScript,
      updateScript,
      deleteScript,
      validateScript,
      exportScript,
      importLoadRunner,
      getHealth,
      getSystemSettings,
      updateSystemSettings,
      scaleVUsers,
      adjustDuration,
      startRecording,
      stopRecording,
      getRecording,
      listRecordings,
      deleteRecording,
      convertRecording,
      getRecorderStatus,
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
      getMetrics,
      getTestArchiveManifest,
      getTestArchiveTransactions,
      getTestArchiveResources,
      getTestArchiveSummaries,
      getTestArchiveReport,
      downloadTestArchive,
      downloadTestArchiveArtifact,
      listScripts,
      createScript,
      getScript,
      updateScript,
      deleteScript,
      validateScript,
      exportScript,
      importLoadRunner,
      getHealth,
      getSystemSettings,
      updateSystemSettings,
      scaleVUsers,
      adjustDuration,
      startRecording,
      stopRecording,
      getRecording,
      listRecordings,
      deleteRecording,
      convertRecording,
      getRecorderStatus,
    ],
  )
}

