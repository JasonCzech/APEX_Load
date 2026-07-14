import { useState, useEffect, useCallback, useRef, type MouseEvent } from 'react'
import { downloadBlob, downloadFilenameFromDisposition } from '../utils/fileUtils'
import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { API_ORIGIN, apiFetch, trimTrailingSlash } from '../hooks/apiClient'
import { type BrowserRecorderAvailability, type BrowserRecorderSession } from '../hooks/api/useBrowserRecorderApi'
import { isWailsLocation } from '../desktop/desktopBridge'
import { Project, type StoredScript } from '../types'
import { type RecorderImportResult, type SalesforceImportResult } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import {
  composeHttpURL,
  DEFAULT_HTTP_URL_SCHEME,
  normalizeHttpURLInput,
  type HttpURLScheme,
} from '../utils/httpUrlInput'
import { withFallbackPersistedScript } from './importSaveUtils'
import BrowserRecorderControlPanel from './BrowserRecorderControlPanel'
import {
  CLIENT_DOWNLOAD_PLATFORMS,
  detectPlatformFromNavigator,
  platformLabel,
} from './ClientDownloadsMenu'
import PageViewToggle from './PageViewToggle'
import { attachImportCorrelationSuggestions } from './scriptEditorUtils'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import {
  DEFAULT_MACOS_NETWORK_SERVICE,
  buildMacOSRestoreProxyCommand,
  buildMacOSStartProxyCommand,
  buildWindowsRestoreProxyCommand,
  buildWindowsStartProxyCommand,
  recorderProxyHost,
  recorderProxyHostIsLocal,
  recorderProxyPort,
} from './recordingProxyCommands'
import './RecordingStudio.css'

