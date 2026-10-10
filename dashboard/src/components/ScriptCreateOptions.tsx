import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { type AuthoringComponent, type AuthoringTemplate, type StoredScript } from '../types'
import { type AuthoringImportSurface } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import { useApi } from '../hooks/useApi'
import { useHasTopbarContributionProvider } from '../hooks/TopbarContributionProvider'
import PageViewToggle from './PageViewToggle'
import { HealthCountBadge, ObjectInfoBadge, ScriptHealthBadge } from './LibraryRowBadges'
import { authoringHealthCounts, prioritizeHealthInputs } from './authoringHealthInputs'
import { SortIndicator } from './SortIndicator'
import './ImportWorkflowGuide.css'

interface Props {
  projectId?: string
  policyRefreshSignal?: number
  onBack: () => void
  onStartApiScript: () => void
  onStartSoapScript: () => void
  onStartJavaHttpScript: () => void
  onStartWebSocketScript: () => void
  onStartGrpcScript: () => void
  onStartDatabaseScript: () => void
  onStartNetworkScript: () => void
  onStartWebScript: () => void
  onOpenRecorder: () => void
  onStartJmsScript: () => void
  onStartKafkaScript: () => void
  onStartTemplateScript: (script: ScriptDefinition) => void
  onComposeComponent: (component: AuthoringComponent) => void
  onOpenImportSurface: (launcher: string, mode?: string | null) => void
  scripts?: StoredScript[]
  onStartExistingScript?: (script: StoredScript, options?: { migrateSecrets?: boolean }) => void
  canMigrateSecrets?: boolean
  workflow?: CreateWorkflow
  onWorkflowChange?: (workflow: CreateWorkflow) => void
}

type ExistingScriptSortKey = 'name' | 'health' | 'usage' | 'associated_releases' | 'created_at' | 'last_run'
type ExistingScriptSortDirection = 'asc' | 'desc'
type StartNewCategory = 'templates' | 'editors' | 'blocks'
export type CreateWorkflow = 'existing' | 'start-new' | 'source'

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
  return (script.associated_releases || []).map(release => release.name).join(', ')
}

function scenarioRunReleaseList(script: StoredScript) {
  return (script.scenario_run_releases || []).map(release => release.name).join(', ')
}

function formatScenarioCount(script: StoredScript) {
  const count = script.scenario_count || 0
  return `${count} scenario${count === 1 ? '' : 's'}`
}

function defaultImportMode(surface: AuthoringImportSurface): string | null {
  return surface.modes?.find(mode => mode.default)?.id || surface.modes?.[0]?.id || null
}

