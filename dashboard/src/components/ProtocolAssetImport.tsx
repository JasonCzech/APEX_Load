import { useEffect, useState } from 'react'

import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { type BrowserReplayReport, type BrowserReplayStartResponse, type ReplayRepairSuggestion, type StoredScript } from '../types'
import { type AgentforceReview, type AgentforceTransformChange, type AsyncAPIImportResult, type AsyncAPISupportMatrixEntry, type BrokerConfigImportResult, type BrokerConfigSupportMatrixEntry, type BrowserRecordingImportResult, type DevWebImportResult, type DevWebSupportMatrixEntry, type EventHubsCaptureImportResult, type EventHubsCaptureSupportMatrixEntry, type HARImportResult, type HARImportSummary, type K6ImportResult, type K6SupportMatrixEntry, type MulesoftReview, type OpenAPICorrelation, type OpenAPIImportResult, type OpenAPISupportMatrixEntry, type PostmanImportResult, type PostmanSupportMatrixEntry, type RAMLImportResult, type RAMLSupportMatrixEntry, type SalesforceImportResult, type SalesforceReview, type WSDLImportResult } from '../types/scriptImports'
import { type ScriptDefinition, type SourceReview } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'
import ImportRuntimeOptions from './ImportRuntimeOptions'
import BrowserReplayProgress from './BrowserReplayProgress'
import ReplayPreflightCard from './ReplayPreflightCard'
import ScriptImportReview from './ScriptImportReview'
import SelectorCandidatesReview, { type SelectorCandidate } from './SelectorCandidatesReview'
import SourceMappingReview from './SourceMappingReview'
import { withFallbackPersistedScript } from './importSaveUtils'
import { salesforceApiSurfaceBreakdown, salesforceEndpointTotal } from './salesforceReviewSummary'
import { buildReplayStartPayload } from './scriptEditorUtils'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import './SampleScriptImport.css'

export type ImportKind = 'devweb' | 'har' | 'recorder-export' | 'openapi' | 'raml' | 'postman' | 'wsdl' | 'asyncapi' | 'k6' | 'eventhubs-capture' | 'broker-config'

