import { useCallback, useMemo } from 'react'

import { type RecorderImportResult } from '../../types/scriptImports'
import { request } from '../apiClient'

export type BrowserRecorderStep = {
  seq: number
  kind: string
  value?: string
  masked?: boolean
  timestamp: string
  url?: string
  title?: string
  frame_selector?: string
  candidates?: { kind: string; value: string; confidence: number; reason?: string }[]
  section?: string
  excluded?: boolean
  deleted?: boolean
  thumbnail?: string
  dialog_type?: string
  dialog_message?: string
  mark_secret?: boolean
  mark_param?: boolean
}

export type BrowserRecorderAnnotation = {
  kind: 'console_error' | 'request_failed'
  text: string
  url?: string
  status?: number
  near_step?: number
  timestamp: string
}

export type BrowserRecorderTransactionSuggestion = {
  name: string
  at_step_seq: number
  url: string
}

export type BrowserRecorderDevice = {
  name: string
  platform: string
  width: number
  height: number
  device_scale_factor: number
  mobile: boolean
  has_touch: boolean
}

export type BrowserRecorderMarker = {
  kind: 'transaction_start' | 'transaction_end' | 'note'
  name?: string
  text?: string
  timestamp: string
  auto?: boolean
}

export type BrowserRecorderSession = {
  id: string
  name: string
  project_id?: string
  status: 'recording' | 'paused' | 'stopping' | 'stopped' | 'failed'
  start_url: string
  capture_har: boolean
  started_at: string
  stopped_at?: string
  stop_reason?: 'requested' | 'browser_closed'
  error?: string
  current_url?: string
  step_count: number
  navigation_count: number
  network_request_count: number
  websocket_count: number
  warning_count: number
  marker_count?: number
  annotation_count?: number
  open_transactions?: string[]
  section?: string
  device_profile?: string
  picking?: boolean
  har_available: boolean
  storage_state_available?: boolean
  steps?: BrowserRecorderStep[]
  markers?: BrowserRecorderMarker[]
  annotations?: BrowserRecorderAnnotation[]
  warnings?: string[]
}

export type BrowserRecorderAvailability = {
  available: boolean
  reason?: string
}

export type BrowserRecorderStartRequest = {
  name: string
  start_url: string
  project_id?: string
  capture_har?: boolean
  device_preset?: string
  storage_state_path?: string
  capture_screenshots?: boolean
  capture_ws_payloads?: boolean
  section?: string
  include_hosts?: string[]
  exclude_hosts?: string[]
}

export type BrowserRecorderConvertRequest = {
  name?: string
  start_seq?: number
  end_seq?: number
  split_at_transactions?: boolean
  also_create_http_script?: boolean
}

type BrowserRecorderSessionEnvelope = {
  session: BrowserRecorderSession
}

type BrowserRecorderMarkerEnvelope = {
  session: BrowserRecorderSession
  marker: BrowserRecorderMarker
}

