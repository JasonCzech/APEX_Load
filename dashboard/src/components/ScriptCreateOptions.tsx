import { useEffect, useMemo, useState } from 'react'
import { type AuthoringComponent, type AuthoringTemplate, type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { useApi } from '../hooks/useApi'
import PageViewToggle from './PageViewToggle'
import ReferenceKeyBadge from './ReferenceKeyBadge'

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
  onStartKafkaScript: () => void
  onStartTemplateScript: (script: ScriptDefinition) => void
  onComposeComponent: (component: AuthoringComponent) => void
  onOpenEnterpriseArchiveImport: () => void
  onOpenJMeterImport: () => void
  onOpenDevWebImport: () => void
  onOpenHARImport: () => void
  onOpenOpenAPIImport: () => void
  onOpenPostmanImport: () => void
  onOpenWSDLImport: () => void
  onOpenAsyncAPIImport: () => void
  scripts?: StoredScript[]
  onStartExistingScript?: (script: StoredScript) => void
}

type ExistingScriptSortKey = 'name' | 'health' | 'usage' | 'associated_releases' | 'created_at' | 'last_run'
type ExistingScriptSortDirection = 'asc' | 'desc'
type StartNewCategory = 'templates' | 'editors' | 'blocks'

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

function formatScenarioCount(script: StoredScript) {
  const count = script.scenario_count || 0
  return `${count} scenario${count === 1 ? '' : 's'}`
}

function authoringHealthLabel(script: StoredScript) {
  const health = script.authoring_health
  if (!health) return 'Unknown'
  const status = health.status === 'needs_review' ? 'Needs Review' : health.status === 'ready' ? 'Ready' : health.status.charAt(0).toUpperCase() + health.status.slice(1)
  return `${status} · ${health.score}`
}

function authoringHealthClass(script: StoredScript) {
  const status = script.authoring_health?.status
  if (status === 'blocked') return 'danger'
  if (status === 'needs_review') return 'warning'
  if (status === 'healthy' || status === 'ready') return 'healthy'
  return ''
}

