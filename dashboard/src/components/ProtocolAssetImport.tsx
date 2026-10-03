import { ImportConversionDetails, ImportEvidenceFacts } from './ImportConversionDetails'
import ImportWorkflowGuide from './ImportWorkflowGuide'
import ImportSaveConfirmation from './ImportSaveConfirmation'
import { useImportSaveOutcome, type ImportSaveDestination } from './useImportSaveOutcome'
import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import { useCallback, useEffect, useRef, useState } from 'react'

import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { type BrowserReplayReport, type BrowserReplayStartResponse, type ReplayRepairSuggestion, type StoredScript } from '../types'
import { type AgentforceReview, type AgentforceTransformChange, type AsyncAPIImportResult, type AsyncAPISupportMatrixEntry, type BrokerConfigImportResult, type BrokerConfigSupportMatrixEntry, type BrowserRecordingImportResult, type CloudTestImportResult, type CloudTestSupportMatrixEntry, type DevWebImportResult, type DevWebSupportMatrixEntry, type EventHubsCaptureImportResult, type EventHubsCaptureSupportMatrixEntry, type HARImportResult, type HARImportSummary, type K6ImportResult, type K6SupportMatrixEntry, type MulesoftReview, type OpenAPICorrelation, type OpenAPIImportResult, type OpenAPISupportMatrixEntry, type PostmanImportResult, type PostmanSupportMatrixEntry, type RAMLImportResult, type RAMLSupportMatrixEntry, type SalesforceImportResult, type SalesforceReview, type SecretBindingSelection, type SharedValues, type WSDLImportResult } from '../types/scriptImports'
import { type ScriptDefinition, type SourceReview } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import AuraOutcomeReview from './AuraOutcomeReview'
import AuraParameterLocationDetails from './AuraParameterLocationDetails'
import ImportCoverageButton from './ImportCoverageButton'
import ImportDropzone from './ImportDropzone'
import BrowserReplayProgress from './BrowserReplayProgress'
import ReplayPreflightCard from './ReplayPreflightCard'
import { type PreparedSecretBindings, type SecretBindingReviewHandle } from './SecretBindingReview'
import ScriptImportReview from './ScriptImportReview'
import SelectorCandidatesReview, { type SelectorCandidate } from './SelectorCandidatesReview'
import SourceMappingReview from './SourceMappingReview'
import type { ImportSourceIssueFocus } from './ImportSourceIssueFocus'
import { withFallbackPersistedScript } from './importSaveUtils'
import { useImportReviewGeneration } from './importReviewGeneration'
import { useSourceSecretSessionLifecycle } from './sourceSecretSessionLifecycle'
import { salesforceApiSurfaceBreakdown, salesforceEndpointTotal } from './salesforceReviewSummary'
import { buildReplayStartPayload, createReplaySessionId, isDefiniteReplayStartRejection } from './scriptEditorUtils'
import { refreshedSecretBindingRequirements, replaceSecretBindingRequirementsForScript, requirementsPreserveDestinationValues, secretBindingRequirementsForScript, updateSecretBindingRequirementNames } from './secretBindingUtils'
import {
  activeProtectedImportSessionId,
  credentialPreservingImportDraftRequiresSave,
	  earliestImportDraftExpiry,
  importDraftExpiryStatus,
  invalidateProtectedImportCandidate,
  mergeProtectedParameterizationSelectionIds,
} from './protectedImportSession'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import './SampleScriptImport.css'

export type ImportKind = 'devweb' | 'har' | 'recorder-export' | 'openapi' | 'raml' | 'postman' | 'wsdl' | 'asyncapi' | 'k6' | 'cloudtest' | 'eventhubs-capture' | 'broker-config'

const ASYNCAPI_SCRIPT_PAGE_SIZE = 50
const REPLACE_TARGET_PAGE_SIZE = 100

interface Props {
  kind: ImportKind
  initialFile?: File | null
  initialMode?: string | null
  projectId?: string
  // folderId is the destination folder for scripts saved from this import,
  // matching EnterpriseArchiveImport and ScriptEditor. A script that already
  // carries its own folder_id keeps it; this only supplies a default for the
  // common case of importing while a folder is open.
  folderId?: string | null
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition) => void
  onClose?: () => void
  onOperationBusyChange?: (busy: boolean) => void
}

const copy = {
  devweb: {
    title: 'Import DevWeb Script',
    subtitle: 'Upload a DevWeb .js file or ZIP archive and convert common WebRequest, WebSocket, and gRPC invoke calls into APEX Load DSL.',
    accept: '.js,.zip,application/javascript,text/javascript,application/zip',
    drop: 'Drop a DevWeb .js or .zip file here',
    button: 'Convert DevWeb Script',
  },
  har: {
    title: 'Import HAR',
    subtitle: 'Upload a Chrome DevTools HAR file and convert recorded HTTP entries into a reviewable APEX Load HTTP script. Salesforce traffic is auto-detected and can emit a native Salesforce draft.',
    accept: '.har,application/json',
    drop: 'Drop a .har file here',
    button: 'Convert HAR',
  },
  'recorder-export': {
    title: 'Import Browser Recording',
    subtitle: 'Upload a Chrome DevTools Recorder JSON export or a Playwright codegen script and convert it into a browser script with ranked selector candidates for replay self-heal.',
    accept: '.json,.js,.ts,application/json,application/javascript,text/javascript',
    drop: 'Drop a Chrome Recorder .json or Playwright .js/.ts file here',
    button: 'Convert Recording',
  },
  openapi: {
    title: 'Import OpenAPI',
    subtitle: 'Upload an OpenAPI or Swagger JSON/YAML document and generate grouped REST request flows, auth placeholders, and data-source review notes.',
    accept: '.json,.yaml,.yml,application/json,application/yaml,text/yaml,text/plain',
    drop: 'Drop an OpenAPI JSON or YAML file here',
    button: 'Convert OpenAPI',
  },
  raml: {
    title: 'Import RAML',
    subtitle: 'Upload a RAML 1.0 JSON/YAML document — or a .zip bundle whose !include files resolve together — and generate HTTP actions with resource, method, example, OAuth2, and Anypoint policy review notes.',
    accept: '.raml,.yaml,.yml,.json,.zip,application/json,application/yaml,text/yaml,text/plain,application/zip',
    drop: 'Drop a RAML file or .zip bundle here',
    button: 'Convert RAML',
  },
  postman: {
    title: 'Import Postman Collection',
    subtitle: 'Upload a Postman collection JSON file and convert folders, requests, variables, and auth placeholders into an APEX Load HTTP script.',
    accept: '.postman_collection.json,.json,application/json',
    drop: 'Drop a Postman collection JSON file here',
    button: 'Convert Postman Collection',
  },
  wsdl: {
    title: 'Import WSDL',
    subtitle: 'Upload a WSDL document and generate SOAP request actions with SOAPAction, namespace, payload, and fault review guidance.',
    accept: '.wsdl,.xml,text/xml,application/xml',
    drop: 'Drop a .wsdl or .xml file here',
    button: 'Convert WSDL',
  },
  asyncapi: {
    title: 'Import AsyncAPI',
    subtitle: 'Upload an AsyncAPI 2.x or 3.x document and generate Kafka produce/consume/request-reply actions with brokers, topics, and sample payloads.',
    accept: '.json,.yaml,.yml,application/json,application/yaml,text/yaml,text/plain',
    drop: 'Drop an AsyncAPI JSON or YAML file here',
    button: 'Convert AsyncAPI',
  },
  k6: {
    title: 'Import k6 Script',
    subtitle: 'Upload a k6 JavaScript file and convert the supported HTTP subset into APEX Load actions with review notes for unsupported code.',
    accept: '.js,.ts,application/javascript,text/javascript,text/plain',
    drop: 'Drop a k6 .js file here',
    button: 'Convert k6 Script',
  },
  cloudtest: {
    title: 'Import Akamai CloudTest Export',
    subtitle: 'Upload a dependency-complete CloudTest XML or ZIP export. APEX classifies every source element and creates fixture-gated Clip scripts, Composition drafts, and preserved review artifacts.',
    accept: '.xml,.zip,text/xml,application/xml,application/zip',
    drop: 'Drop a CloudTest .xml or .zip export here',
    button: 'Inspect CloudTest Export',
  },
  'eventhubs-capture': {
    title: 'Import Event Hubs Capture',
    subtitle: 'Upload an Azure Event Hubs Capture Avro file and seed native eventhubs_produce actions in mock mode for review.',
    accept: '.avro,application/octet-stream',
    drop: 'Drop an Event Hubs Capture .avro file here',
    button: 'Convert Capture',
  },
  'broker-config': {
    title: 'Import Broker Config',
    subtitle: 'Upload IBM MQ MQSC or JMS JNDI properties and seed a JMS topology draft from queues, topics, and bindings.',
    accept: '.mqsc,.properties,.txt,text/plain,application/octet-stream',
    drop: 'Drop MQSC or JNDI properties here',
    button: 'Convert Broker Config',
  },
} satisfies Record<ImportKind, Record<string, string>>

const IMPORT_FILE_MAX_BYTES = 50 * 1024 * 1024
const HAR_FILE_MAX_BYTES = 100 * 1024 * 1024
const POSTMAN_ENVIRONMENT_ACCEPT = '.postman_environment.json,.json,application/json'

