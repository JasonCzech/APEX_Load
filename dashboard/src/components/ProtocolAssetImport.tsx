import { useState } from 'react'

import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { type StoredScript } from '../types'
import { type AsyncAPIImportResult, type DevWebImportResult, type HARImportResult, type OpenAPIImportResult, type PostmanImportResult, type WSDLImportResult } from '../types/scriptImports'
import { type ScriptDefinition, type SourceReview } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import ScriptImportReview from './ScriptImportReview'
import SourceMappingReview from './SourceMappingReview'
import { withFallbackPersistedScript } from './importSaveUtils'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import './SampleScriptImport.css'

type ImportKind = 'devweb' | 'har' | 'openapi' | 'postman' | 'wsdl' | 'asyncapi'

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
  openapi: {
    title: 'Import OpenAPI',
    subtitle: 'Upload an OpenAPI or Swagger JSON/YAML document and generate grouped REST request flows, auth placeholders, and data-source review notes.',
    accept: '.json,.yaml,.yml,application/json,application/yaml,text/yaml,text/plain',
    drop: 'Drop an OpenAPI JSON or YAML file here',
    button: 'Convert OpenAPI',
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

type ProtocolImportResult = DevWebImportResult | HARImportResult | OpenAPIImportResult | PostmanImportResult | WSDLImportResult | AsyncAPIImportResult

function sourceReviewForImport(result?: ProtocolImportResult | null, script?: ScriptDefinition | null): SourceReview | null {
  const metadataReview = script?.metadata?.import?.preserved?.source_review as SourceReview | undefined
  const review = result?.source_review || metadataReview
  if (!review || (!Array.isArray(review.source_files) && !Array.isArray(review.mappings))) return null
  return review
}

export default function ProtocolAssetImport({ kind, projectId, onImported, onEdit, onClose }: Props) {
  const api = useApi()
  const convertTask = useAsyncTask<ProtocolImportResult>()
  const saveTask = useAsyncTask<StoredScript>()
  const result = convertTask.value
  const [file, setFile] = useState<File | null>(null)
  const [name, setName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [soapVersion, setSoapVersion] = useState('1.1')
  const [includeStaticAssets, setIncludeStaticAssets] = useState(false)
  const [messageError, setMessageError] = useState('')
  const labels = copy[kind]

  const handleConvert = async () => {
    if (!file) {
      setMessageError(`Please upload ${kind === 'devweb' ? 'a DevWeb script' : kind === 'har' ? 'a HAR file' : kind === 'openapi' ? 'an OpenAPI document' : kind === 'postman' ? 'a Postman collection' : kind === 'asyncapi' ? 'an AsyncAPI document' : 'a WSDL document'}.`)
      return
    }
    setMessageError('')
    try {
      await convertTask.run(async () => {
        const options = {
          name: name || undefined,
          base_url: baseURL || undefined,
          project_id: projectId,
        }
        if (kind === 'devweb') return api.importDevWeb(file, options)
        if (kind === 'har') return api.importHAR(file, { ...options, include_static_assets: includeStaticAssets })
        if (kind === 'openapi') return api.importOpenAPI(file, options)
        if (kind === 'postman') return api.importPostman(file, options)
        if (kind === 'asyncapi') return api.importAsyncAPI(file, { name: options.name, project_id: options.project_id })
        return api.importWSDL(file, { ...options, soap_version: soapVersion })
      }, { clearValue: true })
    } catch (error: unknown) {
      setMessageError(getErrorMessage(error, `${labels.title} failed`))
    }
  }

  const handleSave = async () => {
    const reviewedScript = result?.script ?? result?.scripts?.[0]
    if (!reviewedScript) return
    setMessageError('')
    try {
      const savedScript = await saveTask.run(async () => api.createScript(reviewedScript, projectId))
      onImported?.({ scripts: [withFallbackPersistedScript(savedScript, reviewedScript)] })
    } catch (error: unknown) {
      setMessageError(getErrorMessage(error, 'Failed to save generated script'))
    }
  }

  const stats = result?.stats as Record<string, unknown> | undefined
  const actionCount = typeof stats?.converted_actions === 'number' ? stats.converted_actions : 0
  const reviewedScript = result?.script || result?.scripts?.[0] || null
  const sourceReview = sourceReviewForImport(result, reviewedScript)

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
          <label className="sample-import-field sample-import-span-2">
            <span>File</span>
            <input type="file" accept={labels.accept} onChange={event => setFile(event.target.files?.[0] || null)} />
            <small>{file ? file.name : labels.drop}</small>
          </label>
          <label className="sample-import-field">
            <span>Script Name</span>
            <input value={name} onChange={event => setName(event.target.value)} placeholder={kind === 'devweb' ? 'Imported DevWeb Script' : kind === 'har' ? 'Imported HAR Script' : kind === 'postman' ? 'Imported Postman Script' : 'Imported API Script'} />
          </label>
          <label className="sample-import-field">
            <span>Base URL Override</span>
            <input value={baseURL} onChange={event => setBaseURL(event.target.value)} placeholder="https://api.example.com" />
          </label>
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
            <label className="sample-toggle sample-import-span-2">
              <input type="checkbox" checked={includeStaticAssets} onChange={event => setIncludeStaticAssets(event.target.checked)} />
              Include static assets
            </label>
          )}
        </div>
      </div>

      <div className="sample-import-actions">
        <button className="btn btn-primary btn-lg" onClick={handleConvert} disabled={convertTask.pending || !file}>
          {convertTask.pending ? <><span className="spinner" /> Converting...</> : labels.button}
        </button>
      </div>

      {(messageError || convertTask.error) && <div className="sample-import-error">{messageError || convertTask.error}</div>}

      {result && reviewedScript && (
        <div className="sample-import-result">
          <ScriptImportReview
            title="Review Generated Script"
            subtitle={`${actionCount} action(s) generated from ${kind === 'devweb' ? 'DevWeb source' : kind === 'har' ? 'HAR entries' : kind === 'openapi' ? 'OpenAPI operations' : kind === 'postman' ? 'Postman requests' : kind === 'asyncapi' ? 'AsyncAPI operations' : 'WSDL operations'}.`}
            script={reviewedScript}
            warnings={result.warnings}
            unsupportedElements={result.unsupported_elements}
            reviewSections={result.review_sections}
            saving={saveTask.pending}
            onSave={handleSave}
            onEdit={onEdit ? () => onEdit(reviewedScript) : undefined}
            previewAside={sourceReview ? <SourceMappingReview sourceReview={sourceReview} script={reviewedScript} title="Source Comparison" /> : undefined}
          >
            <div className="sample-import-stats-grid">
              {stats && Object.entries(stats).filter(([, value]) => typeof value === 'number').slice(0, 5).map(([key, value]) => (
                <div className="sample-stat" key={key}>
                  <span className="sample-stat-value">{Number(value)}</span>
                  <span className="sample-stat-label">{key.replace(/_/g, ' ')}</span>
                </div>
              ))}
            </div>
          </ScriptImportReview>
        </div>
      )}
    </div>
  )
}