export function useBrowserRecorderApi() {
  const startBrowserRecording = useCallback(
    (data: BrowserRecorderStartRequest) =>
      request<BrowserRecorderSessionEnvelope>('/browser-recorder/sessions', { method: 'POST', body: JSON.stringify(data) }),
    [],
  )

  const getBrowserRecording = useCallback(
    (id: string) => request<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}`),
    [],
  )

  const stopBrowserRecording = useCallback(
    (id: string) => request<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/stop`, { method: 'POST' }),
    [],
  )

  const pauseBrowserRecording = useCallback(
    (id: string) => request<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/pause`, { method: 'POST' }),
    [],
  )

  const resumeBrowserRecording = useCallback(
    (id: string) => request<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/resume`, { method: 'POST' }),
    [],
  )

  const navigateBrowserRecording = useCallback(
    (id: string, url: string) =>
      request<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/navigate`, {
        method: 'POST',
        body: JSON.stringify({ url }),
      }),
    [],
  )

  const focusBrowserRecording = useCallback(
    (id: string) => request<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/focus`, { method: 'POST' }),
    [],
  )

  const startBrowserRecordingTransaction = useCallback(
    (id: string, name: string) =>
      request<BrowserRecorderMarkerEnvelope>(`/browser-recorder/sessions/${id}/transactions/start`, {
        method: 'POST',
        body: JSON.stringify({ name }),
      }),
    [],
  )

  const endBrowserRecordingTransaction = useCallback(
    (id: string, name?: string) =>
      request<BrowserRecorderMarkerEnvelope>(`/browser-recorder/sessions/${id}/transactions/end`, {
        method: 'POST',
        body: JSON.stringify({ name }),
      }),
    [],
  )

  const addBrowserRecordingNote = useCallback(
    (id: string, text: string) =>
      request<BrowserRecorderMarkerEnvelope>(`/browser-recorder/sessions/${id}/notes`, {
        method: 'POST',
        body: JSON.stringify({ text }),
      }),
    [],
  )

  const previewBrowserRecordingConversion = useCallback(
    (id: string, req: BrowserRecorderConvertRequest = {}) =>
      request<RecorderImportResult>(`/browser-recorder/sessions/${id}/convert/preview`, {
        method: 'POST',
        body: JSON.stringify(req),
      }),
    [],
  )

  const convertBrowserRecording = useCallback(
    (id: string, req: BrowserRecorderConvertRequest = {}) =>
      request<RecorderImportResult & { http_script?: { id: string; name: string } }>(
        `/browser-recorder/sessions/${id}/convert`,
        { method: 'POST', body: JSON.stringify(req) },
      ),
    [],
  )

  const setBrowserRecordingSection = useCallback(
    (id: string, section: string) =>
      request<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/section`, {
        method: 'POST',
        body: JSON.stringify({ section }),
      }),
    [],
  )

  const setBrowserRecordingPickMode = useCallback(
    (id: string, on: boolean) =>
      request<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/pick-mode`, {
        method: 'POST',
        body: JSON.stringify({ on }),
      }),
    [],
  )

  const deleteLastBrowserRecordingStep = useCallback(
    (id: string) =>
      request<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/steps/last`, { method: 'DELETE' }),
    [],
  )

  const updateBrowserRecordingStep = useCallback(
    (id: string, seq: number, update: { excluded?: boolean; mark_secret?: boolean; mark_param?: boolean }) =>
      request<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/steps/${seq}`, {
        method: 'PATCH',
        body: JSON.stringify(update),
      }),
    [],
  )

  const suggestBrowserRecordingTransactions = useCallback(
    (id: string) =>
      request<{ suggestions: BrowserRecorderTransactionSuggestion[] }>(
        `/browser-recorder/sessions/${id}/transactions/suggestions`,
      ),
    [],
  )

  const verifyBrowserRecording = useCallback(
    (id: string) =>
      request<{ replay_session_id: string; replay_session: unknown; warnings: string[] }>(
        `/browser-recorder/sessions/${id}/verify`,
        { method: 'POST' },
      ),
    [],
  )

  const listBrowserRecorderDevices = useCallback(
    () => request<{ devices: BrowserRecorderDevice[] }>('/browser-recorder/devices'),
    [],
  )

  const installBrowserRecorder = useCallback(
    () => request<{ install: { status: string; error?: string } }>('/browser-recorder/install', { method: 'POST' }),
    [],
  )

  return useMemo(
    () => ({
      startBrowserRecording,
      getBrowserRecording,
      stopBrowserRecording,
      pauseBrowserRecording,
      resumeBrowserRecording,
      navigateBrowserRecording,
      focusBrowserRecording,
      startBrowserRecordingTransaction,
      endBrowserRecordingTransaction,
      addBrowserRecordingNote,
      previewBrowserRecordingConversion,
      convertBrowserRecording,
      setBrowserRecordingSection,
      setBrowserRecordingPickMode,
      deleteLastBrowserRecordingStep,
      updateBrowserRecordingStep,
      suggestBrowserRecordingTransactions,
      verifyBrowserRecording,
      listBrowserRecorderDevices,
      installBrowserRecorder,
    }),
    [
      startBrowserRecording,
      getBrowserRecording,
      stopBrowserRecording,
      pauseBrowserRecording,
      resumeBrowserRecording,
      navigateBrowserRecording,
      focusBrowserRecording,
      startBrowserRecordingTransaction,
      endBrowserRecordingTransaction,
      addBrowserRecordingNote,
      previewBrowserRecordingConversion,
      convertBrowserRecording,
      setBrowserRecordingSection,
      setBrowserRecordingPickMode,
      deleteLastBrowserRecordingStep,
      updateBrowserRecordingStep,
      suggestBrowserRecordingTransactions,
      verifyBrowserRecording,
      listBrowserRecorderDevices,
      installBrowserRecorder,
    ],
  )
}
