import { useCallback, useMemo } from 'react'

import { request } from '../apiClient'

export function useRecorderApi() {
  const startRecording = useCallback(
    (data: { name: string; exclude_static?: boolean; include_hosts?: string[]; exclude_hosts?: string[] }) =>
      request<any>('/recorder/start', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const stopRecording = useCallback((id: string) => request<any>(`/recorder/${id}/stop`, { method: 'POST' }), [])

  const getRecording = useCallback((id: string) => request<any>(`/recorder/${id}`), [])

  const listRecordings = useCallback(() => request<any>('/recorder'), [])

  const deleteRecording = useCallback((id: string) => request<any>(`/recorder/${id}`, { method: 'DELETE' }), [])

  const convertRecording = useCallback(
    (id: string, name?: string) =>
      request<any>(`/recorder/${id}/convert`, { method: 'POST', body: JSON.stringify({ name }) }),
    [],
  )

  const getRecorderStatus = useCallback(() => request<any>('/recorder/status'), [])

  return useMemo(
    () => ({
      startRecording,
      stopRecording,
      getRecording,
      listRecordings,
      deleteRecording,
      convertRecording,
      getRecorderStatus,
    }),
    [
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