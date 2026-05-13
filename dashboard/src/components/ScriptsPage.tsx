import { Fragment, useCallback, useEffect, useMemo, useState } from 'react'
import { StoredScript, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import { type ScriptDefinition } from '../types/scripts'
import ScriptEditor from './ScriptEditor'
import LoadRunnerImport from './LoadRunnerImport'
import JMeterImport from './JMeterImport'
import RecordingStudio from './RecordingStudio'
import SampleScriptImport from './SampleScriptImport'

type SubView = 'list' | 'create-options' | 'create' | 'edit' | 'import' | 'jmeter-import' | 'recorder' | 'sample'

interface Props {
  initialMode?: SubView
  initialEditId?: string
  onSelectScenario?: (id: string) => void
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function formatDateTime(value?: string) {
  return value ? new Date(value).toLocaleString() : '—'
}

function releaseList(script: StoredScript) {
  const releases = script.associated_releases || []
  if (releases.length === 0) return 'No releases yet'
  return releases.map(release => release.name).join(', ')
}

function scenarioRunReleaseList(script: StoredScript) {
  const releases = script.scenario_run_releases || []
  if (releases.length === 0) return 'No scenario-run releases yet'
  return releases.map(release => release.name).join(', ')
}

type SortValue = string | number

function getScriptSortValue(
  script: StoredScript,
  field: 'name' | 'protocol' | 'version' | 'created_at' | 'scenario_count' | 'associated_releases' | 'scenario_run_releases' | 'last_run_at',
): SortValue {
  if (field === 'created_at' || field === 'last_run_at') {
    return new Date(script[field] || 0).getTime()
  }
  if (field === 'scenario_count') {
    return script.scenario_count || 0
  }
  if (field === 'associated_releases') {
    return (script.associated_releases || []).length
  }
  if (field === 'scenario_run_releases') {
    return (script.scenario_run_releases || []).length
  }
  return String(script[field] || '').toLowerCase()
}

function getRunSortValue(
  run: TestSummary,
  field: 'name' | 'status' | 'scenario_id' | 'release_id' | 'start_time' | 'duration',
): SortValue {
  if (field === 'start_time') {
    return new Date(run.start_time || run.created_at || 0).getTime()
  }
  return String(run[field] || '').toLowerCase()
}

function SortIndicator({ active, direction }: { active: boolean, direction: 'asc' | 'desc' }) {
  return (
    <span className={`sort-icon ${active ? 'active' : ''}`}>
      {active ? (direction === 'asc' ? '↑' : '↓') : '↕'}
    </span>
  )
}

function createApiDraft() {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'http',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
    },
    actions: [],
  }
}

function createWebDraft() {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'browser',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
      browser: {
        engine: 'chromium',
        headless: true,
        capture_screenshots: 'per_action',
        viewport: { width: 1440, height: 900 },
      },
    },
    actions: [],
  }
}

function createJmsDraft() {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'jms-solace',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
      jms: {
        provider: 'solace',
        broker_url: '',
        message_vpn: '',
        client_id: '',
        destination: '',
        destination_type: 'queue',
        reply_to: '',
        acknowledgement: 'auto',
        delivery_mode: 'persistent',
        timeout_ms: 5000,
        mock: true,
      },
    },
    actions: [],
  }
}

