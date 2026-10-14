import { useCallback, useEffect, useMemo, useState } from 'react'

import { type StoredScript } from '../../types'
import type { RecordingEnvironment } from '../../types/recordingEnvironment'
import {
  type RecorderImportResult,
  type SecretBindingSelection,
} from '../../types/scriptImports'
import { API_ORIGIN, apiFetch, request, trimTrailingSlash } from '../apiClient'
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
  element?: { aria_label?: string; text?: string; name?: string; placeholder?: string; id?: string }
  frame_selector?: string
  candidates?: { kind: string; value: string; confidence: number; reason?: string }[]
  target_selector?: string
  target_candidates?: { kind: string; value: string; confidence: number; reason?: string }[]
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
  stop_reason?: 'requested' | 'browser_closed' | 'forced'
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
  recording_environment?: RecordingEnvironment
  picking?: boolean
  har_available: boolean
  storage_state_available?: boolean
  steps?: BrowserRecorderStep[]
  markers?: BrowserRecorderMarker[]
  annotations?: BrowserRecorderAnnotation[]
  warnings?: string[]
}

// A login state saved from one of the project's browser recordings; path is
// what a recording start takes as storage_state_path.
export type BrowserRecorderStorageState = {
  path: string
  name: string
  recording_id?: string
  saved_at: string
  expires_at?: string
}

export type BrowserRecorderAvailability = {
  available: boolean
  code?: string
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
  // sidebar toggles the in-page advanced control sidebar embedded in the
  // recorded browser. Omitted/true shows it; false falls back to the minimal
  // pause/assert toolbar.
  sidebar?: boolean
  // quic_mode 'off' launches the recorded browser with QUIC disabled, so the
  // recording holds no HTTP/3 traffic. Omitted means the browser default.
  quic_mode?: 'auto' | 'off'
}

export type BrowserRecorderConvertRequest = {
  name?: string
  // Save-only destination applied by the server to every script created by
  // the atomic conversion. Preview deliberately ignores folder placement.
  folder_id?: string | null
  start_seq?: number
  end_seq?: number
  split_at_transactions?: boolean
  also_create_http_script?: boolean
  /** "url" (default) replays recorded URLs; "html" turns link, image and form clicks into HTML-mode steps. */
  http_script_level?: 'url' | 'html'
  /** Project recording profile the HAR-derived HTTP script applies. */
  recording_profile_id?: string
  /** How the HTTP script maps captured HTTP versions: upgrade (default), exact or inherit. */
  http_version_mode?: 'upgrade' | 'exact' | 'inherit'
  parameterize?: boolean
  // Save-only override applied to the fresh server conversion before its
  // create transaction. The script index binds each opaque requirement ID to
  // the same generated script reviewed in the preview.
  secret_bindings?: Array<SecretBindingSelection & { script_index: number }>
  // Value-free choices re-derived and applied by the server inside the atomic
  // create transaction.
  parameterization_selections?: Array<{ script_index: number; selected_ids: string[] }>
  // Fences a changed preview/save conversion shape before persistence.
  expected_script_count?: number
}

// The server-side convert endpoint persists the converted script(s) inside one
// transaction and returns them in their masked stored form. The optional
// HAR-derived HTTP script, when persisted, is appended last to `scripts`.
export type BrowserRecorderConvertResponse = Omit<RecorderImportResult, 'script' | 'scripts'> & {
  script?: StoredScript
  scripts?: StoredScript[]
  http_script?: StoredScript
}

type BrowserRecorderSessionEnvelope = {
  session: BrowserRecorderSession
}

type BrowserRecorderMarkerEnvelope = {
  session: BrowserRecorderSession
  marker: BrowserRecorderMarker
}

function recorderRequestScope(
  bridge: BrowserRecorderBridgeConnection | null,
  apiOrigin = API_ORIGIN,
): string {
  return bridge
    ? `bridge:${trimTrailingSlash(bridge.origin)}:${bridge.session_id}`
    : `api:${trimTrailingSlash(apiOrigin)}`
}

