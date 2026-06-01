import { useCallback, useMemo } from 'react'

import { type RecorderImportResult } from '../../types/scriptImports'
import { request } from '../apiClient'

type RecorderRequestRecord = {
  seq: number
  timestamp: string
  method: string
  url: string
  scheme?: string
  path: string
  query_string?: string
  host: string
  status_code: number
  response_time_ms: number
  content_type: string
  body: string
  body_size_bytes?: number
  body_truncated?: boolean
  response_body_size_bytes?: number
  response_body_truncated?: boolean
  think_time_ms: number
  is_static: boolean
  resource_type?: string
  step_name?: string
  transaction_name?: string
  note?: string
  excluded?: boolean
}

type RecorderSession = {
  id: string
  name: string
  status: string
  project_id?: string
  started_at: string
  stopped_at?: string
  base_url?: string
  requests: RecorderRequestRecord[]
  filters: {
    exclude_static: boolean
    exclude_hosts?: string[]
    include_hosts?: string[]
  }
}

type RecorderActiveStatus = {
  id: string
  name: string
  status: string
  request_count: number
  started_at: string
} | null

type RecorderStartRequest = {
  name: string
  project_id?: string
  exclude_static?: boolean
  include_hosts?: string[]
  exclude_hosts?: string[]
}

type RecorderStartResponse = {
  session: RecorderSession
  proxy_addr: string
  ca_cert_path: string
}

type RecorderListResponse = {
  sessions: RecorderSession[]
  count: number
  active?: RecorderActiveStatus
}

type RecorderDeleteResponse = {
  deleted: string
}

type RecorderStatusResponse = {
  proxy_addr?: string
  ca_cert_path?: string
  active?: RecorderActiveStatus
}

export function useRecorderApi() {
  const startRecording = useCallback(
    (data: RecorderStartRequest) =>
      request<RecorderStartResponse>('/recorder/start', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const stopRecording = useCallback((id: string) => request<RecorderSession>(`/recorder/${id}/stop`, { method: 'POST' }), [])

  const pauseRecording = useCallback((id: string) => request<RecorderSession>(`/recorder/${id}/pause`, { method: 'POST' }), [])

  const resumeRecording = useCallback((id: string) => request<RecorderSession>(`/recorder/${id}/resume`, { method: 'POST' }), [])

  const getRecording = useCallback((id: string) => request<RecorderSession>(`/recorder/${id}`), [])

  const listRecordings = useCallback(
    (projectId?: string) => request<RecorderListResponse>(`/recorder${projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''}`),
    [],
  )

  const deleteRecording = useCallback((id: string) => request<RecorderDeleteResponse>(`/recorder/${id}`, { method: 'DELETE' }), [])

  const updateRecordedRequest = useCallback(
    (id: string, seq: number, data: {
      step_name?: string
      transaction_name?: string
      note?: string
      excluded?: boolean
      included?: boolean
      think_time_ms?: number
    }) => request<RecorderRequestRecord>(`/recorder/${id}/requests/${seq}`, { method: 'PATCH', body: JSON.stringify(data) }),
    [],
  )

  const convertRecording = useCallback(
    (id: string, name?: string, parameterize?: boolean) =>
      request<RecorderImportResult>(`/recorder/${id}/convert`, { method: 'POST', body: JSON.stringify({ name, parameterize }) }),
    [],
  )

  const previewRecordingConversion = useCallback(
    (id: string, name?: string, parameterize?: boolean) =>
      request<RecorderImportResult>(`/recorder/${id}/convert/preview`, { method: 'POST', body: JSON.stringify({ name, parameterize }) }),
    [],
  )

  const getRecorderStatus = useCallback(
    (projectId?: string) => request<RecorderStatusResponse>(projectId ? `/recorder/status?project_id=${encodeURIComponent(projectId)}` : '/recorder/status'),
    [],
  )

  return useMemo(
    () => ({
      startRecording,
      stopRecording,
      pauseRecording,
      resumeRecording,
      getRecording,
      listRecordings,
      deleteRecording,
      updateRecordedRequest,
      convertRecording,
      previewRecordingConversion,
      getRecorderStatus,
    }),
    [
      startRecording,
      stopRecording,
      pauseRecording,
      resumeRecording,
      getRecording,
      listRecordings,
      deleteRecording,
      updateRecordedRequest,
      convertRecording,
      previewRecordingConversion,
      getRecorderStatus,
    ],
  )
}
