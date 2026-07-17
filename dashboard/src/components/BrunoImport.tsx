import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import {
  type BrunoCollectionItem,
  type BrunoInspectResponse,
  type BrunoProjectionPreview,
  type BrunoSourceRevision,
  type BrunoSupportMatrixResponse,
  type CommitBrunoImportResponse,
  type SecretBindingRequirement,
  type SecretBindingSelection,
} from '../types/scriptImports'
import { useApi } from '../hooks/useApi'
import { resolveApiResourceUrl } from '../hooks/apiClient'
import { getErrorMessage } from '../utils/errorUtils'
import SecretBindingReview, {
  type PreparedSecretBindings,
  type SecretBindingReviewHandle,
} from './SecretBindingReview'
import ParameterizationReview from './ParameterizationReview'
import { formatBytes } from './settingsPageFormatters'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import {
  formatSourceSecretSessionFailure,
  getSourceSecretSessionFailureMetadata,
} from './sourceSecretSession'
import './BrunoImport.css'

interface Props {
  initialFiles?: File[] | null
  projectId?: string
  onImported?: (result: ImportedSaveResult) => void
  onConfigureSecrets?: (projectId: string) => void
  onClose?: () => void
}

type RunOptions = {
  selection: string
  paths: string
  includeTags: string
  excludeTags: string
  recursive: boolean
  execution: string
  delayMs: number
  streamTimeoutMs: number
  bail: boolean
  testsOnly: boolean
  persistSession: boolean
  defaultEnvironment: string
  defaultGlobalEnvironment: string
}

const DEFAULT_RUN_OPTIONS: RunOptions = {
  selection: 'collection',
  paths: '',
  includeTags: '',
  excludeTags: '',
  recursive: true,
  execution: 'sequential',
  delayMs: 0,
  streamTimeoutMs: 60000,
  bail: false,
  testsOnly: false,
  persistSession: false,
  defaultEnvironment: '',
  defaultGlobalEnvironment: '',
}

function splitList(value: string) {
  return value.split(',').map(item => item.trim()).filter(Boolean)
}

function storedScriptFromCommit(response: CommitBrunoImportResponse): StoredScript {
  const script = response.script
  return {
    id: script.id || '',
    ref_key: script.ref_key,
    name: script.name,
    protocol: script.protocol,
    version: script.version,
    project_id: script.project_id,
    folder_id: script.folder_id,
    created_at: script.created_at || new Date().toISOString(),
    script,
  }
}

function CollectionTree({ items }: { items: BrunoCollectionItem[] }) {
  if (items.length === 0) return <p className="bruno-empty">No collection items were detected.</p>
  return (
    <ul className="bruno-tree">
      {items.map(item => (
        <li key={`${item.uid}-${item.path}`}>
          <div className="bruno-tree-row">
            <span className={`bruno-type bruno-type-${item.type}`}>{item.type}</span>
            <span>{item.name}</span>
            {!item.executable && <small>preserved only</small>}
            {item.has_scripts && <small>scripts</small>}
            {item.has_tests && <small>tests</small>}
          </div>
          {item.children && item.children.length > 0 && <CollectionTree items={item.children} />}
        </li>
      ))}
    </ul>
  )
}

