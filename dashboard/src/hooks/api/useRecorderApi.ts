import { useCallback, useMemo } from 'react'

import { type RecordingProfile, type RecordingProfilesResponse } from '../../types/recordingProfiles'
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
  body_encoding?: string
  body_size_bytes?: number
  body_truncated?: boolean
  response_body_size_bytes?: number
  response_body_encoding?: string
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
  requests?: RecorderRequestRecord[]
  request_count?: number
  last_seq?: number
  // Requests the proxy refused for a private target, and the latest such host.
  rejected_requests?: number
  last_rejected_host?: string
  filters: {
    exclude_static: boolean
    exclude_hosts?: string[]
    include_hosts?: string[]
    capture_cookies?: boolean
    recording_profile_id?: string
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
  capture_cookies?: boolean
  // A project recording profile: fills the options left unset here, and
  // conversions of the session apply it.
  recording_profile_id?: string
}

type RecorderStartResponse = {
  session: RecorderSession
  proxy_addr: string
  // Only for a browser on the controller's host.
  ca_cert_path?: string
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
  // Whether this browser can record through the proxy; reason is operator-facing.
  proxy?: {
    available: boolean
    running?: boolean
    reason_code?: string
    reason?: string
    private_targets_allowed?: boolean
  }
  // Whether this browser runs on the controller's host.
  local_client?: boolean
  // Only for a browser on the controller's host.
  ca_cert_path?: string
  active?: RecorderActiveStatus
  browser?: {
    availability: { available: boolean; code?: string; reason?: string }
    active?: {
      id: string
      name: string
      status: string
      start_url: string
      step_count: number
      started_at: string
    } | null
  }
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

  const getRecording = useCallback(
    (id: string, params?: { includeRequests?: boolean; since?: number; tail?: number }) => {
      const query = new URLSearchParams()
      if (params?.includeRequests) query.set('include', 'requests')
      if (typeof params?.since === 'number') query.set('since', String(params.since))
      if (typeof params?.tail === 'number') query.set('tail', String(params.tail))
      const suffix = query.toString()
      return request<RecorderSession>(`/recorder/${id}${suffix ? `?${suffix}` : ''}`)
    },
    [],
  )

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

  const getRecordingProfiles = useCallback(
    (projectId: string) => request<RecordingProfilesResponse>(`/projects/${encodeURIComponent(projectId)}/recording-profiles`),
    [],
  )

  const saveRecordingProfiles = useCallback(
    (projectId: string, profiles: RecordingProfile[], expectedUpdatedAt?: string) =>
      request<RecordingProfilesResponse>(`/projects/${encodeURIComponent(projectId)}/recording-profiles`, {
        method: 'PUT',
        body: JSON.stringify({ profiles, expected_updated_at: expectedUpdatedAt || undefined }),
      }),
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
      getRecordingProfiles,
      saveRecordingProfiles,
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
      getRecordingProfiles,
      saveRecordingProfiles,
    ],
  )
}