interface RecordedRequest {
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
  response_body_encoding?: string
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

interface RecordingSession {
  id: string
  name: string
  status: string
  started_at: string
  stopped_at?: string
  base_url?: string
  requests?: RecordedRequest[]
  request_count?: number
  last_seq?: number
  filters: {
    exclude_static: boolean
    exclude_hosts?: string[]
    include_hosts?: string[]
    capture_cookies?: boolean
  }
}

// confidenceTone buckets a 0..1 correlation confidence for badge styling.
// >=0.8 is auto-applicable in the engine; the UI mirrors that threshold.
function confidenceTone(confidence?: number): 'high' | 'medium' | 'low' | null {
  if (typeof confidence !== 'number') return null
  if (confidence >= 0.8) return 'high'
  if (confidence >= 0.6) return 'medium'
  return 'low'
}

function safeRecordingFileName(name?: string): string {
  return (name || 'recording').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'recording'
}

function recordingLooksSalesforce(session: RecordingSession): boolean {
  const text = [
    session.base_url,
    ...((session.requests || []).flatMap(req => [req.host, req.url, req.path, req.content_type])),
  ].join(' ').toLowerCase()
  return [
    'salesforce.com',
    '.force.com',
    'lightning.force',
    '/aura',
    'agentforce',
    'mulesoft',
    'anypoint',
  ].some(signal => text.includes(signal))
}

function mergeRecordingSession(previous: RecordingSession | null, next: RecordingSession): RecordingSession {
  const incoming = next.requests || []
  if (!previous || previous.id !== next.id) {
    return { ...next, requests: incoming }
  }
  if (incoming.length === 0) {
    return { ...previous, ...next, requests: previous.requests || [] }
  }
  const bySeq = new Map<number, RecordedRequest>()
  ;(previous.requests || []).forEach(req => bySeq.set(req.seq, req))
  incoming.forEach(req => bySeq.set(req.seq, req))
  return {
    ...previous,
    ...next,
    requests: Array.from(bySeq.values()).sort((a, b) => a.seq - b.seq),
  }
}

function isStoredScriptLike(value: unknown): value is StoredScript {
  const candidate = value as Partial<StoredScript> | null | undefined
  return !!candidate && typeof candidate === 'object' && typeof candidate.id === 'string' && candidate.id.trim() !== '' && typeof candidate.name === 'string' && typeof candidate.protocol === 'string'
}

function isScriptDefinitionLike(value: unknown): value is ScriptDefinition {
  const candidate = value as Partial<ScriptDefinition> | null | undefined
  return !!candidate && typeof candidate === 'object' && typeof candidate.name === 'string' && typeof candidate.protocol === 'string' && (Array.isArray(candidate.actions) || Array.isArray(candidate.init) || Array.isArray(candidate.teardown))
}

function recorderImportStoredScript(result?: RecorderImportResult | null): StoredScript | null {
  const script = result?.script as unknown
  return isStoredScriptLike(script) ? script : null
}

function recorderImportReviewScript(result?: RecorderImportResult | null): ScriptDefinition | null {
  const script = result?.script as unknown
  if (isStoredScriptLike(script)) {
    return script.script || null
  }
  return isScriptDefinitionLike(script) ? script : null
}

interface RecorderStatus {
  proxy_addr?: string
  ca_cert_path?: string
  active?: {
    id: string
    name: string
    status: string
    request_count: number
    started_at: string
  } | null
  browser?: {
    availability: BrowserRecorderAvailability
    active?: { id: string; name: string; status: string } | null
    install?: { status: string; error?: string }
  }
}

interface RecordingAgentChecksum {
  algorithm?: string
  filename?: string
  sha256?: string
}

const MISSING_BROWSER_RECORDER_CAPABILITY: BrowserRecorderAvailability = {
  available: false,
  code: 'not_supported',
  reason: 'controller does not expose browser recording endpoints',
}
const DEFAULT_BRIDGE_ORIGIN = 'http://127.0.0.1:27777'
const DEFAULT_RECORDING_AGENT_DOWNLOAD_PATH = '/api/v1/downloads/recording-agent/latest'
const BRIDGE_PAIRING_TOKEN_BYTES = 24
const RECORDING_AGENT_COMMAND = 'apexload-record-agent'

function generateBridgePairingToken(): string {
  const cryptoObj = typeof globalThis !== 'undefined' ? globalThis.crypto : undefined
  if (cryptoObj?.getRandomValues) {
    const bytes = new Uint8Array(BRIDGE_PAIRING_TOKEN_BYTES)
    cryptoObj.getRandomValues(bytes)
    return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
  }
  if (cryptoObj?.randomUUID) {
    return cryptoObj.randomUUID().replace(/-/g, '')
  }
  throw new Error('Secure browser crypto is required to generate a bridge pairing token')
}

function initialBridgePairingToken(): string {
  try {
    return generateBridgePairingToken()
  } catch {
    return ''
  }
}

function browserRecorderInstallRetryable(availability: BrowserRecorderAvailability | null, installState: { status: string; error?: string } | null): boolean {
  if (!installState || installState.status === 'installed') {
    return false
  }
  const code = availability?.code?.trim()
  if (code === 'browsers_not_installed') {
    return true
  }
  if (code && ['display_missing', 'production_disabled', 'disabled', 'not_supported'].includes(code)) {
    return false
  }
  const reason = availability?.reason || ''
  const normalizedReason = reason.toLowerCase()
  return ![
    'no display detected',
    'production mode',
    'disabled via',
    'does not expose browser recording endpoints',
  ].some(blockingReason => normalizedReason.includes(blockingReason))
}

function browserRecorderInstallTone(status: string): 'info' | 'success' | 'error' {
  if (status === 'failed') return 'error'
  if (status === 'installed') return 'success'
  return 'info'
}

function browserRecorderInstallLabel(status: string): string {
  switch (status) {
    case 'failed':
      return 'Recording browser install failed'
    case 'installed':
      return 'Recording browser install completed'
    case 'installing':
      return 'Recording browser install is running'
    default:
      return `Recording browser install: ${status}`
  }
}

function currentDashboardOrigin(): string {
  if (typeof window === 'undefined' || !window.location.origin || window.location.origin === 'null') {
    return 'https://your-apexload-controller.example'
  }
  return window.location.origin
}

function bridgeAllowedDashboardOrigins(): string[] {
  const origin = currentDashboardOrigin()
  const origins = new Set([origin])
  try {
    const url = new URL(origin)
    if (url.hostname === '127.0.0.1') {
      origins.add(`${url.protocol}//localhost${url.port ? `:${url.port}` : ''}`)
    } else if (url.hostname === 'localhost') {
      origins.add(`${url.protocol}//127.0.0.1${url.port ? `:${url.port}` : ''}`)
    }
  } catch {
    // Keep the primary origin if parsing fails.
  }
  return Array.from(origins)
}

function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

function bridgeListenAddress(bridgeOrigin: string): string {
  try {
    const url = new URL(bridgeOrigin)
    return url.host || DEFAULT_BRIDGE_ORIGIN.replace(/^https?:\/\//i, '')
  } catch {
    return bridgeOrigin.replace(/^https?:\/\//i, '').replace(/\/+$/, '').trim() || DEFAULT_BRIDGE_ORIGIN.replace(/^https?:\/\//i, '')
  }
}

function bridgeRecordAgentCommand(bridgeOrigin: string): string {
  const parts = [
    RECORDING_AGENT_COMMAND,
    '--addr',
    bridgeListenAddress(bridgeOrigin),
    '--token-stdin',
  ]
  for (const origin of bridgeAllowedDashboardOrigins()) {
    parts.push('--allow-origin', shellQuote(origin))
  }
  return parts.join(' ')
}

function recordingAgentDownloadConfig(): { url: string; managed: boolean } {
  const env = import.meta.env?.VITE_APEXLOAD_RECORDING_AGENT_DOWNLOAD_URL
  if (typeof env === 'string' && env.trim()) {
    return { url: env.trim(), managed: false }
  }
  return {
    url: `${trimTrailingSlash(API_ORIGIN)}${DEFAULT_RECORDING_AGENT_DOWNLOAD_PATH}`,
    managed: true,
  }
}


function bridgePairingErrorMessage(error: any, bridgeOrigin: string): string {
  const message = error?.message || ''
  if (error instanceof TypeError || /failed to fetch|networkerror/i.test(message)) {
    return `Could not reach ${bridgeOrigin}. Confirm ${RECORDING_AGENT_COMMAND} is running; if this dashboard is served over HTTPS, the browser may also be blocking localhost private-network access.`
  }
  return message || 'Failed to pair with the local recording bridge'
}

function bridgeVerificationErrorMessage(error: any, bridgeOrigin: string): string {
  const message = error?.message || ''
  if (error instanceof TypeError || /failed to fetch|networkerror/i.test(message)) {
    return `APEX Load Recording Agent is not reachable at ${bridgeOrigin}. Download and run the agent command, then verify again. If this dashboard is served over HTTPS, the browser may also be blocking localhost private-network access.`
  }
  return message || `APEX Load Recording Agent is not reachable at ${bridgeOrigin}`
}

function copyTextWithSelectionFallback(text: string): boolean {
  if (typeof document === 'undefined' || !document.body) return false
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  textarea.style.top = '0'
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  try {
    return document.execCommand?.('copy') === true
  } catch {
    return false
  } finally {
    document.body.removeChild(textarea)
  }
}

async function writeClipboardText(text: string): Promise<void> {
  let clipboardError: unknown
  try {
    const clipboard = typeof navigator !== 'undefined' ? navigator.clipboard : undefined
    if (clipboard?.writeText) {
      await clipboard.writeText(text)
      return
    }
  } catch (error) {
    clipboardError = error
  }
  if (copyTextWithSelectionFallback(text)) return
  if (clipboardError instanceof Error) {
    throw new Error('Clipboard is blocked in this browser. Select the terminal command and press Cmd+C or Ctrl+C.')
  }
  throw new Error('Clipboard access is not available in this browser')
}

// The desktop shell embeds the dashboard in a Wails WebView where window.open
// escapes to the system browser, so the popup panel is web-only there. This is
// deliberately origin-based (not bridge-based): it gates webview popup
// behavior, not the presence of bound methods.
function isWailsShell(): boolean {
  return typeof window !== 'undefined' && isWailsLocation(window.location)
}

function browserRecorderPanelURL(sessionId: string): string {
  const fragment = `/recorder-panel?session=${encodeURIComponent(sessionId)}`
  if (typeof window === 'undefined') return `#${fragment}`
  const url = new URL(window.location.href)
  url.hash = fragment
  return url.toString()
}

interface RecordingStudioProps {
  activeProject?: Project | null
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition) => void
  onClose?: () => void
}

export default function RecordingStudio({ activeProject, onImported, onEdit, onClose }: RecordingStudioProps) {
  const api = useApi()
  const [sessions, setSessions] = useState<RecordingSession[]>([])
  const [activeSession, setActiveSession] = useState<RecordingSession | null>(null)
  const [certificateFormatView, setCertificateFormatView] = useState<'crt' | 'pem'>('crt')
  const [proxyPlatformView, setProxyPlatformView] = useState<'macos' | 'windows'>('macos')
  const [proxyAddr, setProxyAddr] = useState('')
  const [caCertPath, setCaCertPath] = useState('')
  const [macOSNetworkService, setMacOSNetworkService] = useState(DEFAULT_MACOS_NETWORK_SERVICE)
  const [sessionName, setSessionName] = useState('')
  const [includeHosts, setIncludeHosts] = useState('')
  const [excludeHosts, setExcludeHosts] = useState('')
  const [captureCookies, setCaptureCookies] = useState(false)
  const [feedback, setFeedback] = useState('')
  const [feedbackType, setFeedbackType] = useState<'success' | 'error'>('success')
  const [selectedSession, setSelectedSession] = useState<RecordingSession | null>(null)
  const [selectedRequest, setSelectedRequest] = useState<RecordedRequest | null>(null)
  const [requestDraft, setRequestDraft] = useState({
    step_name: '',
    transaction_name: '',
    note: '',
    think_time_ms: 0,
    excluded: false,
  })
  const [showSetup, setShowSetup] = useState(false)
  const [parameterizeInputs, setParameterizeInputs] = useState(false)
  const [browserAvailability, setBrowserAvailability] = useState<BrowserRecorderAvailability | null>(null)
  const [browserSessions, setBrowserSessions] = useState<BrowserRecorderSession[]>([])
  const [browserPanelSessionId, setBrowserPanelSessionId] = useState<string | null>(null)
  const [browserName, setBrowserName] = useState('')
  const [browserStartScheme, setBrowserStartScheme] = useState<HttpURLScheme>(DEFAULT_HTTP_URL_SCHEME)
  const [browserStartAddress, setBrowserStartAddress] = useState('')
  const [browserCaptureHAR, setBrowserCaptureHAR] = useState(true)
  const [browserDevice, setBrowserDevice] = useState('')
  const [browserDevices, setBrowserDevices] = useState<{ name: string; platform: string }[]>([])
  const [browserScreenshots, setBrowserScreenshots] = useState(false)
  const [browserWSPayloads, setBrowserWSPayloads] = useState(false)
  const [browserStorageState, setBrowserStorageState] = useState('')
  const [browserExcludeHosts, setBrowserExcludeHosts] = useState('')
  const [browserSidebar, setBrowserSidebar] = useState(true)
  const [browserOptionsOpen, setBrowserOptionsOpen] = useState(false)
  const [installState, setInstallState] = useState<{ status: string; error?: string } | null>(null)
  const [bridgeOrigin, setBridgeOrigin] = useState(DEFAULT_BRIDGE_ORIGIN)
  const [bridgeToken, setBridgeToken] = useState(initialBridgePairingToken)
  const [bridgePairing, setBridgePairing] = useState(false)
  const [bridgeVerifying, setBridgeVerifying] = useState(false)
  const [bridgeStatus, setBridgeStatus] = useState('')
  const [bridgeStatusTone, setBridgeStatusTone] = useState<'info' | 'success' | 'error'>('info')
  const [recordingAgentPlatform, setRecordingAgentPlatform] = useState('')
  const [recordingAgentPlatformDetecting, setRecordingAgentPlatformDetecting] = useState(true)
  const [recordingAgentChecksum, setRecordingAgentChecksum] = useState<RecordingAgentChecksum | null>(null)
  const [recordingAgentChecksumStatus, setRecordingAgentChecksumStatus] = useState('')
  const convertTask = useAsyncTask<RecorderImportResult>()
  const salesforceTask = useAsyncTask<SalesforceImportResult>()
  const refreshInFlightRef = useRef(false)
  const activeSessionRef = useRef<RecordingSession | null>(null)

  const proxyPort = recorderProxyPort(proxyAddr)
  const proxyHost = recorderProxyHost(proxyAddr, currentDashboardOrigin())
  const proxyHostIsLocal = recorderProxyHostIsLocal(proxyHost)

  useEffect(() => {
    activeSessionRef.current = activeSession
  }, [activeSession])

  useEffect(() => {
    let cancelled = false
    setRecordingAgentPlatformDetecting(true)
    detectPlatformFromNavigator().then(platform => {
      if (cancelled) return
      setRecordingAgentPlatform(platform || '')
      setRecordingAgentPlatformDetecting(false)
    }).catch(() => {
      if (cancelled) return
      setRecordingAgentPlatform('')
      setRecordingAgentPlatformDetecting(false)
    })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    const agentDownload = recordingAgentDownloadConfig()
    const shouldShowBridgeDownload = browserAvailability?.available === false || !!api.browserRecorderBridge
    if (!agentDownload.managed || !shouldShowBridgeDownload || !recordingAgentPlatform) {
      setRecordingAgentChecksum(null)
      setRecordingAgentChecksumStatus('')
      return
    }

    let cancelled = false
    setRecordingAgentChecksum(null)
    setRecordingAgentChecksumStatus('Checking SHA-256...')
    apiFetch(`/downloads/recording-agent/latest/checksum?platform=${encodeURIComponent(recordingAgentPlatform)}`)
      .then(async response => {
        if (cancelled) return
        if (!response.ok) {
          setRecordingAgentChecksumStatus('SHA-256 unavailable until this platform package is staged.')
          return
        }
        const payload = await response.json() as RecordingAgentChecksum
        if (cancelled) return
        setRecordingAgentChecksum(payload)
        setRecordingAgentChecksumStatus('')
      })
      .catch(() => {
        if (!cancelled) setRecordingAgentChecksumStatus('SHA-256 unavailable until this platform package is staged.')
      })
    return () => {
      cancelled = true
    }
  }, [api.browserRecorderBridge, browserAvailability?.available, recordingAgentPlatform])

  const handleCopyCAPath = async () => {
    if (!caCertPath) return
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error('Clipboard access is not available in this browser')
      }
      await navigator.clipboard.writeText(caCertPath)
      showFeedback('CA certificate path copied')
    } catch (e: any) {
      showFeedback(e.message || 'Failed to copy CA certificate path', 'error')
    }
  }

  const handleDownloadCA = async (format: 'crt' | 'pem' = 'crt') => {
    try {
      const url = format === 'pem' ? '/recorder/ca.pem?format=pem' : '/recorder/ca.pem'
      const res = await apiFetch(url)
      if (!res.ok) throw new Error('Failed to download CA certificate')
      const blob = await res.blob()
      const filename = format === 'pem' ? 'apexload-ca.pem' : 'apexload-ca.crt'
      downloadBlob(blob, filename)
    } catch (e: any) {
      showFeedback(e.message || 'Failed to download CA certificate', 'error')
    }
  }

