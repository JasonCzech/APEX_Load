import { useCallback, useEffect, useRef, useState } from 'react'

import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { type BrowserReplayReport, type BrowserReplayStartResponse, type ReplayRepairSuggestion, type StoredScript } from '../types'
import { type AgentforceReview, type AgentforceTransformChange, type AsyncAPIImportResult, type AsyncAPISupportMatrixEntry, type BrokerConfigImportResult, type BrokerConfigSupportMatrixEntry, type BrowserRecordingImportResult, type DevWebImportResult, type DevWebSupportMatrixEntry, type EventHubsCaptureImportResult, type EventHubsCaptureSupportMatrixEntry, type HARImportResult, type HARImportSummary, type K6ImportResult, type K6SupportMatrixEntry, type MulesoftReview, type OpenAPICorrelation, type OpenAPIImportResult, type OpenAPISupportMatrixEntry, type PostmanImportResult, type PostmanSupportMatrixEntry, type RAMLImportResult, type RAMLSupportMatrixEntry, type SalesforceImportResult, type SalesforceReview, type SecretBindingSelection, type WSDLImportResult } from '../types/scriptImports'
import { type ScriptDefinition, type SourceReview } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'
import ImportRuntimeOptions from './ImportRuntimeOptions'
import BrowserReplayProgress from './BrowserReplayProgress'
import ReplayPreflightCard from './ReplayPreflightCard'
import { type SecretBindingReviewHandle } from './SecretBindingReview'
import ScriptImportReview from './ScriptImportReview'
import SelectorCandidatesReview, { type SelectorCandidate } from './SelectorCandidatesReview'
import SourceMappingReview from './SourceMappingReview'
import { withFallbackPersistedScript } from './importSaveUtils'
import { salesforceApiSurfaceBreakdown, salesforceEndpointTotal } from './salesforceReviewSummary'
import { buildReplayStartPayload, createReplaySessionId, isDefiniteReplayStartRejection } from './scriptEditorUtils'
import { refreshedSecretBindingRequirements, replaceSecretBindingRequirementsForScript, secretBindingRequirementsForScript } from './secretBindingUtils'
import {
  activeProtectedImportSessionId,
  invalidateProtectedImportCandidate,
  mergeProtectedParameterizationSelectionIds,
} from './protectedImportSession'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import './SampleScriptImport.css'

