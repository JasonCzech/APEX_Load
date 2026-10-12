// @vitest-environment jsdom

import { act } from 'react'
import { type ReactNode, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ScriptsAuthoringShell from './ScriptsAuthoringShell'
import { type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import { type AuthoringState, type ScriptsCreateOptionActions } from './useScriptsAuthoringState'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listAuthoringTemplates: vi.fn(),
  listAuthoringComponents: vi.fn(),
  listAuthoringCapabilities: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('./ScriptsAuthoringSurface', () => ({
  default: ({
    subView,
    draftScript,
    draftReturnTarget,
    authoringNotice,
    editingId,
    projectId,
    onSaved,
    onOpenLibrary,
    onOpenCreateOptions,
    onImported,
    onEditDraft,
    onReturnToDraftSource,
    importLauncher,
    importMode,
  }: {
    subView: string
    importLauncher?: string | null
    importMode?: string | null
    draftScript?: { name?: string } | null
    draftReturnTarget?: { label?: string } | null
    authoringNotice?: string | null
    editingId?: string | null
    projectId?: string
    onSaved?: (script: StoredScript) => void
    onOpenLibrary?: () => void
    onOpenCreateOptions?: () => void
    onImported?: (result: { scripts: StoredScript[]; scenario?: { name?: string } | null }) => void
    onEditDraft?: (script: { name: string; version: string; protocol: string; config: { base_url: string }; actions: unknown[] }) => void
    onReturnToDraftSource?: () => void
  }) => (
    <div data-testid="authoring-surface">
      <span>subview:{subView}</span>
      <span>launcher:{importLauncher || 'none'}</span>
      <span>mode:{importMode || 'none'}</span>
      <span>draft:{draftScript?.name || 'none'}</span>
      <span>notice:{authoringNotice || 'none'}</span>
      <span>return:{draftReturnTarget?.label || 'none'}</span>
      <span>editing:{editingId || 'none'}</span>
      <span>project:{projectId || 'none'}</span>
      <button onClick={() => onSaved?.({
        id: 'saved-script-1',
        name: 'Saved Script',
        protocol: 'http',
        version: '1.0.0',
        created_at: '2026-05-17T00:00:00Z',
      })}>surface-save</button>
      <button onClick={onOpenLibrary}>surface-library</button>
      <button onClick={onOpenCreateOptions}>surface-create-options</button>
      <button onClick={() => onImported?.({
        scripts: [{
          id: 'imported-script-1',
          name: 'Imported Script',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
        }],
      })}>surface-imported</button>
      <button onClick={() => onEditDraft?.({
        name: 'Surface Draft',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://example.com' },
        actions: [],
      })}
      >surface-edit-draft</button>
      <button onClick={onReturnToDraftSource}>surface-return</button>
    </div>
  ),
}))

function createActions(overrides: Partial<ScriptsCreateOptionActions> = {}): ScriptsCreateOptionActions {
  return {
    startApiScript: vi.fn(),
    startSoapScript: vi.fn(),
    startJavaHttpScript: vi.fn(),
    startWebSocketScript: vi.fn(),
    startGrpcScript: vi.fn(),
    startDatabaseScript: vi.fn(),
    startNetworkScript: vi.fn(),
    startWebScript: vi.fn(),
    startJmsScript: vi.fn(),
    startKafkaScript: vi.fn(),
    startTemplateScript: vi.fn(),
    composeComponentScript: vi.fn(),
    openRecorder: vi.fn(),
    openImportSurface: vi.fn(),
    openImportFiles: vi.fn(),
    openEnterpriseArchiveImport: vi.fn(),
    openJMeterImport: vi.fn(),
    openSampleImport: vi.fn(),
    openJavaHttpImport: vi.fn(),
    openDevWebImport: vi.fn(),
    openHARImport: vi.fn(),
    openSalesforceImport: vi.fn(),
    openBrowserRecordingImport: vi.fn(),
    openOpenAPIImport: vi.fn(),
    openRAMLImport: vi.fn(),
    openPostmanImport: vi.fn(),
    openWSDLImport: vi.fn(),
    openAsyncAPIImport: vi.fn(),
    ...overrides,
  }
}

function createState(subView: AuthoringState['subView'], overrides: Partial<AuthoringState> = {}): AuthoringState {
  return {
    subView,
    editingId: null,
    editingScript: null,
    draftScript: null,
    draftEditorFocus: null,
    draftReturnTarget: null,
    authoringNotice: null,
    importLauncher: null,
    importMode: null,
    ...overrides,
    draftInitiallyDirty: overrides.draftInitiallyDirty ?? false,
  }
}

const importSurfaces = [
  surface('openapi', 'OpenAPI / Swagger', 'OpenAPI', 'Contracts & Collections', 10, 'openapi', 'Generate HTTP scripts from OpenAPI 3.x or Swagger 2.0 contracts.'),
  surface('bruno', 'Bruno Collection', 'Bruno', 'Contracts & Collections', 15, 'bruno', 'Preserve and execute Bruno collections without translating source behavior.'),
  surface('postman', 'Postman Collection', 'Postman', 'Contracts & Collections', 20, 'postman', 'Convert Postman collections and environments into script drafts.'),
  surface('wsdl', 'WSDL / SOAP', 'WSDL', 'Contracts & Collections', 30, 'wsdl', 'Import SOAP services and operations from WSDL.'),
  surface('asyncapi', 'AsyncAPI', 'AsyncAPI', 'Contracts & Collections', 40, 'asyncapi', 'Model event-driven flows from AsyncAPI contracts.'),
  surface('raml', 'RAML / Anypoint', 'RAML', 'Contracts & Collections', 50, 'raml', 'Import RAML contracts from Anypoint-style API specs.'),
  surface('traffic_har', 'HAR Traffic', 'HAR', 'Traffic & Samples', 10, 'traffic_har', 'Convert captured browser/API traffic; auto-detects Salesforce and Agentforce.', [
    { id: 'auto_detect', label: 'Auto-detect', description: 'Detect Salesforce and generate native drafts when present.', default: true },
    { id: 'force_generic', label: 'Force Generic HAR', description: 'Return the raw deterministic HAR script.', default: false },
  ]),
  surface('recorder_export', 'Recorder Export / Playwright Codegen', 'Recorder', 'Traffic & Samples', 20, 'recorder_export', 'Import browser recorder exports and Playwright codegen captures.'),
  surface('sample', 'Request / Response Sample', 'Sample', 'Traffic & Samples', 30, 'sample', 'Build from raw HTTP, curl, HAR, REST JSON, SOAP-shaped samples, or Java over HTTP payloads.', [
    { id: 'standard_api', label: 'Standard API', description: 'Use the normal sample converter.', default: true },
    { id: 'java_http', label: 'Java over HTTP', description: 'Promote samples into java_http drafts.', default: false },
  ]),
  surface('enterprise_archive', 'Import Loadrunner Script Archive', 'Archive', 'Legacy & Tool Exports', 10, 'enterprise_archive', 'Convert LoadRunner enterprise script archives.'),
  surface('jmeter', 'Import JMeter', 'JMeter', 'Legacy & Tool Exports', 20, 'jmeter', 'Convert JMeter plans into deterministic script drafts.'),
  surface('devweb', 'DevWeb Source', 'DevWeb', 'Legacy & Tool Exports', 30, 'devweb', 'Convert DevWeb source projects.'),
  surface('k6', 'k6 Script', 'k6', 'Legacy & Tool Exports', 40, 'k6', 'Convert k6 JavaScript scripts into Apex Load drafts.'),
  surface('eventhubs_capture', 'Event Hubs Capture', 'Event Hubs', 'Messaging & Cloud Artifacts', 10, 'eventhubs_capture', 'Import Event Hubs capture metadata and payload examples.'),
  surface('broker_config', 'Broker Config', 'Broker', 'Messaging & Cloud Artifacts', 20, 'broker_config', 'Import messaging broker configuration for JMS/Kafka style drafts.'),
  surface('oracle_ebusiness_review', 'Oracle E-Business / Siebel Review', 'Review', 'Enterprise App Reviews', 10, 'oracle_ebusiness_review', 'Review Oracle E-Business and Siebel assets before script authoring.'),
]

function surface(
  id: string,
  title: string,
  badge: string,
  category: string,
  order: number,
  launcher: string,
  description: string,
  modes: Array<{ id: string; label: string; description: string; default: boolean }> = [],
) {
  return {
    id,
    title,
    description,
    badge,
    category,
    order,
    launcher,
    visible: true,
    modes,
  }
}

describe('ScriptsAuthoringShell', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.listAuthoringTemplates.mockResolvedValue({ templates: [], count: 0 })
    apiMock.listAuthoringComponents.mockResolvedValue({ components: [], count: 0 })
    apiMock.listAuthoringCapabilities.mockResolvedValue({
      sources: [],
      source_map: {},
      import_surfaces: importSurfaces,
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('renders library children when the authoring state is list', async () => {
    await act(async () => {
      root.render(renderShell({
        authoringState: createState('list'),
        children: <div data-testid="library">library-view</div>,
      }))
    })

    expect(container.querySelector('[data-testid="library"]')).not.toBeNull()
    expect(container.textContent).toContain('library-view')
  })

  it('renders the shared assets view for every role, never an authoring surface', async () => {
    for (const canManageScripts of [true, false]) {
      await act(async () => {
        root.render(renderShell({ authoringState: createState('assets'), canManageScripts, children: <div data-testid="library">library-view</div> }))
      })
      expect(container.querySelector('[data-testid="assets"]'), String(canManageScripts)).not.toBeNull()
      expect(container.querySelector('[data-testid="library"]')).toBeNull()
    }
  })

  it('defaults an empty library to the start-new workflow', async () => {
    await act(async () => {
      root.render(renderShell({
        authoringState: createState('create-options'),
        scripts: [],
      }))
    })
    await flushPromises()

    expect(container.textContent).toContain('New draft authoring')
    expect(container.textContent).not.toContain('Copy an Existing Script')
    expect(Array.from(container.querySelectorAll('.script-workflow-card')).find(button => button.textContent?.includes('Create'))?.classList.contains('active')).toBe(true)
  })

  it('renders create options and delegates launcher actions', async () => {
    const actions = createActions()
    const onOpenLibrary = vi.fn()
    const onStartExistingScript = vi.fn()

    await act(async () => {
      root.render(renderShell({
        authoringState: createState('create-options'),
        createOptionActions: actions,
        onOpenLibrary,
        onStartExistingScript,
        scripts: [{
          id: 'script-1',
          name: 'Checkout Model',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
          scenario_count: 2,
        }],
      }))
    })
    await flushPromises()

    expect(container.textContent).toContain('Create script')
    expect(container.textContent).toContain('Start New Script')
    await clickButton('Use existing')
    expect(container.textContent).toContain('Copy an Existing Script')
    expect(container.textContent).toContain('Checkout Model')
    expect(container.textContent).toContain('Create')
    expect(container.textContent).not.toContain('Use Template')
    expect(container.textContent).not.toContain('Build Manually')
    expect(container.textContent).toContain('Import')
    expect(container.textContent).not.toContain('Start From Source')
    expect(container.textContent).not.toContain('Import Existing')
    expect(container.textContent).toContain('Record')
    expect(container.textContent).not.toContain('Browser Recorder')

    await clickButton('Checkout Model')
    expect(onStartExistingScript).toHaveBeenCalledWith(expect.objectContaining({ id: 'script-1' }))

    await clickButton('Record')
    expect(actions.openRecorder).toHaveBeenCalled()
    expect(container.textContent).not.toContain('Browser Recorder')

    await clickButton('Import')
    await flushPromises()
    expect(container.textContent).toContain('Contracts & Collections')
    expect(container.textContent).toContain('Traffic & Samples')
    expect(container.textContent).toContain('Legacy & Tool Exports')
    expect(container.textContent).toContain('Messaging & Cloud Artifacts')
    expect(container.textContent).toContain('Enterprise App Reviews')
    expect(container.textContent).toContain('OpenAPI / Swagger')
    expect(container.textContent).toContain('Postman Collection')
    expect(container.textContent).toContain('WSDL / SOAP')
    expect(container.textContent).toContain('AsyncAPI')
    expect(container.textContent).toContain('RAML / Anypoint')
    expect(container.textContent).not.toContain('Salesforce Family')
    expect(container.textContent).not.toContain('Salesforce HAR')
    expect(container.textContent).not.toContain('Agentforce Template')
    expect(container.textContent).toContain('HAR Traffic')
    expect(container.textContent).toContain('Auto-detect')
    expect(container.textContent).toContain('Force Generic HAR')
    expect(container.textContent).toContain('Request / Response Sample')
    expect(container.textContent).toContain('Java over HTTP')
    expect(container.textContent).not.toContain('Import Java over HTTP')
    expect(container.textContent).toContain('Import Loadrunner Script Archive')
    expect(container.textContent).toContain('Import JMeter')
    expect(container.textContent).toContain('DevWeb Source')
    expect(container.textContent).toContain('k6 Script')
    expect(container.textContent).toContain('Event Hubs Capture')
    expect(container.textContent).toContain('Broker Config')
    expect(container.textContent).toContain('Oracle E-Business / Siebel Review')
    expect(container.textContent).not.toContain('Import SOAP Assets')
    expect(container.querySelectorAll('.script-create-import-groups .script-create-option')).toHaveLength(16)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('OpenAPI / Swagger'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('Postman Collection'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('WSDL / SOAP'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('RAML / Anypoint'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('HAR Traffic'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('DevWeb Source'))).toHaveLength(1)

    await clickButton('OpenAPI / Swagger')
    expect(actions.openImportSurface).toHaveBeenCalledWith('openapi', null)

    await clickButton('Postman Collection')
    expect(actions.openImportSurface).toHaveBeenCalledWith('postman', null)

    await clickButton('HAR Traffic')
    expect(actions.openImportSurface).toHaveBeenCalledWith('traffic_har', 'auto_detect')

    await clickButton('RAML / Anypoint')
    expect(actions.openImportSurface).toHaveBeenCalledWith('raml', null)

    await clickButton('Import Loadrunner Script Archive')
    expect(actions.openImportSurface).toHaveBeenCalledWith('enterprise_archive', null)

    await clickButton('Oracle E-Business / Siebel Review')
    expect(actions.openImportSurface).toHaveBeenCalledWith('oracle_ebusiness_review', null)

    await clickButton('Script library')
    expect(onOpenLibrary).toHaveBeenCalled()
  })

  it('renders model-from-existing scripts as a sortable and filterable table', async () => {
    const actions = createActions()

    await act(async () => {
      root.render(renderShell({
        authoringState: createState('create-options'),
        createOptionActions: actions,
        onStartExistingScript: vi.fn(),
        scripts: [
          {
            id: 'script-checkout',
            name: 'Checkout Model',
            protocol: 'http',
            version: '1.0.0',
            created_at: '2026-05-17T00:00:00Z',
            scenario_count: 2,
            associated_releases: [{ id: 'release-1', name: 'PI30', application_id: 'app-1' }],
            authoring_health: { score: 96, status: 'ready', inputs: [] },
          },
          {
            id: 'script-billing',
            name: 'Billing Flow',
            protocol: 'soap',
            version: '2.1.0',
            created_at: '2026-05-18T00:00:00Z',
            scenario_count: 5,
            associated_releases: [{ id: 'release-2', name: 'PI31', application_id: 'app-1' }],
            authoring_health: { score: 62, status: 'needs_review', inputs: [] },
          },
          {
            id: 'script-browser',
            name: 'Browser Journey',
            protocol: 'web',
            version: '1.0.0',
            created_at: '2026-05-19T00:00:00Z',
            scenario_count: 0,
            authoring_health: { score: 40, status: 'blocked', inputs: [] },
          },
        ],
      }))
    })

    await clickButton('Use existing')
    expect(container.querySelector('.script-create-existing-table')).not.toBeNull()
    expect(container.textContent).toContain('3 of 3 scripts')
    expect(container.textContent).toContain('PI30')
    expect(container.textContent).toContain('PI31')
    const headers = Array.from(container.querySelectorAll('.script-create-existing-table th')).map(header => header.textContent || '')
    // Every script here is local, so there is no Source column.
    expect(headers.some(header => header.includes('Source'))).toBe(false)
    expect(headers.some(header => header.includes('Script'))).toBe(true)
    expect(headers.some(header => header.includes('Health'))).toBe(true)
    expect(headers.some(header => header.includes('Usage'))).toBe(true)
    expect(headers.some(header => header.includes('Run releases'))).toBe(true)
    expect(headers.some(header => header.includes('Created'))).toBe(true)
    expect(headers.some(header => header.includes('Last run'))).toBe(true)
    expect(headers.some(header => header.includes('Actions'))).toBe(true)
    expect(container.querySelector('.script-create-existing-table tbody tr td:first-child')?.classList.contains('cell-name')).toBe(true)

    const searchInput = container.querySelector('input[aria-label="Search existing scripts"]') as HTMLInputElement | null
    expect(searchInput).toBeTruthy()
    if (!searchInput) return

    await act(async () => {
      setInputValue(searchInput, 'billing')
    })

    expect(container.textContent).toContain('1 of 3 scripts')
    expect(container.textContent).toContain('Billing Flow')
    expect(container.textContent).not.toContain('Checkout Model')

    await clickButton('Reset')

    const protocolSelect = container.querySelector('select[aria-label="Filter existing scripts by protocol"]') as HTMLSelectElement | null
    expect(protocolSelect).toBeTruthy()
    if (!protocolSelect) return

    await act(async () => {
      setSelectValue(protocolSelect, 'web')
    })

    expect(container.textContent).toContain('1 of 3 scripts')
    expect(container.textContent).toContain('Browser Journey')
    expect(container.textContent).not.toContain('Billing Flow')

    await act(async () => {
      setSelectValue(protocolSelect, 'all')
    })

    const usageSort = Array.from(container.querySelectorAll('th button')).find(button => button.textContent?.includes('Usage'))
    expect(usageSort).toBeTruthy()
    if (!usageSort) return

    await act(async () => {
      usageSort.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const firstBodyRow = container.querySelector('.script-create-existing-table tbody tr')
    expect(firstBodyRow?.textContent).toContain('Billing Flow')
  })

  it('returns from source-driven card pages to the source-driven authoring workflow', async () => {
    function Harness() {
      const [authoringState, setAuthoringState] = useState<AuthoringState>(createState('create-options'))
      return renderShell({
        authoringState,
        createOptionActions: createActions({
          openImportSurface: (launcher, mode) => setAuthoringState(createState('protocol-import', {
            importLauncher: launcher,
            importMode: mode || null,
          })),
        }),
        onOpenCreateOptions: () => setAuthoringState(createState('create-options')),
      })
    }

    await act(async () => {
      root.render(<Harness />)
    })
    await flushPromises()

    await clickButton('Import')
    expect(container.textContent).toContain('Contracts & Collections')
    expect(container.textContent).not.toContain('Copy an Existing Script')

    await clickButton('OpenAPI / Swagger')
    expect(container.textContent).toContain('subview:protocol-import')
    expect(container.textContent).toContain('launcher:openapi')

    await clickButton('surface-create-options')

    expect(container.textContent).toContain('Contracts & Collections')
    expect(container.textContent).toContain('OpenAPI / Swagger')
    expect(container.textContent).not.toContain('Copy an Existing Script')
  })

  it('renders backend starter templates and delegates template drafts', async () => {
    const actions = createActions()
    apiMock.listAuthoringTemplates.mockResolvedValueOnce({
      templates: [
        {
          id: 'template-http-smoke',
          name: 'HTTP Smoke',
          protocol: 'http',
          use_case: 'ci_smoke',
          description: 'Exercise one critical API path before release promotion.',
          tags: ['api', 'ci', 'smoke'],
          script: {
            name: 'HTTP Smoke',
            version: '1.0.0',
            protocol: 'http',
            config: { base_url: 'https://api.example.com' },
            actions: [],
          },
          recommended_scenario: {
            duration: '2m',
            vusers: 3,
          },
        },
        {
          id: 'template-agentforce-sse',
          name: 'Agentforce SSE',
          protocol: 'http',
          use_case: 'agent_streaming',
          description: 'Exercise an Agentforce session over OAuth2 and SSE.',
          tags: ['agentforce', 'salesforce', 'sse', 'oauth2'],
          script: {
            name: 'Agentforce SSE',
            version: '1.0.0',
            protocol: 'http',
            config: { base_url: '{{env.SF_API_HOST}}' },
            init: [{ name: 'Get Token', type: 'http_oauth2_token', target: '', params: { client_secret: '{{secret.SALESFORCE_CLIENT_SECRET}}' } }],
            actions: [{ name: 'Send Prompt', type: 'request', method: 'POST', target: '/agents/{{env.AGENT_ID}}/messages', body: '{{env.AGENT_PROMPT}}' }],
          },
          recommended_scenario: {
            duration: '5m',
            vusers: 1,
          },
        },
      ],
      count: 2,
    })
    apiMock.listAuthoringComponents.mockResolvedValueOnce({
      components: [{
        id: 'component-oauth',
        kind: 'auth_flow',
        name: 'OAuth Bearer Token Flow',
        protocols: ['http'],
        summary: 'Token acquisition action with reusable Authorization header wiring.',
        tags: ['auth', 'oauth'],
      }],
      count: 1,
    })

    await act(async () => {
      root.render(renderShell({
        authoringState: createState('create-options'),
        createOptionActions: actions,
      }))
    })

    await flushPromises()
    await clickButton('Create')

    expect(apiMock.listAuthoringTemplates).toHaveBeenCalledTimes(1)
    expect(apiMock.listAuthoringComponents).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Starter Templates')
    expect(container.textContent).toContain('Blank & Assisted Editors')
    expect(container.textContent).toContain('Reusable Blocks')
    expect(container.querySelectorAll('.script-create-category-tabs [role="tab"]')).toHaveLength(3)
    expect(container.querySelectorAll('.script-create-table-list')).toHaveLength(1)
    expect(container.querySelector('.script-create-choice-groups .script-create-option')).toBeNull()
    await clickButton('Starter Templates')
    expect(container.textContent).toContain('HTTP Smoke')
    expect(container.textContent).toMatch(/ci smoke\s*api\s*ci\s*smoke/i)
    expect(container.textContent).toMatch(/3 VUsers\s*\/\s*2m/i)
    expect(container.textContent).toContain('Agentforce SSE')
    expect(container.textContent).toContain('Salesforce Family')
    expect(container.textContent).toContain('Required values: env.AGENT_ID, env.AGENT_PROMPT, env.SF_API_HOST, secret.SALESFORCE_CLIENT_SECRET')

    await clickButton('Reusable Blocks')
    expect(container.querySelectorAll('.script-create-table-list')).toHaveLength(1)
    expect(container.textContent).toContain('OAuth Bearer Token Flow')

    await clickButton('Starter Templates')

    await clickButton('HTTP Smoke')
    expect(actions.startTemplateScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'HTTP Smoke',
      protocol: 'http',
    }))
  })

  it('renders authoring subviews through the shared authoring surface', async () => {
    const onSaved = vi.fn()
    const onOpenLibrary = vi.fn()
    const onOpenCreateOptions = vi.fn()
    const onImported = vi.fn()
    const onEditDraft = vi.fn()
    const onReturnToDraftSource = vi.fn()

    await act(async () => {
      root.render(renderShell({
        authoringState: createState('create', {
          draftScript: {
            name: 'Draft Script',
            version: '1.0.0',
            protocol: 'http',
            config: { base_url: 'https://example.com' },
            actions: [],
          },
          authoringNotice: 'Saved 2 scripts. Reviewing the first saved script.',
        }),
        onSaved,
        onOpenLibrary,
        onOpenCreateOptions,
        onImported,
        onEditDraft,
        onReturnToDraftSource,
      }))
    })

    expect(container.querySelector('[data-testid="authoring-surface"]')).not.toBeNull()
    expect(container.textContent).toContain('subview:create')
    expect(container.textContent).toContain('draft:Draft Script')
    expect(container.textContent).toContain('notice:Saved 2 scripts. Reviewing the first saved script.')
    expect(container.textContent).toContain('project:project-1')

    await clickButton('surface-save')
    await clickButton('surface-library')
    await clickButton('surface-create-options')
    await clickButton('surface-imported')
    await clickButton('surface-edit-draft')
    await clickButton('surface-return')

    expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: 'saved-script-1', name: 'Saved Script' }))
    expect(onOpenLibrary).toHaveBeenCalled()
    expect(onOpenCreateOptions).toHaveBeenCalled()
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'imported-script-1', name: 'Imported Script' })],
    }))
    expect(onEditDraft).toHaveBeenCalledWith(expect.objectContaining({ name: 'Surface Draft' }))
    expect(onReturnToDraftSource).toHaveBeenCalled()
  })

  function renderShell({
    authoringState,
    createOptionActions = createActions(),
    scripts = [],
    onSaved = vi.fn(),
    onOpenLibrary = vi.fn(),
    onOpenCreateOptions = vi.fn(),
    onImported = vi.fn(),
    onEditDraft = vi.fn(),
    onReturnToDraftSource = vi.fn(),
    onStartExistingScript = vi.fn(),
    canManageScripts = true,
    children = <div />,
  }: {
    authoringState: AuthoringState
    createOptionActions?: ScriptsCreateOptionActions
    scripts?: StoredScript[]
    onSaved?: (script: StoredScript) => void
    onOpenLibrary?: () => void
    onOpenCreateOptions?: () => void
    onImported?: (result: { scripts: StoredScript[]; scenario?: { name?: string } | null }) => void
    onEditDraft?: (script: ScriptDefinition) => void
    onReturnToDraftSource?: () => void
    onStartExistingScript?: (script: StoredScript) => void
    canManageScripts?: boolean
    children?: ReactNode
  }) {
    return (
      <ScriptsAuthoringShell
        authoringState={authoringState}
        projectId="project-1"
        activeProject={{ id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }}
        scripts={scripts}
        createOptionActions={createOptionActions}
        onSaved={onSaved}
        onOpenLibrary={onOpenLibrary}
        onOpenCreateOptions={onOpenCreateOptions}
        onOpenAssets={vi.fn()}
        assetsView={<div data-testid="assets">assets-view</div>}
        onImported={onImported}
        onEditDraft={onEditDraft}
        onReturnToDraftSource={onReturnToDraftSource}
        onStartExistingScript={onStartExistingScript}
        canManageScripts={canManageScripts}
      >
        {children}
      </ScriptsAuthoringShell>
    )
  }

  async function clickButton(label: string) {
    const button = Array.from(container.querySelectorAll('button')).find(element => (['Create','Import','Record','Use existing'].includes(label) ? element.querySelector('strong')?.textContent === label : element.textContent?.includes(label)))
    expect(button).toBeTruthy()
    if (!button) {
      return
    }
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function setInputValue(input: HTMLInputElement, value: string) {
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    setter?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }

  function setSelectValue(select: HTMLSelectElement, value: string) {
    const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set
    setter?.call(select, value)
    select.dispatchEvent(new Event('change', { bubbles: true }))
  }

  async function flushPromises() {
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })
  }
})
