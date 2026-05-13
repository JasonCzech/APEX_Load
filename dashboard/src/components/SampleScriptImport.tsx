import { useReducer, useRef, useState } from 'react'
import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import {
  describeMergedFiles,
  formatFileLabel,
  mergePrimarySamples,
  mergeSecondarySamples,
  sampleAcceptForFormat,
  supportsMultiPrimary,
  supportsMultiSecondary,
  type LoadedSample,
  type SampleFormat,
} from './sampleImportUtils'
import { type ImportedScript, type SampleImportResult } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { createJavaHttpDraft } from './scriptDraftFactories'
import ScriptImportReview from './ScriptImportReview'
import './SampleScriptImport.css'

type AuthType = 'none' | 'basic' | 'bearer'
type PacingType = 'none' | 'fixed_delay'
type SampleImportPreset = 'default' | 'java-http'

type SampleImportOptions = {
  name: string
  base_url: string
  include_assertions: boolean
  infer_extractors: boolean
  sanitize_sensitive: boolean
  auth_type: AuthType
  auth_username: string
  auth_password: string
  auth_token: string
  think_time_ms: string
  pacing_type: PacingType
  pacing_delay_ms: string
  continue_on_error: boolean
  max_retries: string
  request_timeout_ms: string
  user_agent: string
  default_headers: string
}

type SampleImportOptionsAction =
  | { type: 'update'; field: keyof SampleImportOptions; value: SampleImportOptions[keyof SampleImportOptions] }
  | { type: 'reset' }

interface Props {
  onImported?: (script: ImportedScript) => void
  onEdit?: (script: ScriptDefinition) => void
  onClose?: () => void
  preset?: SampleImportPreset
}

const formatHelp: Record<SampleFormat, string> = {
  raw_http: 'Paste a raw HTTP request and an optional sample response. Include a Host header or provide a base URL override.',
  curl: 'Paste a curl command. The importer derives method, URL, headers, and body automatically.',
  rest_json: 'Paste structured JSON with request/response objects or a requests array.',
  har: 'Paste a HAR file or a single HAR entry JSON object.',
}

const sampleTemplates: Record<SampleFormat, { primary: string; secondary?: string }> = {
  raw_http: {
    primary: 'POST /v1/login HTTP/1.1\nHost: api.example.com\nContent-Type: application/json\n\n{"username":"demo","password":"secret"}',
    secondary: 'HTTP/1.1 200 OK\nContent-Type: application/json\n\n{"token":"session-token-123","userId":91234}',
  },
  curl: {
    primary: `curl -X POST https://api.example.com/v1/orders \\
  -H 'Content-Type: application/json' \\
  -H 'Authorization: Bearer my-token' \\
  -d '{"item":"widget","qty":5}'`,
  },
  rest_json: {
    primary: `{
  "requests": [
    {
      "request": {
        "method": "POST",
        "url": "https://api.example.com/v1/session",
        "headers": {"Content-Type": "application/json"},
        "body": {"username": "demo", "password": "secret"}
      },
      "response": {
        "status": 201,
        "body": {"sessionId": "session-12345"}
      }
    }
  ]
}`,
  },
  har: {
    primary: `{
  "log": {
    "entries": [
      {
        "request": {
          "method": "GET",
          "url": "https://api.example.com/v1/orders",
          "headers": [{"name": "Accept", "value": "application/json"}]
        },
        "response": {
          "status": 200,
          "content": {"text": "{\"orders\":[]}"}
        }
      }
    ]
  }
}`,
  },
}

const javaHttpTemplates: Partial<Record<SampleFormat, { primary: string; secondary?: string }>> = {
  raw_http: {
    primary: 'POST /java/session HTTP/1.1\nHost: api.example.com\nContent-Type: application/x-java-serialized-object\nAccept: application/x-java-serialized-object\n\nrO0ABXQABGRlbW8=',
    secondary: 'HTTP/1.1 200 OK\nContent-Type: application/x-java-serialized-object\n\nrO0ABXQACnNlc3Npb24=',
  },
  curl: {
    primary: `curl -X POST https://api.example.com/java/session \\
  -H 'Content-Type: application/x-java-serialized-object' \\
  -H 'Accept: application/x-java-serialized-object' \\
  --data 'rO0ABXQABGRlbW8='`,
  },
}