function formatBytes(bytes: number): string {
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${bytes} B`
}

function acceptTokens(accept: string): string[] {
  return accept.split(',').map(token => token.trim().toLowerCase()).filter(Boolean)
}

function fileMatchesAccept(file: File, accept: string): boolean {
  const name = file.name.toLowerCase()
  const type = file.type.toLowerCase()
  return acceptTokens(accept).some(token => {
    if (token.startsWith('.')) return name.endsWith(token)
    if (token.endsWith('/*')) return type.startsWith(token.slice(0, -1))
    return type === token
  })
}

function validateImportFile(kind: ImportKind, file: File): string | null {
  const maxBytes = kind === 'har' ? HAR_FILE_MAX_BYTES : IMPORT_FILE_MAX_BYTES
  if (file.size > maxBytes) {
    return `${file.name} is ${formatBytes(file.size)}; imports are limited to ${formatBytes(maxBytes)}.`
  }
  if (!fileMatchesAccept(file, copy[kind].accept)) {
    const extensions = acceptTokens(copy[kind].accept).filter(token => token.startsWith('.')).join(', ')
    return `${file.name} is not a supported ${copy[kind].title.replace(/^Import\s+/i, '').toLowerCase()} file${extensions ? ` (${extensions})` : ''}.`
  }
  return null
}

function validatePostmanEnvironmentFile(file: File): string | null {
  if (file.size > IMPORT_FILE_MAX_BYTES) {
    return `${file.name} is ${formatBytes(file.size)}; imports are limited to ${formatBytes(IMPORT_FILE_MAX_BYTES)}.`
  }
  if (file.name.toLowerCase().endsWith('.postman_collection.json')) {
    return `${file.name} is a Postman collection; choose a Postman environment file for this field.`
  }
  if (!fileMatchesAccept(file, POSTMAN_ENVIRONMENT_ACCEPT)) {
    return `${file.name} is not a supported Postman environment file (.postman_environment.json, .json).`
  }
  return null
}

type ProtocolImportResult = DevWebImportResult | HARImportResult | SalesforceImportResult | BrowserRecordingImportResult | OpenAPIImportResult | RAMLImportResult | PostmanImportResult | WSDLImportResult | AsyncAPIImportResult | K6ImportResult | CloudTestImportResult | EventHubsCaptureImportResult | BrokerConfigImportResult
type SourceImportSupportEntry = DevWebSupportMatrixEntry | OpenAPISupportMatrixEntry | RAMLSupportMatrixEntry | PostmanSupportMatrixEntry | AsyncAPISupportMatrixEntry | K6SupportMatrixEntry | CloudTestSupportMatrixEntry | EventHubsCaptureSupportMatrixEntry | BrokerConfigSupportMatrixEntry

type ProtectedImportCommitOverrides = {
  sessionId: string
  secretBindings: Record<string, string>
  parameterizationSelectedIds: string[]
}

type SourceImportSupportState = {
  entries: SourceImportSupportEntry[]
  notes: string[]
}

// Every importer's support matrix answers the same {support_matrix, notes}
// shape, and only the active kind's matrix is ever rendered. One fetcher keyed
// by kind replaces the eight parallel state slots and eight near-identical
// effects that each existed to hold that single value. Kinds absent here
// publish no matrix, which mirrors the backend importer registry.
type SourceImportSupportResponse = {
  support_matrix?: SourceImportSupportEntry[]
  notes?: string[]
}

const sourceImportSupportFetchers: Partial<Record<ImportKind, (api: ReturnType<typeof useApi>) => Promise<SourceImportSupportResponse>>> = {
  'devweb': api => api.getDevWebSupportMatrix(),
  'raml': api => api.getRAMLSupportMatrix(),
  'openapi': api => api.getOpenAPISupportMatrix(),
  'postman': api => api.getPostmanSupportMatrix(),
  'asyncapi': api => api.getAsyncAPISupportMatrix(),
  'k6': api => api.getK6SupportMatrix(),
  'cloudtest': api => api.getCloudTestSupportMatrix(),
  'eventhubs-capture': api => api.getEventHubsCaptureSupportMatrix(),
  'broker-config': api => api.getBrokerConfigSupportMatrix(),
}

type SourceImportCoverageSection = {
  key: string
  title: string
  items: SourceImportSupportEntry[]
  preview: string
  hint: string
}

const sourceImportProtocolTargets: Partial<Record<ImportKind, string[]>> = {
  devweb: ['HTTP', 'WebSocket', 'gRPC unary (review required)'],
  openapi: ['HTTP / REST'],
  raml: ['HTTP / REST'],
  postman: ['HTTP'],
  asyncapi: ['Kafka', 'JMS Solace for AMQP/JMS servers', 'MQTT review-only'],
  k6: ['HTTP subset'],
  cloudtest: ['Review-only until fixture-proven'],
  'eventhubs-capture': ['Event Hubs'],
  'broker-config': ['JMS Solace', 'JMS IBM MQ review/mock'],
}

function sourceReviewForImport(result?: ProtocolImportResult | null, script?: ScriptDefinition | null): SourceReview | null {
  const metadataReview = script?.metadata?.import?.preserved?.source_review as SourceReview | undefined
  const review = result?.source_review || metadataReview
  if (!review || (!Array.isArray(review.source_files) && !Array.isArray(review.mappings))) return null
  return review
}

function uploadNounForKind(kind: ImportKind): string {
  switch (kind) {
    case 'devweb':
      return 'a DevWeb script'
    case 'har':
      return 'a HAR file'
    case 'recorder-export':
      return 'a Chrome Recorder export or Playwright script'
    case 'openapi':
      return 'an OpenAPI document'
    case 'raml':
      return 'a RAML document'
    case 'postman':
      return 'a Postman collection'
    case 'asyncapi':
      return 'an AsyncAPI document'
    case 'k6':
      return 'a k6 script'
    case 'cloudtest':
      return 'a CloudTest XML or ZIP export'
    case 'eventhubs-capture':
      return 'an Event Hubs Capture file'
    case 'broker-config':
      return 'a broker config file'
    default:
      return 'a WSDL document'
  }
}

function scriptNamePlaceholder(kind: ImportKind): string {
  switch (kind) {
    case 'devweb':
      return 'Imported DevWeb Script'
    case 'har':
      return 'Imported HAR Script'
    case 'postman':
      return 'Imported Postman Script'
    case 'raml':
      return 'Imported RAML Script'
    case 'asyncapi':
      return 'Imported AsyncAPI Script'
    case 'wsdl':
      return 'Imported WSDL Script'
    case 'k6':
      return 'Imported k6 Script'
    case 'cloudtest':
      return 'Imported CloudTest Clip'
    case 'eventhubs-capture':
      return 'Imported Event Hubs Capture Script'
    case 'broker-config':
      return 'Imported Broker Config Script'
    default:
      return 'Imported API Script'
  }
}

function generatedFromLabel(kind: ImportKind): string {
  switch (kind) {
    case 'devweb':
      return 'DevWeb source'
    case 'har':
      return 'HAR entries'
    case 'recorder-export':
      return 'recorded browser steps'
    case 'openapi':
      return 'OpenAPI operations'
    case 'raml':
      return 'RAML resources'
    case 'postman':
      return 'Postman requests'
    case 'asyncapi':
      return 'AsyncAPI operations'
    case 'k6':
      return 'k6 HTTP calls'
    case 'cloudtest':
      return 'CloudTest export elements'
    case 'eventhubs-capture':
      return 'captured Event Hubs events'
    case 'broker-config':
      return 'broker destinations'
    default:
      return 'WSDL operations'
  }
}

function sourceImportCoverageKey(entry: SourceImportSupportEntry): string {
  const status = (entry.status || '').toLowerCase()
  const executionStatus = (entry.execution_status || '').toLowerCase()
  if (status === 'unsupported' || executionStatus === 'unsupported') return 'unsupported'
  if (status === 'review_only' || executionStatus === 'not_executable') return 'review_only'
  if (status === 'partial' || entry.review_required || executionStatus === 'review_required') return 'review_required'
  return 'supported'
}

function sourceImportCoverageTitle(key: string): string {
  switch (key) {
    case 'supported':
      return 'Supported'
    case 'review_required':
      return 'Review required'
    case 'review_only':
      return 'Review-only'
    case 'unsupported':
      return 'Unsupported'
    default:
      return key
  }
}

function sourceImportCoverageHint(key: string): string {
  switch (key) {
    case 'supported':
      return 'Constructs that can be converted into runnable APEX Load script content.'
    case 'review_required':
      return 'Constructs that are converted or preserved but need operator review before replay or promotion.'
    case 'review_only':
      return 'Constructs that are preserved for review and are not emitted as executable replay logic.'
    case 'unsupported':
      return 'Constructs that are reported for visibility but do not currently have an import path.'
    default:
      return 'Import support classification from the code-owned support matrix.'
  }
}

function summarizeSourceImportCoverage(entries: SourceImportSupportEntry[]): SourceImportCoverageSection[] {
  const order = ['supported', 'review_required', 'review_only', 'unsupported']
  return order
    .map(key => {
      const items = entries.filter(entry => sourceImportCoverageKey(entry) === key)
      return {
        key,
        title: sourceImportCoverageTitle(key),
        items,
        preview: items.map(item => item.construct).join(' • '),
        hint: sourceImportCoverageHint(key),
      }
    })
    .filter(section => section.items.length > 0)
}

// useReplaceTargetPicker owns the paged "which script does this replace?"
// browser. OpenAPI and Postman are the two importers with replace semantics and
// each carried a character-identical copy: six state slots, a reset effect on
// kind/project change, and a paged listScripts effect.
function useReplaceTargetPicker(
  api: ReturnType<typeof useApi>,
  kind: ImportKind,
  projectId: string | undefined,
  active: boolean,
) {
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [scriptId, setScriptId] = useState('')
  const [search, setSearch] = useState('')
  const [offset, setOffset] = useState(0)
  const [count, setCount] = useState(0)

  // Changing importer or project invalidates both the chosen target and the
  // page it was chosen from.
  useEffect(() => {
    setScripts([])
    setScriptId('')
    setSearch('')
    setOffset(0)
    setCount(0)
  }, [kind, projectId])

  useEffect(() => {
    if (!active) return
    let cancelled = false
    setScripts([])
    api.listScripts(projectId, undefined, {
      limit: REPLACE_TARGET_PAGE_SIZE,
      offset,
      search,
    })
      .then(response => {
        if (!cancelled) {
          setScripts(response.scripts || [])
          setCount(response.count || 0)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setScripts([])
          setCount(0)
        }
      })
    return () => { cancelled = true }
  }, [active, api, offset, projectId, search])

  return { scripts, scriptId, setScriptId, search, setSearch, offset, setOffset, count }
}

function sourceImportSupportHasLimitations(support: SourceImportSupportState | null): support is SourceImportSupportState {
  if (!support || support.entries.length === 0) return false
  return support.notes.length > 0 || support.entries.some(entry => (
    sourceImportCoverageKey(entry) !== 'supported' || Boolean(entry.review_required) || Boolean(entry.notes)
  ))
}

function SourceImportCoveragePanel({ kind, support }: { kind: ImportKind; support: SourceImportSupportState }) {
  const sections = summarizeSourceImportCoverage(support.entries)
  const protocolTargets = sourceImportProtocolTargets[kind] || []
  if (sections.length === 0) return null
  return (
    <ImportCoverageButton
      title="Import Coverage"
      subtitle="Supported protocols and limitations"
      triggerAriaLabel={`${copy[kind].title} coverage`}
    >
      {protocolTargets.length > 0 && (
        <div className="source-import-protocol-card">
          <span className="source-import-coverage-label">Supported protocols</span>
          <strong>{protocolTargets.join(' • ')}</strong>
        </div>
      )}
      <div className="source-import-coverage-grid">
        {sections.map(section => (
          <div className={`source-import-coverage-card coverage-${section.key}`} key={section.key}>
            <span className="source-import-coverage-label">{section.title}</span>
            <strong>{section.items.length} {section.items.length === 1 ? 'construct' : 'constructs'}</strong>
            <small>{section.preview}</small>
          </div>
        ))}
      </div>
      <div className="source-import-coverage-details">
        <h4>Coverage Details</h4>
        {sections.map(section => (
          <details key={section.key} data-coverage={section.key}>
            <summary>{section.title} ({section.items.length})</summary>
            <p className="source-import-coverage-hint">{section.hint}</p>
            <ul>
              {section.items.map(entry => (
                <li key={entry.id}>
                  <strong>{entry.construct}</strong>
                  {entry.notes ? <> — {entry.notes}</> : null}
                </li>
              ))}
            </ul>
          </details>
        ))}
        {support.notes.length > 0 && (
          <details data-coverage="notes">
            <summary>Importer Notes ({support.notes.length})</summary>
            <ul>{support.notes.map((note, index) => <li key={`source-import-note-${index}`}>{note}</li>)}</ul>
          </details>
        )}
      </div>
    </ImportCoverageButton>
  )
}

function salesforceReviewForImport(result?: ProtocolImportResult | null, script?: ScriptDefinition | null): SalesforceReview | null {
  if (!result && !script) return null
  const summaryReview = (result as SalesforceImportResult | undefined)?.summary?.salesforce_review
  const metadataReview = script?.metadata?.import?.preserved?.salesforce_review as SalesforceReview | undefined
  return summaryReview || metadataReview || null
}

// MuleSoft/Agentforce reviews are attached as preserved import metadata by the
// relevant importers (RAML/OpenAPI/Postman → mulesoft+agentforce; Salesforce → agentforce).
function preservedReviewForImport<T extends { detected: boolean }>(script: ScriptDefinition | null | undefined, key: string): T | null {
  const review = script?.metadata?.import?.preserved?.[key] as T | undefined
  return review && review.detected ? review : null
}

function attachRecorderReplayEvidence(script: ScriptDefinition, report: BrowserReplayReport | null, status: string, draftOverride: boolean, replaySessionId: string): ScriptDefinition {
  return {
    ...script,
    metadata: {
      ...script.metadata,
      import: {
        ...script.metadata?.import,
        preserved: {
          ...(script.metadata?.import?.preserved || {}),
          replay_validation: {
            status,
            success: report?.success ?? false,
            replay_session_id: report?.replay_session_id || replaySessionId || undefined,
            completed_at: report?.completed_at,
            events: report?.events?.length ?? 0,
            repair_suggestions: report?.repair_suggestions?.length ?? 0,
            draft_override: draftOverride,
          },
        },
      },
    },
  }
}

function scriptsFromImportResult(result: BrowserRecordingImportResult | null): ScriptDefinition[] {
  if (!result) return []
  return result.scripts?.length ? result.scripts : result.script ? [result.script] : []
}

function promoteDraftSelector(script: ScriptDefinition, actionIndex: number, selector: string, candidate?: SelectorCandidate): ScriptDefinition {
  if (!script.actions?.[actionIndex]) return script
  const actions = script.actions.map((action, index) => {
    if (index !== actionIndex) return action
    const metadata = { ...(action.metadata || {}) }
    const currentCandidates = Array.isArray(metadata.source_selector_candidates)
      ? metadata.source_selector_candidates.filter((item): item is SelectorCandidate => (
        !!item && typeof item === 'object' && typeof (item as SelectorCandidate).value === 'string'
      ))
      : []
    const promoted: SelectorCandidate = candidate || {
      kind: selector.startsWith('//') ? 'xpath' : 'css',
      value: selector,
      confidence: 100,
      reason: 'Replay self-heal promotion.',
    }
    const nextCandidates = [promoted, ...currentCandidates.filter(item => item.value !== promoted.value)]
    metadata.source_selector_candidates = nextCandidates
    metadata.source_selector_fallbacks = nextCandidates.slice(1)
    metadata.source_selector_confidence = promoted.confidence
    metadata.selector_promoted_from_import_review = true
    return {
      ...action,
      selector: promoted.value,
      target: action.target === action.selector || !action.target ? promoted.value : action.target,
      metadata,
    }
  })
  return { ...script, actions }
}

function findActionIndexForReplaySuggestion(script: ScriptDefinition | null, suggestion: ReplayRepairSuggestion): number {
  if (!script?.actions) return -1
  const fields = suggestion.fields || {}
  const seq = fields.recorder_step_seq
  if (seq) {
    const seqString = String(seq)
    const match = script.actions.findIndex(action => String(action.metadata?.recorder_step_seq || '') === seqString)
    if (match >= 0) return match
  }
  if (suggestion.action) {
    const match = script.actions.findIndex(action => action.name === suggestion.action)
    if (match >= 0) return match
  }
  return -1
}

type RecorderStepMapping = {
  source_index?: number
  source_location?: string
  source_step_type?: string
  action_index?: number
  name?: string
  type?: string
  target_path?: string
  recorder_step_seq?: number
  disposition?: string
  reason?: string
}

function recorderStepMappingsFromSummary(summary?: Record<string, unknown>): RecorderStepMapping[] {
  const raw = summary?.step_mapping
  if (!Array.isArray(raw)) return []
  return raw
    .filter((item): item is Record<string, unknown> => !!item && typeof item === 'object')
    .map(item => ({
      source_index: typeof item.source_index === 'number' ? item.source_index : undefined,
      source_location: typeof item.source_location === 'string' ? item.source_location : undefined,
      source_step_type: typeof item.source_step_type === 'string' ? item.source_step_type : undefined,
      action_index: typeof item.action_index === 'number' ? item.action_index : typeof item.index === 'number' ? item.index : undefined,
      name: typeof item.name === 'string' ? item.name : undefined,
      type: typeof item.type === 'string' ? item.type : undefined,
      target_path: typeof item.target_path === 'string' ? item.target_path : undefined,
      recorder_step_seq: typeof item.recorder_step_seq === 'number' ? item.recorder_step_seq : undefined,
      disposition: typeof item.disposition === 'string' ? item.disposition : 'mapped',
      reason: typeof item.reason === 'string' ? item.reason : undefined,
    }))
}

function RecorderStepMappingReview({ summary }: { summary?: Record<string, unknown> }) {
  const mappings = recorderStepMappingsFromSummary(summary)
  if (mappings.length === 0) return null
  const degraded = mappings.filter(item => item.disposition === 'degraded').length
  const unsupported = mappings.filter(item => item.disposition === 'unsupported').length
  return (
    <div className="sample-import-review-block recorder-step-mapping">
      <h4>Recorder Step Mapping</h4>
      <p>
        {mappings.length} source step(s) reviewed · {degraded} degraded · {unsupported} unsupported
      </p>
      <div className="recorder-step-mapping-table-wrap">
        <table className="recorder-step-mapping-table">
          <thead>
            <tr>
              <th>Source</th>
              <th>Disposition</th>
              <th>Generated Action</th>
              <th>Reason</th>
            </tr>
          </thead>
          <tbody>
            {mappings.map((mapping, index) => (
              <tr key={`${mapping.source_location || mapping.source_index || index}-${mapping.type || 'none'}`} className={`recorder-step-${mapping.disposition || 'mapped'}`}>
                <td>
                  <strong>{mapping.source_location || `step ${mapping.source_index ?? index + 1}`}</strong>
                  {mapping.source_step_type && <small>{mapping.source_step_type}</small>}
                </td>
                <td><span>{mapping.disposition || 'mapped'}</span></td>
                <td>
                  {mapping.name || mapping.type ? (
                    <>
                      <strong>{mapping.name || mapping.type}</strong>
                      {mapping.type && <small>{mapping.type}</small>}
                      {mapping.target_path && <code>{mapping.target_path}</code>}
                    </>
                  ) : (
                    <span className="sample-import-muted">No generated action</span>
                  )}
                </td>
                <td>{mapping.reason || 'Mapped directly.'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

const endpointTotal = salesforceEndpointTotal
const IMPORT_DRAFT_EXPIRED_MESSAGE = 'This credential-preserving import draft has expired. Re-run the import before saving, editing, or changing it.'

let asyncAPIBatchSaveOperationSequence = 0
let genericImportBatchSaveOperationSequence = 0

function createAsyncAPIBatchSaveOperationID(): string {
  const randomID = createReplaySessionId().trim()
  if (randomID) return `asyncapi-batch-${randomID}`
  asyncAPIBatchSaveOperationSequence += 1
  return `asyncapi-batch-${Date.now().toString(36)}-${asyncAPIBatchSaveOperationSequence.toString(36)}`
}

function createGenericImportBatchSaveOperationID(): string {
  const randomID = createReplaySessionId().trim()
  if (randomID) return `import-batch-${randomID}`
  genericImportBatchSaveOperationSequence += 1
  return `import-batch-${Date.now().toString(36)}-${genericImportBatchSaveOperationSequence.toString(36)}`
}

function genericImportSourceProfile(kind: ImportKind) {
  switch (kind) {
    case 'devweb':
    case 'har':
    case 'k6':
    case 'cloudtest':
    case 'raml':
    case 'wsdl':
      return kind
    case 'recorder-export':
      return 'recorder_export' as const
    case 'eventhubs-capture':
      return 'eventhubs_capture' as const
    case 'broker-config':
      return 'broker_config' as const
    default:
      return null
  }
}

function importDraftHasExpired(result: ProtocolImportResult | null | undefined): boolean {
  return importDraftExpiryStatus(result?.import_draft_expires_at)?.expired === true
}

export default function ProtocolAssetImport({ kind, initialFile, initialMode, projectId, folderId, onImported, onEdit, onClose, onOperationBusyChange }: Props) {
  const api = useApi()
  const sourceSecretSessions = useSourceSecretSessionLifecycle(api.discardImportSecretSession)
  const convertTask = useAsyncTask<ProtocolImportResult>()
  const saveTask = useAsyncTask<StoredScript>()
  const result = convertTask.value
  const latestResultRef = useRef<ProtocolImportResult | null>(result || null)
  const protectedCommitOverridesRef = useRef<ProtectedImportCommitOverrides>({
    sessionId: '',
    secretBindings: {},
    parameterizationSelectedIds: [],
  })
  const credentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const {
    generation: reviewGeneration,
    generationRef: reviewGenerationRef,
    advance: advanceReviewGeneration,
  } = useImportReviewGeneration()
  useEffect(() => () => {
    // Route/project transitions can unmount the importer without invoking its
    // Back button. Invalidate captured save scopes synchronously so a
    // sequential Save All cannot start another commit after unmount.
    reviewGenerationRef.current += 1
  }, [reviewGenerationRef])
  const reviewScope = `${reviewGeneration}:${projectId || ''}`
  const { savedResult, publish: publishSaved, openSaved } = useImportSaveOutcome(reviewScope, onImported)
  const reviewProjectIdRef = useRef(projectId)
  reviewProjectIdRef.current = projectId
  const [selectedScriptIndex, setSelectedScriptIndex] = useState(0)
  const selectedScriptIndexRef = useRef(selectedScriptIndex)
  selectedScriptIndexRef.current = selectedScriptIndex
  const asyncAPIScriptButtonRefs = useRef(new Map<number, HTMLButtonElement>())
  const pendingAsyncAPIScriptFocusRef = useRef<number | null>(null)
  // The credentials card exposes only non-sensitive CTA/validation metadata;
  // write-only values never cross the component boundary.
  const [preparedCredentialSummary, setPreparedCredentialSummary] = useState<(PreparedSecretBindings & { scriptIndex: number }) | null>(null)
  const handleCredentialsPreparedChange = useCallback((prepared: PreparedSecretBindings | null) => {
    setPreparedCredentialSummary(prepared ? {
      configureLaterCount: prepared.configureLaterCount,
      valid: prepared.valid ?? true,
      selectedBindingCount: prepared.selectedBindingCount,
      scriptIndex: selectedScriptIndexRef.current,
    } : null)
  }, [])
  const saveAllInFlightRef = useRef(false)
  const asyncAPIBatchSaveOperationRef = useRef<{
    result: ProtocolImportResult
    pendingKey: string
    projectId?: string
    folderId: string | null
    operationId: string
  } | null>(null)
  const genericImportBatchSaveOperationRef = useRef<{
    result: ProtocolImportResult
    scriptIndex: number
    kind: ImportKind
    projectId?: string
    folderId: string | null
    operationId: string
  } | null>(null)
  const saveScriptInFlightRef = useRef<{
    generation: number
    projectId: string | undefined
    scriptIndex: number
    result: ProtocolImportResult
  } | null>(null)
  // useAsyncTask.reset() intentionally cancels a logical task generation. It
  // cannot, however, cancel an HTTP persistence request that already crossed
  // the wire. Keep a separate physical fence so source/project invalidation
  // never reports this surface idle while that request is still reconciling.
  const [persistenceInProgress, setPersistenceInProgress] = useState(false)
  const [saveAllPreparing, setSaveAllPreparing] = useState(false)
  const [importDraftExpiryGeneration, setImportDraftExpiryGeneration] = useState(0)
  const internalReviewBusyRef = useRef(false)
  const [settingsEditing, setSettingsEditing] = useState(false)
  const [internalReviewBusy, setInternalReviewBusy] = useState(false)
  const handleInternalReviewBusyChange = useCallback((busy: boolean) => {
    internalReviewBusyRef.current = busy
    setInternalReviewBusy(busy)
  }, [])
  const [file, setFile] = useState<File | null>(null)
  const sourceFileRef = useRef<File | null>(file)
  sourceFileRef.current = file
  const [postmanEnvironmentFile, setPostmanEnvironmentFile] = useState<File | null>(null)
  const [postmanSanitizeSensitive, setPostmanSanitizeSensitive] = useState(true)
  const [postmanIncludeAssertions, setPostmanIncludeAssertions] = useState(true)
  const [postmanUseDataSource, setPostmanUseDataSource] = useState(false)
  const [postmanThinkTimeMs, setPostmanThinkTimeMs] = useState(1000)
  const [asyncAPIBrokers, setAsyncAPIBrokers] = useState('')
  const [asyncAPIChannels, setAsyncAPIChannels] = useState('')
  const [asyncAPIMock, setAsyncAPIMock] = useState(true)
  const [asyncAPIIncludeAssertions, setAsyncAPIIncludeAssertions] = useState(true)
  const [asyncAPIThinkTimeMs, setAsyncAPIThinkTimeMs] = useState(1000)
  const [asyncAPISerialization, setAsyncAPISerialization] = useState('')
  const [asyncAPISchemaRegistryURL, setAsyncAPISchemaRegistryURL] = useState('')
  const [asyncAPISchemaID, setAsyncAPISchemaID] = useState('')
  const [asyncAPISubject, setAsyncAPISubject] = useState('')
  const asyncAPISavedScriptsRef = useRef<Record<number, StoredScript>>({})
  const [asyncAPISavedScripts, setAsyncAPISavedScripts] = useState<Record<number, StoredScript>>({})
  const settledSourceSecretSelectionsRef = useRef(new Map<string, SecretBindingSelection[]>())
  const asyncAPICredentialBatchRequiredRef = useRef(false)
  const resetAsyncAPISavedScripts = useCallback(() => {
    asyncAPISavedScriptsRef.current = {}
    setAsyncAPISavedScripts({})
  }, [])
  const [, setConversionScope] = useState(0)
  const [secondaryHARFile, setSecondaryHARFile] = useState<File | null>(null)
  const [auraSourceFocus, setAuraSourceFocus] = useState<ImportSourceIssueFocus | null>(null)
  const [name, setName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [soapVersion, setSoapVersion] = useState('1.1')
  const [includeStaticAssets, setIncludeStaticAssets] = useState(false)
  const [redactSensitive, setRedactSensitive] = useState(true)
  const [agentforceReplayReady, setAgentforceReplayReady] = useState(false)
  const [harSalesforceNative, setHARSalesforceNative] = useState(initialMode !== 'force_generic')
  const [retentionDays, setRetentionDays] = useState(7)
  const [messageError, setMessageError] = useState('')
  const [reviewMode, setReviewMode] = useState<'conversion' | 'validation'>('conversion')
  const replayTask = useAsyncTask<BrowserReplayStartResponse>()
  const [recorderReplaySessionId, setRecorderReplaySessionId] = useState('')
  const recorderReplaySessionIdRef = useRef('')
  const recorderReplayStartRef = useRef<{
    generation: number
    projectId: string | undefined
    scriptIndex: number
    script: ScriptDefinition
  } | null>(null)
  const [recorderReplayStatus, setRecorderReplayStatus] = useState<'idle' | 'running' | 'completed' | 'failed' | 'stopped'>('idle')
  const [recorderReplayReport, setRecorderReplayReport] = useState<BrowserReplayReport | null>(null)
  const [allowRecorderDraftSave, setAllowRecorderDraftSave] = useState(false)
  const recorderReplayStatusRef = useRef(recorderReplayStatus)
  const recorderReplayReportRef = useRef<BrowserReplayReport | null>(recorderReplayReport)
  const allowRecorderDraftSaveRef = useRef(allowRecorderDraftSave)
  recorderReplayStatusRef.current = recorderReplayStatus
  recorderReplayReportRef.current = recorderReplayReport
  allowRecorderDraftSaveRef.current = allowRecorderDraftSave
  const [recorderDraftNotice, setRecorderDraftNotice] = useState('')
  const [sourceImportSupport, setSourceImportSupport] = useState<SourceImportSupportState | null>(null)
  const [openAPISaveMode, setOpenAPISaveMode] = useState<'create' | 'replace'>('create')
  const [openAPIReplaceMode, setOpenAPIReplaceMode] = useState<'full' | 'merge_by_operation_id'>('full')
  const openAPIReplaceTarget = useReplaceTargetPicker(api, kind, projectId, kind === 'openapi' && openAPISaveMode === 'replace')

  useEffect(() => {
    latestResultRef.current = result || null
    const sessionId = activeProtectedImportSessionId(result) || ''
    if (protectedCommitOverridesRef.current.sessionId !== sessionId) {
      protectedCommitOverridesRef.current = {
        sessionId,
        secretBindings: {},
        parameterizationSelectedIds: [],
      }
    }
  }, [result])
  useEffect(() => {
    const expiry = Date.parse(result?.import_draft_expires_at || '')
    if (!Number.isFinite(expiry) || expiry <= Date.now()) return
    const timeout = window.setTimeout(
      () => setImportDraftExpiryGeneration(current => current + 1),
      Math.min(expiry - Date.now() + 10, 2_147_483_647),
    )
    return () => window.clearTimeout(timeout)
  }, [importDraftExpiryGeneration, result?.import_draft_expires_at])
  const [postmanSaveMode, setPostmanSaveMode] = useState<'create' | 'replace'>('create')
  const postmanReplaceTarget = useReplaceTargetPicker(api, kind, projectId, kind === 'postman' && postmanSaveMode === 'replace')
  const openAPISaveTargetRef = useRef({
    mode: openAPISaveMode,
    targetScriptId: openAPIReplaceTarget.scriptId,
    replaceMode: openAPIReplaceMode,
  })
  const postmanSaveTargetRef = useRef({
    mode: postmanSaveMode,
    targetScriptId: postmanReplaceTarget.scriptId,
  })
  const captureProtectedSaveTarget = useCallback(() => {
    openAPISaveTargetRef.current = {
      mode: openAPISaveMode,
      targetScriptId: openAPIReplaceTarget.scriptId,
      replaceMode: openAPIReplaceMode,
    }
    postmanSaveTargetRef.current = {
      mode: postmanSaveMode,
      targetScriptId: postmanReplaceTarget.scriptId,
    }
  }, [openAPISaveMode, openAPIReplaceMode, openAPIReplaceTarget.scriptId, postmanReplaceTarget.scriptId, postmanSaveMode])
  const labels = copy[kind]
  const resetConvertTask = convertTask.reset
  const resetSaveTask = saveTask.reset
  const operationBusy = convertTask.pending
    || saveTask.pending
    || persistenceInProgress
    || saveAllPreparing
    || internalReviewBusy
    || replayTask.pending
  const sourceMutationLocked = settingsEditing || persistenceInProgress || saveTask.pending || saveAllPreparing
  const saveTargetLocked = sourceMutationLocked || internalReviewBusy
  const projectScopeRequired = kind === 'har' && (!projectId || projectId === 'unassigned')
  // A credential action can await several API calls before onSave runs. Freeze
  // the replace target at the moment that action becomes busy so the eventual
  // commit cannot close over a target selected after credential preparation.
  if (!internalReviewBusy && !persistenceInProgress && !saveTask.pending) {
    openAPISaveTargetRef.current = {
      mode: openAPISaveMode,
      targetScriptId: openAPIReplaceTarget.scriptId,
      replaceMode: openAPIReplaceMode,
    }
    postmanSaveTargetRef.current = {
      mode: postmanSaveMode,
      targetScriptId: postmanReplaceTarget.scriptId,
    }
  }
  useEffect(() => {
    onOperationBusyChange?.(operationBusy)
    return () => onOperationBusyChange?.(false)
  }, [onOperationBusyChange, operationBusy])
  const invalidateConversionSource = useCallback(() => {
    sourceSecretSessions.clear('conversion')
    settledSourceSecretSelectionsRef.current.clear()
    asyncAPICredentialBatchRequiredRef.current = false
    protectedCommitOverridesRef.current = {
      sessionId: '',
      secretBindings: {},
      parameterizationSelectedIds: [],
    }
    advanceReviewGeneration()
    recorderReplaySessionIdRef.current = ''
    recorderReplayStatusRef.current = 'idle'
    recorderReplayReportRef.current = null
    allowRecorderDraftSaveRef.current = false
    setRecorderReplaySessionId('')
    setRecorderReplayStatus('idle')
    setRecorderReplayReport(null)
    setAllowRecorderDraftSave(false)
    setRecorderDraftNotice('')
    resetConvertTask()
    if (!saveScriptInFlightRef.current && !saveAllInFlightRef.current) resetSaveTask()
    if (!saveAllInFlightRef.current) setSaveAllPreparing(false)
    internalReviewBusyRef.current = false
    setInternalReviewBusy(false)
    setPreparedCredentialSummary(null)
    resetAsyncAPISavedScripts()
    setMessageError('')
  }, [advanceReviewGeneration, resetAsyncAPISavedScripts, resetConvertTask, resetSaveTask, sourceSecretSessions])
  const previousProjectIdRef = useRef(projectId)
  useEffect(() => {
    if (previousProjectIdRef.current === projectId) return
    previousProjectIdRef.current = projectId
    invalidateConversionSource()
  }, [invalidateConversionSource, projectId])
  const supportsAgentforceTransform = kind === 'openapi' || kind === 'raml' || kind === 'postman' || kind === 'har'
  // Event Hubs Capture and Broker Config converters read neither a script name nor a
  // base URL, so do not render inputs that would silently do nothing.
  const supportsScriptName = kind !== 'eventhubs-capture' && kind !== 'broker-config'
  const supportsBaseURL = kind !== 'asyncapi' && kind !== 'eventhubs-capture' && kind !== 'broker-config'

  useEffect(() => {
    if (!initialFile) return
    invalidateConversionSource()
    setFile(initialFile)
  }, [initialFile, invalidateConversionSource])

  useEffect(() => {
    // Clear before fetching, not on resolve. One slot now serves every kind, so
    // leaving the previous format's entries in place would render them under the
    // new format's heading until its own matrix arrived.
    setSourceImportSupport(null)
    const fetchSupportMatrix = sourceImportSupportFetchers[kind]
    if (!fetchSupportMatrix) return
    let cancelled = false
    fetchSupportMatrix(api)
      .then(response => { if (!cancelled) setSourceImportSupport({ entries: response.support_matrix || [], notes: response.notes || [] }) })
      .catch(() => { if (!cancelled) setSourceImportSupport(null) })
    return () => { cancelled = true }
  }, [api, kind])

  useEffect(() => {
    const count = result?.scripts?.length || (result?.script ? 1 : 0)
    if (count === 0) {
      setSelectedScriptIndex(0)
    } else if (selectedScriptIndex >= count) {
      setSelectedScriptIndex(0)
    }
  }, [result, selectedScriptIndex])

  useEffect(() => {
    const pendingIndex = pendingAsyncAPIScriptFocusRef.current
    if (pendingIndex !== selectedScriptIndex) return
    pendingAsyncAPIScriptFocusRef.current = null
    asyncAPIScriptButtonRefs.current.get(pendingIndex)?.focus()
  }, [selectedScriptIndex])

  useEffect(() => {
    recorderReplaySessionIdRef.current = ''
    recorderReplayStatusRef.current = 'idle'
    recorderReplayReportRef.current = null
    allowRecorderDraftSaveRef.current = false
    setRecorderReplaySessionId('')
    setRecorderReplayStatus('idle')
    setRecorderReplayReport(null)
    setAllowRecorderDraftSave(false)
    setRecorderDraftNotice('')
  }, [selectedScriptIndex, file])

  const handleConvert = async (mode: 'conversion' | 'validation' = 'conversion', overrides?: { salesforceNative?: boolean }) => {
    if (!file) {
      setMessageError(`Please upload ${uploadNounForKind(kind)}.`)
      return
    }
    if (projectScopeRequired) {
      setMessageError('Select a project from the workspace context before inspecting HAR traffic. HAR review stores protected source evidence inside that project.')
      return
    }
    resetAsyncAPISavedScripts()
    const sourceGeneration = advanceReviewGeneration()
    const sourceProjectId = reviewProjectIdRef.current
    const sourceFile = file
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && sourceFileRef.current === sourceFile
    setMessageError('')
    setReviewMode(mode)
    recorderReplaySessionIdRef.current = ''
    recorderReplayStatusRef.current = 'idle'
    recorderReplayReportRef.current = null
    allowRecorderDraftSaveRef.current = false
    setRecorderReplaySessionId('')
    setRecorderReplayStatus('idle')
    setRecorderReplayReport(null)
    setAllowRecorderDraftSave(false)
    setRecorderDraftNotice('')
    try {
      await convertTask.run(async () => {
        async function guardResponse<T extends ProtocolImportResult>(responsePromise: Promise<T>): Promise<T> {
          const response = await responsePromise
          if (!requestIsCurrent()) {
            sourceSecretSessions.discardUnaccepted(sourceProjectId, response.source_secret_session_id)
            throw new Error('The import source changed while conversion was running. Review the current input and try again.')
          }
          sourceSecretSessions.replace('conversion', sourceProjectId, response.source_secret_session_id)
          setConversionScope(sourceGeneration)
          return response
        }
        const options = {
          name: name || undefined,
          base_url: baseURL || undefined,
          project_id: sourceProjectId,
        }
        if (kind === 'devweb') return guardResponse(api.importDevWeb(file, options))
        if (kind === 'k6') return guardResponse(api.importK6(file, options))
        if (kind === 'cloudtest') return guardResponse(api.importCloudTest(file, { project_id: sourceProjectId }))
        if (kind === 'eventhubs-capture') return guardResponse(api.importEventHubsCapture(file, { project_id: sourceProjectId }))
        if (kind === 'broker-config') return guardResponse(api.importBrokerConfig(file, { project_id: sourceProjectId }))
        if (kind === 'har') return guardResponse(api.importHAR(file, {
          ...options,
          include_static_assets: includeStaticAssets,
          secondary_file: secondaryHARFile || undefined,
          artifact_mode: secondaryHARFile ? 'design_har_pair' : 'design_har',
          retention_days: retentionDays,
          redact_sensitive: redactSensitive,
          salesforce_native: overrides?.salesforceNative ?? harSalesforceNative,
          agentforce_transform: agentforceReplayReady,
        }))
        if (kind === 'recorder-export') return guardResponse(mode === 'validation' ? api.validateBrowserRecordingImport(file, options) : api.importBrowserRecording(file, options))
        if (kind === 'openapi') return guardResponse(api.importOpenAPI(file, { ...options, agentforce_transform: agentforceReplayReady }))
        if (kind === 'raml') return guardResponse(api.importRAML(file, { ...options, agentforce_transform: agentforceReplayReady }))
        if (kind === 'postman') {
          const postmanOptions = {
            ...options,
            environment_file: postmanEnvironmentFile || undefined,
            include_assertions: postmanIncludeAssertions,
            sanitize_sensitive: postmanSanitizeSensitive,
            use_parameter_data_source: postmanUseDataSource,
            default_think_time_ms: postmanThinkTimeMs,
            agentforce_transform: agentforceReplayReady,
          }
          return guardResponse(mode === 'validation' ? api.validatePostmanImport(file, postmanOptions) : api.importPostman(file, postmanOptions))
        }
        if (kind === 'asyncapi') return guardResponse(api.importAsyncAPI(file, {
          name: options.name,
          project_id: options.project_id,
          brokers: asyncAPIBrokers || undefined,
          channels: asyncAPIChannels || undefined,
          mock: asyncAPIMock,
          include_assertions: asyncAPIIncludeAssertions,
          default_think_time_ms: asyncAPIThinkTimeMs,
          serialization: asyncAPISerialization || undefined,
          schema_registry_url: asyncAPISchemaRegistryURL || undefined,
          schema_id: asyncAPISchemaID ? Math.max(0, Number(asyncAPISchemaID) || 0) : undefined,
          subject: asyncAPISubject || undefined,
        }))
        return guardResponse(api.importWSDL(file, { ...options, soap_version: soapVersion }))
      }, { clearValue: true })
    } catch (error: unknown) {
      if (requestIsCurrent()) {
        sourceSecretSessions.clear('conversion')
        setMessageError(getErrorMessage(error, `${labels.title} failed`))
      }
    }
  }

  const handleHARSalesforceNativeConvert = async (salesforceNative: boolean) => {
    setHARSalesforceNative(salesforceNative)
    await handleConvert('conversion', { salesforceNative })
  }

  const stats = result?.stats as Record<string, unknown> | undefined
  const importedScripts = result?.scripts?.length ? result.scripts : result?.script ? [result.script] : []
  // The converted result is the sole reviewed-script state. Selector repair,
  // credential binding, parameterization, runtime edits, Save, and Edit all
  // read and update this same canonical snapshot.
  const generatedScripts = importedScripts
  const safeAsyncAPISelectedScriptIndex = Math.min(selectedScriptIndex, Math.max(0, generatedScripts.length - 1))
  const asyncAPIScriptPageStart = Math.floor(safeAsyncAPISelectedScriptIndex / ASYNCAPI_SCRIPT_PAGE_SIZE) * ASYNCAPI_SCRIPT_PAGE_SIZE
  const visibleAsyncAPIScripts = generatedScripts.slice(asyncAPIScriptPageStart, asyncAPIScriptPageStart + ASYNCAPI_SCRIPT_PAGE_SIZE)
  const reviewedScript = generatedScripts[selectedScriptIndex] || generatedScripts[0] || null
  const actionCount = typeof stats?.converted_actions === 'number'
    ? stats.converted_actions
    : reviewedScript?.actions?.length ?? 0
  const asyncAPIMultiScript = kind === 'asyncapi' && generatedScripts.length > 1
  const asyncAPIPendingScriptIndices = asyncAPIMultiScript
    ? generatedScripts.flatMap((_script, index) => asyncAPISavedScripts[index] ? [] : [index])
    : []
  const asyncAPISelectedScriptSaved = asyncAPIMultiScript && !!asyncAPISavedScripts[selectedScriptIndex]
  const selectedSecretBindingRequirements = reviewedScript
    && !asyncAPISelectedScriptSaved
    ? secretBindingRequirementsForScript(result?.secret_binding_requirements, selectedScriptIndex, reviewedScript)
    : []
  const secretBindingRequirements = selectedSecretBindingRequirements
  const asyncAPIArchiveSecretBindingRequirements = asyncAPIMultiScript
    ? result?.secret_binding_requirements ?? []
    : secretBindingRequirements
  if (
    asyncAPIMultiScript
    && asyncAPIArchiveSecretBindingRequirements.some(requirement => requirement.destination_value_preserved !== true)
  ) {
    asyncAPICredentialBatchRequiredRef.current = true
  }
  const asyncAPISelectedNeedsCredentialPreparation = asyncAPIMultiScript
    && secretBindingRequirements.some(requirement => requirement.destination_value_preserved !== true)
  const asyncAPIOtherPendingCredentialRequirements = asyncAPIMultiScript
    ? asyncAPIPendingScriptIndices
      .filter(index => index !== selectedScriptIndex)
      .flatMap(index => secretBindingRequirementsForScript(
        result?.secret_binding_requirements,
        index,
        generatedScripts[index],
      ))
      .filter(requirement => requirement.destination_value_preserved !== true)
    : []
  const asyncAPIPrepareSelectedForBatch = asyncAPISelectedNeedsCredentialPreparation
    && asyncAPIOtherPendingCredentialRequirements.length > 0
  const asyncAPIFinalCredentialBatchSave = asyncAPICredentialBatchRequiredRef.current
    && asyncAPIMultiScript
    && asyncAPIPendingScriptIndices.length > 1
    && asyncAPISelectedNeedsCredentialPreparation
    && asyncAPIOtherPendingCredentialRequirements.length === 0
  const defaultConfigureLaterCount = new Set(secretBindingRequirements
      .filter(requirement => requirement.destination_value_preserved !== true)
      .map(requirement => {
        const name = requirement.suggested_name?.trim()
        if (!name) return ''
        const scope = typeof requirement.script_index === 'number'
          ? `index:${requirement.script_index}`
          : requirement.script_id
            ? `id:${requirement.script_id}`
            : requirement.script_name
              ? `name:${requirement.script_name}`
              : 'unscoped'
        return `${scope}:${name}`
      })
      .filter(Boolean)).size
  const selectedPreparedCredentialSummary = preparedCredentialSummary?.scriptIndex === selectedScriptIndex
    ? preparedCredentialSummary
    : null
  const configureLaterCount = selectedPreparedCredentialSummary?.configureLaterCount ?? defaultConfigureLaterCount
  const preparedCredentialsValid = selectedPreparedCredentialSummary?.valid ?? true
  const asyncAPISelectedSaveLabel = asyncAPIMultiScript
    ? asyncAPIPrepareSelectedForBatch
      ? `Prepare Selected Credentials (${selectedScriptIndex + 1} of ${generatedScripts.length})`
      : asyncAPIFinalCredentialBatchSave
        ? `Save All ${asyncAPIPendingScriptIndices.length} Scripts Atomically`
        : `Save Selected Script (${selectedScriptIndex + 1} of ${generatedScripts.length})`
    : ''
  const asyncAPISaveAllLabel = asyncAPIMultiScript
    ? `${asyncAPIPendingScriptIndices.length === generatedScripts.length
        ? `Save All ${generatedScripts.length} Scripts`
        : `Save Remaining ${asyncAPIPendingScriptIndices.length} Script${asyncAPIPendingScriptIndices.length === 1 ? '' : 's'}`
      }${configureLaterCount > 0 ? ` with ${configureLaterCount} secret${configureLaterCount === 1 ? '' : 's'} to configure` : ''}`
    : ''
  const cloudTestSaveLabel = kind === 'cloudtest' && generatedScripts.length > 1
    ? `Save All ${generatedScripts.length} Clip Scripts Atomically`
    : ''
  const asyncAPISaveAllDisabledReason = asyncAPIMultiScript && asyncAPIOtherPendingCredentialRequirements.length > 0
    ? `${asyncAPIOtherPendingCredentialRequirements.length} credential ${asyncAPIOtherPendingCredentialRequirements.length === 1 ? 'choice belongs' : 'choices belong'} to another generated script. Select each affected script and prepare its named credentials; no script is saved until the atomic batch is ready.`
    : !preparedCredentialsValid
      ? 'Enter valid secret reference names for the selected generated script before saving.'
      : ''
  const scriptImportSaveDisabledReason = asyncAPISelectedScriptSaved
    ? 'This generated script has already been saved. Select an unsaved generated script to continue.'
    : asyncAPIMultiScript && asyncAPIOtherPendingCredentialRequirements.length > 0 && !asyncAPISelectedNeedsCredentialPreparation
      ? 'This script has no pending credential choices. Select an affected generated script and prepare its credentials before saving the complete batch.'
    : kind === 'postman' && postmanSaveMode === 'replace' && !postmanReplaceTarget.scriptId
      ? 'Select the existing script that this Postman import should replace.'
      : kind === 'openapi' && openAPISaveMode === 'replace' && !openAPIReplaceTarget.scriptId
        ? 'Select the existing script that this OpenAPI import should replace.'
        : ''
  const asyncAPISelectedSaveDisabled = asyncAPISelectedScriptSaved
    || (asyncAPIMultiScript
      && asyncAPIOtherPendingCredentialRequirements.length > 0
      && !asyncAPISelectedNeedsCredentialPreparation)
  const importDraftExpired = importDraftHasExpired(result)
  const sourceReview = sourceReviewForImport(result, reviewedScript)
  const editRequiresCredentialPreservingSave = credentialPreservingImportDraftRequiresSave(
    result,
    reviewedScript,
    secretBindingRequirements,
  )
  const harSummary: HARImportSummary | undefined = kind === 'har' ? (result as HARImportResult | null | undefined)?.summary : undefined
  const salesforceReview = kind === 'har' ? salesforceReviewForImport(result, reviewedScript) : null
  useEffect(() => { setAuraSourceFocus(null) }, [reviewScope])
  const mulesoftReview = preservedReviewForImport<MulesoftReview>(reviewedScript, 'mulesoft_review')
  const agentforceReview = preservedReviewForImport<AgentforceReview>(reviewedScript, 'agentforce_review')
  const agentforceTransformChanges = (reviewedScript?.metadata?.import?.preserved?.agentforce_transform as AgentforceTransformChange[] | undefined) ?? []
  const ramlSummary = kind === 'raml' ? (result as RAMLImportResult | null | undefined)?.summary : undefined
  const cloudTestResult = kind === 'cloudtest' ? result as CloudTestImportResult | null | undefined : undefined
  const harDiff = harSummary?.har_diff
  const selectedReplayPreflight = reviewedScript?.metadata?.import?.preserved?.replay_preflight as OpenAPIImportResult['preflight'] | undefined
  const replayPreflight = (generatedScripts.length > 1 ? selectedReplayPreflight : result?.preflight || selectedReplayPreflight) || null
  const openAPICorrelations: OpenAPICorrelation[] = kind === 'openapi' ? ((result as OpenAPIImportResult | null | undefined)?.correlations || []) : []

  const handleRunRecorderReplay = async () => {
    if (kind !== 'recorder-export' || !reviewedScript) return
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const sourceScript = reviewedScript
    const sourceScriptIndex = selectedScriptIndex
    const activeStart = recorderReplayStartRef.current
    if (
      activeStart?.generation === sourceGeneration
      && activeStart.projectId === sourceProjectId
      && activeStart.scriptIndex === sourceScriptIndex
      && activeStart.script === sourceScript
    ) return
    const startScope = {
      generation: sourceGeneration,
      projectId: sourceProjectId,
      scriptIndex: sourceScriptIndex,
      script: sourceScript,
    }
    recorderReplayStartRef.current = startScope
    const clientReplaySessionId = createReplaySessionId()
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && selectedScriptIndexRef.current === sourceScriptIndex
      && latestResultRef.current != null
      && scriptsFromImportResult(latestResultRef.current as BrowserRecordingImportResult)[sourceScriptIndex] === sourceScript
    setMessageError('')
    setRecorderDraftNotice('')
    recorderReplaySessionIdRef.current = clientReplaySessionId
    recorderReplayStatusRef.current = 'running'
    recorderReplayReportRef.current = null
    setRecorderReplaySessionId(clientReplaySessionId)
    setRecorderReplayReport(null)
    setRecorderReplayStatus('running')
    try {
      const response = await replayTask.run(async () => api.startDraftBrowserReplaySession(buildReplayStartPayload(sourceScript, {
        projectId: sourceProjectId,
        ignoreThinkTime: true,
        replaySessionId: clientReplaySessionId || undefined,
      })))
      if (requestIsCurrent()) {
        recorderReplaySessionIdRef.current = response.replay_session_id
        setRecorderReplaySessionId(response.replay_session_id)
      }
    } catch (error: unknown) {
      if (requestIsCurrent()) {
        if (clientReplaySessionId && !isDefiniteReplayStartRejection(error)) {
          setRecorderDraftNotice('The replay start response was interrupted. Confirming the accepted replay session…')
        } else {
          recorderReplaySessionIdRef.current = ''
          recorderReplayStatusRef.current = 'failed'
          setRecorderReplaySessionId('')
          setRecorderReplayStatus('failed')
          setMessageError(getErrorMessage(error, 'Failed to start browser replay validation'))
        }
      }
    } finally {
      if (recorderReplayStartRef.current === startScope) {
        recorderReplayStartRef.current = null
      }
    }
  }

  const handleRecorderReplayComplete = (sourceReplaySessionId: string, report: BrowserReplayReport | null) => {
    const activeSessionId = recorderReplaySessionIdRef.current
    if (!activeSessionId || sourceReplaySessionId !== activeSessionId || (report?.replay_session_id && report.replay_session_id !== activeSessionId)) return
    recorderReplayReportRef.current = report
    recorderReplayStatusRef.current = report?.success ? 'completed' : 'failed'
    setRecorderReplayReport(report)
    setRecorderReplayStatus(report?.success ? 'completed' : 'failed')
  }

  const markRecorderDraftChanged = (message: string) => {
    recorderReplaySessionIdRef.current = ''
    recorderReplayReportRef.current = null
    recorderReplayStatusRef.current = 'idle'
    allowRecorderDraftSaveRef.current = false
    setRecorderReplaySessionId('')
    setRecorderReplayReport(null)
    setRecorderReplayStatus('idle')
    setAllowRecorderDraftSave(false)
    setRecorderDraftNotice(message)
  }

  const commitReviewMutation = (
    nextResult: ProtocolImportResult,
    recorderDraftNotice?: string,
    options?: { preserveProtectedCandidate?: boolean },
  ) => {
    const sessionId = activeProtectedImportSessionId(nextResult)
    const preserveProtectedCandidate = options?.preserveProtectedCandidate === true
      && (kind === 'openapi' || kind === 'postman')
      && !!sessionId
    const compatibilityResult = preserveProtectedCandidate
      ? nextResult
      : invalidateProtectedImportCandidate(nextResult)
    if (!preserveProtectedCandidate) {
      protectedCommitOverridesRef.current = {
        sessionId: '',
        secretBindings: {},
        parameterizationSelectedIds: [],
      }
    }
    latestResultRef.current = compatibilityResult
    convertTask.setValue(compatibilityResult)
    if (kind === 'recorder-export' && recorderDraftNotice) {
      markRecorderDraftChanged(recorderDraftNotice)
    }
  }

  const handlePromoteRecorderCandidate = (actionIndex: number, candidate: SelectorCandidate) => {
    if (importDraftHasExpired(latestResultRef.current)) {
      setMessageError(IMPORT_DRAFT_EXPIRED_MESSAGE)
      return
    }
    advanceReviewGeneration()
    const currentResult = latestResultRef.current
    if (!currentResult) return
    const scripts = scriptsFromImportResult(currentResult as BrowserRecordingImportResult)
    const nextScripts = scripts.map((script, index) => (
      index === selectedScriptIndex ? promoteDraftSelector(script, actionIndex, candidate.value, candidate) : script
    ))
    const nextResult = { ...currentResult, script: nextScripts[0], scripts: nextScripts } as ProtocolImportResult
    commitReviewMutation(nextResult, 'Selector updated in the draft. Run replay validation again before saving normally.')
  }

  const handleApplyRecorderReplaySuggestion = async (suggestion: ReplayRepairSuggestion) => {
    if (importDraftHasExpired(latestResultRef.current)) {
      setMessageError(IMPORT_DRAFT_EXPIRED_MESSAGE)
      return
    }
    if (suggestion.draft_patch_type !== 'promote_selector') {
      setMessageError(`Replay suggestion type "${suggestion.draft_patch_type || 'unknown'}" is not supported for imported-draft repair yet.`)
      return
    }
    const selector = typeof suggestion.fields?.selector === 'string' ? suggestion.fields.selector.trim() : ''
    if (!selector || !reviewedScript) {
      setMessageError('This replay suggestion did not include a selector to promote.')
      return
    }
    const actionIndex = findActionIndexForReplaySuggestion(reviewedScript, suggestion)
    if (actionIndex < 0) {
      setMessageError('Could not match this replay suggestion to an imported action.')
      return
    }
    advanceReviewGeneration()
    const currentResult = latestResultRef.current
    if (!currentResult) return
    const scripts = scriptsFromImportResult(currentResult as BrowserRecordingImportResult)
    const nextScripts = scripts.map((script, index) => (
      index === selectedScriptIndex ? promoteDraftSelector(script, actionIndex, selector) : script
    ))
    const nextResult = { ...currentResult, script: nextScripts[0], scripts: nextScripts } as ProtocolImportResult
    commitReviewMutation(nextResult, 'Replay suggestion applied to the draft. Run replay validation again before saving normally.')
  }

  const handleSave = async (destination: ImportSaveDestination = 'stay') => {
    if (savedResult && !asyncAPIMultiScript) return
    const publish = (result: ImportedSaveResult) => publishSaved(result, destination)
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const sourceScriptIndex = selectedScriptIndexRef.current
    const currentResult = latestResultRef.current
    const currentImportedScripts = currentResult?.scripts?.length
      ? currentResult.scripts
      : currentResult?.script
        ? [currentResult.script]
        : []
    const currentGeneratedScripts = currentImportedScripts
    const currentReviewedScript = currentGeneratedScripts[sourceScriptIndex] || currentGeneratedScripts[0]
    if (!currentResult || !currentReviewedScript || saveAllInFlightRef.current || internalReviewBusyRef.current) return
    if (importDraftHasExpired(currentResult)) {
      setMessageError(IMPORT_DRAFT_EXPIRED_MESSAGE)
      return
    }
    if (kind === 'asyncapi' && currentGeneratedScripts.length > 1 && asyncAPISavedScriptsRef.current[sourceScriptIndex]) {
      return
    }
    const activeSave = saveScriptInFlightRef.current
    if (activeSave
      && activeSave.generation === sourceGeneration
      && activeSave.projectId === sourceProjectId
      && activeSave.scriptIndex === sourceScriptIndex
      && activeSave.result === currentResult) {
      return
    }
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && selectedScriptIndexRef.current === sourceScriptIndex
      && latestResultRef.current === currentResult
    setMessageError('')
    const currentRequirements = secretBindingRequirementsForScript(
      currentResult?.secret_binding_requirements,
      sourceScriptIndex,
      currentReviewedScript,
    )
    const preservedRequirementsMayRemain = requirementsPreserveDestinationValues(currentRequirements)
    if (currentRequirements.length > 0 && !preservedRequirementsMayRemain) {
      setMessageError('Resolve every redacted credential binding before saving this script.')
      return
    }
    const saveAttempt = {
      generation: sourceGeneration,
      projectId: sourceProjectId,
      scriptIndex: sourceScriptIndex,
      result: currentResult,
    }
    saveScriptInFlightRef.current = saveAttempt
    setPersistenceInProgress(true)
    const openAPISaveTarget = { ...openAPISaveTargetRef.current }
    const postmanSaveTarget = { ...postmanSaveTargetRef.current }
    try {
      let scriptToSave = currentReviewedScript
      if (kind === 'recorder-export') {
        const currentReplayStatus = recorderReplayStatusRef.current
        const currentReplayReport = recorderReplayReportRef.current
        const replayPassed = currentReplayStatus === 'completed' && currentReplayReport?.success
        if (!replayPassed && !allowRecorderDraftSaveRef.current) {
          setMessageError('Run replay validation successfully before saving, or choose Save as draft needing replay review.')
          return
        }
        scriptToSave = attachRecorderReplayEvidence(currentReviewedScript, currentReplayReport, currentReplayStatus, !replayPassed, recorderReplaySessionIdRef.current)
      }
      if (kind === 'openapi') {
        if (openAPISaveTarget.mode === 'replace' && !openAPISaveTarget.targetScriptId) {
          setMessageError('Select a script to replace.')
          return
        }
        const savedScript = await saveTask.run(async () => {
          const importSessionId = activeProtectedImportSessionId(currentResult)
          const protectedOverrides = importSessionId && protectedCommitOverridesRef.current.sessionId === importSessionId
            ? protectedCommitOverridesRef.current
            : null
          const payload = {
            script: currentReviewedScript,
            import_session_id: importSessionId,
            secret_bindings: protectedOverrides && Object.keys(protectedOverrides.secretBindings).length > 0
              ? { ...protectedOverrides.secretBindings }
              : undefined,
            parameterization_selected_ids: protectedOverrides?.parameterizationSelectedIds.length
              ? [...protectedOverrides.parameterizationSelectedIds]
              : undefined,
            project_id: sourceProjectId,
            name: currentReviewedScript.name,
            folder_id: currentReviewedScript.folder_id ?? folderId ?? null,
            mode: openAPISaveTarget.mode,
            target_script_id: openAPISaveTarget.mode === 'replace' ? openAPISaveTarget.targetScriptId : undefined,
            replace_mode: openAPISaveTarget.mode === 'replace' ? openAPISaveTarget.replaceMode : undefined,
          }
          const response = await api.commitOpenAPIImport(payload)
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('OpenAPI import commit did not return a stored script')
          return storedScript
        })
        publish({
          scripts: [withFallbackPersistedScript(savedScript, currentReviewedScript)],
          ...(!requestIsCurrent() ? { stayInImportReview: true } : {}),
        })
        return
      }
      if (kind === 'postman') {
        if (postmanSaveTarget.mode === 'replace' && !postmanSaveTarget.targetScriptId) {
          setMessageError('Select a script to replace.')
          return
        }
        const savedScript = await saveTask.run(async () => {
          const importSessionId = activeProtectedImportSessionId(currentResult)
          const protectedOverrides = importSessionId && protectedCommitOverridesRef.current.sessionId === importSessionId
            ? protectedCommitOverridesRef.current
            : null
          const payload = {
            script: currentReviewedScript,
            import_session_id: importSessionId,
            secret_bindings: protectedOverrides && Object.keys(protectedOverrides.secretBindings).length > 0
              ? { ...protectedOverrides.secretBindings }
              : undefined,
            parameterization_selected_ids: protectedOverrides?.parameterizationSelectedIds.length
              ? [...protectedOverrides.parameterizationSelectedIds]
              : undefined,
            project_id: sourceProjectId,
            name: currentReviewedScript.name,
            folder_id: currentReviewedScript.folder_id ?? folderId ?? null,
            mode: postmanSaveTarget.mode,
            target_script_id: postmanSaveTarget.mode === 'replace' ? postmanSaveTarget.targetScriptId : undefined,
          }
          const response = await api.savePostmanImport(payload)
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('Postman import save did not return a stored script')
          return storedScript
        })
        publish({
          scripts: [withFallbackPersistedScript(savedScript, currentReviewedScript)],
          ...(!requestIsCurrent() ? { stayInImportReview: true } : {}),
        })
        return
      }
      if (kind === 'asyncapi') {
        const savedScript = await saveTask.run(async () => {
          if (importDraftHasExpired(currentResult)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
          const response = await api.commitAsyncAPIImport({
            script: currentReviewedScript,
            mode: 'create',
            folder_id: currentReviewedScript.folder_id ?? folderId ?? null,
          })
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('AsyncAPI import commit did not return a stored script')
          return storedScript
        })
        const persisted = withFallbackPersistedScript(savedScript, currentReviewedScript)
        if (!requestIsCurrent()) {
          publish({ scripts: [persisted], stayInImportReview: true })
        } else {
          if (currentGeneratedScripts.length > 1) {
            const nextSavedScripts = {
              ...asyncAPISavedScriptsRef.current,
              [sourceScriptIndex]: persisted,
            }
            asyncAPISavedScriptsRef.current = nextSavedScripts
            setAsyncAPISavedScripts(nextSavedScripts)
            const nextPendingIndex = currentGeneratedScripts.findIndex((_script, index) => !nextSavedScripts[index])
            if (nextPendingIndex >= 0) {
              setPreparedCredentialSummary(null)
              advanceReviewGeneration()
              selectedScriptIndexRef.current = nextPendingIndex
              setSelectedScriptIndex(nextPendingIndex)
              publish({ scripts: [persisted], stayInImportReview: true })
            } else {
              const orderedScripts = currentGeneratedScripts
                .map((_script, index) => nextSavedScripts[index])
                .filter((script): script is StoredScript => !!script)
              publish({ scripts: orderedScripts })
            }
          } else {
            publish({ scripts: [persisted] })
          }
        }
        return
      }
      if (kind === 'cloudtest') {
        const destinationFolderID = scriptToSave.folder_id ?? folderId ?? null
        const batchScripts = currentGeneratedScripts.map((script, index) => ({
          ...script,
          ...(index === sourceScriptIndex ? scriptToSave : script),
          folder_id: script.folder_id ?? destinationFolderID,
        }))
        const retainedOperation = genericImportBatchSaveOperationRef.current
        const operationID = retainedOperation
          && retainedOperation.result === currentResult
          && retainedOperation.scriptIndex === -1
          && retainedOperation.kind === kind
          && retainedOperation.projectId === sourceProjectId
          && retainedOperation.folderId === destinationFolderID
          ? retainedOperation.operationId
          : createGenericImportBatchSaveOperationID()
        genericImportBatchSaveOperationRef.current = {
          result: currentResult,
          scriptIndex: -1,
          kind,
          projectId: sourceProjectId,
          folderId: destinationFolderID,
          operationId: operationID,
        }
        let storedBatch: StoredScript[] = []
        await saveTask.run(async () => {
          const response = await api.commitGenericImportBatch({
            scripts: batchScripts,
            folder_id: destinationFolderID,
            operation_id: operationID,
            source_profile: 'cloudtest',
            import_session_ids: currentResult.import_session_ids,
          })
          if (response.count !== batchScripts.length || response.scripts.length !== batchScripts.length) {
            throw new Error('The CloudTest batch save did not return every Clip script. Retry the same save to reconcile its result.')
          }
          storedBatch = response.scripts
          return response.scripts[0]
        })
        genericImportBatchSaveOperationRef.current = null
        publish({
          scripts: storedBatch.map((stored, index) => withFallbackPersistedScript(stored, batchScripts[index])),
          ...(!requestIsCurrent() ? { stayInImportReview: true } : {}),
        })
        return
      }
      const sourceProfile = genericImportSourceProfile(kind)
      if (!sourceProfile) {
        throw new Error(`The ${labels.title} save path is unavailable for this import type.`)
      }
      const destinationFolderID = scriptToSave.folder_id ?? folderId ?? null
      const retainedOperation = genericImportBatchSaveOperationRef.current
      const operationID = retainedOperation
        && retainedOperation.result === currentResult
        && retainedOperation.scriptIndex === sourceScriptIndex
        && retainedOperation.kind === kind
        && retainedOperation.projectId === sourceProjectId
        && retainedOperation.folderId === destinationFolderID
        ? retainedOperation.operationId
        : createGenericImportBatchSaveOperationID()
      genericImportBatchSaveOperationRef.current = {
        result: currentResult,
        scriptIndex: sourceScriptIndex,
        kind,
        projectId: sourceProjectId,
        folderId: destinationFolderID,
        operationId: operationID,
      }
      const savedScript = await saveTask.run(async () => {
        const response = await api.commitGenericImportBatch({
          scripts: [scriptToSave],
          folder_id: destinationFolderID,
          operation_id: operationID,
          source_profile: sourceProfile,
        })
        if (response.count !== 1 || response.scripts.length !== 1) {
          throw new Error('The atomic import save did not return the stored script. Retry the same save to reconcile its result.')
        }
        return response.scripts[0]
      })
      genericImportBatchSaveOperationRef.current = null
      publish({
        scripts: [withFallbackPersistedScript(savedScript, scriptToSave)],
        ...(!requestIsCurrent() ? { stayInImportReview: true } : {}),
      })
    } catch (error: unknown) {
      if (requestIsCurrent()) setMessageError(getErrorMessage(error, 'Failed to save generated script'))
    } finally {
      if (saveScriptInFlightRef.current === saveAttempt) {
        saveScriptInFlightRef.current = null
        setPersistenceInProgress(false)
      }
    }
  }

  const handleEditReviewedScript = () => {
    const currentResult = latestResultRef.current
    const currentScripts = currentResult?.scripts?.length
      ? currentResult.scripts
      : currentResult?.script
        ? [currentResult.script]
        : []
    const currentScript = currentScripts[selectedScriptIndex] || currentScripts[0]
    if (currentScript && currentResult) {
      if (importDraftHasExpired(currentResult)) {
        setMessageError(IMPORT_DRAFT_EXPIRED_MESSAGE)
        return
      }
      if (kind === 'asyncapi' && currentScripts.length > 1 && asyncAPISavedScriptsRef.current[selectedScriptIndex]) {
        return
      }
      const currentRequirements = secretBindingRequirementsForScript(
        currentResult.secret_binding_requirements,
        selectedScriptIndex,
        currentScript,
      )
      if (credentialPreservingImportDraftRequiresSave(currentResult, currentScript, currentRequirements)) {
        if (!onImported) {
          setMessageError('Save this credential-preserving import before opening it in the Visual Editor.')
          return
        }
        void handleSave('editor')
        return
      }
      const compatibilityResult = invalidateProtectedImportCandidate(currentResult)
      protectedCommitOverridesRef.current = {
        sessionId: '',
        secretBindings: {},
        parameterizationSelectedIds: [],
      }
      latestResultRef.current = compatibilityResult
      convertTask.setValue(compatibilityResult)
      onEdit?.(currentScript)
    }
  }

  const handleSaveAllAsyncAPIScripts = async () => {
    if (kind !== 'asyncapi' || generatedScripts.length === 0 || saveAllInFlightRef.current || saveScriptInFlightRef.current || internalReviewBusyRef.current) return
    if (importDraftHasExpired(latestResultRef.current)) {
      setMessageError(IMPORT_DRAFT_EXPIRED_MESSAGE)
      return
    }
    const pendingScriptIndices = generatedScripts.flatMap(
      (_script, index) => asyncAPISavedScriptsRef.current[index] ? [] : [index],
    )
    if (pendingScriptIndices.length === 0) return
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
    saveAllInFlightRef.current = true
    setSaveAllPreparing(true)
    setMessageError('')
    let credentialsPreparedForBatch = false
    try {
      const otherPendingRequirements = pendingScriptIndices
        .filter(index => index !== selectedScriptIndexRef.current)
        .flatMap(index => {
          const script = latestResultRef.current?.scripts?.[index] || generatedScripts[index]
          return script
            ? secretBindingRequirementsForScript(latestResultRef.current?.secret_binding_requirements, index, script)
              .filter(requirement => requirement.destination_value_preserved !== true)
            : []
        })
      if (otherPendingRequirements.length > 0) {
        setMessageError('Select each affected generated script and prepare its credentials before saving the atomic batch. Credential choices remain scoped to the visible script, but preparation does not save any script.')
        return
      }
      const selectedPendingScript = latestResultRef.current?.scripts?.[selectedScriptIndexRef.current]
        || generatedScripts[selectedScriptIndexRef.current]
      const selectedPendingRequirements = selectedPendingScript
        ? secretBindingRequirementsForScript(
          latestResultRef.current?.secret_binding_requirements,
          selectedScriptIndexRef.current,
          selectedPendingScript,
        )
        : []
      if (selectedPendingRequirements.length > 0) {
        if (!credentialReviewRef.current) {
          setMessageError('Credential preparation is unavailable for the selected generated script.')
          return
        }
        const committed = await credentialReviewRef.current.commit()
        if (!committed || !requestIsCurrent()) return
        credentialsPreparedForBatch = true
      }
      if (importDraftHasExpired(latestResultRef.current)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
      const preparedResult = latestResultRef.current
      const scriptsToSave = preparedResult?.scripts?.length
        ? preparedResult.scripts
        : generatedScripts
      const remainingRequirements = pendingScriptIndices.flatMap(index => {
        const script = scriptsToSave[index]
        return script
          ? secretBindingRequirementsForScript(preparedResult?.secret_binding_requirements, index, script)
          : []
      })
      const preservedRequirementsMayRemain = requirementsPreserveDestinationValues(remainingRequirements)
      if (remainingRequirements.length > 0 && !preservedRequirementsMayRemain) {
        throw new Error('Every generated script must use governed secret references before Save All can begin.')
      }
      if (
        scriptsToSave.length !== generatedScripts.length
        || pendingScriptIndices.some(index => !scriptsToSave[index])
      ) {
        throw new Error('Save All preflight failed because a generated script is stale or missing.')
      }
      const sourceResult = preparedResult
      if (!sourceResult) throw new Error('Save All preflight failed because the current import result is missing.')
      const pendingScripts = pendingScriptIndices.map(index => {
        const script = scriptsToSave[index]
        if (!script) throw new Error('Save All preflight failed because a generated script is missing.')
        return script
      })
      const explicitFolderIDs = Array.from(new Set(pendingScripts
        .map(script => script.folder_id?.trim() || '')
        .filter(Boolean)))
      if (explicitFolderIDs.length > 1) {
        throw new Error('Save All requires every generated script to use the same destination folder.')
      }
      const destinationFolderID = explicitFolderIDs[0] || folderId || null
      const pendingKey = pendingScriptIndices.join(',')
      const retainedOperation = asyncAPIBatchSaveOperationRef.current
      const operationID = retainedOperation
        && retainedOperation.result === sourceResult
        && retainedOperation.pendingKey === pendingKey
        && retainedOperation.projectId === sourceProjectId
        && retainedOperation.folderId === destinationFolderID
        ? retainedOperation.operationId
        : createAsyncAPIBatchSaveOperationID()
      asyncAPIBatchSaveOperationRef.current = {
        result: sourceResult,
        pendingKey,
        projectId: sourceProjectId,
        folderId: destinationFolderID,
        operationId: operationID,
      }
      let batchResponseScripts: StoredScript[] = []
      await saveTask.run(async () => {
        if (!requestIsCurrent() || latestResultRef.current !== sourceResult) {
          throw new Error('The import result changed before the atomic save began. Review the current draft and try again.')
        }
        if (importDraftHasExpired(sourceResult)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
        const response = await api.commitAsyncAPIImportBatch({
          scripts: pendingScripts,
          folder_id: destinationFolderID,
          operation_id: operationID,
        })
        if (response.count !== pendingScripts.length || response.scripts.length !== pendingScripts.length) {
          throw new Error('The atomic AsyncAPI save did not return every stored script. Retry the same save to reconcile its result.')
        }
        batchResponseScripts = response.scripts.map((storedScript, index) => (
          withFallbackPersistedScript(storedScript, pendingScripts[index])
        ))
        return batchResponseScripts[0]
      })
      const newlySavedScriptsByIndex = Object.fromEntries(pendingScriptIndices.map((scriptIndex, index) => (
        [scriptIndex, batchResponseScripts[index]]
      ))) as Record<number, StoredScript>
      if (!requestIsCurrent() || latestResultRef.current !== sourceResult) {
        publishSaved({ scripts: batchResponseScripts, stayInImportReview: true })
        return
      }
      asyncAPIBatchSaveOperationRef.current = null
      if (requestIsCurrent() && latestResultRef.current === sourceResult) {
        const allSavedScriptsByIndex = {
          ...asyncAPISavedScriptsRef.current,
          ...newlySavedScriptsByIndex,
        }
        asyncAPISavedScriptsRef.current = allSavedScriptsByIndex
        setAsyncAPISavedScripts(allSavedScriptsByIndex)
        const orderedScripts = scriptsToSave
          .map((_script, index) => allSavedScriptsByIndex[index])
          .filter((script): script is StoredScript => !!script)
        publishSaved({ scripts: orderedScripts })
      }
    } catch (error: unknown) {
      if (requestIsCurrent()) {
        const message = getErrorMessage(error, 'Failed to save generated scripts')
        const credentialNotice = credentialsPreparedForBatch
          ? ' Project-secret values prepared during review remain configured and will be reused.'
          : ''
        setMessageError(`${message} The script batch was not reported complete.${credentialNotice} Retrying Save Remaining safely reuses the same save operation.`)
      }
    } finally {
      saveAllInFlightRef.current = false
      setSaveAllPreparing(false)
    }
  }

  const handleParameterizationApplied = (
    parameterizedScript: ScriptDefinition,
    _diffs: unknown[],
    selectedIds: string[],
    importDraftExpiresAt?: string,
  ) => {
    if (!result) return
    if (importDraftHasExpired(result)) {
      setMessageError(IMPORT_DRAFT_EXPIRED_MESSAGE)
      return
    }
    if (asyncAPISelectedScriptSaved) return
    advanceReviewGeneration()
    const nextScripts = generatedScripts.length ? generatedScripts.map((script, index) => index === selectedScriptIndex ? parameterizedScript : script) : [parameterizedScript]
    const nextResult = {
      ...result,
      script: nextScripts[0],
      scripts: nextScripts,
      ...(nextScripts.length === 1 && importDraftExpiresAt
        ? { import_draft_expires_at: importDraftExpiresAt }
        : {}),
    } as ProtocolImportResult
    const sessionId = activeProtectedImportSessionId(result)
    const preserveProtectedCandidate = (kind === 'openapi' || kind === 'postman') && !!sessionId
    if (preserveProtectedCandidate && sessionId) {
      const current = protectedCommitOverridesRef.current.sessionId === sessionId
        ? protectedCommitOverridesRef.current
        : { sessionId, secretBindings: {}, parameterizationSelectedIds: [] }
      protectedCommitOverridesRef.current = {
        ...current,
        parameterizationSelectedIds: mergeProtectedParameterizationSelectionIds(
          current.parameterizationSelectedIds,
          selectedIds,
        ),
      }
    }
    commitReviewMutation(
      nextResult,
      'Parameterization updated the draft. Run replay validation again before saving normally.',
      { preserveProtectedCandidate },
    )
  }

  // Unlike parameterization, this replaces the WHOLE script array: a
  // collection-wide value is written into every generated script, so keeping
  // only the selected one would silently drop the rest of the change.
  const handleSharedValuesApplied = (
    updatedScripts: ScriptDefinition[],
    _diffs: unknown[],
    refreshed?: SharedValues,
    importDraftExpiresAt?: (string | null)[],
  ) => {
    if (!result || updatedScripts.length === 0) return
    if (importDraftHasExpired(result)) {
      setMessageError(IMPORT_DRAFT_EXPIRED_MESSAGE)
      return
    }
    if (asyncAPISelectedScriptSaved) return
    advanceReviewGeneration()
    const rotatedExpiry = earliestImportDraftExpiry(importDraftExpiresAt)
    const nextResult = {
      ...result,
      script: updatedScripts[0],
      scripts: updatedScripts,
      // The scripts changed, so the candidates derived from them changed too.
      // Keeping the old block would show values the scripts no longer hold.
      ...(refreshed ? { shared_values: refreshed } : {}),
      ...(rotatedExpiry ? { import_draft_expires_at: rotatedExpiry } : {}),
    } as ProtocolImportResult
    // The apply endpoint mutates the server-held draft in place and rotates its
    // capability; the protected candidate is still the one the save must commit
    // through. Dropping it here would push the save onto the unprotected path
    // and lose the preserved credentials, exactly as it would for
    // parameterization.
    const sessionId = activeProtectedImportSessionId(result)
    const preserveProtectedCandidate = (kind === 'openapi' || kind === 'postman') && !!sessionId
    commitReviewMutation(
      nextResult,
      'Collection values updated every generated script. Run replay validation again before saving normally.',
      { preserveProtectedCandidate },
    )
  }

  const handleSourceSecretSessionSettled = (
    sessionId: string,
    migrationSelections: SecretBindingSelection[],
  ) => {
    settledSourceSecretSelectionsRef.current.set(sessionId, migrationSelections)
    const currentResult = latestResultRef.current
    if (currentResult?.source_secret_session_id !== sessionId) return
    sourceSecretSessions.settle('conversion', reviewProjectIdRef.current, sessionId)
    const settledResult = {
      ...currentResult,
      secret_binding_requirements: updateSecretBindingRequirementNames(
        currentResult.secret_binding_requirements,
        migrationSelections,
      ),
      source_secret_session_id: undefined,
      source_secret_session_expires_at: undefined,
      source_secret_migration_allowed: false,
    } as ProtocolImportResult
    latestResultRef.current = settledResult
    convertTask.setValue(settledResult)
  }

  const handlePrepareAsyncAPICredentialsForBatch = () => {
    const currentResult = latestResultRef.current
    const scripts = currentResult?.scripts?.length
      ? currentResult.scripts
      : currentResult?.script
        ? [currentResult.script]
        : []
    const nextCredentialIndex = scripts.findIndex((script, index) => (
      index !== selectedScriptIndexRef.current
      && !asyncAPISavedScriptsRef.current[index]
      && secretBindingRequirementsForScript(
        currentResult?.secret_binding_requirements,
        index,
        script,
      ).some(requirement => requirement.destination_value_preserved !== true)
    ))
    if (nextCredentialIndex < 0) return
    setPreparedCredentialSummary(null)
    advanceReviewGeneration()
    selectedScriptIndexRef.current = nextCredentialIndex
    setSelectedScriptIndex(nextCredentialIndex)
  }

  const handleSecretBindingsApply = async (bindings: SecretBindingSelection[]) => {
    const sourceResult = latestResultRef.current || result
    const sourceScripts = sourceResult?.scripts?.length
      ? sourceResult.scripts
      : sourceResult?.script
        ? [sourceResult.script]
        : []
    const sourceScriptIndex = selectedScriptIndexRef.current
    const sourceReviewedScript = sourceScripts[sourceScriptIndex] || sourceScripts[0]
    if (!sourceResult || !sourceReviewedScript) return
    if (importDraftHasExpired(sourceResult)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && selectedScriptIndexRef.current === sourceScriptIndex
    const assertCurrent = () => {
      if (!requestIsCurrent()) throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
      if (importDraftHasExpired(latestResultRef.current)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
    }
    try {
      assertCurrent()
      const resolved = await api.applySecretBindings({
        script: sourceReviewedScript,
        script_index: sourceScriptIndex,
        bindings: bindings.map(({ requirement_id, secret_name }) => ({ requirement_id, secret_name })),
      })
      assertCurrent()
      const currentResult = latestResultRef.current || sourceResult
      const currentScripts = currentResult.scripts?.length
        ? currentResult.scripts
        : currentResult.script
          ? [currentResult.script]
          : sourceScripts
      const nextScripts = currentScripts.length
        ? currentScripts.map((script, index) => index === sourceScriptIndex ? resolved.script : script)
        : [resolved.script]
      const nextResult = {
        ...currentResult,
        script: nextScripts[0],
        scripts: nextScripts,
        ...(nextScripts.length === 1 && resolved.import_draft_expires_at
          ? { import_draft_expires_at: resolved.import_draft_expires_at }
          : {}),
        secret_binding_requirements: replaceSecretBindingRequirementsForScript(
          updateSecretBindingRequirementNames(
            currentResult.secret_binding_requirements,
            sourceResult.source_secret_session_id
              ? settledSourceSecretSelectionsRef.current.get(sourceResult.source_secret_session_id) ?? []
              : [],
          ),
          sourceScriptIndex,
          sourceReviewedScript,
          resolved.secret_binding_requirements,
        ),
      } as ProtocolImportResult
      const sessionId = activeProtectedImportSessionId(sourceResult)
      const preserveProtectedCandidate = (kind === 'openapi' || kind === 'postman') && !!sessionId
      if (preserveProtectedCandidate && sessionId) {
        const current = protectedCommitOverridesRef.current.sessionId === sessionId
          ? protectedCommitOverridesRef.current
          : { sessionId, secretBindings: {}, parameterizationSelectedIds: [] }
        const secretBindings = { ...current.secretBindings }
        for (const binding of bindings) {
          const requirementId = binding.requirement_id.trim()
          const secretName = binding.secret_name.trim()
          if (requirementId && secretName) secretBindings[requirementId] = secretName
        }
        protectedCommitOverridesRef.current = { ...current, secretBindings }
      }
      commitReviewMutation(
        nextResult,
        'Credential bindings updated the draft. Run replay validation again before saving normally.',
        { preserveProtectedCandidate },
      )
    } catch (error: unknown) {
      if (!requestIsCurrent()) throw error
      const refreshed = refreshedSecretBindingRequirements(error)
      if (refreshed) {
        const refreshedResult = {
          ...sourceResult,
          secret_binding_requirements: replaceSecretBindingRequirementsForScript(
            sourceResult.secret_binding_requirements,
            sourceScriptIndex,
            sourceReviewedScript,
            refreshed,
          ),
        } as ProtocolImportResult
        latestResultRef.current = refreshedResult
        convertTask.setValue(refreshedResult)
      }
      throw error
    }
  }

  const selectAsyncAPIReviewScript = (index: number, focusButton = false) => {
    if (saveAllPreparing || saveTask.pending || internalReviewBusy || generatedScripts.length === 0) return
    const nextIndex = Math.max(0, Math.min(generatedScripts.length - 1, index))
    setPreparedCredentialSummary(null)
    advanceReviewGeneration()
    selectedScriptIndexRef.current = nextIndex
    if (focusButton) pendingAsyncAPIScriptFocusRef.current = nextIndex
    setSelectedScriptIndex(nextIndex)
  }

  const handleAsyncAPIScriptKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>, index: number) => {
    let nextIndex: number | null = null
    if (event.key === 'ArrowRight' || event.key === 'ArrowDown') nextIndex = Math.min(generatedScripts.length - 1, index + 1)
    if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') nextIndex = Math.max(0, index - 1)
    if (event.key === 'Home') nextIndex = 0
    if (event.key === 'End') nextIndex = generatedScripts.length - 1
    if (nextIndex === null || nextIndex === index) return
    event.preventDefault()
    selectAsyncAPIReviewScript(nextIndex, true)
  }

  return (
    <div className="sample-import">
      <ImportSaveConfirmation result={asyncAPIMultiScript ? { scripts: generatedScripts.flatMap((_, index) => asyncAPISavedScripts[index] ? [asyncAPISavedScripts[index]] : []) } : savedResult} onImportMore={() => { invalidateConversionSource(); setFile(null) }} onOpen={openSaved} disabled={sourceMutationLocked} />
      <div className="sample-import-header">
        <div>
          <h2>{labels.title}</h2>
          <p className="sample-import-subtitle">{labels.subtitle}</p>
        </div>
        {onClose && <button className="btn btn-secondary" onClick={() => {
          invalidateConversionSource()
          onClose()
        }}>Back</button>}
      </div>

      <ImportWorkflowGuide stage={savedResult ? 'saved' : result ? 'review' : 'source'} />

      <details className="import-source-setup" open={!result}>
        <summary>Source & conversion options — changing these requires a new review</summary>
      <div className="sample-import-card">
        <div className="sample-import-grid">
          <div className="sample-import-field sample-import-span-2">
            <span>File</span>
            <ImportDropzone
              accept={labels.accept}
              file={file}
              onFile={next => { invalidateConversionSource(); setFile(next) }}
              dropHint={labels.drop}
              validate={candidate => validateImportFile(kind, candidate)}
              onError={setMessageError}
              disabled={sourceMutationLocked}
            />
          </div>
          {kind === 'postman' && (
            <>
              <div className="sample-import-field sample-import-span-2">
                <span>Environment File</span>
                <ImportDropzone
                  accept={POSTMAN_ENVIRONMENT_ACCEPT}
                  file={postmanEnvironmentFile}
                  onFile={next => { invalidateConversionSource(); setPostmanEnvironmentFile(next) }}
                  dropHint="Drop an optional Postman environment JSON file here"
                  validate={validatePostmanEnvironmentFile}
                  onError={setMessageError}
                  disabled={sourceMutationLocked}
                />
              </div>
              <label className="sample-import-field">
                <span>Default Think Time (ms)</span>
                <input type="number" min={0} value={postmanThinkTimeMs} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setPostmanThinkTimeMs(Math.max(0, Number(event.target.value) || 0)) }} />
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={postmanSanitizeSensitive} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setPostmanSanitizeSensitive(event.target.checked) }} />
                Scrub sensitive fields
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={postmanIncludeAssertions} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setPostmanIncludeAssertions(event.target.checked) }} />
                Add status assertions
              </label>
              <label className="sample-toggle sample-import-span-2" title="Bind detected parameters to a generated CSV data source so each virtual user replays with varying data.">
                <input type="checkbox" checked={postmanUseDataSource} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setPostmanUseDataSource(event.target.checked) }} />
                Generate parameter data source
              </label>
            </>
          )}
          {supportsScriptName && (
            <label className="sample-import-field">
              <span>Script Name</span>
              <input value={name} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setName(event.target.value) }} placeholder={scriptNamePlaceholder(kind)} />
            </label>
          )}
          {supportsBaseURL && (
            <label className="sample-import-field">
              <span>Base URL Override</span>
              <input value={baseURL} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setBaseURL(event.target.value) }} placeholder="https://api.example.com" />
            </label>
          )}
          {kind === 'asyncapi' && (
            <>
              <label className="sample-import-field">
                <span>Broker Override</span>
                <input value={asyncAPIBrokers} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setAsyncAPIBrokers(event.target.value) }} placeholder="broker-a:9092, broker-b:9092" />
              </label>
              <label className="sample-import-field">
                <span>Channel Filter</span>
                <input value={asyncAPIChannels} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setAsyncAPIChannels(event.target.value) }} placeholder="orders.created, payments.*" />
              </label>
              <label className="sample-import-field">
                <span>Serialization</span>
                <select value={asyncAPISerialization} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setAsyncAPISerialization(event.target.value) }}>
                  <option value="">From spec / raw</option>
                  <option value="json_schema">JSON Schema</option>
                  <option value="avro">Avro</option>
                </select>
              </label>
              <label className="sample-import-field">
                <span>Schema Registry URL</span>
                <input value={asyncAPISchemaRegistryURL} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setAsyncAPISchemaRegistryURL(event.target.value) }} placeholder="https://registry.example.com" />
              </label>
              <label className="sample-import-field">
                <span>Schema ID</span>
                <input type="number" min={0} value={asyncAPISchemaID} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setAsyncAPISchemaID(event.target.value) }} placeholder="42" />
              </label>
              <label className="sample-import-field">
                <span>Subject</span>
                <input value={asyncAPISubject} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setAsyncAPISubject(event.target.value) }} placeholder="orders-value" />
              </label>
              <label className="sample-import-field">
                <span>Think Time (ms)</span>
                <input type="number" min={0} value={asyncAPIThinkTimeMs} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setAsyncAPIThinkTimeMs(Math.max(0, Number(event.target.value) || 0)) }} />
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={asyncAPIMock} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setAsyncAPIMock(event.target.checked) }} />
                Use mock broker
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={asyncAPIIncludeAssertions} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setAsyncAPIIncludeAssertions(event.target.checked) }} />
                Include replay assertions
              </label>
            </>
          )}
          {kind === 'wsdl' && (
            <label className="sample-import-field">
              <span>SOAP Version</span>
              <select value={soapVersion} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setSoapVersion(event.target.value) }}>
                <option value="1.1">SOAP 1.1</option>
                <option value="1.2">SOAP 1.2</option>
              </select>
            </label>
          )}
          {kind === 'har' && (
            <>
              <div className="sample-import-field sample-import-span-2">
                <span>Comparison HAR (optional)</span>
                <ImportDropzone
                  accept={labels.accept}
                  file={secondaryHARFile}
                  onFile={next => { invalidateConversionSource(); setSecondaryHARFile(next) }}
                  dropHint="Drop an optional comparison HAR file here"
                  validate={candidate => validateImportFile('har', candidate)}
                  onError={setMessageError}
                  disabled={sourceMutationLocked}
                />
                <small>Optional second recording for correlation diff</small>
              </div>
              <label className="sample-import-field">
                <span>Retention Days</span>
                <input type="number" min={1} value={retentionDays} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setRetentionDays(Math.max(1, Number(event.target.value) || 1)) }} />
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={redactSensitive} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setRedactSensitive(event.target.checked) }} />
                Scrub sensitive fields
              </label>
              <label className="sample-toggle sample-import-span-2">
                <input type="checkbox" checked={includeStaticAssets} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setIncludeStaticAssets(event.target.checked) }} />
                Include static assets
              </label>
            </>
          )}
          {supportsAgentforceTransform && file && (
            <label className="sample-toggle sample-import-span-2" title="If an Agentforce Agent API conversation is detected, rewrite the script to acquire an OAuth2 token, correlate the session id, consume SSE, and end the session.">
              <input type="checkbox" checked={agentforceReplayReady} disabled={sourceMutationLocked} onChange={event => { invalidateConversionSource(); setAgentforceReplayReady(event.target.checked) }} />
              Make Agentforce replay-ready (if an Agent API flow is detected)
            </label>
          )}
        </div>
      </div>

      <div className="sample-import-actions">
        {projectScopeRequired && <p id="har-project-scope-required" className="sample-import-error" role="status">Select a project from the workspace context before inspecting HAR traffic.</p>}
        {kind !== 'asyncapi' && (
          <details><summary>Advanced validation only</summary><p>Run the validator to inspect a draft without saving. Review its findings before choosing whether to save.</p><button className="btn btn-secondary btn-lg" onClick={() => handleConvert('validation')} disabled={convertTask.pending || sourceMutationLocked || !file || projectScopeRequired}>
            {convertTask.pending && reviewMode === 'validation' ? <><span className="spinner" /> Validating...</> : 'Validate Only'}
          </button></details>
        )}
        <button className="btn btn-primary btn-lg" onClick={() => handleConvert('conversion')} disabled={convertTask.pending || sourceMutationLocked || !file || projectScopeRequired} aria-describedby={projectScopeRequired ? 'har-project-scope-required' : undefined}>
          {convertTask.pending && reviewMode === 'conversion' ? <><span className="spinner" /> Inspecting...</> : 'Inspect & review'}
        </button>
      </div>

      </details>
      {(messageError || convertTask.error) && <div className="sample-import-error" role="alert">{messageError || convertTask.error}</div>}

      {sourceImportSupportHasLimitations(sourceImportSupport) && (
        <SourceImportCoveragePanel kind={kind} support={sourceImportSupport} />
      )}


      {result && reviewedScript && (
        <div className="sample-import-result">
          <ScriptImportReview
            tabbed
            batchControls={<> {kind === 'asyncapi' && generatedScripts.length > 1 && (
              <div className="sample-import-review-block">
                <h4>Generated Scripts</h4>
                {generatedScripts.length > ASYNCAPI_SCRIPT_PAGE_SIZE && (
                  <nav className="sample-import-script-pagination" aria-label="Generated AsyncAPI script pages">
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm"
                      disabled={saveAllPreparing || saveTask.pending || internalReviewBusy || asyncAPIScriptPageStart === 0}
                      onClick={() => selectAsyncAPIReviewScript(Math.max(0, asyncAPIScriptPageStart - ASYNCAPI_SCRIPT_PAGE_SIZE), true)}
                    >
                      Previous scripts
                    </button>
                    <span role="status" aria-live="polite">
                      Showing {asyncAPIScriptPageStart + 1}–{Math.min(asyncAPIScriptPageStart + ASYNCAPI_SCRIPT_PAGE_SIZE, generatedScripts.length)} of {generatedScripts.length}
                    </span>
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm"
                      disabled={saveAllPreparing || saveTask.pending || internalReviewBusy || asyncAPIScriptPageStart + ASYNCAPI_SCRIPT_PAGE_SIZE >= generatedScripts.length}
                      onClick={() => selectAsyncAPIReviewScript(asyncAPIScriptPageStart + ASYNCAPI_SCRIPT_PAGE_SIZE, true)}
                    >
                      Next scripts
                    </button>
                  </nav>
                )}
                <TabList className="sample-import-script-tabs" role="tablist" aria-label="Generated AsyncAPI scripts" onKeyDown={handleTabKey}>
                  {visibleAsyncAPIScripts.map((script, visibleIndex) => {
                    const index = asyncAPIScriptPageStart + visibleIndex
                    return (
                    <button
                      key={`${script.name}-${index}`}
                      ref={element => {
                        if (element) asyncAPIScriptButtonRefs.current.set(index, element)
                        else asyncAPIScriptButtonRefs.current.delete(index)
                      }}
                      type="button"
                      role="tab"
                      aria-selected={index === selectedScriptIndex}
                      tabIndex={index === selectedScriptIndex ? 0 : -1}
                      className={`sample-import-script-tab ${index === selectedScriptIndex ? 'active' : ''}`}
                      onClick={() => selectAsyncAPIReviewScript(index)}
                      onKeyDown={event => handleAsyncAPIScriptKeyDown(event, index)}
                      disabled={saveAllPreparing || saveTask.pending || internalReviewBusy}
                    >
                      {script.protocol} · {script.name}{asyncAPISavedScripts[index] ? ' · Saved' : ''}
                    </button>
                    )
                  })}
                </TabList>
                <button
                  className="btn btn-secondary btn-sm"
                  type="button"
                  onClick={handleSaveAllAsyncAPIScripts}
                  disabled={importDraftExpired || asyncAPIPendingScriptIndices.length === 0 || saveTask.pending || saveAllPreparing || internalReviewBusy || Boolean(asyncAPISaveAllDisabledReason)}
                  aria-describedby={asyncAPISaveAllDisabledReason ? 'asyncapi-save-all-disabled-reason' : undefined}
                  title={importDraftExpired
                    ? 'Re-run the import to obtain a fresh credential-preserving draft before saving.'
                    : asyncAPISaveAllDisabledReason || undefined}
                >
                  {saveTask.pending ? 'Saving...' : saveAllPreparing ? 'Preparing credentials...' : asyncAPISaveAllLabel}
                </button>
                {asyncAPISaveAllDisabledReason && (
                  <p id="asyncapi-save-all-disabled-reason" className="sample-import-warning" role="status">
                    <strong>Why Save Remaining is unavailable:</strong> {asyncAPISaveAllDisabledReason}
                  </p>
                )}
              </div>
            )} </>}
            title={reviewMode === 'validation' ? 'Validation Review' : 'Review Generated Script'}
            reviewGeneration={reviewScope}
            subtitle={savedResult && !asyncAPIMultiScript ? 'Saved to the Script Library. Open a saved script above or import more files.' : `${actionCount} action(s) generated from ${generatedFromLabel(kind)}.${reviewMode === 'validation' ? ' This preview came from a dry-run validation; nothing has been saved. You can still save the reviewed script if the output looks correct.' : ''}`}
            saveLabel={savedResult && !asyncAPIMultiScript ? 'Saved to Script Library' : asyncAPISelectedSaveLabel || cloudTestSaveLabel || ((kind === 'postman' && postmanSaveMode === 'replace') || (kind === 'openapi' && openAPISaveMode === 'replace') ? 'Replace script' : 'Save draft')}
            script={reviewedScript}
            warnings={result.warnings}
            unsupportedElements={result.unsupported_elements}
            diagnostics={result.diagnostics}
            reviewSections={result.review_sections}
            parameterization={asyncAPISelectedScriptSaved ? undefined : result.parameterization}
            onParameterizationApplied={handleParameterizationApplied}
            onSettingsBusyChange={setSettingsEditing}
            onSettingsApplied={updated => {
              if (!result) return
              advanceReviewGeneration()
              const scripts = generatedScripts.length ? generatedScripts.map((entry, index) => index === selectedScriptIndex ? updated.script : entry) : [updated.script]
              commitReviewMutation({ ...result, script: scripts[0], scripts, secret_binding_requirements: replaceSecretBindingRequirementsForScript(result.secret_binding_requirements, selectedScriptIndex, updated.script, (updated.secret_binding_requirements || []).map(requirement => ({ ...requirement, script_index: selectedScriptIndex }))), import_draft_expires_at: updated.import_draft_expires_at } as ProtocolImportResult, 'Settings applied. Run replay validation again before saving normally.')
            }}
            settingsDisabled={asyncAPISelectedScriptSaved || Boolean(savedResult && !asyncAPIMultiScript)}
            sharedValues={asyncAPISelectedScriptSaved ? undefined : result.shared_values}
            sharedValueScripts={generatedScripts}
            onSharedValuesApplied={handleSharedValuesApplied}
            secretBindingRequirements={secretBindingRequirements}
            sourceMigrationRequirements={asyncAPIArchiveSecretBindingRequirements}
            preserveSourceSessionAfterApply={asyncAPIMultiScript && asyncAPIArchiveSecretBindingRequirements.length > secretBindingRequirements.length}
            onSecretBindingsApply={handleSecretBindingsApply}
            projectId={projectId}
            sourceSecretSessionId={result.source_secret_session_id}
            sourceSecretSessionExpiresAt={result.source_secret_session_expires_at}
            sourceSecretMigrationAllowed={result.source_secret_migration_allowed}
            onSourceSecretSessionSettled={asyncAPIMultiScript ? handleSourceSecretSessionSettled : undefined}
            protectedImportSessionExpiresAt={result.import_session_id ? result.expires_at : undefined}
            importDraftExpiresAt={result.import_draft_expires_at}
            credentialReviewRef={credentialReviewRef}
            onCredentialsPreparedChange={handleCredentialsPreparedChange}
            onReviewBusyChange={handleInternalReviewBusyChange}
            onCredentialActionStart={captureProtectedSaveTarget}
            saving={saveTask.pending || persistenceInProgress}
            reviewBusy={saveAllPreparing || saveTask.pending || persistenceInProgress}
            saveDisabled={(Boolean(savedResult) && !asyncAPIMultiScript) || asyncAPISelectedSaveDisabled || (kind === 'postman' && postmanSaveMode === 'replace' && !postmanReplaceTarget.scriptId) || (kind === 'openapi' && openAPISaveMode === 'replace' && !openAPIReplaceTarget.scriptId)}
            saveDisabledReason={savedResult && !asyncAPIMultiScript ? 'This import is already saved. Open it above or import more files.' : scriptImportSaveDisabledReason}
            onSave={asyncAPIPrepareSelectedForBatch
              ? handlePrepareAsyncAPICredentialsForBatch
              : asyncAPIFinalCredentialBatchSave
                ? handleSaveAllAsyncAPIScripts
                : handleSave}
            onEdit={onEdit && !savedResult && !asyncAPISelectedScriptSaved && generatedScripts.length === 1 ? handleEditReviewedScript : undefined}
            editLabel={editRequiresCredentialPreservingSave ? 'Save and Open in Visual Editor' : 'Open Draft in Visual Editor'}
            sourceFocusRequest={auraSourceFocus}
            previewAside={kind === 'recorder-export'
              ? <SelectorCandidatesReview script={reviewedScript} onPromoteCandidate={handlePromoteRecorderCandidate} />
              : sourceReview ? <SourceMappingReview sourceReview={sourceReview} script={reviewedScript} title="Source Comparison" /> : undefined}
          >
            {kind === 'recorder-export' && (
              <RecorderStepMappingReview summary={result.summary as Record<string, unknown> | undefined} />
            )}

            {replayPreflight && (
              <div className="sample-import-preflight">
                <ReplayPreflightCard preflight={replayPreflight} />
              </div>
            )}
            {kind === 'recorder-export' && reviewedScript && (
              <div className="sample-import-review-block">
                <h4>Replay Validation</h4>
                <div className="sample-import-actions compact">
                  <button className="btn btn-secondary btn-sm" type="button" onClick={handleRunRecorderReplay} disabled={replayTask.pending || recorderReplayStatus === 'running'}>
                    {replayTask.pending || recorderReplayStatus === 'running' ? 'Starting Replay...' : 'Run Replay Validation'}
                  </button>
                  <label className="sample-toggle" title="Save the import as a draft even if replay has not passed yet.">
                    <input type="checkbox" checked={allowRecorderDraftSave} onChange={event => {
                      allowRecorderDraftSaveRef.current = event.target.checked
                      setAllowRecorderDraftSave(event.target.checked)
                    }} />
                    Save as draft needing replay review
                  </label>
                </div>
                {recorderReplaySessionId && (
                  <BrowserReplayProgress
                    replaySessionId={recorderReplaySessionId}
                    projectId={projectId}
                    onComplete={report => handleRecorderReplayComplete(recorderReplaySessionId, report)}
                    onApplySuggestion={handleApplyRecorderReplaySuggestion}
                  />
                )}
                {recorderReplayStatus === 'completed' && <p className="sample-import-success">Replay validation passed.</p>}
                {recorderReplayStatus === 'failed' && <p className="sample-import-warning">Replay validation found issues. Review fixes or save explicitly as a draft.</p>}
                {recorderDraftNotice && <p className="sample-import-warning">{recorderDraftNotice}</p>}
              </div>
            )}
            {kind === 'asyncapi' && (
              <div className="sample-import-review-block">
                <h4>AsyncAPI Replay Review</h4>
                <ImportEvidenceFacts facts={[
                  { label: 'protocol', value: reviewedScript.protocol },
                  { label: 'adapter', value: reviewedScript.protocol === 'kafka' ? (reviewedScript.config.kafka?.mock === false ? 'Live' : 'Mock') : (reviewedScript.config.jms?.mock === false ? 'Live' : 'Mock') },
                  { label: 'request reply', value: (stats?.request_reply_actions as number | undefined) ?? 0 },
                  { label: 'correlations', value: (stats?.messages_with_correlation as number | undefined) ?? 0 },
                  { label: 'skipped operations', value: (stats?.skipped_operations as string[] | undefined)?.length ?? 0 }
                ]} />
                {(stats?.brokers as string[] | undefined)?.length ? (
                  <ul className="sample-import-findings">
                    {(stats!.brokers as string[]).map(broker => <li key={broker}><strong>Broker</strong> {broker}</li>)}
                  </ul>
                ) : null}
                {(stats?.server_protocols as string[] | undefined)?.length ? (
                  <ul className="sample-import-findings">
                    {(stats!.server_protocols as string[]).map(protocol => <li key={protocol}><strong>Server protocol</strong> {protocol}</li>)}
                  </ul>
                ) : null}
                {(stats?.schema_formats_detected as string[] | undefined)?.length ? (
                  <ul className="sample-import-findings">
                    {(stats!.schema_formats_detected as string[]).map(format => <li key={format}><strong>Schema format</strong> {format}</li>)}
                  </ul>
                ) : null}
                {(stats?.skipped_operations as string[] | undefined)?.length ? (
                  <ul className="sample-import-findings">
                    {(stats!.skipped_operations as string[]).map(item => <li key={item}><strong>Skipped</strong> {item}</li>)}
                  </ul>
                ) : null}
              </div>
            )}
            {kind === 'openapi' && openAPICorrelations.length > 0 && (
              <div className="sample-import-review-block">
                <h4>Correlation Review</h4>
                <ul className="sample-import-findings">
                  {openAPICorrelations.slice(0, 8).map(correlation => (
                    <li key={`${correlation.name}-${correlation.found_in_step}`}>
                      <strong>{correlation.name}</strong> step {correlation.found_in_step} → {correlation.used_in_steps.join(', ')} <code>{correlation.expression}</code>
                    </li>
                  ))}
                </ul>
              </div>
            )}
            {kind === 'openapi' && (
              <div className="sample-import-review-block">
                <h4>Save Target</h4>
                <div className="sample-import-grid">
                  <label className="sample-import-field">
                    <span>Mode</span>
                    <select value={openAPISaveMode} disabled={saveTargetLocked} onChange={event => setOpenAPISaveMode(event.target.value as 'create' | 'replace')}>
                      <option value="create">Create new script</option>
                      <option value="replace">Replace existing script</option>
                    </select>
                  </label>
                  {openAPISaveMode === 'replace' && (
                    <>
                      <label className="sample-import-field">
                        <span>Find script</span>
                        <input
                          type="search"
                          value={openAPIReplaceTarget.search}
                          disabled={saveTargetLocked}
                          onChange={event => {
                            openAPIReplaceTarget.setSearch(event.target.value)
                            openAPIReplaceTarget.setOffset(0)
                            openAPIReplaceTarget.setScriptId('')
                          }}
                          placeholder="Search by name or reference"
                        />
                      </label>
                      <label className="sample-import-field">
                        <span>Script</span>
                        <select value={openAPIReplaceTarget.scriptId} disabled={saveTargetLocked} onChange={event => openAPIReplaceTarget.setScriptId(event.target.value)}>
                          <option value="">Select script</option>
                          {openAPIReplaceTarget.scripts.map(script => (
                            <option key={script.id} value={script.id}>{script.name}</option>
                          ))}
                        </select>
                      </label>
                      <nav className="sample-import-script-pagination" aria-label="OpenAPI replacement script pages">
                        <button
                          type="button"
                          disabled={saveTargetLocked || openAPIReplaceTarget.offset === 0}
                          onClick={() => {
                            openAPIReplaceTarget.setOffset(Math.max(0, openAPIReplaceTarget.offset - REPLACE_TARGET_PAGE_SIZE))
                            openAPIReplaceTarget.setScriptId('')
                          }}
                        >
                          Previous
                        </button>
                        <span>
                          {openAPIReplaceTarget.count === 0
                            ? 'No matching scripts'
                            : `Showing ${openAPIReplaceTarget.offset + 1}–${Math.min(openAPIReplaceTarget.offset + openAPIReplaceTarget.scripts.length, openAPIReplaceTarget.count)} of ${openAPIReplaceTarget.count}`}
                        </span>
                        <button
                          type="button"
                          disabled={saveTargetLocked || openAPIReplaceTarget.offset + openAPIReplaceTarget.scripts.length >= openAPIReplaceTarget.count}
                          onClick={() => {
                            openAPIReplaceTarget.setOffset(openAPIReplaceTarget.offset + REPLACE_TARGET_PAGE_SIZE)
                            openAPIReplaceTarget.setScriptId('')
                          }}
                        >
                          Next
                        </button>
                      </nav>
                      <label className="sample-import-field">
                        <span>Replace Mode</span>
                        <select value={openAPIReplaceMode} disabled={saveTargetLocked} onChange={event => setOpenAPIReplaceMode(event.target.value as 'full' | 'merge_by_operation_id')}>
                          <option value="full">Full replacement</option>
                          <option value="merge_by_operation_id">Merge by operation id</option>
                        </select>
                      </label>
                    </>
                  )}
                </div>
              </div>
            )}
            {kind === 'postman' && (
              <div className="sample-import-review-block">
                <h4>Save Target</h4>
                <div className="sample-import-grid">
                  <label className="sample-import-field">
                    <span>Mode</span>
                    <select value={postmanSaveMode} disabled={saveTargetLocked} onChange={event => setPostmanSaveMode(event.target.value as 'create' | 'replace')}>
                      <option value="create">Create new script</option>
                      <option value="replace">Replace existing script</option>
                    </select>
                  </label>
                  {postmanSaveMode === 'replace' && (
                    <>
                      <label className="sample-import-field">
                        <span>Find script</span>
                        <input
                          type="search"
                          value={postmanReplaceTarget.search}
                          disabled={saveTargetLocked}
                          onChange={event => {
                            postmanReplaceTarget.setSearch(event.target.value)
                            postmanReplaceTarget.setOffset(0)
                            postmanReplaceTarget.setScriptId('')
                          }}
                          placeholder="Search by name or reference"
                        />
                      </label>
                      <label className="sample-import-field">
                        <span>Script</span>
                        <select value={postmanReplaceTarget.scriptId} disabled={saveTargetLocked} onChange={event => postmanReplaceTarget.setScriptId(event.target.value)}>
                          <option value="">Select script</option>
                          {postmanReplaceTarget.scripts.map(script => (
                            <option key={script.id} value={script.id}>{script.name}</option>
                          ))}
                        </select>
                      </label>
                      <nav className="sample-import-script-pagination" aria-label="Postman replacement script pages">
                        <button
                          type="button"
                          disabled={saveTargetLocked || postmanReplaceTarget.offset === 0}
                          onClick={() => {
                            postmanReplaceTarget.setOffset(Math.max(0, postmanReplaceTarget.offset - REPLACE_TARGET_PAGE_SIZE))
                            postmanReplaceTarget.setScriptId('')
                          }}
                        >
                          Previous
                        </button>
                        <span>
                          {postmanReplaceTarget.count === 0
                            ? 'No matching scripts'
                            : `Showing ${postmanReplaceTarget.offset + 1}–${Math.min(postmanReplaceTarget.offset + postmanReplaceTarget.scripts.length, postmanReplaceTarget.count)} of ${postmanReplaceTarget.count}`}
                        </span>
                        <button
                          type="button"
                          disabled={saveTargetLocked || postmanReplaceTarget.offset + postmanReplaceTarget.scripts.length >= postmanReplaceTarget.count}
                          onClick={() => {
                            postmanReplaceTarget.setOffset(postmanReplaceTarget.offset + REPLACE_TARGET_PAGE_SIZE)
                            postmanReplaceTarget.setScriptId('')
                          }}
                        >
                          Next
                        </button>
                      </nav>
                    </>
                  )}
                </div>
              </div>
            )}

            {kind === 'har' && harSummary && (
              <div className="sample-import-review-block">
                <h4>HAR Artifact Review</h4>
                <ImportEvidenceFacts facts={[
                  { label: 'artifact mode', value: harSummary.artifact_mode },
                  { label: 'sensitivity', value: harSummary.artifact_sensitivity || harSummary.governance?.sensitivity || 'unknown' },
                  { label: 'retention days', value: harSummary.retention_days ?? harSummary.governance?.retention_days ?? 0 },
                  ...(harDiff ? [{ label: 'dynamic candidates', value: harDiff.dynamic_candidates }, { label: 'static candidates', value: harDiff.static_candidates }] : [])
                ]} />
                {harDiff?.classifications?.length ? (
                  <ul className="sample-import-findings">
                    {harDiff.classifications.slice(0, 5).map(item => (
                      <li key={`${item.name}-${item.classification}-${item.found_in_step || 0}`}>
                        <strong>{item.name}</strong> {item.classification} ({Math.round((item.diff_confidence || 0) * 100)}%)
                      </li>
                    ))}
                  </ul>
                ) : null}
              </div>
            )}
            {kind === 'har' && salesforceReview && (
              <div className="sample-import-review-block">
                <h4>Salesforce Native Review</h4>
                {salesforceReview.detected && (
                  <div className="sample-import-actions compact">
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm"
                      onClick={() => handleHARSalesforceNativeConvert(false)}
                      disabled={convertTask.pending || !file || !harSalesforceNative}
                    >
                      {convertTask.pending && harSalesforceNative ? 'Converting...' : 'Force Generic HAR'}
                    </button>
                    <button
                      type="button"
                      className="btn btn-secondary btn-sm"
                      onClick={() => handleHARSalesforceNativeConvert(true)}
                      disabled={convertTask.pending || !file || harSalesforceNative}
                    >
                      {convertTask.pending && !harSalesforceNative ? 'Converting...' : 'Use Native Salesforce Draft'}
                    </button>
                  </div>
                )}
                <AuraOutcomeReview report={salesforceReview.aura_outcomes} sourceReview={sourceReview || undefined} onViewSource={mapping => setAuraSourceFocus(previous => ({ revision: (previous?.revision || 0) + 1, title: 'Captured Aura request', source: mapping.id, description: 'Inspect the existing source mapping for this captured request.' }))} />
                <ImportEvidenceFacts facts={[
                  { label: 'traffic match', value: salesforceReview.detected ? 'Detected' : 'Needs review' },
                  { label: 'HAR coverage', value: `${Math.round(salesforceReview.har_match_coverage?.coverage_pct || 0)}%` },
                  { label: 'matched requests', value: `${salesforceReview.har_match_coverage?.matched_requests || 0} / ${salesforceReview.har_match_coverage?.imported_requests || 0}` },
                  { label: 'domains', value: salesforceReview.domains?.length || 0 },
                  { label: 'Aura calls', value: endpointTotal(salesforceReview, 'aura_endpoints') },
                  { label: 'Lightning calls', value: endpointTotal(salesforceReview, 'lightning_endpoints') }
                ]} />
                {(salesforceReview.domains?.length || 0) > 0 && (
                  <ul className="sample-import-findings">
                    {salesforceReview.domains!.slice(0, 5).map(domain => (
                      <li key={`${domain.host}-${domain.kind}`}>
                        <strong>{domain.host}</strong> {domain.kind} ({domain.count} request{domain.count === 1 ? '' : 's'})
                      </li>
                    ))}
                  </ul>
                )}
                {(salesforceReview.transactions?.length || 0) > 0 && (
                  <ul className="sample-import-findings">
                    {salesforceReview.transactions!.slice(0, 5).map(tx => (
                      <li key={`${tx.name}-${tx.source}`}>
                        <strong>{tx.name}</strong> {tx.request_count} request{tx.request_count === 1 ? '' : 's'}{tx.aura_actions ? ` / ${tx.aura_actions} Aura action${tx.aura_actions === 1 ? '' : 's'}` : ''}
                      </li>
                    ))}
                  </ul>
                )}
                {salesforceApiSurfaceBreakdown(salesforceReview).length > 0 && (
                  <>
                    <p className="sample-import-subtitle">API surface</p>
                    <ul className="sample-import-findings">
                      {salesforceApiSurfaceBreakdown(salesforceReview).map(surface => (
                        <li key={surface.key}>
                          <strong>{surface.label}</strong> {surface.calls} call{surface.calls === 1 ? '' : 's'} across {surface.endpoints} endpoint{surface.endpoints === 1 ? '' : 's'}
                        </li>
                      ))}
                    </ul>
                  </>
                )}
                {(salesforceReview.parameterizations?.length || 0) > 0 && (
                  <>
                    <p className="sample-import-subtitle">Aura parameterization candidates</p>
                    <ul className="sample-import-findings">
                      {salesforceReview.parameterizations!.slice(0, 6).map(param => (
                        <li key={param.name}>
                          <strong>{param.param_key}</strong> ({param.kind}) → <code>{param.template}</code>
                          {param.decoded_field_path && <AuraParameterLocationDetails location={param} />}
                        </li>
                      ))}
                    </ul>
                  </>
                )}
                {(salesforceReview.blockers?.length || salesforceReview.next_actions?.length) ? (
                  <ul className="sample-import-findings">
                    {salesforceReview.blockers?.slice(0, 4).map(item => <li key={`blocker-${item}`}><strong>Blocker</strong> {item}</li>)}
                    {salesforceReview.next_actions?.slice(0, 4).map(item => <li key={`next-${item}`}><strong>Next action</strong> {item}</li>)}
                  </ul>
                ) : null}
              </div>
            )}
            {kind === 'raml' && (ramlSummary || stats) && (
              <div className="sample-import-review-block">
                <h4>RAML Import Review</h4>
                <ImportEvidenceFacts facts={[
                  { label: 'resources', value: (stats?.total_resources as number | undefined) ?? ramlSummary?.resources?.length ?? 0 },
                  { label: 'methods', value: (stats?.total_methods as number | undefined) ?? 0 },
                  { label: 'request bodies', value: (stats?.request_bodies as number | undefined) ?? 0 },
                  { label: 'auth schemes', value: ramlSummary?.auth_schemes?.length || 0 },
                  ...(ramlSummary?.base_url ? [{ label: 'Base URL', value: ramlSummary.base_url }] : [])
                ]} />
                {(ramlSummary?.resources?.length || 0) > 0 && (
                  <ul className="sample-import-findings">
                    {ramlSummary!.resources!.slice(0, 6).map(resource => (
                      <li key={resource}><strong>{resource}</strong> converted from RAML resources</li>
                    ))}
                  </ul>
                )}
                {(ramlSummary?.auth_schemes?.length || 0) > 0 && (
                  <ul className="sample-import-findings">
                    {ramlSummary!.auth_schemes!.map(scheme => (
                      <li key={scheme}><strong>{scheme}</strong> mapped into OAuth2 token setup when token metadata was present</li>
                    ))}
                  </ul>
                )}
                {(ramlSummary?.policies?.length || 0) > 0 && (
                  <ul className="sample-import-findings">
                    {ramlSummary!.policies!.map(policy => (
                      <li key={policy.policy}><strong>{policy.policy}</strong> — {policy.detail}{policy.scheme ? ` (${policy.scheme})` : ''}</li>
                    ))}
                  </ul>
                )}
              </div>
            )}
            {kind === 'cloudtest' && cloudTestResult && (
              <div className="sample-import-review-block">
                <h4>CloudTest Fixture-Gated Review</h4>
                <p className="sample-import-subtitle">
                  Every imported element is preserved, but all Clip actions remain execution-blocked until sanitized golden fixtures prove the CloudTest wire shape.
                </p>
                <ImportEvidenceFacts facts={[
                  { label: 'Clip drafts', value: cloudTestResult.stats?.clips ?? 0 },
                  { label: 'Composition drafts', value: cloudTestResult.stats?.compositions ?? 0 },
                  { label: 'recognized elements', value: cloudTestResult.stats?.recognized_elements ?? 0 },
                  { label: 'unknown elements', value: cloudTestResult.stats?.unknown_elements ?? 0 }
                ]} />
                {(cloudTestResult.scenario_drafts?.length || 0) > 0 && (
                  <ul className="sample-import-findings">
                    {cloudTestResult.scenario_drafts!.slice(0, 8).map(scenario => (
                      <li key={`${scenario.source_file}-${scenario.source_path}`}>
                        <strong>{scenario.name}</strong> — {scenario.groups.length} track{scenario.groups.length === 1 ? '' : 's'}; review required
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            )}
            {mulesoftReview && (
              <div className="sample-import-review-block">
                <h4>MuleSoft / Anypoint Review</h4>
                {(mulesoftReview.domains?.length || 0) > 0 && (
                  <ul className="sample-import-findings">
                    {mulesoftReview.domains!.slice(0, 5).map(domain => (
                      <li key={`${domain.host}-${domain.kind}`}><strong>{domain.host}</strong> {domain.kind} ({domain.count} request{domain.count === 1 ? '' : 's'})</li>
                    ))}
                  </ul>
                )}
                {(mulesoftReview.policy_findings?.length || 0) > 0 && (
                  <>
                    <p className="sample-import-subtitle">Policies</p>
                    <ul className="sample-import-findings">
                      {mulesoftReview.policy_findings!.map((policy, index) => <li key={`policy-${index}`}><strong>{policy.policy}</strong> {policy.detail}</li>)}
                    </ul>
                  </>
                )}
                {(mulesoftReview.auth_findings?.length || 0) > 0 && (
                  <>
                    <p className="sample-import-subtitle">Auth</p>
                    <ul className="sample-import-findings">
                      {mulesoftReview.auth_findings!.map((auth, index) => <li key={`auth-${index}`}><strong>{auth.type}</strong> {auth.detail}</li>)}
                    </ul>
                  </>
                )}
                {(mulesoftReview.blockers?.length || mulesoftReview.next_actions?.length) ? (
                  <ul className="sample-import-findings">
                    {mulesoftReview.blockers?.slice(0, 4).map(item => <li key={`blocker-${item}`}><strong>Blocker</strong> {item}</li>)}
                    {mulesoftReview.next_actions?.slice(0, 4).map(item => <li key={`next-${item}`}><strong>Next action</strong> {item}</li>)}
                  </ul>
                ) : null}
              </div>
            )}
            {agentforceReview && (
              <div className="sample-import-review-block">
                <h4>Agentforce Agent API Review {agentforceReview.streaming ? <span className="tag-chip">streaming (SSE)</span> : null}</h4>
                {agentforceTransformChanges.length > 0 && (
                  <>
                    <p className="sample-import-subtitle">Applied replay-readiness changes ({agentforceTransformChanges.length})</p>
                    <ul className="sample-import-findings">
                      {agentforceTransformChanges.map((change, index) => (
                        <li key={`af-change-${index}`}><strong>{change.step}</strong>{change.action_name ? ` — ${change.action_name}` : ''}: {change.detail}</li>
                      ))}
                    </ul>
                  </>
                )}
                {(agentforceReview.lifecycle?.length || 0) > 0 && (
                  <>
                    <p className="sample-import-subtitle">Conversation lifecycle</p>
                    <ul className="sample-import-findings">
                      {agentforceReview.lifecycle!.map((step, index) => (
                        <li key={`step-${index}`}><strong>{step.step}</strong> {step.method} {step.path}</li>
                      ))}
                    </ul>
                  </>
                )}
                {(agentforceReview.session_vars?.length || 0) > 0 && (
                  <>
                    <p className="sample-import-subtitle">Correlation</p>
                    <ul className="sample-import-findings">
                      {agentforceReview.session_vars!.map((sessionVar, index) => (
                        <li key={`var-${index}`}><strong>{sessionVar.name}</strong> {sessionVar.type} <code>{sessionVar.expression}</code></li>
                      ))}
                    </ul>
                  </>
                )}
                {(agentforceReview.assertion_hints?.length || 0) > 0 && (
                  <ul className="sample-import-findings">
                    {agentforceReview.assertion_hints!.map((hint, index) => <li key={`hint-${index}`}><strong>Assertion</strong> {hint.detail}</li>)}
                  </ul>
                )}
                {(agentforceReview.blockers?.length || agentforceReview.next_actions?.length) ? (
                  <ul className="sample-import-findings">
                    {agentforceReview.blockers?.slice(0, 4).map(item => <li key={`blocker-${item}`}><strong>Blocker</strong> {item}</li>)}
                    {agentforceReview.next_actions?.slice(0, 4).map(item => <li key={`next-${item}`}><strong>Next action</strong> {item}</li>)}
                  </ul>
                ) : null}
              </div>
            )}
            <ImportConversionDetails facts={stats ? Object.entries(stats)
              .filter((entry): entry is [string, number] => typeof entry[1] === 'number')
              .map(([key, value]) => ({ label: key.replace(/_/g, ' '), value })) : []} />
          </ScriptImportReview>
        </div>
      )}
    </div>
  )
}