interface Props {
  kind: ImportKind
  initialMode?: string | null
  projectId?: string
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

type ProtocolImportResult = DevWebImportResult | HARImportResult | SalesforceImportResult | BrowserRecordingImportResult | OpenAPIImportResult | RAMLImportResult | PostmanImportResult | WSDLImportResult | AsyncAPIImportResult | K6ImportResult | EventHubsCaptureImportResult | BrokerConfigImportResult
type SourceImportSupportEntry = DevWebSupportMatrixEntry | OpenAPISupportMatrixEntry | RAMLSupportMatrixEntry | PostmanSupportMatrixEntry | AsyncAPISupportMatrixEntry | K6SupportMatrixEntry | EventHubsCaptureSupportMatrixEntry | BrokerConfigSupportMatrixEntry

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

export default function ProtocolAssetImport({ kind, initialMode, projectId, onImported, onEdit, onClose }: Props) {
  const api = useApi()
  const convertTask = useAsyncTask<ProtocolImportResult>()
  const saveTask = useAsyncTask<StoredScript>()
  const result = convertTask.value
  const [file, setFile] = useState<File | null>(null)
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
  const [recorderReplayStatus, setRecorderReplayStatus] = useState<'idle' | 'running' | 'completed' | 'failed' | 'stopped'>('idle')
  const [recorderReplayReport, setRecorderReplayReport] = useState<BrowserReplayReport | null>(null)
  const [allowRecorderDraftSave, setAllowRecorderDraftSave] = useState(false)
  const [editedRecorderScripts, setEditedRecorderScripts] = useState<ScriptDefinition[]>([])
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
  const [postmanSaveMode, setPostmanSaveMode] = useState<'create' | 'replace'>('create')
  const [postmanReplaceScriptId, setPostmanReplaceScriptId] = useState('')
  const [postmanScripts, setPostmanScripts] = useState<StoredScript[]>([])
  const labels = copy[kind]
  const supportsAgentforceTransform = kind === 'openapi' || kind === 'raml' || kind === 'postman' || kind === 'har'

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
    setRecorderReplaySessionId('')
    setRecorderReplayStatus('idle')
    setRecorderReplayReport(null)
    setAllowRecorderDraftSave(false)
    setRecorderDraftNotice('')
  }, [result, selectedScriptIndex, file])

  useEffect(() => {
    setEditedRecorderScripts(kind === 'recorder-export' ? scriptsFromImportResult(result as BrowserRecordingImportResult | null) : [])
  }, [kind, result])

  useEffect(() => {
    if (kind !== 'openapi') return
    let cancelled = false
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
    setMessageError('')
    setReviewMode(mode)
    try {
      await convertTask.run(async () => {
        const options = {
          name: name || undefined,
          base_url: baseURL || undefined,
          project_id: projectId,
        }
        if (kind === 'devweb') return api.importDevWeb(file, options)
        if (kind === 'k6') return api.importK6(file, options)
        if (kind === 'eventhubs-capture') return api.importEventHubsCapture(file, { project_id: projectId })
        if (kind === 'broker-config') return api.importBrokerConfig(file, { project_id: projectId })
        if (kind === 'har') return api.importHAR(file, {
          ...options,
          include_static_assets: includeStaticAssets,
          secondary_file: secondaryHARFile || undefined,
          artifact_mode: secondaryHARFile ? 'design_har_pair' : 'design_har',
          retention_days: retentionDays,
          redact_sensitive: redactSensitive,
          salesforce_native: overrides?.salesforceNative ?? harSalesforceNative,
          agentforce_transform: agentforceReplayReady,
        })
        if (kind === 'recorder-export') return mode === 'validation' ? api.validateBrowserRecordingImport(file, options) : api.importBrowserRecording(file, options)
        if (kind === 'openapi') return api.importOpenAPI(file, { ...options, agentforce_transform: agentforceReplayReady })
        if (kind === 'raml') return api.importRAML(file, { ...options, agentforce_transform: agentforceReplayReady })
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
          return mode === 'validation' ? api.validatePostmanImport(file, postmanOptions) : api.importPostman(file, postmanOptions)
        }
        if (kind === 'asyncapi') return api.importAsyncAPI(file, {
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
        })
        return api.importWSDL(file, { ...options, soap_version: soapVersion })
      }, { clearValue: true })
    } catch (error: unknown) {
      setMessageError(getErrorMessage(error, `${labels.title} failed`))
    }
  }

  const handleHARSalesforceNativeConvert = async (salesforceNative: boolean) => {
    setHARSalesforceNative(salesforceNative)
    await handleConvert('conversion', { salesforceNative })
  }

  const stats = result?.stats as Record<string, unknown> | undefined
  const actionCount = typeof stats?.converted_actions === 'number' ? stats.converted_actions : 0
  const importedScripts = result?.scripts?.length ? result.scripts : result?.script ? [result.script] : []
  const generatedScripts = kind === 'recorder-export' && editedRecorderScripts.length > 0 ? editedRecorderScripts : importedScripts
  const reviewedScript = generatedScripts[selectedScriptIndex] || generatedScripts[0] || null
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
    setMessageError('')
    setRecorderDraftNotice('')
    setRecorderReplayReport(null)
    setRecorderReplayStatus('running')
    try {
      const response = await replayTask.run(async () => api.startDraftBrowserReplaySession(buildReplayStartPayload(reviewedScript, {
        projectId,
        ignoreThinkTime: true,
      })))
      setRecorderReplaySessionId(response.replay_session_id)
    } catch (error: unknown) {
      setRecorderReplayStatus('failed')
      setMessageError(getErrorMessage(error, 'Failed to start browser replay validation'))
    }
  }

  const handleRecorderReplayComplete = (report: BrowserReplayReport | null) => {
    setRecorderReplayReport(report)
    setRecorderReplayStatus(report?.success ? 'completed' : 'failed')
  }

  const markRecorderDraftChanged = (message: string) => {
    setRecorderReplaySessionId('')
    setRecorderReplayReport(null)
    setRecorderReplayStatus('idle')
    setAllowRecorderDraftSave(false)
    setRecorderDraftNotice(message)
  }

  const handlePromoteRecorderCandidate = (actionIndex: number, candidate: SelectorCandidate) => {
    setEditedRecorderScripts(prev => prev.map((script, index) => (
      index === selectedScriptIndex ? promoteDraftSelector(script, actionIndex, candidate.value, candidate) : script
    )))
    markRecorderDraftChanged('Selector updated in the draft. Run replay validation again before saving normally.')
  }

  const handleApplyRecorderReplaySuggestion = async (suggestion: ReplayRepairSuggestion) => {
    if (suggestion.draft_patch_type !== 'promote_selector') return
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
    setEditedRecorderScripts(prev => prev.map((script, index) => (
      index === selectedScriptIndex ? promoteDraftSelector(script, actionIndex, selector) : script
    )))
    markRecorderDraftChanged('Replay suggestion applied to the draft. Run replay validation again before saving normally.')
  }

  const handleSave = async () => {
    if (!reviewedScript) return
    setMessageError('')
    try {
      let scriptToSave = reviewedScript
      if (kind === 'recorder-export') {
        const replayPassed = recorderReplayStatus === 'completed' && recorderReplayReport?.success
        if (!replayPassed && !allowRecorderDraftSave) {
          setMessageError('Run replay validation successfully before saving, or choose Save as draft needing replay review.')
          return
        }
        scriptToSave = attachRecorderReplayEvidence(reviewedScript, recorderReplayReport, recorderReplayStatus, !replayPassed, recorderReplaySessionId)
      }
      if (kind === 'openapi') {
        if (openAPISaveMode === 'replace' && !openAPIReplaceScriptId) {
          setMessageError('Select a script to replace.')
          return
        }
        const savedScript = await saveTask.run(async () => {
          const response = await api.commitOpenAPIImport({
            script: reviewedScript,
            mode: openAPISaveMode,
            target_script_id: openAPISaveMode === 'replace' ? openAPIReplaceScriptId : undefined,
            replace_mode: openAPISaveMode === 'replace' ? openAPIReplaceMode : undefined,
          })
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('OpenAPI import commit did not return a stored script')
          return storedScript
        })
        onImported?.({ scripts: [withFallbackPersistedScript(savedScript, reviewedScript)] })
        return
      }
      if (kind === 'postman') {
        if (postmanSaveMode === 'replace' && !postmanReplaceScriptId) {
          setMessageError('Select a script to replace.')
          return
        }
        const savedScript = await saveTask.run(async () => {
          const response = await api.savePostmanImport({
            script: reviewedScript,
            mode: postmanSaveMode,
            target_script_id: postmanSaveMode === 'replace' ? postmanReplaceScriptId : undefined,
          })
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('Postman import save did not return a stored script')
          return storedScript
        })
        onImported?.({ scripts: [withFallbackPersistedScript(savedScript, reviewedScript)] })
        return
      }
      if (kind === 'asyncapi') {
        const savedScript = await saveTask.run(async () => {
          const response = await api.commitAsyncAPIImport({ script: reviewedScript, mode: 'create' })
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('AsyncAPI import commit did not return a stored script')
          return storedScript
        })
        onImported?.({ scripts: [withFallbackPersistedScript(savedScript, reviewedScript)] })
        return
      }
      const savedScript = await saveTask.run(async () => api.createScript(scriptToSave, projectId))
      onImported?.({ scripts: [withFallbackPersistedScript(savedScript, scriptToSave)] })
    } catch (error: unknown) {
      setMessageError(getErrorMessage(error, 'Failed to save generated script'))
    }
  }

  const handleSaveAllAsyncAPIScripts = async () => {
    if (kind !== 'asyncapi' || generatedScripts.length === 0) return
    setMessageError('')
    try {
      const savedScripts: StoredScript[] = []
      await saveTask.run(async () => {
        for (const script of generatedScripts) {
          const response = await api.commitAsyncAPIImport({ script, mode: 'create' })
          const storedScript = response.stored_script || response.scripts?.[0]
          if (!storedScript) throw new Error('AsyncAPI import commit did not return a stored script')
          savedScripts.push(withFallbackPersistedScript(storedScript, script))
        }
        if (!savedScripts[0]) throw new Error('AsyncAPI import commit did not return a stored script')
        return savedScripts[0]
      })
      onImported?.({ scripts: savedScripts })
    } catch (error: unknown) {
      setMessageError(getErrorMessage(error, 'Failed to save generated scripts'))
    }
  }

  const handleParameterizationApplied = (parameterizedScript: ScriptDefinition) => {
    if (!result) return
    const nextScripts = generatedScripts.length ? generatedScripts.map((script, index) => index === selectedScriptIndex ? parameterizedScript : script) : [parameterizedScript]
    convertTask.setValue({ ...result, script: parameterizedScript, scripts: nextScripts } as ProtocolImportResult)
  }

  const handleRuntimeOptionsChanged = (updatedScript: ScriptDefinition) => {
    if (!result) return
    const nextScripts = generatedScripts.length ? generatedScripts.map((script, index) => index === selectedScriptIndex ? updatedScript : script) : [updatedScript]
    convertTask.setValue({ ...result, script: updatedScript, scripts: nextScripts } as ProtocolImportResult)
  }

  return (
    <div className="sample-import">
      <div className="sample-import-header">
        <div>
          <h2>{labels.title}</h2>
          <p className="sample-import-subtitle">{labels.subtitle}</p>
        </div>
        {onClose && <button className="btn btn-secondary" onClick={onClose}>Back</button>}
      </div>

      <div className="sample-import-card">
        <div className="sample-import-grid">
          <div className="sample-import-field sample-import-span-2">
            <span>File</span>
            <ImportDropzone
              accept={labels.accept}
              file={file}
              onFile={next => { setFile(next); setMessageError('') }}
              dropHint={labels.drop}
            />
          </div>
          {kind === 'postman' && (
            <>
              <div className="sample-import-field sample-import-span-2">
                <span>Environment File</span>
                <ImportDropzone
                  accept=".postman_environment.json,.json,application/json"
                  file={postmanEnvironmentFile}
                  onFile={next => { setPostmanEnvironmentFile(next); setMessageError('') }}
                  dropHint="Drop an optional Postman environment JSON file here"
                />
              </div>
              <label className="sample-import-field">
                <span>Default Think Time (ms)</span>
                <input type="number" min={0} value={postmanThinkTimeMs} onChange={event => setPostmanThinkTimeMs(Math.max(0, Number(event.target.value) || 0))} />
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={postmanSanitizeSensitive} onChange={event => setPostmanSanitizeSensitive(event.target.checked)} />
                Scrub sensitive fields
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={postmanIncludeAssertions} onChange={event => setPostmanIncludeAssertions(event.target.checked)} />
                Add status assertions
              </label>
              <label className="sample-toggle sample-import-span-2" title="Bind detected parameters to a generated CSV data source so each virtual user replays with varying data.">
                <input type="checkbox" checked={postmanUseDataSource} onChange={event => setPostmanUseDataSource(event.target.checked)} />
                Generate parameter data source
              </label>
            </>
          )}
          <label className="sample-import-field">
            <span>Script Name</span>
            <input value={name} onChange={event => setName(event.target.value)} placeholder={scriptNamePlaceholder(kind)} />
          </label>
          {kind !== 'asyncapi' && (
            <label className="sample-import-field">
              <span>Base URL Override</span>
              <input value={baseURL} onChange={event => setBaseURL(event.target.value)} placeholder="https://api.example.com" />
            </label>
          )}
          {kind === 'asyncapi' && (
            <>
              <label className="sample-import-field">
                <span>Broker Override</span>
                <input value={asyncAPIBrokers} onChange={event => setAsyncAPIBrokers(event.target.value)} placeholder="broker-a:9092, broker-b:9092" />
              </label>
              <label className="sample-import-field">
                <span>Channel Filter</span>
                <input value={asyncAPIChannels} onChange={event => setAsyncAPIChannels(event.target.value)} placeholder="orders.created, payments.*" />
              </label>
              <label className="sample-import-field">
                <span>Serialization</span>
                <select value={asyncAPISerialization} onChange={event => setAsyncAPISerialization(event.target.value)}>
                  <option value="">From spec / raw</option>
                  <option value="json_schema">JSON Schema</option>
                  <option value="avro">Avro</option>
                </select>
              </label>
              <label className="sample-import-field">
                <span>Schema Registry URL</span>
                <input value={asyncAPISchemaRegistryURL} onChange={event => setAsyncAPISchemaRegistryURL(event.target.value)} placeholder="https://registry.example.com" />
              </label>
              <label className="sample-import-field">
                <span>Schema ID</span>
                <input type="number" min={0} value={asyncAPISchemaID} onChange={event => setAsyncAPISchemaID(event.target.value)} placeholder="42" />
              </label>
              <label className="sample-import-field">
                <span>Subject</span>
                <input value={asyncAPISubject} onChange={event => setAsyncAPISubject(event.target.value)} placeholder="orders-value" />
              </label>
              <label className="sample-import-field">
                <span>Think Time (ms)</span>
                <input type="number" min={0} value={asyncAPIThinkTimeMs} onChange={event => setAsyncAPIThinkTimeMs(Math.max(0, Number(event.target.value) || 0))} />
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={asyncAPIMock} onChange={event => setAsyncAPIMock(event.target.checked)} />
                Use mock broker
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={asyncAPIIncludeAssertions} onChange={event => setAsyncAPIIncludeAssertions(event.target.checked)} />
                Include replay assertions
              </label>
            </>
          )}
          {kind === 'wsdl' && (
            <label className="sample-import-field">
              <span>SOAP Version</span>
              <select value={soapVersion} onChange={event => setSoapVersion(event.target.value)}>
                <option value="1.1">SOAP 1.1</option>
                <option value="1.2">SOAP 1.2</option>
              </select>
            </label>
          )}
          {kind === 'har' && (
            <>
              <label className="sample-import-field sample-import-span-2">
                <span>Comparison HAR</span>
                <input type="file" accept={labels.accept} onChange={event => setSecondaryHARFile(event.target.files?.[0] || null)} />
                <small>{secondaryHARFile ? secondaryHARFile.name : 'Optional second recording for correlation diff'}</small>
              </label>
              <label className="sample-import-field">
                <span>Retention Days</span>
                <input type="number" min={1} value={retentionDays} onChange={event => setRetentionDays(Math.max(1, Number(event.target.value) || 1))} />
              </label>
              <label className="sample-toggle">
                <input type="checkbox" checked={redactSensitive} onChange={event => setRedactSensitive(event.target.checked)} />
                Scrub sensitive fields
              </label>
              <label className="sample-toggle sample-import-span-2">
                <input type="checkbox" checked={includeStaticAssets} onChange={event => setIncludeStaticAssets(event.target.checked)} />
                Include static assets
              </label>
            </>
          )}
          {supportsAgentforceTransform && (
            <label className="sample-toggle sample-import-span-2" title="If an Agentforce Agent API conversation is detected, rewrite the script to acquire an OAuth2 token, correlate the session id, consume SSE, and end the session.">
              <input type="checkbox" checked={agentforceReplayReady} onChange={event => setAgentforceReplayReady(event.target.checked)} />
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
            subtitle={`${actionCount} action(s) generated from ${generatedFromLabel(kind)}.${reviewMode === 'validation' ? ' This preview came from a dry-run validation; nothing has been saved. You can still save the reviewed script if the output looks correct.' : ''}`}
            saveLabel={(kind === 'postman' && postmanSaveMode === 'replace') || (kind === 'openapi' && openAPISaveMode === 'replace') ? 'Replace Script' : reviewMode === 'validation' ? 'Save Reviewed Script' : 'Save To Script Library'}
            script={reviewedScript}
            warnings={result.warnings}
            unsupportedElements={result.unsupported_elements}
            reviewSections={result.review_sections}
            parameterization={result.parameterization}
            onParameterizationApplied={handleParameterizationApplied}
            saving={saveTask.pending}
            saveDisabled={(kind === 'postman' && postmanSaveMode === 'replace' && !postmanReplaceScriptId) || (kind === 'openapi' && openAPISaveMode === 'replace' && !openAPIReplaceScriptId)}
            onSave={handleSave}
            onEdit={onEdit ? () => onEdit(reviewedScript) : undefined}
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
                      onClick={() => setSelectedScriptIndex(index)}
                    >
                      {script.protocol} · {script.name}
                    </button>
                  ))}
                </div>
                <button className="btn btn-secondary btn-sm" type="button" onClick={handleSaveAllAsyncAPIScripts} disabled={saveTask.pending}>
                  {saveTask.pending ? 'Saving...' : 'Save All Scripts'}
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
            {(kind === 'openapi' || kind === 'asyncapi' || kind === 'recorder-export') && replayPreflight && (
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
                    <input type="checkbox" checked={allowRecorderDraftSave} onChange={event => setAllowRecorderDraftSave(event.target.checked)} />
                    Save as draft needing replay review
                  </label>
                </div>
                {recorderReplaySessionId && (
                  <BrowserReplayProgress
                    replaySessionId={recorderReplaySessionId}
                    projectId={projectId}
                    onComplete={handleRecorderReplayComplete}
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
            <ImportRuntimeOptions script={reviewedScript} onChange={handleRuntimeOptionsChanged} disabled={saveTask.pending} />
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