const defaultFormatByPreset: Record<SampleImportPreset, SampleFormat> = {
  default: 'rest_json',
  'java-http': 'raw_http',
}

const availableFormatsByPreset: Record<SampleImportPreset, SampleFormat[]> = {
  default: ['rest_json', 'raw_http', 'curl', 'har'],
  'java-http': ['raw_http', 'curl'],
}

const javaHttpDefaults = createJavaHttpDraft().config.java_http || {}

function getSampleTemplate(preset: SampleImportPreset, format: SampleFormat) {
  if (preset === 'java-http' && javaHttpTemplates[format]) {
    return javaHttpTemplates[format]!
  }
  return sampleTemplates[format]
}

function getFormatHelp(preset: SampleImportPreset, format: SampleFormat): string {
  if (preset === 'java-http') {
    if (format === 'raw_http') {
      return 'Paste a raw HTTP request whose body is already a base64-encoded serialized Java payload. Optional sample responses should also preserve the opaque base64 body.'
    }
    if (format === 'curl') {
      return 'Paste a curl command whose --data body is already a base64-encoded serialized Java payload.'
    }
  }
  return formatHelp[format]
}

function looksLikeBase64(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) return true
  return trimmed.length % 4 === 0 && /^[A-Za-z0-9+/=\r\n]+$/.test(trimmed)
}

function promoteScriptToJavaHTTP(script: ScriptDefinition): ScriptDefinition {
  return {
    ...script,
    protocol: 'java_http',
    config: {
      ...script.config,
      java_http: {
        ...javaHttpDefaults,
        ...(script.config?.java_http || {}),
      },
    },
  }
}

function appendJavaHTTPWarnings(script: ScriptDefinition, warnings: string[]): string[] {
  const nextWarnings = [...warnings]
  const hasNonBase64Body = script.actions.some(action => typeof action.body === 'string' && action.body.trim() !== '' && !looksLikeBase64(action.body))
  const warning = hasNonBase64Body
    ? 'One or more imported request bodies are not base64-encoded. Java-over-HTTP replay expects opaque base64-encoded serialized bytes, so review those actions before saving.'
    : 'Java-over-HTTP imports assume request and response payloads are already base64-encoded serialized Java bytes. Review the generated draft before saving.'
  if (!nextWarnings.includes(warning)) {
    nextWarnings.push(warning)
  }
  return nextWarnings
}

const initialOptions: SampleImportOptions = {
  name: '',
  base_url: '',
  include_assertions: true,
  infer_extractors: true,
  sanitize_sensitive: true,
  auth_type: 'none',
  auth_username: '',
  auth_password: '',
  auth_token: '',
  think_time_ms: '1000',
  pacing_type: 'none',
  pacing_delay_ms: '0',
  continue_on_error: false,
  max_retries: '0',
  request_timeout_ms: '60000',
  user_agent: '',
  default_headers: '',
}

function sampleImportOptionsReducer(state: SampleImportOptions, action: SampleImportOptionsAction): SampleImportOptions {
  switch (action.type) {
    case 'update':
      return {
        ...state,
        [action.field]: action.value,
      }
    case 'reset':
      return initialOptions
    default:
      return state
  }
}

