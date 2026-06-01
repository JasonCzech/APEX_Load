import { useEffect, useMemo, useState } from 'react'
import { type AuthoringComponent, type AuthoringTemplate, type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { useApi } from '../hooks/useApi'
import PageViewToggle from './PageViewToggle'

interface Props {
  onBack: () => void
  onStartApiScript: () => void
  onStartSoapScript: () => void
  onStartJavaHttpScript: () => void
  onStartWebSocketScript: () => void
  onStartGrpcScript: () => void
  onStartDatabaseScript: () => void
  onStartNetworkScript: () => void
  onOpenJavaHttpImport: () => void
  onOpenSampleImport: () => void
  onStartWebScript: () => void
  onOpenRecorder: () => void
  onStartJmsScript: () => void
  onStartTemplateScript: (script: ScriptDefinition) => void
  onComposeComponent: (component: AuthoringComponent) => void
  onOpenLoadRunnerImport: () => void
  onOpenJMeterImport: () => void
  onOpenDevWebImport: () => void
  onOpenHARImport: () => void
  onOpenOpenAPIImport: () => void
  onOpenPostmanImport: () => void
  onOpenWSDLImport: () => void
  scripts?: StoredScript[]
  onStartExistingScript?: (script: StoredScript) => void
}

type ExistingScriptSortKey = 'name' | 'protocol' | 'health' | 'usage' | 'last_run'
type ExistingScriptSortDirection = 'asc' | 'desc'

function formatScriptStatus(status?: string) {
  return status ? status.replace(/_/g, ' ') : 'not scored'
}

function formatShortDate(value?: string) {
  if (!value) return 'Never'
  const timestamp = Date.parse(value)
  if (Number.isNaN(timestamp)) return 'Never'
  return new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', year: 'numeric' }).format(new Date(timestamp))
}

function getScriptReleaseNames(script: StoredScript) {
  const releases = [
    ...(script.associated_releases || []),
    ...(script.scenario_run_releases || []),
  ]
  return Array.from(new Set(releases.map(release => release.name).filter(Boolean)))
}

function getExistingScriptSearchText(script: StoredScript) {
  return [
    script.name,
    script.protocol,
    script.version,
    script.authoring_health?.status,
    script.authoring_health?.score,
    script.scenario_count,
    formatShortDate(script.last_run_at),
    ...getScriptReleaseNames(script),
  ].filter(value => value !== undefined && value !== null).join(' ').toLowerCase()
}

function compareExistingScripts(a: StoredScript, b: StoredScript, key: ExistingScriptSortKey) {
  if (key === 'usage') {
    return (a.scenario_count || 0) - (b.scenario_count || 0)
  }
  if (key === 'last_run') {
    const aTime = Date.parse(a.last_run_at || a.created_at || '')
    const bTime = Date.parse(b.last_run_at || b.created_at || '')
    return (Number.isNaN(aTime) ? 0 : aTime) - (Number.isNaN(bTime) ? 0 : bTime)
  }
  if (key === 'health') {
    return `${a.authoring_health?.status || 'not_scored'} ${a.authoring_health?.score ?? -1}`.localeCompare(`${b.authoring_health?.status || 'not_scored'} ${b.authoring_health?.score ?? -1}`)
  }
  if (key === 'protocol') {
    return (a.protocol || '').localeCompare(b.protocol || '')
  }
  return a.name.localeCompare(b.name)
}

export default function ScriptCreateOptions({
  onBack,
  onStartApiScript,
  onStartSoapScript,
  onStartJavaHttpScript,
  onStartWebSocketScript,
  onStartGrpcScript,
  onStartDatabaseScript,
  onStartNetworkScript,
  onOpenJavaHttpImport,
  onOpenSampleImport,
  onStartWebScript,
  onOpenRecorder,
  onStartJmsScript,
  onStartTemplateScript,
  onComposeComponent,
  onOpenLoadRunnerImport,
  onOpenJMeterImport,
  onOpenDevWebImport,
  onOpenHARImport,
  onOpenOpenAPIImport,
  onOpenPostmanImport,
  onOpenWSDLImport,
  scripts = [],
  onStartExistingScript,
}: Props) {
  const api = useApi()
  const [templates, setTemplates] = useState<AuthoringTemplate[]>([])
  const [components, setComponents] = useState<AuthoringComponent[]>([])
  const [workflow, setWorkflow] = useState<'existing' | 'start-new' | 'source' | 'record'>('existing')
  const [existingScriptQuery, setExistingScriptQuery] = useState('')
  const [existingScriptProtocol, setExistingScriptProtocol] = useState('all')
  const [existingScriptHealth, setExistingScriptHealth] = useState('all')
  const [existingScriptSort, setExistingScriptSort] = useState<{ key: ExistingScriptSortKey; direction: ExistingScriptSortDirection }>({ key: 'name', direction: 'asc' })

  const existingScriptProtocols = useMemo(() => {
    return Array.from(new Set(scripts.map(script => script.protocol).filter(Boolean))).sort((a, b) => a.localeCompare(b))
  }, [scripts])

  const existingScriptHealthStatuses = useMemo(() => {
    return Array.from(new Set(scripts.map(script => script.authoring_health?.status || 'not_scored'))).sort((a, b) => a.localeCompare(b))
  }, [scripts])

  const visibleExistingScripts = useMemo(() => {
    const query = existingScriptQuery.trim().toLowerCase()
    return scripts
      .filter(script => existingScriptProtocol === 'all' || script.protocol === existingScriptProtocol)
      .filter(script => existingScriptHealth === 'all' || (script.authoring_health?.status || 'not_scored') === existingScriptHealth)
      .filter(script => !query || getExistingScriptSearchText(script).includes(query))
      .slice()
      .sort((a, b) => {
        const result = compareExistingScripts(a, b, existingScriptSort.key)
        const adjusted = existingScriptSort.direction === 'asc' ? result : -result
        return adjusted || a.name.localeCompare(b.name)
      })
  }, [existingScriptHealth, existingScriptProtocol, existingScriptQuery, existingScriptSort, scripts])

  const requestExistingScriptSort = (key: ExistingScriptSortKey) => {
    setExistingScriptSort(prev => {
      if (prev.key === key) {
        return { key, direction: prev.direction === 'asc' ? 'desc' : 'asc' }
      }
      return { key, direction: key === 'usage' || key === 'last_run' ? 'desc' : 'asc' }
    })
  }

  const renderExistingSortLabel = (key: ExistingScriptSortKey, label: string) => {
    if (existingScriptSort.key !== key) return `${label} ↕`
    return `${label} ${existingScriptSort.direction === 'asc' ? '↑' : '↓'}`
  }

  const resetExistingScriptFilters = () => {
    setExistingScriptQuery('')
    setExistingScriptProtocol('all')
    setExistingScriptHealth('all')
    setExistingScriptSort({ key: 'name', direction: 'asc' })
  }

  useEffect(() => {
    let cancelled = false
    Promise.all([
      api.listAuthoringTemplates(),
      api.listAuthoringComponents(),
    ])
      .then(([templateResponse, componentResponse]) => {
        if (!cancelled) {
          setTemplates(templateResponse.templates || [])
          setComponents(componentResponse.components || [])
        }
      })
      .catch(() => {
        if (!cancelled) {
          setTemplates([])
          setComponents([])
        }
      })
    return () => {
      cancelled = true
    }
  }, [api])

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Create Script</h2>
          <p className="page-subtitle">Model from saved work, start a new script, record a browser flow, or import from a source asset.</p>
        </div>
        <PageViewToggle
          value="create-options"
          ariaLabel="Script page view"
          className="script-page-toggle"
          onChange={view => {
            if (view === 'library') onBack()
          }}
          options={[
            { value: 'library', label: 'Script Library' },
            { value: 'create-options', label: 'Create Script Options' },
          ]}
        />
      </div>

      <div className="script-workflow-picker" aria-label="Script creation workflows">
        <button type="button" className={`script-workflow-card ${workflow === 'existing' ? 'active' : ''}`} onClick={() => setWorkflow('existing')}>
          <span>1</span>
          <strong>Model Existing</strong>
          <small>Clone a saved script into a new editable draft.</small>
        </button>
        <button type="button" className={`script-workflow-card ${workflow === 'start-new' ? 'active' : ''}`} onClick={() => setWorkflow('start-new')}>
          <span>2</span>
          <strong>Start New Script</strong>
          <small>Use templates, reusable blocks, or blank protocol editors.</small>
        </button>
        <button type="button" className={`script-workflow-card ${workflow === 'record' ? 'active' : ''}`} onClick={() => setWorkflow('record')}>
          <span>3</span>
          <strong>Record Browser</strong>
          <small>Capture a user journey and review it before save.</small>
        </button>
        <button type="button" className={`script-workflow-card ${workflow === 'source' ? 'active' : ''}`} onClick={() => setWorkflow('source')}>
          <span>4</span>
          <strong>Import From Source</strong>
          <small>Generate or migrate scripts from contracts, collections, traffic captures, source archives, and legacy tool exports.</small>
        </button>
      </div>

      <div className="script-create-lanes compact">
        {workflow === 'existing' && (
          <section className="script-create-lane api full">
            <div className="script-create-lane-header">
              <span className="script-create-kicker">Script library</span>
              <h3>Model From Existing Script</h3>
              <p>Choose a saved script to open a new editable copy. The original script remains unchanged until you save the draft.</p>
            </div>
            <div className="script-create-existing-toolbar">
              <label>
                <span>Search scripts</span>
                <input
                  type="search"
                  aria-label="Search existing scripts"
                  value={existingScriptQuery}
                  onChange={event => setExistingScriptQuery(event.target.value)}
                  placeholder="Find by script, protocol, release, health, or version..."
                />
              </label>
              <label>
                <span>Protocol</span>
                <select aria-label="Filter existing scripts by protocol" value={existingScriptProtocol} onChange={event => setExistingScriptProtocol(event.target.value)}>
                  <option value="all">All protocols</option>
                  {existingScriptProtocols.map(protocol => (
                    <option key={protocol} value={protocol}>{protocol.toUpperCase()}</option>
                  ))}
                </select>
              </label>
              <label>
                <span>Health</span>
                <select aria-label="Filter existing scripts by health" value={existingScriptHealth} onChange={event => setExistingScriptHealth(event.target.value)}>
                  <option value="all">All health</option>
                  {existingScriptHealthStatuses.map(status => (
                    <option key={status} value={status}>{formatScriptStatus(status)}</option>
                  ))}
                </select>
              </label>
              <button type="button" className="script-create-existing-reset" onClick={resetExistingScriptFilters}>Reset</button>
            </div>
            <div className="script-create-existing-summary">
              {visibleExistingScripts.length} of {scripts.length} script{scripts.length === 1 ? '' : 's'}
            </div>
            {scripts.length > 0 ? (
              <div className="script-create-existing-table-wrap">
                <table className="script-create-existing-table">
                  <thead>
                    <tr>
                      <th><button type="button" onClick={() => requestExistingScriptSort('name')}>{renderExistingSortLabel('name', 'Script')}</button></th>
                      <th><button type="button" onClick={() => requestExistingScriptSort('protocol')}>{renderExistingSortLabel('protocol', 'Protocol')}</button></th>
                      <th><button type="button" onClick={() => requestExistingScriptSort('health')}>{renderExistingSortLabel('health', 'Health')}</button></th>
                      <th><button type="button" onClick={() => requestExistingScriptSort('usage')}>{renderExistingSortLabel('usage', 'Usage')}</button></th>
                      <th>Releases</th>
                      <th><button type="button" onClick={() => requestExistingScriptSort('last_run')}>{renderExistingSortLabel('last_run', 'Last Run')}</button></th>
                      <th>Action</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleExistingScripts.map(script => {
                      const releaseNames = getScriptReleaseNames(script)
                      return (
                        <tr key={script.id}>
                          <td>
                            <button type="button" className="script-create-existing-name" onClick={() => onStartExistingScript?.(script)}>
                              {script.name}
                            </button>
                            <span className="script-create-existing-meta">v{script.version || '—'}</span>
                          </td>
                          <td><span className="script-create-type-badge inline">{script.protocol.toUpperCase()}</span></td>
                          <td>
                            <span className={`script-create-health-pill ${script.authoring_health?.status || 'not_scored'}`}>{formatScriptStatus(script.authoring_health?.status)}</span>
                            <span className="script-create-existing-meta">{script.authoring_health?.score ?? '—'} score</span>
                          </td>
                          <td>{script.scenario_count || 0} scenario{script.scenario_count === 1 ? '' : 's'}</td>
                          <td>{releaseNames.length > 0 ? releaseNames.join(', ') : 'No releases'}</td>
                          <td>{formatShortDate(script.last_run_at)}</td>
                          <td>
                            <button type="button" className="script-create-existing-action" onClick={() => onStartExistingScript?.(script)}>Model</button>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
                {visibleExistingScripts.length === 0 && (
                  <div className="script-create-empty">No saved scripts match these filters.</div>
                )}
              </div>
            ) : (
              <div className="script-create-empty">No saved scripts are available to model from yet.</div>
            )}
          </section>
        )}

        {workflow === 'start-new' && (
          <section className="script-create-lane api full">
            <div className="script-create-lane-header">
              <span className="script-create-kicker">New draft authoring</span>
              <h3>Start New Script</h3>
              <p>Choose a starter template, compose a reusable block, or open a blank protocol editor for a new draft.</p>
            </div>
            <div className="script-create-choice-groups">
              <div className="script-create-choice-group">
                <h4>Starter Templates</h4>
                <div className="script-create-grid">
                  {templates.length > 0 ? templates.map(template => (
                    <button key={template.id} className="script-create-option" type="button" onClick={() => onStartTemplateScript(template.script)}>
                      <span className="script-create-type-badge">{template.protocol.toUpperCase()}</span>
                      <h3>{template.name}</h3>
                      <p>{template.description}</p>
                      <div className="script-create-template-tags">
                        <span className="tag-chip use-case">{template.use_case.replace(/_/g, ' ')}</span>
                        {template.tags?.slice(0, 3).map(tag => (
                          <span key={tag} className="tag-chip">{tag}</span>
                        ))}
                      </div>
                      {template.recommended_scenario && (
                        <div className="script-create-recommendation" title="A starting recommendation only — not applied automatically. Set load when you create a scenario.">
                          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"></polygon></svg>
                          <span>Suggested: {template.recommended_scenario.vusers || 1} VUsers / {template.recommended_scenario.duration || 'script default'}</span>
                        </div>
                      )}
                    </button>
                  )) : (
                    <div className="script-create-empty">No starter templates are available from this controller.</div>
                  )}
                </div>
              </div>

              <div className="script-create-choice-group">
                <h4>Blank & Assisted Editors</h4>
                <div className="script-create-grid">
                  <button className="script-create-option primary" onClick={onStartApiScript}>
                    <span className="script-create-type-badge">API</span>
                    <h3>API Visual Editor</h3>
                    <p>Build a protocol-level script from requests and reusable action definitions.</p>
                  </button>
                  <button className="script-create-option" onClick={onStartSoapScript}>
                    <span className="script-create-type-badge">SOAP</span>
                    <h3>Web Services (SOAP)</h3>
                    <p>Create a SOAP draft with WS-Security, namespaces, fault handling, and SOAPAction support.</p>
                  </button>
                  <button className="script-create-option" onClick={onStartJavaHttpScript}>
                    <span className="script-create-type-badge">JAVA</span>
                    <h3>Java over HTTP</h3>
                    <p>Create a first-class Java-over-HTTP draft with the `java_serialized_object_base64` codec and choose embedded or sidecar helper execution on the HTTP transport.</p>
                  </button>
                  <button className="script-create-option" onClick={onStartWebSocketScript}>
                    <span className="script-create-type-badge">WS</span>
                    <h3>WebSocket Script</h3>
                    <p>Model connect, send, receive, ping, close, handshake headers, message assertions, and transcript evidence.</p>
                  </button>
                  <button className="script-create-option" onClick={onStartGrpcScript}>
                    <span className="script-create-type-badge">gRPC</span>
                    <h3>gRPC Script</h3>
                    <p>Start unary or streaming calls with reflection/proto setup, metadata, status checks, and message-count assertions.</p>
                  </button>
                  <button className="script-create-option" onClick={onStartDatabaseScript}>
                    <span className="script-create-type-badge">DB</span>
                    <h3>Database Script</h3>
                    <p>Author query, exec, transaction, bind, row/scalar assertion, extractor, and read-only fixture flows.</p>
                  </button>
                  <button className="script-create-option" onClick={onStartNetworkScript}>
                    <span className="script-create-type-badge">NET</span>
                    <h3>Network Protocol Script</h3>
                    <p>Build DNS, TCP, UDP, FTP, SMTP, POP3, IMAP, LDAP, and Winsock-style probes with transcript evidence.</p>
                  </button>
                  <button className="script-create-option" onClick={onOpenSampleImport}>
                    <span className="script-create-type-badge">API</span>
                    <h3>Build From Sample</h3>
                    <p>Generate an API script from sample traffic or payload examples.</p>
                  </button>
                  <button className="script-create-option primary" onClick={onStartWebScript}>
                    <span className="script-create-type-badge web">WEB</span>
                    <h3>Web/HTML Visual Editor</h3>
                    <p>Build a browser-level script that interacts with the page like a user.</p>
                  </button>
                  <button className="script-create-option primary" onClick={onStartJmsScript}>
                    <span className="script-create-type-badge jms">JMS</span>
                    <h3>JMS Solace Script</h3>
                    <p>Create a reusable JMS script draft for Solace publish/consume flows.</p>
                  </button>
                </div>
              </div>

              <div className="script-create-choice-group">
                <h4>Reusable Blocks</h4>
                <div className="script-create-component-list">
                  {components.length > 0 ? components.map(component => (
                    <button key={component.id} type="button" className="script-create-component" onClick={() => onComposeComponent(component)}>
                      <div>
                        <span className="script-create-type-badge inline">{component.kind.replace(/_/g, ' ')}</span>
                        <h3>{component.name}</h3>
                        <p>{component.summary}</p>
                      </div>
                      <div className="script-create-component-meta">
                        <span>{component.protocols.includes('*') ? 'All protocols' : component.protocols.join(', ')}</span>
                        {component.tags?.length ? <span>{component.tags.slice(0, 3).join(' / ')}</span> : null}
                      </div>
                    </button>
                  )) : (
                    <div className="script-create-empty">No reusable blocks are available from this controller.</div>
                  )}
                </div>
              </div>
            </div>
          </section>
        )}

        {workflow === 'record' && (
        <section className="script-create-lane web full">
          <div className="script-create-lane-header">
            <span className="script-create-kicker">Browser capture</span>
            <h3>Record Browser</h3>
            <p>Capture a rendered browser flow, then review and edit the generated draft before promotion.</p>
          </div>
          <div className="script-create-grid">
            <button className="script-create-option" onClick={onOpenRecorder}>
              <span className="script-create-type-badge web">WEB</span>
              <h3>Browser Recorder</h3>
              <p>Capture a browser session and convert selected traffic into reusable Web/HTML script assets.</p>
            </button>
          </div>
        </section>
        )}

        {workflow === 'source' && (
        <section className="script-create-lane import full">
          <div className="script-create-lane-header">
            <span className="script-create-kicker">Source-driven authoring</span>
            <h3>Import From Source</h3>
            <p>Choose a source asset, then review generated actions, source comparisons, data fit, validation notes, and approval evidence in one draft workflow.</p>
          </div>
          <div className="script-create-import-groups">
            <div className="script-create-import-group">
              <h4>Contracts & Collections</h4>
              <div className="script-create-grid">
                <button className="script-create-option primary" onClick={onOpenOpenAPIImport}>
                  <span className="script-create-type-badge">OAS</span>
                  <h3>OpenAPI / Swagger</h3>
                  <p>Generate grouped REST request flows with detected auth schemes, examples, variables, and candidate data pools.</p>
                </button>
                <button className="script-create-option primary" onClick={onOpenPostmanImport}>
                  <span className="script-create-type-badge">PM</span>
                  <h3>Postman Collection</h3>
                  <p>Convert collection folders, requests, variables, and auth placeholders into a reviewable HTTP script.</p>
                </button>
                <button className="script-create-option primary" onClick={onOpenWSDLImport}>
                  <span className="script-create-type-badge">WSDL</span>
                  <h3>WSDL / SOAP</h3>
                  <p>Generate SOAP operation actions with SOAPAction, namespaces, sample envelopes, and fault-review guidance.</p>
                </button>
              </div>
            </div>

            <div className="script-create-import-group">
              <h4>Traffic & Samples</h4>
              <div className="script-create-grid">
                <button className="script-create-option" onClick={onOpenHARImport}>
                  <span className="script-create-type-badge import">HAR</span>
                  <h3>HAR Traffic</h3>
                  <p>Convert captured browser API traffic into HTTP actions with reviewable correlation and data-source suggestions.</p>
                </button>
                <button className="script-create-option" onClick={onOpenSampleImport}>
                  <span className="script-create-type-badge">API</span>
                  <h3>Request / Response Sample</h3>
                  <p>Build an API draft from raw HTTP, cURL, or payload examples when formal contracts are unavailable.</p>
                </button>
                <button
                  className="script-create-option"
                  onClick={onOpenJavaHttpImport}
                  title="Import raw HTTP or cURL samples that already preserve base64-encoded serialized Java payloads."
                >
                  <span className="script-create-type-badge import">JAVA</span>
                  <h3>Import Java over HTTP</h3>
                  <p>Build a Java-over-HTTP draft from raw HTTP or cURL samples that already carry opaque base64-encoded serialized payloads.</p>
                </button>
              </div>
            </div>

            <div className="script-create-import-group">
              <h4>Legacy & Tool Exports</h4>
              <div className="script-create-grid">
                <button className="script-create-option primary" onClick={onOpenLoadRunnerImport}>
                  <span className="script-create-type-badge import">LR</span>
                  <h3>Import LoadRunner</h3>
                  <p>Convert a LoadRunner ZIP into a APEX Load script and keep unsupported functions visible for cleanup.</p>
                </button>
                <button className="script-create-option" onClick={onOpenJMeterImport}>
                  <span className="script-create-type-badge import">JMX</span>
                  <h3>Import JMeter</h3>
                  <p>Convert a JMeter .jmx plan into HTTP scripts with warnings, unsupported elements, and scenario draft context.</p>
                </button>
                <button className="script-create-option" onClick={onOpenDevWebImport}>
                  <span className="script-create-type-badge import">DEV</span>
                  <h3>DevWeb Source</h3>
                  <p>Convert DevWeb JavaScript assets into HTTP, WebSocket, or gRPC script drafts for review.</p>
                </button>
                <button className="script-create-option" type="button" disabled title="Use Import LoadRunner today for SOAP-style LoadRunner request assets.">
                  <span className="script-create-type-badge import">SOAP</span>
                  <h3>Import SOAP Assets</h3>
                  <p>Dedicated SOAP import remains staged. For now, use Import LoadRunner for SOAP-style request assets and review the converted draft.</p>
                </button>
              </div>
            </div>
          </div>
        </section>
        )}
      </div>
    </div>
  )
}