export default function ScriptsPage({ initialMode = 'list', initialEditId, onSelectScenario }: Props) {
  const api = useApi()
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [subView, setSubView] = useState<SubView>(initialMode === 'create' ? 'create-options' : initialMode)
  const [editingId, setEditingId] = useState<string | null>(initialEditId || null)
  const [editingScript, setEditingScript] = useState<ScriptDefinition | null>(null)
  const [draftScript, setDraftScript] = useState<ScriptDefinition | null>(null)
  const [sortField, setSortField] = useState<'name' | 'protocol' | 'version' | 'created_at' | 'scenario_count' | 'associated_releases' | 'scenario_run_releases' | 'last_run_at'>('name')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('asc')
  const [expandedScriptId, setExpandedScriptId] = useState<string | null>(null)
  const [scriptRuns, setScriptRuns] = useState<Record<string, TestSummary[]>>({})
  const [loadingRunsFor, setLoadingRunsFor] = useState<string | null>(null)
  const [innerSortField, setInnerSortField] = useState<'name' | 'status' | 'scenario_id' | 'release_id' | 'start_time' | 'duration'>('start_time')
  const [innerSortDirection, setInnerSortDirection] = useState<'asc' | 'desc'>('desc')

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScripts()
      setScripts(res.scripts || [])
    } catch { /* API not available */ }
  }, [api])

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => {
    if (initialMode === 'edit' && initialEditId) handleEdit(initialEditId)
    else if (initialMode && initialMode !== subView) setSubView(initialMode === 'create' ? 'create-options' : initialMode)
  }, [initialEditId, initialMode])

  const handleEdit = async (id: string) => {
    try {
      const res = await api.getScript(id)
      setEditingId(id)
      setEditingScript(res.script)
      setSubView('edit')
    } catch { /* ignore */ }
  }

  const handleDelete = async (id: string) => {
    if (!confirm('Delete this script?')) return
    try { await api.deleteScript(id); refresh() } catch { /* ignore */ }
  }

  const sortedScripts = useMemo(() => {
    return [...scripts].sort((a, b) => {
      const aValue = getScriptSortValue(a, sortField)
      const bValue = getScriptSortValue(b, sortField)
      if (aValue < bValue) return sortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return sortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [scripts, sortField, sortDirection])

  const handleSort = (field: typeof sortField) => {
    if (sortField === field) setSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else { setSortField(field); setSortDirection('asc') }
  }

  const toggleScriptRuns = async (script: StoredScript) => {
    if (expandedScriptId === script.id) {
      setExpandedScriptId(null)
      return
    }
    setExpandedScriptId(script.id)
    if (scriptRuns[script.id]) return
    setLoadingRunsFor(script.id)
    try {
      const res = await api.listTests()
      const linkedRuns = (res.tests || [])
        .filter((test: TestSummary) =>
          !!test.scenario_id && (test.groups || []).some(group => group.script_id === script.id),
        )
        .sort((a: TestSummary, b: TestSummary) => {
          const aTime = new Date(a.start_time || a.created_at || 0).getTime()
          const bTime = new Date(b.start_time || b.created_at || 0).getTime()
          return bTime - aTime
        })
      setScriptRuns(prev => ({ ...prev, [script.id]: linkedRuns }))
    } catch {
      setScriptRuns(prev => ({ ...prev, [script.id]: [] }))
    } finally {
      setLoadingRunsFor(null)
    }
  }

  const handleInnerSort = (field: typeof innerSortField) => {
    if (innerSortField === field) setInnerSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else { setInnerSortField(field); setInnerSortDirection('asc') }
  }

  const getSortedInnerRuns = (runs: TestSummary[]) => {
    return [...runs].sort((a, b) => {
      const aValue = getRunSortValue(a, innerSortField)
      const bValue = getRunSortValue(b, innerSortField)
      if (aValue < bValue) return innerSortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return innerSortDirection === 'asc' ? 1 : -1
      return 0
    })
  }

  const handleSaved = () => { refresh(); setSubView('list'); setEditingId(null); setEditingScript(null); setDraftScript(null) }
  const handleImported = () => { refresh(); setSubView('list') }
  const handleSampleEdit = (script: ScriptDefinition) => { setDraftScript(script); setEditingId(null); setEditingScript(null); setSubView('create') }
  const startApiScript = () => { setDraftScript(createApiDraft()); setEditingId(null); setEditingScript(null); setSubView('create') }
  const startWebScript = () => { setDraftScript(createWebDraft()); setEditingId(null); setEditingScript(null); setSubView('create') }
  const startJmsScript = () => { setDraftScript(createJmsDraft()); setEditingId(null); setEditingScript(null); setSubView('create') }

  if (subView === 'create') {
    return <div className="page"><ScriptEditor initialScript={draftScript} onSave={handleSaved} onClose={() => setSubView('list')} /></div>
  }

  if (subView === 'edit') {
    return <div className="page"><ScriptEditor scriptId={editingId} initialScript={editingScript} onSave={handleSaved} onClose={() => setSubView('list')} /></div>
  }

  if (subView === 'import') {
    return <div className="page"><LoadRunnerImport onImported={handleImported} onClose={() => setSubView('list')} /></div>
  }

  if (subView === 'jmeter-import') {
    return <div className="page"><JMeterImport onImported={handleImported} onClose={() => setSubView('list')} /></div>
  }

  if (subView === 'recorder') {
    return (
      <div className="page">
        <button className="btn btn-secondary" onClick={() => setSubView('create-options')} style={{ alignSelf: 'flex-start' }}>Back to Create Script</button>
        <RecordingStudio />
      </div>
    )
  }

  if (subView === 'sample') {
    return (
      <div className="page">
        <button className="btn btn-secondary" onClick={() => setSubView('create-options')} style={{ alignSelf: 'flex-start' }}>Back to Create Script</button>
        <SampleScriptImport onImported={handleImported} onEdit={handleSampleEdit} onClose={() => setSubView('create-options')} />
      </div>
    )
  }

  if (subView === 'create-options') {
    return (
      <div className="page">
        <div className="page-header">
          <div>
            <h2>Create Script</h2>
            <p className="page-subtitle">Choose how you want to create a reusable script.</p>
          </div>
          <button className="btn btn-secondary" onClick={() => setSubView('list')}>Back to Library</button>
        </div>
        <div className="script-create-lanes">
          <section className="script-create-lane api">
            <div className="script-create-lane-header">
              <span className="script-create-kicker">API level</span>
              <h3>HTTP/API Scripts</h3>
              <p>Exercise service endpoints directly with HTTP methods, headers, payloads, extractors, assertions, and parameter data.</p>
            </div>
            <div className="script-create-grid">
              <button className="script-create-option primary" onClick={startApiScript}>
                <span className="script-create-type-badge">API</span>
                <h3>API Visual Editor</h3>
                <p>Build a protocol-level script from requests and reusable action definitions.</p>
              </button>
              <button className="script-create-option" onClick={() => setSubView('sample')}>
                <span className="script-create-type-badge">API</span>
                <h3>Build From Sample</h3>
                <p>Generate an API script from sample traffic or payload examples.</p>
              </button>
            </div>
          </section>

          <section className="script-create-lane web">
            <div className="script-create-lane-header">
              <span className="script-create-kicker">Web / HTML</span>
              <h3>Browser Scripts</h3>
              <p>Drive a rendered page with browser navigation, clicks, form fills, waits, screenshots, DOM capture, and browser replay.</p>
            </div>
            <div className="script-create-grid">
              <button className="script-create-option primary" onClick={startWebScript}>
                <span className="script-create-type-badge web">WEB</span>
                <h3>Web/HTML Visual Editor</h3>
                <p>Build a browser-level script that interacts with the page like a user.</p>
              </button>
              <button className="script-create-option" onClick={() => setSubView('recorder')}>
                <span className="script-create-type-badge web">WEB</span>
                <h3>Browser Recorder</h3>
                <p>Capture a browser session and convert selected traffic into reusable Web/HTML script assets.</p>
              </button>
            </div>
          </section>

          <section className="script-create-lane jms">
            <div className="script-create-lane-header">
              <span className="script-create-kicker">Messaging</span>
              <h3>JMS Scripts</h3>
              <p>Model Solace queue/topic workloads with reusable publish, consume, and request/reply actions.</p>
            </div>
            <div className="script-create-grid">
              <button className="script-create-option primary" onClick={startJmsScript}>
                <span className="script-create-type-badge jms">JMS</span>
                <h3>JMS Solace Script</h3>
                <p>Create a reusable JMS script draft for Solace publish/consume flows.</p>
              </button>
            </div>
          </section>

          <section className="script-create-lane import">
            <div className="script-create-lane-header">
              <span className="script-create-kicker">Migration</span>
              <h3>Migration Imports</h3>
              <p>Bring existing LoadRunner and JMeter assets forward while preserving source metadata and conversion notes for review.</p>
            </div>
            <div className="script-create-grid">
              <button className="script-create-option primary" onClick={() => setSubView('import')}>
                <span className="script-create-type-badge import">LR</span>
                <h3>Import LoadRunner</h3>
                <p>Convert a LoadRunner ZIP into a Stormrunner script and keep unsupported functions visible for cleanup.</p>
              </button>
              <button className="script-create-option" onClick={() => setSubView('jmeter-import')}>
                <span className="script-create-type-badge import">JMX</span>
                <h3>Import JMeter</h3>
                <p>Convert a JMeter .jmx plan into HTTP scripts with warnings, unsupported elements, and scenario draft context.</p>
              </button>
            </div>
          </section>
        </div>
      </div>
    )
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Scripts</h2>
          <p className="page-subtitle">Reusable scripts. Release associations are inferred from actual test usage.</p>
        </div>
        <button className="btn btn-primary" onClick={() => setSubView('create-options')}>+ Create Script</button>
      </div>

      <div className="page-section">
        <h3 className="section-title">Script Library ({scripts.length})</h3>
        <div className="data-table">
          <table>
            <thead>
              <tr>
                <th onClick={() => handleSort('name')} className="sortable">Name <SortIndicator active={sortField === 'name'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('protocol')} className="sortable">Protocol <SortIndicator active={sortField === 'protocol'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('version')} className="sortable">Version <SortIndicator active={sortField === 'version'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('created_at')} className="sortable">Created <SortIndicator active={sortField === 'created_at'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('scenario_count')} className="sortable">Linked Scenarios <SortIndicator active={sortField === 'scenario_count'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('associated_releases')} className="sortable">Associated Releases <SortIndicator active={sortField === 'associated_releases'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('scenario_run_releases')} className="sortable">Scenario Run Releases <SortIndicator active={sortField === 'scenario_run_releases'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('last_run_at')} className="sortable">Last Run <SortIndicator active={sortField === 'last_run_at'} direction={sortDirection} /></th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sortedScripts.map(script => (
                <Fragment key={script.id}>
                  <tr key={script.id} className="clickable-row" onClick={() => toggleScriptRuns(script)}>
                    <td className="cell-name">{script.name}</td>
                    <td><span className="protocol-badge">{script.protocol}</span></td>
                    <td>{script.version || '—'}</td>
                    <td className="cell-muted">{formatDate(script.created_at)}</td>
                    <td>{script.scenario_count || 0}</td>
                    <td className="cell-muted">{releaseList(script)}</td>
                    <td className="cell-muted">{scenarioRunReleaseList(script)}</td>
                    <td className="cell-muted">{formatDate(script.last_run_at)}</td>
                    <td style={{ textAlign: 'right' }}>
                      <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); handleEdit(script.id) }}>Edit</button>
                      <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={(event) => { event.stopPropagation(); handleDelete(script.id) }}>Delete</button>
                    </td>
                  </tr>
                  {expandedScriptId === script.id && (
                    <tr className="no-hover">
                      <td colSpan={9} style={{ background: 'var(--bg-primary)' }}>
                        <div style={{ padding: '10px 4px' }}>
                          <div className="cell-muted" style={{ marginBottom: 8 }}>
                            Linked Scenario Runs {loadingRunsFor === script.id ? '(loading...)' : `(${(scriptRuns[script.id] || []).length})`}
                          </div>
                          {(scriptRuns[script.id] || []).length === 0 ? (
                            <div className="cell-muted">No linked scenario runs for this script yet.</div>
                          ) : (
                            <div className="data-table">
                              <table>
                                <thead>
                                  <tr>
                                    <th onClick={() => handleInnerSort('name')} className="sortable">
                                      Run Name <SortIndicator active={innerSortField === 'name'} direction={innerSortDirection} />
                                    </th>
                                    <th onClick={() => handleInnerSort('status')} className="sortable">
                                      Status <SortIndicator active={innerSortField === 'status'} direction={innerSortDirection} />
                                    </th>
                                    <th onClick={() => handleInnerSort('scenario_id')} className="sortable">
                                      Scenario ID <SortIndicator active={innerSortField === 'scenario_id'} direction={innerSortDirection} />
                                    </th>
                                    <th onClick={() => handleInnerSort('release_id')} className="sortable">
                                      Release ID <SortIndicator active={innerSortField === 'release_id'} direction={innerSortDirection} />
                                    </th>
                                    <th onClick={() => handleInnerSort('start_time')} className="sortable">
                                      Started <SortIndicator active={innerSortField === 'start_time'} direction={innerSortDirection} />
                                    </th>
                                    <th onClick={() => handleInnerSort('duration')} className="sortable">
                                      Duration <SortIndicator active={innerSortField === 'duration'} direction={innerSortDirection} />
                                    </th>
                                  </tr>
                                </thead>
                                <tbody>
                                  {getSortedInnerRuns(scriptRuns[script.id] || []).map(run => (
                                    <tr key={run.id} className="clickable-row" onClick={(e) => { e.stopPropagation(); onSelectScenario?.(run.id) }}>
                                      <td className="cell-name">{run.name}</td>
                                      <td>{run.status}</td>
                                      <td className="cell-mono">{run.scenario_id || '—'}</td>
                                      <td className="cell-mono">{run.release_id || '—'}</td>
                                      <td className="cell-muted">{formatDateTime(run.start_time || run.created_at)}</td>
                                      <td className="cell-muted">{run.duration}</td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </div>
                          )}
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
              {sortedScripts.length === 0 && <tr><td colSpan={9} className="cell-muted">No scripts yet.</td></tr>}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
