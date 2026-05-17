import { useCallback, useEffect, useRef, useState, type MouseEvent } from 'react'
import { useApi } from '../hooks/useApi'
import { useWebSocket } from '../hooks/useWebSocket'
import {
  BrowserReplayEvent,
  BrowserReplayReport,
  BrowserReplaySession,
  BrowserReplaySnapshot,
  StoredScript,
  WSMessage,
} from '../types'
import {
  type ContentCheckConfig,
  type DFETransform,
  type ScriptActionDefinition,
  type ScriptAssertionDefinition,
  type ScriptDefinition,
  type ScriptDataSource,
  type ScriptParameterDefinition,
} from '../types/scripts'
import BrowserReplayDebugger from './BrowserReplayDebugger'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import { extractSOAPFaultSummaryFromBody, formatSOAPFaultSummary } from './soapFaultUtils'
import {
  formatSnapshotRequestSection,
  formatSnapshotResponseSection,
} from '../utils/snapshotFormatUtils'
import { createZipBlob, downloadBlob, type ZipFileEntry } from '../utils/fileUtils'
import {
  EMPTY_ACTION,
  EMPTY_BROWSER_ACTION,
  EMPTY_DATA_SOURCE,
  EMPTY_JMS_ACTION,
  EMPTY_PARAMETER,
  EMPTY_SCRIPT,
  EMPTY_SOAP_ACTION,
  buildReplayStartPayload,
  buildReplayVariableDiffs,
  cloneDataSource,
  createReplaySessionId,
  dedupeHeaders,
  downloadTextFile,
  ensureUniqueParameterName,
  fillEmptyHeaders,
  formatConsoleEntries,
  formatNetworkEntries,
  formatReplayHeaders,
  formatReplayTime,
  getErrorIssues,
  getErrorMessage,
  getErrorWarnings,
  getImportSummary,
  hasBrowserReplayArtifacts,
  isLikelyParameterValue,
  mergeReplayVariables,
  normalizeHeaders,
  parseReplayVariablesText,
  parseInlineData,
  sanitizeVarName,
  stringifyInlineData,
  summarizeInlineData,
  validateInlineData,
} from './scriptEditorUtils'
import './ScriptEditor.css'

type Action = ScriptActionDefinition
type Assertion = ScriptAssertionDefinition
type ScriptData = ScriptDefinition

type ReplaySnapshot = BrowserReplaySnapshot
type ReplayActionEvent = BrowserReplayEvent
type ReplayReport = BrowserReplayReport

type BrowserEngineName = 'chromium' | 'webkit'

type BrowserDeviceProfileOption = {
  name: string
  engine: BrowserEngineName
}

const BROWSER_DEVICE_PROFILES: readonly BrowserDeviceProfileOption[] = [
  { name: 'desktop_1080p', engine: 'chromium' },
  { name: 'galaxy_s24', engine: 'chromium' },
  { name: 'ipad_air', engine: 'webkit' },
  { name: 'iphone_15_pro', engine: 'webkit' },
  { name: 'iphone_se', engine: 'webkit' },
  { name: 'pixel_8', engine: 'chromium' },
]

const WEBKIT_INSTALL_ADVISORY = 'WebKit execution requires Playwright WebKit assets installed on the controller host. Run go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5700.1 install webkit before running browser tests or replay sessions.'

function lookupBrowserDeviceProfile(name?: string) {
  return BROWSER_DEVICE_PROFILES.find(profile => profile.name === name)
}

function formatBrowserDeviceProfileLabel(profile: BrowserDeviceProfileOption) {
  return profile.engine === 'webkit' ? `${profile.name} (WebKit)` : profile.name
}

function sanitizeDownloadName(value: string | undefined, fallback: string) {
  const normalized = (value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return normalized || fallback
}

function normalizeWarnings(messages?: string[]) {
  if (!Array.isArray(messages)) return []
  return Array.from(new Set(messages.map(message => message.trim()).filter(Boolean)))
}

function hasStructuredReplaySnapshot(snapshot?: ReplaySnapshot | null) {
  return Boolean(
    snapshot?.request_method ||
    snapshot?.request_url ||
    snapshot?.request_headers ||
    snapshot?.request_body ||
    snapshot?.response_status ||
    snapshot?.response_headers ||
    snapshot?.response_body,
  )
}

function decodeBase64Bytes(value: string): Uint8Array {
  const decoded = atob(value)
  const bytes = new Uint8Array(decoded.length)
  for (let index = 0; index < decoded.length; index += 1) {
    bytes[index] = decoded.charCodeAt(index)
  }
  return bytes
}

interface ReplayOptionsState {
  groupName: string
  ignoreThinkTime: boolean
  startPaused: boolean
  variablesText: string
}

interface ReplayActivityItem {
  id: string
  kind: 'start' | 'event' | 'complete' | 'error'
  title: string
  detail?: string
  timestamp: string
  success?: boolean
  changedVariables?: string[]
}

interface ReplayWSMessage extends WSMessage {
  replay_session_id?: string
  event_index?: number
  event?: ReplayActionEvent
  report?: ReplayReport
  session?: BrowserReplaySession
}

type DataSourceConfig = ScriptDataSource
type ParameterConfig = ScriptParameterDefinition

interface DataSourceHistory {
  past: DataSourceConfig[]
  future: DataSourceConfig[]
}

interface CandidateOccurrence {
  actionIdx: number
  field: 'target' | 'body' | 'header'
  headerKey?: string
}

interface ParameterCandidate {
  id: string
  keyHint: string
  value: string
  varName: string
  occurrences: CandidateOccurrence[]
  selected: boolean
}

function formatKeyValueLines(values?: Record<string, string>) {
  return Object.entries(values || {}).map(([key, value]) => `${key}=${value}`).join('\n')
}

function parseKeyValueLines(value: string) {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .reduce<Record<string, string>>((acc, line) => {
      const separator = line.indexOf('=')
      if (separator === -1) {
        acc[line] = ''
        return acc
      }
      const key = line.slice(0, separator).trim()
      const itemValue = line.slice(separator + 1).trim()
      if (key) acc[key] = itemValue
      return acc
    }, {})
}

function formatLineList(values?: string[]) {
  return (values || []).join('\n')
}

function parseLineList(value: string) {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
}

function formatContentChecks(value?: ContentCheckConfig[]) {
  return (value || [])
    .map(check => `${check.text || ''}|${check.scope || 'any_response'}|${check.fail_if_found === false ? 'false' : 'true'}`)
    .join('\n')
}

function parseContentChecks(value: string): ContentCheckConfig[] {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map(line => {
      const [text, scope, failIfFound] = line.split('|').map(part => part.trim())
      return {
        text,
        scope: scope || 'any_response',
        fail_if_found: failIfFound ? failIfFound !== 'false' : true,
      }
    })
}

function formatDFEChain(value?: DFETransform[]) {
  return (value || [])
    .map((transform) => {
      const type = transform.type?.trim()
      if (!type) return ''
      const options = Object.entries(transform.options || {})
        .filter(([key, optionValue]) => key.trim() && optionValue)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, optionValue]) => `${key}=${optionValue}`)
      return [type, ...options].join(' ')
    })
    .filter(Boolean)
    .join('\n')
}

const SOAP11EnvelopeNamespace = 'http://schemas.xmlsoap.org/soap/envelope/'
const SOAP12EnvelopeNamespace = 'http://www.w3.org/2003/05/soap-envelope'

function defaultSOAPEnvelopeNamespace(version?: string) {
  return version === '1.2' ? SOAP12EnvelopeNamespace : SOAP11EnvelopeNamespace
}

function syncSOAPEnvelopeNamespace(version: string, namespaces?: Record<string, string>, previousVersion?: string) {
  const nextNamespaces = { ...(namespaces || {}) }
  const currentEnvelope = nextNamespaces.soapenv?.trim()
  const previousEnvelope = defaultSOAPEnvelopeNamespace(previousVersion)
  if (!currentEnvelope || currentEnvelope === previousEnvelope) {
    nextNamespaces.soapenv = defaultSOAPEnvelopeNamespace(version)
  }
  return nextNamespaces
}

function parseDFEChain(value: string): DFETransform[] {
  return value
    .split(/\r?\n/)
    .map(line => line.trim())
    .filter(Boolean)
    .map((line) => {
      const [type, ...optionTokens] = line.split(/\s+/)
      const options = optionTokens.reduce<Record<string, string>>((acc, token) => {
        const separator = token.indexOf('=')
        if (separator <= 0 || separator === token.length - 1) {
          return acc
        }
        const key = token.slice(0, separator).trim()
        const optionValue = token.slice(separator + 1).trim()
        if (!key || !optionValue) {
          return acc
        }
        acc[key] = optionValue
        return acc
      }, {})

      return Object.keys(options).length > 0 ? { type, options } : { type }
    })
}

interface ActionEditorParameterRequest {
  selectedText: string
  requestedName: string
  existingName?: string | null
}

interface ActionEditorSelectionState {
  start: number
  end: number
  selectedText: string
  x: number
  y: number
  existingName: string | null
}

interface ActionEditorParameterDraft {
  actionIndex: number
  start: number
  end: number
  selectedText: string
  existingName: string | null
  requestedName: string
  seedValue: string
}

interface Props {
  scriptId?: string | null
  initialScript?: ScriptData | null
  initialFocusedActionName?: string | null
  initialFocusedSection?: string | null
  initialFocusedDataSourceName?: string | null
  projectId?: string
  folderId?: string | null
  onSave?: (script: StoredScript) => void
  onClose?: () => void
}

function findActionIndexByName(actions: ScriptActionDefinition[], targetName?: string | null): number | null {
	const normalizedTarget = targetName?.trim()
	if (!normalizedTarget) return null

	const exactMatch = actions.findIndex(action => action.name?.trim() === normalizedTarget)
	if (exactMatch >= 0) return exactMatch

	const normalizedLower = normalizedTarget.toLowerCase()
	const caseInsensitiveMatch = actions.findIndex(action => action.name?.trim().toLowerCase() === normalizedLower)
	return caseInsensitiveMatch >= 0 ? caseInsensitiveMatch : null
}

function findActionIndexByImportedLocation(actions: ScriptActionDefinition[], location?: string | null): number | null {
  const normalizedLocation = location?.trim()
  if (!normalizedLocation) return null

  const match = normalizedLocation.match(/^(?:Step|Request)\s+(\d+)$/i)
  if (!match) return null

  const actionIndex = Number.parseInt(match[1], 10) - 1
  if (!Number.isInteger(actionIndex) || actionIndex < 0 || actionIndex >= actions.length) {
    return null
  }

  return actionIndex
}

function formatImportedLocationLabel(actions: ScriptActionDefinition[], location?: string | null, actionIndex?: number | null): string {
  const normalizedLocation = location?.trim()
  if (!normalizedLocation) return 'Unknown action'
  if (actionIndex === null || actionIndex === undefined || actionIndex < 0 || actionIndex >= actions.length) {
    return normalizedLocation
  }

  const actionName = actions[actionIndex]?.name?.trim()
  return actionName ? `${normalizedLocation}: ${actionName}` : normalizedLocation
}

function findDataSourceIndexByName(dataSources: ScriptDataSource[] | undefined, targetName?: string | null): number | null {
  const normalizedTarget = targetName?.trim()
  if (!normalizedTarget) return null

  const exactMatch = (dataSources || []).findIndex(source => source.name?.trim() === normalizedTarget)
  if (exactMatch >= 0) return exactMatch

  const normalizedLower = normalizedTarget.toLowerCase()
  const caseInsensitiveMatch = (dataSources || []).findIndex(source => source.name?.trim().toLowerCase() === normalizedLower)
  return caseInsensitiveMatch >= 0 ? caseInsensitiveMatch : null
}

function getImportedParameterMappingValue(values: Record<string, string>, keys: string[]): string {
  const targetKeys = new Set(keys.map(key => key.toLowerCase()))
  const match = Object.entries(values).find(([key]) => targetKeys.has(key.trim().toLowerCase()))
  return match?.[1]?.trim() || ''
}

function sanitizeImportedParameterDataSourceName(dataFile?: string | null): string {
  const normalizedFile = dataFile?.trim()
  if (!normalizedFile) return 'parameter_data'

  const baseName = normalizedFile.split(/[\\/]/).pop() || normalizedFile
  const withoutExtension = baseName.replace(/\.[^.]+$/, '')
  const normalized = withoutExtension
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')

  return normalized || 'parameter_data'
}

function findImportedParameterMappingDataSourceIndex(dataSources: ScriptDataSource[] | undefined, values: Record<string, string>): number | null {
  const dataFile = getImportedParameterMappingValue(values, ['table', 'filename', 'file', 'datafile', 'sourcefile'])
  if (!dataFile) return null

  return findDataSourceIndexByName(dataSources, sanitizeImportedParameterDataSourceName(dataFile))
}

function getImportedRuntimeSectionId(area?: string | null): string | null {
  const normalizedArea = area?.trim().toLowerCase()
  switch (normalizedArea) {
    case 'think time':
      return 'think'
    case 'pacing':
      return 'pacing'
    case 'run logic':
      return 'runlogic'
    case 'error handling':
      return 'errors'
    case 'logging':
      return 'log'
    case 'http':
      return 'http'
    case 'web http/html':
    case 'web runtime':
      return 'web_runtime'
    case 'soap':
    case 'soap settings':
      return 'soap'
    case 'java http':
    case 'java over http':
      return 'java_http'
    case 'jms':
      return 'jms'
    default:
      return null
  }
}

function upsertReplayActivity(items: ReplayActivityItem[], next: ReplayActivityItem): ReplayActivityItem[] {
  const index = items.findIndex(item => item.id === next.id)
  if (index === -1) return [...items, next]
  const updated = [...items]
  updated[index] = next
  return updated
}

interface ReplayScreenshotSelection {
  alt: string
  src: string
}