// Best-effort fallback used only when the controller capabilities response omits
// `import_surfaces` (older controllers) or the fetch fails. It MUST stay in sync with the
// backend authoringImportSurfaces() registry; the launcher set is pinned by
// ScriptCreateOptions.fallback.test.ts (and the backend by TestAuthoringCapabilitiesEndpoint)
// so drift fails a test rather than silently shipping a stale offline card list.
export const defaultImportSurfaces: AuthoringImportSurface[] = [
  {
    id: 'openapi',
    title: 'OpenAPI / Swagger',
    description: 'Generate grouped REST request flows with detected auth schemes, examples, variables, and candidate data pools.',
    badge: 'OAS',
    category: 'Contracts & Collections',
    order: 10,
    launcher: 'openapi',
    visible: true,
  },
  {
    id: 'bruno',
    title: 'Bruno Collection',
    description: 'Convert Bruno HTTP requests into native APEX scripts, with source retention and review of unsupported behavior.',
    badge: 'BRU',
    category: 'Contracts & Collections',
    order: 15,
    launcher: 'bruno',
    visible: true,
  },
  {
    id: 'postman',
    title: 'Postman Collection',
    description: 'Convert collection folders, requests, variables, and auth placeholders into a reviewable HTTP script.',
    badge: 'PM',
    category: 'Contracts & Collections',
    order: 20,
    launcher: 'postman',
    visible: true,
  },
  {
    id: 'wsdl',
    title: 'WSDL / SOAP',
    description: 'Generate SOAP operation actions with SOAPAction, namespaces, sample envelopes, and fault-review guidance.',
    badge: 'WSDL',
    category: 'Contracts & Collections',
    order: 30,
    launcher: 'wsdl',
    visible: true,
  },
  {
    id: 'asyncapi',
    title: 'AsyncAPI',
    description: 'Convert AsyncAPI 2.x/3.x channels into Kafka produce, consume, and request-reply actions with brokers, topics, and sample payloads.',
    badge: 'ASYNC',
    category: 'Contracts & Collections',
    order: 40,
    launcher: 'asyncapi',
    visible: true,
  },
  {
    id: 'raml',
    title: 'RAML / Anypoint',
    description: 'Convert RAML resources, methods, examples, and OAuth2 security schemes into reviewable HTTP actions.',
    badge: 'RAML',
    category: 'Contracts & Collections',
    order: 50,
    launcher: 'raml',
    visible: true,
  },
  {
    id: 'traffic_har',
    title: 'HAR Traffic',
    description: 'Convert captured browser/API traffic into HTTP actions; auto-detects Salesforce, Lightning, and Agentforce and generates a native, correlation-rich draft.',
    badge: 'HAR',
    category: 'Traffic & Samples',
    order: 100,
    launcher: 'traffic_har',
    visible: true,
    modes: [
      { id: 'auto_detect', label: 'Auto-detect', description: 'Detect Salesforce traffic and emit the native Salesforce draft when found.', default: true },
      { id: 'force_generic', label: 'Force Generic HAR', description: 'Return the raw HAR-derived HTTP script while still attaching detected Salesforce review.', default: false },
    ],
  },
  {
    id: 'recorder_export',
    title: 'Recorder Export / Playwright Codegen',
    description: 'Convert a Chrome DevTools Recorder export or Playwright codegen script into a browser script with ranked selector candidates.',
    badge: 'REC',
    category: 'Traffic & Samples',
    order: 110,
    launcher: 'recorder_export',
    visible: true,
  },
  {
    id: 'sample',
    title: 'Request / Response Sample',
    description: 'Build an API draft from raw HTTP, cURL, HAR, or payload examples when formal contracts are unavailable.',
    badge: 'API',
    category: 'Traffic & Samples',
    order: 120,
    launcher: 'sample',
    visible: true,
    modes: [
      { id: 'standard_api', label: 'Standard API', description: 'Infer HTTP actions from raw HTTP, cURL, HAR, or structured request/response samples.', default: true },
      { id: 'java_http', label: 'Java over HTTP', description: 'Preserve opaque base64-encoded Java serialized payloads and promote the draft to java_http.', default: false },
    ],
  },
  {
    id: 'enterprise_archive',
    title: 'LoadRunner Script Archive',
    description: 'Import VuGen ZIP or USZ projects, including a package of several projects, and review each script, its settings, and migration requirements.',
    badge: 'ESA',
    category: 'Legacy & Tool Exports',
    order: 200,
    launcher: 'enterprise_archive',
    visible: true,
  },
  {
    id: 'jmeter',
    title: 'JMeter Test Plan',
    description: 'Convert a JMeter .jmx plan into HTTP scripts with warnings, unsupported elements, and scenario draft context.',
    badge: 'JMX',
    category: 'Legacy & Tool Exports',
    order: 210,
    launcher: 'jmeter',
    visible: true,
  },
  {
    id: 'devweb',
    title: 'DevWeb Source',
    description: 'Convert DevWeb JavaScript assets into HTTP, WebSocket, or gRPC script drafts for review.',
    badge: 'DEV',
    category: 'Legacy & Tool Exports',
    order: 220,
    launcher: 'devweb',
    visible: true,
  },
  {
    id: 'k6',
    title: 'k6 Script',
    description: 'Convert supported k6 HTTP flows into APEX Load actions and preserve unsupported JavaScript for review.',
    badge: 'k6',
    category: 'Legacy & Tool Exports',
    order: 230,
    launcher: 'k6',
    visible: true,
  },
  {
    id: 'cloudtest',
    title: 'Akamai CloudTest Export',
    description: 'Inspect CloudTest XML/ZIP exports into fixture-gated Clip scripts, Composition scenarios, and preserved review artifacts.',
    badge: 'CT',
    category: 'Legacy & Tool Exports',
    order: 240,
    launcher: 'cloudtest',
    visible: true,
  },
  {
    id: 'eventhubs_capture',
    title: 'Event Hubs Capture',
    description: 'Seed native Event Hubs producer actions from Azure Event Hubs Capture files or any Avro data file.',
    badge: 'EH',
    category: 'Messaging & Cloud Artifacts',
    order: 300,
    launcher: 'eventhubs_capture',
    visible: true,
  },
  {
    id: 'broker_config',
    title: 'Broker Config',
    description: 'Import IBM MQ MQSC or JMS JNDI broker configuration into a JMS topology draft.',
    badge: 'JMS',
    category: 'Messaging & Cloud Artifacts',
    order: 310,
    launcher: 'broker_config',
    visible: true,
  },
  {
    id: 'oracle_ebusiness_review',
    title: 'Oracle E-Business / Siebel Review',
    description: 'Review Oracle E-Business, Oracle NCA, and Siebel assets for package signals, correlation packs, and execution posture.',
    badge: 'EBS',
    category: 'Enterprise App Reviews',
    order: 400,
    launcher: 'oracle_ebusiness_review',
    visible: true,
  },
]