export function useBrowserRecorderApi() {
  const [recorderEndpoint, setRecorderEndpoint] = useState(() => {
    const bridge = readBrowserRecorderBridge()
    return {
      bridge,
      requestScope: recorderRequestScope(bridge),
    }
  })
  const browserRecorderBridge = recorderEndpoint.bridge
  const browserRecorderRequestScope = recorderEndpoint.requestScope

  useEffect(() => {
    const refreshBridge = () => {
      const bridge = readBrowserRecorderBridge()
      setRecorderEndpoint({
        bridge,
        requestScope: recorderRequestScope(bridge),
      })
    }
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

  const getRawBrowserRecording = useCallback(
    (id: string) => recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}?raw=true`),
    [recorderRequest],
  )

  const listBrowserRecordings = useCallback(
    (projectId?: string) => {
      const query = projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''
      return recorderRequest<{ sessions: BrowserRecorderSession[]; count: number }>(`/browser-recorder/sessions${query}`)
    },
    [recorderRequest],
  )

  const deleteBrowserRecording = useCallback(
    (id: string) => recorderRequest<{ deleted: string; session: BrowserRecorderSession }>(`/browser-recorder/sessions/${id}`, { method: 'DELETE' }),
    [recorderRequest],
  )

  // Keeps a stopped recording's login state for later recordings to reuse;
  // the answer is the kept state's path (a recorder that predates
  // persist_only answers with the scrubbed state instead, without a path).
  const saveBrowserRecordingStorageState = useCallback(
    (id: string) => recorderRequest<{ path?: string }>(`/browser-recorder/sessions/${id}/storage-state?persist_only=true`),
    [recorderRequest],
  )

  const listBrowserRecordingStorageStates = useCallback(
    (projectId?: string) => {
      const query = projectId ? `?project_id=${encodeURIComponent(projectId)}` : ''
      return recorderRequest<{ states: BrowserRecorderStorageState[]; count: number }>(`/browser-recorder/storage-states${query}`)
    },
    [recorderRequest],
  )

  const stopBrowserRecording = useCallback(
    (id: string) => recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/stop`, { method: 'POST' }),
    [recorderRequest],
  )

  const forceStopBrowserRecording = useCallback(
    (id: string) => recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/force-stop`, { method: 'POST' }),
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

  const openBrowserRecordingControlPanel = useCallback(
    (id: string, url: string) =>
      recorderRequest<BrowserRecorderSessionEnvelope>(`/browser-recorder/sessions/${id}/control-panel`, {
        method: 'POST',
        body: JSON.stringify({
          url,
          bridge_connection: browserRecorderBridge || undefined,
        }),
      }),
    [browserRecorderBridge, recorderRequest],
  )

  const startBrowserRecordingTransaction = useCallback(
    (id: string, name: string, atStepSeq?: number) =>
      recorderRequest<BrowserRecorderMarkerEnvelope>(`/browser-recorder/sessions/${id}/transactions/start`, {
        method: 'POST',
        body: JSON.stringify({ name, at_step_seq: atStepSeq || undefined }),
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
      recorderRequest<BrowserRecorderConvertResponse>(
        `/browser-recorder/sessions/${id}/convert`,
        { method: 'POST', body: JSON.stringify(req) },
      ),
    [recorderRequest],
  )

  const attestBridgedBrowserRecordingImport = useCallback(
    (scripts: RecorderImportResult['scripts'], projectId?: string) =>
      request<RecorderImportResult>('/scripts/import/browser-recorder/attest', {
        method: 'POST',
        body: JSON.stringify({ scripts, project_id: projectId }),
      }),
    [],
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
    (id: string, seq: number, update: { excluded?: boolean; mark_secret?: boolean; mark_param?: boolean; promote_selector?: string }) =>
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
    (id: string, replaySessionId?: string, conversion?: BrowserRecorderConvertRequest, scriptIndex = 0) =>
      recorderRequest<{ replay_session_id: string; replay_session: unknown; warnings: string[] }>(
        `/browser-recorder/sessions/${id}/verify`,
        {
          method: 'POST',
          body: JSON.stringify({ replay_session_id: replaySessionId || undefined, conversion, script_index: scriptIndex }),
        },
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
      setRecorderEndpoint({
        bridge: null,
        requestScope: recorderRequestScope(null),
      })
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
      browserRecorderRequestScope,
      getBrowserRecorderBridgeStatus,
      pairBrowserRecorderBridge: pairBrowserRecorderBridgeTo,
      verifyBrowserRecorderBridgeAgent: verifyBrowserRecorderBridgeAgentAt,
      clearBrowserRecorderBridge: clearBrowserRecorderBridgeConnection,
      startBrowserRecording,
      getBrowserRecording,
      getRawBrowserRecording,
      listBrowserRecordings,
      deleteBrowserRecording,
      saveBrowserRecordingStorageState,
      listBrowserRecordingStorageStates,
      stopBrowserRecording,
      forceStopBrowserRecording,
      pauseBrowserRecording,
      resumeBrowserRecording,
      navigateBrowserRecording,
      focusBrowserRecording,
      openBrowserRecordingControlPanel,
      startBrowserRecordingTransaction,
      endBrowserRecordingTransaction,
      addBrowserRecordingNote,
      previewBrowserRecordingConversion,
      convertBrowserRecording,
      attestBridgedBrowserRecordingImport,
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
      browserRecorderRequestScope,
      getBrowserRecorderBridgeStatus,
      pairBrowserRecorderBridgeTo,
      verifyBrowserRecorderBridgeAgentAt,
      clearBrowserRecorderBridgeConnection,
      startBrowserRecording,
      getBrowserRecording,
      getRawBrowserRecording,
      listBrowserRecordings,
      deleteBrowserRecording,
      saveBrowserRecordingStorageState,
      listBrowserRecordingStorageStates,
      stopBrowserRecording,
      forceStopBrowserRecording,
      pauseBrowserRecording,
      resumeBrowserRecording,
      navigateBrowserRecording,
      focusBrowserRecording,
      openBrowserRecordingControlPanel,
      startBrowserRecordingTransaction,
      endBrowserRecordingTransaction,
      addBrowserRecordingNote,
      previewBrowserRecordingConversion,
      convertBrowserRecording,
      attestBridgedBrowserRecordingImport,
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