  const handleDownloadRecordingAgent = async (event: MouseEvent<HTMLAnchorElement>, managedDownload: boolean) => {
    if (!managedDownload) return
    event.preventDefault()
    if (!recordingAgentPlatform) {
      showFeedback('Choose a Recording Agent platform before downloading.', 'error')
      return
    }
    try {
      const res = await apiFetch(`/downloads/recording-agent/latest?platform=${encodeURIComponent(recordingAgentPlatform)}`)
      const contentType = (res.headers.get('content-type') || '').toLowerCase()
      if (!res.ok) throw new Error('Recording Agent package is not available from this controller')
      if (contentType.includes('text/html')) {
        throw new Error('Recording Agent download endpoint returned the dashboard instead of an agent archive. Restart the controller with the latest build and stage the agent packages.')
      }
      const blob = await res.blob()
      downloadBlob(blob, downloadFilenameFromDisposition(res.headers.get('content-disposition'), 'apexload-recording-agent.tar.gz'))
      const sum = res.headers.get('x-content-sha256') || recordingAgentChecksum?.sha256
      showFeedback(sum
        ? `APEX Load Recording Agent download started (${platformLabel(recordingAgentPlatform)}). SHA-256: ${sum}`
        : `APEX Load Recording Agent download started (${platformLabel(recordingAgentPlatform)})`)
    } catch (e: any) {
      showFeedback(e.message || 'Failed to download the APEX Load Recording Agent', 'error')
    }
  }

  const copyProxyCommand = async (command: string, successMessage: string, failureMessage: string) => {
    try {
      await writeClipboardText(command)
      showFeedback(successMessage)
    } catch (e: any) {
      showFeedback(e.message || failureMessage, 'error')
    }
  }

  const ensureBridgeToken = (): string => {
    if (bridgeToken) return bridgeToken
    try {
      const token = generateBridgePairingToken()
      setBridgeToken(token)
      return token
    } catch (e: any) {
      showFeedback(e.message || 'Failed to generate bridge pairing token', 'error')
      return ''
    }
  }

  const handleCopyBridgeCommand = async () => {
    ensureBridgeToken()
    await copyProxyCommand(
      bridgeRecordAgentCommand(bridgeOrigin),
      'Terminal command copied. Paste it into a local terminal, then paste the pairing token at the prompt.',
      'Failed to copy local bridge command',
    )
  }

  const handleCopyBridgeToken = async () => {
    const token = ensureBridgeToken()
    if (!token) return
    await copyProxyCommand(
      token,
      'Pairing token copied. Paste it into the local agent prompt.',
      'Failed to copy pairing token',
    )
  }

  const handleRegenerateBridgeToken = () => {
    try {
      setBridgeToken(generateBridgePairingToken())
      setBridgeStatus('')
      setBridgeStatusTone('info')
      showFeedback('New local bridge pairing token generated')
    } catch (e: any) {
      showFeedback(e.message || 'Failed to generate bridge pairing token', 'error')
    }
  }

  const handleCopyMacOSProxyCommand = async (mode: 'start' | 'restore') => {
    const command = mode === 'start'
      ? buildMacOSStartProxyCommand(proxyPort, macOSNetworkService, proxyHost)
      : buildMacOSRestoreProxyCommand(macOSNetworkService)
    await copyProxyCommand(
      command,
      mode === 'start' ? 'macOS start proxy command copied' : 'macOS restore proxy command copied',
      'Failed to copy macOS proxy command',
    )
  }

  const handleCopyWindowsProxyCommand = async (mode: 'start' | 'restore') => {
    const command = mode === 'start'
      ? buildWindowsStartProxyCommand(proxyPort, proxyHost)
      : buildWindowsRestoreProxyCommand()
    await copyProxyCommand(
      command,
      mode === 'start' ? 'Windows start proxy command copied' : 'Windows restore proxy command copied',
      'Failed to copy Windows proxy command',
    )
  }

  const handleOpenMacOSNetworkSettings = () => {
    window.location.href = 'x-apple.systempreferences:com.apple.Network-Settings.extension'
  }

  const handleOpenWindowsProxySettings = () => {
    window.location.href = 'ms-settings:network-proxy'
  }

  const showFeedback = (msg: string, type: 'success' | 'error' = 'success') => {
    setFeedback(msg)
    setFeedbackType(type)
    setTimeout(() => setFeedback(''), 4000)
  }

