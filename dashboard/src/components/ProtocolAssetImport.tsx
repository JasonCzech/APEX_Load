import { useEffect, useState } from 'react'

import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { type StoredScript } from '../types'
import { type AgentforceReview, type AgentforceTransformChange, type AsyncAPIImportResult, type AsyncAPISupportMatrixEntry, type BrowserRecordingImportResult, type DevWebImportResult, type HARImportResult, type HARImportSummary, type MulesoftReview, type OpenAPICorrelation, type OpenAPIImportResult, type OpenAPISupportMatrixEntry, type PostmanImportResult, type RAMLImportResult, type RAMLSupportMatrixEntry, type SalesforceImportResult, type SalesforceReview, type WSDLImportResult } from '../types/scriptImports'
import { type ScriptDefinition, type SourceReview } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import ImportDropzone from './ImportDropzone'
import ImportRuntimeOptions from './ImportRuntimeOptions'
import ReplayPreflightCard from './ReplayPreflightCard'
import ScriptImportReview from './ScriptImportReview'
import SelectorCandidatesReview from './SelectorCandidatesReview'
import SourceMappingReview from './SourceMappingReview'
import { withFallbackPersistedScript } from './importSaveUtils'
import { salesforceApiSurfaceBreakdown, salesforceEndpointTotal } from './salesforceReviewSummary'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import './SampleScriptImport.css'

type ImportKind = 'devweb' | 'har' | 'salesforce' | 'recorder-export' | 'openapi' | 'raml' | 'postman' | 'wsdl' | 'asyncapi'

interface Props {
  kind: ImportKind
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
    subtitle: 'Upload a Chrome DevTools HAR file and convert recorded HTTP entries into a reviewable APEX Load HTTP script.',
    accept: '.har,application/json',
    drop: 'Drop a .har file here',
    button: 'Convert HAR',
  },
  salesforce: {
    title: 'Import Salesforce HAR',
    subtitle: 'Upload a Salesforce, Lightning, MuleSoft, or Agentforce HAR and generate a Salesforce-aware HTTP draft with native correlation review.',
    accept: '.har,.json,application/json',
    drop: 'Drop a Salesforce HAR or JSON file here',
    button: 'Convert Salesforce HAR',
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
} satisfies Record<ImportKind, Record<string, string>>

type ProtocolImportResult = DevWebImportResult | HARImportResult | SalesforceImportResult | BrowserRecordingImportResult | OpenAPIImportResult | RAMLImportResult | PostmanImportResult | WSDLImportResult | AsyncAPIImportResult

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
    case 'salesforce':
      return 'a Salesforce HAR file'
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
    case 'salesforce':
      return 'Imported Salesforce Script'
    case 'postman':
      return 'Imported Postman Script'
    case 'raml':
      return 'Imported RAML Script'
    case 'asyncapi':
      return 'Imported AsyncAPI Script'
    case 'wsdl':
      return 'Imported WSDL Script'
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
    case 'salesforce':
      return 'Salesforce HAR entries'
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
    default:
      return 'WSDL operations'
  }
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

const endpointTotal = salesforceEndpointTotal