export default function ScriptEditor({ scriptId, initialScript, initialFocusedActionName, initialFocusedSection, initialFocusedDataSourceName, projectId, folderId, onSave, onClose }: Props) {
  const api = useApi()
  const showProtocolSelector = Boolean(scriptId)
  const {
    activeAction,
    activeReplaySessionId,
    browserReplaySession,
    copiedReplaySnapshotSection,
    dataEditorMode,
    dataSourceHistory,
    error,
    expandedReplayBrowserArtifacts,
    expandedReplayEvent,
    hydrateEditorState,
    isParameterDialogOpen,
    jsonMode,
    jsonText,
    openDataEditor,
    openSections,
    parameterCandidates,
    pendingParameterDraft,
    replayActivity,
    replayEventRefs,
    replayOptions,
    replayReport,
    replayScreenshotSelection,
    replaySeedVariables,
    replaying,
    saving,
    script,
    setActiveAction,
    setActiveReplaySessionId,
    setBrowserReplaySession,
    setCopiedReplaySnapshotSection,
    setDataEditorMode,
    setDataSourceHistory,
    setError,
    setExpandedReplayBrowserArtifacts,
    setExpandedReplayEvent,
    setIsParameterDialogOpen,
    setJsonMode,
    setJsonText,
    setOpenDataEditor,
    setOpenSections,
    setParameterCandidates,
    setPendingParameterDraft,
    setReplayActivity,
    setReplayOptions,
    setReplayReport,
    setReplayScreenshotSelection,
    setReplaySeedVariables,
    setReplaying,
    setSaving,
    setScript,
    setSuccess,
    setValidating,
    setWarnings,
    success,
    toggleSection,
    validating,
    warnings,
    closeParameterDialog,
    closeReplayScreenshot,
  } = useScriptEditorState(initialScript)
  const isBrowserScript = script.protocol === 'browser'
  const isHttpScript = script.protocol === 'http'
  const isJmsScript = script.protocol === 'jms-solace'
  const isJavaHttpScript = script.protocol === 'java_http'
  const isSoapScript = script.protocol === 'soap'
  const importMetadata = script.metadata?.import
  const importSummary = getImportSummary(importMetadata)
  const importedCorrelationFocusTargets = importSummary.correlationSuggestions.map((correlation) => {
    const foundAtActionIndex = findActionIndexByImportedLocation(script.actions, correlation.found_at)
    const foundAtLabel = formatImportedLocationLabel(script.actions, correlation.found_at, foundAtActionIndex)
    const usedAtLocations = correlation.used_at
      .map((location) => ({
        location,
        actionIndex: findActionIndexByImportedLocation(script.actions, location),
        label: formatImportedLocationLabel(script.actions, location, findActionIndexByImportedLocation(script.actions, location)),
      }))

    const usedAtTargets = usedAtLocations
      .filter((target): target is { location: string; actionIndex: number; label: string } => target.actionIndex !== null)

    return {
      correlation,
      foundAtActionIndex,
      foundAtLabel,
      usedAtLocations,
      usedAtTargets,
    }
  })
  const runtimeSettingParitySummary = Object.entries(importSummary.runtimeSettingStatusCounts)
    .filter(([, count]) => count > 0)
    .map(([status, count]) => `${count} ${formatRuntimeSettingStatusLabel(status)}`)
    .join(', ')
  const runtimeSettingReadinessSummary = importSummary.runtimeSettingReadiness
    ? importSummary.runtimeSettingReadiness.summary || `${importSummary.runtimeSettingReadiness.score_pct}% readiness`
    : ''
  const importedParameterFileMappings = importSummary.parameterFileSections.map((section) => {
    const sourceIndex = findImportedParameterMappingDataSourceIndex(script.data_sources, section.values)
    return {
      section,
      sourceName: sourceIndex !== null ? script.data_sources?.[sourceIndex]?.name || null : null,
    }
  })
  const importedDataSourceSummaries = (script.data_sources || []).map((source, index) => {
    const summary = summarizeInlineData(source)
    const columnDetails = source.first_row_header === false
      ? source.columns || []
      : []
    return {
      name: source.name || `source_${index + 1}`,
      type: source.type || 'csv',
      rows: summary.rows,
      columns: columnDetails.length > 0 ? columnDetails : summary.columns,
    }
  })
  const openImportedDataSourceReview = useCallback((sourceName?: string | null) => {
    setIsParameterDialogOpen(true)
    const sourceIndex = findDataSourceIndexByName(script.data_sources, sourceName)
    if (sourceIndex !== null) {
      setOpenDataEditor(sourceIndex)
    }
  }, [script.data_sources, setIsParameterDialogOpen, setOpenDataEditor])
  const openImportedRuntimeSettings = useCallback((area?: string | null) => {
    const sectionId = getImportedRuntimeSectionId(area)
    setOpenSections(prev => {
      const next: Record<string, boolean> = { ...prev, runtime: true }
      if (sectionId) {
        next[sectionId] = true
      }
      return next
    })
  }, [setOpenSections])
  const selectedBrowserEngine = script.config.browser?.engine || 'chromium'
  const selectedBrowserProfile = lookupBrowserDeviceProfile(script.config.browser?.device_profile)
  const browserEngineAdvisory = selectedBrowserProfile && selectedBrowserProfile.engine !== selectedBrowserEngine
    ? `${selectedBrowserProfile.name} requires ${selectedBrowserProfile.engine}. Choose a matching engine or switch to a compatible preset.`
    : selectedBrowserEngine === 'webkit'
      ? WEBKIT_INSTALL_ADVISORY
      : ''

  useEffect(() => {
    if (!isParameterDialogOpen) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeParameterDialog()
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [closeParameterDialog, isParameterDialogOpen])

  useEffect(() => {
    if (!replayScreenshotSelection) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeReplayScreenshot()
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [closeReplayScreenshot, replayScreenshotSelection])

  const handleCopyReplaySnapshotSection = async (key: string, value: string) => {
    if (!navigator.clipboard?.writeText) return
    await navigator.clipboard.writeText(value)
    setCopiedReplaySnapshotSection(key)
  }

  const toggleReplayBrowserArtifact = (key: string, expanded: boolean) => {
    setExpandedReplayBrowserArtifacts(prev => ({
      ...prev,
      [key]: expanded,
    }))
  }

  const buildAutoParameterCandidates = (current: ScriptData): ParameterCandidate[] => {
    const buckets = new Map<string, { keyHint: string; value: string; occurrences: CandidateOccurrence[] }>()
    const add = (value: string, keyHint: string, occurrence: CandidateOccurrence) => {
      const normalizedValue = value.trim()
      if (!isLikelyParameterValue(normalizedValue)) return
      const bucketKey = `${keyHint}::${normalizedValue}`
      const bucket = buckets.get(bucketKey)
      if (bucket) {
        bucket.occurrences.push(occurrence)
      } else {
        buckets.set(bucketKey, { keyHint, value: normalizedValue, occurrences: [occurrence] })
      }
    }

    current.actions.forEach((action, actionIdx) => {
      const target = action.target || ''
      if (target) {
        const queryPart = target.includes('?') ? target.split('?')[1] : ''
        if (queryPart) {
          queryPart.split('&').forEach(pair => {
            const [rawKey, rawValue] = pair.split('=')
            if (!rawValue) return
            const key = decodeURIComponent(rawKey || 'query_value')
            const value = decodeURIComponent(rawValue || '')
            add(value, key, { actionIdx, field: 'target' })
          })
        }
      }

      Object.entries(action.headers || {}).forEach(([headerKey, headerValue]) => {
        add(String(headerValue || ''), headerKey, { actionIdx, field: 'header', headerKey })
      })

      const body = action.body || ''
      if (body) {
        try {
          const parsed = JSON.parse(body)
          const walk = (node: unknown, path: string[]) => {
            if (node === null || node === undefined) return
            if (typeof node === 'string' || typeof node === 'number' || typeof node === 'boolean') {
              const key = path[path.length - 1] || 'body_value'
              add(String(node), key, { actionIdx, field: 'body' })
              return
            }
            if (Array.isArray(node)) {
              node.forEach((child, idx) => walk(child, [...path, `item_${idx + 1}`]))
              return
            }
            if (typeof node === 'object') {
              Object.entries(node).forEach(([k, v]) => walk(v, [...path, k]))
            }
          }
          walk(parsed, [])
        } catch {
          const quoted = Array.from(body.matchAll(/"([^"]{3,})"/g))
          quoted.forEach((match, idx) => {
            add(match[1], `body_field_${idx + 1}`, { actionIdx, field: 'body' })
          })
        }
      }
    })

    return Array.from(buckets.entries())
      .map(([id, bucket]) => ({
        id,
        keyHint: bucket.keyHint,
        value: bucket.value,
        varName: sanitizeVarName(bucket.keyHint),
        occurrences: bucket.occurrences,
        selected: bucket.occurrences.length > 0,
      }))
      .sort((a, b) => b.occurrences.length - a.occurrences.length)
  }

  useEffect(() => {
    if (!initialScript) return
    hydrateEditorState(initialScript)

		const focusedActionIndex = findActionIndexByName(initialScript.actions, initialFocusedActionName)
		if (focusedActionIndex !== null) {
			setActiveAction(focusedActionIndex)
		}

    if (initialFocusedSection === 'runtime') {
      setOpenSections(prev => ({ ...prev, runtime: true }))
    }

    if (initialFocusedSection === 'parameter-data') {
      setIsParameterDialogOpen(true)
    }

    const focusedDataSourceIndex = findDataSourceIndexByName(initialScript.data_sources, initialFocusedDataSourceName)
    if (focusedDataSourceIndex !== null) {
      setIsParameterDialogOpen(true)
      setOpenDataEditor(focusedDataSourceIndex)
    }
  }, [hydrateEditorState, initialFocusedActionName, initialFocusedDataSourceName, initialFocusedSection, initialScript, setActiveAction, setIsParameterDialogOpen, setOpenDataEditor, setOpenSections])

  useEffect(() => {
    if (!scriptId || initialScript) return

    let isCancelled = false

    api.getScript(scriptId)
      .then((res) => {
        if (isCancelled) return
        if (!res.script) {
          setError('Failed to load script')
          return
        }
        hydrateEditorState(res.script)
      })
      .catch((loadError: unknown) => {
        if (isCancelled) return
        setError(getErrorMessage(loadError, 'Failed to load script'))
      })

    return () => {
      isCancelled = true
    }
  }, [api, hydrateEditorState, initialScript, scriptId, setError])

  const handleReplaySocketMessage = useCallback((message: WSMessage) => {
    const msg = message as ReplayWSMessage
    if (!activeReplaySessionId || msg.replay_session_id !== activeReplaySessionId) return

    if (msg.type === 'browser_replay_started' && msg.session) {
      const session = msg.session
      setBrowserReplaySession(prev => {
        if (prev?.id === session.id && prev.status === 'paused' && session.status === 'running') {
          return { ...session, status: 'paused' }
        }
        return session
      })
      setReplayReport(prev => prev ? {
        ...prev,
        group: session.group || prev.group,
        protocol: session.protocol || prev.protocol,
        started_at: session.started_at || prev.started_at,
      } : {
        group: session.group || 'browser-replay',
        protocol: session.protocol || 'browser',
        started_at: session.started_at || new Date().toISOString(),
        completed_at: '',
        success: true,
        events: [],
      })
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'start',
        kind: 'start',
        title: 'Browser replay started',
        detail: session.group || 'browser replay',
        timestamp: session.started_at || new Date().toISOString(),
      }))
      setExpandedReplayEvent(null)
      setExpandedReplayBrowserArtifacts({})
      return
    }

    if (msg.type === 'replay_started' && msg.report) {
      const report = msg.report
      setReplayReport(report)
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'start',
        kind: 'start',
        title: 'Replay started',
        detail: report.group || 'replay',
        timestamp: report.started_at,
      }))
      setExpandedReplayEvent(null)
      setExpandedReplayBrowserArtifacts({})
      return
    }

    if (msg.type === 'replay_event' && msg.event) {
      const replayEvent = msg.event
      setReplayReport(prev => {
        if (!prev) return prev
        const nextEvents = [...prev.events]
        const index = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : nextEvents.length
        nextEvents[index] = replayEvent
        return {
          ...prev,
          events: nextEvents,
          success: prev.success && replayEvent.success,
          error: prev.error || replayEvent.error_message,
        }
      })
      const replayIndex = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : 0
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: `event-${replayIndex}`,
        kind: 'event',
        title: `${replayEvent.action_name || 'Action'} ${replayEvent.success ? 'completed' : 'failed'}`,
        detail: replayEvent.success
          ? `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`
          : replayEvent.error_message || `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`,
        timestamp: replayEvent.completed_at || replayEvent.started_at,
        success: replayEvent.success,
        changedVariables: Object.keys(replayEvent.extractions || {}),
      }))
      if (typeof msg.event_index === 'number' && msg.event_index >= 0) {
        setExpandedReplayEvent(msg.event_index)
      }
      return
    }

    if (msg.type === 'browser_replay_event' && msg.event) {
      const replayEvent = msg.event
      setBrowserReplaySession(prev => {
        if (msg.session) return msg.session
        if (!prev) return prev
        const index = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : prev.events?.length || 0
        const events = [...(prev.events || [])]
        events[index] = replayEvent as unknown as BrowserReplayEvent
        return {
          ...prev,
          current_event_index: index,
          current_event: replayEvent as unknown as BrowserReplayEvent,
          events,
        }
      })
      setReplayReport(prev => {
        if (!prev) return prev
        const nextEvents = [...prev.events]
        const index = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : nextEvents.length
        nextEvents[index] = replayEvent
        return {
          ...prev,
          events: nextEvents,
          success: prev.success && replayEvent.success,
          error: prev.error || replayEvent.error_message,
        }
      })
      const replayIndex = typeof msg.event_index === 'number' && msg.event_index >= 0 ? msg.event_index : 0
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: `event-${replayIndex}`,
        kind: 'event',
        title: `${replayEvent.action_name || 'Action'} ${replayEvent.success ? 'completed' : 'failed'}`,
        detail: replayEvent.success
          ? `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`
          : replayEvent.error_message || `${replayEvent.method || replayEvent.action_type} ${replayEvent.target}`,
        timestamp: replayEvent.completed_at || replayEvent.started_at,
        success: replayEvent.success,
        changedVariables: Object.keys(replayEvent.extractions || {}),
      }))
      if (typeof msg.event_index === 'number' && msg.event_index >= 0) {
        setExpandedReplayEvent(msg.event_index)
      }
      return
    }

    if ((msg.type === 'browser_replay_paused' || msg.type === 'browser_replay_resumed') && msg.session) {
      const session = msg.session
      setBrowserReplaySession(session)
      const isPaused = msg.type === 'browser_replay_paused'
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: isPaused ? 'paused' : 'resumed',
        kind: 'event',
        title: isPaused ? 'Browser replay paused' : 'Browser replay resumed',
        detail: session.group || 'browser replay',
        timestamp: new Date().toISOString(),
      }))
      return
    }

    if (msg.type === 'replay_complete' && msg.report) {
      const report = msg.report
      setReplayReport(report)
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'complete',
        kind: 'complete',
        title: report.success ? 'Replay completed successfully' : 'Replay completed with failures',
        detail: report.error,
        timestamp: report.completed_at,
        success: report.success,
      }))
      setExpandedReplayEvent(report.events.length ? report.events.length - 1 : null)
      setExpandedReplayBrowserArtifacts({})
      setActiveReplaySessionId(null)
      setReplaying(false)
      return
    }

    if ((msg.type === 'browser_replay_completed' || msg.type === 'browser_replay_failed' || msg.type === 'browser_replay_stopped') && (msg.report || msg.session)) {
      if (msg.session) setBrowserReplaySession(msg.session)
      const report = msg.report || msg.session?.report || null
      const completedAt = msg.session?.completed_at || report?.completed_at || new Date().toISOString()
      if (report) {
        setReplayReport(report)
      } else {
        setReplayReport(prev => prev ? {
          ...prev,
          completed_at: completedAt,
          success: msg.type === 'browser_replay_completed',
          error: msg.session?.error || prev.error,
        } : prev)
      }
      const title = msg.type === 'browser_replay_completed'
        ? 'Browser replay completed successfully'
        : msg.type === 'browser_replay_stopped'
          ? 'Browser replay stopped'
          : 'Browser replay completed with failures'
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'complete',
        kind: msg.type === 'browser_replay_stopped' ? 'complete' : msg.type === 'browser_replay_completed' ? 'complete' : 'error',
        title,
        detail: report?.error || msg.session?.error,
        timestamp: completedAt,
        success: msg.type === 'browser_replay_completed' ? true : msg.type === 'browser_replay_failed' ? false : undefined,
      }))
      if (report) {
        setExpandedReplayEvent(report.events.length ? report.events.length - 1 : null)
      }
      setExpandedReplayBrowserArtifacts({})
      setActiveReplaySessionId(null)
      setReplaying(false)
    }
  }, [activeReplaySessionId, setActiveReplaySessionId, setBrowserReplaySession, setExpandedReplayBrowserArtifacts, setExpandedReplayEvent, setReplayActivity, setReplayReport, setReplaying])

  useWebSocket({ onMessage: handleReplaySocketMessage, projectId })

  useEffect(() => {
    if (expandedReplayEvent === null) return
    const node = replayEventRefs.current[expandedReplayEvent]
    if (!node) return
    node.scrollIntoView({
      block: 'nearest',
      behavior: activeReplaySessionId ? 'smooth' : 'auto',
    })
  }, [activeReplaySessionId, expandedReplayEvent, replayEventRefs])

  const updateField = (path: string, value: unknown) => {
    setScript(prev => {
      const next = JSON.parse(JSON.stringify(prev)) as ScriptData
      const keys = path.split('.')
      let obj: Record<string, unknown> = next as unknown as Record<string, unknown>
      for (let i = 0; i < keys.length - 1; i++) {
        const key = keys[i]
        const current = obj[key]
        if (!current || typeof current !== 'object' || Array.isArray(current)) obj[key] = {}
        obj = obj[key] as Record<string, unknown>
      }
      obj[keys[keys.length - 1]] = value
      return next
    })
  }

  const updateProtocol = (protocol: string) => {
    let shouldSelectFirstAction = false
    setScript(prev => {
      const next = JSON.parse(JSON.stringify(prev)) as ScriptData
      next.protocol = protocol
      next.config = next.config || { base_url: '' }
      if (protocol === 'soap') {
        next.config.soap = {
          version: next.config.soap?.version || '1.1',
          namespaces: Object.keys(next.config.soap?.namespaces || {}).length > 0
            ? next.config.soap?.namespaces
            : { soapenv: defaultSOAPEnvelopeNamespace(next.config.soap?.version || '1.1') },
          ws_security: next.config.soap?.ws_security,
          fault_handling: {
            fail_on_fault: next.config.soap?.fault_handling?.fail_on_fault ?? true,
          },
        }
        if (next.actions.length === 0) {
          next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_SOAP_ACTION)), name: 'SOAP_Request' }]
          shouldSelectFirstAction = true
        }
      }
      if (protocol === 'java_http') {
        next.config.java_http = {
          codec: next.config.java_http?.codec || 'java_serialized_object_base64',
          helper_mode: next.config.java_http?.helper_mode || 'embedded',
          content_type: next.config.java_http?.content_type || 'application/x-java-serialized-object',
          accept: next.config.java_http?.accept || 'application/x-java-serialized-object',
          body_encoding: next.config.java_http?.body_encoding || 'base64',
          object_schema: next.config.java_http?.object_schema,
          request_mapping: next.config.java_http?.request_mapping,
          response_mapping: next.config.java_http?.response_mapping,
          extractor_type: next.config.java_http?.extractor_type || 'opaque',
          preserve_binary_result: next.config.java_http?.preserve_binary_result ?? true,
        }
        if (next.actions.length === 0) {
          next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_ACTION)), name: 'Java_HTTP_Request', type: 'POST', method: 'POST' }]
          shouldSelectFirstAction = true
        }
      }
      if (protocol === 'jms-solace') {
        next.config.jms = {
          provider: 'solace',
          destination_type: 'queue',
          acknowledgement: 'auto',
          delivery_mode: 'persistent',
          timeout_ms: 5000,
                          mock: true,
                          ...(next.config.jms || {}),
        }
        if (next.actions.length === 0) {
          next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_JMS_ACTION)), name: 'Publish_Message' }]
          shouldSelectFirstAction = true
        }
      }
      if (protocol === 'browser' && next.actions.length === 0) {
        next.actions = [{ ...JSON.parse(JSON.stringify(EMPTY_BROWSER_ACTION)), name: 'Navigate' }]
        shouldSelectFirstAction = true
      }
      return next
    })
    if (shouldSelectFirstAction) setActiveAction(0)
  }

  const addAction = () => {
    const template = isBrowserScript ? EMPTY_BROWSER_ACTION : isJmsScript ? EMPTY_JMS_ACTION : isSoapScript ? EMPTY_SOAP_ACTION : EMPTY_ACTION
    const a = JSON.parse(JSON.stringify({
      ...template,
      name: `Action_${script.actions.length + 1}`,
      extractors: template.extractors || [],
      assertions: template.assertions || [],
    })) as Action
    setScript(prev => ({ ...prev, actions: [...prev.actions, a] }))
    setActiveAction(script.actions.length)
  }

  const removeAction = (idx: number) => {
    setScript(prev => ({ ...prev, actions: prev.actions.filter((_, i) => i !== idx) }))
    if (activeAction === idx) setActiveAction(null)
    else if (activeAction !== null && activeAction > idx) setActiveAction(activeAction - 1)
  }

  const moveAction = (idx: number, dir: -1 | 1) => {
    const newIdx = idx + dir
    if (newIdx < 0 || newIdx >= script.actions.length) return
    setScript(prev => {
      const actions = [...prev.actions]
      ;[actions[idx], actions[newIdx]] = [actions[newIdx], actions[idx]]
      return { ...prev, actions }
    })
    setActiveAction(newIdx)
  }

  const updateAction = (idx: number, field: keyof Action, value: unknown) => {
    setScript(prev => {
      const actions = [...prev.actions]
      actions[idx] = { ...actions[idx], [field]: value }
      return { ...prev, actions }
    })
  }

  const addExtractor = (actionIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const ext = [...(actions[actionIdx].extractors || [])]
      ext.push(prev.protocol === 'jms-solace'
        ? { name: '', source: 'body', type: 'jsonpath', expression: '' }
        : { name: '', source: 'body', type: 'jsonpath', expression: '' })
      actions[actionIdx] = { ...actions[actionIdx], extractors: ext }
      return { ...prev, actions }
    })
  }

  const updateExtractor = (actionIdx: number, extIdx: number, field: string, value: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const ext = [...(actions[actionIdx].extractors || [])]
      ext[extIdx] = { ...ext[extIdx], [field]: value }
      actions[actionIdx] = { ...actions[actionIdx], extractors: ext }
      return { ...prev, actions }
    })
  }

  const removeExtractor = (actionIdx: number, extIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const ext = (actions[actionIdx].extractors || []).filter((_, i) => i !== extIdx)
      actions[actionIdx] = { ...actions[actionIdx], extractors: ext }
      return { ...prev, actions }
    })
  }

  const addAssertion = (actionIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const assertions = [...(actions[actionIdx].assertions || [])]
      assertions.push(prev.protocol === 'jms-solace' ? { type: 'message_received' } : { type: 'status_code', expected: 200 })
      actions[actionIdx] = { ...actions[actionIdx], assertions }
      return { ...prev, actions }
    })
  }

  const updateAssertion = (actionIdx: number, aIdx: number, field: keyof Assertion, value: unknown) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const assertions = [...(actions[actionIdx].assertions || [])]
      assertions[aIdx] = { ...assertions[aIdx], [field]: value }
      actions[actionIdx] = { ...actions[actionIdx], assertions }
      return { ...prev, actions }
    })
  }

  const removeAssertion = (actionIdx: number, aIdx: number) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const assertions = (actions[actionIdx].assertions || []).filter((_, i) => i !== aIdx)
      actions[actionIdx] = { ...actions[actionIdx], assertions }
      return { ...prev, actions }
    })
  }

  const addHeader = (actionIdx: number) => {
    const key = prompt('Header name:')
    if (!key) return
    setScript(prev => {
      const actions = [...prev.actions]
      const headers = { ...(actions[actionIdx].headers || {}), [key]: '' }
      actions[actionIdx] = { ...actions[actionIdx], headers }
      return { ...prev, actions }
    })
  }

  const updateHeader = (actionIdx: number, oldKey: string, newKey: string, value: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const headers = { ...(actions[actionIdx].headers || {}) }
      if (oldKey !== newKey) delete headers[oldKey]
      headers[newKey] = value
      actions[actionIdx] = { ...actions[actionIdx], headers }
      return { ...prev, actions }
    })
  }

  const removeHeader = (actionIdx: number, key: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const headers = { ...(actions[actionIdx].headers || {}) }
      delete headers[key]
      actions[actionIdx] = { ...actions[actionIdx], headers }
      return { ...prev, actions }
    })
  }

  const addMetadata = (actionIdx: number) => {
    const key = prompt('Property name:')
    if (!key) return
    setScript(prev => {
      const actions = [...prev.actions]
      const metadata = { ...(actions[actionIdx].metadata || {}), [key]: '' }
      actions[actionIdx] = { ...actions[actionIdx], metadata }
      return { ...prev, actions }
    })
  }

  const updateMetadata = (actionIdx: number, oldKey: string, newKey: string, value: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const metadata = { ...(actions[actionIdx].metadata || {}) }
      if (oldKey !== newKey) delete metadata[oldKey]
      metadata[newKey] = value
      actions[actionIdx] = { ...actions[actionIdx], metadata }
      return { ...prev, actions }
    })
  }

  const removeMetadata = (actionIdx: number, key: string) => {
    setScript(prev => {
      const actions = [...prev.actions]
      const metadata = { ...(actions[actionIdx].metadata || {}) }
      delete metadata[key]
      actions[actionIdx] = { ...actions[actionIdx], metadata }
      return { ...prev, actions }
    })
  }

  const addSoapNamespace = () => {
    const key = prompt('Namespace prefix:')
    if (!key) return
    setScript(prev => ({
      ...prev,
      config: {
        ...prev.config,
        soap: {
          ...(prev.config.soap || {}),
          namespaces: { ...(prev.config.soap?.namespaces || {}), [key]: '' },
        },
      },
    }))
  }

  const updateSoapNamespace = (oldKey: string, newKey: string, value: string) => {
    setScript(prev => {
      const namespaces = { ...(prev.config.soap?.namespaces || {}) }
      if (oldKey !== newKey) delete namespaces[oldKey]
      namespaces[newKey] = value
      return {
        ...prev,
        config: {
          ...prev.config,
          soap: {
            ...(prev.config.soap || {}),
            namespaces,
          },
        },
      }
    })
  }

  const removeSoapNamespace = (key: string) => {
    setScript(prev => {
      const namespaces = { ...(prev.config.soap?.namespaces || {}) }
      delete namespaces[key]
      return {
        ...prev,
        config: {
          ...prev.config,
          soap: {
            ...(prev.config.soap || {}),
            namespaces,
          },
        },
      }
    })
  }

  const addJavaHTTPMapping = (field: 'request_mapping' | 'response_mapping') => {
    const key = prompt(field === 'request_mapping' ? 'Request mapping key:' : 'Response mapping key:')
    if (!key) return
    setScript(prev => {
      const mapping = {
        ...((prev.config.java_http?.[field] as Record<string, string> | undefined) || {}),
        [key]: '',
      }
      return {
        ...prev,
        config: {
          ...prev.config,
          java_http: {
            ...(prev.config.java_http || {}),
            [field]: mapping,
          },
        },
      }
    })
  }

  const updateJavaHTTPMapping = (field: 'request_mapping' | 'response_mapping', oldKey: string, newKey: string, value: string) => {
    setScript(prev => {
      const mapping = { ...((prev.config.java_http?.[field] as Record<string, string> | undefined) || {}) }
      if (oldKey !== newKey) delete mapping[oldKey]
      mapping[newKey] = value
      return {
        ...prev,
        config: {
          ...prev.config,
          java_http: {
            ...(prev.config.java_http || {}),
            [field]: mapping,
          },
        },
      }
    })
  }

  const removeJavaHTTPMapping = (field: 'request_mapping' | 'response_mapping', key: string) => {
    setScript(prev => {
      const mapping = { ...((prev.config.java_http?.[field] as Record<string, string> | undefined) || {}) }
      delete mapping[key]
      return {
        ...prev,
        config: {
          ...prev.config,
          java_http: {
            ...(prev.config.java_http || {}),
            [field]: mapping,
          },
        },
      }
    })
  }

  const resolveCurrentScript = (): ScriptData => {
    if (!jsonMode) return script
    const parsed = JSON.parse(jsonText) as ScriptData | { script?: ScriptData }
    if ('script' in parsed && parsed.script) {
      return parsed.script
    }
    return parsed as ScriptData
  }

  const handleSave = async () => {
    setError(''); setSuccess(''); setWarnings([]); setSaving(true)
    try {
      const currentScript = resolveCurrentScript()
      let savedScript: StoredScript
      if (scriptId) {
        savedScript = await api.updateScript(scriptId, currentScript)
      } else {
        savedScript = await api.createScript(currentScript, projectId, folderId)
      }
      if (jsonMode) setScript(currentScript)
      setSuccess('Script saved successfully!')
      onSave?.(savedScript)
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to save script'))
    } finally { setSaving(false) }
  }

  const handleValidateScript = async () => {
    setError('')
    setSuccess('')
    setWarnings([])
    setValidating(true)
    try {
      const currentScript = resolveCurrentScript()
      const res = scriptId
        ? await api.validateScript(scriptId)
        : await api.validateDraftScript(currentScript, projectId, folderId)
      const validationWarnings = normalizeWarnings(res.warnings)
      setWarnings(validationWarnings)
      if (jsonMode) setScript(currentScript)
      if (res.valid) {
        setSuccess(validationWarnings.length > 0 ? 'Script validation passed with warnings.' : 'Script validation passed.')
        return
      }
      setError(res.issues.join(' | ') || 'Script validation failed.')
    } catch (e: unknown) {
      const validationWarnings = normalizeWarnings(getErrorWarnings(e))
      setWarnings(validationWarnings)
      setError(getErrorMessage(e, 'Script validation failed'))
    } finally {
      setValidating(false)
    }
  }

  const handleReplay = async () => {
    setError('')
    setSuccess('')
    setWarnings([])
    setReplaying(true)
    let browserSessionStarted = false
    try {
      const currentScript = resolveCurrentScript()
      const variables = parseReplayVariablesText(replayOptions.variablesText)
      const replaySessionId = createReplaySessionId()
      const payload = buildReplayStartPayload(currentScript, {
        projectId,
        groupName: replayOptions.groupName,
        replaySessionId,
        variables,
        ignoreThinkTime: replayOptions.ignoreThinkTime,
        startPaused: replayOptions.startPaused,
      })
      const replayGroup = payload.group_name || `${currentScript.name || 'script'}-replay`
      setActiveReplaySessionId(replaySessionId)
      setBrowserReplaySession(null)
      setReplaySeedVariables(variables || {})
      setReplayActivity([{
        id: 'queued',
        kind: 'start',
        title: 'Replay requested',
        detail: replayGroup,
        timestamp: new Date().toISOString(),
      }])
      setExpandedReplayBrowserArtifacts({})
      setReplayScreenshotSelection(null)
      setReplayReport({
        group: replayGroup,
        protocol: currentScript.protocol,
        started_at: new Date().toISOString(),
        completed_at: '',
        success: true,
        events: [],
        final_variables: variables,
      })
      setExpandedReplayEvent(null)

      if (currentScript.protocol === 'browser') {
        const res = scriptId
          ? await api.startBrowserReplaySession(scriptId, payload)
          : await api.startDraftBrowserReplaySession(payload)
        const replayWarnings = normalizeWarnings(res.warnings)
        if (jsonMode) setScript(currentScript)
        if (res.session) {
          setBrowserReplaySession(res.session)
          if (res.session.status === 'paused') {
            setReplayActivity(prev => upsertReplayActivity(prev, {
              id: 'paused',
              kind: 'event',
              title: 'Browser replay paused',
              detail: res.session.group || 'browser replay',
              timestamp: new Date().toISOString(),
            }))
          }
        }
        setActiveReplaySessionId(res.replay_session_id || res.session?.id || replaySessionId)
        setWarnings(replayWarnings)
        setSuccess(replayWarnings.length > 0 ? 'Browser replay started with warnings.' : 'Browser replay started.')
        browserSessionStarted = true
        return
      }

      const res = scriptId
        ? await api.replayScript(scriptId, payload)
        : await api.replayDraftScript(payload)
      if (jsonMode) setScript(currentScript)
      setReplayReport(res.report)
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'complete',
        kind: 'complete',
        title: res.report?.success ? 'Replay completed successfully' : 'Replay completed with failures',
        detail: res.report?.error,
        timestamp: res.report?.completed_at || new Date().toISOString(),
        success: res.report?.success,
      }))
      setExpandedReplayEvent(res.report?.events?.length ? res.report.events.length - 1 : null)
      setExpandedReplayBrowserArtifacts({})
      setActiveReplaySessionId(null)
      setSuccess(res.report?.success ? 'Replay completed successfully.' : 'Replay completed with failures.')
    } catch (e: unknown) {
      const issues = getErrorIssues(e).join(' | ')
      const replayWarnings = normalizeWarnings(getErrorWarnings(e))
      const message = getErrorMessage(e, 'Replay failed')
      setWarnings(replayWarnings)
      setError(issues || message)
      setReplayReport(null)
      setActiveReplaySessionId(null)
      setBrowserReplaySession(null)
      setExpandedReplayBrowserArtifacts({})
      setReplayScreenshotSelection(null)
      setReplaySeedVariables({})
      setReplayActivity(prev => upsertReplayActivity(prev, {
        id: 'error',
        kind: 'error',
        title: 'Replay failed before completion',
        detail: issues || message,
        timestamp: new Date().toISOString(),
        success: false,
      }))
    } finally {
      if (!browserSessionStarted) {
        setReplaying(false)
      }
    }
  }

  const handleStopReplay = async () => {
    if (!isBrowserScript || !activeReplaySessionId) return
    try {
      await api.stopBrowserReplaySession(activeReplaySessionId)
      setError('')
      setSuccess('Stopping browser replay…')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to stop browser replay'))
    }
  }

  const handlePauseResumeReplay = async () => {
    if (!isBrowserScript || !activeReplaySessionId || !browserReplaySession) return
    try {
      const isPaused = browserReplaySession.status === 'paused'
      const res = isPaused
        ? await api.resumeBrowserReplaySession(activeReplaySessionId)
        : await api.pauseBrowserReplaySession(activeReplaySessionId)
      if (res?.session) setBrowserReplaySession(res.session)
      setError('')
      setSuccess(isPaused ? 'Resuming browser replay…' : 'Pausing browser replay…')
    } catch (e: unknown) {
      setError(getErrorMessage(e, 'Failed to update browser replay state'))
    }
  }

  const handleDownloadBrowserReplayExport = () => {
    if (!isBrowserScript || !replayReport) return

    const replaySessionId = activeReplaySessionId || browserReplaySession?.id
    const payload = {
      schema_version: 'stormrunner.browser_replay_export.v1',
      exported_at: new Date().toISOString(),
      replay_session_id: replaySessionId,
      script: {
        id: scriptId || undefined,
        name: script.name,
        project_id: script.project_id || projectId,
        protocol: script.protocol,
      },
      replay_options: {
        group_name: replayReport.group || replayOptions.groupName || undefined,
        ignore_think_time: replayOptions.ignoreThinkTime,
        start_paused: replayOptions.startPaused,
        variables: replaySeedVariables,
      },
      session: browserReplaySession,
      report: replayReport,
    }

    const scriptBase = sanitizeDownloadName(script.name, 'script')
    const replayBase = sanitizeDownloadName(replaySessionId || replayReport.group || 'browser-replay', 'browser-replay')
    downloadTextFile(JSON.stringify(payload, null, 2), `${scriptBase}-${replayBase}-browser-replay.json`)
    setError('')
    setSuccess('Exported browser replay session JSON.')
  }

  const handleDownloadBrowserReplayBundle = () => {
    if (!isBrowserScript || !replayReport) return

    const replaySessionId = activeReplaySessionId || browserReplaySession?.id
    const exportedAt = new Date().toISOString()
    const exportPayload = {
      schema_version: 'stormrunner.browser_replay_export.v1',
      exported_at: exportedAt,
      replay_session_id: replaySessionId,
      script: {
        id: scriptId || undefined,
        name: script.name,
        project_id: script.project_id || projectId,
        protocol: script.protocol,
      },
      replay_options: {
        group_name: replayReport.group || replayOptions.groupName || undefined,
        ignore_think_time: replayOptions.ignoreThinkTime,
        start_paused: replayOptions.startPaused,
        variables: replaySeedVariables,
      },
      session: browserReplaySession,
      report: replayReport,
    }
    const replayEvents = replayReport.events.length >= (browserReplaySession?.events?.length || 0)
      ? replayReport.events
      : browserReplaySession?.events || []
    const scriptBase = sanitizeDownloadName(script.name, 'script')
    const replayBase = sanitizeDownloadName(replaySessionId || replayReport.group || 'browser-replay', 'browser-replay')
    const bundleBase = `${scriptBase}-${replayBase}-browser-replay`
    const eventNumberWidth = Math.max(2, String(Math.max(replayEvents.length, 1)).length)
    const entries: ZipFileEntry[] = [
      {
        name: `${bundleBase}/manifest.json`,
        content: JSON.stringify({
          schema_version: 'stormrunner.browser_replay_bundle.v1',
          exported_at: exportedAt,
          replay_session_id: replaySessionId,
          group_name: replayReport.group,
          status: browserReplaySession?.status || (replayReport.success ? 'completed' : 'failed'),
          success: replayReport.success,
          event_count: replayEvents.length,
          includes_session: Boolean(browserReplaySession),
          includes_report: true,
        }, null, 2),
      },
      {
        name: `${bundleBase}/replay-export.json`,
        content: JSON.stringify(exportPayload, null, 2),
      },
      {
        name: `${bundleBase}/script.json`,
        content: JSON.stringify(script, null, 2),
      },
      {
        name: `${bundleBase}/seed-variables.json`,
        content: JSON.stringify(replaySeedVariables, null, 2),
      },
      {
        name: `${bundleBase}/report.json`,
        content: JSON.stringify(replayReport, null, 2),
      },
    ]

    if (browserReplaySession) {
      entries.push({
        name: `${bundleBase}/session.json`,
        content: JSON.stringify(browserReplaySession, null, 2),
      })
    }

    replayEvents.forEach((event, index) => {
      const eventBase = `${bundleBase}/events/${String(index + 1).padStart(eventNumberWidth, '0')}-${sanitizeDownloadName(event.action_name || event.action_type, 'event')}`
      const replayConsoleEntries = event.console_entries || event.snapshot?.console_entries
      const replayNetworkEntries = event.network_entries || event.snapshot?.network_entries
      const replayHTML = event.html || event.snapshot?.html
      const replayScreenshotBase64 = event.screenshot_base64 || event.snapshot?.screenshot_base64
      const replayDOMSummary = event.dom_summary || event.snapshot?.dom_summary

      entries.push({
        name: `${eventBase}/event.json`,
        content: JSON.stringify(event, null, 2),
      })

      if (hasStructuredReplaySnapshot(event.snapshot)) {
        entries.push(
          {
            name: `${eventBase}/snapshot.json`,
            content: JSON.stringify(event.snapshot, null, 2),
          },
          {
            name: `${eventBase}/request.txt`,
            content: formatSnapshotRequestSection(event.snapshot),
          },
          {
            name: `${eventBase}/response.txt`,
            content: formatSnapshotResponseSection(event.snapshot),
          },
        )
      }

      if (replayDOMSummary) {
        entries.push({
          name: `${eventBase}/dom-summary.json`,
          content: JSON.stringify(replayDOMSummary, null, 2),
        })
      }

      if (replayConsoleEntries?.length) {
        entries.push({
          name: `${eventBase}/console.txt`,
          content: formatConsoleEntries(replayConsoleEntries),
        })
      }

      if (replayNetworkEntries?.length) {
        entries.push({
          name: `${eventBase}/network.txt`,
          content: formatNetworkEntries(replayNetworkEntries),
        })
      }

      if (replayHTML) {
        entries.push({
          name: `${eventBase}/page.html`,
          content: replayHTML,
        })
      }

      if (replayScreenshotBase64) {
        entries.push({
          name: `${eventBase}/screenshot.png`,
          content: decodeBase64Bytes(replayScreenshotBase64),
        })
      }
    })

    downloadBlob(createZipBlob(entries), `${bundleBase}.zip`)
    setError('')
    setSuccess('Exported browser replay bundle ZIP.')
  }

  const toggleJsonMode = () => {
    if (!jsonMode) {
      setJsonText(JSON.stringify({ script }, null, 2))
    } else {
      try {
        const parsed = JSON.parse(jsonText)
        setScript(parsed.script || parsed)
      } catch { setError('Invalid JSON') ; return }
    }
    setJsonMode(!jsonMode)
  }

  const methodColor = (m: string) => {
    switch (m) {
      case 'GET': return 'var(--success)'
      case 'POST': return 'var(--info)'
      case 'PUT': return 'var(--warning)'
      case 'DELETE': return 'var(--danger)'
      case 'PATCH': return 'var(--chart-6)'
      case 'jms_publish': return 'var(--info)'
      case 'jms_consume': return 'var(--success)'
      case 'jms_request_reply': return 'var(--chart-6)'
      case 'jms_ack':
      case 'jms_commit':
      case 'jms_rollback': return 'var(--warning)'
      default: return 'var(--text-secondary)'
    }
  }

  const replayStatusLabel = replaying && replayReport && !replayReport.completed_at
    ? 'Running'
    : replayReport?.success
      ? 'Success'
      : 'Failed'
  const replayLatestActivity = replayActivity[replayActivity.length - 1]
  const replayVariableInspector = replayReport
    ? (replayReport.completed_at && replayReport.final_variables
        ? replayReport.final_variables
        : mergeReplayVariables(replaySeedVariables, replayReport.events))
    : null
  const replayVariableDiffs = replayReport
    ? buildReplayVariableDiffs(replaySeedVariables, replayReport.events)
    : []
  const isReplayPaused = isBrowserScript && browserReplaySession?.status === 'paused'

  const addDataSource = () => {
    const nextIndex = (script.data_sources?.length || 0) + 1
    const nextSource: DataSourceConfig = {
      ...EMPTY_DATA_SOURCE,
      name: `source_${nextIndex}`,
    }
    setScript(prev => ({
      ...prev,
      data_sources: [...(prev.data_sources || []), nextSource],
    }))
    setDataSourceHistory({})
    setDataEditorMode({})
  }

  const addParameter = () => {
    const nextIndex = (script.parameters?.length || 0) + 1
    setScript(prev => ({
      ...prev,
      parameters: [...(prev.parameters || []), { ...EMPTY_PARAMETER, name: `param_${nextIndex}` }],
    }))
  }

  const updateParameter = (idx: number, field: keyof ParameterConfig, value: unknown) => {
    setScript(prev => {
      const parameters = [...(prev.parameters || [])]
      const current = parameters[idx] || { ...EMPTY_PARAMETER }
      parameters[idx] = { ...current, [field]: value }
      return { ...prev, parameters }
    })
  }

  const updateParameterType = (idx: number, nextType: string) => {
    setScript(prev => {
      const parameters = [...(prev.parameters || [])]
      const current = parameters[idx] || { ...EMPTY_PARAMETER }
      parameters[idx] = {
        ...current,
        type: nextType,
        update: current.update || 'each_iteration',
        selection: current.selection || 'sequential',
        on_exhaustion: current.on_exhaustion || 'wrap',
        step: nextType === 'counter' ? (current.step || 1) : current.step,
        length: nextType === 'random_string' ? (current.length || 12) : current.length,
        format: nextType === 'timestamp'
          ? (current.format || 'iso8601')
          : nextType === 'date_time'
            ? (current.format || '2006-01-02 15:04:05')
            : current.format,
      }
      return { ...prev, parameters }
    })
  }

  const removeParameter = (idx: number) => {
    setScript(prev => ({
      ...prev,
      parameters: (prev.parameters || []).filter((_, i) => i !== idx),
    }))
  }

  const upsertParameterFromActionEditor = ({ selectedText, requestedName, existingName }: ActionEditorParameterRequest): string => {
    const normalizedExistingName = existingName ? sanitizeVarName(existingName) : ''
    const resolvedName = ensureUniqueParameterName(
      requestedName || selectedText,
      script.parameters || [],
      normalizedExistingName || undefined,
    )
    const seedValue = (selectedText || '').trim()

    setScript(prev => {
      const parameters = [...(prev.parameters || [])]
      const existingIdx = normalizedExistingName
        ? parameters.findIndex(parameter => sanitizeVarName(parameter.name || '') === normalizedExistingName)
        : -1
      const byResolvedNameIdx = parameters.findIndex(parameter => sanitizeVarName(parameter.name || '') === resolvedName)
      const targetIdx = existingIdx >= 0 ? existingIdx : byResolvedNameIdx

      if (targetIdx >= 0) {
        const current = { ...(parameters[targetIdx] || EMPTY_PARAMETER) }
        const nextParameter: ParameterConfig = {
          ...current,
          name: resolvedName,
          type: current.type || 'list',
          update: current.update || 'each_iteration',
        }
        if (nextParameter.type === 'list') {
          const values = [...(nextParameter.values || [])]
          if (seedValue && !values.includes(seedValue)) values.unshift(seedValue)
          nextParameter.values = values
        }
        parameters[targetIdx] = nextParameter
      } else {
        parameters.push({
          ...EMPTY_PARAMETER,
          name: resolvedName,
          type: 'list',
          update: 'each_iteration',
          values: seedValue ? [seedValue] : [],
        })
      }

      return { ...prev, parameters }
    })

    setIsParameterDialogOpen(true)
    setError('')
    setSuccess(existingName ? `Updated parameter ${resolvedName}.` : `Created parameter ${resolvedName}.`)
    return resolvedName
  }

  const beginParameterEditFromActionEditor = (draft: ActionEditorParameterDraft) => {
    setPendingParameterDraft(draft)
    setIsParameterDialogOpen(true)
    setError('')
  }

  const applyPendingParameterDraft = () => {
    if (!pendingParameterDraft) return

    const name = upsertParameterFromActionEditor({
      selectedText: pendingParameterDraft.seedValue,
      requestedName: pendingParameterDraft.requestedName,
      existingName: pendingParameterDraft.existingName,
    })
    const action = script.actions[pendingParameterDraft.actionIndex]
    if (action) {
      const body = action.body || ''
      const replacement = `{{param.${name}}}`
      const nextBody = `${body.slice(0, pendingParameterDraft.start)}${replacement}${body.slice(pendingParameterDraft.end)}`
      updateAction(pendingParameterDraft.actionIndex, 'body', nextBody)
    }
    setPendingParameterDraft(null)
  }

  const updatePendingParameterDraftName = (value: string) => {
    setPendingParameterDraft(prev => (prev ? { ...prev, requestedName: sanitizeVarName(value) } : prev))
  }

  const updatePendingParameterDraftValue = (value: string) => {
    setPendingParameterDraft(prev => (prev ? { ...prev, seedValue: value } : prev))
  }

  const clearPendingParameterDraft = () => {
    setPendingParameterDraft(null)
  }

  const toggleOpenDataEditor = (idx: number) => {
    setOpenDataEditor(prev => (prev === idx ? null : idx))
  }

  const toggleDataEditorModeAt = (idx: number) => {
    setDataEditorMode(prev => ({ ...prev, [idx]: (prev[idx] || 'grid') === 'grid' ? 'raw' : 'grid' }))
  }

  const removeDataSource = (idx: number) => {
    setScript(prev => ({
      ...prev,
      data_sources: (prev.data_sources || []).filter((_, i) => i !== idx),
    }))
    setDataSourceHistory({})
    setDataEditorMode({})
  }

  const mutateDataSource = (
    idx: number,
    mutator: (current: DataSourceConfig) => DataSourceConfig,
    recordHistory = true,
  ) => {
    setScript(prev => {
      const sources = [...(prev.data_sources || [])]
      const current = sources[idx] || { ...EMPTY_DATA_SOURCE }
      const next = mutator(cloneDataSource(current))
      if (recordHistory) {
        setDataSourceHistory(history => {
          const entry = history[idx] || { past: [], future: [] }
          const past = [...entry.past, cloneDataSource(current)].slice(-100)
          return { ...history, [idx]: { past, future: [] } }
        })
      }
      sources[idx] = cloneDataSource(next)
      return { ...prev, data_sources: sources }
    })
  }

  const updateDataSource = (idx: number, field: keyof DataSourceConfig, value: unknown, recordHistory = true) => {
    mutateDataSource(idx, current => ({ ...current, [field]: value }), recordHistory)
  }

  const undoDataSource = (idx: number) => {
    const entry = dataSourceHistory[idx]
    if (!entry || entry.past.length === 0) return

    mutateDataSource(
      idx,
      current => {
        const previous = cloneDataSource(entry.past[entry.past.length - 1])
        setDataSourceHistory(history => {
          const active = history[idx] || { past: [], future: [] }
          const past = active.past.slice(0, -1)
          const future = [cloneDataSource(current), ...active.future].slice(0, 100)
          return { ...history, [idx]: { past, future } }
        })
        return previous
      },
      false,
    )
  }

  const redoDataSource = (idx: number) => {
    const entry = dataSourceHistory[idx]
    if (!entry || entry.future.length === 0) return

    mutateDataSource(
      idx,
      current => {
        const [nextState, ...remainingFuture] = entry.future
        setDataSourceHistory(history => {
          const active = history[idx] || { past: [], future: [] }
          const past = [...active.past, cloneDataSource(current)].slice(-100)
          return { ...history, [idx]: { past, future: remainingFuture } }
        })
        return cloneDataSource(nextState)
      },
      false,
    )
  }

  const importDataSourceFile = async (idx: number, file: File | null) => {
    if (!file) return
    try {
      const text = await file.text()
      updateDataSource(idx, 'inline_data', text)
      setSuccess(`Imported ${file.name}`)
      setError('')
    } catch {
      setError('Failed to import parameter file')
    }
  }

  const exportDataSourceFile = (source: DataSourceConfig, idx: number) => {
    const content = source.inline_data || ''
    if (!content.trim()) {
      setError('No inline parameter data to export for this source')
      return
    }
    const base = (source.name || `source_${idx + 1}`).replace(/[^a-zA-Z0-9_-]+/g, '_')
    const ext = (source.type || 'csv').toLowerCase() === 'dat' ? 'dat' : 'csv'
    downloadTextFile(content, `${base}.${ext}`)
    setError('')
    setSuccess(`Exported ${base}.${ext}`)
  }

  const updateDataCell = (sourceIdx: number, rowIdx: number, colIdx: number, value: string) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = rows.map(row => [...row])
    while (nextRows.length <= rowIdx) nextRows.push(Array(columns.length).fill(''))
    while (nextRows[rowIdx].length < columns.length) nextRows[rowIdx].push('')
    nextRows[rowIdx][colIdx] = value
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, columns, nextRows))
  }

  const updateDataColumnName = (sourceIdx: number, colIdx: number, value: string) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextColumns = [...columns]
    nextColumns[colIdx] = value || `col_${colIdx + 1}`
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? nextColumns : [],
      inline_data: stringifyInlineData(current, nextColumns, rows),
    }))
  }

  const addDataRow = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = [...rows, Array(columns.length).fill('')]
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, columns, nextRows))
  }

  const removeDataRow = (sourceIdx: number, rowIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextRows = rows.filter((_, idx) => idx !== rowIdx)
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, columns, nextRows))
  }

  const addDataColumn = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    const nextColumns = [...columns, `col_${columns.length + 1}`]
    const nextRows = rows.map(row => [...row, ''])
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? nextColumns : [],
      inline_data: stringifyInlineData(current, nextColumns, nextRows),
    }))
  }

  const removeDataColumn = (sourceIdx: number, colIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const { columns, rows } = parseInlineData(source)
    if (columns.length <= 1) return
    const nextColumns = columns.filter((_, idx) => idx !== colIdx)
    const nextRows = rows.map(row => row.filter((_, idx) => idx !== colIdx))
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? nextColumns : [],
      inline_data: stringifyInlineData(current, nextColumns, nextRows),
    }))
  }

  const autoFixDataSource = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = normalizeHeaders(parsed.columns)
    const fixedRows = parsed.rows.map(row => {
      const next = [...row]
      while (next.length < fixedColumns.length) next.push('')
      return next.slice(0, fixedColumns.length)
    })
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, fixedRows),
    }))
    setSuccess(`Applied data auto-fix to ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
  }

  const applyFillHeaders = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = fillEmptyHeaders(parsed.columns)
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, parsed.rows),
    }))
    setSuccess(`Filled empty headers for ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
  }

  const applyDedupeHeaders = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedColumns = dedupeHeaders(parsed.columns)
    mutateDataSource(sourceIdx, current => ({
      ...current,
      columns: current.first_row_header === false ? fixedColumns : [],
      inline_data: stringifyInlineData(current, fixedColumns, parsed.rows),
    }))
    setSuccess(`Deduplicated headers for ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
  }

  const applyNormalizeRows = (sourceIdx: number) => {
    const source = (script.data_sources || [])[sourceIdx]
    if (!source) return
    const parsed = parseInlineData(source)
    const fixedRows = parsed.rows.map(row => {
      const next = [...row]
      while (next.length < parsed.columns.length) next.push('')
      return next.slice(0, parsed.columns.length)
    })
    updateDataSource(sourceIdx, 'inline_data', stringifyInlineData(source, parsed.columns, fixedRows))
    setSuccess(`Normalized row widths for ${source.name || `source_${sourceIdx + 1}`}`)
    setError('')
  }

  const scanForAutoParameters = () => {
    const candidates = buildAutoParameterCandidates(script)
    setParameterCandidates(candidates)
    if (candidates.length === 0) {
      setSuccess('No strong parameterization candidates were detected.')
      setError('')
      return
    }
    setSuccess(`Detected ${candidates.length} parameter candidates. Review and apply below.`)
    setError('')
  }

  const updateCandidate = (id: string, patch: Partial<ParameterCandidate>) => {
    setParameterCandidates(prev => prev.map(candidate => candidate.id === id ? { ...candidate, ...patch } : candidate))
  }

  const applyAutoParameterization = () => {
    const selected = parameterCandidates
      .filter(candidate => candidate.selected)
      .map(candidate => ({ ...candidate, varName: sanitizeVarName(candidate.varName) }))
      .filter(candidate => candidate.varName.length > 0)

    if (selected.length === 0) {
      setError('Select at least one candidate before applying auto-parameterization.')
      return
    }

    const replacementMap = new Map<string, string>()
    selected.forEach(candidate => {
      replacementMap.set(candidate.id, `{{data.auto_params.${candidate.varName}}}`)
    })

    setScript(prev => {
      const next = JSON.parse(JSON.stringify(prev)) as ScriptData
      next.actions = next.actions.map((action, actionIdx) => {
        const updated = { ...action, headers: { ...(action.headers || {}) } }
        selected.forEach(candidate => {
          candidate.occurrences
            .filter(occ => occ.actionIdx === actionIdx)
            .forEach(occ => {
              const token = replacementMap.get(candidate.id) || candidate.value
              if (occ.field === 'target' && updated.target) {
                updated.target = updated.target.split(candidate.value).join(token)
              } else if (occ.field === 'body' && typeof updated.body === 'string') {
                updated.body = updated.body.split(candidate.value).join(token)
              } else if (occ.field === 'header' && occ.headerKey && updated.headers && updated.headers[occ.headerKey]) {
                updated.headers[occ.headerKey] = updated.headers[occ.headerKey].split(candidate.value).join(token)
              }
            })
        })
        return updated
      })

      const autoSourceIdx = (next.data_sources || []).findIndex(source => source.name === 'auto_params')
      const existing = autoSourceIdx >= 0 ? next.data_sources?.[autoSourceIdx] : undefined
      const existingParsed = existing ? parseInlineData(existing) : { columns: [] as string[], rows: [] as string[][] }
      const mergedColumns = [...existingParsed.columns]
      selected.forEach(candidate => {
        if (!mergedColumns.includes(candidate.varName)) mergedColumns.push(candidate.varName)
      })

      const mergedRow: string[] = mergedColumns.map((column, idx) => {
        const selectedMatch = selected.find(candidate => candidate.varName === column)
        if (selectedMatch) return selectedMatch.value
        return existingParsed.rows[0]?.[idx] || ''
      })

      const sourcePayload: DataSourceConfig = {
        ...(existing || EMPTY_DATA_SOURCE),
        name: 'auto_params',
        type: (existing?.type || 'csv'),
        strategy: existing?.strategy || 'sequential',
        first_row_header: true,
        columns: [],
        inline_data: stringifyInlineData(
          { ...(existing || EMPTY_DATA_SOURCE), type: existing?.type || 'csv', delimiter: existing?.delimiter || ',' },
          mergedColumns,
          [mergedRow],
        ),
      }

      if (!next.data_sources) next.data_sources = []
      if (autoSourceIdx >= 0) next.data_sources[autoSourceIdx] = sourcePayload
      else next.data_sources.push(sourcePayload)
      return next
    })

    setSuccess(`Applied auto-parameterization for ${selected.length} candidate${selected.length === 1 ? '' : 's'}.`)
    setError('')
  }

  return (
    <div className="script-editor">
      <div className="editor-header">
        <div className="editor-title-row">
          <h2>{scriptId ? 'Edit Script' : 'Create Script'}</h2>
          <div className="editor-actions">
            <button className="btn btn-ghost" onClick={toggleJsonMode}>
              {jsonMode ? '◧ Form' : '{ } JSON'}
            </button>
            <button className="btn btn-ghost" onClick={handleValidateScript} disabled={saving || replaying || validating || !script.name}>
              {validating ? 'Validating…' : (scriptId ? 'Validate Saved Script' : 'Validate Draft')}
            </button>
            {isBrowserScript && activeReplaySessionId && (
              <button className="btn btn-ghost" onClick={handleStopReplay} disabled={!replaying}>
                Stop Replay
              </button>
            )}
            <button className="btn btn-ghost" onClick={handleReplay} disabled={replaying || saving || validating || !script.name || script.actions.length === 0}>
              {replaying ? (isBrowserScript ? 'Browser Replay…' : 'Replaying…') : (isBrowserScript ? 'Browser Replay' : 'Replay')}
            </button>
            <button className="btn btn-primary" onClick={handleSave} disabled={saving || validating || !script.name}>
              {saving ? 'Saving…' : 'Save Script'}
            </button>
            {onClose && <button className="btn btn-ghost" onClick={onClose}>✕</button>}
          </div>
        </div>
        {error && <div className="editor-toast error">{error}</div>}
        {warnings.length > 0 && (
          <div className="editor-toast warning">
            {warnings.map((warning, index) => (
              <div key={`${warning}-${index}`}>{warning}</div>
            ))}
          </div>
        )}
        {success && <div className="editor-toast success">{success}</div>}
      </div>

      {jsonMode ? (
        <div className="json-editor">
          <textarea value={jsonText} onChange={e => setJsonText(e.target.value)} spellCheck={false} />
        </div>
      ) : (
        <div className="editor-body">
          {/* Script Config Panel */}
          <div className={`config-panel ${openSections['config'] ? 'open' : ''}`}>
            <button className="panel-header-toggle" onClick={() => toggleSection('config')}>
              <h3 className="panel-title">Script Configuration</h3>
              <span className="chevron">{openSections['config'] ? '▾' : '▸'}</span>
            </button>
            {openSections['config'] && (
              <div className="panel-body">
                <div className="form-grid">
                  <label>
                    <span>Name</span>
                    <input value={script.name} onChange={e => updateField('name', e.target.value)} placeholder="My_Test_Script" />
                  </label>
                  {showProtocolSelector && (
                    <label>
                      <span>Protocol</span>
                      <select value={script.protocol} onChange={e => updateProtocol(e.target.value)}>
                        <option value="http">HTTP API</option>
                        <option value="https">HTTPS API</option>
                        <option value="java_http">Java over HTTP</option>
                        <option value="soap">Web Services (SOAP)</option>
                        <option value="browser">Web/HTML Browser</option>
                        <option value="jms-solace">JMS Solace</option>
                      </select>
                    </label>
                  )}
                  <label>
                    <span>Version</span>
                    <input value={script.version} onChange={e => updateField('version', e.target.value)} placeholder="1.0.0" />
                  </label>
                  {!isJmsScript && (
                    <label>
                      <span>Base URL</span>
                      <input value={script.config.base_url} onChange={e => updateField('config.base_url', e.target.value)} placeholder="https://api.example.com" />
                    </label>
                  )}
                  {isJmsScript && (
                    <>
                      <label>
                        <span>Broker URL</span>
                        <input value={script.config.jms?.broker_url || ''} onChange={e => updateField('config.jms.broker_url', e.target.value)} placeholder="tcps://broker.example.com:55443" />
                      </label>
                      <label>
                        <span>Message VPN</span>
                        <input value={script.config.jms?.message_vpn || ''} onChange={e => updateField('config.jms.message_vpn', e.target.value)} placeholder="default" />
                      </label>
                      <label>
                        <span>Default Destination</span>
                        <input value={script.config.jms?.destination || ''} onChange={e => updateField('config.jms.destination', e.target.value)} placeholder="orders.in" />
                      </label>
                    </>
                  )}
                </div>
                {isJmsScript && (
                  <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                    JMS Solace scripts use publish, consume, and request/reply actions with message body, headers, properties, correlation IDs, and reply destinations in the action editor below.
                  </div>
                )}
                {isBrowserScript && (
                  <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                    Browser scripts can use navigate, click, fill, select, wait, tap, long-press, swipe, and screenshot actions with device presets, touch controls, and client metrics in the action editor below.
                  </div>
                )}
                {isSoapScript && (
                  <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                    SOAP scripts reuse the HTTP transport model and add SOAP envelope, namespace, WS-Security, SOAPAction, and fault-handling settings below.
                  </div>
                )}
                {isJavaHttpScript && (
                  <div className="parameter-empty-state" style={{ marginTop: 12 }}>
                    Java-over-HTTP scripts reuse the HTTP transport client, support embedded or sidecar helper modes, and currently ship with the `java_serialized_object_base64` codec where request bodies are base64-encoded serialized bytes.
                  </div>
                )}
                <div className="auto-parameter-toolbar">
                  <button className="btn btn-sm btn-ghost" onClick={scanForAutoParameters}>Auto Parameterization</button>
                  <span className="cell-muted">Scans targets, headers, and body fields for likely parameter candidates.</span>
                </div>
                {parameterCandidates.length > 0 && (
                  <div className="auto-parameter-panel">
                    <div className="auto-parameter-header">
                      <h4>Review Candidates</h4>
                      <button className="btn btn-sm btn-accent" onClick={applyAutoParameterization}>Apply Selected</button>
                    </div>
                    <div className="auto-parameter-table-wrap">
                      <table className="auto-parameter-table">
                        <thead>
                          <tr>
                            <th>Use</th>
                            <th>Field Hint</th>
                            <th>Current Value</th>
                            <th>Variable Name</th>
                            <th>Hits</th>
                          </tr>
                        </thead>
                        <tbody>
                          {parameterCandidates.map(candidate => (
                            <tr key={candidate.id}>
                              <td>
                                <input
                                  type="checkbox"
                                  checked={candidate.selected}
                                  onChange={e => updateCandidate(candidate.id, { selected: e.target.checked })}
                                />
                              </td>
                              <td className="cell-mono">{candidate.keyHint}</td>
                              <td className="cell-mono">{candidate.value}</td>
                              <td>
                                <input
                                  value={candidate.varName}
                                  onChange={e => updateCandidate(candidate.id, { varName: sanitizeVarName(e.target.value) })}
                                  placeholder="parameter_name"
                                />
                              </td>
                              <td>{candidate.occurrences.length}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>

          <div className={`config-panel replay-panel ${openSections['replay'] ? 'open' : ''}`}>
            <button className="panel-header-toggle" onClick={() => toggleSection('replay')}>
              <div className="panel-title-row">
                <div>
                  <h3 className="panel-title">Replay Output {replaying && <span className="replaying-dot" />}</h3>
                </div>
              </div>
              <span className="chevron">{openSections['replay'] ? '▾' : '▸'}</span>
            </button>
            {openSections['replay'] && (
              <div className="panel-body">
                <p className="replay-panel-subtitle">{isBrowserScript ? 'Start an asynchronous browser replay session and inspect live events, failures, and final replay state.' : 'Run the current draft once and inspect resolved requests, extractions, failures, and final replay variables.'}</p>
                <div className="replay-options-grid">
                  <label>
                    <span>Replay Group</span>
                    <input
                      value={replayOptions.groupName}
                      onChange={e => setReplayOptions(prev => ({ ...prev, groupName: e.target.value }))}
                      placeholder={`${script.name || 'script'}-replay`}
                    />
                  </label>
                  <label className="checkbox-label replay-checkbox">
                    <input
                      type="checkbox"
                      checked={replayOptions.ignoreThinkTime}
                      onChange={e => setReplayOptions(prev => ({ ...prev, ignoreThinkTime: e.target.checked }))}
                    />
                    <span>Ignore think time for replay</span>
                  </label>
                  {isBrowserScript && (
                    <label className="checkbox-label replay-checkbox">
                      <input
                        type="checkbox"
                        checked={replayOptions.startPaused}
                        onChange={e => setReplayOptions(prev => ({ ...prev, startPaused: e.target.checked }))}
                      />
                      <span>Start paused</span>
                    </label>
                  )}
                  <label className="full-width replay-variables-field">
                    <span>Replay Variables JSON</span>
                    <textarea
                      value={replayOptions.variablesText}
                      onChange={e => setReplayOptions(prev => ({ ...prev, variablesText: e.target.value }))}
                      spellCheck={false}
                    />
                  </label>
                </div>
                <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-end' }}>
                  {isBrowserScript && replayReport && (
                    <button className="btn btn-sm btn-ghost" onClick={handleDownloadBrowserReplayBundle}>
                      Download Replay ZIP
                    </button>
                  )}
                  {isBrowserScript && replayReport && (
                    <button className="btn btn-sm btn-ghost" onClick={handleDownloadBrowserReplayExport}>
                      Download Replay JSON
                    </button>
                  )}
                  {isBrowserScript && activeReplaySessionId && (
                    <button className="btn btn-sm btn-ghost" onClick={handlePauseResumeReplay} disabled={!replaying || !browserReplaySession || browserReplaySession.status === 'completed' || browserReplaySession.status === 'failed' || browserReplaySession.status === 'stopped'}>
                      {isReplayPaused ? 'Resume Replay' : 'Pause Replay'}
                    </button>
                  )}
                  {isBrowserScript && activeReplaySessionId && (
                    <button className="btn btn-sm btn-ghost" onClick={handleStopReplay} disabled={!replaying}>
                      Stop Replay
                    </button>
                  )}
                  <button className="btn btn-sm btn-accent" onClick={handleReplay} disabled={replaying || saving || !script.name || script.actions.length === 0}>
                    {replaying ? (isBrowserScript ? 'Running Browser Replay…' : 'Running…') : (isBrowserScript ? 'Run Browser Replay' : 'Run Replay')}
                  </button>
                </div>

                {!replayReport ? (
                  <div className="replay-empty-state">{isBrowserScript ? 'No browser replay has been started yet. Use Browser Replay to launch an asynchronous session for the current draft.' : 'No replay has been run yet. Use Replay to execute the current draft once.'}</div>
                ) : (
                  <div className="replay-report">
                    <div className={`replay-live-banner ${replaying ? 'running' : replayReport.success ? 'success' : 'error'}`}>
                      <div className="replay-live-banner-main">
                        <span className="replay-live-badge">{replaying ? 'Streaming live' : 'Replay finished'}</span>
                        <strong>{replayLatestActivity?.title || 'Replay status available'}</strong>
                        <span>{replayLatestActivity?.detail || 'Inspect the timeline below for request-by-request execution details.'}</span>
                      </div>
                      <div className="replay-live-banner-meta">
                        <span>{replayReport.events.length} / {script.actions.length} actions reported</span>
                        <span>{formatReplayTime(replayLatestActivity?.timestamp || replayReport.completed_at || replayReport.started_at)}</span>
                      </div>
                    </div>
                    <div className="replay-summary-grid">
                      <div className="replay-summary-card">
                        <span>Status</span>
                        <strong className={replayStatusLabel === 'Failed' ? 'text-danger' : replayStatusLabel === 'Running' ? '' : 'text-success'}>{replayStatusLabel}</strong>
                      </div>
                      <div className="replay-summary-card">
                        <span>Group</span>
                        <strong>{replayReport.group || 'replay'}</strong>
                      </div>
                      <div className="replay-summary-card">
                        <span>Protocol</span>
                        <strong>{replayReport.protocol}</strong>
                      </div>
                      <div className="replay-summary-card">
                        <span>Events</span>
                        <strong>{replayReport.events.length}</strong>
                      </div>
                      <div className="replay-summary-card">
                        <span>Completed</span>
                        <strong>{formatReplayTime(replayReport.completed_at)}</strong>
                      </div>
                    </div>
                    {replayReport.error && <p className="replay-report-error">{replayReport.error}</p>}
                    {isBrowserScript && (
                      <BrowserReplayDebugger
                        sessionId={activeReplaySessionId}
                        session={browserReplaySession}
                        report={replayReport}
                        running={replaying}
                        selectedEventIndex={expandedReplayEvent}
                        onSelectEvent={setExpandedReplayEvent}
                      />
                    )}
                    {replayActivity.length > 0 && (
                      <div className="replay-activity-card">
                        <h4>Execution Timeline</h4>
                        <div className="replay-activity-list">
                          {replayActivity.map(item => (
                            <div key={item.id} className={`replay-activity-item ${item.kind} ${item.success === false ? 'error' : item.success === true ? 'success' : ''}`}>
                              <div className="replay-activity-marker" />
                              <div className="replay-activity-content">
                                <div className="replay-activity-header-row">
                                  <strong>{item.title}</strong>
                                  <span>{formatReplayTime(item.timestamp)}</span>
                                </div>
                                {item.detail && <div className="replay-activity-detail">{item.detail}</div>}
                                {item.changedVariables && item.changedVariables.length > 0 && (
                                  <div className="replay-variable-chip-row">
                                    {item.changedVariables.map(name => (
                                      <span key={`${item.id}-${name}`} className="replay-variable-chip">{name}</span>
                                    ))}
                                  </div>
                                )}
                              </div>
                            </div>
                          ))}
                        </div>
                      </div>
                    )}
                    {replayVariableInspector && (
                      <div className="replay-variables-card">
                        <h4>{replaying ? 'Live Variables' : 'Variable Inspector'}</h4>
                        {Object.keys(replayVariableInspector).length > 0 ? (
                          <SnapshotBodyPreview
                            body={JSON.stringify(replayVariableInspector, null, 2)}
                            headers={{ 'Content-Type': 'application/json' }}
                          />
                        ) : (
                          <div className="replay-variables-empty">
                            {replaying
                              ? 'No replay variables have been captured yet.'
                              : 'Replay completed without any extracted or seeded variables.'}
                          </div>
                        )}
                      </div>
                    )}
                    <div className="replay-events-list">
                      {replayReport.events.map((event, index) => {
                        const isOpen = expandedReplayEvent === index
                        const variableDiff = replayVariableDiffs[index] || {}
                        const changedVariables = Object.entries(variableDiff)
                        const snapshotSoapFault = event.snapshot ? extractSOAPFaultSummaryFromBody(event.snapshot.response_body) : null
                        const replayPageURL = event.page_url || event.snapshot?.page_url
                        const replayPageTitle = event.page_title || event.snapshot?.page_title
                        const replayDOMSummary = event.dom_summary || event.snapshot?.dom_summary
                        const replayConsoleEntries = event.console_entries || event.snapshot?.console_entries
                        const replayNetworkEntries = event.network_entries || event.snapshot?.network_entries
                        const replayHTML = event.html || event.snapshot?.html
                        const replayScreenshotBase64 = event.screenshot_base64 || event.snapshot?.screenshot_base64
                        return (
                          <div
                            key={`${event.phase}-${event.action_name}-${index}`}
                            className={`replay-event-card ${event.success ? 'success' : 'error'}`}
                            ref={node => {
                              replayEventRefs.current[index] = node
                            }}
                          >
                            <button className="replay-event-header" onClick={() => setExpandedReplayEvent(isOpen ? null : index)}>
                              <div className="replay-event-header-main">
                                <span className="replay-event-phase">{event.phase}</span>
                                <strong>{event.action_name || 'Unnamed action'}</strong>
                                <span className="replay-event-target">{event.method || event.action_type} {event.target}</span>
                              </div>
                              <div className="replay-event-header-meta">
                                {changedVariables.length > 0 && (
                                  <span className="replay-event-diff-count">{changedVariables.length} variable{changedVariables.length === 1 ? '' : 's'} changed</span>
                                )}
                                <span className={event.success ? 'text-success' : 'text-danger'}>{event.success ? `HTTP ${event.status_code || 'OK'}` : event.error_message || 'Failed'}</span>
                                <span>{event.duration_ms.toFixed(1)} ms</span>
                                <span>{isOpen ? '▾' : '▸'}</span>
                              </div>
                            </button>
                            {isOpen && (
                              <div className="replay-event-body">
                                <div className="replay-event-detail-grid">
                                  <div>
                                    <span className="replay-detail-label">Started</span>
                                    <div>{formatReplayTime(event.started_at)}</div>
                                  </div>
                                  <div>
                                    <span className="replay-detail-label">Completed</span>
                                    <div>{formatReplayTime(event.completed_at)}</div>
                                  </div>
                                  {replayPageURL && (
                                    <div>
                                      <span className="replay-detail-label">Page URL</span>
                                      <div className="replay-page-url">{replayPageURL}</div>
                                    </div>
                                  )}
                                  {replayPageTitle && (
                                    <div>
                                      <span className="replay-detail-label">Page Title</span>
                                      <div>{replayPageTitle}</div>
                                    </div>
                                  )}
                                </div>
                                {hasBrowserReplayArtifacts(event) && (
                                  <div className="replay-browser-grid">
                                    {(() => {
                                      const browserArtifacts = [
                                        replayDOMSummary ? 'dom_summary' : null,
                                        replayConsoleEntries?.length ? 'console' : null,
                                        replayNetworkEntries?.length ? 'network' : null,
                                        replayHTML ? 'html' : null,
                                      ].filter((artifact): artifact is 'dom_summary' | 'console' | 'network' | 'html' => Boolean(artifact))
                                      const browserArtifactKey = (artifact: 'dom_summary' | 'console' | 'network' | 'html') => `${index}:${artifact}`
                                      const allBrowserArtifactsExpanded = browserArtifacts.length > 0 && browserArtifacts.every(artifact => expandedReplayBrowserArtifacts[browserArtifactKey(artifact)])

                                      return (
                                        <div className="replay-browser-grid-header">
                                          <span className="replay-detail-label">Browser Artifacts</span>
                                          {browserArtifacts.length > 0 && (
                                            <button
                                              type="button"
                                              className="btn btn-sm btn-ghost"
                                              onClick={() => {
                                                browserArtifacts.forEach(artifact => {
                                                  toggleReplayBrowserArtifact(browserArtifactKey(artifact), !allBrowserArtifactsExpanded)
                                                })
                                              }}
                                            >
                                              {allBrowserArtifactsExpanded ? 'Collapse artifacts' : 'Expand artifacts'}
                                            </button>
                                          )}
                                        </div>
                                      )
                                    })()}
                                    {replayDOMSummary && (
                                      <div className="replay-code-block">
                                        <span className="replay-detail-label">DOM Summary</span>
                                        <SnapshotBodyPreview
                                          body={JSON.stringify(replayDOMSummary, null, 2)}
                                          headers={{ 'Content-Type': 'application/json' }}
                                          expanded={expandedReplayBrowserArtifacts[`${index}:dom_summary`]}
                                          onExpandedChange={(expanded) => toggleReplayBrowserArtifact(`${index}:dom_summary`, expanded)}
                                        />
                                      </div>
                                    )}
                                    {replayConsoleEntries?.length ? (
                                      <div className="replay-code-block">
                                        <span className="replay-detail-label">Console</span>
                                        <SnapshotTextPreview
                                          text={formatConsoleEntries(replayConsoleEntries)}
                                          expanded={expandedReplayBrowserArtifacts[`${index}:console`]}
                                          onExpandedChange={(expanded) => toggleReplayBrowserArtifact(`${index}:console`, expanded)}
                                        />
                                      </div>
                                    ) : null}
                                    {replayNetworkEntries?.length ? (
                                      <div className="replay-code-block">
                                        <span className="replay-detail-label">Network</span>
                                        <SnapshotTextPreview
                                          text={formatNetworkEntries(replayNetworkEntries)}
                                          expanded={expandedReplayBrowserArtifacts[`${index}:network`]}
                                          onExpandedChange={(expanded) => toggleReplayBrowserArtifact(`${index}:network`, expanded)}
                                        />
                                      </div>
                                    ) : null}
                                    {replayScreenshotBase64 && (
                                      <div className="replay-screenshot-block">
                                        <div className="replay-code-block-header">
                                          <span className="replay-detail-label">Screenshot</span>
                                          <button
                                            type="button"
                                            className="btn btn-sm btn-ghost"
                                            onClick={() => setReplayScreenshotSelection({
                                              alt: `${event.action_name || 'Replay action'} screenshot`,
                                              src: `data:image/png;base64,${replayScreenshotBase64}`,
                                            })}
                                          >
                                            View full size
                                          </button>
                                        </div>
                                        <button
                                          type="button"
                                          className="replay-screenshot-button"
                                          onClick={() => setReplayScreenshotSelection({
                                            alt: `${event.action_name || 'Replay action'} screenshot`,
                                            src: `data:image/png;base64,${replayScreenshotBase64}`,
                                          })}
                                        >
                                          <img src={`data:image/png;base64,${replayScreenshotBase64}`} alt={`${event.action_name || 'Replay action'} screenshot`} />
                                        </button>
                                      </div>
                                    )}
                                    {replayHTML && (
                                      <div className="replay-code-block replay-browser-html">
                                        <span className="replay-detail-label">HTML</span>
                                        <SnapshotTextPreview
                                          text={replayHTML}
                                          expanded={expandedReplayBrowserArtifacts[`${index}:html`]}
                                          onExpandedChange={(expanded) => toggleReplayBrowserArtifact(`${index}:html`, expanded)}
                                        />
                                      </div>
                                    )}
                                  </div>
                                )}
                                <div className="replay-code-block">
                                  <span className="replay-detail-label">Resolved Headers</span>
                                  <SnapshotTextPreview text={formatReplayHeaders(event.headers)} />
                                </div>
                                {event.body && (
                                  <div className="replay-code-block">
                                    <span className="replay-detail-label">Resolved Body</span>
                                    <SnapshotBodyPreview
                                      body={event.body}
                                      headers={event.headers}
                                    />
                                  </div>
                                )}
                                {event.extractions && Object.keys(event.extractions).length > 0 && (
                                  <div className="replay-code-block">
                                    <span className="replay-detail-label">Extractions</span>
                                    <SnapshotBodyPreview
                                      body={JSON.stringify(event.extractions, null, 2)}
                                      headers={{ 'Content-Type': 'application/json' }}
                                    />
                                  </div>
                                )}
                                {changedVariables.length > 0 && (
                                  <div className="replay-code-block">
                                    <span className="replay-detail-label">Variable Changes</span>
                                    <div className="replay-variable-diff-list">
                                      {changedVariables.map(([name, diff]) => (
                                        <div key={`${index}-${name}`} className="replay-variable-diff-item">
                                          <div className="replay-variable-diff-name">{name}</div>
                                          <div className="replay-variable-diff-values">
                                            <span className="replay-variable-diff-before">{diff.previous ?? 'unset'}</span>
                                            <span className="replay-variable-diff-arrow">{'->'}</span>
                                            <span className="replay-variable-diff-after">{diff.next}</span>
                                          </div>
                                        </div>
                                      ))}
                                    </div>
                                  </div>
                                )}
                                {event.snapshot && (
                                  hasStructuredReplaySnapshot(event.snapshot) ? (
                                    <div className="replay-event-detail-grid">
                                      {snapshotSoapFault && (
                                        <div className="replay-code-block">
                                          <div className="replay-code-block-header">
                                            <span className="replay-detail-label">SOAP Fault</span>
                                            <button
                                              type="button"
                                              className="btn btn-sm btn-ghost"
                                              onClick={() => void handleCopyReplaySnapshotSection(`${index}:soap-fault`, formatSOAPFaultSummary(snapshotSoapFault))}
                                            >
                                              {copiedReplaySnapshotSection === `${index}:soap-fault` ? 'Copied' : 'Copy fault'}
                                            </button>
                                          </div>
                                          <pre>{formatSOAPFaultSummary(snapshotSoapFault)}</pre>
                                        </div>
                                      )}
                                      <div className="replay-code-block">
                                        <div className="replay-code-block-header">
                                          <span className="replay-detail-label">Snapshot Request</span>
                                          <button
                                            type="button"
                                            className="btn btn-sm btn-ghost"
                                            onClick={() => void handleCopyReplaySnapshotSection(`${index}:request`, formatSnapshotRequestSection(event.snapshot))}
                                          >
                                            {copiedReplaySnapshotSection === `${index}:request` ? 'Copied' : 'Copy request'}
                                          </button>
                                        </div>
                                        <pre>{`${event.snapshot.request_method || 'n/a'} ${event.snapshot.request_url || ''}`.trim()}</pre>
                                        <SnapshotTextPreview text={formatReplayHeaders(event.snapshot.request_headers)} />
                                        <SnapshotBodyPreview
                                          body={event.snapshot.request_body}
                                          headers={event.snapshot.request_headers}
                                          emptyMessage="No request body captured"
                                        />
                                      </div>
                                      <div className="replay-code-block">
                                        <div className="replay-code-block-header">
                                          <span className="replay-detail-label">Snapshot Response</span>
                                          <button
                                            type="button"
                                            className="btn btn-sm btn-ghost"
                                            onClick={() => void handleCopyReplaySnapshotSection(`${index}:response`, formatSnapshotResponseSection(event.snapshot, event.status_code))}
                                          >
                                            {copiedReplaySnapshotSection === `${index}:response` ? 'Copied' : 'Copy response'}
                                          </button>
                                        </div>
                                        <pre>{String(event.snapshot.response_status || event.status_code || 'n/a')}</pre>
                                        <SnapshotTextPreview text={formatReplayHeaders(event.snapshot.response_headers)} />
                                        <SnapshotBodyPreview
                                          body={event.snapshot.response_body}
                                          headers={event.snapshot.response_headers}
                                          emptyMessage="No response body captured"
                                        />
                                      </div>
                                    </div>
                                  ) : (
                                    <div className="replay-code-block">
                                      <span className="replay-detail-label">Snapshot</span>
                                      <SnapshotBodyPreview
                                        body={JSON.stringify(event.snapshot, null, 2)}
                                        headers={{ 'Content-Type': 'application/json' }}
                                      />
                                    </div>
                                  )
                                )}
                              </div>
                            )}
                          </div>
                        )
                      })}
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>

          {importMetadata && (
            <div className={`import-metadata-panel config-panel ${openSections['import'] ? 'open' : ''}`} style={{ padding: 0 }}>
              <button className="panel-header-toggle" onClick={() => toggleSection('import')} style={{ padding: '16px 20px' }}>
                <div className="import-metadata-header" style={{ marginBottom: 0 }}>
                  <div>
                    <h3 className="panel-title">Imported Metadata</h3>
                  </div>
                  <span className="import-source-chip">{importMetadata.source || 'loadrunner'}</span>
                </div>
                <span className="chevron">{openSections['import'] ? '▾' : '▸'}</span>
              </button>

              {openSections['import'] && (
                <div className="panel-body" style={{ padding: '0 20px 20px' }}>
                  <p style={{ color: 'var(--text-muted)', fontSize: 13, marginBottom: 14 }}>This script retains source-specific import metadata that Stormrunner uses during review but does not edit directly.</p>
                  <div className="import-metadata-grid">
                    <div className="import-metadata-card">
                      <span>Source Script</span>
                      <strong>{importMetadata.source_script_name || script.name}</strong>
                    </div>
                    <div className="import-metadata-card">
                      <span>Project Sections</span>
                      <strong>{importSummary.projectSections.length}</strong>
                      <small>{importSummary.projectSections.join(', ') || 'None captured'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Runtime Files</span>
                      <strong>{importSummary.runtimeFiles.length}</strong>
                      <small>{importSummary.runtimeFiles.join(', ') || 'None found'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Unsupported Functions</span>
                      <strong>{importSummary.unsupportedFunctions.length}</strong>
                      <small>{importSummary.unsupportedFunctions.slice(0, 4).join(', ') || 'None'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Parameter Files</span>
                      <strong>{importSummary.parameterFiles.length}</strong>
                      <small>{importSummary.parameterFiles.join(', ') || 'None found'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Correlation Suggestions</span>
                      <strong>{importSummary.correlationSuggestions.length}</strong>
                      <small>{importSummary.correlationSuggestions.length > 0 ? 'Imported from the conversion preview' : 'None detected'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Generated Data Sources</span>
                      <strong>{importedDataSourceSummaries.length}</strong>
                      <small>{importedDataSourceSummaries.length > 0 ? importedDataSourceSummaries.map((source) => source.name).join(', ') : 'None generated'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Runtime Setting Parity</span>
                      <strong>{importSummary.runtimeSettingsParity.length}</strong>
                      <small>{runtimeSettingParitySummary || 'No classified runtime settings found'}</small>
                    </div>
                    <div className="import-metadata-card">
                      <span>Migration Readiness</span>
                      <strong>{importSummary.runtimeSettingReadiness ? `${importSummary.runtimeSettingReadiness.score_pct}%` : 'N/A'}</strong>
                      <small>{runtimeSettingReadinessSummary || 'No migration readiness score captured'}</small>
                    </div>
                  </div>

                  {importSummary.runtimeSettingReadiness && (
                    <div className="import-correlation-section">
                      <h4>Migration Readiness</h4>
                      <p>{runtimeSettingReadinessSummary}</p>
                      {importSummary.runtimeSettingReadiness.blocking_settings.length > 0 && (
                        <div className="import-correlation-list">
                          {importSummary.runtimeSettingReadiness.blocking_settings.map((entry) => (
                            <div key={`readiness-${entry.id}`} className="import-correlation-card">
                              <strong>{formatRuntimeSettingStatusLabel(entry.status)}: {entry.area}</strong>
                              <span>{entry.concept}</span>
                              <span>{entry.runtime_files.length > 0 ? entry.runtime_files.join(', ') : 'No runtime file recorded'}</span>
                              {getImportedRuntimeSectionId(entry.area) && (
                                <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                                  <button className="btn btn-secondary btn-sm" type="button" onClick={() => openImportedRuntimeSettings(entry.area)}>
                                    {`Open ${entry.area} Settings`}
                                  </button>
                                </div>
                              )}
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  )}

                  {importSummary.runtimeSettingsParity.length > 0 && (
                    <div className="import-correlation-section">
                      <h4>Runtime Settings Parity</h4>
                      <p>These imported LoadRunner runtime settings were classified against Stormrunner&apos;s code-owned support matrix.</p>
                      <div className="import-correlation-list">
                        {importSummary.runtimeSettingsParity.map((entry) => (
                          <div key={entry.id} className="import-correlation-card">
                            <strong>{formatRuntimeSettingStatusLabel(entry.status)}: {entry.area}</strong>
                            <span>{entry.concept}</span>
                            <span>{entry.runtime_files.length > 0 ? entry.runtime_files.join(', ') : 'No runtime file recorded'}</span>
                            {getImportedRuntimeSectionId(entry.area) && (
                              <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                                <button className="btn btn-secondary btn-sm" type="button" onClick={() => openImportedRuntimeSettings(entry.area)}>
                                  {`Open ${entry.area} Settings`}
                                </button>
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {importSummary.correlationSuggestions.length > 0 && (
                    <div className="import-correlation-section">
                      <h4>Correlation Suggestions</h4>
                      <p>Review the imported extractor mappings before you rename variables or rewrite dependent requests.</p>
                      <div className="import-correlation-list">
                        {importedCorrelationFocusTargets.map(({ correlation, foundAtActionIndex, foundAtLabel, usedAtLocations, usedAtTargets }) => (
                          <div key={`${correlation.name}:${correlation.expression}:${correlation.found_at}`} className="import-correlation-card">
                            <strong>{correlation.name}</strong>
                            <span>{correlation.source} via {correlation.expression}</span>
                            <div className="import-correlation-flow">
                              <div className="import-correlation-flow-row">
                                <span className="import-correlation-flow-label">Found In</span>
                                <span className="import-correlation-flow-value">{foundAtLabel}</span>
                              </div>
                              {usedAtLocations.length > 0 && (
                                <div className="import-correlation-flow-row">
                                  <span className="import-correlation-flow-label">Used In</span>
                                  <div className="import-correlation-flow-tags">
                                    {usedAtLocations.map(({ location, label }) => (
                                      <span key={`${correlation.name}:${location}:label`} className="import-correlation-flow-chip">{label}</span>
                                    ))}
                                  </div>
                                </div>
                              )}
                            </div>
                            {correlation.sample_value && <code>{correlation.sample_value}</code>}
                            {(foundAtActionIndex !== null || usedAtTargets.length > 0) && (
                              <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                                {foundAtActionIndex !== null && (
                                  <button className="btn btn-secondary btn-sm" type="button" onClick={() => setActiveAction(foundAtActionIndex)}>
                                    Open {correlation.found_at}
                                  </button>
                                )}
                                {usedAtTargets.map(({ location, actionIndex }) => (
                                  <button key={`${correlation.name}:${location}`} className="btn btn-secondary btn-sm" type="button" onClick={() => setActiveAction(actionIndex)}>
                                    Open {location}
                                  </button>
                                ))}
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {importSummary.parameterFileSections.length > 0 && (
                    <div className="import-correlation-section">
                      <h4>Parameter File Mappings</h4>
                      <p>These preserved LoadRunner parameter definitions show how the converter mapped file-backed data into Stormrunner data sources.</p>
                      <div className="import-correlation-list">
                        {importedParameterFileMappings.map(({ section, sourceName }) => (
                          <div key={`${section.file}:${section.name}`} className="import-correlation-card">
                            <strong>{section.file}: {section.name}</strong>
                            <span>{formatImportParameterMapping(section.values)}</span>
                            {sourceName && <span>Generated source: {sourceName}</span>}
                            {sourceName && (
                              <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                                <button className="btn btn-secondary btn-sm" type="button" onClick={() => openImportedDataSourceReview(sourceName)}>
                                  Open Mapped Data Source
                                </button>
                              </div>
                            )}
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  {importedDataSourceSummaries.length > 0 && (
                    <div className="import-correlation-section">
                      <h4>Generated Data Sources</h4>
                      <p>These data sources were created from the imported parameter files and are ready for review in the editor.</p>
                      <div className="import-correlation-list">
                        {importedDataSourceSummaries.map((source) => (
                          <div key={source.name} className="import-correlation-card">
                            <strong>{source.name}</strong>
                            <span>{source.type} • {source.rows} row{source.rows === 1 ? '' : 's'}</span>
                            <span>{typeof source.columns === 'number'
                              ? `${source.columns} column${source.columns === 1 ? '' : 's'}`
                              : source.columns.join(', ') || 'No columns'}</span>
                            <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }}>
                              <button className="btn btn-secondary btn-sm" type="button" onClick={() => openImportedDataSourceReview(source.name)}>
                                Open In Parameter Data
                              </button>
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}

                  <details className="import-metadata-details">
                    <summary>View preserved metadata JSON</summary>
                    <pre>{JSON.stringify(importMetadata, null, 2)}</pre>
                  </details>
                </div>
              )}
            </div>
          )}

          {/* Runtime Settings Panels */}
          <div className={`runtime-settings config-panel ${openSections['runtime'] ? 'open' : ''}`} style={{ padding: 0 }}>
            <button className="panel-header-toggle" onClick={() => toggleSection('runtime')} style={{ padding: '16px 20px', borderBottom: openSections['runtime'] ? '1px solid var(--border)' : 'none' }}>
              <h3 className="panel-title">Runtime Settings</h3>
              <span className="chevron">{openSections['runtime'] ? '▾' : '▸'}</span>
            </button>
            {openSections['runtime'] && (
              <div className="runtime-settings-grid" style={{ padding: 20 }}>
                {/* Think Time */}
                <div className={`settings-section ${openSections['think'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('think')}>
                    <span className="section-icon">{'\u23f1'}</span>
                    <span className="section-label">Think Time</span>
                    <span className="section-summary">
                      {script.config.think_time?.ignore ? 'Disabled' : `${script.config.think_time?.type || 'fixed'} — ${script.config.think_time?.mean_ms || 0}ms`}
                    </span>
                    <span className="chevron">{openSections['think'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['think'] && (
                    <div className="section-body">
                      <div className="form-grid">
                        <label>
                          <span>Distribution</span>
                          <select value={script.config.think_time?.type || 'fixed'}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, type: e.target.value })}>
                            <option value="fixed">Fixed</option>
                            <option value="gaussian">Gaussian (Normal)</option>
                            <option value="uniform">Uniform (Random)</option>
                          </select>
                        </label>
                        <label>
                          <span>Mean (ms)</span>
                          <input type="number" value={script.config.think_time?.mean_ms || 0}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, mean_ms: Number(e.target.value) })} />
                        </label>
                        {script.config.think_time?.type === 'gaussian' && (
                          <label>
                            <span>Std Dev (ms)</span>
                            <input type="number" value={script.config.think_time?.std_ms || 0}
                              onChange={e => updateField('config.think_time', { ...script.config.think_time, std_ms: Number(e.target.value) })} />
                          </label>
                        )}
                        {(script.config.think_time?.type === 'uniform' || script.config.think_time?.type === 'gaussian') && (
                          <>
                            <label>
                              <span>Min (ms)</span>
                              <input type="number" value={script.config.think_time?.min_ms || 0}
                                onChange={e => updateField('config.think_time', { ...script.config.think_time, min_ms: Number(e.target.value) })} />
                            </label>
                            <label>
                              <span>Max (ms)</span>
                              <input type="number" value={script.config.think_time?.max_ms || 0}
                                onChange={e => updateField('config.think_time', { ...script.config.think_time, max_ms: Number(e.target.value) })} />
                            </label>
                          </>
                        )}
                        <label>
                          <span>Multiplier</span>
                          <input type="number" step="0.1" value={script.config.think_time?.multiplier || 1.0}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, multiplier: Number(e.target.value) })} />
                        </label>
                        <label>
                          <span>Limit (ms)</span>
                          <input type="number" value={script.config.think_time?.limit_ms || 0} placeholder="0 = no limit"
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, limit_ms: Number(e.target.value) })} />
                        </label>
                        <label className="checkbox-label">
                          <input type="checkbox" checked={script.config.think_time?.ignore || false}
                            onChange={e => updateField('config.think_time', { ...script.config.think_time, ignore: e.target.checked })} />
                          <span>Ignore all think times</span>
                        </label>
                      </div>
                    </div>
                  )}
                </div>

                {/* Pacing */}
                <div className={`settings-section ${openSections['pacing'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('pacing')}>
                    <span className="section-icon">{'\ud83d\udd04'}</span>
                    <span className="section-label">Iteration Pacing</span>
                    <span className="section-summary">{script.config.pacing?.type || 'none'}</span>
                    <span className="chevron">{openSections['pacing'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['pacing'] && (
                    <div className="section-body">
                      <div className="form-grid">
                        <label className="full-width">
                          <span>Pacing Mode</span>
                          <select value={script.config.pacing?.type || 'none'}
                            onChange={e => updateField('config.pacing', { ...script.config.pacing, type: e.target.value })}>
                            <option value="none">No delay (as fast as possible)</option>
                            <option value="fixed_delay">Fixed delay after iteration</option>
                            <option value="random_delay">Random delay after iteration</option>
                            <option value="fixed_interval">Fixed interval from iteration start</option>
                            <option value="random_interval">Random interval from iteration start</option>
                          </select>
                        </label>
                        {script.config.pacing?.type === 'fixed_delay' && (
                          <label><span>Delay (ms)</span>
                            <input type="number" value={script.config.pacing?.delay_ms || 0}
                              onChange={e => updateField('config.pacing', { ...script.config.pacing, delay_ms: Number(e.target.value) })} />
                          </label>
                        )}
                        {script.config.pacing?.type === 'random_delay' && (
                          <>
                            <label><span>Min Delay (ms)</span>
                              <input type="number" value={script.config.pacing?.min_delay_ms || 0}
                                onChange={e => updateField('config.pacing', { ...script.config.pacing, min_delay_ms: Number(e.target.value) })} />
                            </label>
                            <label><span>Max Delay (ms)</span>
                              <input type="number" value={script.config.pacing?.max_delay_ms || 0}
                                onChange={e => updateField('config.pacing', { ...script.config.pacing, max_delay_ms: Number(e.target.value) })} />
                            </label>
                          </>
                        )}
                        {script.config.pacing?.type === 'fixed_interval' && (
                          <label><span>Interval (ms)</span>
                            <input type="number" value={script.config.pacing?.interval_ms || 0}
                              onChange={e => updateField('config.pacing', { ...script.config.pacing, interval_ms: Number(e.target.value) })} />
                          </label>
                        )}
                        {script.config.pacing?.type === 'random_interval' && (
                          <>
                            <label><span>Min Interval (ms)</span>
                              <input type="number" value={script.config.pacing?.min_interval_ms || 0}
                                onChange={e => updateField('config.pacing', { ...script.config.pacing, min_interval_ms: Number(e.target.value) })} />
                            </label>
                            <label><span>Max Interval (ms)</span>
                              <input type="number" value={script.config.pacing?.max_interval_ms || 0}
                                onChange={e => updateField('config.pacing', { ...script.config.pacing, max_interval_ms: Number(e.target.value) })} />
                            </label>
                          </>
                        )}
                      </div>
                    </div>
                  )}
                </div>

                {/* Run Logic */}
                <div className={`settings-section ${openSections['runlogic'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('runlogic')}>
                    <span className="section-icon">{'\ud83d\udd01'}</span>
                    <span className="section-label">Run Logic</span>
                    <span className="section-summary">
                      {script.config.run_logic?.iterations ? `${script.config.run_logic.iterations} iterations` : 'Run until duration'}
                    </span>
                    <span className="chevron">{openSections['runlogic'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['runlogic'] && (
                    <div className="section-body">
                      <div className="form-grid">
                        <label>
                          <span>Iterations</span>
                          <input type="number" value={script.config.run_logic?.iterations || 0} placeholder="0 = run until duration"
                            onChange={e => updateField('config.run_logic', { ...script.config.run_logic, iterations: Number(e.target.value) })} />
                        </label>
                        <label>
                          <span>Auto-Transactions</span>
                          <select value={script.config.run_logic?.auto_transactions || 'none'}
                            onChange={e => updateField('config.run_logic', { ...script.config.run_logic, auto_transactions: e.target.value })}>
                            <option value="none">None</option>
                            <option value="per_action">Per Action</option>
                            <option value="per_iteration">Per Iteration</option>
                          </select>
                        </label>
                        <label>
                          <span>Block Scheduling</span>
                          <select value={script.config.run_logic?.block_scheduling || 'sequential'}
                            onChange={e => updateField('config.run_logic', { ...script.config.run_logic, block_scheduling: e.target.value })}>
                            <option value="sequential">Sequential</option>
                            <option value="random">Random</option>
                            <option value="percentage">Percentage-based</option>
                          </select>
                        </label>
                        <label className="full-width">
                          <span>Action Sequence</span>
                          <input value={(script.config.run_logic?.action_sequence || []).join(', ')} placeholder="init, action, teardown"
                            onChange={e => updateField('config.run_logic', {
                              ...script.config.run_logic,
                              action_sequence: e.target.value.split(',').map(value => value.trim()).filter(Boolean),
                            })} />
                        </label>
                      </div>
                    </div>
                  )}
                </div>

                {/* Error Handling */}
                <div className={`settings-section ${openSections['errors'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('errors')}>
                    <span className="section-icon">{'\u26a0\ufe0f'}</span>
                    <span className="section-label">Error Handling</span>
                    <span className="section-summary">
                      {script.config.error_handling?.continue_on_error ? 'Continue on error' : 'Stop on error'}
                    </span>
                    <span className="chevron">{openSections['errors'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['errors'] && (
                    <div className="section-body">
                      <div className="form-grid">
                        <label className="checkbox-label">
                          <input type="checkbox" checked={script.config.error_handling?.continue_on_error || false}
                            onChange={e => updateField('config.error_handling', { ...script.config.error_handling, continue_on_error: e.target.checked })} />
                          <span>Continue on error</span>
                        </label>
                        <label className="checkbox-label">
                          <input type="checkbox" checked={script.config.error_handling?.snapshot_on_error || false}
                            onChange={e => updateField('config.error_handling', { ...script.config.error_handling, snapshot_on_error: e.target.checked })} />
                          <span>Capture snapshot on error</span>
                        </label>
                        <label>
                          <span>Max Retries</span>
                          <input type="number" value={script.config.error_handling?.max_retries || 0}
                            onChange={e => updateField('config.error_handling', { ...script.config.error_handling, max_retries: Number(e.target.value) })} />
                        </label>
                        {(script.config.error_handling?.max_retries || 0) > 0 && (
                          <>
                            <label>
                              <span>Retry Delay (ms)</span>
                              <input type="number" value={script.config.error_handling?.retry_delay_ms || 1000}
                                onChange={e => updateField('config.error_handling', { ...script.config.error_handling, retry_delay_ms: Number(e.target.value) })} />
                            </label>
                            <label>
                              <span>Retry Backoff</span>
                              <select value={script.config.error_handling?.retry_backoff || 'fixed'}
                                onChange={e => updateField('config.error_handling', { ...script.config.error_handling, retry_backoff: e.target.value })}>
                                <option value="fixed">Fixed</option>
                                <option value="exponential">Exponential</option>
                              </select>
                            </label>
                          </>
                        )}
                      </div>
                    </div>
                  )}
                </div>

                {isJmsScript && (
                  <div className={`settings-section ${openSections['jms'] ? 'open' : ''}`}>
                    <button className="section-toggle" onClick={() => toggleSection('jms')}>
                      <span className="section-icon">JMS</span>
                      <span className="section-label">Solace JMS</span>
                      <span className="section-summary">
                        {script.config.jms?.mock ? 'Mock broker' : script.config.jms?.broker_url ? 'Broker configured' : 'Not configured'}
                      </span>
                      <span className="chevron">{openSections['jms'] ? '▾' : '▸'}</span>
                    </button>
                    {openSections['jms'] && (
                      <div className="section-body">
                        <div className="settings-sub-group">
                          <h5>Connection</h5>
                          <div className="form-grid">
                            <label className="full-width"><span>Broker URL</span>
                              <input value={script.config.jms?.broker_url || ''} placeholder="tcps://broker.example.com:55443"
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', broker_url: e.target.value })} /></label>
                            <label><span>Client ID</span>
                              <input value={script.config.jms?.client_id || ''} placeholder="stormrunner-{{fn.uuid()}}"
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', client_id: e.target.value })} /></label>
                            <label><span>Message VPN</span>
                              <input value={script.config.jms?.message_vpn || ''} placeholder="default"
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', message_vpn: e.target.value })} /></label>
                            <label><span>Username</span>
                              <input value={script.config.jms?.username || ''}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', username: e.target.value })} /></label>
                            <label><span>Password</span>
                              <input type="password" value={script.config.jms?.password || ''}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', password: e.target.value })} /></label>
                            <label className="checkbox-label">
                              <input type="checkbox" checked={script.config.jms?.mock ?? true}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', mock: e.target.checked })} />
                              <span>Use local mock broker for replay</span>
                            </label>
                          </div>
                        </div>
                        <div className="settings-sub-group">
                          <h5>Defaults</h5>
                          <div className="form-grid">
                            <label><span>Destination</span>
                              <input value={script.config.jms?.destination || ''} placeholder="orders.in"
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', destination: e.target.value })} /></label>
                            <label><span>Destination Type</span>
                              <select value={script.config.jms?.destination_type || 'queue'}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', destination_type: e.target.value })}>
                                <option value="queue">Queue</option>
                                <option value="topic">Topic</option>
                              </select></label>
                            <label><span>Reply To</span>
                              <input value={script.config.jms?.reply_to || ''} placeholder="orders.reply"
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', reply_to: e.target.value })} /></label>
                            <label><span>Timeout (ms)</span>
                              <input type="number" value={script.config.jms?.timeout_ms || 5000}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', timeout_ms: Number(e.target.value) })} /></label>
                            <label><span>Acknowledgement</span>
                              <select value={script.config.jms?.acknowledgement || 'auto'}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', acknowledgement: e.target.value })}>
                                <option value="auto">Auto</option>
                                <option value="client">Client</option>
                                <option value="transacted">Transacted</option>
                              </select></label>
                            <label><span>Delivery Mode</span>
                              <select value={script.config.jms?.delivery_mode || 'persistent'}
                                onChange={e => updateField('config.jms', { ...script.config.jms, provider: 'solace', delivery_mode: e.target.value })}>
                                <option value="persistent">Persistent</option>
                                <option value="non_persistent">Non-persistent</option>
                              </select></label>
                          </div>
                        </div>
                      </div>
                    )}
                  </div>
                )}

                {/* HTTP Protocol */}
                {!isJmsScript && <div className={`settings-section ${openSections['http'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('http')}>
                    <span className="section-icon">{'\ud83c\udf10'}</span>
                    <span className="section-label">HTTP Protocol</span>
                    <span className="section-summary">
                      {script.config.http?.proxy?.url ? 'Proxy configured' : script.config.http?.auth?.type ? `Auth: ${script.config.http.auth.type}` : 'Default'}
                    </span>
                    <span className="chevron">{openSections['http'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['http'] && (
                    <div className="section-body">
                      <div className="settings-sub-group">
                        <h5>Timeouts</h5>
                        <div className="form-grid">
                          <label><span>Connect (ms)</span>
                            <input type="number" value={script.config.http?.connect_timeout_ms || 30000}
                              onChange={e => updateField('config.http', { ...script.config.http, connect_timeout_ms: Number(e.target.value) })} /></label>
                          <label><span>Request (ms)</span>
                            <input type="number" value={script.config.http?.request_timeout_ms || 60000}
                              onChange={e => updateField('config.http', { ...script.config.http, request_timeout_ms: Number(e.target.value) })} /></label>
                          <label><span>TLS Handshake (ms)</span>
                            <input type="number" value={script.config.http?.tls_timeout_ms || 10000}
                              onChange={e => updateField('config.http', { ...script.config.http, tls_timeout_ms: Number(e.target.value) })} /></label>
                          <label><span>Idle Connection (ms)</span>
                            <input type="number" value={script.config.http?.idle_timeout_ms || 90000}
                              onChange={e => updateField('config.http', { ...script.config.http, idle_timeout_ms: Number(e.target.value) })} /></label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Connection</h5>
                        <div className="form-grid">
                          <label><span>HTTP Version</span>
                            <select value={script.config.http?.version || 'auto'}
                              onChange={e => updateField('config.http', { ...script.config.http, version: e.target.value })}>
                              <option value="auto">Auto-negotiate</option>
                              <option value="1.1">HTTP/1.1</option>
                              <option value="2">HTTP/2</option>
                            </select></label>
                          <label><span>Max Conns/Host</span>
                            <input type="number" value={script.config.http?.max_conns_per_host || 100}
                              onChange={e => updateField('config.http', { ...script.config.http, max_conns_per_host: Number(e.target.value) })} /></label>
                          <label><span>Max Redirects</span>
                            <input type="number" value={script.config.http?.max_redirects ?? 10}
                              onChange={e => updateField('config.http', { ...script.config.http, max_redirects: Number(e.target.value) })} /></label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>TLS / SSL</h5>
                        <div className="form-grid">
                          <label><span>Min TLS Version</span>
                            <select value={script.config.http?.tls?.min_version || '1.2'}
                              onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, min_version: e.target.value } })}>
                              <option value="1.0">TLS 1.0</option><option value="1.1">TLS 1.1</option>
                              <option value="1.2">TLS 1.2</option><option value="1.3">TLS 1.3</option>
                            </select></label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.http?.tls?.insecure_skip_verify || false}
                              onChange={e => updateField('config.http', { ...script.config.http, tls: { ...script.config.http?.tls, insecure_skip_verify: e.target.checked } })} />
                            <span>Skip certificate verification</span>
                          </label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Proxy</h5>
                        <div className="form-grid">
                          <label>
                            <span>Proxy Mode</span>
                            <select value={script.config.http?.proxy_mode || 'none'}
                              onChange={e => updateField('config.http', { ...script.config.http, proxy_mode: e.target.value })}>
                              <option value="none">No Proxy</option>
                              <option value="system">Use system proxy</option>
                              <option value="custom">Custom proxy</option>
                            </select>
                          </label>
                          <label className="full-width"><span>Proxy URL</span>
                            <input value={script.config.http?.proxy?.url || ''} placeholder="http://proxy:8080"
                              onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, url: e.target.value } })} /></label>
                          {(script.config.http?.proxy_mode || 'none') === 'custom' && script.config.http?.proxy?.url && (
                            <>
                              <label><span>Proxy Username</span>
                                <input value={script.config.http?.proxy?.username || ''}
                                  onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, username: e.target.value } })} /></label>
                              <label><span>Proxy Password</span>
                                <input type="password" value={script.config.http?.proxy?.password || ''}
                                  onChange={e => updateField('config.http', { ...script.config.http, proxy: { ...script.config.http?.proxy, password: e.target.value } })} /></label>
                            </>
                          )}
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Authentication</h5>
                        <div className="form-grid">
                          <label><span>Auth Type</span>
                            <select value={script.config.http?.auth?.type || ''}
                              onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, type: e.target.value } })}>
                              <option value="">None</option>
                              <option value="basic">Basic</option>
                              <option value="bearer">Bearer Token</option>
                              <option value="ntlm">NTLM</option>
                            </select></label>
                          {(script.config.http?.auth?.type === 'basic' || script.config.http?.auth?.type === 'ntlm') && (
                            <>
                              <label><span>Username</span>
                                <input value={script.config.http?.auth?.username || ''}
                                  onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, username: e.target.value } })} /></label>
                              <label><span>Password</span>
                                <input type="password" value={script.config.http?.auth?.password || ''}
                                  onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, password: e.target.value } })} /></label>
                            </>
                          )}
                          {script.config.http?.auth?.type === 'bearer' && (
                            <label className="full-width"><span>Token</span>
                              <input value={script.config.http?.auth?.token || ''} placeholder="eyJhbGciOiJIUzI1NiIs..."
                                onChange={e => updateField('config.http', { ...script.config.http, auth: { ...script.config.http?.auth, token: e.target.value } })} /></label>
                          )}
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Browser Emulation</h5>
                        <div className="form-grid">
                          <label>
                            <span>Browser Engine</span>
                            <select value={script.config.browser?.engine || 'chromium'}
                              onChange={e => updateField('config.browser', { ...script.config.browser, engine: e.target.value })}>
                              <option value="chromium">Chromium</option>
                              <option value="webkit">WebKit</option>
                            </select>
                          </label>
                          <label>
                            <span>Device Profile</span>
                            <select value={script.config.browser?.device_profile || ''}
                              onChange={e => {
                                const nextDeviceProfile = e.target.value || undefined
                                const nextProfile = lookupBrowserDeviceProfile(nextDeviceProfile)
                                updateField('config.browser', {
                                  ...script.config.browser,
                                  device_profile: nextDeviceProfile,
                                  engine: nextProfile?.engine || script.config.browser?.engine,
                                })
                              }}>
                              <option value="">Custom viewport</option>
                              {BROWSER_DEVICE_PROFILES.map(profile => <option key={profile.name} value={profile.name}>{formatBrowserDeviceProfileLabel(profile)}</option>)}
                            </select>
                          </label>
                          {browserEngineAdvisory && (
                            <div className="browser-config-warning">{browserEngineAdvisory}</div>
                          )}
                          <label>
                            <span>Orientation</span>
                            <select value={script.config.browser?.orientation || ''}
                              onChange={e => updateField('config.browser', { ...script.config.browser, orientation: e.target.value || undefined })}>
                              <option value="">Profile default</option>
                              <option value="portrait">Portrait</option>
                              <option value="landscape">Landscape</option>
                            </select>
                          </label>
                          <label><span>Viewport Width</span>
                            <input type="number" min="0" value={script.config.browser?.viewport?.width ?? ''} placeholder="393"
                              onChange={e => updateField('config.browser', {
                                ...script.config.browser,
                                viewport: {
                                  ...script.config.browser?.viewport,
                                  width: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                                },
                              })} /></label>
                          <label><span>Viewport Height</span>
                            <input type="number" min="0" value={script.config.browser?.viewport?.height ?? ''} placeholder="852"
                              onChange={e => updateField('config.browser', {
                                ...script.config.browser,
                                viewport: {
                                  ...script.config.browser?.viewport,
                                  height: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0),
                                },
                              })} /></label>
                          <label><span>Slow Mo (ms)</span>
                            <input type="number" min="0" value={script.config.browser?.slow_mo_ms ?? ''} placeholder="50"
                              onChange={e => updateField('config.browser', { ...script.config.browser, slow_mo_ms: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value) || 0) })} /></label>
                          <label><span>Capture Screenshots</span>
                            <select value={script.config.browser?.capture_screenshots || 'per_action'}
                              onChange={e => updateField('config.browser', { ...script.config.browser, capture_screenshots: e.target.value })}>
                              <option value="off">Off</option>
                              <option value="on_error">On error</option>
                              <option value="per_action">Per action</option>
                            </select>
                          </label>
                          <label className="full-width"><span>User-Agent</span>
                            <input value={script.config.http?.user_agent || ''} placeholder="Mozilla/5.0 ..."
                              onChange={e => updateField('config.http', { ...script.config.http, user_agent: e.target.value })} /></label>
                          <label><span>Accept-Encoding</span>
                            <input value={script.config.http?.accept_encoding || ''} placeholder="gzip, deflate, br"
                              onChange={e => updateField('config.http', { ...script.config.http, accept_encoding: e.target.value })} /></label>
                          <label><span>Accept-Language</span>
                            <input value={script.config.http?.accept_language || ''} placeholder="en-US,en;q=0.9"
                              onChange={e => updateField('config.http', { ...script.config.http, accept_language: e.target.value })} /></label>
                          <label>
                            <span>Download Resources</span>
                            <select value={script.config.browser?.download_resources || 'recorded'}
                              onChange={e => updateField('config.browser', { ...script.config.browser, download_resources: e.target.value })}>
                              <option value="none">HTML only</option>
                              <option value="recorded">Recorded resources</option>
                              <option value="all">All non-HTML resources</option>
                            </select>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.browser?.headless ?? true}
                              onChange={e => updateField('config.browser', { ...script.config.browser, headless: e.target.checked })} />
                            <span>Run headless</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.browser?.touch_mode ?? false}
                              onChange={e => updateField('config.browser', { ...script.config.browser, touch_mode: e.target.checked })} />
                            <span>Force touch mode</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.browser?.client_metrics ?? false}
                              onChange={e => updateField('config.browser', { ...script.config.browser, client_metrics: e.target.checked })} />
                            <span>Capture client metrics</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.browser?.cache_enabled || false}
                              onChange={e => updateField('config.browser', { ...script.config.browser, cache_enabled: e.target.checked })} />
                            <span>Simulate browser cache</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.browser?.cache_content_urls || false}
                              onChange={e => updateField('config.browser', { ...script.config.browser, cache_content_urls: e.target.checked })} />
                            <span>Cache URLs requiring content</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.browser?.check_newer_versions || false}
                              onChange={e => updateField('config.browser', { ...script.config.browser, check_newer_versions: e.target.checked })} />
                            <span>Check for newer cached pages</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.browser?.new_user_per_iteration || false}
                              onChange={e => updateField('config.browser', { ...script.config.browser, new_user_per_iteration: e.target.checked })} />
                            <span>Simulate a new user each iteration</span>
                          </label>
                        </div>
                      </div>
                    </div>
                  )}
                </div>}

                {isSoapScript && <div className={`settings-section ${openSections['soap'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('soap')}>
                    <span className="section-icon">SOAP</span>
                    <span className="section-label">SOAP Settings</span>
                    <span className="section-summary">
                      {script.config.soap?.ws_security?.username_token?.username
                        ? 'WS-Security configured'
                        : script.config.soap?.version || '1.1'}
                    </span>
                    <span className="chevron">{openSections['soap'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['soap'] && (
                    <div className="section-body">
                      <div className="settings-sub-group">
                        <h5>Envelope</h5>
                        <div className="form-grid">
                          <label>
                            <span>SOAP Version</span>
                            <select value={script.config.soap?.version || '1.1'} onChange={e => updateField('config.soap', {
                              ...(script.config.soap || {}),
                              version: e.target.value,
                              namespaces: syncSOAPEnvelopeNamespace(
                                e.target.value,
                                script.config.soap?.namespaces,
                                script.config.soap?.version,
                              ),
                            })}>
                              <option value="1.1">SOAP 1.1</option>
                              <option value="1.2">SOAP 1.2</option>
                            </select>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.soap?.fault_handling?.fail_on_fault ?? true} onChange={e => updateField('config.soap.fault_handling.fail_on_fault', e.target.checked)} />
                            <span>Fail on SOAP Fault</span>
                          </label>
                        </div>
                      </div>

                      <div className="settings-sub-group">
                        <div className="sub-header"><h4>Namespaces</h4><button className="btn btn-sm btn-ghost" onClick={addSoapNamespace}>+ Add</button></div>
                        {Object.entries(script.config.soap?.namespaces || {}).map(([key, value]) => (
                          <div key={key} className="kv-row">
                            <input className="kv-key" value={key} onChange={e => updateSoapNamespace(key, e.target.value, value)} placeholder="Prefix" />
                            <input className="kv-val" value={value} onChange={e => updateSoapNamespace(key, key, e.target.value)} placeholder="Namespace URI" />
                            <button className="btn-icon danger" onClick={() => removeSoapNamespace(key)}>✕</button>
                          </div>
                        ))}
                      </div>

                      <div className="settings-sub-group">
                        <h5>WS-Security UsernameToken</h5>
                        <div className="form-grid">
                          <label>
                            <span>Username</span>
                            <input value={script.config.soap?.ws_security?.username_token?.username || ''} onChange={e => updateField('config.soap.ws_security.username_token.username', e.target.value)} placeholder="service-user" />
                          </label>
                          <label>
                            <span>Password</span>
                            <input type="password" value={script.config.soap?.ws_security?.username_token?.password || ''} onChange={e => updateField('config.soap.ws_security.username_token.password', e.target.value)} placeholder="secret" />
                          </label>
                          <label>
                            <span>Password Type</span>
                            <select value={script.config.soap?.ws_security?.username_token?.password_type || 'text'} onChange={e => updateField('config.soap.ws_security.username_token.password_type', e.target.value)}>
                              <option value="text">text</option>
                              <option value="digest">digest</option>
                            </select>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.soap?.ws_security?.username_token?.nonce || false} onChange={e => updateField('config.soap.ws_security.username_token.nonce', e.target.checked)} />
                            <span>Include nonce</span>
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.soap?.ws_security?.username_token?.created || false} onChange={e => updateField('config.soap.ws_security.username_token.created', e.target.checked)} />
                            <span>Include created timestamp</span>
                          </label>
                        </div>
                      </div>

                      <div className="settings-sub-group">
                        <h5>WS-Security Timestamp</h5>
                        <div className="form-grid">
                          <label>
                            <span>TTL (seconds)</span>
                            <input type="number" min="0" value={script.config.soap?.ws_security?.timestamp?.ttl_seconds ?? 0} onChange={e => updateField('config.soap.ws_security.timestamp.ttl_seconds', Math.max(0, Number(e.target.value) || 0))} />
                          </label>
                        </div>
                      </div>
                    </div>
                  )}
                </div>}

                {isJavaHttpScript && <div className={`settings-section ${openSections['java_http'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('java_http')}>
                    <span className="section-icon">JAVA</span>
                    <span className="section-label">Java HTTP Settings</span>
                    <span className="section-summary">{`${script.config.java_http?.codec || 'java_serialized_object_base64'} · ${script.config.java_http?.helper_mode || 'embedded'}`}</span>
                    <span className="chevron">{openSections['java_http'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['java_http'] && (
                    <div className="section-body">
                      <div className="settings-sub-group">
                        <h5>Codec</h5>
                        <div className="form-grid">
                          <label>
                            <span>Wire Format</span>
                            <select value={script.config.java_http?.codec || 'java_serialized_object_base64'} onChange={e => updateField('config.java_http.codec', e.target.value)}>
                              <option value="java_serialized_object_base64">java_serialized_object_base64</option>
                            </select>
                          </label>
                          <label>
                            <span>Helper Mode</span>
                            <select value={script.config.java_http?.helper_mode || 'embedded'} onChange={e => updateField('config.java_http.helper_mode', e.target.value)}>
                              <option value="embedded">embedded</option>
                              <option value="sidecar">sidecar</option>
                            </select>
                          </label>
                          <label>
                            <span>Body Encoding</span>
                            <select value={script.config.java_http?.body_encoding || 'base64'} onChange={e => updateField('config.java_http.body_encoding', e.target.value)}>
                              <option value="base64">base64</option>
                            </select>
                          </label>
                        </div>
                      </div>

                      <div className="settings-sub-group">
                        <h5>Transport Defaults</h5>
                        <div className="form-grid">
                          <label>
                            <span>Content-Type</span>
                            <input value={script.config.java_http?.content_type || ''} onChange={e => updateField('config.java_http.content_type', e.target.value)} placeholder="application/x-java-serialized-object" />
                          </label>
                          <label>
                            <span>Accept</span>
                            <input value={script.config.java_http?.accept || ''} onChange={e => updateField('config.java_http.accept', e.target.value)} placeholder="application/x-java-serialized-object" />
                          </label>
                          <label>
                            <span>Object Schema</span>
                            <input value={script.config.java_http?.object_schema || ''} onChange={e => updateField('config.java_http.object_schema', e.target.value)} placeholder="com.example.AuthRequest" />
                          </label>
                          <label>
                            <span>Extractor Type</span>
                            <input value={script.config.java_http?.extractor_type || ''} onChange={e => updateField('config.java_http.extractor_type', e.target.value)} placeholder="opaque" />
                          </label>
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.java_http?.preserve_binary_result ?? true} onChange={e => updateField('config.java_http.preserve_binary_result', e.target.checked)} />
                            <span>Preserve binary responses as base64 text</span>
                          </label>
                        </div>
                      </div>

                      <div className="settings-sub-group">
                        <div className="sub-header"><h4>Request Mapping</h4><button className="btn btn-sm btn-ghost" onClick={() => addJavaHTTPMapping('request_mapping')}>+ Add</button></div>
                        <div className="cell-muted" style={{ marginBottom: 8 }}>Mappings are stored with the draft for future helper-aware serializers. Current Java-over-HTTP execution still sends opaque base64 payloads.</div>
                        {Object.entries(script.config.java_http?.request_mapping || {}).length === 0 && <div className="cell-muted">No request mapping entries defined.</div>}
                        {Object.entries(script.config.java_http?.request_mapping || {}).map(([key, value]) => (
                          <div key={`request-${key}`} className="pair-row">
                            <input value={key} onChange={e => updateJavaHTTPMapping('request_mapping', key, e.target.value, value)} placeholder="logical field" />
                            <input value={value} onChange={e => updateJavaHTTPMapping('request_mapping', key, key, e.target.value)} placeholder="serialized property path" />
                            <button className="btn btn-sm btn-ghost" onClick={() => removeJavaHTTPMapping('request_mapping', key)}>✕</button>
                          </div>
                        ))}
                      </div>

                      <div className="settings-sub-group">
                        <div className="sub-header"><h4>Response Mapping</h4><button className="btn btn-sm btn-ghost" onClick={() => addJavaHTTPMapping('response_mapping')}>+ Add</button></div>
                        {Object.entries(script.config.java_http?.response_mapping || {}).length === 0 && <div className="cell-muted">No response mapping entries defined.</div>}
                        {Object.entries(script.config.java_http?.response_mapping || {}).map(([key, value]) => (
                          <div key={`response-${key}`} className="pair-row">
                            <input value={key} onChange={e => updateJavaHTTPMapping('response_mapping', key, e.target.value, value)} placeholder="serialized property path" />
                            <input value={value} onChange={e => updateJavaHTTPMapping('response_mapping', key, key, e.target.value)} placeholder="replay variable or logical field" />
                            <button className="btn btn-sm btn-ghost" onClick={() => removeJavaHTTPMapping('response_mapping', key)}>✕</button>
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </div>}

                {isHttpScript && <div className={`settings-section ${openSections['web_runtime'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('web_runtime')}>
                    <span className="section-icon">RTS</span>
                    <span className="section-label">Web HTTP/HTML Compatibility</span>
                    <span className="section-summary">
                      {script.config.web_runtime?.content_checks?.length
                        ? `${script.config.web_runtime.content_checks.length} content checks`
                        : script.config.web_runtime?.network_simulation?.mode || 'Compatibility settings'}
                    </span>
                    <span className="chevron">{openSections['web_runtime'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['web_runtime'] && (
                    <div className="section-body">
                      <div className="cell-muted" style={{ marginBottom: 12 }}>
                        These settings are captured in the draft now so Web HTTP/HTML scripts can mirror VuGen RTS coverage even where full Stormrunner execution support is still rolling out.
                      </div>
                      <div className="settings-sub-group">
                        <h5>Additional Attributes</h5>
                        <label className="full-width">
                          <span>Name=Value pairs</span>
                          <textarea rows={4} placeholder={'env=perf\nregion=east'} value={formatKeyValueLines(script.config.web_runtime?.additional_attributes)}
                            onChange={e => updateField('config.web_runtime', {
                              ...script.config.web_runtime,
                              additional_attributes: parseKeyValueLines(e.target.value),
                            })} />
                        </label>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Download Filters</h5>
                        <div className="form-grid">
                          <label>
                            <span>Mode</span>
                            <select value={script.config.web_runtime?.download_filters?.mode || 'none'}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                download_filters: {
                                  ...script.config.web_runtime?.download_filters,
                                  mode: e.target.value,
                                },
                              })}>
                              <option value="none">No filters</option>
                              <option value="allow_list">Allow list</option>
                              <option value="block_list">Block list</option>
                            </select>
                          </label>
                          <label className="full-width">
                            <span>Hosts</span>
                            <textarea rows={4} placeholder={'cdn.example.test\nmetrics.example.test'} value={formatLineList(script.config.web_runtime?.download_filters?.hosts)}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                download_filters: {
                                  ...script.config.web_runtime?.download_filters,
                                  hosts: parseLineList(e.target.value),
                                },
                              })} />
                          </label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Content Check</h5>
                        <label className="full-width">
                          <span>One per line: text|scope|fail_if_found</span>
                          <textarea rows={4} placeholder={'Access Denied|any_response|true'} value={formatContentChecks(script.config.web_runtime?.content_checks)}
                            onChange={e => updateField('config.web_runtime', {
                              ...script.config.web_runtime,
                              content_checks: parseContentChecks(e.target.value),
                            })} />
                        </label>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Network Emulation</h5>
                        <div className="form-grid">
                          <label>
                            <span>Mode</span>
                            <select value={script.config.web_runtime?.network_simulation?.mode || 'max'}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  mode: e.target.value,
                                },
                              })}>
                              <option value="max">Maximum throughput</option>
                              <option value="preset">Preset profile</option>
                              <option value="custom">Custom profile</option>
                            </select>
                          </label>
                          <label>
                            <span>Preset</span>
                            <select value={script.config.web_runtime?.network_simulation?.preset || 'dsl'}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  preset: e.target.value,
                                },
                              })}>
                              <option value="56k">56k</option>
                              <option value="dsl">DSL</option>
                              <option value="t1">T1</option>
                              <option value="10mbps">10 Mbps</option>
                            </select>
                          </label>
                          <label>
                            <span>Bandwidth (Kbps)</span>
                            <input type="number" value={script.config.web_runtime?.network_simulation?.bandwidth_kbps || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  bandwidth_kbps: Number(e.target.value),
                                },
                              })} />
                          </label>
                          <label>
                            <span>Latency (ms)</span>
                            <input type="number" value={script.config.web_runtime?.network_simulation?.latency_ms || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  latency_ms: Number(e.target.value),
                                },
                              })} />
                          </label>
                          <label>
                            <span>Jitter (ms)</span>
                            <input type="number" value={script.config.web_runtime?.network_simulation?.jitter_ms || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  jitter_ms: Number(e.target.value),
                                },
                              })} />
                          </label>
                          <label>
                            <span>Packet Loss (%)</span>
                            <input type="number" min="0" max="100" step="0.1" value={script.config.web_runtime?.network_simulation?.packet_loss_pct || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                network_simulation: {
                                  ...script.config.web_runtime?.network_simulation,
                                  packet_loss_pct: Number(e.target.value),
                                },
                              })} />
                          </label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Streaming</h5>
                        <div className="cell-muted" style={{ marginBottom: 12 }}>
                          Streaming settings are compatibility-only today. Stormrunner preserves and imports these values, but does not execute media playback buffering or adaptive bitrate behavior yet.
                        </div>
                        <div className="form-grid">
                          <label>
                            <span>Mode</span>
                            <select value={script.config.web_runtime?.streaming?.mode || 'adaptive'}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                streaming: {
                                  ...script.config.web_runtime?.streaming,
                                  mode: e.target.value,
                                },
                              })}>
                              <option value="adaptive">Adaptive bitrate</option>
                              <option value="fixed">Fixed bitrate</option>
                            </select>
                          </label>
                          <label>
                            <span>Buffer Size (seconds)</span>
                            <input type="number" value={script.config.web_runtime?.streaming?.buffer_size_seconds || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                streaming: {
                                  ...script.config.web_runtime?.streaming,
                                  buffer_size_seconds: Number(e.target.value),
                                },
                              })} />
                          </label>
                          <label>
                            <span>Fixed Bitrate (Kbps)</span>
                            <input type="number" value={script.config.web_runtime?.streaming?.fixed_bitrate_kbps || 0}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                streaming: {
                                  ...script.config.web_runtime?.streaming,
                                  fixed_bitrate_kbps: Number(e.target.value),
                                },
                              })} />
                          </label>
                          <label>
                            <span>Bandwidth Estimation</span>
                            <select value={script.config.web_runtime?.streaming?.bandwidth_estimation || 'rolling'}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                streaming: {
                                  ...script.config.web_runtime?.streaming,
                                  bandwidth_estimation: e.target.value,
                                },
                              })}>
                              <option value="rolling">Rolling</option>
                              <option value="instant">Instant</option>
                            </select>
                          </label>
                        </div>
                      </div>
                      <div className="settings-sub-group">
                        <h5>Data Formatting</h5>
                        <div className="form-grid">
                          <label className="checkbox-label">
                            <input type="checkbox" checked={script.config.web_runtime?.data_formatting?.auto_detect || false}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                data_formatting: {
                                  ...script.config.web_runtime?.data_formatting,
                                  auto_detect: e.target.checked,
                                },
                              })} />
                            <span>Auto-detect data format extensions</span>
                          </label>
                          <label className="full-width">
                            <span>DFE Chain</span>
                            <textarea rows={4} placeholder={'base64\ngzip mode=decode\njson_to_xml'} value={formatDFEChain(script.config.web_runtime?.data_formatting?.chain)}
                              onChange={e => updateField('config.web_runtime', {
                                ...script.config.web_runtime,
                                data_formatting: {
                                  ...script.config.web_runtime?.data_formatting,
                                  chain: parseDFEChain(e.target.value),
                                },
                              })} />
                          </label>
                        </div>
                      </div>
                    </div>
                  )}
                </div>}

                {/* Logging */}
                <div className={`settings-section ${openSections['log'] ? 'open' : ''}`}>
                  <button className="section-toggle" onClick={() => toggleSection('log')}>
                    <span className="section-icon">{'\ud83d\udccb'}</span>
                    <span className="section-label">Logging</span>
                    <span className="section-summary">{script.config.log?.level || 'standard'}</span>
                    <span className="chevron">{openSections['log'] ? '▾' : '▸'}</span>
                  </button>
                  {openSections['log'] && (
                    <div className="section-body">
                      <div className="form-grid">
                        <label>
                          <span>Log Level</span>
                          <select value={script.config.log?.level || 'standard'}
                            onChange={e => updateField('config.log', { ...script.config.log, level: e.target.value })}>
                            <option value="disabled">Disabled</option>
                            <option value="standard">Standard</option>
                            <option value="extended">Extended (verbose)</option>
                          </select>
                        </label>
                        <label className="checkbox-label">
                          <input type="checkbox" checked={script.config.log?.on_error_only || false}
                            onChange={e => updateField('config.log', { ...script.config.log, on_error_only: e.target.checked })} />
                          <span>Extended log on error only</span>
                        </label>
                        <label className="checkbox-label">
                          <input type="checkbox" checked={script.config.log?.extended_options?.includes('parameter_substitution') || false}
                            onChange={e => updateField('config.log', {
                              ...script.config.log,
                              extended_options: e.target.checked
                                ? [...new Set([...(script.config.log?.extended_options || []), 'parameter_substitution'])]
                                : (script.config.log?.extended_options || []).filter(option => option !== 'parameter_substitution'),
                            })} />
                          <span>Parameter substitution</span>
                        </label>
                        <label className="checkbox-label">
                          <input type="checkbox" checked={script.config.log?.extended_options?.includes('data_returned') || false}
                            onChange={e => updateField('config.log', {
                              ...script.config.log,
                              extended_options: e.target.checked
                                ? [...new Set([...(script.config.log?.extended_options || []), 'data_returned'])]
                                : (script.config.log?.extended_options || []).filter(option => option !== 'data_returned'),
                            })} />
                          <span>Data returned by server</span>
                        </label>
                        <label className="checkbox-label">
                          <input type="checkbox" checked={script.config.log?.extended_options?.includes('advanced_trace') || false}
                            onChange={e => updateField('config.log', {
                              ...script.config.log,
                              extended_options: e.target.checked
                                ? [...new Set([...(script.config.log?.extended_options || []), 'advanced_trace'])]
                                : (script.config.log?.extended_options || []).filter(option => option !== 'advanced_trace'),
                            })} />
                          <span>Advanced trace</span>
                        </label>
                      </div>
                    </div>
                  )}
                </div>
              </div>
            )}
          </div>

          {/* Actions List + Detail */}
          <div className="actions-layout">
            <div className="actions-content-grid">
              <div className="actions-sidebar">
                <div className="actions-sidebar-header">
                  <h3 className="panel-title" style={{ fontSize: 11 }}>Action List</h3>
                  <div className="actions-sidebar-controls">
                    <button className="btn btn-sm btn-ghost" onClick={() => setIsParameterDialogOpen(true)}>Parameter Data</button>
                    <button className="btn btn-sm btn-accent" onClick={addAction}>+ Add</button>
                  </div>
                </div>
                <div className="action-list">
                  {script.actions.map((a, i) => (
                    <div key={i} className={`action-item ${activeAction === i ? 'active' : ''}`} onClick={() => setActiveAction(i)}>
                      <span className="action-method" style={{ color: methodColor(a.type) }}>{a.type}</span>
                      <span className="action-name-text">{a.name || 'Unnamed'}</span>
                      <div className="action-item-actions">
                        <button className="btn-icon" onClick={e => { e.stopPropagation(); moveAction(i, -1) }} disabled={i === 0}>↑</button>
                        <button className="btn-icon" onClick={e => { e.stopPropagation(); moveAction(i, 1) }} disabled={i === script.actions.length - 1}>↓</button>
                        <button className="btn-icon danger" onClick={e => { e.stopPropagation(); removeAction(i) }}>✕</button>
                      </div>
                    </div>
                  ))}
                  {script.actions.length === 0 && (
                    <div className="action-empty">No actions yet. Click "+ Add" to create one.</div>
                  )}
                </div>
              </div>

              {/* Action Detail */}
              <div className="action-detail">
                {activeAction !== null && script.actions[activeAction] ? (
                  <ActionEditor
                    action={script.actions[activeAction]}
                    index={activeAction}
                    parameters={script.parameters || []}
                    onUpdate={updateAction}
                    onAddExtractor={addExtractor}
                    onUpdateExtractor={updateExtractor}
                    onRemoveExtractor={removeExtractor}
                    onAddAssertion={addAssertion}
                    onUpdateAssertion={updateAssertion}
                    onRemoveAssertion={removeAssertion}
                    onAddHeader={addHeader}
                    onUpdateHeader={updateHeader}
                    onRemoveHeader={removeHeader}
                    onAddMetadata={addMetadata}
                    onUpdateMetadata={updateMetadata}
                    onRemoveMetadata={removeMetadata}
                    onBeginParameterEdit={beginParameterEditFromActionEditor}
                    methodColor={methodColor}
                    isBrowserScript={isBrowserScript}
                    isJmsScript={isJmsScript}
                    isSoapScript={isSoapScript}
                  />
                ) : (
                  <div className="action-placeholder">
                    <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>
                    <p>Select an action to edit its details</p>
                  </div>
                )}
              </div>
            </div>
          </div>

          {isParameterDialogOpen && (
            <ParameterDataDialog
              dataEditorMode={dataEditorMode}
              dataSourceHistory={dataSourceHistory}
              onAddDataColumn={addDataColumn}
              onAddDataRow={addDataRow}
              onAddDataSource={addDataSource}
              onAddParameter={addParameter}
              onApplyDedupeHeaders={applyDedupeHeaders}
              onApplyFillHeaders={applyFillHeaders}
              onApplyNormalizeRows={applyNormalizeRows}
              onApplyPendingParameterDraft={applyPendingParameterDraft}
              onAutoFixDataSource={autoFixDataSource}
              onClearPendingParameterDraft={clearPendingParameterDraft}
              onClose={closeParameterDialog}
              onExportDataSourceFile={exportDataSourceFile}
              onImportDataSourceFile={importDataSourceFile}
              onRedoDataSource={redoDataSource}
              onRemoveDataColumn={removeDataColumn}
              onRemoveDataRow={removeDataRow}
              onRemoveDataSource={removeDataSource}
              onRemoveParameter={removeParameter}
              onToggleDataEditorMode={toggleDataEditorModeAt}
              onToggleOpenDataEditor={toggleOpenDataEditor}
              onUndoDataSource={undoDataSource}
              onUpdateDataCell={updateDataCell}
              onUpdateDataColumnName={updateDataColumnName}
              onUpdateDataSource={updateDataSource}
              onUpdateParameter={updateParameter}
              onUpdateParameterDraftName={updatePendingParameterDraftName}
              onUpdateParameterDraftValue={updatePendingParameterDraftValue}
              onUpdateParameterType={updateParameterType}
              openDataEditor={openDataEditor}
              pendingParameterDraft={pendingParameterDraft}
              script={script}
            />
          )}

          {replayScreenshotSelection && (
            <ReplayScreenshotDialog
              alt={replayScreenshotSelection.alt}
              onClose={closeReplayScreenshot}
              src={replayScreenshotSelection.src}
            />
          )}
        </div>
      )}
    </div>
  )
}

function formatRuntimeSettingStatusLabel(status: string) {
  switch (status) {
    case 'executed':
      return 'Executed'
    case 'partial':
      return 'Partial'
    case 'compatibility_only':
      return 'Compatibility-only'
    case 'blocked':
      return 'Blocked'
    case 'non_applicable':
      return 'Non-applicable'
    default:
      return status.replace(/_/g, ' ')
  }
}

function formatImportParameterMapping(values: Record<string, string>) {
  const details: string[] = []
  if (values.Type) details.push(values.Type)
  if (values.ColumnName) details.push(`column ${values.ColumnName}`)
  if (values.Table) details.push(`table ${values.Table}`)
  if (values.SelectNextRow) details.push(values.SelectNextRow)
  if (values.UpdateValueOn) details.push(values.UpdateValueOn)
  return details.join(' • ') || 'No preserved mapping details'
}

interface ReplayScreenshotDialogProps {
  alt: string
  onClose: () => void
  src: string
}

function ReplayScreenshotDialog({ alt, onClose, src }: ReplayScreenshotDialogProps) {
  return (
    <div className="editor-dialog-backdrop" onClick={onClose}>
      <div
        className="editor-dialog replay-screenshot-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Replay Screenshot"
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Replay Screenshot</h3>
            <p className="editor-dialog-subtitle">Inspect the captured browser replay screenshot at full size.</p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body replay-screenshot-dialog-body">
          <img className="replay-screenshot-dialog-image" src={src} alt={alt} />
        </div>
      </div>
    </div>
  )
}

interface ParameterDataDialogProps {
  dataEditorMode: Record<number, 'grid' | 'raw'>
  dataSourceHistory: Record<number, DataSourceHistory>
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onAddDataSource: () => void
  onAddParameter: () => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onApplyPendingParameterDraft: () => void
  onAutoFixDataSource: (idx: number) => void
  onClearPendingParameterDraft: () => void
  onClose: () => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onRemoveDataSource: (idx: number) => void
  onRemoveParameter: (idx: number) => void
  onToggleDataEditorMode: (idx: number) => void
  onToggleOpenDataEditor: (idx: number) => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  onUpdateParameter: (idx: number, field: keyof ParameterConfig, value: unknown) => void
  onUpdateParameterDraftName: (value: string) => void
  onUpdateParameterDraftValue: (value: string) => void
  onUpdateParameterType: (idx: number, nextType: string) => void
  openDataEditor: number | null
  pendingParameterDraft: ActionEditorParameterDraft | null
  script: ScriptData
}

function ParameterDataDialog({
  dataEditorMode,
  dataSourceHistory,
  onAddDataColumn,
  onAddDataRow,
  onAddDataSource,
  onAddParameter,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onApplyPendingParameterDraft,
  onAutoFixDataSource,
  onClearPendingParameterDraft,
  onClose,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onRemoveDataSource,
  onRemoveParameter,
  onToggleDataEditorMode,
  onToggleOpenDataEditor,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  onUpdateParameter,
  onUpdateParameterDraftName,
  onUpdateParameterDraftValue,
  onUpdateParameterType,
  openDataEditor,
  pendingParameterDraft,
  script,
}: ParameterDataDialogProps) {
  return (
    <div className="editor-dialog-backdrop" onClick={onClose}>
      <div
        className="editor-dialog parameter-data-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Parameter Data"
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Parameter Data ({script.data_sources?.length || 0})</h3>
            <p className="editor-dialog-subtitle">Manage named parameters and reusable data sources without leaving the action workspace.</p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body">
          {pendingParameterDraft && (
            <ParameterDraftPanel
              draft={pendingParameterDraft}
              onApplyPendingParameterDraft={onApplyPendingParameterDraft}
              onClearPendingParameterDraft={onClearPendingParameterDraft}
              onUpdateParameterDraftName={onUpdateParameterDraftName}
              onUpdateParameterDraftValue={onUpdateParameterDraftValue}
            />
          )}
          <ParameterDefinitionsSection
            onAddParameter={onAddParameter}
            onRemoveParameter={onRemoveParameter}
            onUpdateParameter={onUpdateParameter}
            onUpdateParameterType={onUpdateParameterType}
            script={script}
          />

          <ParameterDataSourcesSection
            dataEditorMode={dataEditorMode}
            dataSourceHistory={dataSourceHistory}
            onAddDataColumn={onAddDataColumn}
            onAddDataRow={onAddDataRow}
            onAddDataSource={onAddDataSource}
            onApplyDedupeHeaders={onApplyDedupeHeaders}
            onApplyFillHeaders={onApplyFillHeaders}
            onApplyNormalizeRows={onApplyNormalizeRows}
            onAutoFixDataSource={onAutoFixDataSource}
            onExportDataSourceFile={onExportDataSourceFile}
            onImportDataSourceFile={onImportDataSourceFile}
            onRedoDataSource={onRedoDataSource}
            onRemoveDataColumn={onRemoveDataColumn}
            onRemoveDataRow={onRemoveDataRow}
            onRemoveDataSource={onRemoveDataSource}
            onToggleDataEditorMode={onToggleDataEditorMode}
            onToggleOpenDataEditor={onToggleOpenDataEditor}
            onUndoDataSource={onUndoDataSource}
            onUpdateDataCell={onUpdateDataCell}
            onUpdateDataColumnName={onUpdateDataColumnName}
            onUpdateDataSource={onUpdateDataSource}
            openDataEditor={openDataEditor}
            script={script}
          />
        </div>
      </div>
    </div>
  )
}

interface ParameterDraftPanelProps {
  draft: ActionEditorParameterDraft
  onApplyPendingParameterDraft: () => void
  onClearPendingParameterDraft: () => void
  onUpdateParameterDraftName: (value: string) => void
  onUpdateParameterDraftValue: (value: string) => void
}

function ParameterDraftPanel({
  draft,
  onApplyPendingParameterDraft,
  onClearPendingParameterDraft,
  onUpdateParameterDraftName,
  onUpdateParameterDraftValue,
}: ParameterDraftPanelProps) {
  return (
    <div className="parameter-draft-panel">
      <div className="panel-title-row" style={{ marginBottom: 8 }}>
        <div>
          <strong style={{ display: 'block', marginBottom: 4 }}>Parameterize Selection</strong>
          <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>This selection came from the Action List editor. Review it here with the rest of the parameter configuration.</span>
        </div>
      </div>
      <div className="parameter-draft-grid">
        <label>
          <span>Name</span>
          <input
            value={draft.requestedName}
            onChange={event => onUpdateParameterDraftName(event.target.value)}
            placeholder="param_name"
          />
        </label>
        <label>
          <span>Value</span>
          <input
            value={draft.seedValue}
            onChange={event => onUpdateParameterDraftValue(event.target.value)}
            placeholder="Selected value"
          />
        </label>
      </div>
      <div className="parameter-draft-actions">
        <button type="button" className="btn btn-sm btn-primary" onClick={onApplyPendingParameterDraft} disabled={!draft.requestedName.trim()}>
          Save To Parameter Data
        </button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={onClearPendingParameterDraft}>
          Cancel
        </button>
      </div>
    </div>
  )
}

interface ParameterDefinitionsSectionProps {
  onAddParameter: () => void
  onRemoveParameter: (idx: number) => void
  onUpdateParameter: (idx: number, field: keyof ParameterConfig, value: unknown) => void
  onUpdateParameterType: (idx: number, nextType: string) => void
  script: ScriptData
}

function ParameterDefinitionsSection({
  onAddParameter,
  onRemoveParameter,
  onUpdateParameter,
  onUpdateParameterType,
  script,
}: ParameterDefinitionsSectionProps) {
  return (
    <>
      <div className="panel-title-row" style={{ marginBottom: 12 }}>
        <div>
          <strong style={{ display: 'block', marginBottom: 4 }}>Named Parameters</strong>
          <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>Define named parameters with VuGen-style update cadence and generated values.</span>
        </div>
        <button className="btn btn-sm btn-accent" onClick={onAddParameter}>+ Add Parameter</button>
      </div>

      {(script.parameters?.length || 0) === 0 ? (
        <div className="parameter-empty-state" style={{ marginBottom: 20 }}>No named parameters defined. Add one for random, date/time, counter, file, or list-driven values.</div>
      ) : (
        <div className="parameter-table-wrap" style={{ marginBottom: 20 }}>
          <table className="parameter-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Update</th>
                <th>Source / Selection</th>
                <th>Settings</th>
                <th>Template</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {(script.parameters || []).map((parameter, idx) => (
                <tr key={`${parameter.name || 'parameter'}-${idx}`}>
                  <td>
                    <input
                      value={parameter.name || ''}
                      onChange={e => onUpdateParameter(idx, 'name', e.target.value)}
                      placeholder="session_id"
                    />
                  </td>
                  <td>
                    <select value={parameter.type || 'list'} onChange={e => onUpdateParameterType(idx, e.target.value)}>
                      <option value="list">list</option>
                      <option value="file">file</option>
                      <option value="random_number">random_number</option>
                      <option value="date_time">date_time</option>
                      <option value="timestamp">timestamp</option>
                      <option value="counter">counter</option>
                      <option value="random_string">random_string</option>
                      <option value="uuid">uuid</option>
                    </select>
                  </td>
                  <td>
                    <select value={parameter.update || 'each_iteration'} onChange={e => onUpdateParameter(idx, 'update', e.target.value)}>
                      <option value="once">once</option>
                      <option value="each_iteration">each_iteration</option>
                      <option value="each_occurrence">each_occurrence</option>
                    </select>
                  </td>
                  <td>
                    <div style={{ display: 'grid', gap: 8 }}>
                      {(parameter.type === 'file' || parameter.type === 'list') && (
                        <>
                          <select value={parameter.selection || 'sequential'} onChange={e => onUpdateParameter(idx, 'selection', e.target.value)}>
                            <option value="sequential">sequential</option>
                            <option value="random">random</option>
                            <option value="unique">unique</option>
                          </select>
                          <select value={parameter.on_exhaustion || 'wrap'} onChange={e => onUpdateParameter(idx, 'on_exhaustion', e.target.value)}>
                            <option value="wrap">wrap</option>
                            <option value="empty">empty</option>
                            <option value="error">error</option>
                          </select>
                          <input
                            value={parameter.data_source || ''}
                            onChange={e => onUpdateParameter(idx, 'data_source', e.target.value)}
                            placeholder="data source name"
                          />
                          <input
                            value={parameter.column || ''}
                            onChange={e => onUpdateParameter(idx, 'column', e.target.value)}
                            placeholder="column name"
                          />
                          {parameter.type === 'list' && (
                            <input
                              value={(parameter.values || []).join(', ')}
                              onChange={e => onUpdateParameter(idx, 'values', e.target.value.split(',').map(v => v.trim()).filter(Boolean))}
                              placeholder="inline values (optional)"
                            />
                          )}
                        </>
                      )}
                      {parameter.type === 'random_number' && (
                        <div style={{ display: 'grid', gap: 8 }}>
                          <input type="number" value={parameter.min ?? 0} onChange={e => onUpdateParameter(idx, 'min', Number(e.target.value))} placeholder="min" />
                          <input type="number" value={parameter.max ?? 100} onChange={e => onUpdateParameter(idx, 'max', Number(e.target.value))} placeholder="max" />
                        </div>
                      )}
                      {parameter.type === 'counter' && (
                        <div style={{ display: 'grid', gap: 8 }}>
                          <input type="number" value={parameter.start ?? 0} onChange={e => onUpdateParameter(idx, 'start', Number(e.target.value))} placeholder="start" />
                          <input type="number" value={parameter.end ?? 0} onChange={e => onUpdateParameter(idx, 'end', Number(e.target.value))} placeholder="end (optional)" />
                          <input type="number" value={parameter.step ?? 1} onChange={e => onUpdateParameter(idx, 'step', Number(e.target.value))} placeholder="step" />
                        </div>
                      )}
                      {parameter.type === 'random_string' && (
                        <div style={{ display: 'grid', gap: 8 }}>
                          <input type="number" value={parameter.length ?? 12} onChange={e => onUpdateParameter(idx, 'length', Number(e.target.value))} placeholder="length" />
                          <input value={parameter.charset || ''} onChange={e => onUpdateParameter(idx, 'charset', e.target.value)} placeholder="charset override" />
                        </div>
                      )}
                      {parameter.type === 'uuid' && <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>Generated UUID per update cadence.</span>}
                    </div>
                  </td>
                  <td>
                    <div style={{ display: 'grid', gap: 8 }}>
                      {(parameter.type === 'date_time' || parameter.type === 'timestamp') && (
                        <>
                          {parameter.type === 'timestamp' ? (
                            <select value={parameter.format || 'iso8601'} onChange={e => onUpdateParameter(idx, 'format', e.target.value)}>
                              <option value="unix">unix</option>
                              <option value="unix_ms">unix_ms</option>
                              <option value="iso8601">iso8601</option>
                              <option value="rfc3339">rfc3339</option>
                              <option value="rfc3339_nano">rfc3339_nano</option>
                            </select>
                          ) : (
                            <input value={parameter.format || ''} onChange={e => onUpdateParameter(idx, 'format', e.target.value)} placeholder="Go time layout" />
                          )}
                          <input value={parameter.timezone || ''} onChange={e => onUpdateParameter(idx, 'timezone', e.target.value)} placeholder="timezone (UTC, America/New_York)" />
                          <input type="number" value={parameter.offset_seconds ?? 0} onChange={e => onUpdateParameter(idx, 'offset_seconds', Number(e.target.value))} placeholder="offset seconds" />
                        </>
                      )}
                      {(parameter.type === 'counter' || parameter.type === 'random_number') && (
                        <>
                          <input type="number" value={parameter.pad_width ?? 0} onChange={e => onUpdateParameter(idx, 'pad_width', Number(e.target.value))} placeholder="pad width" />
                          <input value={parameter.pad_char || ''} onChange={e => onUpdateParameter(idx, 'pad_char', e.target.value)} placeholder="pad char" />
                        </>
                      )}
                      <input type="number" value={parameter.seed ?? 0} onChange={e => onUpdateParameter(idx, 'seed', Number(e.target.value) || undefined)} placeholder="seed (optional)" />
                    </div>
                  </td>
                  <td className="cell-mono">{parameter.name ? `{{param.${parameter.name}}}` : '—'}</td>
                  <td style={{ textAlign: 'right' }}>
                    <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={() => onRemoveParameter(idx)}>Remove</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}

interface ParameterDataSourcesSectionProps {
  dataEditorMode: Record<number, 'grid' | 'raw'>
  dataSourceHistory: Record<number, DataSourceHistory>
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onAddDataSource: () => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onAutoFixDataSource: (idx: number) => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onRemoveDataSource: (idx: number) => void
  onToggleDataEditorMode: (idx: number) => void
  onToggleOpenDataEditor: (idx: number) => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  openDataEditor: number | null
  script: ScriptData
}

function ParameterDataSourcesSection({
  dataEditorMode,
  dataSourceHistory,
  onAddDataColumn,
  onAddDataRow,
  onAddDataSource,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onAutoFixDataSource,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onRemoveDataSource,
  onToggleDataEditorMode,
  onToggleOpenDataEditor,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  openDataEditor,
  script,
}: ParameterDataSourcesSectionProps) {
  return (
    <>
      <div className="panel-title-row">
        <div>
          <strong style={{ display: 'block', marginBottom: 4 }}>Parameter Data Sources</strong>
          <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>Define reusable data sources for file-backed parameter values and row-based request data.</span>
        </div>
        <button className="btn btn-sm btn-accent" onClick={onAddDataSource}>+ Add Source</button>
      </div>

      {(script.data_sources?.length || 0) === 0 ? (
        <div className="parameter-empty-state">No data sources defined. Add a source for CSV or DAT-backed parameter values.</div>
      ) : (
        <div className="parameter-table-wrap">
          <table className="parameter-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Type</th>
                <th>Strategy</th>
                <th>Header Row</th>
                <th>Rows</th>
                <th>Columns</th>
                <th>Delimiter</th>
                <th>Data File Path</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {(script.data_sources || []).map((source, idx) => {
                const summary = summarizeInlineData(source)
                const parsed = parseInlineData(source)
                const warnings = validateInlineData(source)
                const isEditorOpen = openDataEditor === idx
                const history = dataSourceHistory[idx]

                return (
                  <ParameterDataSourceRows
                    dataEditorMode={dataEditorMode[idx] || 'grid'}
                    history={history}
                    index={idx}
                    isEditorOpen={isEditorOpen}
                    key={`${source.name || 'source'}-${idx}`}
                    onAddDataColumn={onAddDataColumn}
                    onAddDataRow={onAddDataRow}
                    onApplyDedupeHeaders={onApplyDedupeHeaders}
                    onApplyFillHeaders={onApplyFillHeaders}
                    onApplyNormalizeRows={onApplyNormalizeRows}
                    onAutoFixDataSource={onAutoFixDataSource}
                    onExportDataSourceFile={onExportDataSourceFile}
                    onImportDataSourceFile={onImportDataSourceFile}
                    onRedoDataSource={onRedoDataSource}
                    onRemoveDataColumn={onRemoveDataColumn}
                    onRemoveDataRow={onRemoveDataRow}
                    onRemoveDataSource={onRemoveDataSource}
                    onToggleDataEditorMode={onToggleDataEditorMode}
                    onToggleOpenDataEditor={onToggleOpenDataEditor}
                    onUndoDataSource={onUndoDataSource}
                    onUpdateDataCell={onUpdateDataCell}
                    onUpdateDataColumnName={onUpdateDataColumnName}
                    onUpdateDataSource={onUpdateDataSource}
                    parsed={parsed}
                    source={source}
                    summary={summary}
                    warnings={warnings}
                  />
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}

interface ParameterDataSourceRowsProps {
  dataEditorMode: 'grid' | 'raw'
  history?: DataSourceHistory
  index: number
  isEditorOpen: boolean
  onAddDataColumn: (idx: number) => void
  onAddDataRow: (idx: number) => void
  onApplyDedupeHeaders: (idx: number) => void
  onApplyFillHeaders: (idx: number) => void
  onApplyNormalizeRows: (idx: number) => void
  onAutoFixDataSource: (idx: number) => void
  onExportDataSourceFile: (source: DataSourceConfig, idx: number) => void
  onImportDataSourceFile: (idx: number, file: File | null) => void
  onRedoDataSource: (idx: number) => void
  onRemoveDataColumn: (idx: number, colIdx: number) => void
  onRemoveDataRow: (idx: number, rowIdx: number) => void
  onRemoveDataSource: (idx: number) => void
  onToggleDataEditorMode: (idx: number) => void
  onToggleOpenDataEditor: (idx: number) => void
  onUndoDataSource: (idx: number) => void
  onUpdateDataCell: (idx: number, rowIdx: number, colIdx: number, value: string) => void
  onUpdateDataColumnName: (idx: number, colIdx: number, value: string) => void
  onUpdateDataSource: (idx: number, field: keyof DataSourceConfig, value: unknown, trackHistory?: boolean) => void
  parsed: { columns: string[]; rows: string[][] }
  source: DataSourceConfig
  summary: { rows: number; columns: number }
  warnings: string[]
}

function ParameterDataSourceRows({
  dataEditorMode,
  history,
  index,
  isEditorOpen,
  onAddDataColumn,
  onAddDataRow,
  onApplyDedupeHeaders,
  onApplyFillHeaders,
  onApplyNormalizeRows,
  onAutoFixDataSource,
  onExportDataSourceFile,
  onImportDataSourceFile,
  onRedoDataSource,
  onRemoveDataColumn,
  onRemoveDataRow,
  onRemoveDataSource,
  onToggleDataEditorMode,
  onToggleOpenDataEditor,
  onUndoDataSource,
  onUpdateDataCell,
  onUpdateDataColumnName,
  onUpdateDataSource,
  parsed,
  source,
  summary,
  warnings,
}: ParameterDataSourceRowsProps) {
  return (
    <>
      <tr>
        <td>
          <input
            value={source.name || ''}
            onChange={e => onUpdateDataSource(index, 'name', e.target.value)}
            placeholder="users"
          />
        </td>
        <td>
          <select value={source.type || 'csv'} onChange={e => onUpdateDataSource(index, 'type', e.target.value)}>
            <option value="csv">csv</option>
            <option value="dat">dat</option>
          </select>
        </td>
        <td>
          <select value={source.strategy || 'sequential'} onChange={e => onUpdateDataSource(index, 'strategy', e.target.value)}>
            <option value="sequential">sequential</option>
            <option value="random">random</option>
            <option value="unique">unique</option>
          </select>
        </td>
        <td>
          <label className="checkbox-label compact">
            <input
              type="checkbox"
              checked={source.first_row_header !== false}
              onChange={e => onUpdateDataSource(index, 'first_row_header', e.target.checked)}
            />
            <span>{source.first_row_header !== false ? 'Yes' : 'No'}</span>
          </label>
        </td>
        <td className="cell-mono">{summary.rows || '—'}</td>
        <td>
          <input
            value={(source.columns || []).join(', ')}
            onChange={e => onUpdateDataSource(
              index,
              'columns',
              e.target.value.split(',').map(v => v.trim()).filter(Boolean),
            )}
            placeholder="username, password"
          />
        </td>
        <td>
          <input
            value={source.delimiter ?? (source.type === 'dat' ? '\t' : ',')}
            onChange={e => onUpdateDataSource(index, 'delimiter', e.target.value)}
            placeholder={source.type === 'dat' ? '\\t' : ','}
          />
        </td>
        <td>
          <input
            value={source.path || ''}
            onChange={e => onUpdateDataSource(index, 'path', e.target.value)}
            placeholder="/absolute/path/to/data.csv (optional when inline data is used)"
          />
        </td>
        <td style={{ textAlign: 'right' }}>
          <div className="parameter-actions">
            <button className="btn btn-sm btn-ghost" onClick={() => onToggleOpenDataEditor(index)}>
              {isEditorOpen ? 'Hide Data' : 'Edit Data'}
            </button>
            <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={() => onRemoveDataSource(index)}>Remove</button>
          </div>
        </td>
      </tr>
      {isEditorOpen && (
        <tr className="parameter-editor-row">
          <td colSpan={9}>
            <div className="parameter-editor">
              <div className="parameter-editor-header">
                <strong>{source.name || `Source ${index + 1}`}</strong>
                <div className="parameter-editor-meta">
                  <span className="cell-mono">
                    {summary.rows} rows · {summary.columns} columns
                  </span>
                  <div className="parameter-editor-controls">
                    <button className="btn btn-sm btn-ghost" disabled={!history?.past?.length} onClick={() => onUndoDataSource(index)}>Undo</button>
                    <button className="btn btn-sm btn-ghost" disabled={!history?.future?.length} onClick={() => onRedoDataSource(index)}>Redo</button>
                    <button className="btn btn-sm btn-ghost" onClick={() => onToggleDataEditorMode(index)}>
                      {dataEditorMode === 'grid' ? 'Raw Mode' : 'Grid Mode'}
                    </button>
                    <label className="btn btn-sm btn-ghost file-btn">
                      Import
                      <input
                        type="file"
                        accept=".csv,.dat,.txt"
                        onChange={e => {
                          const file = e.target.files?.[0] || null
                          onImportDataSourceFile(index, file)
                          e.currentTarget.value = ''
                        }}
                      />
                    </label>
                    <button className="btn btn-sm btn-ghost" onClick={() => onExportDataSourceFile(source, index)}>Export</button>
                  </div>
                </div>
              </div>
              {warnings.length > 0 && (
                <div className="parameter-validation">
                  <div className="parameter-validation-header">
                    <strong>Data Warnings</strong>
                    <div className="parameter-validation-actions">
                      <button className="btn btn-sm btn-ghost" onClick={() => onApplyFillHeaders(index)}>Fill Headers</button>
                      <button className="btn btn-sm btn-ghost" onClick={() => onApplyDedupeHeaders(index)}>Deduplicate</button>
                      <button className="btn btn-sm btn-ghost" onClick={() => onApplyNormalizeRows(index)}>Normalize Rows</button>
                      <button className="btn btn-sm btn-ghost" onClick={() => onAutoFixDataSource(index)}>Auto-Fix All</button>
                    </div>
                  </div>
                  {warnings.map((warning, warningIdx) => (
                    <div key={`${index}-warn-${warningIdx}`} className="parameter-validation-item">{warning}</div>
                  ))}
                </div>
              )}
              {dataEditorMode === 'grid' ? (
                <div className="parameter-grid-wrap">
                  <div className="parameter-grid-actions">
                    <button className="btn btn-sm btn-ghost" onClick={() => onAddDataRow(index)}>+ Row</button>
                    <button className="btn btn-sm btn-ghost" onClick={() => onAddDataColumn(index)}>+ Column</button>
                  </div>
                  <div className="parameter-grid-scroll">
                    <table className="parameter-grid">
                      <thead>
                        <tr>
                          <th>#</th>
                          {parsed.columns.map((column, colIdx) => (
                            <th key={`${index}-h-${colIdx}`}>
                              <div className="parameter-grid-head">
                                <input
                                  value={column}
                                  onChange={e => onUpdateDataColumnName(index, colIdx, e.target.value)}
                                  placeholder={`col_${colIdx + 1}`}
                                />
                                <button className="btn-icon danger" onClick={() => onRemoveDataColumn(index, colIdx)} title="Remove column">✕</button>
                              </div>
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {parsed.rows.map((row, rowIdx) => (
                          <tr key={`${index}-r-${rowIdx}`}>
                            <td className="parameter-grid-index">
                              {rowIdx + 1}
                              <button className="btn-icon danger" onClick={() => onRemoveDataRow(index, rowIdx)} title="Remove row">✕</button>
                            </td>
                            {parsed.columns.map((_, colIdx) => (
                              <td key={`${index}-c-${rowIdx}-${colIdx}`}>
                                <input
                                  value={row[colIdx] || ''}
                                  onChange={e => onUpdateDataCell(index, rowIdx, colIdx, e.target.value)}
                                />
                              </td>
                            ))}
                          </tr>
                        ))}
                        {parsed.rows.length === 0 && (
                          <tr>
                            <td colSpan={parsed.columns.length + 1} className="parameter-grid-empty">
                              No rows yet. Use + Row to add parameter records.
                            </td>
                          </tr>
                        )}
                      </tbody>
                    </table>
                  </div>
                </div>
              ) : (
                <textarea
                  value={source.inline_data || ''}
                  onChange={e => onUpdateDataSource(index, 'inline_data', e.target.value, true)}
                  placeholder={source.first_row_header !== false ? 'username,password\nalice,pass1\nbob,pass2' : 'alice,pass1\nbob,pass2'}
                  spellCheck={false}
                />
              )}
            </div>
          </td>
        </tr>
      )}
    </>
  )
}

/* Sub-component for editing a single action's details */
function ActionEditor({ action, index, parameters, onUpdate, onAddExtractor, onUpdateExtractor, onRemoveExtractor, onAddAssertion, onUpdateAssertion, onRemoveAssertion, onAddHeader, onUpdateHeader, onRemoveHeader, onAddMetadata, onUpdateMetadata, onRemoveMetadata, onBeginParameterEdit, methodColor, isBrowserScript, isJmsScript, isSoapScript }: {
  action: Action; index: number
  parameters: ParameterConfig[]
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onAddExtractor: (i: number) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onAddAssertion: (i: number) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onAddHeader: (i: number) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
  onRemoveHeader: (ai: number, k: string) => void
  onAddMetadata: (i: number) => void
  onUpdateMetadata: (ai: number, ok: string, nk: string, v: string) => void
  onRemoveMetadata: (ai: number, k: string) => void
  onBeginParameterEdit: (draft: ActionEditorParameterDraft) => void
  methodColor: (m: string) => string
  isBrowserScript: boolean
  isJmsScript: boolean
  isSoapScript: boolean
}) {
  const { selectionState, openSelectionMenu, openParameterEditor } = useActionParameterMenu({
    index,
    onBeginParameterEdit,
    parameters,
  })

  return (
    <div className="action-editor-panel">
      <ActionEditorHeader action={action} methodColor={methodColor} />

      <ActionCoreFields
        action={action}
        index={index}
        isBrowserScript={isBrowserScript}
        isJmsScript={isJmsScript}
        onUpdate={onUpdate}
      />

      {isBrowserScript && (
        <BrowserActionFields action={action} index={index} onUpdate={onUpdate} />
      )}

      {isJmsScript ? (
        <JmsActionFields
          action={action}
          index={index}
          onAddHeader={onAddHeader}
          onAddMetadata={onAddMetadata}
          onAddAssertion={onAddAssertion}
          onAddExtractor={onAddExtractor}
          onRemoveAssertion={onRemoveAssertion}
          onRemoveExtractor={onRemoveExtractor}
          onRemoveHeader={onRemoveHeader}
          onRemoveMetadata={onRemoveMetadata}
          onUpdate={onUpdate}
          onUpdateAssertion={onUpdateAssertion}
          onUpdateExtractor={onUpdateExtractor}
          onUpdateHeader={onUpdateHeader}
          onUpdateMetadata={onUpdateMetadata}
          onSelectionMenu={openSelectionMenu}
        />
      ) : (
        <HttpActionFields
          action={action}
          index={index}
          isBrowserScript={isBrowserScript}
          isSoapScript={isSoapScript}
          onAddAssertion={onAddAssertion}
          onAddExtractor={onAddExtractor}
          onAddHeader={onAddHeader}
          onRemoveAssertion={onRemoveAssertion}
          onRemoveExtractor={onRemoveExtractor}
          onRemoveHeader={onRemoveHeader}
          onUpdate={onUpdate}
          onUpdateAssertion={onUpdateAssertion}
          onUpdateExtractor={onUpdateExtractor}
          onUpdateHeader={onUpdateHeader}
          onSelectionMenu={openSelectionMenu}
        />
      )}

      <ActionThinkTimeToggle action={action} index={index} onUpdate={onUpdate} />

      <ActionParameterMenu selectionState={selectionState} onOpenParameterEditor={openParameterEditor} />
    </div>
  )
}

const BROWSER_ACTION_TYPES = ['browser_navigate', 'browser_click', 'browser_fill', 'browser_select', 'browser_wait', 'browser_screenshot', 'browser_tap', 'browser_long_press', 'browser_swipe']
const JMS_ACTION_TYPES = ['jms_publish', 'jms_consume', 'jms_request_reply', 'jms_ack', 'jms_commit', 'jms_rollback']
const HTTP_ACTION_TYPES = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

interface ActionEditorHeaderProps {
  action: Action
  methodColor: (method: string) => string
}

function ActionEditorHeader({ action, methodColor }: ActionEditorHeaderProps) {
  return (
    <div className="action-editor-top">
      <span className="action-badge" style={{ background: methodColor(action.type), color: '#fff' }}>{action.type}</span>
      <h3>{action.name || 'Unnamed Action'}</h3>
    </div>
  )
}

interface ActionCoreFieldsProps {
  action: Action
  index: number
  isBrowserScript: boolean
  isJmsScript: boolean
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function ActionCoreFields({ action, index, isBrowserScript, isJmsScript, onUpdate }: ActionCoreFieldsProps) {
  const showSelector = isBrowserScript && ['browser_click', 'browser_fill', 'browser_select', 'browser_wait', 'browser_screenshot', 'browser_tap', 'browser_long_press', 'browser_swipe'].includes(action.type)
  const showValue = isBrowserScript && ['browser_fill', 'browser_select'].includes(action.type)
  const targetLabel = isJmsScript
    ? 'Destination'
    : isBrowserScript
    ? action.type === 'browser_navigate'
      ? 'URL or Path'
      : action.type === 'browser_wait'
        ? 'Wait Target or Duration'
        : 'Target Selector'
    : 'Target (path)'
  const targetPlaceholder = isJmsScript
    ? 'orders.in or {{data.destinations.queue}}'
    : isBrowserScript
    ? action.type === 'browser_navigate'
      ? 'https://example.com or /login'
      : action.type === 'browser_wait'
        ? '#ready, text, or 750ms'
        : '#submit'
    : '/api/resource'
  const actionTypes = isBrowserScript ? BROWSER_ACTION_TYPES : isJmsScript ? JMS_ACTION_TYPES : HTTP_ACTION_TYPES

  return (
    <div className="form-grid">
      <label><span>Name</span><input value={action.name} onChange={e => onUpdate(index, 'name', e.target.value)} /></label>
      <label><span>{isBrowserScript || isJmsScript ? 'Action Type' : 'Method'}</span>
        <select value={action.type} onChange={e => onUpdate(index, 'type', e.target.value)}>
          {actionTypes.map(type => <option key={type}>{type}</option>)}
        </select>
      </label>
      <label className="full-width"><span>{targetLabel}</span><input value={action.target} onChange={e => onUpdate(index, 'target', e.target.value)} placeholder={targetPlaceholder} /></label>
      {showSelector && (
        <label className="full-width"><span>Selector Override</span><input value={action.selector || ''} onChange={e => onUpdate(index, 'selector', e.target.value)} placeholder="#submit, input[name=email], [data-testid=save]" /></label>
      )}
      {showValue && (
        <label className="full-width"><span>Value</span><input value={action.value || ''} onChange={e => onUpdate(index, 'value', e.target.value)} placeholder="{{data.users.email}}" /></label>
      )}
    </div>
  )
}

interface ActionThinkTimeToggleProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function ActionThinkTimeToggle({ action, index, onUpdate }: ActionThinkTimeToggleProps) {
  return (
    <div className="sub-section">
      <label className="checkbox-label">
        <input type="checkbox" checked={action.think_time_after ?? false} onChange={e => onUpdate(index, 'think_time_after', e.target.checked)} />
        <span>Apply think time after this action</span>
      </label>
    </div>
  )
}

interface ActionParameterMenuProps {
  selectionState: ActionEditorSelectionState | null
  onOpenParameterEditor: (nextExistingName?: string | null) => void
}

function ActionParameterMenu({ selectionState, onOpenParameterEditor }: ActionParameterMenuProps) {
  if (!selectionState) return null

  return (
    <div
      className="action-parameter-menu"
      style={{ left: selectionState.x, top: selectionState.y }}
      onClick={event => event.stopPropagation()}
    >
      <button type="button" className="btn btn-sm" onClick={() => onOpenParameterEditor(selectionState.existingName)}>
        Parameterize selection
      </button>
      {selectionState.existingName && (
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => onOpenParameterEditor(selectionState.existingName)}>
          Edit parameter
        </button>
      )}
    </div>
  )
}

function useScriptEditorState(initialScript?: ScriptData | null) {
  const [script, setScript] = useState<ScriptData>(initialScript || { ...EMPTY_SCRIPT })
  const [activeAction, setActiveAction] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)
  const [validating, setValidating] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [warnings, setWarnings] = useState<string[]>([])
  const [jsonMode, setJsonMode] = useState(false)
  const [jsonText, setJsonText] = useState('')
  const [openSections, setOpenSections] = useState<Record<string, boolean>>({
    config: true,
    actions: true,
    runtime: false,
    import: true,
    replay: true,
  })
  const [isParameterDialogOpen, setIsParameterDialogOpen] = useState(false)
  const [pendingParameterDraft, setPendingParameterDraft] = useState<ActionEditorParameterDraft | null>(null)
  const [openDataEditor, setOpenDataEditor] = useState<number | null>(null)
  const [dataEditorMode, setDataEditorMode] = useState<Record<number, 'grid' | 'raw'>>({})
  const [dataSourceHistory, setDataSourceHistory] = useState<Record<number, DataSourceHistory>>({})
  const [parameterCandidates, setParameterCandidates] = useState<ParameterCandidate[]>([])
  const [replaying, setReplaying] = useState(false)
  const [replayReport, setReplayReport] = useState<ReplayReport | null>(null)
  const [expandedReplayEvent, setExpandedReplayEvent] = useState<number | null>(null)
  const [expandedReplayBrowserArtifacts, setExpandedReplayBrowserArtifacts] = useState<Record<string, boolean>>({})
  const [copiedReplaySnapshotSection, setCopiedReplaySnapshotSection] = useState<string | null>(null)
  const [activeReplaySessionId, setActiveReplaySessionId] = useState<string | null>(null)
  const [browserReplaySession, setBrowserReplaySession] = useState<BrowserReplaySession | null>(null)
  const [replayScreenshotSelection, setReplayScreenshotSelection] = useState<ReplayScreenshotSelection | null>(null)
  const [replayActivity, setReplayActivity] = useState<ReplayActivityItem[]>([])
  const [replaySeedVariables, setReplaySeedVariables] = useState<Record<string, string>>({})
  const replayEventRefs = useRef<Record<number, HTMLDivElement | null>>({})
  const [replayOptions, setReplayOptions] = useState<ReplayOptionsState>({
    groupName: '',
    ignoreThinkTime: true,
    startPaused: false,
    variablesText: '{\n  \n}',
  })

  const toggleSection = (id: string) => setOpenSections(prev => ({ ...prev, [id]: !prev[id] }))

  const hydrateEditorState = useCallback((nextScript: ScriptData) => {
    setScript(nextScript)
    setJsonText(JSON.stringify({ script: nextScript }, null, 2))
    setActiveAction(null)
    setIsParameterDialogOpen(false)
    setPendingParameterDraft(null)
    setOpenDataEditor(null)
    setDataEditorMode({})
    setDataSourceHistory({})
    setParameterCandidates([])
    setError('')
    setSuccess('')
    setWarnings([])
    setExpandedReplayBrowserArtifacts({})
    setReplayScreenshotSelection(null)
  }, [])

  const closeParameterDialog = () => {
    setIsParameterDialogOpen(false)
    setPendingParameterDraft(null)
  }

  const closeReplayScreenshot = () => {
    setReplayScreenshotSelection(null)
  }

  return {
    activeAction,
    activeReplaySessionId,
    browserReplaySession,
    closeParameterDialog,
    copiedReplaySnapshotSection,
    dataEditorMode,
    dataSourceHistory,
    error,
    expandedReplayBrowserArtifacts,
    expandedReplayEvent,
    hydrateEditorState,
    isParameterDialogOpen,
    jsonMode,
    jsonText,
    openDataEditor,
    openSections,
    parameterCandidates,
    pendingParameterDraft,
    replayActivity,
    replayEventRefs,
    replayOptions,
    replayReport,
    replayScreenshotSelection,
    replaySeedVariables,
    replaying,
    saving,
    script,
    setActiveAction,
    setActiveReplaySessionId,
    setBrowserReplaySession,
    setCopiedReplaySnapshotSection,
    setDataEditorMode,
    setDataSourceHistory,
    setError,
    setExpandedReplayBrowserArtifacts,
    setExpandedReplayEvent,
    setIsParameterDialogOpen,
    setJsonMode,
    setJsonText,
    setOpenDataEditor,
    setOpenSections,
    setParameterCandidates,
    setPendingParameterDraft,
    setReplayActivity,
    setReplayOptions,
    setReplayReport,
    setReplayScreenshotSelection,
    setReplaySeedVariables,
    setReplaying,
    setSaving,
    setScript,
    setSuccess,
    setValidating,
    setWarnings,
    success,
    toggleSection,
    validating,
    warnings,
    closeReplayScreenshot,
  }
}

function useActionParameterMenu({
  index,
  onBeginParameterEdit,
  parameters,
}: {
  index: number
  onBeginParameterEdit: (draft: ActionEditorParameterDraft) => void
  parameters: ParameterConfig[]
}) {
  const [selectionState, setSelectionState] = useState<ActionEditorSelectionState | null>(null)

  useEffect(() => {
    if (!selectionState) return
    const closeMenu = () => setSelectionState(null)
    window.addEventListener('click', closeMenu)
    return () => window.removeEventListener('click', closeMenu)
  }, [selectionState])

  const detectSelectedParameter = useCallback((selectedText: string): ParameterConfig | null => {
    const match = selectedText.trim().match(/^{{\s*param\.([a-zA-Z0-9_]+)\s*}}$/)
    if (!match) return null
    const normalizedName = sanitizeVarName(match[1])
    return parameters.find(parameter => sanitizeVarName(parameter.name || '') === normalizedName) || null
  }, [parameters])

  const openSelectionMenu = useCallback((event: MouseEvent<HTMLTextAreaElement>) => {
    const textarea = event.currentTarget
    const start = textarea.selectionStart ?? 0
    const end = textarea.selectionEnd ?? 0
    const selectedText = (textarea.value || '').slice(start, end)
    if (!selectedText.trim()) {
      setSelectionState(null)
      return
    }

    event.preventDefault()
    const existingParameter = detectSelectedParameter(selectedText)
    setSelectionState({
      start,
      end,
      selectedText,
      x: event.clientX,
      y: event.clientY,
      existingName: existingParameter?.name || null,
    })
  }, [detectSelectedParameter])

  const openParameterEditor = useCallback((nextExistingName?: string | null) => {
    if (!selectionState) return
    const existingParameter = nextExistingName
      ? parameters.find(parameter => sanitizeVarName(parameter.name || '') === sanitizeVarName(nextExistingName))
      : detectSelectedParameter(selectionState.selectedText)

    const requestedName = existingParameter?.name
      || sanitizeVarName(selectionState.selectedText.replace(/^{{\s*param\.|\s*}}$/g, ''))
    const seedValue = existingParameter?.values?.[0] || selectionState.selectedText

    setSelectionState(null)
    onBeginParameterEdit({
      actionIndex: index,
      start: selectionState.start,
      end: selectionState.end,
      selectedText: selectionState.selectedText,
      existingName: existingParameter?.name || null,
      requestedName,
      seedValue,
    })
  }, [detectSelectedParameter, index, onBeginParameterEdit, parameters, selectionState])

  return { openParameterEditor, openSelectionMenu, selectionState }
}

interface ActionBodyTextareaProps {
  onChange: (value: string) => void
  onContextMenu: (event: MouseEvent<HTMLTextAreaElement>) => void
  placeholder: string
  rows: number
  value: string
}

function ActionBodyTextarea({ onChange, onContextMenu, placeholder, rows, value }: ActionBodyTextareaProps) {
  return (
    <textarea
      className="body-textarea"
      value={value}
      onChange={event => onChange(event.target.value)}
      onContextMenu={onContextMenu}
      placeholder={placeholder}
      rows={rows}
    />
  )
}

interface BrowserActionFieldsProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function BrowserActionFields({ action, index, onUpdate }: BrowserActionFieldsProps) {
  const waitFor = action.wait_for || {}
  const capture = action.capture || {}
  const params = action.params || {}

  return (
    <>
      {action.type === 'browser_wait' && (
        <div className="sub-section browser-action-section">
          <div className="sub-header"><h4>Wait Condition</h4></div>
          <div className="browser-action-grid">
            <label>
              <span>Type</span>
              <select
                value={waitFor.type || 'selector'}
                onChange={e => onUpdate(index, 'wait_for', { ...waitFor, type: e.target.value })}
              >
                <option value="selector">Selector visible</option>
                <option value="hidden">Selector hidden</option>
                <option value="text">Text appears</option>
                <option value="url_contains">URL contains</option>
                <option value="duration">Duration</option>
              </select>
            </label>
            <label>
              <span>Value</span>
              <input
                value={waitFor.value || ''}
                onChange={e => onUpdate(index, 'wait_for', { ...waitFor, value: e.target.value })}
                placeholder={waitFor.type === 'duration' ? '1000ms' : '#ready or expected text'}
              />
            </label>
            <label>
              <span>Timeout (ms)</span>
              <input
                type="number"
                min="0"
                value={waitFor.timeout_ms ?? 10000}
                onChange={e => onUpdate(index, 'wait_for', { ...waitFor, timeout_ms: Math.max(0, Number(e.target.value) || 0) })}
              />
            </label>
          </div>
        </div>
      )}

      {action.type === 'browser_long_press' && (
        <div className="sub-section browser-action-section">
          <div className="sub-header"><h4>Long Press</h4></div>
          <div className="browser-action-grid">
            <label>
              <span>Duration (ms)</span>
              <input
                type="number"
                min="1"
                value={typeof params.duration_ms === 'number' ? params.duration_ms : 350}
                onChange={e => onUpdate(index, 'params', { ...params, duration_ms: Math.max(1, Number(e.target.value) || 1) })}
              />
            </label>
          </div>
        </div>
      )}

      {action.type === 'browser_swipe' && (
        <div className="sub-section browser-action-section">
          <div className="sub-header"><h4>Swipe Gesture</h4></div>
          <div className="browser-action-grid">
            <label>
              <span>Direction</span>
              <select
                value={typeof params.direction === 'string' ? params.direction : 'left'}
                onChange={e => onUpdate(index, 'params', { ...params, direction: e.target.value })}
              >
                <option value="left">Left</option>
                <option value="right">Right</option>
                <option value="up">Up</option>
                <option value="down">Down</option>
              </select>
            </label>
            <label>
              <span>Distance (px)</span>
              <input
                type="number"
                min="1"
                value={typeof params.distance === 'number' ? params.distance : 180}
                onChange={e => onUpdate(index, 'params', { ...params, distance: Math.max(1, Number(e.target.value) || 1) })}
              />
            </label>
          </div>
        </div>
      )}

      <div className="sub-section browser-action-section">
        <div className="sub-header"><h4>Capture</h4></div>
        <div className="browser-capture-row">
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={!!capture.screenshot || action.type === 'browser_screenshot'}
              disabled={action.type === 'browser_screenshot'}
              onChange={e => onUpdate(index, 'capture', { ...capture, screenshot: e.target.checked })}
            />
            <span>Screenshot</span>
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={!!capture.full_page}
              onChange={e => onUpdate(index, 'capture', { ...capture, full_page: e.target.checked })}
            />
            <span>Full page</span>
          </label>
          <label className="checkbox-label">
            <input
              type="checkbox"
              checked={!!capture.html}
              onChange={e => onUpdate(index, 'capture', { ...capture, html: e.target.checked })}
            />
            <span>HTML</span>
          </label>
        </div>
      </div>
    </>
  )
}

interface JmsActionFieldsProps {
  action: Action
  index: number
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onAddHeader: (i: number) => void
  onAddMetadata: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onRemoveMetadata: (ai: number, k: string) => void
  onSelectionMenu: (event: MouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
  onUpdateMetadata: (ai: number, ok: string, nk: string, v: string) => void
}

function JmsActionFields({
  action,
  index,
  onAddAssertion,
  onAddExtractor,
  onAddHeader,
  onAddMetadata,
  onRemoveAssertion,
  onRemoveExtractor,
  onRemoveHeader,
  onRemoveMetadata,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
  onUpdateHeader,
  onUpdateMetadata,
}: JmsActionFieldsProps) {
  const params = action.params || {}

  return (
    <>
      <div className="sub-section">
        <div className="sub-header"><h4>Message</h4></div>
        <div className="form-grid">
          {(action.type === 'jms_publish' || action.type === 'jms_request_reply') && (
            <label className="full-width">
              <span>Body</span>
              <ActionBodyTextarea
                onChange={value => onUpdate(index, 'body', value)}
                onContextMenu={onSelectionMenu}
                placeholder='{"orderId": "{{data.orders.id}}"}'
                rows={5}
                value={action.body || ''}
              />
            </label>
          )}
          <label>
            <span>Correlation ID</span>
            <input
              value={typeof params.correlation_id === 'string' ? params.correlation_id : ''}
              onChange={e => onUpdate(index, 'params', { ...params, correlation_id: e.target.value })}
              placeholder="{{var.jms_message_id}} or {{fn.uuid()}}"
            />
          </label>
          {(action.type === 'jms_request_reply' || action.type === 'jms_publish' || action.type === 'jms_consume') && (
            <label>
              <span>Timeout (ms)</span>
              <input
                type="number"
                min="0"
                value={typeof params.timeout_ms === 'number' ? params.timeout_ms : ''}
                onChange={e => onUpdate(index, 'params', { ...params, timeout_ms: Number(e.target.value) || undefined })}
                placeholder="Use script default"
              />
            </label>
          )}
          {action.type === 'jms_request_reply' && (
            <label className="full-width">
              <span>Reply To Override</span>
              <input
                value={typeof params.reply_to === 'string' ? params.reply_to : ''}
                onChange={e => onUpdate(index, 'params', { ...params, reply_to: e.target.value })}
                placeholder="orders.reply"
              />
            </label>
          )}
        </div>
      </div>

      <div className="sub-section">
        <div className="sub-header"><h4>Message Headers</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddHeader(index)}>+ Add</button></div>
        {Object.entries(action.headers || {}).map(([key, value]) => (
          <div key={key} className="kv-row">
            <input className="kv-key" value={key} onChange={e => onUpdateHeader(index, key, e.target.value, value)} placeholder="Header" />
            <input className="kv-val" value={value} onChange={e => onUpdateHeader(index, key, key, e.target.value)} placeholder="Value" />
            <button className="btn-icon danger" onClick={() => onRemoveHeader(index, key)}>✕</button>
          </div>
        ))}
      </div>

      <div className="sub-section">
        <div className="sub-header"><h4>Message Properties</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddMetadata(index)}>+ Add</button></div>
        {Object.entries(action.metadata || {}).map(([key, value]) => (
          <div key={key} className="kv-row">
            <input className="kv-key" value={key} onChange={e => onUpdateMetadata(index, key, e.target.value, String(value || ''))} placeholder="Property" />
            <input className="kv-val" value={String(value || '')} onChange={e => onUpdateMetadata(index, key, key, e.target.value)} placeholder="Value" />
            <button className="btn-icon danger" onClick={() => onRemoveMetadata(index, key)}>✕</button>
          </div>
        ))}
      </div>

      <ProtocolExtractorsSection
        action={action}
        index={index}
        isJmsScript={true}
        onAddExtractor={onAddExtractor}
        onRemoveExtractor={onRemoveExtractor}
        onUpdateExtractor={onUpdateExtractor}
      />

      <ProtocolAssertionsSection
        action={action}
        index={index}
        isJmsScript={true}
        onAddAssertion={onAddAssertion}
        onRemoveAssertion={onRemoveAssertion}
        onUpdateAssertion={onUpdateAssertion}
      />
    </>
  )
}

interface HttpActionFieldsProps {
  action: Action
  index: number
  isBrowserScript: boolean
  isSoapScript: boolean
  onAddAssertion: (i: number) => void
  onAddExtractor: (i: number) => void
  onAddHeader: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onSelectionMenu: (event: MouseEvent<HTMLTextAreaElement>) => void
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
}

function HttpActionFields({
  action,
  index,
  isBrowserScript,
  isSoapScript,
  onAddAssertion,
  onAddExtractor,
  onAddHeader,
  onRemoveAssertion,
  onRemoveExtractor,
  onRemoveHeader,
  onSelectionMenu,
  onUpdate,
  onUpdateAssertion,
  onUpdateExtractor,
  onUpdateHeader,
}: HttpActionFieldsProps) {
  return (
    <>
      {isSoapScript && (
        <SoapActionFields action={action} index={index} onUpdate={onUpdate} />
      )}

      {!isBrowserScript && (
        <ProtocolHeadersSection
          action={action}
          index={index}
          onAddHeader={onAddHeader}
          onRemoveHeader={onRemoveHeader}
          onUpdateHeader={onUpdateHeader}
        />
      )}

      {!isBrowserScript && ['POST', 'PUT', 'PATCH'].includes(action.type) && (
        <div className="sub-section">
          <h4>Body</h4>
          <ActionBodyTextarea
            onChange={value => onUpdate(index, 'body', value)}
            onContextMenu={onSelectionMenu}
            placeholder='{"key": "value"}'
            rows={4}
            value={action.body || ''}
          />
        </div>
      )}

      {!isBrowserScript && (
        <ProtocolExtractorsSection
          action={action}
          index={index}
          isJmsScript={false}
          onAddExtractor={onAddExtractor}
          onRemoveExtractor={onRemoveExtractor}
          onUpdateExtractor={onUpdateExtractor}
        />
      )}

      {!isBrowserScript && (
        <ProtocolAssertionsSection
          action={action}
          index={index}
          isJmsScript={false}
          onAddAssertion={onAddAssertion}
          onRemoveAssertion={onRemoveAssertion}
          onUpdateAssertion={onUpdateAssertion}
        />
      )}
    </>
  )
}

interface SoapActionFieldsProps {
  action: Action
  index: number
  onUpdate: (i: number, f: keyof Action, v: unknown) => void
}

function SoapActionFields({ action, index, onUpdate }: SoapActionFieldsProps) {
  return (
    <div className="sub-section">
      <div className="sub-header"><h4>SOAP Action</h4></div>
      <div className="form-grid">
        <label>
          <span>SOAPAction</span>
          <input value={action.soap?.action || ''} onChange={e => onUpdate(index, 'soap', { ...(action.soap || {}), action: e.target.value })} placeholder="urn:CreateOrder" />
        </label>
        <label>
          <span>Operation</span>
          <input value={action.soap?.operation || ''} onChange={e => onUpdate(index, 'soap', { ...(action.soap || {}), operation: e.target.value })} placeholder="CreateOrder" />
        </label>
        <label>
          <span>Body Mode</span>
          <select value={action.soap?.body_mode || 'raw_xml'} onChange={e => onUpdate(index, 'soap', { ...(action.soap || {}), body_mode: e.target.value })}>
            <option value="raw_xml">raw_xml</option>
            <option value="generated">generated</option>
          </select>
        </label>
      </div>
    </div>
  )
}

interface ProtocolHeadersSectionProps {
  action: Action
  index: number
  onAddHeader: (i: number) => void
  onRemoveHeader: (ai: number, k: string) => void
  onUpdateHeader: (ai: number, ok: string, nk: string, v: string) => void
}

function ProtocolHeadersSection({ action, index, onAddHeader, onRemoveHeader, onUpdateHeader }: ProtocolHeadersSectionProps) {
  return (
    <div className="sub-section">
      <div className="sub-header"><h4>Headers</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddHeader(index)}>+ Add</button></div>
      {Object.entries(action.headers || {}).map(([key, value]) => (
        <div key={key} className="kv-row">
          <input className="kv-key" value={key} onChange={e => onUpdateHeader(index, key, e.target.value, value)} placeholder="Key" />
          <input className="kv-val" value={value} onChange={e => onUpdateHeader(index, key, key, e.target.value)} placeholder="Value" />
          <button className="btn-icon danger" onClick={() => onRemoveHeader(index, key)}>✕</button>
        </div>
      ))}
    </div>
  )
}

interface ProtocolExtractorsSectionProps {
  action: Action
  index: number
  isJmsScript: boolean
  onAddExtractor: (i: number) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
}

function ProtocolExtractorsSection({ action, index, isJmsScript, onAddExtractor, onRemoveExtractor, onUpdateExtractor }: ProtocolExtractorsSectionProps) {
  return (
    <div className="sub-section">
      <div className="sub-header"><h4>Extractors</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddExtractor(index)}>+ Add</button></div>
      {(action.extractors || []).map((extractor, extractorIndex) => (
        <div key={extractorIndex} className="extractor-row">
          <input value={extractor.name} onChange={e => onUpdateExtractor(index, extractorIndex, 'name', e.target.value)} placeholder="Variable name" />
          <select value={extractor.source} onChange={e => onUpdateExtractor(index, extractorIndex, 'source', e.target.value)}>
            <option value="body">Body</option><option value="header">Header</option>
            {isJmsScript ? <><option value="property">Property</option><option value="message_id">Message ID</option><option value="correlation_id">Correlation ID</option></> : <option value="cookie">Cookie</option>}
          </select>
          <select value={extractor.type} onChange={e => onUpdateExtractor(index, extractorIndex, 'type', e.target.value)}>
            <option value="jsonpath">JSONPath</option><option value="regex">Regex</option>{!isJmsScript && <><option value="boundary">Boundary</option><option value="xpath">XPath</option></>}
          </select>
          <input value={extractor.expression} onChange={e => onUpdateExtractor(index, extractorIndex, 'expression', e.target.value)} placeholder="$.data.id" className="ext-expr" />
          <button className="btn-icon danger" onClick={() => onRemoveExtractor(index, extractorIndex)}>✕</button>
        </div>
      ))}
    </div>
  )
}

interface ProtocolAssertionsSectionProps {
  action: Action
  index: number
  isJmsScript: boolean
  onAddAssertion: (i: number) => void
  onRemoveAssertion: (ai: number, ai2: number) => void
  onUpdateAssertion: (ai: number, ai2: number, f: keyof Assertion, v: unknown) => void
}

function ProtocolAssertionsSection({ action, index, isJmsScript, onAddAssertion, onRemoveAssertion, onUpdateAssertion }: ProtocolAssertionsSectionProps) {
  return (
    <div className="sub-section">
      <div className="sub-header"><h4>Assertions</h4><button className="btn btn-sm btn-ghost" onClick={() => onAddAssertion(index)}>+ Add</button></div>
      {(action.assertions || []).map((assertion, assertionIndex) => (
        <div key={assertionIndex} className="assertion-row">
          <select value={assertion.type} onChange={e => onUpdateAssertion(index, assertionIndex, 'type', e.target.value)}>
            {isJmsScript ? (
              <>
                <option value="message_received">Message Received</option><option value="body_contains">Body Contains</option>
                <option value="property_equals">Property Equals</option><option value="header_equals">Header Equals</option>
                <option value="correlation_match">Correlation Match</option><option value="response_time_ms">Response Time (ms)</option>
              </>
            ) : (
              <>
                <option value="status_code">Status Code</option><option value="response_time_ms">Response Time (ms)</option>
                <option value="body_contains">Body Contains</option><option value="header_contains">Header Contains</option>
              </>
            )}
          </select>
          {assertion.type === 'status_code' && <input type="number" value={typeof assertion.expected === 'number' ? assertion.expected : 200} onChange={e => onUpdateAssertion(index, assertionIndex, 'expected', Number(e.target.value))} />}
          {assertion.type === 'response_time_ms' && <input type="number" value={assertion.max ?? 5000} onChange={e => onUpdateAssertion(index, assertionIndex, 'max', Number(e.target.value))} placeholder="Max ms" />}
          {(assertion.type === 'body_contains' || assertion.type === 'header_contains' || assertion.type === 'correlation_match') && <input value={assertion.value ?? ''} onChange={e => onUpdateAssertion(index, assertionIndex, 'value', e.target.value)} placeholder="Expected text" />}
          {(assertion.type === 'property_equals' || assertion.type === 'header_equals') && <input value={assertion.value ?? ''} onChange={e => onUpdateAssertion(index, assertionIndex, 'value', e.target.value)} placeholder="Name" />}
          {(assertion.type === 'property_equals' || assertion.type === 'header_equals') && <input value={typeof assertion.expected === 'string' || typeof assertion.expected === 'number' ? String(assertion.expected) : ''} onChange={e => onUpdateAssertion(index, assertionIndex, 'expected', e.target.value)} placeholder="Expected value" />}
          <button className="btn-icon danger" onClick={() => onRemoveAssertion(index, assertionIndex)}>✕</button>
        </div>
      ))}
    </div>
  )
}