  const refresh = useCallback(async () => {
    if (refreshInFlightRef.current) return
    if (typeof document !== 'undefined' && document.hidden) return
    refreshInFlightRef.current = true
    try {
      const [listRes, statusRes, browserListRes] = await Promise.all([
        api.listRecordings(activeProject?.id),
        api.getRecorderStatus(),
        api.listBrowserRecordings(activeProject?.id).catch(() => ({ sessions: [], count: 0 })),
      ])
      const recorderStatus = statusRes as RecorderStatus
      setSessions(listRes.sessions || [])
      setBrowserSessions(browserListRes.sessions || [])
      setProxyAddr(recorderStatus.proxy_addr || '')
      setCaCertPath(recorderStatus.ca_cert_path || '')
      let liveBrowser = recorderStatus.browser?.active
      if (api.browserRecorderBridge) {
        try {
          const bridgeRes = await api.getBrowserRecorderBridgeStatus()
          if (bridgeRes?.bridge) {
            setBrowserAvailability(bridgeRes.bridge.availability)
            setInstallState(null)
            liveBrowser = bridgeRes.bridge.active || null
            setBridgeStatus(`Connected to ${api.browserRecorderBridge.origin}`)
          }
        } catch (e: any) {
          setBrowserAvailability({ available: false, reason: bridgePairingErrorMessage(e, api.browserRecorderBridge.origin) })
          setBridgeStatus('Local recording bridge is not responding')
        }
      } else if (recorderStatus.browser) {
        setBrowserAvailability(recorderStatus.browser.availability)
        setInstallState(recorderStatus.browser.install ?? null)
      } else {
        setBrowserAvailability(MISSING_BROWSER_RECORDER_CAPABILITY)
        setInstallState(null)
      }
      if (liveBrowser?.id) {
        // Adopt a recording started elsewhere (popup, another tab, reload).
        setBrowserPanelSessionId(current => current ?? liveBrowser.id)
      }
      if (recorderStatus.active) {
        const current = activeSessionRef.current
        const full = await api.getRecording(
          recorderStatus.active.id,
          current?.id === recorderStatus.active.id && current.last_seq
            ? { includeRequests: true, since: current.last_seq }
            : { includeRequests: true },
        )
        setActiveSession(prev => mergeRecordingSession(prev, full))
      } else {
        setActiveSession(null)
      }
    } catch { /* API not ready */ }
    finally {
      refreshInFlightRef.current = false
    }
  }, [api, activeProject?.id])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 2000)
    return () => clearInterval(interval)
  }, [refresh])

  useEffect(() => {
    if (!selectedRequest) return
    setRequestDraft({
      step_name: selectedRequest.step_name || '',
      transaction_name: selectedRequest.transaction_name || '',
      note: selectedRequest.note || '',
      think_time_ms: selectedRequest.think_time_ms || 0,
      excluded: !!selectedRequest.excluded,
    })
  }, [selectedRequest])

  // Load device presets once for the launcher.
  useEffect(() => {
    api.listBrowserRecorderDevices()
      .then(res => setBrowserDevices(res.devices || []))
      .catch(() => { /* devices are optional */ })
  }, [api])

  const handleInstallBrowsers = async () => {
    try {
      const res = await api.installBrowserRecorder()
      setInstallState(res.install)
      showFeedback('Installing the recording browser — this can take a few minutes')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handlePairBridge = async () => {
    const origin = bridgeOrigin.trim().replace(/\/+$/, '')
    if (!/^https?:\/\/.+/i.test(origin)) {
      const message = `Enter the local bridge URL used by ${RECORDING_AGENT_COMMAND}`
      setBridgeStatusTone('error')
      setBridgeStatus(message)
      showFeedback(message, 'error')
      return
    }
    const token = ensureBridgeToken()
    if (!token) return
    setBridgePairing(true)
    setBridgeStatusTone('info')
    setBridgeStatus('')
    try {
      const paired = await api.pairBrowserRecorderBridge(origin, token)
      setBridgeStatusTone('success')
      setBridgeStatus(`Connected to ${origin}`)
      setBrowserAvailability(paired.status.availability)
      setInstallState(null)
      showFeedback('Local browser recording bridge paired')
    } catch (e: any) {
      const message = bridgePairingErrorMessage(e, origin)
      setBridgeStatusTone('error')
      setBridgeStatus(message)
      showFeedback(message, 'error')
    } finally {
      setBridgePairing(false)
    }
  }

  const handleVerifyBridgeAgent = async () => {
    const origin = bridgeOrigin.trim().replace(/\/+$/, '')
    if (!/^https?:\/\/.+/i.test(origin)) {
      setBridgeStatusTone('error')
      setBridgeStatus(`Enter the local bridge URL used by ${RECORDING_AGENT_COMMAND}`)
      return
    }
    setBridgeVerifying(true)
    setBridgeStatusTone('info')
    setBridgeStatus(`Checking APEX Load Recording Agent at ${origin}...`)
    try {
      await api.verifyBrowserRecorderBridgeAgent(origin)
      setBridgeOrigin(origin)
      setBridgeStatusTone('success')
      setBridgeStatus(`APEX Load Recording Agent is running at ${origin}. Click Pair Local Bridge to connect.`)
      showFeedback('APEX Load Recording Agent verified')
    } catch (e: any) {
      const message = bridgeVerificationErrorMessage(e, origin)
      setBridgeStatusTone('error')
      setBridgeStatus(message)
      showFeedback(message, 'error')
    } finally {
      setBridgeVerifying(false)
    }
  }

  const handleDisconnectBridge = () => {
    api.clearBrowserRecorderBridge()
    setBridgeStatusTone('info')
    setBridgeStatus('')
    showFeedback('Local recording bridge disconnected')
    refresh()
  }

  const handleBrowserStartAddressChange = (value: string) => {
    const normalized = normalizeHttpURLInput(value, browserStartScheme)
    setBrowserStartScheme(normalized.scheme)
    setBrowserStartAddress(normalized.address)
  }

  const handleStartBrowserRecording = async () => {
    const startURL = composeHttpURL(browserStartScheme, browserStartAddress)
    if (!startURL) {
      showFeedback('Enter an http(s) URL to record', 'error')
      return
    }
    try {
      const res = await api.startBrowserRecording({
        name: browserName.trim() || 'Browser Recording',
        start_url: startURL,
        project_id: activeProject?.id,
        capture_har: browserCaptureHAR,
        device_preset: browserDevice || undefined,
        capture_screenshots: browserScreenshots,
        capture_ws_payloads: browserWSPayloads,
        storage_state_path: browserStorageState.trim() || undefined,
        sidebar: browserSidebar,
        exclude_hosts: browserExcludeHosts.trim()
          ? browserExcludeHosts.split(',').map(h => h.trim()).filter(Boolean)
          : undefined,
      })
      const sessionId = res.session.id
      setBrowserPanelSessionId(sessionId)
      const panelURL = browserRecorderPanelURL(sessionId)
      if (!isWailsShell()) {
        try {
          await api.openBrowserRecordingControlPanel(sessionId, panelURL)
          showFeedback('Recording browser launched — control panel opened beside it')
        } catch {
          // Older agents/controllers do not expose the same-browser companion
          // tab endpoint. Keep the separate popup as a fallback.
          window.open(
            panelURL,
            'apexload-browser-recorder',
            'width=440,height=760',
          )
          showFeedback('Recording browser launched — interact with the page, then stop from the control panel')
        }
      } else {
        showFeedback('Recording browser launched — interact with the page, then stop from the control panel')
      }
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleOpenBrowserRecordingSession = (session: BrowserRecorderSession) => {
    setBrowserPanelSessionId(session.id)
    showFeedback(`Opened browser recording "${session.name}"`)
  }

  const handleDeleteBrowserRecordingSession = async (session: BrowserRecorderSession, event?: MouseEvent) => {
    event?.stopPropagation()
    if (typeof window !== 'undefined' && window.confirm && !window.confirm('Delete this stopped browser recording? This cannot be undone.')) {
      return
    }
    try {
      await api.deleteBrowserRecording(session.id)
      if (browserPanelSessionId === session.id) {
        setBrowserPanelSessionId(null)
      }
      setBrowserSessions(prev => prev.filter(item => item.id !== session.id))
      showFeedback(`Deleted browser recording "${session.name}"`)
      refresh()
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleStart = async () => {
    try {
      const data: any = {
        name: sessionName || 'Recording Session',
        project_id: activeProject?.id,
        exclude_static: true,
        capture_cookies: captureCookies,
      }
      if (includeHosts.trim()) {
        data.include_hosts = includeHosts.split(',').map((h: string) => h.trim()).filter(Boolean)
      }
      if (excludeHosts.trim()) {
        data.exclude_hosts = excludeHosts.split(',').map((h: string) => h.trim()).filter(Boolean)
      }
      const res = await api.startRecording(data)
      setActiveSession(res.session)
      showFeedback('Recording started — configure your browser proxy and navigate the target site')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleStop = async () => {
    if (!activeSession) return
    try {
      await api.stopRecording(activeSession.id)
      showFeedback(`Recording stopped — ${activeSession.request_count ?? activeSession.requests?.length ?? 0} requests captured`)
      setActiveSession(null)
      refresh()
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handlePauseResume = async () => {
    if (!activeSession) return
    try {
      const next = activeSession.status === 'paused'
        ? await api.resumeRecording(activeSession.id)
        : await api.pauseRecording(activeSession.id)
      setActiveSession(next)
      showFeedback(next.status === 'paused' ? 'Recording paused' : 'Recording resumed')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

	  const replaceRequest = (sessionId: string, updated: RecordedRequest) => {
	    const apply = (session: RecordingSession | null) => {
	      if (!session || session.id !== sessionId) return session
	      if (!session.requests) return session
	      return {
	        ...session,
	        requests: session.requests.map(req => req.seq === updated.seq ? updated : req),
	      }
    }
    setSelectedSession(apply)
    setActiveSession(apply)
    setSessions(prev => prev.map(session => apply(session) || session))
    setSelectedRequest(current => current?.seq === updated.seq ? updated : current)
  }

  const handleSelectSession = async (session: RecordingSession) => {
    if (selectedSession?.id === session.id) {
      setSelectedSession(null)
      setSelectedRequest(null)
      return
    }
    setSelectedRequest(null)
    if (session.requests && session.requests.length > 0) {
      setSelectedSession(session)
      return
    }
    try {
      const full = await api.getRecording(session.id, { includeRequests: true })
      setSelectedSession({ ...full, requests: full.requests || [] })
    } catch {
      setSelectedSession(session)
    }
  }

  const handleToggleRequestExcluded = async (session: RecordingSession, req: RecordedRequest) => {
    try {
      const updated = await api.updateRecordedRequest(session.id, req.seq, { excluded: !req.excluded })
      replaceRequest(session.id, updated)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleSaveRequestDetails = async () => {
    if (!selectedSession || !selectedRequest) return
    try {
      const updated = await api.updateRecordedRequest(selectedSession.id, selectedRequest.seq, requestDraft)
      replaceRequest(selectedSession.id, updated)
      showFeedback(`Request #${updated.seq} updated`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handlePreviewConversion = async (session: RecordingSession) => {
    try {
      const result = await convertTask.run(
        () => api.previewRecordingConversion(session.id, session.name, parameterizeInputs),
        { clearValue: true },
      )
      showFeedback(`Preview generated with ${result.script?.actions?.length || 0} actions and ${result.correlations?.length || 0} correlations`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleExportHAR = async (session: RecordingSession) => {
    try {
      const res = await apiFetch(`/recorder/${session.id}/export?format=har`)
      if (!res.ok) {
        const payload = await res.json().catch(() => ({ error: 'Failed to export HAR' }))
        throw new Error(payload.error || 'Failed to export HAR')
      }
      const blob = await res.blob()
      const safeName = safeRecordingFileName(session.name)
      downloadBlob(blob, `${safeName}.har`)
      showFeedback(`Exported "${session.name}" as HAR`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleReviewSalesforceSession = async (session: RecordingSession) => {
    try {
      const result = await salesforceTask.run(async () => {
        const res = await apiFetch(`/recorder/${session.id}/export?format=har`)
        if (!res.ok) {
          const payload = await res.json().catch(() => ({ error: 'Failed to export HAR' }))
          throw new Error(payload.error || 'Failed to export HAR')
        }
        const blob = await res.blob()
        const file = new File([blob], `${safeRecordingFileName(session.name)}.har`, { type: blob.type || 'application/json' })
        return api.importSalesforce(file, {
          name: `${session.name} Salesforce HTTP`,
          base_url: session.base_url,
          project_id: activeProject?.id,
        })
      }, { clearValue: true })
      showFeedback(`Salesforce HTTP draft generated with ${result.script?.actions?.length || 0} actions`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleConvert = async (session: RecordingSession) => {
    try {
      const result = await convertTask.run(
        () => api.convertRecording(session.id, session.name, parameterizeInputs),
        { clearValue: true },
      )
      showFeedback(`Script "${result.script?.name || session.name}" created with ${result.correlations?.length || 0} correlations detected`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const buildReviewedScript = (): ScriptDefinition | null => {
    const script = recorderImportReviewScript(convertTask.value)
    if (!script) {
      return null
    }
    return attachImportCorrelationSuggestions(script, 'recorder', convertTask.value?.correlations)
  }

  const handleEditConvertedScript = () => {
    const script = buildReviewedScript()
    if (!script) {
      return
    }
    onEdit?.(script)
  }

  const handleSaveConvertedScript = async () => {
    const script = buildReviewedScript()
    if (!script) {
      const stored = recorderImportStoredScript(convertTask.value)
      if (stored) {
        onImported?.({ scripts: [stored] })
        showFeedback(`Script "${stored.name}" is already in the script library`)
      }
      return
    }
    try {
      const stored = recorderImportStoredScript(convertTask.value)
      if (stored) {
        onImported?.({ scripts: [withFallbackPersistedScript(stored, script)] })
        showFeedback(`Script "${stored.name}" is already in the script library`)
        return
      }
      const savedScript = await api.createScript(script, activeProject?.id)
      showFeedback(`Script "${script.name}" created with ${convertTask.value?.correlations?.length || 0} correlations detected`)
      onImported?.({ scripts: [withFallbackPersistedScript(savedScript, script)] })
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleEditSalesforceScript = () => {
    const script = salesforceTask.value?.script || salesforceTask.value?.scripts?.[0]
    if (script) {
      onEdit?.(script)
    }
  }

  const handleSaveSalesforceScript = async () => {
    const script = salesforceTask.value?.script || salesforceTask.value?.scripts?.[0]
    if (!script) {
      return
    }
    try {
      const savedScript = await api.createScript(script, activeProject?.id)
      showFeedback(`Salesforce HTTP script "${script.name}" saved to the script library`)
      onImported?.({ scripts: [withFallbackPersistedScript(savedScript, script)] })
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleDelete = async (id: string) => {
    if (typeof window !== 'undefined' && window.confirm && !window.confirm('Delete this stopped recording session? This cannot be undone.')) {
      return
    }
    try {
      await api.deleteRecording(id)
      if (selectedSession?.id === id) setSelectedSession(null)
      refresh()
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const stoppedSessions = sessions.filter(s => s.status === 'stopped')
  const recentBrowserSessions = browserSessions
    .filter(s => s.status === 'stopped' || s.status === 'failed')
    .sort((a, b) => new Date(b.stopped_at || b.started_at).getTime() - new Date(a.stopped_at || a.started_at).getTime())
  const convertResult = convertTask.value
  const convertedStoredScript = recorderImportStoredScript(convertResult)
  const convertedReviewScript = recorderImportReviewScript(convertResult)
  const convertedScript = convertedReviewScript || convertedStoredScript
  const converting = convertTask.pending
  const salesforceResult = salesforceTask.value
  const salesforceScript = salesforceResult?.script || salesforceResult?.scripts?.[0]
  const reviewingSalesforce = salesforceTask.pending
  const browserUnavailableReason = browserAvailability?.reason?.trim() || ''
  const browserUnavailableCode = browserAvailability?.code?.trim() || ''
  const browserUnavailableDueToDisplay = browserUnavailableCode === 'display_missing' || browserUnavailableReason.toLowerCase().includes('no display detected')
  const canRetryBrowserInstall = browserRecorderInstallRetryable(browserAvailability, installState)
  const installStatusTone = installState ? browserRecorderInstallTone(installState.status) : 'info'
  const installStatusVisible = !!installState && installState.status !== 'idle'
  const bridgeCommand = bridgeRecordAgentCommand(bridgeOrigin)
  const agentDownload = recordingAgentDownloadConfig()
  const agentDownloadHref = agentDownload.managed && recordingAgentPlatform
    ? `${agentDownload.url}?platform=${encodeURIComponent(recordingAgentPlatform)}`
    : agentDownload.url

  return (
    <div className="page recording-studio">
      <div className="page-header">
        <div>
          <h2>Recording Studio</h2>
          <p className="page-subtitle">Record a browser session and convert it to a test script</p>
        </div>
        {onClose && <button type="button" className="btn btn-secondary" onClick={onClose}>Back</button>}
      </div>

      {feedback && (
        <div className={`rec-feedback ${feedbackType}`}>{feedback}</div>
      )}

      <div className="rec-recorder-options-grid">
        <div className="rec-controls-card rec-browser-launcher">
          <div className="rec-option-heading">
            <h3 className="rec-section-title">Record Browser Session</h3>
            <p>Launch a controlled Chromium session from this controller and capture user actions directly.</p>
          </div>
          {!browserAvailability?.available ? (
            <div className="rec-browser-unavailable" data-testid="browser-recorder-unavailable">
              <div className="rec-browser-unavailable-title">Browser recording is not available on this controller</div>
              <div>{browserUnavailableReason || 'Checking controller capability.'}</div>
              <div>
                {browserUnavailableDueToDisplay
                  ? 'Install cannot add a display to this controller. Run the controller on a desktop host with DISPLAY or WAYLAND_DISPLAY, or use the proxy recorder option instead.'
                  : 'Use the proxy recorder option instead.'}
              </div>
              {installStatusVisible && (
                <div className={`rec-install-status ${installStatusTone}`} role={installState.status === 'failed' ? 'alert' : 'status'}>
                  <strong>{browserRecorderInstallLabel(installState.status)}</strong>
                  {installState.error && <span>{installState.error}</span>}
                </div>
              )}
              {installState && canRetryBrowserInstall && (
                <button className="btn btn-secondary btn-sm" onClick={handleInstallBrowsers} disabled={installState.status === 'installing'}>
                  {installState.status === 'installing' ? 'Installing...' : 'Install Recording Browser'}
                </button>
              )}
              <div className="rec-bridge-panel" data-testid="browser-recorder-bridge-panel">
                <div className="rec-browser-unavailable-title">Use a local recording bridge</div>
                <div className="rec-bridge-steps">
                  <div className="rec-bridge-step">
                    <div className="rec-bridge-step-number">1</div>
                    <div className="rec-bridge-step-content">
                      <div className="rec-bridge-step-title">Download the Recording Agent</div>
                      {agentDownload.managed && (
                        <div className="rec-form-row rec-agent-platform-row">
                          <label className="rec-label">Platform</label>
                          <select
                            className="rec-input"
                            value={recordingAgentPlatform}
                            onChange={event => setRecordingAgentPlatform(event.target.value)}
                            aria-label="Recording Agent platform"
                          >
                            <option value="">{recordingAgentPlatformDetecting ? 'Detecting platform...' : 'Choose platform'}</option>
                            {CLIENT_DOWNLOAD_PLATFORMS.map(platform => (
                              <option key={platform.value} value={platform.value}>{platform.label}</option>
                            ))}
                          </select>
                        </div>
                      )}
                      <div className="rec-bridge-install-note">
                        <a
                          href={agentDownloadHref}
                          target="_blank"
                          rel="noreferrer"
                          onClick={event => handleDownloadRecordingAgent(event, agentDownload.managed)}
                        >
                          Download the APEX Load Recording Agent{agentDownload.managed && recordingAgentPlatform ? ` for ${platformLabel(recordingAgentPlatform)}` : ''}
                        </a>
                      </div>
                      {agentDownload.managed && (
                        <div className="rec-bridge-help">
                          {recordingAgentChecksum?.sha256
                            ? <>SHA-256 <code>{recordingAgentChecksum.sha256}</code>{recordingAgentChecksum.filename ? <> for <code>{recordingAgentChecksum.filename}</code></> : null}</>
                            : recordingAgentChecksumStatus || (recordingAgentPlatform ? 'SHA-256 will appear when the package manifest is staged.' : 'Choose a platform to show the package SHA-256.')}
                        </div>
                      )}
                    </div>
                  </div>
                  <div className="rec-bridge-step">
                    <div className="rec-bridge-step-number">2</div>
                    <div className="rec-bridge-step-content">
                      <div className="rec-bridge-step-title">Run the local command</div>
                      <div className="rec-bridge-step-desc">Paste this command into Terminal or PowerShell on your workstation.</div>
                        <div className="rec-bridge-terminal" data-testid="browser-recorder-bridge-terminal">
                          <div className="rec-bridge-terminal-header">
                            <span>Local terminal command</span>
                            <span>Token entered at prompt</span>
                          </div>
                          <div className="rec-bridge-command-row">
                            <span className="rec-terminal-prompt">$</span>
                            <code className="rec-command" data-testid="browser-recorder-bridge-command" tabIndex={0}>{bridgeCommand}</code>
                          </div>
                        <div className="rec-bridge-command-actions">
                          <button type="button" className="btn btn-secondary btn-sm" onClick={handleCopyBridgeCommand}>
                            Copy Terminal Command
                          </button>
                          <button type="button" className="rec-inline-action" onClick={handleRegenerateBridgeToken}>
                            Regenerate Token
                          </button>
                        </div>
                      </div>
                      <div className="rec-bridge-help">
                        When the agent asks, paste this pairing token. It is not included in the command or process arguments.
                      </div>
                      <div className="rec-bridge-command-row">
                        <span className="rec-terminal-prompt">#</span>
                        <code className="rec-command" data-testid="browser-recorder-bridge-token" tabIndex={0}>{bridgeToken}</code>
                      </div>
                      <div className="rec-bridge-command-actions">
                        <button type="button" className="btn btn-secondary btn-sm" onClick={handleCopyBridgeToken}>
                          Copy Pairing Token
                        </button>
                      </div>
                    </div>
                  </div>
                  <div className="rec-bridge-step">
                    <div className="rec-bridge-step-number">3</div>
                    <div className="rec-bridge-step-content">
                      <div className="rec-bridge-step-title">Verify the agent</div>
                      <div className="rec-form-row">
                        <label className="rec-label">Bridge URL</label>
                        <input
                          className="rec-input"
                          value={bridgeOrigin}
                          onChange={e => setBridgeOrigin(e.target.value)}
                          placeholder="http://127.0.0.1:49152"
                        />
                      </div>
                      <button className="btn btn-secondary btn-sm" onClick={handleVerifyBridgeAgent} disabled={bridgeVerifying}>
                        {bridgeVerifying ? 'Verifying...' : 'Verify Agent'}
                      </button>
                    </div>
                  </div>
                  <div className="rec-bridge-step">
                    <div className="rec-bridge-step-number">4</div>
                    <div className="rec-bridge-step-content">
                      <div className="rec-bridge-step-title">Pair the local bridge</div>
                      <button className="btn btn-secondary btn-sm" onClick={handlePairBridge} disabled={bridgePairing}>
                        {bridgePairing ? 'Pairing...' : 'Pair Local Bridge'}
                      </button>
                    </div>
                  </div>
                </div>
                {bridgeStatus && <div className={`rec-bridge-status ${bridgeStatusTone}`}>{bridgeStatus}</div>}
              </div>
            </div>
          ) : (
            <div className="rec-start-form">
              {api.browserRecorderBridge && (
                <div className="rec-bridge-connected" data-testid="browser-recorder-bridge-connected">
                  <span>Local bridge: {api.browserRecorderBridge.origin}</span>
                  <button type="button" className="rec-inline-action" onClick={handleDisconnectBridge}>Disconnect</button>
                </div>
              )}
              <div className="rec-form-row">
                <label className="rec-label">Start URL</label>
                <div className="rec-url-input-group">
                  <select
                    className="rec-input rec-url-scheme-select"
                    value={browserStartScheme}
                    onChange={e => setBrowserStartScheme(e.target.value as HttpURLScheme)}
                    aria-label="Start URL scheme"
                  >
                    <option value="https://">https://</option>
                    <option value="http://">http://</option>
                  </select>
                  <input
                    className="rec-input"
                    value={browserStartAddress}
                    onChange={e => handleBrowserStartAddressChange(e.target.value)}
                    placeholder="app.example.com/login"
                    aria-label="Start URL address"
                  />
                </div>
              </div>
              <div className="rec-form-row">
                <label className="rec-label">Session Name</label>
                <input
                  className="rec-input"
                  value={browserName}
                  onChange={e => setBrowserName(e.target.value)}
                  placeholder="e.g. Login & Checkout Flow"
                />
              </div>
              <div className="rec-form-row">
                <label className="rec-label">Device</label>
                <select className="rec-input" value={browserDevice} onChange={e => setBrowserDevice(e.target.value)} aria-label="Device preset">
                  <option value="">Desktop (default)</option>
                  {browserDevices.filter(d => d.name !== 'desktop_1080p').map(d => (
                    <option key={d.name} value={d.name}>{d.name.replace(/_/g, ' ')} ({d.platform})</option>
                  ))}
                </select>
              </div>
              <label className="rec-checkbox-row">
                <input
                  type="checkbox"
                  checked={browserCaptureHAR}
                  onChange={e => setBrowserCaptureHAR(e.target.checked)}
                />
                <span>Capture side-car HAR (network traffic for protocol-level scripting)</span>
              </label>

              <button type="button" className="rec-inline-action" onClick={() => setBrowserOptionsOpen(v => !v)}>
                {browserOptionsOpen ? 'Hide advanced options' : 'Advanced options'}
              </button>
              {browserOptionsOpen && (
                <div className="rec-browser-options">
                  <label className="rec-checkbox-row">
                    <input type="checkbox" checked={browserSidebar} onChange={e => setBrowserSidebar(e.target.checked)} />
                    <span>Show in-page advanced sidebar <span className="rec-hint">(section, transactions, notes, recent steps — uncheck for the minimal pause/assert toolbar)</span></span>
                  </label>
                  <label className="rec-checkbox-row">
                    <input type="checkbox" checked={browserScreenshots} onChange={e => setBrowserScreenshots(e.target.checked)} />
                    <span>Capture per-step screenshot thumbnails</span>
                  </label>
                  <label className="rec-checkbox-row">
                    <input type="checkbox" checked={browserWSPayloads} onChange={e => setBrowserWSPayloads(e.target.checked)} />
                    <span>Capture WebSocket frame payloads (scrubbed)</span>
                  </label>
                  <div className="rec-form-row">
                    <label className="rec-label">Exclude hosts <span className="rec-hint">(comma-separated, HAR noise filter)</span></label>
                    <input className="rec-input" value={browserExcludeHosts} onChange={e => setBrowserExcludeHosts(e.target.value)} placeholder="analytics.google.com, cdn.example.com" />
                  </div>
                  <div className="rec-form-row">
                    <label className="rec-label">Reuse session state <span className="rec-hint">(saved storage-state path, skip login)</span></label>
                    <input className="rec-input" value={browserStorageState} onChange={e => setBrowserStorageState(e.target.value)} placeholder="/path/to/state.json" />
                  </div>
                </div>
              )}

              <button
                className="btn btn-primary rec-start-btn"
                onClick={handleStartBrowserRecording}
                disabled={!!browserPanelSessionId}
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="12" r="10" /></svg>
                Record Browser
              </button>
              <div className="rec-browser-unavailable">
                A Chromium window opens on this machine with an in-page recording sidebar (sections, transactions, notes,
                recent steps, pause and verification picker) you can drag to any corner. Manage the session from there or
                the control panel docked below — which also opens as a compact popup window.
              </div>
            </div>
          )}

          {browserPanelSessionId && (
            <BrowserRecorderControlPanel
              sessionId={browserPanelSessionId}
              onImported={onImported}
              onEdit={onEdit}
              onDismiss={() => {
                setBrowserPanelSessionId(null)
                refresh()
              }}
            />
          )}

          {recentBrowserSessions.length > 0 && (
            <div className="rec-browser-session-section" data-testid="browser-recording-recovery-list">
              <div className="rec-feed-header">
                <span>Recent Browser Recordings</span>
                <span className="rec-feed-count">{recentBrowserSessions.length}</span>
              </div>
              <div className="rec-browser-session-list">
                {recentBrowserSessions.map(session => (
                  <div key={session.id} className="rec-browser-session-row">
                    <button
                      type="button"
                      className="rec-browser-session-main"
                      onClick={() => handleOpenBrowserRecordingSession(session)}
                    >
                      <span className="rec-session-name">{session.name || 'Browser Recording'}</span>
                      <span className="rec-session-meta">
                        <span>{session.step_count || 0} steps</span>
                        <span>{session.network_request_count || 0} requests</span>
                        <span>{new Date(session.stopped_at || session.started_at).toLocaleString()}</span>
                      </span>
                    </button>
                    <div className="rec-browser-session-actions">
                      <button type="button" className="btn btn-sm btn-secondary" onClick={() => handleOpenBrowserRecordingSession(session)}>Open</button>
                      <button type="button" className="btn btn-sm btn-danger" onClick={event => handleDeleteBrowserRecordingSession(session, event)}>Delete</button>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>

        <div className="rec-controls-card rec-proxy-recorder-option" data-testid="rec-proxy-recorder-option">
          <div className="rec-option-heading">
            <h3 className="rec-section-title">Advanced Proxy Recorder</h3>
            <p>Capture HTTP(S) traffic through the controller recording proxy when headed browser recording is unavailable.</p>
          </div>

      {/* Proxy Setup */}
      <div className="rec-setup-section">
        <button
          type="button"
          className="rec-setup-toggle"
          aria-expanded={showSetup}
          aria-controls="recording-proxy-setup-steps"
          onClick={() => setShowSetup(!showSetup)}
        >
          <div className="rec-proxy-header">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" />
              <line x1="12" y1="16" x2="12" y2="12" />
              <line x1="12" y1="8" x2="12.01" y2="8" />
            </svg>
            <span>Proxy Setup</span>
          </div>
          <svg
            className={`rec-chevron ${showSetup ? 'open' : ''}`}
            width="16" height="16" viewBox="0 0 24 24" fill="none"
            stroke="var(--text-muted)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
          >
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </button>

        {showSetup && (
          <div className="rec-setup-steps" id="recording-proxy-setup-steps">
            <div className="rec-step">
              <div className="rec-step-number">1</div>
              <div className="rec-step-content">
                <div className="rec-step-title">Download &amp; Install CA Certificate</div>
                <div className="rec-step-desc">
                  Install and trust the APEX Load CA certificate for SSL/TLS so the proxy can intercept HTTPS traffic.
                </div>
                <div className="rec-step-note">
                  Only trust this private recording CA on a workstation you control. The recorder reuses the same CA across controller restarts, so install it again only if you delete the CA files or change the recorder CA directory.
                  {caCertPath && (
                    <div className="rec-step-note-path">
                      <span>Current certificate path:</span>
                      <code>{caCertPath}</code>
                      <button type="button" className="rec-inline-action" onClick={handleCopyCAPath}>Copy path</button>
                    </div>
                  )}
                  <div className="rec-step-note-troubleshoot">
                    If the browser still reports <code>ERR_CERT_AUTHORITY_INVALID</code>, the CA is imported but not trusted for SSL/TLS, the browser is using a separate trust store, or an older APEX Load CA is still selected.
                  </div>
                </div>
                <div className="rec-download-row">
                  <PageViewToggle
                    value={certificateFormatView}
                    ariaLabel="Certificate format"
                    className="rec-certificate-format-toggle script-page-toggle"
                    onChange={value => setCertificateFormatView(value)}
                    options={[
                      { value: 'crt', label: 'CRT' },
                      { value: 'pem', label: 'PEM' },
                    ]}
                  />
                  <button className="rec-download-btn" onClick={() => handleDownloadCA(certificateFormatView)}>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                      <polyline points="7 10 12 15 17 10" />
                      <line x1="12" y1="15" x2="12" y2="3" />
                    </svg>
                    {certificateFormatView === 'crt' ? 'Download CA Certificate (.crt)' : 'Download CA Certificate (.pem)'}
                  </button>
                </div>
                <div className="rec-os-instructions">
                  {certificateFormatView === 'crt' ? (
                    <>
                      <div><span className="rec-os-label">macOS Chrome, Edge, Safari:</span> Download .crt &rarr; open Keychain Access &rarr; import into the System keychain &rarr; open &ldquo;APEX Load Recording Proxy CA&rdquo; &rarr; expand Trust &rarr; set &ldquo;When using this certificate&rdquo; or &ldquo;Secure Sockets Layer (SSL)&rdquo; to Always Trust &rarr; close the window, enter your password, and restart the browser.</div>
                      <div><span className="rec-os-label">Windows Chrome, Edge:</span> Download .crt &rarr; Install Certificate &rarr; Local Machine &rarr; place it in &ldquo;Trusted Root Certification Authorities&rdquo; &rarr; finish the wizard and restart the browser.</div>
                    </>
                  ) : (
                    <>
                      <div><span className="rec-os-label">Firefox:</span> Download PEM format &rarr; Settings &rarr; Privacy &amp; Security &rarr; Certificates &rarr; View Certificates &rarr; Authorities &rarr; Import &rarr; enable &ldquo;Trust this CA to identify websites&rdquo; &rarr; restart Firefox. If Firefox is configured to use system roots, restart after trusting the OS root instead.</div>
                      <div><span className="rec-os-label">Linux:</span> Download PEM format and add it to the browser or distro trust store used by your browser, then restart the browser.</div>
                    </>
                  )}
                  <div><span className="rec-os-label">Cleanup:</span> Remove older &ldquo;APEX Load Recording Proxy CA&rdquo; entries if more than one exists, then trust the current CA shown above.</div>
                </div>
              </div>
            </div>

            <div className="rec-step">
              <div className="rec-step-number">2</div>
              <div className="rec-step-content">
                <div className="rec-step-title">Configure Browser Proxy</div>
                <div className="rec-step-desc">
                  Route browser traffic through <code>{proxyHost}:{proxyPort}</code>
                </div>
                {!proxyHostIsLocal && (
                  <div className="rec-step-note rec-step-note-troubleshoot">
                    This controller is remote. Use the Recording Agent bridge for workstation-local browser capture unless an operator has explicitly exposed the proxy on <code>{proxyHost}</code>.
                  </div>
                )}
                <div className="rec-os-instructions">
                  <div>Use a browser extension like SwitchyOmega (Chrome) or FoxyProxy (Firefox) to route traffic through the proxy.</div>
                  <div><span className="rec-os-label">System proxy:</span> macOS System Settings &rarr; Network &rarr; Wi-Fi &rarr; Details &rarr; Proxies &rarr; Web Proxy (HTTP) &amp; Secure Web Proxy (HTTPS) &rarr; <code>{proxyHost}</code> port <code>{proxyPort}</code></div>
                  <div><span className="rec-os-label">Windows 11:</span> Settings &rarr; Network &amp; internet &rarr; Proxy &rarr; Manual proxy setup &rarr; <code>{proxyHost}</code> port <code>{proxyPort}</code></div>
                </div>
                <div className="rec-proxy-shortcut">
                  <PageViewToggle
                    value={proxyPlatformView}
                    ariaLabel="Proxy command platform"
                    className="rec-proxy-platform-toggle script-page-toggle"
                    onChange={value => setProxyPlatformView(value)}
                    options={[
                      { value: 'macos', label: 'macOS' },
                      { value: 'windows', label: 'Windows 11' },
                    ]}
                  />
                  {proxyPlatformView === 'macos' ? (
                    <div className="rec-proxy-platform">
                      <div className="rec-proxy-platform-title">macOS</div>
                      <label className="rec-proxy-service">
                        <span>Network service</span>
                        <input
                          className="rec-input"
                          aria-label="macOS network service"
                          value={macOSNetworkService}
                          onChange={e => setMacOSNetworkService(e.target.value)}
                        />
                      </label>
                      <div className="rec-shortcut-actions">
                        <button className="rec-download-alt" type="button" onClick={() => handleCopyMacOSProxyCommand('start')}>
                          Copy Start Proxy Command
                        </button>
                        <button className="rec-download-alt" type="button" onClick={() => handleCopyMacOSProxyCommand('restore')}>
                          Copy Restore Proxy Command
                        </button>
                        <button className="rec-download-alt" type="button" onClick={handleOpenMacOSNetworkSettings}>
                          Open Network Settings
                        </button>
                      </div>
                    </div>
                  ) : (
                    <div className="rec-proxy-platform">
                      <div className="rec-proxy-platform-title">Windows 11</div>
                      <div className="rec-shortcut-actions">
                        <button className="rec-download-alt" type="button" onClick={() => handleCopyWindowsProxyCommand('start')}>
                          Copy Windows Start Command
                        </button>
                        <button className="rec-download-alt" type="button" onClick={() => handleCopyWindowsProxyCommand('restore')}>
                          Copy Windows Restore Command
                        </button>
                        <button className="rec-download-alt" type="button" onClick={handleOpenWindowsProxySettings}>
                          Open Windows Proxy Settings
                        </button>
                      </div>
                    </div>
                  )}
                </div>
              </div>
            </div>

            <div className="rec-step">
              <div className="rec-step-number">3</div>
              <div className="rec-step-content">
                <div className="rec-step-title">Start Recording</div>
                <div className="rec-step-desc">
                  Click &ldquo;Start Recording&rdquo; below, then browse the target website. All HTTP and HTTPS traffic will be captured.
                </div>
              </div>
            </div>

            <div className="rec-step">
              <div className="rec-step-number">4</div>
              <div className="rec-step-content">
                <div className="rec-step-title">Stop &amp; Convert</div>
                <div className="rec-step-desc">
                  When done, stop the recording and convert to a test script. Remember to disable the proxy.
                </div>
              </div>
            </div>
          </div>
        )}
      </div>

      <div className="rec-proxy-run-panel">
        <h3 className="rec-section-title">
          {activeSession ? (
            <>
              <span className="rec-live-dot" />
              {activeSession.status === 'paused' ? 'Paused' : 'Recording'}: {activeSession.name}
            </>
          ) : (
            'New Recording'
          )}
        </h3>

        {!activeSession ? (
          <div className="rec-start-form">
            <div className="rec-form-row">
              <label className="rec-label">Session Name</label>
              <input
                className="rec-input"
                value={sessionName}
                onChange={e => setSessionName(e.target.value)}
                placeholder="e.g. Login & Checkout Flow"
              />
            </div>
            <div className="rec-form-row">
              <label className="rec-label">Include Hosts <span className="rec-hint">(comma-separated, optional)</span></label>
              <input
                className="rec-input"
                value={includeHosts}
                onChange={e => setIncludeHosts(e.target.value)}
                placeholder="e.g. api.example.com, app.example.com"
              />
            </div>
            <div className="rec-form-row">
              <label className="rec-label">Exclude Hosts <span className="rec-hint">(comma-separated, optional)</span></label>
              <input
                className="rec-input"
                value={excludeHosts}
                onChange={e => setExcludeHosts(e.target.value)}
                placeholder="e.g. analytics.google.com, cdn.example.com"
              />
            </div>
            <label className="rec-checkbox-row">
              <input
                type="checkbox"
                checked={captureCookies}
                onChange={e => setCaptureCookies(e.target.checked)}
              />
              <span>Capture cookies (may include secrets)</span>
            </label>
            <button className="btn btn-primary rec-start-btn" onClick={handleStart}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="12" r="10" /></svg>
              Start Recording
            </button>
          </div>
        ) : (
          <div className="rec-active-panel">
            <div className="rec-active-stats">
              <div className="rec-stat">
                <span className="rec-stat-value">{activeSession.request_count ?? activeSession.requests?.length ?? 0}</span>
                <span className="rec-stat-label">Requests</span>
              </div>
              <div className="rec-stat">
                <span className="rec-stat-value">{activeSession.base_url || '—'}</span>
                <span className="rec-stat-label">Base URL</span>
              </div>
              <div className="rec-stat">
                <span className="rec-stat-value">{new Date(activeSession.started_at).toLocaleTimeString()}</span>
                <span className="rec-stat-label">Started</span>
              </div>
            </div>

            {/* Live request feed */}
            {activeSession.requests && activeSession.requests.length > 0 && (
              <div className="rec-live-feed">
                <div className="rec-feed-header">
                  <span>Live Requests</span>
                  <span className="rec-feed-count">{activeSession.request_count ?? activeSession.requests.length}</span>
                </div>
                <div className="rec-feed-list">
                  {[...activeSession.requests].reverse().slice(0, 20).map(req => (
                    <div key={req.seq} className="rec-feed-item">
                      <span className={`rec-method ${req.method.toLowerCase()}`}>{req.method}</span>
                      <span className="rec-path" title={req.url}>{req.path}</span>
                      <span className={`rec-status ${req.status_code >= 400 ? 'error' : ''}`}>{req.status_code}</span>
                      <span className="rec-timing">{req.response_time_ms.toFixed(0)}ms</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            <button className="btn btn-danger rec-stop-btn" onClick={handleStop}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="4" y="4" width="16" height="16" rx="2" /></svg>
              Stop Recording
            </button>
            <button className="btn btn-secondary rec-stop-btn" onClick={handlePauseResume}>
              {activeSession.status === 'paused' ? 'Resume Recording' : 'Pause Recording'}
            </button>
            <button className="btn btn-secondary rec-stop-btn" type="button" onClick={() => handleCopyMacOSProxyCommand('restore')}>
              Copy Restore Proxy Command
            </button>
            <button className="btn btn-secondary rec-stop-btn" type="button" onClick={() => handleCopyWindowsProxyCommand('restore')}>
              Copy Windows Restore Command
            </button>
          </div>
        )}
      </div>
        </div>
      </div>

      {/* Previous Sessions */}
      {stoppedSessions.length > 0 && (
        <div className="rec-sessions-section">
          <h3 className="rec-section-title">Recorded Sessions</h3>
          <label className="rec-convert-option">
            <input
              type="checkbox"
              checked={parameterizeInputs}
              onChange={e => setParameterizeInputs(e.target.checked)}
            />
            Parameterize input values (promote recorded form/query inputs into a data source)
          </label>
          <div className="rec-sessions-list">
            {stoppedSessions.map(session => (
              <div
                key={session.id}
                className={`rec-session-card ${selectedSession?.id === session.id ? 'selected' : ''}`}
              >
                <button
                  type="button"
                  className="rec-session-select"
                  aria-pressed={selectedSession?.id === session.id}
                  onClick={() => handleSelectSession(session)}
                >
                  <span className="rec-session-header">
                    <span className="rec-session-name">{session.name}</span>
                    <span className="rec-session-count">{session.request_count ?? session.requests?.length ?? 0} requests</span>
                  </span>
                  <span className="rec-session-meta">
                    <span>{session.base_url || 'No base URL'}</span>
                    <span>{new Date(session.started_at).toLocaleString()}</span>
                  </span>
                </button>
                <div className="rec-session-actions">
                  <button
                    type="button"
                    className="btn btn-sm btn-primary"
                    onClick={(e) => { e.stopPropagation(); handleConvert(session) }}
                    disabled={converting}
                  >
                    {converting ? 'Converting...' : 'Convert to Script'}
                  </button>
                  <button
                    type="button"
                    className="btn btn-sm btn-secondary"
                    onClick={(e) => { e.stopPropagation(); handlePreviewConversion(session) }}
                    disabled={converting}
                  >
                    Review Script
                  </button>
                  {recordingLooksSalesforce(session) && (
                    <button
                      type="button"
                      className="btn btn-sm btn-secondary"
                      onClick={(e) => { e.stopPropagation(); handleReviewSalesforceSession(session) }}
                      disabled={reviewingSalesforce}
                    >
                      {reviewingSalesforce ? 'Reviewing...' : 'Review Salesforce HTTP'}
                    </button>
                  )}
                  <button
                    type="button"
                    className="btn btn-sm btn-secondary"
                    onClick={(e) => { e.stopPropagation(); handleExportHAR(session) }}
                  >
                    Export HAR
                  </button>
                  <button
                    type="button"
                    className="btn btn-sm btn-secondary"
                    style={{ color: 'var(--danger)' }}
                    onClick={(e) => { e.stopPropagation(); handleDelete(session.id) }}
                  >
                    Delete
                  </button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Selected session detail */}
      {selectedSession && (
        <div className="rec-detail-section">
          <h3 className="rec-section-title">
            Request Detail: {selectedSession.name}
            <span className="rec-detail-count">{selectedSession.request_count ?? selectedSession.requests?.length ?? 0} requests</span>
          </h3>
          <div className="rec-detail-table-wrap">
            <table className="rec-detail-table">
              <thead>
                <tr>
                  <th>#</th>
                  <th>Method</th>
                  <th>Path</th>
                  <th>Status</th>
                  <th>Time</th>
                  <th>Think</th>
                  <th>Type</th>
                  <th>Size</th>
                  <th>Use</th>
                </tr>
              </thead>
              <tbody>
                {selectedSession.requests?.map(req => (
                  <tr
                    key={req.seq}
                    className={`${req.is_static ? 'static' : ''} ${req.excluded ? 'static' : ''} ${selectedRequest?.seq === req.seq ? 'selected' : ''}`}
                  >
                    <td className="cell-mono">{req.seq}</td>
                    <td><span className={`rec-method ${req.method.toLowerCase()}`}>{req.method}</span></td>
                    <td className="cell-path" title={req.url}>
                      <button
                        type="button"
                        className="rec-request-select"
                        aria-pressed={selectedRequest?.seq === req.seq}
                        onClick={() => setSelectedRequest(req)}
                      >
                        {req.step_name || req.path}{req.query_string ? `?${req.query_string}` : ''}
                      </button>
                    </td>
                    <td className={req.status_code >= 400 ? 'text-danger' : ''}>{req.status_code}</td>
                    <td className="cell-mono">{req.response_time_ms.toFixed(0)}ms</td>
                    <td className="cell-mono">{req.think_time_ms > 0 ? `${req.think_time_ms}ms` : '—'}</td>
                    <td className="cell-muted">{req.resource_type || (req.content_type ? req.content_type.split(';')[0] : '—')}</td>
                    <td className="cell-mono">
                      {(req.body_size_bytes || 0) + (req.response_body_size_bytes || 0)}B
                      {(req.body_truncated || req.response_body_truncated) ? '*' : ''}
                    </td>
                    <td>
                      <button
                        type="button"
                        className="btn btn-sm btn-secondary"
                        onClick={(e) => { e.stopPropagation(); handleToggleRequestExcluded(selectedSession, req) }}
                      >
                        {req.excluded ? 'Include' : 'Exclude'}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {selectedRequest && (
            <div className="rec-request-editor">
              <div className="rec-editor-header">
                <div>
                  <span className={`rec-method ${selectedRequest.method.toLowerCase()}`}>{selectedRequest.method}</span>
                  <code>{selectedRequest.path}{selectedRequest.query_string ? `?${selectedRequest.query_string}` : ''}</code>
                </div>
                <span className="cell-mono">#{selectedRequest.seq}</span>
              </div>
              <div className="rec-editor-grid">
                <label className="rec-form-row">
                  <span className="rec-label">Step Name</span>
                  <input
                    className="rec-input"
                    value={requestDraft.step_name}
                    onChange={e => setRequestDraft(prev => ({ ...prev, step_name: e.target.value }))}
                    placeholder={`${selectedRequest.method} ${selectedRequest.path}`}
                  />
                </label>
                <label className="rec-form-row">
                  <span className="rec-label">Transaction</span>
                  <input
                    className="rec-input"
                    value={requestDraft.transaction_name}
                    onChange={e => setRequestDraft(prev => ({ ...prev, transaction_name: e.target.value }))}
                    placeholder="e.g. Login, Browse Catalog, Checkout"
                  />
                </label>
                <label className="rec-form-row">
                  <span className="rec-label">Think Time (ms)</span>
                  <input
                    className="rec-input"
                    type="number"
                    min="0"
                    value={requestDraft.think_time_ms}
                    onChange={e => setRequestDraft(prev => ({ ...prev, think_time_ms: Math.max(0, Number(e.target.value) || 0) }))}
                  />
                </label>
                <label className="rec-editor-check">
                  <input
                    type="checkbox"
                    checked={!requestDraft.excluded}
                    onChange={e => setRequestDraft(prev => ({ ...prev, excluded: !e.target.checked }))}
                  />
                  Use in generated script
                </label>
              </div>
              <label className="rec-form-row">
                <span className="rec-label">Note</span>
                <textarea
                  className="rec-input rec-textarea"
                  value={requestDraft.note}
                  onChange={e => setRequestDraft(prev => ({ ...prev, note: e.target.value }))}
                  placeholder="Capture intent, manual correlation notes, or cleanup reminders"
                />
              </label>
              <div className="rec-editor-actions">
                <button className="btn btn-primary btn-sm" onClick={handleSaveRequestDetails}>Save Request</button>
                <button className="btn btn-secondary btn-sm" onClick={() => setSelectedRequest(null)}>Close</button>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Convert Result */}
      {convertResult && convertedScript && (
        <div className="rec-convert-result">
          <h3 className="rec-section-title">Conversion Result</h3>
          <div className="rec-result-summary">
            <div className="rec-result-item">
              <span className="rec-result-label">Script</span>
              <span className="rec-result-value">{convertedScript.name}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Protocol</span>
              <span className="rec-result-value">{convertedScript.protocol}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Script ID</span>
              <code className="rec-result-value">{convertedStoredScript?.id || 'preview'}</code>
            </div>
          </div>
          <div className="rec-result-actions">
            <button className="btn btn-primary" type="button" onClick={handleSaveConvertedScript}>Save To Script Library</button>
            {onEdit && <button className="btn btn-secondary" type="button" onClick={handleEditConvertedScript} disabled={!convertedReviewScript}>Open In Visual Editor</button>}
          </div>

          {convertResult.correlations && convertResult.correlations.length > 0 && (
            <div className="rec-correlations">
              <h4 className="rec-subsection-title">Auto-Detected Correlations ({convertResult.correlations.length})</h4>
              <div className="rec-corr-list">
                {convertResult.correlations.map((c, i) => (
                  <div key={i} className="rec-corr-item">
                    <div className="rec-corr-header">
                      <code className="rec-corr-name">{`{{var.${c.name}}}`}</code>
                      {confidenceTone(c.confidence) && (
                        <span
                          className={`rec-corr-confidence tone-${confidenceTone(c.confidence)}`}
                          title={c.rule_id ? `rule: ${c.rule_id}` : undefined}
                        >
                          {Math.round((c.confidence as number) * 100)}% confidence
                        </span>
                      )}
                      <span className="rec-corr-type">{c.extract_type} / {c.source}</span>
                    </div>
                    <div className="rec-corr-detail">
                      <span>Extracted from request #{c.found_in_seq}</span>
                      <span>Used in #{c.used_in_seqs.join(', #')}</span>
                    </div>
                    {c.reasons && c.reasons.length > 0 && (
                      <div className="rec-corr-reasons">Why: {c.reasons.join('; ')}</div>
                    )}
                    <div className="rec-corr-sample">
                      Sample: <code>{c.sample_value}</code>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {convertResult.warnings && convertResult.warnings.length > 0 && (
            <div className="rec-warnings">
              {convertResult.warnings.map((w, i) => (
                <div key={i} className="rec-warning-item">{w}</div>
              ))}
            </div>
          )}
        </div>
      )}

      {salesforceResult && salesforceScript && (
        <div className="rec-convert-result">
          <h3 className="rec-section-title">Salesforce HTTP Draft</h3>
          <div className="rec-result-summary">
            <div className="rec-result-item">
              <span className="rec-result-label">Script</span>
              <span className="rec-result-value">{salesforceScript.name}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Protocol</span>
              <span className="rec-result-value">{salesforceScript.protocol}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">HAR Coverage</span>
              <span className="rec-result-value">{Math.round(salesforceResult.summary?.salesforce_review?.har_match_coverage?.coverage_pct || 0)}%</span>
            </div>
          </div>
          <div className="rec-result-actions">
            <button className="btn btn-primary" type="button" onClick={handleSaveSalesforceScript}>Save To Script Library</button>
            {onEdit && <button className="btn btn-secondary" type="button" onClick={handleEditSalesforceScript}>Open In Visual Editor</button>}
          </div>
          {(salesforceResult.warnings?.length || 0) > 0 && (
            <div className="rec-warnings">
              {salesforceResult.warnings.slice(0, 4).map((warning, idx) => (
                <div key={idx} className="rec-warning-item">{warning}</div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
