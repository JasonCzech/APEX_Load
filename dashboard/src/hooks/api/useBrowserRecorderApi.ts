import { useCallback, useEffect, useMemo, useState } from 'react'

import { type RecorderImportResult } from '../../types/scriptImports'
import { apiFetch, request } from '../apiClient'
import {
  bridgeFetch,
  bridgeRequest,
  clearBrowserRecorderBridge,
  pairBrowserRecorderBridge,
  readBrowserRecorderBridge,
  subscribeBrowserRecorderBridgeChanges,
  verifyBrowserRecorderBridgeAgent,
  type BrowserRecorderBridgeConnection,
  type BrowserRecorderBridgeStatusEnvelope,
} from './browserRecorderBridge'

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
  const [browserRecorderBridge, setBrowserRecorderBridge] = useState<BrowserRecorderBridgeConnection | null>(() => readBrowserRecorderBridge())

  useEffect(() => {
    const refreshBridge = () => setBrowserRecorderBridge(readBrowserRecorderBridge())
    return subscribeBrowserRecorderBridgeChanges(refreshBridge)
  }, [])

  const recorderRequest = useCallback(
    <T,>(path: string, options?: RequestInit) =>
      browserRecorderBridge
        ? bridgeRequest<T>(browserRecorderBridge, path, options)
        : request<T>(path, options),
    [browserRecorderBridge],
  )

  const startBrowserRecording = useCallback(
    (data: BrowserRecorderStartRequest) =>
      recorderRequest<BrowserRecorderSessionEnvelope>('/browser-recorder/sessions', { method: 'POST', body: JSON.stringify(data) }),
    [recorderRequest],
  )

  const getBrowserRecording = useCallback(
    (id: string) => recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}`),
    [recorderRequest],
  )

  const stopBrowserRecording = useCallback(
    (id: string) => recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/stop`, { method: 'POST' }),
    [recorderRequest],
  )

  const pauseBrowserRecording = useCallback(
    (id: string) => recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/pause`, { method: 'POST' }),
    [recorderRequest],
  )

  const resumeBrowserRecording = useCallback(
    (id: string) => recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/resume`, { method: 'POST' }),
    [recorderRequest],
  )

  const navigateBrowserRecording = useCallback(
    (id: string, url: string) =>
      recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/navigate`, {
        method: 'POST',
        body: JSON.stringify({ url }),
      }),
    [recorderRequest],
  )

  const focusBrowserRecording = useCallback(
    (id: string) => recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/focus`, { method: 'POST' }),
    [recorderRequest],
  )

  const startBrowserRecordingTransaction = useCallback(
    (id: string, name: string) =>
      recorderRequest<BrowserRecorderMarkerEnvelope>(`/browser-recorder/sessions/${id}/transactions/start`, {
        method: 'POST',
        body: JSON.stringify({ name }),
      }),
    [recorderRequest],
  )

  const endBrowserRecordingTransaction = useCallback(
    (id: string, name?: string) =>
      recorderRequest<BrowserRecorderMarkerEnvelope>(`/browser-recorder/sessions/${id}/transactions/end`, {
        method: 'POST',
        body: JSON.stringify({ name }),
      }),
    [recorderRequest],
  )

  const addBrowserRecordingNote = useCallback(
    (id: string, text: string) =>
      recorderRequest<BrowserRecorderMarkerEnvelope>(`/browser-recorder/sessions/${id}/notes`, {
        method: 'POST',
        body: JSON.stringify({ text }),
      }),
    [recorderRequest],
  )

  const previewBrowserRecordingConversion = useCallback(
    (id: string, req: BrowserRecorderConvertRequest = {}) =>
      recorderRequest<RecorderImportResult>(`/browser-recorder/sessions/${id}/convert/preview`, {
        method: 'POST',
        body: JSON.stringify(req),
      }),
    [recorderRequest],
  )

  const convertBrowserRecording = useCallback(
    (id: string, req: BrowserRecorderConvertRequest = {}) =>
      recorderRequest<RecorderImportResult & { http_script?: { id: string; name: string } }>(
        `/browser-recorder/sessions/${id}/convert`,
        { method: 'POST', body: JSON.stringify(req) },
      ),
    [recorderRequest],
  )

  const setBrowserRecordingSection = useCallback(
    (id: string, section: string) =>
      recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/section`, {
        method: 'POST',
        body: JSON.stringify({ section }),
      }),
    [recorderRequest],
  )

  const setBrowserRecordingPickMode = useCallback(
    (id: string, on: boolean) =>
      recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/pick-mode`, {
        method: 'POST',
        body: JSON.stringify({ on }),
      }),
    [recorderRequest],
  )

  const deleteLastBrowserRecordingStep = useCallback(
    (id: string) =>
      recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/steps/last`, { method: 'DELETE' }),
    [recorderRequest],
  )

  const updateBrowserRecordingStep = useCallback(
    (id: string, seq: number, update: { excluded?: boolean; mark_secret?: boolean; mark_param?: boolean }) =>
      recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/steps/${seq}`, {
        method: 'PATCH',
        body: JSON.stringify(update),
      }),
    [recorderRequest],
  )

  const suggestBrowserRecordingTransactions = useCallback(
    (id: string) =>
      recorderRequest<{ suggestions: BrowserRecorderTransactionSuggestion[] }>(
        `/browser-recorder/sessions/${id}/transactions/suggestions`,
      ),
    [recorderRequest],
  )

  const verifyBrowserRecording = useCallback(
    (id: string) =>
      recorderRequest<{ replay_session_id: string; replay_session: unknown; warnings: string[] }>(
        `/browser-recorder/sessions/${id}/verify`,
        { method: 'POST' },
      ),
    [recorderRequest],
  )

  const listBrowserRecorderDevices = useCallback(
    () => recorderRequest<{ devices: BrowserRecorderDevice[] }>('/browser-recorder/devices'),
    [recorderRequest],
  )

  const installBrowserRecorder = useCallback(
    () => recorderRequest<{ install: { status: string; error?: string } }>('/browser-recorder/install', { method: 'POST' }),
    [recorderRequest],
  )

  const getBrowserRecorderBridgeStatus = useCallback(
    () => browserRecorderBridge
      ? bridgeRequest<BrowserRecorderBridgeStatusEnvelope>(browserRecorderBridge, '/bridge/status')
      : Promise.resolve(null),
    [browserRecorderBridge],
  )

  const pairBrowserRecorderBridgeTo = useCallback(
    (origin: string, token: string) => pairBrowserRecorderBridge(origin, token),
    [],
  )

  const verifyBrowserRecorderBridgeAgentAt = useCallback(
    (origin: string) => verifyBrowserRecorderBridgeAgent(origin),
    [],
  )

  const clearBrowserRecorderBridgeConnection = useCallback(
    () => {
      clearBrowserRecorderBridge()
      setBrowserRecorderBridge(null)
    },
    [],
  )

  const exportBrowserRecording = useCallback(
    (id: string, format = 'har') => {
      const path = `/browser-recorder/sessions/${id}/export?format=${encodeURIComponent(format)}`
      return browserRecorderBridge
        ? bridgeFetch(browserRecorderBridge, path)
        : apiFetch(path)
    },
    [browserRecorderBridge],
  )

  return useMemo(
    () => ({
      browserRecorderBridge,
      getBrowserRecorderBridgeStatus,
      pairBrowserRecorderBridge: pairBrowserRecorderBridgeTo,
      verifyBrowserRecorderBridgeAgent: verifyBrowserRecorderBridgeAgentAt,
      clearBrowserRecorderBridge: clearBrowserRecorderBridgeConnection,
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
      exportBrowserRecording,
    }),
    [
      browserRecorderBridge,
      getBrowserRecorderBridgeStatus,
      pairBrowserRecorderBridgeTo,
      verifyBrowserRecorderBridgeAgentAt,
      clearBrowserRecorderBridgeConnection,
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
      exportBrowserRecording,
    ],
  )
}
