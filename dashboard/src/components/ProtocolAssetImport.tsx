import { useState } from 'react'

import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { type StoredScript } from '../types'
import { type DevWebImportResult, type HARImportResult } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import ScriptImportReview from './ScriptImportReview'
import { withFallbackPersistedScript } from './importSaveUtils'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import './SampleScriptImport.css'

type ImportKind = 'devweb' | 'har'

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
    subtitle: 'Upload a DevWeb .js file or ZIP archive and convert common WebRequest, WebSocket, and gRPC invoke calls into Stormrunner DSL.',
    accept: '.js,.zip,application/javascript,text/javascript,application/zip',
    drop: 'Drop a DevWeb .js or .zip file here',
    button: 'Convert DevWeb Script',
  },
  har: {
    title: 'Import HAR',
    subtitle: 'Upload a Chrome DevTools HAR file and convert recorded HTTP entries into a reviewable Stormrunner HTTP script.',
    accept: '.har,application/json',
    drop: 'Drop a .har file here',
    button: 'Convert HAR',
  },
} satisfies Record<ImportKind, Record<string, string>>

type ProtocolImportResult = DevWebImportResult | HARImportResult

export default function ProtocolAssetImport({ kind, projectId, onImported, onEdit, onClose }: Props) {
  const api = useApi()
  const convertTask = useAsyncTask<ProtocolImportResult>()
  const saveTask = useAsyncTask<StoredScript>()
  const result = convertTask.value
  const [file, setFile] = useState<File | null>(null)
  const [name, setName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [messageError, setMessageError] = useState('')
  const labels = copy[kind]

  const handleConvert = async () => {
    if (!file) {
      setMessageError(`Please upload ${kind === 'devweb' ? 'a DevWeb script' : 'a HAR file'}.`)
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
        return kind === 'devweb'
          ? api.importDevWeb(file, options)
          : api.importHAR(file, options)
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
            <input value={name} onChange={event => setName(event.target.value)} placeholder={kind === 'devweb' ? 'Imported DevWeb Script' : 'Imported HAR Script'} />
          </label>
          <label className="sample-import-field">
            <span>Base URL Override</span>
            <input value={baseURL} onChange={event => setBaseURL(event.target.value)} placeholder="https://api.example.com" />
          </label>
        </div>
      </div>

      <div className="sample-import-actions">
        <button className="btn btn-primary btn-lg" onClick={handleConvert} disabled={convertTask.pending || !file}>
          {convertTask.pending ? <><span className="spinner" /> Converting...</> : labels.button}
        </button>
      </div>

      {(messageError || convertTask.error) && <div className="sample-import-error">{messageError || convertTask.error}</div>}

      {result && (result.script || result.scripts?.[0]) && (
        <div className="sample-import-result">
          <ScriptImportReview
            title="Review Generated Script"
            subtitle={`${actionCount} action(s) generated from ${kind === 'devweb' ? 'DevWeb source' : 'HAR entries'}.`}
            script={(result.script || result.scripts?.[0])!}
            warnings={result.warnings}
            saving={saveTask.pending}
            onSave={handleSave}
            onEdit={onEdit ? () => onEdit((result.script || result.scripts?.[0]) as ScriptDefinition) : undefined}
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