export default function ProtocolAssetImport({ kind, projectId, onImported, onEdit, onClose }: Props) {
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
  const [retentionDays, setRetentionDays] = useState(7)
  const [messageError, setMessageError] = useState('')
  const [reviewMode, setReviewMode] = useState<'conversion' | 'validation'>('conversion')
  const [ramlSupport, setRamlSupport] = useState<{ entries: RAMLSupportMatrixEntry[]; notes: string[] } | null>(null)
  const [openAPISupport, setOpenAPISupport] = useState<{ entries: OpenAPISupportMatrixEntry[]; notes: string[] } | null>(null)
  const [asyncAPISupport, setAsyncAPISupport] = useState<{ entries: AsyncAPISupportMatrixEntry[]; notes: string[] } | null>(null)
  const [openAPISaveMode, setOpenAPISaveMode] = useState<'create' | 'replace'>('create')
  const [openAPIReplaceScriptId, setOpenAPIReplaceScriptId] = useState('')
  const [openAPIReplaceMode, setOpenAPIReplaceMode] = useState<'full' | 'merge_by_operation_id'>('full')
  const [openAPIScripts, setOpenAPIScripts] = useState<StoredScript[]>([])
  const [postmanSaveMode, setPostmanSaveMode] = useState<'create' | 'replace'>('create')
  const [postmanReplaceScriptId, setPostmanReplaceScriptId] = useState('')
  const [postmanScripts, setPostmanScripts] = useState<StoredScript[]>([])
  const labels = copy[kind]
  const supportsAgentforceTransform = kind === 'openapi' || kind === 'raml' || kind === 'postman' || kind === 'salesforce'

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
    if (kind !== 'asyncapi') return
    let cancelled = false
    api.getAsyncAPISupportMatrix()
      .then(response => { if (!cancelled) setAsyncAPISupport({ entries: response.support_matrix || [], notes: response.notes || [] }) })
      .catch(() => { if (!cancelled) setAsyncAPISupport(null) })
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

  const handleConvert = async (mode: 'conversion' | 'validation' = 'conversion') => {
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
        if (kind === 'har') return api.importHAR(file, {
          ...options,
          include_static_assets: includeStaticAssets,
          secondary_file: secondaryHARFile || undefined,
          artifact_mode: secondaryHARFile ? 'design_har_pair' : 'design_har',
          retention_days: retentionDays,
          redact_sensitive: redactSensitive,
        })
        if (kind === 'salesforce') return api.importSalesforce(file, { ...options, agentforce_transform: agentforceReplayReady })
        if (kind === 'recorder-export') return api.importBrowserRecording(file, options)
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

  const stats = result?.stats as Record<string, unknown> | undefined
  const actionCount = typeof stats?.converted_actions === 'number' ? stats.converted_actions : 0
  const generatedScripts = result?.scripts?.length ? result.scripts : result?.script ? [result.script] : []
  const reviewedScript = generatedScripts[selectedScriptIndex] || generatedScripts[0] || null
  const sourceReview = sourceReviewForImport(result, reviewedScript)
  const harSummary: HARImportSummary | undefined = kind === 'har' ? (result as HARImportResult | null | undefined)?.summary : undefined
  const salesforceReview = kind === 'salesforce' ? salesforceReviewForImport(result, reviewedScript) : null
  const mulesoftReview = preservedReviewForImport<MulesoftReview>(reviewedScript, 'mulesoft_review')
  const agentforceReview = preservedReviewForImport<AgentforceReview>(reviewedScript, 'agentforce_review')
  const agentforceTransformChanges = (reviewedScript?.metadata?.import?.preserved?.agentforce_transform as AgentforceTransformChange[] | undefined) ?? []
  const ramlSummary = kind === 'raml' ? (result as RAMLImportResult | null | undefined)?.summary : undefined
  const harDiff = harSummary?.har_diff
  const replayPreflight = result?.preflight || (reviewedScript?.metadata?.import?.preserved?.replay_preflight as OpenAPIImportResult['preflight'] | undefined) || null
  const openAPICorrelations: OpenAPICorrelation[] = kind === 'openapi' ? ((result as OpenAPIImportResult | null | undefined)?.correlations || []) : []

  const handleSave = async () => {
    if (!reviewedScript) return
    setMessageError('')
    try {
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
      const savedScript = await saveTask.run(async () => api.createScript(reviewedScript, projectId))
      onImported?.({ scripts: [withFallbackPersistedScript(savedScript, reviewedScript)] })
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

      {kind === 'raml' && ramlSupport && ramlSupport.entries.length > 0 && (
        <details className="sample-import-support-matrix">
          <summary>RAML import coverage ({ramlSupport.entries.length} constructs)</summary>
          <ul className="sample-import-findings">
            {ramlSupport.entries.map(entry => (
              <li key={entry.id}>
                <strong>{entry.construct}</strong> — {entry.status}{entry.review_required ? ' (review)' : ''}{entry.notes ? `: ${entry.notes}` : ''}
              </li>
            ))}
          </ul>
          {ramlSupport.notes.length > 0 && (
            <ul className="sample-import-findings">
              {ramlSupport.notes.map((note, index) => <li key={`note-${index}`}>{note}</li>)}
            </ul>
          )}
        </details>
      )}

      {kind === 'openapi' && openAPISupport && openAPISupport.entries.length > 0 && (
        <details className="sample-import-support-matrix">
          <summary>OpenAPI import coverage ({openAPISupport.entries.length} constructs)</summary>
          <ul className="sample-import-findings">
            {openAPISupport.entries.map(entry => (
              <li key={entry.id}>
                <strong>{entry.construct}</strong> — {entry.status}{entry.review_required ? ' (review)' : ''}{entry.notes ? `: ${entry.notes}` : ''}
              </li>
            ))}
          </ul>
          {openAPISupport.notes.length > 0 && (
            <ul className="sample-import-findings">
              {openAPISupport.notes.map((note, index) => <li key={`openapi-note-${index}`}>{note}</li>)}
            </ul>
          )}
        </details>
      )}

      {kind === 'asyncapi' && asyncAPISupport && asyncAPISupport.entries.length > 0 && (
        <details className="sample-import-support-matrix">
          <summary>AsyncAPI import coverage ({asyncAPISupport.entries.length} constructs)</summary>
          <ul className="sample-import-findings">
            {asyncAPISupport.entries.map(entry => (
              <li key={entry.id}>
                <strong>{entry.construct}</strong> — {entry.status}{entry.review_required ? ' (review)' : ''}{entry.notes ? `: ${entry.notes}` : ''}
              </li>
            ))}
          </ul>
          {asyncAPISupport.notes.length > 0 && (
            <ul className="sample-import-findings">
              {asyncAPISupport.notes.map((note, index) => <li key={`asyncapi-note-${index}`}>{note}</li>)}
            </ul>
          )}
        </details>
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
              ? <SelectorCandidatesReview script={reviewedScript} />
              : sourceReview ? <SourceMappingReview sourceReview={sourceReview} script={reviewedScript} title="Source Comparison" /> : undefined}
          >
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
            {(kind === 'openapi' || kind === 'asyncapi') && replayPreflight && (
              <div className="sample-import-preflight">
                <ReplayPreflightCard preflight={replayPreflight} />
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
            {kind === 'salesforce' && salesforceReview && (
              <div className="sample-import-review-block">
                <h4>Salesforce Native Review</h4>
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