export default function BrunoImport({ initialFiles, projectId, onImported, onConfigureSecrets, onClose }: Props) {
  const api = useApi()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const directoryInputRef = useRef<HTMLInputElement>(null)
  const [files, setFiles] = useState<File[]>(initialFiles || [])
  const [name, setName] = useState('')
  const [sandbox, setSandbox] = useState<'safe' | 'developer'>('safe')
  const [syncScriptId, setSyncScriptId] = useState('')
  const [runOptions, setRunOptions] = useState<RunOptions>(DEFAULT_RUN_OPTIONS)
  const [inspection, setInspection] = useState<BrunoInspectResponse | null>(null)
  const inspectionRef = useRef<BrunoInspectResponse | null>(inspection)
  inspectionRef.current = inspection
  const [support, setSupport] = useState<BrunoSupportMatrixResponse | null>(null)
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [migrateSecrets, setMigrateSecrets] = useState(false)
  const [preparedSecrets, setPreparedSecrets] = useState<PreparedSecretBindings | null>(null)
  const secretBindingsRef = useRef<Record<string, string>>({})
  const credentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const [conflictResolutions, setConflictResolutions] = useState<Record<string, string>>({})
  const [committed, setCommitted] = useState<CommitBrunoImportResponse | null>(null)
  const [committedDeferredSecretCount, setCommittedDeferredSecretCount] = useState(0)
  const [parameterizationSelectedIds, setParameterizationSelectedIds] = useState<string[]>([])
  const [parameterizationApplying, setParameterizationApplying] = useState(false)
  const [revisions, setRevisions] = useState<BrunoSourceRevision[]>([])
  const [projection, setProjection] = useState<BrunoProjectionPreview | null>(null)
  const [loading, setLoading] = useState(false)
  const [rollbackLoading, setRollbackLoading] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const sourceGenerationRef = useRef(0)
  const [sourceGeneration, setSourceGeneration] = useState(0)
  const advanceSourceGeneration = useCallback(() => {
    const next = sourceGenerationRef.current + 1
    sourceGenerationRef.current = next
    setSourceGeneration(next)
    return next
  }, [])
  const sourceProjectIdRef = useRef(projectId)
  sourceProjectIdRef.current = projectId

  useEffect(() => {
    directoryInputRef.current?.setAttribute('webkitdirectory', '')
    directoryInputRef.current?.setAttribute('directory', '')
  }, [])

  useEffect(() => {
    advanceSourceGeneration()
    setFiles(initialFiles || [])
    setInspection(null)
    setCommitted(null)
    setCommittedDeferredSecretCount(0)
    setPreparedSecrets(null)
    setMigrateSecrets(false)
    secretBindingsRef.current = {}
    setLoading(false)
    setError('')
    setNotice('')
  }, [advanceSourceGeneration, initialFiles])

  useEffect(() => {
    advanceSourceGeneration()
    setInspection(null)
    setCommitted(null)
    setCommittedDeferredSecretCount(0)
    setPreparedSecrets(null)
    setMigrateSecrets(false)
    secretBindingsRef.current = {}
    setLoading(false)
    setError('')
    setNotice('')
  }, [advanceSourceGeneration, projectId])

  useEffect(() => {
    let cancelled = false
    void Promise.all([
      api.getBrunoSupportMatrix().catch(() => null),
      projectId ? api.listScripts(projectId).catch(() => ({ scripts: [] as StoredScript[], count: 0 })) : Promise.resolve({ scripts: [] as StoredScript[], count: 0 }),
    ]).then(([matrix, response]) => {
      if (cancelled) return
      setSupport(matrix)
      setScripts((response.scripts || []).filter(script => script.protocol === 'bruno'))
    })
    return () => { cancelled = true }
  }, [api, projectId])

  const requirements = useMemo(() => inspection?.result.secret_requirements || [], [inspection])
  const credentialRequirements = useMemo<SecretBindingRequirement[]>(() => requirements.map(requirement => ({
    id: requirement.id,
    path: requirement.path,
    label: requirement.suggested_name,
    context: requirement.context,
    suggested_name: requirement.suggested_name,
    replacement: requirement.replacement,
    source_value_available: requirement.source_value_available,
  })), [requirements])
  const sourceMigrationAvailable = Boolean(
    support?.secret_store?.source_secret_migration_policy_enabled
    && support.secret_store.writable
    && requirements.some(requirement => requirement.source_value_available),
  )
  const conflicts = inspection?.sync_preview?.conflicts || []
  const unresolvedConflicts = conflicts.filter(conflict => conflictResolutions[conflict.id] !== conflict.required_resolution)
  const selectedBytes = useMemo(() => files.reduce((total, file) => total + file.size, 0), [files])
  const canInspect = Boolean(projectId && files.length > 0 && !loading)
	const selectionMissing = (runOptions.selection === 'folder' || runOptions.selection === 'request') && splitList(runOptions.paths).length === 0
  const credentialsValid = requirements.length === 0 || preparedSecrets?.valid === true
  const canCommit = Boolean(
    inspection &&
    !loading &&
    !parameterizationApplying &&
    credentialsValid &&
	!selectionMissing && unresolvedConflicts.length === 0 &&
    (!syncScriptId || inspection.sync_preview),
  )
  const deferredSecretCount = preparedSecrets?.configureLaterCount ?? 0
  const commitActionLabel = syncScriptId ? 'Synchronize Bruno source' : 'Import Bruno collection'
  const commitButtonLabel = loading
    ? 'Committing…'
    : deferredSecretCount > 0
      ? `${commitActionLabel} with ${deferredSecretCount} ${deferredSecretCount === 1 ? 'secret' : 'secrets'} to configure`
      : commitActionLabel
  const developerBlocked = sandbox === 'developer' && support && !support.developer_execution_enabled

  useEffect(() => {
    if (!sourceMigrationAvailable) setMigrateSecrets(false)
  }, [sourceMigrationAvailable])

  useEffect(() => {
    setParameterizationSelectedIds([])
    setParameterizationApplying(false)
  }, [inspection?.import_session_id])

  const selectFiles = (next: File[]) => {
    advanceSourceGeneration()
    setFiles(next)
    setInspection(null)
    setCommitted(null)
    setCommittedDeferredSecretCount(0)
    setPreparedSecrets(null)
    setMigrateSecrets(false)
    secretBindingsRef.current = {}
    setConflictResolutions({})
    setError('')
    setNotice('')
  }

  const inspect = async () => {
    if (!projectId || files.length === 0) return
    const sourceRequestGeneration = advanceSourceGeneration()
    const sourceProjectId = sourceProjectIdRef.current
    const requestIsCurrent = () => sourceGenerationRef.current === sourceRequestGeneration && sourceProjectIdRef.current === sourceProjectId
    setLoading(true)
    setError('')
    setNotice('')
    setCommitted(null)
    setPreparedSecrets(null)
    setMigrateSecrets(false)
    try {
      const response = await api.inspectBrunoImport(files, {
        project_id: projectId,
        name: name.trim() || undefined,
        sandbox,
        sync_script_id: syncScriptId || undefined,
        run_options: {
          default_environment: runOptions.defaultEnvironment || undefined,
          default_global_environment: runOptions.defaultGlobalEnvironment || undefined,
          selection: runOptions.selection,
          paths: splitList(runOptions.paths),
          include_tags: splitList(runOptions.includeTags),
          exclude_tags: splitList(runOptions.excludeTags),
          recursive: runOptions.recursive,
          execution: runOptions.execution,
          delay_ms: runOptions.delayMs,
          stream_timeout_ms: runOptions.streamTimeoutMs,
          bail: runOptions.bail,
          tests_only: runOptions.testsOnly,
          persist_session: runOptions.persistSession,
        },
      })
      if (!requestIsCurrent()) return
      setInspection(response)
      const nextBindings: Record<string, string> = {}
      ;(response.result.secret_requirements || []).forEach(requirement => {
        nextBindings[requirement.id] = secretBindingsRef.current[requirement.id] || requirement.suggested_name
      })
      secretBindingsRef.current = nextBindings
      setConflictResolutions({})
      setNotice(`Inspection preserved ${response.result.fidelity.preserved_files} source artifacts with ${response.result.fidelity.dropped_items} dropped items.`)
    } catch (cause) {
      if (requestIsCurrent()) setError(getErrorMessage(cause, 'Unable to inspect the Bruno source.'))
    } finally {
      if (requestIsCurrent()) setLoading(false)
    }
  }

  const refreshRevisionState = async (scriptId: string, requestIsCurrent: () => boolean = () => true) => {
    const [revisionResponse, projectionResponse] = await Promise.all([
      api.listBrunoRevisions(scriptId).catch(() => ({ revisions: [], count: 0 })),
      api.previewBrunoProjection(scriptId).catch(() => null),
    ])
    if (!requestIsCurrent()) return
    setRevisions(revisionResponse.revisions || [])
    setProjection(projectionResponse)
  }

  const commit = async () => {
    if (!inspection || !projectId || !canCommit) return
    const sourceRequestGeneration = sourceGenerationRef.current
    const sourceProjectId = sourceProjectIdRef.current
    const sourceImportSessionId = inspection.import_session_id
    const requestIsCurrent = () => sourceGenerationRef.current === sourceRequestGeneration
      && sourceProjectIdRef.current === sourceProjectId
      && inspectionRef.current?.import_session_id === sourceImportSessionId
    setLoading(true)
    setError('')
    try {
      if (requirements.length > 0) {
        if (!credentialReviewRef.current) throw new Error('Credential preparation is unavailable.')
        const committedCredentials = await credentialReviewRef.current.commit()
        if (!committedCredentials) return
      }
      if (!requestIsCurrent()) return
      const response = await api.commitBrunoImport({
        project_id: projectId,
        import_session_id: inspection.import_session_id,
        mode: syncScriptId ? 'sync' : 'create',
        sync_script_id: syncScriptId || undefined,
        name: name.trim() || undefined,
        expected_updated_at: inspection.sync_preview?.expected_updated_at,
        expected_active_revision_id: inspection.sync_preview?.active_revision,
        migrate_secrets: migrateSecrets,
        parameterization_selected_ids: parameterizationSelectedIds,
        secret_bindings: secretBindingsRef.current,
        conflict_resolutions: conflictResolutions,
      })
      if (!requestIsCurrent()) return
      setMigrateSecrets(false)
      setPreparedSecrets(null)
      secretBindingsRef.current = {}
      setCommitted(response)
      setCommittedDeferredSecretCount(deferredSecretCount)
      setNotice(syncScriptId ? 'Bruno source synchronized as a new immutable revision.' : 'Bruno collection imported as a canonical APEX script.')
      if (response.script.id) {
        await refreshRevisionState(response.script.id, requestIsCurrent)
        try {
          const status = await api.getScriptSecretStatus(response.script.id)
          if (requestIsCurrent()) setCommittedDeferredSecretCount(status.missing?.length || 0)
        } catch {
          // The pre-commit write-only summary is a safe fallback when the status
          // endpoint is temporarily unavailable.
        }
      }
    } catch (cause) {
      if (requestIsCurrent()) {
        const sourceSessionFailure = getSourceSecretSessionFailureMetadata(cause)
        if (migrateSecrets && (sourceSessionFailure.consumed || sourceSessionFailure.manualConfigurationRequired)) {
          // The server reports only logical names here. Clear the consumed
          // import session and require a fresh inspection; never retain or
          // reconstruct any source credential material in the browser.
          setMigrateSecrets(false)
          setInspection(null)
          setPreparedSecrets(null)
          secretBindingsRef.current = {}
          setError(`${formatSourceSecretSessionFailure(cause)} Re-inspect the Bruno source to continue the script import.`)
        } else {
          setError(getErrorMessage(cause, 'Unable to commit the Bruno import.'))
        }
      }
    } finally {
      if (requestIsCurrent()) setLoading(false)
    }
  }

  const rollback = async (revision: BrunoSourceRevision) => {
    const scriptId = committed?.script.id
    const activeRevision = revisions.find(candidate => candidate.active)
    if (!scriptId || revision.active) return
    setRollbackLoading(revision.id)
    setError('')
    try {
      await api.rollbackBrunoRevision(scriptId, revision.id, activeRevision?.id)
      await refreshRevisionState(scriptId)
      setNotice(`Activated Bruno source revision ${revision.id}.`)
    } catch (cause) {
      setError(getErrorMessage(cause, 'Unable to roll back the Bruno source revision.'))
    } finally {
      setRollbackLoading('')
    }
  }

  const applyCredentialBindings = (bindings: SecretBindingSelection[]) => {
    const next = Object.fromEntries(bindings.map(binding => [binding.requirement_id, binding.secret_name]))
    secretBindingsRef.current = next
  }

  const applyParameterization = (parameterizedScript: ScriptDefinition, _diffs: unknown[], selectedIds: string[]) => {
    const importSessionId = inspection?.import_session_id
    if (!importSessionId) return
    credentialReviewRef.current?.preserveEditsForNextReviewGeneration()
    advanceSourceGeneration()
    setParameterizationSelectedIds(selectedIds)
    setInspection(current => current?.import_session_id === importSessionId
      ? { ...current, result: { ...current.result, script: parameterizedScript } }
      : current)
    setNotice(`Applied ${selectedIds.length} parameterization suggestion${selectedIds.length === 1 ? '' : 's'} to the pending Bruno script.`)
  }

  const commitProjection = async () => {
    if (!committed?.script.id || !projection?.eligible) return
    setLoading(true)
    setError('')
    try {
      await api.commitBrunoProjection(committed.script.id)
      setNotice('Native projection committed with its equivalence proof.')
    } catch (cause) {
      setError(getErrorMessage(cause, 'Unable to commit the Bruno native projection.'))
    } finally {
      setLoading(false)
    }
  }

  const openCommitted = () => {
    if (!committed) return
    onImported?.({ scripts: [storedScriptFromCommit(committed)] })
  }

  const updateRunOption = <K extends keyof RunOptions>(key: K, value: RunOptions[K]) => {
    advanceSourceGeneration()
    setRunOptions(previous => ({ ...previous, [key]: value }))
    setInspection(null)
  }

  return (
    <div className="bruno-import">
      <header className="bruno-import-header">
        <div>
          <span className="bruno-kicker">Full-fidelity source adapter</span>
          <h2>Import Bruno Collection</h2>
          <p>Preserve and execute classic <code>.bru</code>, OpenCollection YAML, archives, environments, scripts, dependencies, certificates, fixtures, and Bruno Apps without translating behavior away.</p>
        </div>
        {onClose && <button className="btn btn-secondary" onClick={onClose}>Back</button>}
      </header>

      {requirements.length > 0 && !committed && (
        <SecretBindingReview
          ref={credentialReviewRef}
          requirements={credentialRequirements}
          reviewGeneration={`${sourceGeneration}:${projectId || ''}`}
          disabled={loading || parameterizationApplying}
          projectId={projectId}
          sourceSecretSessionExpiresAt={inspection?.expires_at}
          sourceMigrationAllowed={sourceMigrationAvailable}
          externalSourceMigrationSelected={migrateSecrets}
          onExternalSourceMigrationChange={setMigrateSecrets}
          onApply={applyCredentialBindings}
          onPreparedChange={setPreparedSecrets}
        />
      )}

      {inspection?.parameterization && inspection.parameterization.suggestions.length > 0 && !committed && (
        <ParameterizationReview
          parameterization={inspection.parameterization}
          script={inspection.result.script}
          reviewGeneration={`${sourceGeneration}:${projectId || ''}`}
          disabled={loading}
          onApplyingChange={setParameterizationApplying}
          onApplied={applyParameterization}
        />
      )}

      {error && <div className="settings-error" role="alert">{error}</div>}
      {notice && <div className="settings-notice" role="status">{notice}</div>}
      {!projectId && <div className="settings-error">Select a project before importing Bruno source.</div>}

      <section className="bruno-card">
        <div className="bruno-card-heading">
          <div>
            <h3>1. Source bundle</h3>
            <p>Choose a complete collection directory, deterministic archive, or standalone request file.</p>
          </div>
          {files.length > 0 && <span className="bruno-stat">{files.length} file{files.length === 1 ? '' : 's'} · {formatBytes(selectedBytes)}</span>}
        </div>
        <div className="bruno-source-actions">
          <button className="btn btn-secondary" disabled={loading} onClick={() => fileInputRef.current?.click()}>Choose files or archive</button>
          <button className="btn btn-secondary" disabled={loading} onClick={() => directoryInputRef.current?.click()}>Choose collection directory</button>
          {files.length > 0 && <button className="btn btn-ghost" disabled={loading} onClick={() => selectFiles([])}>Clear</button>}
        </div>
        <input
          ref={fileInputRef}
          hidden
          disabled={loading}
          multiple
          type="file"
          accept=".bru,.yml,.yaml,.json,.env,.js,.ts,.zip,.tar,.gz,.tgz,application/zip,application/gzip,text/yaml,application/yaml,application/json,text/plain"
          onChange={event => selectFiles(Array.from(event.target.files || []))}
        />
        <input
          ref={directoryInputRef}
          hidden
          disabled={loading}
          multiple
          type="file"
          onChange={event => selectFiles(Array.from(event.target.files || []))}
        />
        {files.length > 0 && (
          <div className="bruno-file-list">
            {files.slice(0, 12).map((file, index) => (
              <span key={`${file.webkitRelativePath || file.name}-${index}`}>{file.webkitRelativePath || file.name}</span>
            ))}
            {files.length > 12 && <span>+ {files.length - 12} more</span>}
          </div>
        )}
      </section>

      <section className="bruno-card">
        <div className="bruno-card-heading">
          <div>
            <h3>2. Import and execution profile</h3>
            <p>These settings become the canonical <code>bruno_run</code> selection. Operator limits can only reduce them.</p>
          </div>
        </div>
        <div className="bruno-options-grid">
          <label>
            <span>Script name</span>
            <input disabled={loading} value={name} onChange={event => { advanceSourceGeneration(); setName(event.target.value); setInspection(null) }} placeholder="Use collection name" />
          </label>
          <label>
            <span>Synchronization target</span>
            <select disabled={loading} value={syncScriptId} onChange={event => { advanceSourceGeneration(); setSyncScriptId(event.target.value); setInspection(null); setCommitted(null) }}>
              <option value="">Create a new canonical script</option>
              {scripts.map(script => <option key={script.id} value={script.id}>{script.name} ({script.ref_key || script.id})</option>)}
            </select>
          </label>
          <label>
            <span>Sandbox</span>
            <select disabled={loading} value={sandbox} onChange={event => { advanceSourceGeneration(); setSandbox(event.target.value as 'safe' | 'developer'); setInspection(null) }}>
              <option value="safe">Safe (default)</option>
              <option value="developer">Developer isolated worker</option>
            </select>
          </label>
          <label>
            <span>Selection</span>
            <select disabled={loading} value={runOptions.selection} onChange={event => updateRunOption('selection', event.target.value)}>
              <option value="collection">Entire collection</option>
              <option value="folder">Folder path</option>
              <option value="request">Request path</option>
              <option value="tag">Tag selection</option>
            </select>
          </label>
          <label>
            <span>Paths (comma separated)</span>
            <input disabled={loading} value={runOptions.paths} onChange={event => updateRunOption('paths', event.target.value)} placeholder="orders/create.yml, smoke/" />
          </label>
          <label>
            <span>Execution</span>
            <select disabled={loading} value={runOptions.execution} onChange={event => updateRunOption('execution', event.target.value)}>
              <option value="sequential">Sequential, source order</option>
              <option value="parallel">Bounded parallel</option>
            </select>
          </label>
          <label>
            <span>Include tags</span>
            <input disabled={loading} value={runOptions.includeTags} onChange={event => updateRunOption('includeTags', event.target.value)} placeholder="smoke, checkout" />
          </label>
          <label>
            <span>Exclude tags</span>
            <input disabled={loading} value={runOptions.excludeTags} onChange={event => updateRunOption('excludeTags', event.target.value)} placeholder="manual, destructive" />
          </label>
          <label>
            <span>Delay between requests (ms)</span>
            <input disabled={loading} type="number" min={0} value={runOptions.delayMs} onChange={event => updateRunOption('delayMs', Math.max(0, Number(event.target.value) || 0))} />
          </label>
          <label>
            <span>Stream deadline (ms)</span>
            <input disabled={loading} type="number" min={1} value={runOptions.streamTimeoutMs} onChange={event => updateRunOption('streamTimeoutMs', Math.max(1, Number(event.target.value) || 1))} />
          </label>
          {inspection?.result.environments && inspection.result.environments.length > 0 && (
            <>
              <label>
                <span>Collection environment</span>
                <select disabled={loading} value={runOptions.defaultEnvironment} onChange={event => updateRunOption('defaultEnvironment', event.target.value)}>
                  <option value="">None</option>
                  {inspection.result.environments.map(environment => <option key={environment.path} value={environment.name}>{environment.name}</option>)}
                </select>
              </label>
              <label>
                <span>Global environment</span>
                <select disabled={loading} value={runOptions.defaultGlobalEnvironment} onChange={event => updateRunOption('defaultGlobalEnvironment', event.target.value)}>
                  <option value="">None</option>
                  {inspection.result.environments.map(environment => <option key={environment.path} value={environment.name}>{environment.name}</option>)}
                </select>
              </label>
            </>
          )}
        </div>
        <div className="bruno-checks">
          <label><input type="checkbox" disabled={loading} checked={runOptions.recursive} onChange={event => updateRunOption('recursive', event.target.checked)} /> Recursive folders</label>
          <label><input type="checkbox" disabled={loading} checked={runOptions.bail} onChange={event => updateRunOption('bail', event.target.checked)} /> Bail on failure</label>
          <label><input type="checkbox" disabled={loading} checked={runOptions.testsOnly} onChange={event => updateRunOption('testsOnly', event.target.checked)} /> Tests only</label>
          <label><input type="checkbox" disabled={loading} checked={runOptions.persistSession} onChange={event => updateRunOption('persistSession', event.target.checked)} /> Persist VUser session across APEX iterations</label>
        </div>
        {developerBlocked && (
          <div className="bruno-preflight warning">
            Developer execution is disabled or no isolated worker class is available. Import remains available, but execution will fail preflight until an operator enables it.
          </div>
        )}
		{selectionMissing && <div className="bruno-preflight warning">Choose at least one path for {runOptions.selection} selection.</div>}
		{inspection?.execution_preflight && !inspection.execution_preflight.ready && (
		  <div className="bruno-preflight warning" role="status">
			Execution preflight is blocked: {inspection.execution_preflight.blockers.join(', ')}. The source can be preserved, but it cannot run until these operator prerequisites are resolved.
		  </div>
		)}
        <button className="btn btn-primary" disabled={!canInspect} onClick={() => void inspect()}>
          {loading ? 'Inspecting…' : inspection ? 'Re-inspect source' : 'Inspect source'}
        </button>
      </section>

      {inspection && (
        <>
          <section className="bruno-summary-grid">
            <article className="bruno-card">
              <h3>Collection</h3>
              <strong>{inspection.result.collection.name}</strong>
              <p>{inspection.result.fidelity.executable_items} executable items · {inspection.result.fidelity.preserved_apps} Bruno Apps preserved</p>
              <CollectionTree items={inspection.result.collection.items} />
            </article>
            <article className="bruno-card">
              <h3>Fidelity and capabilities</h3>
              <dl className="bruno-details">
                <div><dt>Fidelity</dt><dd>{inspection.result.fidelity.status}</dd></div>
                <div><dt>Compatibility</dt><dd>{inspection.result.fidelity.compatibility}</dd></div>
                <div><dt>Bundle</dt><dd>{formatBytes(inspection.result.manifest.total_bytes)}</dd></div>
                <div><dt>Requests</dt><dd>{inspection.result.capabilities.request_types.join(', ') || 'None'}</dd></div>
                <div><dt>Required sandbox</dt><dd>{inspection.result.capabilities.required_sandbox}</dd></div>
              </dl>
              <div className="bruno-capability-list">
                {inspection.result.capabilities.has_javascript && <span>JavaScript</span>}
                {inspection.result.capabilities.has_npm_dependencies && <span>npm dependencies</span>}
                {inspection.result.capabilities.has_local_modules && <span>local modules</span>}
                {inspection.result.capabilities.has_file_bodies && <span>file bodies</span>}
                {inspection.result.capabilities.has_certificates && <span>certificates</span>}
                {inspection.result.capabilities.has_bruno_apps && <span>Bruno Apps</span>}
              </div>
            </article>
          </section>

          {(inspection.result.security_findings || []).length > 0 && (
            <section className="bruno-card">
              <h3>Security findings</h3>
              <div className="bruno-findings">
                {(inspection.result.security_findings || []).map(finding => (
                  <div key={`${finding.code}-${finding.path || ''}`} className={`bruno-finding ${finding.severity}`}>
                    <strong>{finding.code}</strong>
                    <span>{finding.summary}</span>
                    {finding.path && <small>{finding.path}</small>}
                  </div>
                ))}
              </div>
            </section>
          )}

          {(inspection.result.missing_variables || []).length > 0 && (
            <section className="bruno-card">
              <h3>Preflight requirements</h3>
              <p>These values were referenced but not defined in the uploaded source. Provide them through Bruno environments or APEX runtime bindings before execution.</p>
              <div className="bruno-capability-list">
                {(inspection.result.missing_variables || []).map(variable => <span key={variable}>{variable}</span>)}
              </div>
            </section>
          )}

          {inspection.sync_preview && (
            <section className="bruno-card">
              <h3>Synchronization preview</h3>
              <pre className="bruno-json">{JSON.stringify(inspection.sync_preview.changes, null, 2)}</pre>
              {conflicts.length > 0 && (
                <div className="bruno-conflicts">
                  {conflicts.map(conflict => (
                    <label key={conflict.id}>
                      <input
                        type="checkbox"
                        disabled={loading}
                        checked={conflictResolutions[conflict.id] === conflict.required_resolution}
                        onChange={event => setConflictResolutions(previous => ({
                          ...previous,
                          [conflict.id]: event.target.checked ? conflict.required_resolution : '',
                        }))}
                      />
                      <span><strong>{conflict.field}</strong>{conflict.summary}<small>Resolve with: {conflict.required_resolution}</small></span>
                    </label>
                  ))}
                </div>
              )}
            </section>
          )}

          {!committed && (
            <section className="bruno-commit-bar">
              <div>
                <strong>{syncScriptId ? 'Create synchronized revision' : 'Commit canonical Bruno script'}</strong>
                <span>Raw and sanitized source artifacts are immutable, encrypted, and content-addressed.</span>
              </div>
              <button className="btn btn-primary" disabled={!canCommit} onClick={() => void commit()}>
                {commitButtonLabel}
              </button>
            </section>
          )}
        </>
      )}

      {committed && (
        <section className="bruno-card bruno-committed">
          <div className="bruno-card-heading">
            <div>
              <span className="bruno-kicker">Canonical source active</span>
              <h3>{committed.script.name}</h3>
              <p>Revision {revisions.find(revision => revision.active)?.id || committed.revision.id}</p>
              {committedDeferredSecretCount > 0 && (
                <p>{committedDeferredSecretCount} project secret{committedDeferredSecretCount === 1 ? ' still needs' : 's still need'} configuration.</p>
              )}
            </div>
            <div className="settings-inline-actions">
              {committedDeferredSecretCount > 0 && projectId && onConfigureSecrets && (
                <button className="btn btn-secondary" type="button" onClick={() => onConfigureSecrets(projectId)}>Configure secrets</button>
              )}
              <button className="btn btn-primary" onClick={openCommitted}>Open imported script</button>
            </div>
          </div>
          <div className="bruno-source-links">
            <a className="btn btn-secondary" href={resolveApiResourceUrl(`/api/v1/scripts/${committed.script.id}/bruno-imports/source`)}>Download sanitized source</a>
            <a className="btn btn-secondary" href={resolveApiResourceUrl(`/api/v1/scripts/${committed.script.id}/bruno-imports/source?raw=true`)}>Download restricted raw source</a>
          </div>
          {revisions.length > 0 && (
            <div className="bruno-revisions">
              <h4>Source revisions</h4>
              {revisions.map(revision => (
                <div key={revision.id} className={revision.active ? 'active' : ''}>
                  <span><strong>{revision.id}</strong><small>{new Date(revision.created_at).toLocaleString()} · {revision.source_format}</small></span>
                  {revision.active
                    ? <span className="bruno-active-pill">Active</span>
                    : <button className="btn btn-sm btn-secondary" disabled={rollbackLoading === revision.id} onClick={() => void rollback(revision)}>{rollbackLoading === revision.id ? 'Activating…' : 'Roll back'}</button>}
                </div>
              ))}
            </div>
          )}
          {projection && (
            <div className="bruno-projection">
              <div>
                <h4>Native APEX projection</h4>
                <p>{projection.eligible ? 'Differential analysis found an eligible native subset.' : 'Canonical Bruno execution remains authoritative; no equivalence proof is available.'}</p>
              </div>
              <button
                className="btn btn-secondary"
                disabled={!projection.eligible || !support?.native_projection_enabled || loading}
                onClick={() => void commitProjection()}
              >
                Commit proven projection
              </button>
            </div>
          )}
        </section>
      )}
    </div>
  )
}

export function BrunoImportPreviewScript({ script }: { script: ScriptDefinition }) {
  return <pre className="bruno-json">{JSON.stringify(script, null, 2)}</pre>
}
