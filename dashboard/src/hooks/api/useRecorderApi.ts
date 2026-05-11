import { useCallback, useMemo } from 'react'

import { request } from '../apiClient'

export function useRecorderApi() {
  const startRecording = useCallback(
    (data: { name: string; exclude_static?: boolean; include_hosts?: string[]; exclude_hosts?: string[] }) =>
      request<any>('/recorder/start', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const stopRecording = useCallback((id: string) => request<any>(`/recorder/${id}/stop`, { method: 'POST' }), [])

  const pauseRecording = useCallback((id: string) => request<any>(`/recorder/${id}/pause`, { method: 'POST' }), [])

  const resumeRecording = useCallback((id: string) => request<any>(`/recorder/${id}/resume`, { method: 'POST' }), [])

  const getRecording = useCallback((id: string) => request<any>(`/recorder/${id}`), [])

  const listRecordings = useCallback(() => request<any>('/recorder'), [])

  const deleteRecording = useCallback((id: string) => request<any>(`/recorder/${id}`, { method: 'DELETE' }), [])

  const updateRecordedRequest = useCallback(
    (id: string, seq: number, data: {
      step_name?: string
      transaction_name?: string
      note?: string
      excluded?: boolean
      included?: boolean
      think_time_ms?: number
    }) => request<any>(`/recorder/${id}/requests/${seq}`, { method: 'PATCH', body: JSON.stringify(data) }),
    [],
  )

  const convertRecording = useCallback(
    (id: string, name?: string) =>
      request<any>(`/recorder/${id}/convert`, { method: 'POST', body: JSON.stringify({ name }) }),
    [],
  )

  const previewRecordingConversion = useCallback(
    (id: string, name?: string) =>
      request<any>(`/recorder/${id}/convert/preview`, { method: 'POST', body: JSON.stringify({ name }) }),
    [],
  )

  const getRecorderStatus = useCallback(() => request<any>('/recorder/status'), [])

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