export default function SampleScriptImport({ onImported, onEdit, onClose, preset = 'default' }: Props) {
  const api = useApi()
  const primaryFileRef = useRef<HTMLInputElement>(null)
  const secondaryFileRef = useRef<HTMLInputElement>(null)
  const defaultFormat = defaultFormatByPreset[preset]
  const defaultTemplate = getSampleTemplate(preset, defaultFormat)
  const [format, setFormat] = useState<SampleFormat>(defaultFormat)
  const [primaryContent, setPrimaryContent] = useState(defaultTemplate.primary)
  const [secondaryContent, setSecondaryContent] = useState(defaultTemplate.secondary || '')
  const [primaryFileName, setPrimaryFileName] = useState('')
  const [secondaryFileName, setSecondaryFileName] = useState('')
  const [primaryFileNames, setPrimaryFileNames] = useState<string[]>([])
  const [secondaryFileNames, setSecondaryFileNames] = useState<string[]>([])
  const [primaryMergedCount, setPrimaryMergedCount] = useState(0)
  const [secondaryMergedCount, setSecondaryMergedCount] = useState(0)
  const [dragTarget, setDragTarget] = useState<'primary' | 'secondary' | null>(null)
  const [messageError, setMessageError] = useState('')
  const convertTask = useAsyncTask<SampleImportResult>()
  const saveTask = useAsyncTask<boolean>()
  const [options, dispatchOptions] = useReducer(sampleImportOptionsReducer, initialOptions)

  const updateOption = <K extends keyof SampleImportOptions>(field: K, value: SampleImportOptions[K]) => {
    dispatchOptions({ type: 'update', field, value })
  }

  const result = convertTask.value
  const error = messageError || convertTask.error || saveTask.error
  const converting = convertTask.pending
  const saving = saveTask.pending
  const availableFormats = availableFormatsByPreset[preset]
  const isJavaHttpPreset = preset === 'java-http'

  const handleFormatChange = (nextFormat: SampleFormat) => {
    const template = getSampleTemplate(preset, nextFormat)
    setFormat(nextFormat)
    setPrimaryContent(template.primary)
    setSecondaryContent(template.secondary || '')
    setPrimaryFileName('')
    setSecondaryFileName('')
    setPrimaryFileNames([])
    setSecondaryFileNames([])
    setPrimaryMergedCount(0)
    setSecondaryMergedCount(0)
    convertTask.reset()
    saveTask.clearError()
    setMessageError('')
  }

  const handleFileLoad = async (file: File, target: 'primary' | 'secondary') => {
    await handleFilesLoad([file], target)
  }

  const handleFilesLoad = async (files: File[], target: 'primary' | 'secondary') => {
    try {
      const loaded = await Promise.all(files.map(async (file) => ({ file, text: await file.text() })))
      const normalized: LoadedSample[] = loaded.map(item => ({ name: item.file.name, text: item.text }))
      if (target === 'primary') {
        const merged = mergePrimarySamples(format, normalized)
        const names = normalized.map(item => item.name)
        setPrimaryContent(merged.content)
        setPrimaryFileName(formatFileLabel(names))
        setPrimaryFileNames(names)
        setPrimaryMergedCount(merged.mergedCount)
      } else {
        const merged = mergeSecondarySamples(format, normalized)
        const names = normalized.map(item => item.name)
        setSecondaryContent(merged.content)
        setSecondaryFileName(formatFileLabel(names))
        setSecondaryFileNames(names)
        setSecondaryMergedCount(merged.mergedCount)
      }
      setMessageError('')
      convertTask.reset()
      saveTask.clearError()
    } catch (error: unknown) {
      setMessageError(getErrorMessage(error, `Failed to read ${files[0]?.name || 'sample file'}`))
    }
  }

  const handleDragOver = (event: React.DragEvent, target: 'primary' | 'secondary') => {
    event.preventDefault()
    if (event.dataTransfer.types.includes('Files')) {
      setDragTarget(target)
      event.dataTransfer.dropEffect = 'copy'
    }
  }

  const handleDragLeave = (event: React.DragEvent, target: 'primary' | 'secondary') => {
    event.preventDefault()
    if (dragTarget === target) {
      setDragTarget(null)
    }
  }

  const handleDrop = (event: React.DragEvent, target: 'primary' | 'secondary') => {
    event.preventDefault()
    setDragTarget(null)
    const files = Array.from(event.dataTransfer.files || [])
    if (files.length > 0) {
      const allowMultiple = target === 'primary' ? supportsMultiPrimary(format) : supportsMultiSecondary(format)
      void handleFilesLoad(allowMultiple ? files : [files[0]], target)
    }
  }

  const buildScriptWithOptions = (baseScript?: ImportedScript) => {
    const script = JSON.parse(JSON.stringify(baseScript || {})) as ScriptDefinition
    script.name = options.name.trim() || script.name
    script.config = script.config || {}
    script.config.base_url = options.base_url.trim() || script.config.base_url || ''
    script.config.think_time = {
      type: 'fixed',
      mean_ms: Number(options.think_time_ms) || 1000,
    }
    script.config.pacing = options.pacing_type === 'fixed_delay'
      ? { type: 'fixed_delay', delay_ms: Number(options.pacing_delay_ms) || 0 }
      : { type: 'none' }
    script.config.error_handling = {
      ...(script.config.error_handling || {}),
      continue_on_error: options.continue_on_error,
      max_retries: Number(options.max_retries) || 0,
    }
    script.config.http = {
      ...(script.config.http || {}),
      request_timeout_ms: Number(options.request_timeout_ms) || 60000,
    }
    if (options.user_agent.trim()) {
      script.config.http.user_agent = options.user_agent.trim()
    }

    const parsedHeaders = parseDefaultHeaders(options.default_headers)
    if (parsedHeaders && parsedHeaders !== true) {
      script.config.http.default_headers = parsedHeaders
    }

    if (options.auth_type === 'basic') {
      script.config.http.auth = {
        type: 'basic',
        username: options.auth_username.trim(),
        password: options.auth_password,
      }
    } else if (options.auth_type === 'bearer') {
      script.config.http.auth = {
        type: 'bearer',
        token: options.auth_token.trim(),
      }
    }

    return script
  }

  const handleConvert = async () => {
    if (!primaryContent.trim()) {
      setMessageError('Provide sample input before converting.')
      return
    }
    const headersValid = parseDefaultHeaders(options.default_headers, true)
    if (headersValid === false) {
      setMessageError('Default headers must be a valid JSON object.')
      return
    }

    setMessageError('')
    saveTask.clearError()
    try {
      await convertTask.run(
        async () => {
          const res = await api.importSample({
            name: options.name || undefined,
            format,
            content: primaryContent,
            response_content: secondaryContent || undefined,
            options: {
              base_url: options.base_url || undefined,
              include_assertions: options.include_assertions,
              infer_extractors: options.infer_extractors,
              sanitize_sensitive: options.sanitize_sensitive,
            },
          })

          const builtScript = buildScriptWithOptions(res.script)
          const finalScript = isJavaHttpPreset ? promoteScriptToJavaHTTP(builtScript) : builtScript
          const warnings = isJavaHttpPreset ? appendJavaHTTPWarnings(finalScript, res.warnings) : res.warnings

          return {
            ...res,
            script: finalScript,
            warnings,
          }
        },
        { clearValue: true },
      )
    } catch (error: unknown) {
      setMessageError(getErrorMessage(error, 'Sample conversion failed'))
    }
  }

  const handleSave = async () => {
    const reviewedScript = result?.script ?? result?.scripts?.[0]
    if (!reviewedScript) return
    setMessageError('')
    try {
      await saveTask.run(async () => {
        await api.createScript(reviewedScript)
        return true
      })
      onImported?.(reviewedScript)
    } catch (error: unknown) {
      setMessageError(getErrorMessage(error, 'Failed to save generated script'))
    }
  }

  return (
    <div className="sample-import">
      <div className="sample-import-header">
        <div>
          <h2>{isJavaHttpPreset ? 'Import Java over HTTP Samples' : 'Build Script From Sample'}</h2>
          <p className="sample-import-subtitle">
            {isJavaHttpPreset
              ? 'Create a Java-over-HTTP draft from raw HTTP or cURL samples that already preserve base64-encoded serialized Java payloads.'
              : 'Create a Stormrunner script from raw HTTP, curl, REST JSON, or HAR data, then layer in runtime details before saving.'}
          </p>
        </div>
        {onClose && <button className="btn btn-ghost" onClick={onClose}>✕</button>}
      </div>

      <div className="sample-import-card">
        <div className="sample-import-section-header">
          <div>
            <h3>1. Input Format</h3>
            <p>{getFormatHelp(preset, format)}</p>
          </div>
          <select value={format} onChange={e => handleFormatChange(e.target.value as SampleFormat)}>
            {availableFormats.map((option) => (
              <option key={option} value={option}>
                {option === 'rest_json' ? 'REST JSON' : option === 'raw_http' ? 'Raw HTTP' : option === 'curl' ? 'cURL' : 'HAR / HAR Entry'}
              </option>
            ))}
          </select>
        </div>

        <div className="settings-notice">
          {isJavaHttpPreset
            ? 'Java-over-HTTP imports are intentionally bounded to raw HTTP and cURL samples whose opaque request bodies are already base64-encoded serialized Java bytes. The generated draft is promoted to the `java_http` protocol for review in the editor.'
            : 'SOAP-shaped raw HTTP, curl, REST JSON, and HAR request samples can now be promoted into SOAP scripts when the imported sample set is consistently SOAP-oriented. Mixed sample sets still stay on the HTTP protocol surface.'}
        </div>

        <div className="sample-import-grid two-col">
          <label
            className={`sample-import-field sample-import-span-2 ${dragTarget === 'primary' ? 'sample-import-drop-active' : ''}`}
            onDragOver={event => handleDragOver(event, 'primary')}
            onDragLeave={event => handleDragLeave(event, 'primary')}
            onDrop={event => handleDrop(event, 'primary')}
          >
            <span>Primary Sample</span>
            <div className="sample-import-upload-row">
              <input
                ref={primaryFileRef}
                type="file"
                accept={sampleAcceptForFormat(format)}
                multiple={supportsMultiPrimary(format)}
                hidden
                onChange={e => {
                  const files = Array.from(e.target.files || [])
                  if (files.length > 0) handleFilesLoad(files, 'primary')
                  e.target.value = ''
                }}
              />
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => primaryFileRef.current?.click()}>
                {supportsMultiPrimary(format) ? 'Upload Sample Files' : 'Upload Sample File'}
              </button>
              <span className="sample-import-drop-hint">{supportsMultiPrimary(format) ? 'or drag files here' : 'or drag a file here'}</span>
              {primaryFileName && <span className="sample-import-file-name">{primaryFileName}</span>}
            </div>
            {primaryFileNames.length > 0 && (
              <div className="sample-import-upload-summary">
                <p>{describeMergedFiles(format, 'primary', primaryFileNames, primaryMergedCount)}</p>
                <div className="sample-import-file-list">
                  {primaryFileNames.map((name) => <span key={name} className="sample-import-file-chip">{name}</span>)}
                </div>
              </div>
            )}
            <textarea value={primaryContent} onChange={e => setPrimaryContent(e.target.value)} rows={format === 'curl' ? 8 : 14} spellCheck={false} />
          </label>

          {(format === 'raw_http' || format === 'curl') && (
            <label
              className={`sample-import-field sample-import-span-2 ${dragTarget === 'secondary' ? 'sample-import-drop-active' : ''}`}
              onDragOver={event => handleDragOver(event, 'secondary')}
              onDragLeave={event => handleDragLeave(event, 'secondary')}
              onDrop={event => handleDrop(event, 'secondary')}
            >
              <span>Sample Response (optional)</span>
              <div className="sample-import-upload-row">
                <input
                  ref={secondaryFileRef}
                  type="file"
                  accept=".txt,.json,.http,.log"
                  multiple={supportsMultiSecondary(format)}
                  hidden
                  onChange={e => {
                    const files = Array.from(e.target.files || [])
                    if (files.length > 0) handleFilesLoad(files, 'secondary')
                    e.target.value = ''
                  }}
                />
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => secondaryFileRef.current?.click()}>
                  {supportsMultiSecondary(format) ? 'Upload Response Files' : 'Upload Response File'}
                </button>
                <span className="sample-import-drop-hint">{supportsMultiSecondary(format) ? 'or drag files here' : 'or drag a file here'}</span>
                {secondaryFileName && <span className="sample-import-file-name">{secondaryFileName}</span>}
              </div>
              {secondaryFileNames.length > 0 && (
                <div className="sample-import-upload-summary">
                  <p>{describeMergedFiles(format, 'secondary', secondaryFileNames, secondaryMergedCount)}</p>
                  <div className="sample-import-file-list">
                    {secondaryFileNames.map((name) => <span key={name} className="sample-import-file-chip">{name}</span>)}
                  </div>
                </div>
              )}
              <textarea value={secondaryContent} onChange={e => setSecondaryContent(e.target.value)} rows={10} spellCheck={false} />
            </label>
          )}
        </div>
      </div>

      <div className="sample-import-card">
        <div className="sample-import-section-header">
          <div>
            <h3>2. Script Details</h3>
            <p>Capture the runtime and protocol details that usually need to be set before a script is usable.</p>
          </div>
        </div>

        <div className="sample-import-grid three-col">
          <label className="sample-import-field">
            <span>Script Name</span>
            <input value={options.name} onChange={e => updateOption('name', e.target.value)} placeholder="Order Flow Sample" />
          </label>
          <label className="sample-import-field">
            <span>Base URL Override</span>
            <input value={options.base_url} onChange={e => updateOption('base_url', e.target.value)} placeholder="https://api.example.com" />
          </label>
          <label className="sample-import-field">
            <span>Request Timeout (ms)</span>
            <input value={options.request_timeout_ms} onChange={e => updateOption('request_timeout_ms', e.target.value)} inputMode="numeric" />
          </label>

          <label className="sample-import-field">
            <span>Think Time (ms)</span>
            <input value={options.think_time_ms} onChange={e => updateOption('think_time_ms', e.target.value)} inputMode="numeric" />
          </label>
          <label className="sample-import-field">
            <span>Pacing</span>
            <select value={options.pacing_type} onChange={e => updateOption('pacing_type', e.target.value as PacingType)}>
              <option value="none">None</option>
              <option value="fixed_delay">Fixed Delay</option>
            </select>
          </label>
          <label className="sample-import-field">
            <span>Pacing Delay (ms)</span>
            <input value={options.pacing_delay_ms} onChange={e => updateOption('pacing_delay_ms', e.target.value)} inputMode="numeric" disabled={options.pacing_type !== 'fixed_delay'} />
          </label>

          <label className="sample-import-field">
            <span>Authentication</span>
            <select value={options.auth_type} onChange={e => updateOption('auth_type', e.target.value as AuthType)}>
              <option value="none">None</option>
              <option value="basic">Basic</option>
              <option value="bearer">Bearer</option>
            </select>
          </label>
          <label className="sample-import-field">
            <span>User Agent</span>
            <input value={options.user_agent} onChange={e => updateOption('user_agent', e.target.value)} placeholder="Stormrunner Sample Import" />
          </label>
          <label className="sample-import-field">
            <span>Max Retries</span>
            <input value={options.max_retries} onChange={e => updateOption('max_retries', e.target.value)} inputMode="numeric" />
          </label>

          {options.auth_type === 'basic' && (
            <>
              <label className="sample-import-field">
                <span>Auth Username</span>
                <input value={options.auth_username} onChange={e => updateOption('auth_username', e.target.value)} />
              </label>
              <label className="sample-import-field">
                <span>Auth Password</span>
                <input type="password" value={options.auth_password} onChange={e => updateOption('auth_password', e.target.value)} />
              </label>
            </>
          )}

          {options.auth_type === 'bearer' && (
            <label className="sample-import-field sample-import-span-2">
              <span>Bearer Token</span>
              <input type="password" value={options.auth_token} onChange={e => updateOption('auth_token', e.target.value)} />
            </label>
          )}

          <label className="sample-import-field sample-import-span-3">
            <span>Default Headers (JSON object, optional)</span>
            <textarea value={options.default_headers} onChange={e => updateOption('default_headers', e.target.value)} rows={5} spellCheck={false} placeholder='{"x-tenant-id":"demo"}' />
          </label>
        </div>

        <div className="sample-import-toggles">
          <label className="sample-toggle"><input type="checkbox" checked={options.include_assertions} onChange={e => updateOption('include_assertions', e.target.checked)} />Infer status assertions</label>
          <label className="sample-toggle"><input type="checkbox" checked={options.infer_extractors} onChange={e => updateOption('infer_extractors', e.target.checked)} />Infer extractors and correlation variables</label>
          <label className="sample-toggle"><input type="checkbox" checked={options.sanitize_sensitive} onChange={e => updateOption('sanitize_sensitive', e.target.checked)} />Sanitize obvious secrets</label>
          <label className="sample-toggle"><input type="checkbox" checked={options.continue_on_error} onChange={e => updateOption('continue_on_error', e.target.checked)} />Continue on error</label>
        </div>
      </div>

      <div className="sample-import-actions">
        <button className="btn btn-primary btn-lg" onClick={handleConvert} disabled={converting || !primaryContent.trim()}>
          {converting ? <><span className="spinner" /> Converting…</> : 'Convert Sample'}
        </button>
        <button className="btn btn-secondary" onClick={() => handleFormatChange(format)}>Reset Template</button>
      </div>

      {error && <div className="sample-import-error">{error}</div>}

      {result && (result.script || result.scripts?.[0]) && (
        <div className="sample-import-result">
          <ScriptImportReview
            title="3. Review Generated Script"
            subtitle={`${result.stats.converted_actions} action(s) derived from ${result.stats.total_requests} request sample(s).`}
            script={(result.script || result.scripts?.[0])!}
            warnings={result.warnings}
            saving={saving}
            onSave={handleSave}
            onEdit={onEdit ? () => onEdit((result.script || result.scripts?.[0]) as ScriptDefinition) : undefined}
          >

            <div className="sample-import-stats-grid">
              <div className="sample-stat"><span className="sample-stat-value">{result.stats.converted_actions}</span><span className="sample-stat-label">Actions</span></div>
              <div className="sample-stat"><span className="sample-stat-value">{result.stats.assertions}</span><span className="sample-stat-label">Assertions</span></div>
              <div className="sample-stat"><span className="sample-stat-value">{result.stats.extractors}</span><span className="sample-stat-label">Extractors</span></div>
              <div className="sample-stat"><span className="sample-stat-value">{result.stats.correlations_detected}</span><span className="sample-stat-label">Correlations</span></div>
            </div>

            {result.correlations.length > 0 && (
              <div className="sample-import-correlations">
                <h4>Correlation Suggestions</h4>
                <div className="sample-import-correlation-list">
                  {result.correlations.map((correlation) => (
                    <div key={correlation.name} className="sample-correlation-card">
                      <strong>{correlation.name}</strong>
                      <span>{correlation.source} via {correlation.expression}</span>
                      <span>Step {correlation.found_in_step}{' -> '}{correlation.used_in_steps.join(', ')}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </ScriptImportReview>
        </div>
      )}
    </div>
  )
}

function parseDefaultHeaders(value: string, validateOnly = false) {
  if (!value.trim()) {
    return validateOnly ? true : null
  }
  try {
    const parsed = JSON.parse(value)
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      return validateOnly ? false : null
    }
    const normalized: Record<string, string> = {}
    Object.entries(parsed).forEach(([key, entryValue]) => {
      normalized[key] = String(entryValue)
    })
    return validateOnly ? true : normalized
  } catch {
    return validateOnly ? false : null
  }
}