function importSurfacesFromCapabilities(response: { import_surfaces?: AuthoringImportSurface[] }): AuthoringImportSurface[] {
  if (Array.isArray(response.import_surfaces)) {
    return response.import_surfaces
  }
  return defaultImportSurfaces
}

function policyAllowsFeature(
  policy: { features: Array<{ id: string; effective_enabled: boolean }> } | null | undefined,
  featureId: string,
) {
  return policy?.features.some(feature => feature.id === featureId && feature.effective_enabled) === true
}

function policyFilteredImportSurfaces(
  response: { import_surfaces?: AuthoringImportSurface[]; feature_policy?: { features: Array<{ id: string; effective_enabled: boolean }> } },
  projectId?: string,
) {
  const surfaces = importSurfacesFromCapabilities(response)
  if (!projectId || !response.feature_policy) return surfaces
  return surfaces.filter(surface => policyAllowsFeature(response.feature_policy, `script_import.${surface.id}`))
}

function groupImportSurfaces(surfaces: AuthoringImportSurface[]): Array<{ category: string; surfaces: AuthoringImportSurface[] }> {
  const groups = new Map<string, AuthoringImportSurface[]>()
  surfaces
    .filter(surface => surface.visible)
    .sort((a, b) => a.order - b.order || a.title.localeCompare(b.title))
    .forEach(surface => {
      const entries = groups.get(surface.category) || []
      entries.push(surface)
      groups.set(surface.category, entries)
    })
  return Array.from(groups.entries()).map(([category, entries]) => ({ category, surfaces: entries }))
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

function isSalesforceFamilyTemplate(template: AuthoringTemplate) {
  const text = [
    template.id,
    template.name,
    template.description,
    template.protocol,
    template.use_case,
    ...(template.tags || []),
  ].join(' ').toLowerCase()
  return ['agentforce', 'salesforce', 'mulesoft', 'sse', 'oauth2'].some(signal => text.includes(signal))
}

function templateRuntimeReferences(template: AuthoringTemplate) {
  const content = JSON.stringify(template.script || {})
  const refs = new Set<string>()
  const pattern = /\{\{(env|secret)\.([A-Za-z0-9_.-]+)[^}]*\}\}/g
  let match: RegExpExecArray | null
  while ((match = pattern.exec(content)) !== null) {
    refs.add(`${match[1]}.${match[2]}`)
  }
  return Array.from(refs).sort((a, b) => a.localeCompare(b))
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
  projectId,
  policyRefreshSignal,
  onBack,
  onStartApiScript,
  onStartSoapScript,
  onStartJavaHttpScript,
  onStartWebSocketScript,
  onStartGrpcScript,
  onStartDatabaseScript,
  onStartNetworkScript,
  onStartWebScript,
  onOpenRecorder,
  onStartJmsScript,
  onStartKafkaScript,
  onStartTemplateScript,
  onComposeComponent,
  onOpenImportSurface,
  scripts = [],
  onStartExistingScript,
  canMigrateSecrets = false,
  workflow,
  onWorkflowChange,
}: Props) {
  const api = useApi()
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const [templates, setTemplates] = useState<AuthoringTemplate[]>([])
  const [components, setComponents] = useState<AuthoringComponent[]>([])
  const [importSurfaces, setImportSurfaces] = useState<AuthoringImportSurface[]>([])
  const [importSurfacesLoading, setImportSurfacesLoading] = useState(Boolean(projectId))
  const [importSurfacesError, setImportSurfacesError] = useState('')
  const [sourceQuery, setSourceQuery] = useState('')
  const [featurePolicy, setFeaturePolicy] = useState<{ features: Array<{ id: string; effective_enabled: boolean }> } | null>(null)
  const [featurePolicyResolved, setFeaturePolicyResolved] = useState(!projectId)
  const [internalWorkflow, setInternalWorkflow] = useState<CreateWorkflow>('start-new')
  const [existingScriptQuery, setExistingScriptQuery] = useState('')
  const [existingScriptProtocol, setExistingScriptProtocol] = useState('all')
  const [existingScriptHealth, setExistingScriptHealth] = useState('all')
  const [existingScriptSort, setExistingScriptSort] = useState<{ key: ExistingScriptSortKey; direction: ExistingScriptSortDirection }>({ key: 'name', direction: 'asc' })
  const [startNewCategory, setStartNewCategory] = useState<StartNewCategory>('editors')
  const [sourceSecretMigrationAllowed, setSourceSecretMigrationAllowed] = useState(false)
  const [migrateExistingSecrets, setMigrateExistingSecrets] = useState(false)
  const activeWorkflow = workflow ?? internalWorkflow
  const setActiveWorkflow = useCallback((nextWorkflow: CreateWorkflow) => {
    if (onWorkflowChange) {
      onWorkflowChange(nextWorkflow)
      return
    }
    setInternalWorkflow(nextWorkflow)
  }, [onWorkflowChange])

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

  const existingAriaSort = (key: ExistingScriptSortKey): 'ascending' | 'descending' | 'none' => existingScriptSort.key !== key ? 'none' : existingScriptSort.direction === 'asc' ? 'ascending' : 'descending'

  const renderExistingSortLabel = (key: ExistingScriptSortKey, label: string) => (
    <>{label} <SortIndicator active={existingScriptSort.key === key} direction={existingScriptSort.direction} /></>
  )

  const resetExistingScriptFilters = () => {
    setExistingScriptQuery('')
    setExistingScriptProtocol('all')
    setExistingScriptHealth('all')
    setExistingScriptSort({ key: 'name', direction: 'asc' })
  }

  const featureEnabled = useCallback((id: string) => !projectId || (featurePolicyResolved && (!featurePolicy || policyAllowsFeature(featurePolicy, id))), [featurePolicy, featurePolicyResolved, projectId])
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
      onSelect: () => onOpenImportSurface('sample', 'standard_api'),
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
  ].filter(row => featureEnabled(row.id === 'java-http'
    ? 'script_editor.java_http'
    : row.id === 'web'
      ? 'script_editor.browser'
      : row.id === 'sample'
        ? 'script_import.sample'
        : `script_editor.${row.id}`))
  const startNewCategoryTabs: Array<{ id: StartNewCategory, label: string, count: number }> = useMemo(() => [
    ...(featureEnabled('script_builder.starter_templates') ? [{ id: 'templates' as const, label: 'Starter Templates', count: templates.length }] : []),
    ...(featureEnabled('script_builder.blank_editors') ? [{ id: 'editors' as const, label: 'Blank & Assisted Editors', count: blankEditorRows.length }] : []),
    ...(featureEnabled('script_builder.reusable_blocks') ? [{ id: 'blocks' as const, label: 'Reusable Blocks', count: components.length }] : []),
  ], [blankEditorRows.length, components.length, featureEnabled, templates.length])

  useEffect(() => {
    if (!projectId || !featurePolicy) return
    const availableWorkflows: CreateWorkflow[] = [
      ...(featureEnabled('script_builder.existing_scripts') ? ['existing' as const] : []),
      ...(startNewCategoryTabs.length > 0 ? ['start-new' as const] : []),
      ...(featureEnabled('script_builder.imports') ? ['source' as const] : []),
    ]
    if (!availableWorkflows.includes(activeWorkflow) && availableWorkflows[0]) {
      setActiveWorkflow(availableWorkflows[0])
    }
    if (!startNewCategoryTabs.some(tab => tab.id === startNewCategory) && startNewCategoryTabs[0]) {
      setStartNewCategory(startNewCategoryTabs[0].id)
    }
  }, [activeWorkflow, featureEnabled, featurePolicy, projectId, setActiveWorkflow, startNewCategory, startNewCategoryTabs])

  useEffect(() => {
    let cancelled = false
    Promise.all([
      api.listAuthoringTemplates(projectId ? { projectId } : undefined),
      api.listAuthoringComponents(projectId ? { projectId } : undefined),
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
  }, [api, projectId])

  useEffect(() => {
    if (!canMigrateSecrets || typeof api.getSystemSettings !== 'function') {
      setSourceSecretMigrationAllowed(false)
      setMigrateExistingSecrets(false)
      return
    }
    let cancelled = false
    void api.getSystemSettings()
      .then(settings => {
        if (cancelled) return
        const allowed = settings.script_imports?.allow_source_secret_migration === true
        setSourceSecretMigrationAllowed(allowed)
        if (!allowed) setMigrateExistingSecrets(false)
      })
      .catch(() => {
        if (!cancelled) {
          setSourceSecretMigrationAllowed(false)
          setMigrateExistingSecrets(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [api, canMigrateSecrets])

  const startExistingScript = (script: StoredScript) => {
    if (!onStartExistingScript) return
    if (canMigrateSecrets) {
      onStartExistingScript(script, { migrateSecrets: migrateExistingSecrets })
      return
    }
    onStartExistingScript(script)
  }

  // One fetch shared by the mount/refresh effect and the Retry button. They
  // previously duplicated this call and cleared different state on failure,
  // leaving Retry with a stale feature policy behind an unresolved flag.
  const loadImportSurfaces = useCallback((isCancelled: () => boolean = () => false) => {
    setImportSurfacesLoading(true)
    setImportSurfacesError('')
    setFeaturePolicyResolved(false)
    return api.listAuthoringCapabilities(projectId)
      .then(response => {
        if (isCancelled()) return
        setImportSurfaces(policyFilteredImportSurfaces(response, projectId))
        setFeaturePolicy(response.feature_policy || null)
        setFeaturePolicyResolved(true)
      })
      .catch(error => {
        if (isCancelled()) return
        setImportSurfaces([])
        setFeaturePolicy(null)
        setFeaturePolicyResolved(false)
        setImportSurfacesError(error instanceof Error ? error.message : 'Unable to load import source capabilities.')
      })
      .finally(() => {
        if (!isCancelled()) setImportSurfacesLoading(false)
      })
  }, [api, projectId])

  useEffect(() => {
    let cancelled = false
    void loadImportSurfaces(() => cancelled)
    return () => {
      cancelled = true
    }
  }, [loadImportSurfaces, policyRefreshSignal])

  const visibleImportSurfaces = importSurfaces.filter(surface => surface.visible)
  const filteredImportSurfaces = visibleImportSurfaces.filter(surface => {
    const text = [surface.title, surface.description, surface.category, surface.badge, surface.id,
      ...(surface.modes || []).flatMap(mode => [mode.label, mode.description])].join(' ').toLowerCase()
    return sourceQuery.toLowerCase().trim().split(/\s+/).every(term => text.includes(term))
  })
  const importSurfaceGroups = groupImportSurfaces(filteredImportSurfaces)

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Create script</h2>
          <p className="page-subtitle">Model from saved work, start a new script, record a browser flow, or import from a source asset.</p>
        </div>
        {!hasTopbarContributionProvider && (
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
        )}
      </div>

      {projectId && !featurePolicyResolved && (
        importSurfacesLoading
          ? <div className="script-create-empty" role="status">Loading domain feature access…</div>
          : <div className="script-create-empty" role="alert">
              <p>{importSurfacesError || 'Unable to load domain feature access.'}</p>
              <button type="button" className="btn btn-sm btn-secondary" onClick={() => void loadImportSurfaces()}>Retry</button>
            </div>
      )}

      <div className="script-workflow-picker" aria-label="Script creation workflows">
        {featureEnabled('script_builder.existing_scripts') && <button type="button" className={`script-workflow-card ${activeWorkflow === 'existing' ? 'active' : ''}`} aria-label="Use existing" onClick={() => setActiveWorkflow('existing')}>
          <strong>Use existing</strong>
          <small>Clone a saved script into a new editable draft.</small>
        </button>}
        {startNewCategoryTabs.length > 0 && <button type="button" className={`script-workflow-card ${activeWorkflow === 'start-new' ? 'active' : ''}`} aria-label="Create" onClick={() => setActiveWorkflow('start-new')}>
          <strong>Create</strong>
          <small>Use templates, reusable blocks, or blank protocol editors.</small>
        </button>}
        {featureEnabled('script_builder.browser_recording') && <button type="button" className="script-workflow-card" aria-label="Record" onClick={onOpenRecorder}>
          <strong>Record</strong>
          <small>Capture a user journey and review it before save.</small>
        </button>}
        {featureEnabled('script_builder.imports') && <button type="button" className={`script-workflow-card ${activeWorkflow === 'source' ? 'active' : ''}`} aria-label="Import" onClick={() => setActiveWorkflow('source')}>
          <strong>Import</strong>
          <small>Generate or migrate scripts from contracts, collections, traffic captures, source archives, and legacy tool exports.</small>
        </button>}
      </div>

      <div className="script-create-lanes compact">
        {activeWorkflow === 'existing' && (
          <section className="script-create-lane api full">
            <div className="script-create-lane-header">
              <span className="script-create-kicker">Script library</span>
              <h3>Copy an Existing Script</h3>
              <p>Choose a saved script to open a new editable copy. Saving creates a separate script; the original remains unchanged.</p>
            </div>
            <div className="script-create-existing-toolbar">
              <label>
                <span>Search scripts</span>
                <input
                  type="search"
                  aria-label="Search existing scripts"
                  value={existingScriptQuery}
                  onChange={event => setExistingScriptQuery(event.target.value)}
                  placeholder="Find by script, protocol, release, health, or version…"
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
            {canMigrateSecrets && sourceSecretMigrationAllowed && (
              <label className="credentials-source-migration script-create-secret-migration">
                <input
                  type="checkbox"
                  checked={migrateExistingSecrets}
                  onChange={event => setMigrateExistingSecrets(event.target.checked)}
                />
                <span>
                  Import detected credential values
                  <small>Admin-only and create-only. Existing project secrets are never overwritten.</small>
                </span>
              </label>
            )}
            {scripts.length > 0 ? (
              <div className="script-create-existing-table-wrap">
                <table className="script-create-existing-table">
                  <thead>
                    <tr>
                      <th aria-sort={existingAriaSort('name')}><button type="button" onClick={() => requestExistingScriptSort('name')}>{renderExistingSortLabel('name', 'Script')}</button></th>
                      <th aria-sort={existingAriaSort('health')}><button type="button" onClick={() => requestExistingScriptSort('health')}>{renderExistingSortLabel('health', 'Health')}</button></th>
                      <th aria-sort={existingAriaSort('usage')}><button type="button" onClick={() => requestExistingScriptSort('usage')}>{renderExistingSortLabel('usage', 'Usage')}</button></th>
                      <th aria-sort={existingAriaSort('associated_releases')}><button type="button" onClick={() => requestExistingScriptSort('associated_releases')}>{renderExistingSortLabel('associated_releases', 'Run releases')}</button></th>
                      <th aria-sort={existingAriaSort('created_at')}><button type="button" onClick={() => requestExistingScriptSort('created_at')}>{renderExistingSortLabel('created_at', 'Created')}</button></th>
                      <th aria-sort={existingAriaSort('last_run')}><button type="button" onClick={() => requestExistingScriptSort('last_run')}>{renderExistingSortLabel('last_run', 'Last run')}</button></th>
                      <th>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleExistingScripts.map(script => {
                      const healthCounts = authoringHealthCounts(script)
                      const scenarioRunReleases = scenarioRunReleaseList(script)
                      const runReleases = releaseList(script)
                      return (
                        <tr key={script.id}>
                          <td className="cell-name">
                            <button type="button" className="script-create-existing-name" onClick={() => startExistingScript(script)}>
                              {script.name}
                            </button>
                            <div className="script-table-meta">
                              <span className="protocol-badge">{script.protocol}</span>
                              <span className="script-health-token">v{script.version || '—'}</span>
                              <ObjectInfoBadge name={script.name} refKey={script.ref_key} createdAt={script.created_at} activity={script.activity} />
                            </div>
                          </td>
                          <td aria-label="Authoring health">
                            <div className="script-health-badges">
                              <ScriptHealthBadge name={script.name} health={script.authoring_health} inputs={prioritizeHealthInputs(script.authoring_health?.inputs || [])} />
                              {healthCounts.blockers > 0 && <HealthCountBadge tone="danger" icon="cross" count={healthCounts.blockers} label={`${healthCounts.blockers} ${healthCounts.blockers === 1 ? 'blocker' : 'blockers'}`} />}
                              {healthCounts.warnings > 0 && <HealthCountBadge tone="warning" icon="alert" count={healthCounts.warnings} label={`${healthCounts.warnings} ${healthCounts.warnings === 1 ? 'warning' : 'warnings'}`} />}
                            </div>
                          </td>
                          <td aria-label="Scenario usage">
                            <div className="script-table-usage">
                              <strong>{formatScenarioCount(script)}</strong>
                              {scenarioRunReleases && <span>{scenarioRunReleases}</span>}
                            </div>
                          </td>
                          <td aria-label="Associated releases">
                            {runReleases ? <div className="script-table-usage"><span>{runReleases}</span></div> : <span className="cell-muted">—</span>}
                          </td>
                          <td className="cell-muted">{formatShortDate(script.created_at)}</td>
                          <td className="cell-muted">{formatShortDate(script.last_run_at)}</td>
                          <td style={{ textAlign: 'right' }}>
                            <button type="button" className="script-create-existing-action" onClick={() => startExistingScript(script)}>Model</button>
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

        {activeWorkflow === 'start-new' && (
          <section className="script-create-lane api full">
            <div className="script-create-lane-header">
              <span className="script-create-kicker">New draft authoring</span>
              <h3>Start New Script</h3>
              <p>Choose a starter template, compose a reusable block, or open a blank protocol editor for a new draft.</p>
            </div>
            <TabList className="script-create-category-tabs" role="tablist" aria-label="Start new script categories" onKeyDown={handleTabKey}>
              {startNewCategoryTabs.map(tab => (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  aria-selected={startNewCategory === tab.id}
                  className={startNewCategory === tab.id ? 'active' : ''}
                  onClick={() => setStartNewCategory(tab.id)} tabIndex={(startNewCategory === tab.id) ? 0 : -1}
                >
                  <span>{tab.label}</span>
                  <strong>{tab.count}</strong>
                </button>
              ))}
            </TabList>
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
                        {templates.map(template => {
                          const familyTemplate = isSalesforceFamilyTemplate(template)
                          const runtimeRefs = familyTemplate ? templateRuntimeReferences(template).slice(0, 6) : []
                          return (
                            <tr key={template.id}>
                              <td><span className="script-create-type-badge inline">{template.protocol.toUpperCase()}</span></td>
                              <td className="cell-name">
                                <div>{template.name}</div>
                                <div className="script-create-table-description">{template.description}</div>
                                {runtimeRefs.length > 0 && (
                                  <div className="script-create-table-description cell-muted">
                                    Required values: {runtimeRefs.join(', ')}
                                  </div>
                                )}
                              </td>
                              <td><span className="tag-chip use-case">{template.use_case.replace(/_/g, ' ')}</span></td>
                              <td>
                                <div className="script-create-template-tags compact">
                                  {familyTemplate && <span className="tag-chip use-case">Salesforce Family</span>}
                                  {template.tags?.slice(0, familyTemplate ? 2 : 3).map(tag => (
                                    <span key={tag} className={`tag-chip ${['agentforce', 'salesforce', 'sse', 'oauth2'].includes(tag.toLowerCase()) ? 'use-case' : ''}`}>{tag}</span>
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
                          )
                        })}
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

        {activeWorkflow === 'source' && (
          <section className="script-create-lane import full">
            <div className="script-create-lane-header">
              <span className="script-create-kicker">Source-driven authoring</span>
              <h3>Import from source</h3>
              <p>Choose a source asset, then review generated actions, source comparisons, data fit, validation notes, and approval evidence in one draft workflow.</p>
            </div>
            <div className="import-source-search">
              <label>
                <span>Find a source format</span>
                <input type="search" className="settings-input" value={sourceQuery} onChange={event => setSourceQuery(event.target.value)} placeholder="Search formats, protocols, or tools" />
              </label>
              {sourceQuery && <button type="button" className="btn btn-secondary" onClick={() => setSourceQuery('')}>Clear search</button>}
              {!importSurfacesLoading && !importSurfacesError && <span role="status">{filteredImportSurfaces.length} of {visibleImportSurfaces.length} sources</span>}
            </div>
            <div className="script-create-import-groups">
              {importSurfacesLoading && <div className="script-create-empty">Loading import sources…</div>}
              {importSurfacesError && (
                <div className="script-create-empty" role="alert">
                  <p>{importSurfacesError}</p>
                  <button type="button" className="btn btn-sm btn-secondary" onClick={() => void loadImportSurfaces()}>Retry</button>
                </div>
              )}
              {!importSurfacesLoading && !importSurfacesError && importSurfaceGroups.length === 0 && (
                <div className="script-create-empty">{visibleImportSurfaces.length ? 'No source formats match your search. Try a format such as HAR, JMeter, or OpenAPI.' : 'No import sources are available from this controller.'}</div>
              )}
              {importSurfaceGroups.map(group => (
                <div className="script-create-import-group" key={group.category}>
                  <h4>{group.category}</h4>
                  <div className="script-create-grid">
                    {group.surfaces.map(surface => (
                      <button
                        key={surface.id}
                        type="button"
                        className="script-create-option primary"
                        onClick={() => onOpenImportSurface(surface.launcher, defaultImportMode(surface))}
                      >
                        <span className="script-create-type-badge import">{surface.badge}</span>
                        <h3>{surface.title}</h3>
                        <p>{surface.description}</p>
                        {surface.modes?.length ? (
                          <span className="script-create-template-tags compact" aria-label={`${surface.title} modes`}>
                            {surface.modes.map(mode => <span key={mode.id} className="tag-chip">{mode.label}</span>)}
                          </span>
                        ) : null}
                      </button>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </section>
        )}
      </div>
    </div>
  )
}