function authoringHealthSummary(script: StoredScript) {
  const inputs = script.authoring_health?.inputs || []
  const blockers = inputs.filter(input => input.status === 'blocker').length
  const warnings = inputs.filter(input => input.status === 'warning').length
  const reviews = inputs.filter(input => input.status === 'needs_review').length
  return { blockers, warnings: warnings + reviews }
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
  if (key === 'associated_releases') {
    return (a.associated_releases || []).length - (b.associated_releases || []).length
  }
  if (key === 'created_at') {
    const aTime = Date.parse(a.created_at || '')
    const bTime = Date.parse(b.created_at || '')
    return (Number.isNaN(aTime) ? 0 : aTime) - (Number.isNaN(bTime) ? 0 : bTime)
  }
  if (key === 'last_run') {
    const aTime = Date.parse(a.last_run_at || a.created_at || '')
    const bTime = Date.parse(b.last_run_at || b.created_at || '')
    return (Number.isNaN(aTime) ? 0 : aTime) - (Number.isNaN(bTime) ? 0 : bTime)
  }
  if (key === 'health') {
    return `${a.authoring_health?.status || 'not_scored'} ${a.authoring_health?.score ?? -1}`.localeCompare(`${b.authoring_health?.status || 'not_scored'} ${b.authoring_health?.score ?? -1}`)
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
  onStartKafkaScript,
  onStartTemplateScript,
  onComposeComponent,
  onOpenEnterpriseArchiveImport,
  onOpenJMeterImport,
  onOpenDevWebImport,
  onOpenHARImport,
  onOpenOpenAPIImport,
  onOpenPostmanImport,
  onOpenWSDLImport,
  onOpenAsyncAPIImport,
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
  const [startNewCategory, setStartNewCategory] = useState<StartNewCategory>('templates')

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
      return { key, direction: key === 'usage' || key === 'associated_releases' || key === 'created_at' || key === 'last_run' ? 'desc' : 'asc' }
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

  const blankEditorRows: Array<{
    id: string
    type: string
    typeClass?: string
    name: string
    description: string
    primary?: boolean
    onSelect: () => void
  }> = [
    {
      id: 'api',
      type: 'API',
      name: 'API Visual Editor',
      description: 'Build a protocol-level script from requests and reusable action definitions.',
      primary: true,
      onSelect: onStartApiScript,
    },
    {
      id: 'soap',
      type: 'SOAP',
      name: 'Web Services (SOAP)',
      description: 'Create a SOAP draft with WS-Security, namespaces, fault handling, and SOAPAction support.',
      onSelect: onStartSoapScript,
    },
    {
      id: 'java-http',
      type: 'JAVA',
      name: 'Java over HTTP',
      description: 'Create a first-class Java-over-HTTP draft with the java_serialized_object_base64 codec and choose embedded or sidecar helper execution on the HTTP transport.',
      onSelect: onStartJavaHttpScript,
    },
    {
      id: 'websocket',
      type: 'WS',
      name: 'WebSocket Script',
      description: 'Model connect, send, receive, ping, close, handshake headers, message assertions, and transcript evidence.',
      onSelect: onStartWebSocketScript,
    },
    {
      id: 'grpc',
      type: 'gRPC',
      name: 'gRPC Script',
      description: 'Start unary or streaming calls with reflection/proto setup, metadata, status checks, and message-count assertions.',
      onSelect: onStartGrpcScript,
    },
    {
      id: 'database',
      type: 'DB',
      name: 'Database Script',
      description: 'Author query, exec, transaction, bind, row/scalar assertion, extractor, and read-only fixture flows.',
      onSelect: onStartDatabaseScript,
    },
    {
      id: 'network',
      type: 'NET',
      name: 'Network Protocol Script',
      description: 'Build DNS, TCP, UDP, FTP, SMTP, POP3, IMAP, LDAP, and Winsock-style probes with transcript evidence.',
      onSelect: onStartNetworkScript,
    },
    {
      id: 'sample',
      type: 'API',
      name: 'Build From Sample',
      description: 'Generate an API script from sample traffic or payload examples.',
      onSelect: onOpenSampleImport,
    },
    {
      id: 'web',
      type: 'WEB',
      typeClass: 'web',
      name: 'Web/HTML Visual Editor',
      description: 'Build a browser-level script that interacts with the page like a user.',
      primary: true,
      onSelect: onStartWebScript,
    },
    {
      id: 'jms',
      type: 'JMS',
      typeClass: 'jms',
      name: 'JMS Solace Script',
      description: 'Create a reusable JMS script draft for Solace publish/consume flows.',
      primary: true,
      onSelect: onStartJmsScript,
    },
    {
      id: 'kafka',
      type: 'KFK',
      typeClass: 'jms',
      name: 'Kafka Script',
      description: 'Author Kafka produce, consume, and request-reply flows with brokers, topics, partition keys, and offset/lag assertions.',
      primary: true,
      onSelect: onStartKafkaScript,
    },
  ]
  const startNewCategoryTabs: Array<{ id: StartNewCategory, label: string, count: number }> = [
    { id: 'templates', label: 'Starter Templates', count: templates.length },
    { id: 'editors', label: 'Blank & Assisted Editors', count: blankEditorRows.length },
    { id: 'blocks', label: 'Reusable Blocks', count: components.length },
  ]

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
            { value: 'create-options', label: 'Script Builder' },
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
                      <th>Source</th>
                      <th><button type="button" onClick={() => requestExistingScriptSort('name')}>{renderExistingSortLabel('name', 'Script')}</button></th>
                      <th><button type="button" onClick={() => requestExistingScriptSort('health')}>{renderExistingSortLabel('health', 'Health')}</button></th>
                      <th><button type="button" onClick={() => requestExistingScriptSort('usage')}>{renderExistingSortLabel('usage', 'Usage')}</button></th>
                      <th><button type="button" onClick={() => requestExistingScriptSort('associated_releases')}>{renderExistingSortLabel('associated_releases', 'Associated Releases')}</button></th>
                      <th><button type="button" onClick={() => requestExistingScriptSort('created_at')}>{renderExistingSortLabel('created_at', 'Create Date')}</button></th>
                      <th><button type="button" onClick={() => requestExistingScriptSort('last_run')}>{renderExistingSortLabel('last_run', 'Last Run')}</button></th>
                      <th>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleExistingScripts.map(script => {
                      const healthSummary = authoringHealthSummary(script)
                      return (
                        <tr key={script.id}>
                          <td><span className="script-health-token">Local</span></td>
                          <td className="cell-name">
                            <button type="button" className="script-create-existing-name" onClick={() => onStartExistingScript?.(script)}>
                              {script.name}
                            </button>
                            <div className="script-table-meta">
                              <span className="protocol-badge">{script.protocol}</span>
                              <span className="script-health-token">v{script.version || '—'}</span>
                              <ReferenceKeyBadge value={script.ref_key} />
                            </div>
                          </td>
                          <td>
                            <span className={`protocol-badge ${authoringHealthClass(script)}`}>{authoringHealthLabel(script)}</span>
                            {script.authoring_health && (
                              <div className="script-health-inline">
                                {healthSummary.blockers > 0 && <span className="script-health-token danger">{healthSummary.blockers} blockers</span>}
                                {healthSummary.warnings > 0 && <span className="script-health-token warning">{healthSummary.warnings} warnings</span>}
                                {healthSummary.blockers === 0 && healthSummary.warnings === 0 && <span className="script-health-token healthy">ready</span>}
                              </div>
                            )}
                          </td>
                          <td>
                            <div className="script-table-usage">
                              <strong>{formatScenarioCount(script)}</strong>
                              <span>{scenarioRunReleaseList(script)}</span>
                            </div>
                          </td>
                          <td>
                            <div className="script-table-usage">
                              <span>{releaseList(script)}</span>
                            </div>
                          </td>
                          <td className="cell-muted">{formatShortDate(script.created_at)}</td>
                          <td className="cell-muted">{formatShortDate(script.last_run_at)}</td>
                          <td style={{ textAlign: 'right' }}>
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
            <div className="script-create-category-tabs" role="tablist" aria-label="Start new script categories">
              {startNewCategoryTabs.map(tab => (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  aria-selected={startNewCategory === tab.id}
                  className={startNewCategory === tab.id ? 'active' : ''}
                  onClick={() => setStartNewCategory(tab.id)}
                >
                  <span>{tab.label}</span>
                  <strong>{tab.count}</strong>
                </button>
              ))}
            </div>
            <div className="script-create-choice-groups">
              {startNewCategory === 'templates' && (
              <div className="script-create-choice-group" role="tabpanel" aria-label="Starter Templates">
                <h4>Starter Templates</h4>
                {templates.length > 0 ? (
                  <div className="script-create-table-list">
                    <table>
                      <thead>
                        <tr>
                          <th>Protocol</th>
                          <th>Template</th>
                          <th>Use Case</th>
                          <th>Tags</th>
                          <th>Suggested Scenario</th>
                          <th style={{ textAlign: 'right' }}>Actions</th>
                        </tr>
                      </thead>
                      <tbody>
                        {templates.map(template => (
                          <tr key={template.id}>
                            <td><span className="script-create-type-badge inline">{template.protocol.toUpperCase()}</span></td>
                            <td className="cell-name">
                              <div>{template.name}</div>
                              <div className="script-create-table-description">{template.description}</div>
                            </td>
                            <td><span className="tag-chip use-case">{template.use_case.replace(/_/g, ' ')}</span></td>
                            <td>
                              <div className="script-create-template-tags compact">
                                {template.tags?.slice(0, 3).map(tag => (
                                  <span key={tag} className="tag-chip">{tag}</span>
                                ))}
                                {!template.tags?.length && <span className="cell-muted">No tags</span>}
                              </div>
                            </td>
                            <td className="cell-muted">
                              {template.recommended_scenario
                                ? `${template.recommended_scenario.vusers || 1} VUsers / ${template.recommended_scenario.duration || 'script default'}`
                                : 'Not specified'}
                            </td>
                            <td style={{ textAlign: 'right' }}>
                              <button type="button" className="btn btn-sm btn-secondary" onClick={() => onStartTemplateScript(template.script)}>Use {template.name}</button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <div className="script-create-empty">No starter templates are available from this controller.</div>
                )}
              </div>
              )}

              {startNewCategory === 'editors' && (
              <div className="script-create-choice-group" role="tabpanel" aria-label="Blank & Assisted Editors">
                <h4>Blank & Assisted Editors</h4>
                <div className="script-create-table-list">
                  <table>
                    <thead>
                      <tr>
                        <th>Type</th>
                        <th>Editor</th>
                        <th>Description</th>
                        <th>Mode</th>
                        <th style={{ textAlign: 'right' }}>Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {blankEditorRows.map(row => (
                        <tr key={row.id}>
                          <td><span className={`script-create-type-badge inline ${row.typeClass || ''}`}>{row.type}</span></td>
                          <td className="cell-name">{row.name}</td>
                          <td><div className="script-create-table-description">{row.description}</div></td>
                          <td><span className={`script-health-token ${row.primary ? 'healthy' : ''}`}>{row.primary ? 'Primary' : 'Standard'}</span></td>
                          <td style={{ textAlign: 'right' }}>
                            <button type="button" className="btn btn-sm btn-secondary" onClick={row.onSelect}>Open {row.name}</button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
              )}

              {startNewCategory === 'blocks' && (
              <div className="script-create-choice-group" role="tabpanel" aria-label="Reusable Blocks">
                <h4>Reusable Blocks</h4>
                {components.length > 0 ? (
                  <div className="script-create-table-list">
                    <table>
                      <thead>
                        <tr>
                          <th>Kind</th>
                          <th>Block</th>
                          <th>Protocols</th>
                          <th>Tags</th>
                          <th style={{ textAlign: 'right' }}>Actions</th>
                        </tr>
                      </thead>
                      <tbody>
                        {components.map(component => (
                          <tr key={component.id}>
                            <td><span className="script-create-type-badge inline">{component.kind.replace(/_/g, ' ')}</span></td>
                            <td className="cell-name">
                              <div>{component.name}</div>
                              <div className="script-create-table-description">{component.summary}</div>
                            </td>
                            <td>{component.protocols.includes('*') ? 'All protocols' : component.protocols.join(', ')}</td>
                            <td>
                              <div className="script-create-template-tags compact">
                                {component.tags?.slice(0, 3).map(tag => <span key={tag} className="tag-chip">{tag}</span>)}
                                {!component.tags?.length && <span className="cell-muted">No tags</span>}
                              </div>
                            </td>
                            <td style={{ textAlign: 'right' }}>
                              <button type="button" className="btn btn-sm btn-secondary" onClick={() => onComposeComponent(component)}>Compose {component.name}</button>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <div className="script-create-empty">No reusable blocks are available from this controller.</div>
                )}
              </div>
              )}
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
                <button className="script-create-option primary" onClick={onOpenAsyncAPIImport}>
                  <span className="script-create-type-badge">ASYNC</span>
                  <h3>AsyncAPI / Kafka</h3>
                  <p>Convert AsyncAPI 2.x/3.x channels into Kafka produce, consume, and request-reply actions with brokers, topics, and sample payloads.</p>
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
                <button className="script-create-option primary" onClick={onOpenEnterpriseArchiveImport}>
                  <span className="script-create-type-badge import">ESA</span>
                  <h3>Import Enterprise Script Archive</h3>
                  <p>Convert an enterprise script archive ZIP into an APEX Load script and keep unsupported source functions visible for cleanup.</p>
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
              </div>
            </div>
          </div>
        </section>
        )}
      </div>
    </div>
  )
}