export type ImportKind = 'devweb' | 'har' | 'recorder-export' | 'openapi' | 'raml' | 'postman' | 'wsdl' | 'asyncapi' | 'k6' | 'eventhubs-capture' | 'broker-config'

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
  if (file.size > IMPORT_FILE_MAX_BYTES) {
    return `${file.name} is ${formatBytes(file.size)}; imports are limited to ${formatBytes(IMPORT_FILE_MAX_BYTES)}.`
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

type ProtocolImportResult = DevWebImportResult | HARImportResult | SalesforceImportResult | BrowserRecordingImportResult | OpenAPIImportResult | RAMLImportResult | PostmanImportResult | WSDLImportResult | AsyncAPIImportResult | K6ImportResult | EventHubsCaptureImportResult | BrokerConfigImportResult
type SourceImportSupportEntry = DevWebSupportMatrixEntry | OpenAPISupportMatrixEntry | RAMLSupportMatrixEntry | PostmanSupportMatrixEntry | AsyncAPISupportMatrixEntry | K6SupportMatrixEntry | EventHubsCaptureSupportMatrixEntry | BrokerConfigSupportMatrixEntry

type ProtectedImportCommitOverrides = {
  sessionId: string
  secretBindings: Record<string, string>
  parameterizationSelectedIds: string[]
}

type SourceImportSupportState = {
  entries: SourceImportSupportEntry[]
  notes: string[]
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

function sourceImportSupportHasLimitations(support: SourceImportSupportState | null): support is SourceImportSupportState {
  if (!support || support.entries.length === 0) return false
  return support.notes.length > 0 || support.entries.some(entry => (
    sourceImportCoverageKey(entry) !== 'supported' || Boolean(entry.review_required) || Boolean(entry.notes)
  ))
}

function sourceImportSupportForKind(
  kind: ImportKind,
  support: Partial<Record<ImportKind, SourceImportSupportState | null>>,
): SourceImportSupportState | null {
  return support[kind] || null
}

function SourceImportCoveragePanel({ kind, support }: { kind: ImportKind; support: SourceImportSupportState }) {
  const sections = summarizeSourceImportCoverage(support.entries)
  const protocolTargets = sourceImportProtocolTargets[kind] || []
  if (sections.length === 0) return null
  return (
    <div className="source-import-coverage" aria-label={`${copy[kind].title} coverage`}>
      <div className="source-import-coverage-header">
        <h4>Import Coverage</h4>
        <span>Supported protocols and limitations</span>
      </div>
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
    </div>
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

export default function ProtocolAssetImport({ kind, initialFile, initialMode, projectId, folderId, onImported, onEdit, onClose }: Props) {
  const api = useApi()
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
  const reviewGenerationRef = useRef(0)
  const [reviewGeneration, setReviewGeneration] = useState(0)
  const advanceReviewGeneration = useCallback(() => {
    const next = reviewGenerationRef.current + 1
    reviewGenerationRef.current = next
    setReviewGeneration(next)
    return next
  }, [])
  const reviewScope = `${reviewGeneration}:${projectId || ''}`
  const reviewProjectIdRef = useRef(projectId)
  reviewProjectIdRef.current = projectId
  // The credentials card exposes only non-sensitive CTA/validation metadata;
  // write-only values never cross the component boundary.
  const [preparedCredentialSummary, setPreparedCredentialSummary] = useState<{
    configureLaterCount: number
    valid: boolean
  } | null>(null)
  const handleCredentialsPreparedChange = useCallback((prepared: { configureLaterCount: number; valid?: boolean } | null) => {
    setPreparedCredentialSummary(prepared ? {
      configureLaterCount: prepared.configureLaterCount,
      valid: prepared.valid ?? true,
    } : null)
  }, [])
  const saveAllInFlightRef = useRef(false)
  const saveScriptInFlightRef = useRef<{
    generation: number
    projectId: string | undefined
    scriptIndex: number
    result: ProtocolImportResult
  } | null>(null)
  const [saveAllPreparing, setSaveAllPreparing] = useState(false)
  const internalReviewBusyRef = useRef(false)
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
  const [selectedScriptIndex, setSelectedScriptIndex] = useState(0)
  const [conversionScope, setConversionScope] = useState(0)
  const selectedScriptIndexRef = useRef(selectedScriptIndex)
  selectedScriptIndexRef.current = selectedScriptIndex
  const [secondaryHARFile, setSecondaryHARFile] = useState<File | null>(null)
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
  const [devWebSupport, setDevWebSupport] = useState<SourceImportSupportState | null>(null)
  const [ramlSupport, setRamlSupport] = useState<{ entries: RAMLSupportMatrixEntry[]; notes: string[] } | null>(null)
  const [openAPISupport, setOpenAPISupport] = useState<{ entries: OpenAPISupportMatrixEntry[]; notes: string[] } | null>(null)
  const [postmanSupport, setPostmanSupport] = useState<SourceImportSupportState | null>(null)
  const [asyncAPISupport, setAsyncAPISupport] = useState<{ entries: AsyncAPISupportMatrixEntry[]; notes: string[] } | null>(null)
  const [k6Support, setK6Support] = useState<{ entries: K6SupportMatrixEntry[]; notes: string[] } | null>(null)
  const [eventHubsCaptureSupport, setEventHubsCaptureSupport] = useState<{ entries: EventHubsCaptureSupportMatrixEntry[]; notes: string[] } | null>(null)
  const [brokerConfigSupport, setBrokerConfigSupport] = useState<{ entries: BrokerConfigSupportMatrixEntry[]; notes: string[] } | null>(null)
  const [openAPISaveMode, setOpenAPISaveMode] = useState<'create' | 'replace'>('create')
  const [openAPIReplaceScriptId, setOpenAPIReplaceScriptId] = useState('')
  const [openAPIReplaceMode, setOpenAPIReplaceMode] = useState<'full' | 'merge_by_operation_id'>('full')
  const [openAPIScripts, setOpenAPIScripts] = useState<StoredScript[]>([])

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
  const [postmanSaveMode, setPostmanSaveMode] = useState<'create' | 'replace'>('create')
  const [postmanReplaceScriptId, setPostmanReplaceScriptId] = useState('')
  const [postmanScripts, setPostmanScripts] = useState<StoredScript[]>([])
  const labels = copy[kind]
  const resetConvertTask = convertTask.reset
  const resetSaveTask = saveTask.reset
  const invalidateConversionSource = useCallback(() => {
    void credentialReviewRef.current?.discardSourceSession()
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
    resetSaveTask()
    setSaveAllPreparing(false)
    internalReviewBusyRef.current = false
    setInternalReviewBusy(false)
    setPreparedCredentialSummary(null)
    setMessageError('')
  }, [advanceReviewGeneration, resetConvertTask, resetSaveTask])
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
    if (kind !== 'devweb') return
    let cancelled = false
    api.getDevWebSupportMatrix()
      .then(response => { if (!cancelled) setDevWebSupport({ entries: response.support_matrix || [], notes: response.notes || [] }) })
      .catch(() => { if (!cancelled) setDevWebSupport(null) })
    return () => { cancelled = true }
  }, [api, kind])

  useEffect(() => {
    if (kind !== 'raml') return
    let cancelled = false
    api.getRAMLSupportMatrix()
      .then(response => { if (!cancelled) setRamlSupport({ entries: response.support_matrix || [], notes: response.notes || [] }) })
      .catch(() => { if (!cancelled) setRamlSupport(null) })
    return () => { cancelled = true }
  }, [api, kind])

  useEffect(() => {
    if (kind !== 'openapi') return
    let cancelled = false
    api.getOpenAPISupportMatrix()
      .then(response => { if (!cancelled) setOpenAPISupport({ entries: response.support_matrix || [], notes: response.notes || [] }) })
      .catch(() => { if (!cancelled) setOpenAPISupport(null) })
    return () => { cancelled = true }
  }, [api, kind])

  useEffect(() => {
    if (kind !== 'postman') return
    let cancelled = false
    api.getPostmanSupportMatrix()
      .then(response => { if (!cancelled) setPostmanSupport({ entries: response.support_matrix || [], notes: response.notes || [] }) })
      .catch(() => { if (!cancelled) setPostmanSupport(null) })
    return () => { cancelled = true }
  }, [api, kind])

  useEffect(() => {
    if (kind !== 'asyncapi') return
    let cancelled = false
    api.getAsyncAPISupportMatrix()
      .then(response => { if (!cancelled) setAsyncAPISupport({ entries: response.support_matrix || [], notes: response.notes || [] }) })
      .catch(() => { if (!cancelled) setAsyncAPISupport(null) })
    return () => { cancelled = true }
  }, [api, kind])

  useEffect(() => {
    if (kind !== 'k6') return
    let cancelled = false
    api.getK6SupportMatrix()
      .then(response => { if (!cancelled) setK6Support({ entries: response.support_matrix || [], notes: response.notes || [] }) })
      .catch(() => { if (!cancelled) setK6Support(null) })
    return () => { cancelled = true }
  }, [api, kind])

  useEffect(() => {
    if (kind !== 'eventhubs-capture') return
    let cancelled = false
    api.getEventHubsCaptureSupportMatrix()
      .then(response => { if (!cancelled) setEventHubsCaptureSupport({ entries: response.support_matrix || [], notes: response.notes || [] }) })
      .catch(() => { if (!cancelled) setEventHubsCaptureSupport(null) })
    return () => { cancelled = true }
  }, [api, kind])

  useEffect(() => {
    if (kind !== 'broker-config') return
    let cancelled = false
    api.getBrokerConfigSupportMatrix()
      .then(response => { if (!cancelled) setBrokerConfigSupport({ entries: response.support_matrix || [], notes: response.notes || [] }) })
      .catch(() => { if (!cancelled) setBrokerConfigSupport(null) })
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

  useEffect(() => {
    if (kind !== 'openapi') return
    let cancelled = false
    setOpenAPIScripts([])
    setOpenAPIReplaceScriptId('')
    api.listScripts(projectId)
      .then(response => {
        if (!cancelled) setOpenAPIScripts(response.scripts || [])
      })
      .catch(() => {
        if (!cancelled) setOpenAPIScripts([])
      })
    return () => { cancelled = true }
  }, [api, kind, projectId])

  useEffect(() => {
    if (kind !== 'postman') return
    let cancelled = false
    setPostmanScripts([])
    setPostmanReplaceScriptId('')
    api.listScripts(projectId)
      .then(response => {
        if (!cancelled) setPostmanScripts(response.scripts || [])
      })
      .catch(() => {
        if (!cancelled) setPostmanScripts([])
      })
    return () => { cancelled = true }
  }, [api, kind, projectId])

  const handleConvert = async (mode: 'conversion' | 'validation' = 'conversion', overrides?: { salesforceNative?: boolean }) => {
    if (!file) {
      setMessageError(`Please upload ${uploadNounForKind(kind)}.`)
      return
    }
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
            throw new Error('The import source changed while conversion was running. Review the current input and try again.')
          }
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
      if (requestIsCurrent()) setMessageError(getErrorMessage(error, `${labels.title} failed`))
    }
  }

  const handleHARSalesforceNativeConvert = async (salesforceNative: boolean) => {
    setHARSalesforceNative(salesforceNative)
    await handleConvert('conversion', { salesforceNative })
  }

  const stats = result?.stats as Record<string, unknown> | undefined
  const actionCount = typeof stats?.converted_actions === 'number' ? stats.converted_actions : 0
  const importedScripts = result?.scripts?.length ? result.scripts : result?.script ? [result.script] : []
  // The converted result is the sole reviewed-script state. Selector repair,
  // credential binding, parameterization, runtime edits, Save, and Edit all
  // read and update this same canonical snapshot.
  const generatedScripts = importedScripts
  const reviewedScript = generatedScripts[selectedScriptIndex] || generatedScripts[0] || null
  const selectedSecretBindingRequirements = reviewedScript
    ? secretBindingRequirementsForScript(result?.secret_binding_requirements, selectedScriptIndex, reviewedScript)
    : []
  const secretBindingRequirements = kind === 'asyncapi' && generatedScripts.length > 1
    ? result?.secret_binding_requirements || []
    : selectedSecretBindingRequirements
  const defaultConfigureLaterCount = new Set(secretBindingRequirements
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
  const configureLaterCount = preparedCredentialSummary?.configureLaterCount ?? defaultConfigureLaterCount
  const preparedCredentialsValid = preparedCredentialSummary?.valid ?? true
  const asyncAPIMultiScript = kind === 'asyncapi' && generatedScripts.length > 1
  const asyncAPISelectedSaveLabel = asyncAPIMultiScript
    ? `Save Selected Script (${selectedScriptIndex + 1} of ${generatedScripts.length})`
    : ''
  const asyncAPISaveAllLabel = asyncAPIMultiScript
    ? `Save All ${generatedScripts.length} Scripts${configureLaterCount > 0 ? ` with ${configureLaterCount} secret${configureLaterCount === 1 ? '' : 's'} to configure` : ''}`
    : ''
  const sourceReview = sourceReviewForImport(result, reviewedScript)
  const harSummary: HARImportSummary | undefined = kind === 'har' ? (result as HARImportResult | null | undefined)?.summary : undefined
  const salesforceReview = kind === 'har' ? salesforceReviewForImport(result, reviewedScript) : null
  const mulesoftReview = preservedReviewForImport<MulesoftReview>(reviewedScript, 'mulesoft_review')
  const agentforceReview = preservedReviewForImport<AgentforceReview>(reviewedScript, 'agentforce_review')
  const agentforceTransformChanges = (reviewedScript?.metadata?.import?.preserved?.agentforce_transform as AgentforceTransformChange[] | undefined) ?? []
  const ramlSummary = kind === 'raml' ? (result as RAMLImportResult | null | undefined)?.summary : undefined
  const harDiff = harSummary?.har_diff
  const selectedReplayPreflight = reviewedScript?.metadata?.import?.preserved?.replay_preflight as OpenAPIImportResult['preflight'] | undefined
  const replayPreflight = (generatedScripts.length > 1 ? selectedReplayPreflight : result?.preflight || selectedReplayPreflight) || null
  const openAPICorrelations: OpenAPICorrelation[] = kind === 'openapi' ? ((result as OpenAPIImportResult | null | undefined)?.correlations || []) : []
  const sourceImportSupport = sourceImportSupportForKind(kind, {
    devweb: devWebSupport,
    openapi: openAPISupport,
    raml: ramlSupport,
    postman: postmanSupport,
    asyncapi: asyncAPISupport,
    k6: k6Support,
    'eventhubs-capture': eventHubsCaptureSupport,
    'broker-config': brokerConfigSupport,
  })

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

  const handleSave = async () => {
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
    const currentReviewedScript = currentGeneratedScripts[selectedScriptIndex] || currentGeneratedScripts[0]
    if (!currentResult || !currentReviewedScript || saveAllInFlightRef.current || internalReviewBusyRef.current) return
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
    const currentRequirements = kind === 'asyncapi' && currentGeneratedScripts.length > 1
      ? currentResult?.secret_binding_requirements || []
      : secretBindingRequirementsForScript(currentResult?.secret_binding_requirements, selectedScriptIndex, currentReviewedScript)
    if (currentRequirements.length > 0) {
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
        if (openAPISaveMode === 'replace' && !openAPIReplaceScriptId) {
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
            mode: openAPISaveMode,
            target_script_id: openAPISaveMode === 'replace' ? openAPIReplaceScriptId : undefined,
            replace_mode: openAPISaveMode === 'replace' ? openAPIReplaceMode : undefined,
          }
          const response = await api.commitOpenAPIImport(payload)
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('OpenAPI import commit did not return a stored script')
          return storedScript
        })
        if (requestIsCurrent()) onImported?.({ scripts: [withFallbackPersistedScript(savedScript, currentReviewedScript)] })
        return
      }
      if (kind === 'postman') {
        if (postmanSaveMode === 'replace' && !postmanReplaceScriptId) {
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
            mode: postmanSaveMode,
            target_script_id: postmanSaveMode === 'replace' ? postmanReplaceScriptId : undefined,
          }
          const response = await api.savePostmanImport(payload)
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('Postman import save did not return a stored script')
          return storedScript
        })
        if (requestIsCurrent()) onImported?.({ scripts: [withFallbackPersistedScript(savedScript, currentReviewedScript)] })
        return
      }
      if (kind === 'asyncapi') {
        const savedScript = await saveTask.run(async () => {
          const response = await api.commitAsyncAPIImport({
            script: currentReviewedScript,
            mode: 'create',
            folder_id: currentReviewedScript.folder_id ?? folderId ?? null,
          })
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('AsyncAPI import commit did not return a stored script')
          return storedScript
        })
        if (requestIsCurrent()) onImported?.({ scripts: [withFallbackPersistedScript(savedScript, currentReviewedScript)] })
        return
      }
      const savedScript = await saveTask.run(async () => api.createScript(scriptToSave, sourceProjectId, scriptToSave.folder_id ?? folderId ?? null))
      if (requestIsCurrent()) onImported?.({ scripts: [withFallbackPersistedScript(savedScript, scriptToSave)] })
    } catch (error: unknown) {
      if (requestIsCurrent()) setMessageError(getErrorMessage(error, 'Failed to save generated script'))
    } finally {
      if (saveScriptInFlightRef.current === saveAttempt) {
        saveScriptInFlightRef.current = null
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
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
    const savedScripts: StoredScript[] = []
    saveAllInFlightRef.current = true
    setSaveAllPreparing(true)
    setMessageError('')
    try {
      if ((latestResultRef.current?.secret_binding_requirements?.length ?? 0) > 0) {
        if (!credentialReviewRef.current) {
          setMessageError('Credential preparation is unavailable for these generated scripts.')
          return
        }
        const committed = await credentialReviewRef.current.commit()
        if (!committed || !requestIsCurrent()) return
      }
      const preparedResult = latestResultRef.current
      if ((preparedResult?.secret_binding_requirements?.length ?? 0) > 0) {
        throw new Error('Every generated script must use governed secret references before Save All can begin.')
      }
      const scriptsToSave = preparedResult?.scripts?.length
        ? preparedResult.scripts
        : generatedScripts
      if (scriptsToSave.length !== generatedScripts.length || scriptsToSave.some(script => JSON.stringify(script).includes('[REDACTED]'))) {
        throw new Error('Save All preflight failed because a generated script is stale or still contains a redacted credential sentinel.')
      }
      const sourceResult = preparedResult
      await saveTask.run(async () => {
        for (const script of scriptsToSave) {
          if (!requestIsCurrent() || latestResultRef.current !== sourceResult) {
            throw new Error('The import result changed while scripts were being saved. Review the current draft and try again.')
          }
          const response = await api.commitAsyncAPIImport({
            script,
            mode: 'create',
            folder_id: script.folder_id ?? folderId ?? null,
          })
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('AsyncAPI import commit did not return a stored script')
          savedScripts.push(withFallbackPersistedScript(storedScript, script))
          if (!requestIsCurrent() || latestResultRef.current !== sourceResult) {
            throw new Error('The import result changed while scripts were being saved. Review the current draft and try again.')
          }
        }
        if (!savedScripts[0]) throw new Error('AsyncAPI import commit did not return a stored script')
        return savedScripts[0]
      })
      if (requestIsCurrent() && latestResultRef.current === sourceResult) onImported?.({ scripts: savedScripts })
    } catch (error: unknown) {
      const cleanup = await Promise.allSettled(
        savedScripts.slice().reverse().map(script => api.deleteScript(script.id)),
      )
      const cleanupFailed = cleanup.some(result => result.status === 'rejected')
      if (requestIsCurrent()) {
        const message = getErrorMessage(error, 'Failed to save generated scripts')
        setMessageError(cleanupFailed
          ? `${message} Some partially created scripts could not be removed; review the Script Library before retrying.`
          : savedScripts.length > 0
            ? `${message} Partially created scripts were removed.`
            : message)
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
  ) => {
    if (!result) return
    advanceReviewGeneration()
    const nextScripts = generatedScripts.length ? generatedScripts.map((script, index) => index === selectedScriptIndex ? parameterizedScript : script) : [parameterizedScript]
    const nextResult = { ...result, script: nextScripts[0], scripts: nextScripts } as ProtocolImportResult
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

  const handleSecretBindingsApply = async (bindings: SecretBindingSelection[]) => {
    if (!result || !reviewedScript) return
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration && reviewProjectIdRef.current === sourceProjectId
    const assertCurrent = () => {
      if (!requestIsCurrent()) throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
    }
    if (kind === 'asyncapi' && generatedScripts.length > 1) {
      let resolvedResult = result
      for (let scriptIndex = 0; scriptIndex < generatedScripts.length; scriptIndex += 1) {
        const script = resolvedResult.scripts[scriptIndex]
        if (!script) continue
        const requirements = secretBindingRequirementsForScript(
          resolvedResult.secret_binding_requirements,
          scriptIndex,
          script,
        )
        const requirementIDs = new Set(requirements.map(requirement => requirement.id))
        const scriptBindings = bindings
          .filter(binding => (
            typeof binding.script_index === 'number'
              ? binding.script_index === scriptIndex
              : requirementIDs.has(binding.requirement_id)
          ))
          .map(({ requirement_id, secret_name }) => ({ requirement_id, secret_name }))
        if (scriptBindings.length === 0) continue
        try {
          assertCurrent()
          const resolved = await api.applySecretBindings({
            script,
            script_index: scriptIndex,
            bindings: scriptBindings,
          })
          assertCurrent()
          const nextScripts = resolvedResult.scripts.map((entry, index) => index === scriptIndex ? resolved.script : entry)
          resolvedResult = {
            ...resolvedResult,
            script: nextScripts[0],
            scripts: nextScripts,
            secret_binding_requirements: replaceSecretBindingRequirementsForScript(
              resolvedResult.secret_binding_requirements,
              scriptIndex,
              script,
              resolved.secret_binding_requirements,
            ),
          } as ProtocolImportResult
        } catch (error: unknown) {
          if (!requestIsCurrent()) throw error
          const refreshed = refreshedSecretBindingRequirements(error)
          if (refreshed) {
            resolvedResult = {
              ...resolvedResult,
              secret_binding_requirements: replaceSecretBindingRequirementsForScript(
                resolvedResult.secret_binding_requirements,
                scriptIndex,
                script,
                refreshed,
              ),
            } as ProtocolImportResult
            latestResultRef.current = resolvedResult
            convertTask.setValue(resolvedResult)
          }
          throw error
        }
      }
      assertCurrent()
      commitReviewMutation(resolvedResult, 'Credential bindings updated the draft. Run replay validation again before saving normally.')
      return
    }
    try {
      assertCurrent()
      const resolved = await api.applySecretBindings({
        script: reviewedScript,
        script_index: selectedScriptIndex,
        bindings: bindings.map(({ requirement_id, secret_name }) => ({ requirement_id, secret_name })),
      })
      assertCurrent()
      const nextScripts = generatedScripts.length
        ? generatedScripts.map((script, index) => index === selectedScriptIndex ? resolved.script : script)
        : [resolved.script]
      const nextResult = {
        ...result,
        script: nextScripts[0],
        scripts: nextScripts,
        secret_binding_requirements: replaceSecretBindingRequirementsForScript(
          result.secret_binding_requirements,
          selectedScriptIndex,
          reviewedScript,
          resolved.secret_binding_requirements,
        ),
      } as ProtocolImportResult
      const sessionId = activeProtectedImportSessionId(result)
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
          ...result,
          secret_binding_requirements: replaceSecretBindingRequirementsForScript(
            result.secret_binding_requirements,
            selectedScriptIndex,
            reviewedScript,
            refreshed,
          ),
        } as ProtocolImportResult
        latestResultRef.current = refreshedResult
        convertTask.setValue(refreshedResult)
      }
      throw error
    }
  }

  const handleRuntimeOptionsChanged = (updatedScript: ScriptDefinition) => {
    if (!result) return
    advanceReviewGeneration()
    const nextScripts = generatedScripts.length ? generatedScripts.map((script, index) => index === selectedScriptIndex ? updatedScript : script) : [updatedScript]
    const nextResult = { ...result, script: nextScripts[0], scripts: nextScripts } as ProtocolImportResult
    commitReviewMutation(nextResult, 'Runtime settings updated the draft. Run replay validation again before saving normally.')
  }

  return (
    <div className="sample-import">
      <div className="sample-import-header">
        <div>
          <h2>{labels.title}</h2>
          <p className="sample-import-subtitle">{labels.subtitle}</p>
        </div>
        {onClose && <button className="btn btn-secondary" onClick={() => { void credentialReviewRef.current?.discardSourceSession(); onClose() }}>Back</button>}
      </div>

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
                />
              </div>
              <label className="sample-import-field">
                <span>Default Think Time (ms)</span>
                <input type="number" min={0} value={postmanThinkTimeMs} onChange={event => { invalidateConversionSource(); setPostmanThinkTimeMs(Math.max(0, Number(event.target.value) || 0)) }} />
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={postmanSanitizeSensitive} onChange={event => { invalidateConversionSource(); setPostmanSanitizeSensitive(event.target.checked) }} />
                Scrub sensitive fields
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={postmanIncludeAssertions} onChange={event => { invalidateConversionSource(); setPostmanIncludeAssertions(event.target.checked) }} />
                Add status assertions
              </label>
              <label className="sample-toggle sample-import-span-2" title="Bind detected parameters to a generated CSV data source so each virtual user replays with varying data.">
                <input type="checkbox" checked={postmanUseDataSource} onChange={event => { invalidateConversionSource(); setPostmanUseDataSource(event.target.checked) }} />
                Generate parameter data source
              </label>
            </>
          )}
          {supportsScriptName && (
            <label className="sample-import-field">
              <span>Script Name</span>
              <input value={name} onChange={event => { invalidateConversionSource(); setName(event.target.value) }} placeholder={scriptNamePlaceholder(kind)} />
            </label>
          )}
          {supportsBaseURL && (
            <label className="sample-import-field">
              <span>Base URL Override</span>
              <input value={baseURL} onChange={event => { invalidateConversionSource(); setBaseURL(event.target.value) }} placeholder="https://api.example.com" />
            </label>
          )}
          {kind === 'asyncapi' && (
            <>
              <label className="sample-import-field">
                <span>Broker Override</span>
                <input value={asyncAPIBrokers} onChange={event => { invalidateConversionSource(); setAsyncAPIBrokers(event.target.value) }} placeholder="broker-a:9092, broker-b:9092" />
              </label>
              <label className="sample-import-field">
                <span>Channel Filter</span>
                <input value={asyncAPIChannels} onChange={event => { invalidateConversionSource(); setAsyncAPIChannels(event.target.value) }} placeholder="orders.created, payments.*" />
              </label>
              <label className="sample-import-field">
                <span>Serialization</span>
                <select value={asyncAPISerialization} onChange={event => { invalidateConversionSource(); setAsyncAPISerialization(event.target.value) }}>
                  <option value="">From spec / raw</option>
                  <option value="json_schema">JSON Schema</option>
                  <option value="avro">Avro</option>
                </select>
              </label>
              <label className="sample-import-field">
                <span>Schema Registry URL</span>
                <input value={asyncAPISchemaRegistryURL} onChange={event => { invalidateConversionSource(); setAsyncAPISchemaRegistryURL(event.target.value) }} placeholder="https://registry.example.com" />
              </label>
              <label className="sample-import-field">
                <span>Schema ID</span>
                <input type="number" min={0} value={asyncAPISchemaID} onChange={event => { invalidateConversionSource(); setAsyncAPISchemaID(event.target.value) }} placeholder="42" />
              </label>
              <label className="sample-import-field">
                <span>Subject</span>
                <input value={asyncAPISubject} onChange={event => { invalidateConversionSource(); setAsyncAPISubject(event.target.value) }} placeholder="orders-value" />
              </label>
              <label className="sample-import-field">
                <span>Think Time (ms)</span>
                <input type="number" min={0} value={asyncAPIThinkTimeMs} onChange={event => { invalidateConversionSource(); setAsyncAPIThinkTimeMs(Math.max(0, Number(event.target.value) || 0)) }} />
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={asyncAPIMock} onChange={event => { invalidateConversionSource(); setAsyncAPIMock(event.target.checked) }} />
                Use mock broker
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={asyncAPIIncludeAssertions} onChange={event => { invalidateConversionSource(); setAsyncAPIIncludeAssertions(event.target.checked) }} />
                Include replay assertions
              </label>
            </>
          )}
          {kind === 'wsdl' && (
            <label className="sample-import-field">
              <span>SOAP Version</span>
              <select value={soapVersion} onChange={event => { invalidateConversionSource(); setSoapVersion(event.target.value) }}>
                <option value="1.1">SOAP 1.1</option>
                <option value="1.2">SOAP 1.2</option>
              </select>
            </label>
          )}
          {kind === 'har' && (
            <>
              <label className="sample-import-field sample-import-span-2">
                <span>Comparison HAR</span>
                <input type="file" accept={labels.accept} onChange={event => { invalidateConversionSource(); setSecondaryHARFile(event.target.files?.[0] || null) }} />
                <small>{secondaryHARFile ? secondaryHARFile.name : 'Optional second recording for correlation diff'}</small>
              </label>
              <label className="sample-import-field">
                <span>Retention Days</span>
                <input type="number" min={1} value={retentionDays} onChange={event => { invalidateConversionSource(); setRetentionDays(Math.max(1, Number(event.target.value) || 1)) }} />
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={redactSensitive} onChange={event => { invalidateConversionSource(); setRedactSensitive(event.target.checked) }} />
                Scrub sensitive fields
              </label>
              <label className="sample-toggle sample-import-span-2">
                <input type="checkbox" checked={includeStaticAssets} onChange={event => { invalidateConversionSource(); setIncludeStaticAssets(event.target.checked) }} />
                Include static assets
              </label>
            </>
          )}
          {supportsAgentforceTransform && (
            <label className="sample-toggle sample-import-span-2" title="If an Agentforce Agent API conversation is detected, rewrite the script to acquire an OAuth2 token, correlate the session id, consume SSE, and end the session.">
              <input type="checkbox" checked={agentforceReplayReady} onChange={event => { invalidateConversionSource(); setAgentforceReplayReady(event.target.checked) }} />
              Make Agentforce replay-ready (if an Agent API flow is detected)
            </label>
          )}
        </div>
      </div>

      <div className="sample-import-actions">
        {kind !== 'asyncapi' && (
          <button className="btn btn-secondary btn-lg" onClick={() => handleConvert('validation')} disabled={convertTask.pending || !file}>
            {convertTask.pending && reviewMode === 'validation' ? <><span className="spinner" /> Validating...</> : 'Validate Only'}
          </button>
        )}
        <button className="btn btn-primary btn-lg" onClick={() => handleConvert('conversion')} disabled={convertTask.pending || !file}>
          {convertTask.pending && reviewMode === 'conversion' ? <><span className="spinner" /> Converting...</> : labels.button}
        </button>
      </div>

      {sourceImportSupportHasLimitations(sourceImportSupport) && (
        <SourceImportCoveragePanel kind={kind} support={sourceImportSupport} />
      )}

      {(messageError || convertTask.error) && <div className="sample-import-error">{messageError || convertTask.error}</div>}

      {result && reviewedScript && (
        <div className="sample-import-result">
          <ScriptImportReview
            title={reviewMode === 'validation' ? 'Validation Review' : 'Review Generated Script'}
            reviewGeneration={reviewScope}
            subtitle={`${actionCount} action(s) generated from ${generatedFromLabel(kind)}.${reviewMode === 'validation' ? ' This preview came from a dry-run validation; nothing has been saved. You can still save the reviewed script if the output looks correct.' : ''}`}
            saveLabel={asyncAPISelectedSaveLabel || ((kind === 'postman' && postmanSaveMode === 'replace') || (kind === 'openapi' && openAPISaveMode === 'replace') ? 'Replace Script' : reviewMode === 'validation' ? 'Save Reviewed Script' : 'Save To Script Library')}
            script={reviewedScript}
            warnings={result.warnings}
            unsupportedElements={result.unsupported_elements}
            reviewSections={result.review_sections}
            parameterization={result.parameterization}
            onParameterizationApplied={handleParameterizationApplied}
            secretBindingRequirements={secretBindingRequirements}
            onSecretBindingsApply={handleSecretBindingsApply}
            projectId={projectId}
            sourceSecretSessionId={result.source_secret_session_id}
            sourceSecretSessionExpiresAt={result.source_secret_session_expires_at}
            sourceSecretMigrationAllowed={result.source_secret_migration_allowed}
            protectedImportSessionExpiresAt={result.import_session_id ? result.expires_at : undefined}
            credentialReviewRef={credentialReviewRef}
            onCredentialsPreparedChange={handleCredentialsPreparedChange}
            onReviewBusyChange={handleInternalReviewBusyChange}
            saving={saveTask.pending}
            reviewBusy={saveAllPreparing || saveTask.pending}
            saveDisabled={(kind === 'postman' && postmanSaveMode === 'replace' && !postmanReplaceScriptId) || (kind === 'openapi' && openAPISaveMode === 'replace' && !openAPIReplaceScriptId)}
            onSave={handleSave}
            onEdit={onEdit ? handleEditReviewedScript : undefined}
            previewAside={kind === 'recorder-export'
              ? <SelectorCandidatesReview script={reviewedScript} onPromoteCandidate={handlePromoteRecorderCandidate} />
              : sourceReview ? <SourceMappingReview sourceReview={sourceReview} script={reviewedScript} title="Source Comparison" /> : undefined}
          >
            {kind === 'recorder-export' && (
              <RecorderStepMappingReview summary={result.summary as Record<string, unknown> | undefined} />
            )}
            {kind === 'asyncapi' && generatedScripts.length > 1 && (
              <div className="sample-import-review-block">
                <h4>Generated Scripts</h4>
                <div className="sample-import-script-tabs" role="tablist" aria-label="Generated AsyncAPI scripts">
                  {generatedScripts.map((script, index) => (
                    <button
                      key={`${script.name}-${index}`}
                      type="button"
                      role="tab"
                      aria-selected={index === selectedScriptIndex}
                      className={`sample-import-script-tab ${index === selectedScriptIndex ? 'active' : ''}`}
                      onClick={() => { advanceReviewGeneration(); setSelectedScriptIndex(index) }}
                      disabled={saveAllPreparing || saveTask.pending || internalReviewBusy}
                    >
                      {script.protocol} · {script.name}
                    </button>
                  ))}
                </div>
                <button
                  className="btn btn-secondary btn-sm"
                  type="button"
                  onClick={handleSaveAllAsyncAPIScripts}
                  disabled={saveTask.pending || saveAllPreparing || internalReviewBusy || !preparedCredentialsValid}
                  title={!preparedCredentialsValid ? 'Enter valid secret reference names before saving these scripts.' : undefined}
                >
                  {saveTask.pending ? 'Saving...' : saveAllPreparing ? 'Preparing credentials...' : asyncAPISaveAllLabel}
                </button>
              </div>
            )}
            <div className="sample-import-stats-grid">
              {stats && Object.entries(stats).filter(([, value]) => typeof value === 'number').map(([key, value]) => (
                <div className="sample-stat" key={key}>
                  <span className="sample-stat-value">{Number(value)}</span>
                  <span className="sample-stat-label">{key.replace(/_/g, ' ')}</span>
                </div>
              ))}
            </div>
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
                <div className="sample-import-stats-grid">
                  <div className="sample-stat">
                    <span className="sample-stat-value">{reviewedScript.protocol}</span>
                    <span className="sample-stat-label">protocol</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{reviewedScript.protocol === 'kafka' ? (reviewedScript.config.kafka?.mock === false ? 'Live' : 'Mock') : (reviewedScript.config.jms?.mock === false ? 'Live' : 'Mock')}</span>
                    <span className="sample-stat-label">adapter</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{(stats?.request_reply_actions as number | undefined) ?? 0}</span>
                    <span className="sample-stat-label">request reply</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{(stats?.messages_with_correlation as number | undefined) ?? 0}</span>
                    <span className="sample-stat-label">correlations</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{(stats?.skipped_operations as string[] | undefined)?.length ?? 0}</span>
                    <span className="sample-stat-label">skipped operations</span>
                  </div>
                </div>
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
                    <select value={openAPISaveMode} onChange={event => setOpenAPISaveMode(event.target.value as 'create' | 'replace')}>
                      <option value="create">Create new script</option>
                      <option value="replace">Replace existing script</option>
                    </select>
                  </label>
                  {openAPISaveMode === 'replace' && (
                    <>
                      <label className="sample-import-field">
                        <span>Script</span>
                        <select value={openAPIReplaceScriptId} onChange={event => setOpenAPIReplaceScriptId(event.target.value)}>
                          <option value="">Select script</option>
                          {openAPIScripts.map(script => (
                            <option key={script.id} value={script.id}>{script.name}</option>
                          ))}
                        </select>
                      </label>
                      <label className="sample-import-field">
                        <span>Replace Mode</span>
                        <select value={openAPIReplaceMode} onChange={event => setOpenAPIReplaceMode(event.target.value as 'full' | 'merge_by_operation_id')}>
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
                    <select value={postmanSaveMode} onChange={event => setPostmanSaveMode(event.target.value as 'create' | 'replace')}>
                      <option value="create">Create new script</option>
                      <option value="replace">Replace existing script</option>
                    </select>
                  </label>
                  {postmanSaveMode === 'replace' && (
                    <label className="sample-import-field">
                      <span>Script</span>
                      <select value={postmanReplaceScriptId} onChange={event => setPostmanReplaceScriptId(event.target.value)}>
                        <option value="">Select script</option>
                        {postmanScripts.map(script => (
                          <option key={script.id} value={script.id}>{script.name}</option>
                        ))}
                      </select>
                    </label>
                  )}
                </div>
              </div>
            )}
            <ImportRuntimeOptions
              draftScope={`${projectId || ''}:${kind}:${conversionScope}:${selectedScriptIndex}`}
              script={reviewedScript}
              onChange={handleRuntimeOptionsChanged}
              disabled={saveTask.pending}
            />
            {kind === 'har' && harSummary && (
              <div className="sample-import-review-block">
                <h4>HAR Artifact Review</h4>
                <div className="sample-import-stats-grid">
                  <div className="sample-stat">
                    <span className="sample-stat-value">{harSummary.artifact_mode}</span>
                    <span className="sample-stat-label">artifact mode</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{harSummary.artifact_sensitivity || harSummary.governance?.sensitivity || 'unknown'}</span>
                    <span className="sample-stat-label">sensitivity</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{harSummary.retention_days ?? harSummary.governance?.retention_days ?? 0}</span>
                    <span className="sample-stat-label">retention days</span>
                  </div>
                  {harDiff && (
                    <>
                      <div className="sample-stat">
                        <span className="sample-stat-value">{harDiff.dynamic_candidates}</span>
                        <span className="sample-stat-label">dynamic candidates</span>
                      </div>
                      <div className="sample-stat">
                        <span className="sample-stat-value">{harDiff.static_candidates}</span>
                        <span className="sample-stat-label">static candidates</span>
                      </div>
                    </>
                  )}
                </div>
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
                <div className="sample-import-stats-grid">
                  <div className="sample-stat">
                    <span className="sample-stat-value">{salesforceReview.detected ? 'Detected' : 'Needs review'}</span>
                    <span className="sample-stat-label">traffic match</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{Math.round(salesforceReview.har_match_coverage?.coverage_pct || 0)}%</span>
                    <span className="sample-stat-label">HAR coverage</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{salesforceReview.har_match_coverage?.matched_requests || 0} / {salesforceReview.har_match_coverage?.imported_requests || 0}</span>
                    <span className="sample-stat-label">matched requests</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{salesforceReview.domains?.length || 0}</span>
                    <span className="sample-stat-label">domains</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{endpointTotal(salesforceReview, 'aura_endpoints')}</span>
                    <span className="sample-stat-label">Aura calls</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{endpointTotal(salesforceReview, 'lightning_endpoints')}</span>
                    <span className="sample-stat-label">Lightning calls</span>
                  </div>
                </div>
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
                        <li key={`${param.param_key}-${param.expression}`}>
                          <strong>{param.param_key}</strong> ({param.kind}){param.sample_value ? ` e.g. ${param.sample_value}` : ''} → <code>{param.template}</code>
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
                <div className="sample-import-stats-grid">
                  <div className="sample-stat">
                    <span className="sample-stat-value">{(stats?.total_resources as number | undefined) ?? ramlSummary?.resources?.length ?? 0}</span>
                    <span className="sample-stat-label">resources</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{(stats?.total_methods as number | undefined) ?? 0}</span>
                    <span className="sample-stat-label">methods</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{(stats?.request_bodies as number | undefined) ?? 0}</span>
                    <span className="sample-stat-label">request bodies</span>
                  </div>
                  <div className="sample-stat">
                    <span className="sample-stat-value">{ramlSummary?.auth_schemes?.length || 0}</span>
                    <span className="sample-stat-label">auth schemes</span>
                  </div>
                  {ramlSummary?.base_url && (
                    <div className="sample-stat">
                      <span className="sample-stat-value">{ramlSummary.base_url}</span>
                      <span className="sample-stat-label">base URL</span>
                    </div>
                  )}
                </div>
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
          </ScriptImportReview>
        </div>
      )}
    </div>
  )
}
