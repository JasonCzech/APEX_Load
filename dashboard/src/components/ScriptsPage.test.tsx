// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import ScriptsPage from './ScriptsPage'
import {
  beginScriptSecretStatusRequest,
  isLatestScriptSecretStatusRequest,
} from './scriptSecretStatusRequest'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  // The authoring surface now renders a destination-folder control.
  listFolders: vi.fn(() => Promise.resolve({ folders: [], count: 0 })),
  listScripts: vi.fn(),
  getScript: vi.fn(),
  getScriptSecretStatus: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  getProjectSecretUsage: vi.fn(),
  listProjectSecrets: vi.fn(),
  createProjectSecrets: vi.fn(),
  rebindScriptSecretReference: vi.fn(),
  updateScript: vi.fn(),
  cloneScriptDraft: vi.fn(),
  applySecretBindings: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
  deleteScript: vi.fn(),
  listTests: vi.fn(),
  listAuthoringTemplates: vi.fn(),
  listAuthoringComponents: vi.fn(),
  listAuthoringCapabilities: vi.fn(),
  listAuthoringSessions: vi.fn(),
  approveAuthoringSession: vi.fn(),
  deleteAuthoringSession: vi.fn(),
  previewAuthoringCorrelation: vi.fn(),
  getAuthEffectiveAccess: vi.fn(),
  getSystemSettings: vi.fn(),
}

const libraryApiMock = {
  listRemotes: vi.fn(),
  localCatalog: vi.fn(),
  remoteCatalog: vi.fn(),
  pullRemoteItem: vi.fn(),
  pushLocalItem: vi.fn(),
  compareItems: vi.fn(),
}

const projectState = {
  activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
}
let publishLateEnterpriseSave: (() => void) | null = null

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/api/useLibraryApi', () => ({
  useLibraryApi: () => libraryApiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => projectState,
}))

vi.mock('./ScriptEditor', () => ({
  default: ({ initialScript, initiallyDirty, scriptId, onSave, onClose }: { initialScript?: { name?: string }, initiallyDirty?: boolean, scriptId?: string | null, onSave?: (script: { id: string; name: string }) => void, onClose?: () => void }) => (
    <div data-testid="script-editor">
      <span>editor:{initialScript?.name || 'blank'}</span>
      <span>script-id:{scriptId || 'none'}</span>
      <span>initially-dirty:{initiallyDirty ? 'yes' : 'no'}</span>
      <button onClick={() => onSave?.({ id: 'saved-script-1', name: initialScript?.name || 'Saved Script' })}>mock-save</button>
      <button onClick={onClose}>mock-close</button>
    </div>
  ),
}))

vi.mock('./EnterpriseArchiveImport', () => ({
  default: ({ onImported, onEdit, onClose }: { onImported?: (result: { scripts: Array<{ id: string; name: string; protocol: string; script?: { name: string; version: string; protocol: string; config: { base_url: string }; actions: unknown[] } }>; scenario?: { name?: string } | null; stayInImportReview?: boolean }) => void, onEdit?: (script: { name: string; version: string; protocol: string; config: { base_url: string }; actions: unknown[] }, focus?: { targetSection?: string }) => void, onClose?: () => void }) => {
    publishLateEnterpriseSave = () => onImported?.({
      scripts: [{
        id: 'imported-script-late',
        name: 'Late Enterprise Save',
        protocol: 'http',
      }],
      stayInImportReview: true,
    })
    return <div data-testid="enterprise-archive-import">
      <input aria-label="mock-enterprise-archive-state" defaultValue="converted-review" />
      <button onClick={() => onImported?.({
        scripts: [{
          id: 'imported-script-partial',
          name: 'Saved Enterprise Lane',
          protocol: 'http',
        }],
        stayInImportReview: true,
      })}>mock-enterprise-archive-refresh-only-save</button>
      <button onClick={() => onImported?.({
        scripts: [{
          id: 'imported-script-1',
          name: 'Imported Enterprise Script Archive',
          protocol: 'http',
          script: {
            name: 'Imported Enterprise Script Archive',
            version: '1.0.0',
            protocol: 'http',
            config: { base_url: 'https://import.example.com' },
            actions: [],
          },
        }],
      })}>mock-enterprise-archive-save</button>
      <button onClick={() => onEdit?.({
        name: 'Converted Enterprise Script Archive Draft',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://import.example.com' },
        actions: [],
      }, { targetSection: 'runtime' })}>mock-enterprise-archive-review-option</button>
      <button onClick={onClose}>mock-enterprise-archive-close</button>
    </div>
  },
}))

vi.mock('./JMeterImport', () => ({
  default: ({ onEdit, onImported, onClose }: { onEdit?: (script: { name: string, version: string, protocol: string, config: { base_url: string }, actions: unknown[] }) => void, onImported?: (result: { scripts: Array<{ id: string; name: string; protocol: string; script?: { name: string; version: string; protocol: string; config: { base_url: string }; actions: unknown[] } }>; scenario?: { name?: string } | null }) => void, onClose?: () => void }) => (
    <div data-testid="jmeter-import">
      <button onClick={() => onEdit?.({
        name: 'JMeter Draft',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://jmeter.example.com' },
        actions: [],
      })}
      >mock-jmeter-edit</button>
      <button onClick={() => onImported?.({
        scripts: [
          {
            id: 'jmeter-script-1',
            name: 'Auth Users',
            protocol: 'http',
            script: {
              name: 'Auth Users',
              version: '1.0.0',
              protocol: 'http',
              config: { base_url: 'https://jmeter.example.com' },
              actions: [],
            },
          },
          {
            id: 'jmeter-script-2',
            name: 'Checkout Users',
            protocol: 'http',
          },
        ],
        scenario: { name: 'Imported JMeter Scenario' },
      })}
      >mock-jmeter-save</button>
      <button onClick={onClose}>mock-jmeter-close</button>
    </div>
  ),
}))

vi.mock('./RecordingStudio', () => ({
  default: () => <div data-testid="recording-studio">recording-studio</div>,
}))

vi.mock('./SampleScriptImport', () => ({
  default: ({ onEdit, preset }: { onEdit?: (script: { name: string, version: string, protocol: string, config: { base_url: string }, actions: unknown[] }) => void, preset?: string }) => (
    <div data-testid="sample-import">
      <span>sample-import:{preset || 'default'}</span>
      <button onClick={() => onEdit?.({
        name: 'Sample Draft',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://api.example.com' },
        actions: [],
      })}
      >mock-sample-edit</button>
    </div>
  ),
}))

vi.mock('./ProtocolAssetImport', () => ({
  default: ({ kind, onEdit }: { kind: string, onEdit?: (script: { name: string, version: string, protocol: string, config: { base_url: string }, actions: unknown[] }) => void }) => (
    <div data-testid={`protocol-import-${kind}`}>
      <span>protocol-import:{kind}</span>
      <button onClick={() => onEdit?.({
        name: `${kind.toUpperCase()} Draft`,
        version: '1.0.0',
        protocol: kind === 'wsdl' ? 'soap' : 'http',
        config: { base_url: `https://${kind}.example.com` },
        actions: [],
      })}
      >mock-{kind}-edit</button>
    </div>
  ),
}))

vi.mock('./OracleEBusinessReviewImport', () => ({
  default: () => <div data-testid="oracle-ebusiness-review">oracle-review</div>,
}))

function TopbarContributionHarness() {
  const contribution = useActiveTopbarContribution()
  return (
    <div data-testid="topbar-contribution">
      {contribution?.titleNode || contribution?.title}
      {contribution?.controls}
    </div>
  )
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

describe('ScriptsPage', () => {
  let container: HTMLDivElement
  let root: Root

  it('orders automatic and retry secret checks through one per-script token', async () => {
    const tokens = new Map<string, number>()
    const automatic = deferred<'stale automatic'>()
    const retry = deferred<'ready retry'>()
    let visibleStatus = ''

    const automaticToken = beginScriptSecretStatusRequest(tokens, 'script-1')
    const automaticApply = automatic.promise.then(status => {
      if (isLatestScriptSecretStatusRequest(tokens, 'script-1', automaticToken)) visibleStatus = status
    })
    const retryToken = beginScriptSecretStatusRequest(tokens, 'script-1')
    const retryApply = retry.promise.then(status => {
      if (isLatestScriptSecretStatusRequest(tokens, 'script-1', retryToken)) visibleStatus = status
    })

    retry.resolve('ready retry')
    await retryApply
    automatic.resolve('stale automatic')
    await automaticApply

    expect(visibleStatus).toBe('ready retry')
  })

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    projectState.activeProject = { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }

    apiMock.listScripts.mockResolvedValue({
      scripts: [
        {
          id: 'script-1',
          name: 'Existing Script',
          protocol: 'http',
          version: '1.0.0',
          project_id: 'project-1',
          created_at: '2026-05-12T20:00:00Z',
          scenario_count: 0,
          associated_releases: [],
          scenario_run_releases: [],
          last_run_at: '',
          authoring_health: {
            score: 72,
            status: 'needs_review',
            inputs: [
              { key: 'warnings', status: 'warning', count: 1, summary: 'Validation warnings need review before promotion.' },
            ],
          },
        },
      ],
    })
    apiMock.getScript.mockResolvedValue({ script: null })
    apiMock.getScriptSecretStatus.mockResolvedValue({
      script_id: 'script-1',
      project_id: 'project-1',
      required: [],
      configured: [],
      missing: [],
      secrets: [],
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
    })
    apiMock.cloneScriptDraft.mockResolvedValue({
      script: {
        name: 'Existing Script (Copy)',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://existing.example.com' },
        actions: [],
      },
      secret_binding_requirements: [],
    })
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: true,
      },
      secrets: [],
    })
    apiMock.getProjectSecretUsage.mockResolvedValue({
      project_id: 'project-1',
      name: 'LOGIN_TOKEN',
      usage_revision: 'usage-revision-1',
      consumer_count: 1,
      reference_count: 1,
      consumers: [{ script_id: 'script-1', script_name: 'Existing Script', reference_count: 1 }],
      count: 1,
      limit: 50,
      offset: 0,
    })
    apiMock.listProjectSecrets.mockResolvedValue({
      project_id: 'project-1',
      provider: 'encrypted_database',
      writable: true,
      secrets: [],
      count: 0,
    })
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.rebindScriptSecretReference.mockResolvedValue({ script: null, changed_locations: [], changed_count: 0 })
    apiMock.updateScript.mockResolvedValue(scriptRecord('script-1', 'Existing Script', 'project-1'))
    apiMock.applySecretBindings.mockResolvedValue({ script: null, secret_binding_requirements: [] })
    apiMock.migrateImportSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.deleteScript.mockResolvedValue({})
    apiMock.listTests.mockResolvedValue({ tests: [] })
    apiMock.listAuthoringTemplates.mockResolvedValue({ templates: [], count: 0 })
    apiMock.listAuthoringComponents.mockResolvedValue({ components: [], count: 0 })
    apiMock.listAuthoringCapabilities.mockResolvedValue({
      sources: [],
      source_map: {},
      import_surfaces: importSurfaces,
    })
    apiMock.listAuthoringSessions.mockResolvedValue({ sessions: [], count: 0 })
    apiMock.approveAuthoringSession.mockResolvedValue({
      session: { id: 'session-1', name: 'Migrated checkout review', status: 'approved' },
      script: { id: 'approved-script-1', name: 'Approved Script', protocol: 'http', version: '1.0.0', created_at: '2026-05-18T00:00:00Z' },
      authoring_health: { score: 100, status: 'ready', inputs: [] },
    })
    apiMock.deleteAuthoringSession.mockResolvedValue({ deleted: 'session-1' })
    apiMock.previewAuthoringCorrelation.mockResolvedValue({
      session_id: 'session-1',
      applied: false,
      extractor: { name: 'correlated_value', source: 'body', type: 'jsonpath', expression: '$.token' },
      changes: [{ kind: 'extractor_added', path: 'actions[0].extractors' }],
      authoring_health: { score: 100, status: 'ready', inputs: [] },
    })
    libraryApiMock.listRemotes.mockResolvedValue({ remotes: [], count: 0 })
    libraryApiMock.localCatalog.mockResolvedValue({ items: [], count: 0 })
    libraryApiMock.remoteCatalog.mockResolvedValue({ catalog: { items: [], count: 0 } })
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: false,
      authenticated: false,
      resources: [
        {
          id: 'scripts_and_imports',
          name: 'Scripts, Imports, and Replay',
          description: '',
          operations: [{ id: 'manage_scripts', label: 'Manage scripts', allowed: true }],
        },
      ],
    })
    apiMock.getSystemSettings.mockResolvedValue({
      script_imports: { allow_source_secret_migration: false },
    })
    window.localStorage.removeItem('apexload.settings.focusSection')
    window.localStorage.removeItem('apexload.settings.projectSubsection')
    window.localStorage.removeItem('apexload.settings.projectId')
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    publishLateEnterpriseSave = null
    window.localStorage.removeItem('apexload.settings.focusSection')
    window.localStorage.removeItem('apexload.settings.projectSubsection')
    window.localStorage.removeItem('apexload.settings.projectId')
  })

  it('opens the script-scoped bindings dialog from library health without navigating away', async () => {
    apiMock.listScripts.mockResolvedValueOnce({
      scripts: [{
        ...scriptRecord('script-1', 'Existing Script', 'project-1'),
        script: {
          name: 'Existing Script', version: '1.0.0', protocol: 'http', config: { base_url: '' },
          actions: [{ name: 'Login', type: 'GET', target: '/', headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' } }],
        },
      }],
      count: 1,
    })
    apiMock.getProjectSecretStatuses.mockResolvedValueOnce({
      project_id: 'project-1',
      secrets: [{ name: 'LOGIN_TOKEN', configured: false }],
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
    })
    const onNavigate = vi.fn()

    await act(async () => {
      root.render(<ScriptsPage onNavigate={onNavigate} />)
    })
    await flushPromises()
    await flushPromises()

    const configureButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === 'Configure secrets')
    expect(configureButton).not.toBeNull()
    await act(async () => {
      configureButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const dialog = document.body.querySelector<HTMLElement>('[role="dialog"][aria-modal="true"]')
    expect(dialog?.textContent).toContain('Secret Bindings · Existing Script')
    expect(dialog?.textContent).toContain('LOGIN_TOKEN')
    expect(onNavigate).not.toHaveBeenCalled()
    expect(window.localStorage.getItem('apexload.settings.focusSection')).toBeNull()
    expect(window.localStorage.getItem('apexload.settings.projectSubsection')).toBeNull()
    expect(window.localStorage.getItem('apexload.settings.projectId')).toBeNull()
  })

  it('loads script secret readiness through one project-scoped batch', async () => {
    const scripts = Array.from({ length: 20 }, (_, index) => ({
      ...scriptRecord(`script-${index + 1}`, `Script ${index + 1}`, 'project-1'),
      script: {
        name: `Script ${index + 1}`,
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: '' },
        actions: [{ name: 'Call', type: 'http', target: '/', headers: { Authorization: `Bearer {{secret.TOKEN_${index + 1}}}` } }],
      },
    }))
    apiMock.listScripts.mockResolvedValueOnce({ scripts, count: scripts.length })
    apiMock.getProjectSecretStatuses.mockResolvedValueOnce({
      project_id: 'project-1',
      secrets: Array.from({ length: 20 }, (_, index) => ({ name: `TOKEN_${index + 1}`, configured: index === 0 })),
      capabilities: { provider: 'encrypted_database', writable: true },
    })

    await act(async () => {
      root.render(<ScriptsPage />)
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledTimes(1)
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledWith(
      'project-1',
      Array.from({ length: 20 }, (_, index) => `TOKEN_${index + 1}`).sort(),
    )
    const readyRow = Array.from(container.querySelectorAll('tr')).find(row => row.textContent?.includes('Script 1'))
    const stillPendingRow = Array.from(container.querySelectorAll('tr')).find(row => row.textContent?.includes('Script 2'))
    expect(readyRow?.textContent).not.toContain('Checking secrets')
    expect(stillPendingRow?.textContent).toContain('1 secret missing')
  })

  it('preserves cached secret readiness when an unchanged library refresh replaces the scripts array', async () => {
    const secretScript = {
      ...scriptRecord('script-cache', 'Cached Secret Script', 'project-1'),
      script: {
        name: 'Cached Secret Script',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: '' },
        actions: [{ name: 'Call', type: 'http', target: '/', headers: { Authorization: 'Bearer {{secret.CACHED_TOKEN}}' } }],
      },
    }
    apiMock.listScripts.mockResolvedValue({ scripts: [secretScript], count: 1 })
    apiMock.getProjectSecretStatuses.mockResolvedValueOnce({
      project_id: 'project-1',
      secrets: [{ name: 'CACHED_TOKEN', configured: true }],
      capabilities: { provider: 'encrypted_database', writable: true },
    })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)

    await act(async () => root.render(<ScriptsPage />))
    await flushPromises()
    await flushPromises()
    await clickButton('Delete')
    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledTimes(2)
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Checking secrets')
    confirmSpy.mockRestore()
  })

  it('retries only the failed script secret check', async () => {
    const scripts = ['ALPHA_TOKEN', 'BETA_TOKEN'].map((secretName, index) => ({
      ...scriptRecord(`script-${index + 1}`, index === 0 ? 'Alpha Script' : 'Beta Script', 'project-1'),
      script: {
        name: index === 0 ? 'Alpha Script' : 'Beta Script',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: '' },
        actions: [{ name: 'Call', type: 'http', target: '/', headers: { Authorization: `Bearer {{secret.${secretName}}}` } }],
      },
    }))
    apiMock.listScripts.mockResolvedValueOnce({ scripts, count: scripts.length })
    apiMock.getProjectSecretStatuses
      .mockRejectedValueOnce(new Error('status unavailable'))
      .mockResolvedValueOnce({
        project_id: 'project-1',
        secrets: [{ name: 'ALPHA_TOKEN', configured: true }],
        capabilities: { provider: 'encrypted_database', writable: true },
      })

    await act(async () => root.render(<ScriptsPage />))
    await flushPromises()
    await flushPromises()

    const alphaRow = Array.from(container.querySelectorAll('tr')).find(row => row.textContent?.includes('Alpha Script'))
    const retry = Array.from(alphaRow?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'Retry secret check')
    expect(retry).toBeDefined()
    await act(async () => retry?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()
    await flushPromises()

    expect(apiMock.getProjectSecretStatuses).toHaveBeenNthCalledWith(1, 'project-1', ['ALPHA_TOKEN', 'BETA_TOKEN'])
    expect(apiMock.getProjectSecretStatuses).toHaveBeenNthCalledWith(2, 'project-1', ['ALPHA_TOKEN'])
    expect(alphaRow?.textContent).not.toContain('Secret status unavailable')
    const betaRow = Array.from(container.querySelectorAll('tr')).find(row => row.textContent?.includes('Beta Script'))
    expect(betaRow?.textContent).toContain('Secret status unavailable')
  })

  it('canonicalizes every backend-valid secret reference before readiness lookup', async () => {
    const script = {
      ...scriptRecord('script-1', 'Canonical Secrets', 'project-1'),
      script: {
        name: 'Canonical Secrets',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: '' },
        actions: [{
          name: 'Call', type: 'http', target: '/',
          headers: { Authorization: 'Bearer {{secret.login-token}} {{secret.1token}}' },
        }],
      },
    }
    apiMock.listScripts.mockResolvedValueOnce({ scripts: [script], count: 1 })
    apiMock.getProjectSecretStatuses.mockResolvedValueOnce({
      project_id: 'project-1',
      secrets: [
        { name: 'LOGIN_TOKEN', configured: true },
        { name: '1TOKEN', configured: true },
      ],
      capabilities: { provider: 'encrypted_database', writable: true },
    })

    await act(async () => {
      root.render(<ScriptsPage />)
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledWith('project-1', ['1TOKEN', 'LOGIN_TOKEN'])
    const row = Array.from(container.querySelectorAll('tr')).find(candidate => candidate.textContent?.includes('Canonical Secrets'))
    expect(row?.textContent).not.toContain('secret missing')
  })

  it('does not issue ID-routed requests or actions for scripts with missing or noncanonical IDs', async () => {
    apiMock.listScripts.mockResolvedValueOnce({
      scripts: [
        {
          ...scriptRecord('', 'Legacy Blank ID', 'project-1'),
          ref_key: 'LOC-SCR-0015-v1',
        },
        {
          ...scriptRecord(' addressable-script ', 'Legacy Whitespace ID', 'project-1'),
          ref_key: 'LOC-SCR-0016-v1',
        },
        scriptRecord('addressable-script', 'Addressable Script', 'project-1'),
      ],
      count: 3,
    })

    await act(async () => {
      root.render(<ScriptsPage />)
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.getScriptSecretStatus).not.toHaveBeenCalled()
    expect(apiMock.getProjectSecretStatuses).not.toHaveBeenCalled()

    const legacyRow = Array.from(container.querySelectorAll('tbody > tr'))
      .find(row => row.textContent?.includes('Legacy Blank ID'))
    expect(legacyRow).toBeDefined()
    expect(legacyRow?.textContent).toContain('Script unavailable')
    expect(legacyRow?.textContent).toContain('missing script id')
    expect(legacyRow?.textContent).toContain('Unavailable')
    expect(Array.from(legacyRow?.querySelectorAll('button') || []).some(button => (
      ['Edit', 'Delete', 'GitHub', 'Push', 'Compare'].includes(button.textContent?.trim() || '')
    ))).toBe(false)
    expect(legacyRow?.querySelector<HTMLButtonElement>('.script-row-toggle')?.disabled).toBe(true)

    const whitespaceRow = Array.from(container.querySelectorAll('tbody > tr'))
      .find(row => row.textContent?.includes('Legacy Whitespace ID'))
    expect(whitespaceRow?.textContent).toContain('Script unavailable')
    expect(whitespaceRow?.textContent).toContain('Unavailable')
    expect(whitespaceRow?.querySelector<HTMLButtonElement>('.script-row-toggle')?.disabled).toBe(true)
  })

  it('routes sample import edit handoff through the draft editor and into persisted edit mode after save', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledWith('project-1')

    expect(container.textContent).toContain('Script Library')
    expect(container.textContent).toContain('Existing Script')
    expect(container.textContent).toContain('Needs Review · 72')
    expect(container.textContent).toContain('1 warning')
    const libraryHeaders = Array.from(container.querySelectorAll('th')).map(th => th.textContent || '')
    expect(libraryHeaders.some(header => header.includes('Script'))).toBe(true)
    expect(libraryHeaders.some(header => header.includes('Usage'))).toBe(true)
    expect(container.textContent).toContain('0 scenarios')

    const existingRow = Array.from(container.querySelectorAll('tr')).find(row => row.textContent?.includes('Existing Script'))
    expect(existingRow).toBeDefined()
    if (!existingRow) {
      return
    }
    const existingRowToggle = existingRow.querySelector<HTMLButtonElement>('.script-row-toggle')
    expect(existingRowToggle).not.toBeNull()

    await act(async () => {
      existingRowToggle?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(container.textContent).toContain('Authoring Health Inputs')
    expect(container.textContent).toContain('warnings · warning · 1')
    expect(container.textContent).toContain('Validation warnings need review before promotion.')

    await clickButton('Script Builder')

    expect(container.textContent).toContain('Create Script')
    expect(container.textContent).toContain('Model From Existing Script')

    await clickButton('Start New Script')
    await clickButton('Blank & Assisted Editors')

    const sampleButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Build From Sample'))
    expect(sampleButton).toBeDefined()
    if (!sampleButton) {
      return
    }

    await act(async () => {
      sampleButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      await vi.dynamicImportSettled()
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="sample-import"]')).not.toBeNull()

    const sampleEditButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-sample-edit'))
    expect(sampleEditButton).toBeDefined()
    if (!sampleEditButton) {
      return
    }

    await act(async () => {
      sampleEditButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Sample Draft')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-save'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledTimes(2)
    expect(apiMock.listScripts).toHaveBeenLastCalledWith('project-1')
    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Sample Draft')
    expect(container.textContent).toContain('script-id:saved-script-1')
  })

  it('toggles between the script library and script builder', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    let toggleButtons = Array.from(container.querySelectorAll('.script-page-toggle button'))
    expect(toggleButtons.find(button => button.textContent?.includes('Script Library'))?.getAttribute('aria-pressed')).toBe('true')
    expect(toggleButtons.find(button => button.textContent?.includes('Script Builder'))?.getAttribute('aria-pressed')).toBe('false')
    expect(container.textContent).toContain('Script Library')
    expect(container.textContent).not.toContain('Script Library (')
    expect(container.textContent).not.toContain('Model From Existing Script')
    expect(container.textContent).toContain('Library Sources')
    expect(container.textContent).toContain('Associated Release')
    expect(container.textContent).toContain('Search Release')
    expect(container.textContent).not.toContain('Asset Type')
    expect(container.textContent).not.toContain('Select a Remote Workspace first.')

    await clickButton('Script Builder')

    toggleButtons = Array.from(container.querySelectorAll('.script-page-toggle button'))
    expect(toggleButtons.find(button => button.textContent?.includes('Script Library'))?.getAttribute('aria-pressed')).toBe('false')
    expect(toggleButtons.find(button => button.textContent?.includes('Script Builder'))?.getAttribute('aria-pressed')).toBe('true')
    expect(container.textContent).toContain('Model From Existing Script')
    expect(container.textContent).not.toContain('Script Library (')
    const builderHeaders = Array.from(container.querySelectorAll('.script-create-existing-table th')).map(header => header.textContent || '')
    expect(builderHeaders.some(header => header.includes('Source'))).toBe(true)
    expect(builderHeaders.some(header => header.includes('Script'))).toBe(true)
    expect(builderHeaders.some(header => header.includes('Health'))).toBe(true)
    expect(builderHeaders.some(header => header.includes('Usage'))).toBe(true)
    expect(builderHeaders.some(header => header.includes('Associated Releases'))).toBe(true)
    expect(builderHeaders.some(header => header.includes('Create Date'))).toBe(true)
    expect(builderHeaders.some(header => header.includes('Last Run'))).toBe(true)
    expect(builderHeaders.some(header => header.includes('Actions'))).toBe(true)

    await clickButton('Script Library')

    toggleButtons = Array.from(container.querySelectorAll('.script-page-toggle button'))
    expect(toggleButtons.find(button => button.textContent?.includes('Script Library'))?.getAttribute('aria-pressed')).toBe('true')
    expect(toggleButtons.find(button => button.textContent?.includes('Script Builder'))?.getAttribute('aria-pressed')).toBe('false')
    expect(container.textContent).toContain('Script Library')
    expect(container.textContent).not.toContain('Script Library (')
    expect(container.textContent).not.toContain('Model From Existing Script')
  })

  it('renders only one script library/builder toggle when the title bar provider is present', async () => {
    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TopbarContributionHarness />
          <ScriptsPage />
        </TopbarContributionProvider>,
      )
    })

    await flushPromises()

    let toggleButtons = Array.from(container.querySelectorAll('.script-page-toggle button'))
    expect(toggleButtons).toHaveLength(2)
    expect(toggleButtons.find(button => button.textContent?.includes('Script Library'))?.getAttribute('aria-pressed')).toBe('true')

    const builderButton = toggleButtons.find(button => button.textContent?.includes('Script Builder'))
    expect(builderButton).toBeDefined()
    await act(async () => {
      builderButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    toggleButtons = Array.from(container.querySelectorAll('.script-page-toggle button'))
    expect(toggleButtons).toHaveLength(2)
    expect(toggleButtons.find(button => button.textContent?.includes('Script Library'))?.getAttribute('aria-pressed')).toBe('false')
    expect(toggleButtons.find(button => button.textContent?.includes('Script Builder'))?.getAttribute('aria-pressed')).toBe('true')
    expect(container.textContent).toContain('Model From Existing Script')
    expect(container.querySelectorAll('[data-testid="topbar-contribution"] .script-page-toggle')).toHaveLength(1)
  })

  it('consolidates source import choices under one create workflow', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    const workflowCards = Array.from(container.querySelectorAll('.script-workflow-card')).map(button => button.textContent || '')
    expect(workflowCards).toHaveLength(4)
    expect(workflowCards.some(label => label.includes('Start New Script'))).toBe(true)
    expect(workflowCards.some(label => label.includes('Use Template'))).toBe(false)
    expect(workflowCards.some(label => label.includes('Build Manually'))).toBe(false)
    expect(workflowCards.some(label => label.includes('Import From Source'))).toBe(true)
    expect(workflowCards.some(label => label.includes('Start From Source'))).toBe(false)
    expect(workflowCards.some(label => label.includes('Import Existing'))).toBe(false)

    await clickButton('Import From Source')
    await flushPromises()

    expect(container.textContent).toContain('Contracts & Collections')
    expect(container.textContent).toContain('Traffic & Samples')
    expect(container.textContent).toContain('Legacy & Tool Exports')
    expect(container.textContent).toContain('Messaging & Cloud Artifacts')
    expect(container.textContent).toContain('Enterprise App Reviews')
    expect(container.textContent).not.toContain('Import OpenAPI')
    expect(container.textContent).not.toContain('Import Postman')
    expect(container.textContent).not.toContain('Import WSDL')
    expect(container.textContent).not.toContain('Import HAR')
    expect(container.textContent).not.toContain('Import DevWeb')
    expect(container.textContent).not.toContain('Salesforce Family')
    expect(container.textContent).not.toContain('Salesforce HAR')

    const countImportOption = (label: string) =>
      Array.from(container.querySelectorAll('.script-create-option h3')).filter(heading => heading.textContent === label).length

    expect(countImportOption('OpenAPI / Swagger')).toBe(1)
    expect(countImportOption('Postman Collection')).toBe(1)
    expect(countImportOption('WSDL / SOAP')).toBe(1)
    expect(countImportOption('AsyncAPI')).toBe(1)
    expect(countImportOption('RAML / Anypoint')).toBe(1)
    expect(countImportOption('HAR Traffic')).toBe(1)
    expect(countImportOption('Request / Response Sample')).toBe(1)
    expect(countImportOption('Import Java over HTTP')).toBe(0)
    expect(container.textContent).toContain('Java over HTTP')
    expect(countImportOption('Import Loadrunner Script Archive')).toBe(1)
    expect(countImportOption('Import JMeter')).toBe(1)
    expect(countImportOption('DevWeb Source')).toBe(1)
    expect(countImportOption('k6 Script')).toBe(1)
    expect(countImportOption('Event Hubs Capture')).toBe(1)
    expect(countImportOption('Broker Config')).toBe(1)
    expect(countImportOption('Oracle E-Business / Siebel Review')).toBe(1)
    expect(container.querySelectorAll('.script-create-import-groups .script-create-option')).toHaveLength(16)
    expect(countImportOption('Import SOAP Assets')).toBe(0)
  })

  it('falls back to the built-in source registry when an older controller omits import_surfaces', async () => {
    apiMock.listAuthoringCapabilities.mockResolvedValueOnce({
      sources: [],
      source_map: {},
    })

    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()
    await clickButton('Import From Source')
    await flushPromises()

    expect(container.textContent).toContain('Contracts & Collections')
    expect(container.textContent).toContain('Traffic & Samples')
    expect(container.textContent).toContain('Messaging & Cloud Artifacts')
    expect(container.querySelectorAll('.script-create-import-groups .script-create-option')).toHaveLength(16)
    expect(container.textContent).toContain('HAR Traffic')
    expect(container.textContent).toContain('Oracle E-Business / Siebel Review')
    expect(container.textContent).not.toContain('No import sources are available from this controller.')
  })

  it('opens the recording studio directly from the Record Browser workflow card', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Record Browser')
    expect(container.textContent).not.toContain('Browser Recorder')

    await clickButton('Record Browser')
    await act(async () => {
      await vi.dynamicImportSettled()
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="recording-studio"]')).not.toBeNull()
    expect(container.textContent).not.toContain('Browser Recorder')
  })

  it('consolidates template and manual choices under Start New Script', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await clickButton('Start New Script')

    const lanes = Array.from(container.querySelectorAll('.script-create-lane.full'))
    expect(lanes).toHaveLength(1)
    expect(container.textContent).toContain('Starter Templates')
    expect(container.textContent).toContain('Blank & Assisted Editors')
    expect(container.textContent).toContain('Reusable Blocks')
    expect(container.querySelectorAll('.script-create-category-tabs [role="tab"]')).toHaveLength(3)
    expect(container.querySelector('.script-create-choice-groups .script-create-option')).toBeNull()

    await clickButton('Blank & Assisted Editors')

    expect(container.textContent).toContain('API Visual Editor')
    expect(container.textContent).toContain('Build From Sample')
    expect(container.querySelectorAll('.script-create-table-list')).toHaveLength(1)
    expect(container.textContent).not.toContain('Use Template')
    expect(container.textContent).not.toContain('Build Manually')
  })

  it('models a new draft from an existing saved script without editing the original', async () => {
    apiMock.cloneScriptDraft.mockResolvedValueOnce({
      script: {
        name: 'Existing Script (Copy)',
        version: '1.0.0',
        protocol: 'http',
        project_id: 'project-1',
        config: { base_url: 'https://existing.example.com' },
        actions: [{ name: 'Get Existing', type: 'request', method: 'GET', target: '/existing' }],
        created_at: '2026-05-12T20:00:00Z',
        updated_at: '2026-05-12T20:00:00Z',
      },
    })

    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Model From Existing Script')
    await clickButton('Existing Script')
    await flushPromises()

    expect(apiMock.cloneScriptDraft).toHaveBeenCalledWith('script-1', {
      import_detected_credential_values: false,
    })
    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Existing Script (Copy)')
    expect(container.textContent).toContain('script-id:none')
  })

  it('ignores a late Model Existing response after authoring navigation', async () => {
    let resolveClone: ((value: {
      script: {
        name: string
        version: string
        protocol: string
        config: { base_url: string }
        actions: unknown[]
      }
      secret_binding_requirements: unknown[]
    }) => void) | undefined
    apiMock.cloneScriptDraft.mockReturnValueOnce(new Promise(resolve => {
      resolveClone = resolve
    }))

    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })
    await flushPromises()

    await clickButton('Existing Script')
    expect(apiMock.cloneScriptDraft).toHaveBeenCalledTimes(1)
    await clickButton('Script Library')
    expect(container.textContent).not.toContain('Model From Existing Script')

    await act(async () => {
      resolveClone?.({
        script: {
          name: 'Late Existing Script (Copy)',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://late.example.com' },
          actions: [],
        },
        secret_binding_requirements: [],
      })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelector('[data-testid="script-editor"]')).toBeNull()
    expect(container.textContent).not.toContain('Late Existing Script (Copy)')
    expect(container.textContent).toContain('Script Library')
  })

  it('optionally migrates recoverable source credentials while always applying safe references', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValueOnce({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [{
        id: 'scripts_and_projects',
        name: 'Scripts and projects',
        description: '',
        operations: [
          { id: 'manage_scripts', label: 'Manage scripts', allowed: true },
          { id: 'manage_projects', label: 'Manage projects', allowed: true },
        ],
      }],
    })
    apiMock.getSystemSettings.mockResolvedValueOnce({
      script_imports: { allow_source_secret_migration: true },
    })
    const maskedDraft = {
      name: 'Existing Script (Copy)',
      version: '1.0.0',
      protocol: 'http',
      project_id: 'project-1',
      config: { base_url: 'https://existing.example.com' },
      actions: [{
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        headers: { Authorization: 'Bearer [REDACTED]' },
      }],
    }
    const safeDraft = {
      ...maskedDraft,
      actions: [{
        ...maskedDraft.actions[0],
        headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' },
      }],
    }
    apiMock.cloneScriptDraft.mockResolvedValueOnce({
      script: safeDraft,
      secret_binding_requirements: [{
        id: 'login-token',
        path: '/actions/0/headers/Authorization',
        label: 'Login token',
        suggested_name: 'LOGIN_TOKEN',
        replacement: 'Bearer {{secret.LOGIN_TOKEN}}',
        source_value_available: true,
      }, {
        id: 'redacted-only',
        path: '/actions/0/body/password',
        label: 'Password',
        suggested_name: 'LOGIN_PASSWORD',
        replacement: '{{secret.LOGIN_PASSWORD}}',
      }],
      source_secret_session_id: 'session-1',
      source_value_requirement_ids: ['login-token'],
      secret_provider_capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: true,
      },
    })
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })
    await flushPromises()

    const migrateToggle = container.querySelector<HTMLInputElement>('.script-create-secret-migration input')
    expect(migrateToggle).not.toBeNull()
    await act(async () => migrateToggle?.click())
    await clickButton('Existing Script')
    await flushPromises()

    expect(apiMock.cloneScriptDraft).toHaveBeenCalledWith('script-1', {
      import_detected_credential_values: true,
    })
    expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledWith('project-1', ['LOGIN_TOKEN'])
    expect(apiMock.migrateImportSecrets).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'session-1',
      secrets: [{ requirement_id: 'login-token', secret_name: 'LOGIN_TOKEN' }],
    })
    expect(apiMock.discardImportSecretSession).not.toHaveBeenCalled()
    expect(container.textContent).toContain('editor:Existing Script (Copy)')
  })

  it('does not consume a Model Existing source-secret session after navigation during the status probe', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValueOnce({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [{
        id: 'scripts_and_projects',
        name: 'Scripts and projects',
        description: '',
        operations: [
          { id: 'manage_scripts', label: 'Manage scripts', allowed: true },
          { id: 'manage_projects', label: 'Manage projects', allowed: true },
        ],
      }],
    })
    apiMock.getSystemSettings.mockResolvedValueOnce({
      script_imports: { allow_source_secret_migration: true },
    })
    apiMock.cloneScriptDraft.mockResolvedValueOnce({
      script: {
        name: 'Existing Script (Copy)',
        version: '1.0.0',
        protocol: 'http',
        project_id: 'project-1',
        config: { base_url: 'https://existing.example.com' },
        actions: [{ name: 'Login', type: 'request', target: '/', headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' } }],
      },
      secret_binding_requirements: [{
        id: 'login-token',
        path: '/actions/0/headers/Authorization',
        label: 'Login token',
        suggested_name: 'LOGIN_TOKEN',
        replacement: 'Bearer {{secret.LOGIN_TOKEN}}',
        source_value_available: true,
      }],
      source_secret_session_id: 'session-late',
    })
    let resolveStatus: ((value: unknown) => void) | undefined
    apiMock.getProjectSecretStatuses.mockReturnValueOnce(new Promise(resolve => {
      resolveStatus = resolve
    }))

    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })
    await flushPromises()
    const migrateToggle = container.querySelector<HTMLInputElement>('.script-create-secret-migration input')
    await act(async () => migrateToggle?.click())
    await clickButton('Existing Script')
    await flushPromises()
    expect(apiMock.getProjectSecretStatuses).toHaveBeenCalledWith('project-1', ['LOGIN_TOKEN'])

    await clickButton('Script Library')
    await act(async () => {
      resolveStatus?.({ project_id: 'project-1', secrets: [] })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'session-late',
    })
    expect(container.querySelector('[data-testid="script-editor"]')).toBeNull()
  })

  it('opens a safe Model Existing draft when optional source migration conflicts', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValueOnce({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [{
        id: 'scripts_and_projects',
        name: 'Scripts and projects',
        description: '',
        operations: [
          { id: 'manage_scripts', label: 'Manage scripts', allowed: true },
          { id: 'manage_projects', label: 'Manage projects', allowed: true },
        ],
      }],
    })
    apiMock.getSystemSettings.mockResolvedValueOnce({
      script_imports: { allow_source_secret_migration: true },
    })
    apiMock.cloneScriptDraft.mockResolvedValueOnce({
      script: {
        name: 'Existing Script (Copy)',
        version: '1.0.0',
        protocol: 'http',
        project_id: 'project-1',
        config: { base_url: 'https://existing.example.com' },
        actions: [{ name: 'Login', type: 'request', target: '/', headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' } }],
      },
      secret_binding_requirements: [{
        id: 'login-token',
        path: '/actions/0/headers/Authorization',
        label: 'Login token',
        suggested_name: 'LOGIN_TOKEN',
        replacement: 'Bearer {{secret.LOGIN_TOKEN}}',
        source_value_available: true,
      }],
      source_secret_session_id: 'session-conflict',
    })
    apiMock.migrateImportSecrets.mockRejectedValueOnce(new ApiError('provider write failed', 502, {
      partial: true,
      source_secret_session_consumed: true,
      manual_configuration_required: true,
      remaining_names: ['LOGIN_TOKEN'],
      migrated: [{ name: 'ALREADY_CREATED_TOKEN', provider: 'database' }],
    }))

    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })
    await flushPromises()

    const migrateToggle = container.querySelector<HTMLInputElement>('.script-create-secret-migration input')
    await act(async () => migrateToggle?.click())
    await clickButton('Existing Script')
    await flushPromises()

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Existing Script (Copy)')
    expect(container.textContent).toContain('initially-dirty:yes')
    expect(container.textContent).toContain('safe draft was opened without importing detected credential values')
    expect(container.textContent).toContain('Remaining credentials: LOGIN_TOKEN')
    expect(container.textContent).not.toContain('ALREADY_CREATED_TOKEN')
  })

  it('surfaces a recoverable error when an existing script cannot be cloned', async () => {
    apiMock.cloneScriptDraft.mockRejectedValueOnce(new Error('clone source unavailable'))

    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Model From Existing Script')
    await clickButton('Existing Script')
    await flushPromises()

    expect(apiMock.cloneScriptDraft).toHaveBeenCalledWith('script-1', {
      import_detected_credential_values: false,
    })
    expect(container.querySelector('[data-testid="script-editor"]')).toBeNull()
    expect(container.textContent).toContain('clone source unavailable')
  })

  it('surfaces script library load failures', async () => {
    apiMock.listScripts.mockRejectedValueOnce(new Error('script list unavailable'))

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('script list unavailable')
    expect(container.textContent).toContain('No scripts yet.')
  })

  it('routes an empty library CTA into the start-new workflow', async () => {
    apiMock.listScripts.mockResolvedValueOnce({ scripts: [], count: 0 })

    await act(async () => {
      root.render(<ScriptsPage />)
    })
    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent?.trim() === 'Create your first script')
    expect(createButton).toBeDefined()
    await act(async () => { createButton?.click() })
    await flushPromises()

    expect(container.textContent).toContain('New draft authoring')
    expect(container.textContent).not.toContain('Model From Existing Script')
  })

  it('surfaces authoring-session load failures', async () => {
    apiMock.listAuthoringSessions.mockRejectedValueOnce(new Error('session list unavailable'))

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    expect(container.querySelector('[role="alert"]')?.textContent).toContain('session list unavailable')
    expect(container.textContent).toContain('Existing Script')
  })

  it('keeps simultaneous library load failures in one notice', async () => {
    apiMock.listScripts.mockRejectedValueOnce(new Error('script list unavailable'))
    apiMock.listAuthoringSessions.mockRejectedValueOnce(new Error('session list unavailable'))

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const alertText = container.querySelector('[role="alert"]')?.textContent || ''
    expect(alertText).toContain('script list unavailable')
    expect(alertText).toContain('session list unavailable')
  })

  it('surfaces script deletion failures without removing the row', async () => {
    apiMock.deleteScript.mockRejectedValueOnce(new Error('delete denied'))
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValueOnce(true)

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()
    await clickButton('Delete')
    await flushPromises()

    expect(confirmSpy).toHaveBeenCalled()
    expect(apiMock.deleteScript).toHaveBeenCalledWith('script-1')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('delete denied')
    expect(container.textContent).toContain('Existing Script')
  })

  it('filters the script library by visible script metadata', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    await clickButton('Script Library')

    const searchInput = container.querySelector('input[type="search"]') as HTMLInputElement | null
    expect(searchInput).not.toBeNull()
    if (!searchInput) {
      return
    }

    await act(async () => {
      setInputValue(searchInput, 'needs_review')
    })

    expect(container.textContent).toContain('Existing Script')
    expect(container.textContent).toContain('1 matching script')

    await act(async () => {
      setInputValue(searchInput, 'does-not-exist')
    })

    expect(container.textContent).toContain('0 matching scripts')
    expect(container.textContent).toContain('No scripts match these filters.')
    expect(container.textContent).not.toContain('Existing Script')
  })

  it('filters the script library by associated release and shows release and create date columns', async () => {
    apiMock.listFolders.mockResolvedValueOnce({
      folders: [
        { id: 'folder-checkout', project_id: 'project-1', name: 'Checkout APIs', category: 'scripts', created_at: '', updated_at: '' },
        { id: 'folder-payments', project_id: 'project-1', name: 'Payments APIs', category: 'scripts', created_at: '', updated_at: '' },
      ],
      count: 2,
    } as never)
    apiMock.listScripts.mockResolvedValueOnce({
      scripts: [
        {
          id: 'script-checkout',
          name: 'Checkout Script',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-12T20:00:00Z',
          scenario_count: 1,
          folder_id: 'folder-checkout',
          associated_releases: [{ id: 'release-29', name: 'PI29', application_id: 'app-1' }],
          scenario_run_releases: [],
          last_run_at: '',
        },
        {
          id: 'script-payments',
          name: 'Payments Script',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-13T20:00:00Z',
          scenario_count: 1,
          folder_id: 'folder-payments',
          associated_releases: [{ id: 'release-30', name: 'PI30', application_id: 'app-1' }],
          scenario_run_releases: [],
          last_run_at: '',
        },
      ],
    })

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const headers = Array.from(container.querySelectorAll('th')).map(th => th.textContent || '')
    expect(headers.some(header => header.includes('Associated Releases'))).toBe(true)
    expect(headers.some(header => header.includes('Create Date'))).toBe(true)
    expect(headers.some(header => header.includes('Folder'))).toBe(true)
    expect(container.textContent).toContain('Checkout Script')
    expect(container.textContent).toContain('Payments Script')

    const releaseFilter = container.querySelector('select.library-release-filter') as HTMLSelectElement | null
    expect(releaseFilter).toBeTruthy()
    if (!releaseFilter) return

    expect(Array.from(releaseFilter.options).map(option => option.textContent)).toEqual(['All releases', 'PI29', 'PI30'])

    await act(async () => {
      setSelectValue(releaseFilter, 'PI29')
    })

    expect(container.textContent).toContain('1 matching script')
    expect(container.textContent).toContain('Checkout Script')
    expect(container.textContent).not.toContain('Payments Script')

    await act(async () => {
      setSelectValue(releaseFilter, 'PI30')
    })

    expect(container.textContent).toContain('Payments Script')
    expect(container.textContent).not.toContain('Checkout Script')

    await act(async () => {
      setSelectValue(container.querySelector('select.library-release-filter') as HTMLSelectElement, 'all')
      setSelectValue(container.querySelector('select.library-folder-filter') as HTMLSelectElement, 'folder-checkout')
    })
    expect(container.textContent).toContain('Checkout Script')
    expect(container.textContent).not.toContain('Payments Script')

    await act(async () => {
      setSelectValue(container.querySelector('select.library-folder-filter') as HTMLSelectElement, 'all')
    })
    const folderSort = Array.from(container.querySelectorAll<HTMLButtonElement>('th button'))
      .find(button => button.textContent?.includes('Folder'))
    await act(async () => folderSort?.click())
    const firstScriptRow = container.querySelector<HTMLTableRowElement>('.scripts-library-table tbody tr')
    expect(firstScriptRow?.textContent).toContain('Checkout Script')
  })

  it('routes script library source pills to integrations settings', async () => {
    const onNavigate = vi.fn()

    await act(async () => {
      root.render(<ScriptsPage onNavigate={onNavigate} />)
    })

    await flushPromises()

    const noRemoteButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('No Remote Workspace'))
    const credentialsButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Credentials Missing'))
    expect(noRemoteButton).toBeDefined()
    expect(credentialsButton).toBeDefined()
    if (!noRemoteButton || !credentialsButton) return

    await act(async () => {
      noRemoteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('settings')
    expect(window.localStorage.getItem('apexload.settings.focusSection')).toBe('integrations')

    window.localStorage.removeItem('apexload.settings.focusSection')
    await act(async () => {
      credentialsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('settings')
    expect(window.localStorage.getItem('apexload.settings.focusSection')).toBe('integrations')
  })

  it('notifies the parent topbar when a library script opens in edit mode', async () => {
    const onAuthoringStateChange = vi.fn()
    apiMock.getScript.mockResolvedValueOnce({
      script: {
        id: 'script-1',
        name: 'Existing Script',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://scripts.example.com' },
        actions: [],
      },
    })

    await act(async () => {
      root.render(<ScriptsPage onAuthoringStateChange={onAuthoringStateChange} />)
    })

    await flushPromises()

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Edit')
    expect(editButton).toBeDefined()
    if (!editButton) return

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.getScript).toHaveBeenCalledWith('script-1')
    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(onAuthoringStateChange).toHaveBeenCalledWith({ subView: 'edit', editingId: 'script-1' })
  })

  it('filters the script library by authoring health categories', async () => {
    apiMock.listScripts.mockResolvedValueOnce({
      scripts: [
        {
          id: 'blocked-script',
          name: 'Blocked Script',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-12T20:00:00Z',
          authoring_health: { score: 20, status: 'blocked', inputs: [{ key: 'validation', status: 'blocker', summary: 'Script has blocking validation errors.' }] },
        },
        {
          id: 'runtime-script',
          name: 'Runtime Warning Script',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-12T20:00:00Z',
          authoring_health: { score: 70, status: 'needs_review', inputs: [{ key: 'runtime_parity', status: 'warning', summary: 'Runtime parity warning requires review.' }] },
        },
        {
          id: 'ai-script',
          name: 'AI Guardrail Script',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-12T20:00:00Z',
          authoring_health: { score: 80, status: 'needs_review', inputs: [{ key: 'ai_guardrails', status: 'warning', summary: 'AI provenance requires approval.' }] },
        },
      ],
    })

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    await clickButton('Script Library')

    expect(container.textContent).toContain('Blocked')
    expect(container.textContent).toContain('Runtime Warning')
    expect(container.textContent).toContain('AI Guardrail')

    await clickButton('Runtime Warning')

    expect(container.textContent).toContain('Runtime Warning Script')
    expect(container.textContent).not.toContain('Blocked Script')
    expect(container.textContent).not.toContain('AI Guardrail Script')

    await clickButton('AI Guardrail')

    expect(container.textContent).toContain('AI Guardrail Script')
    expect(container.textContent).not.toContain('Runtime Warning Script')
  })

  it('routes source-guided OpenAPI import through the draft editor', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await clickButton('Import From Source')
    await clickButton('OpenAPI / Swagger')

    expect(container.querySelector('[data-testid="protocol-import-openapi"]')).not.toBeNull()

    await clickButton('mock-openapi-edit')

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:OPENAPI Draft')
  })

  it('routes jmeter import edit handoff through the draft editor', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await clickButton('Import From Source')

    const jmeterButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import JMeter'))
    expect(jmeterButton).toBeDefined()
    if (!jmeterButton) {
      return
    }

    await act(async () => {
      jmeterButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="jmeter-import"]')).not.toBeNull()

    const jmeterEditButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-jmeter-edit'))
    expect(jmeterEditButton).toBeDefined()
    if (!jmeterEditButton) {
      return
    }

    await act(async () => {
      jmeterEditButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:JMeter Draft')
  })

  it('shows a follow-on notice when a multi-script import also saves a scenario draft', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await clickButton('Import From Source')

    const jmeterButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import JMeter'))
    expect(jmeterButton).toBeDefined()
    if (!jmeterButton) {
      return
    }

    await act(async () => {
      jmeterButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const importSaveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-jmeter-save'))
    expect(importSaveButton).toBeDefined()
    if (!importSaveButton) {
      return
    }

    await act(async () => {
      importSaveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Auth Users')
    expect(container.textContent).toContain('script-id:jmeter-script-1')
    expect(container.textContent).toContain('Saved 2 scripts and scenario draft "Imported JMeter Scenario". Reviewing the first saved script.')
  })

  it('keeps the persisted editor open after an import save handoff', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await clickButton('Import From Source')

    const enterpriseArchiveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import Loadrunner Script Archive'))
    expect(enterpriseArchiveButton).toBeDefined()
    if (!enterpriseArchiveButton) {
      return
    }

    await act(async () => {
      enterpriseArchiveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="enterprise-archive-import"]')).not.toBeNull()

    const importSaveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-enterprise-archive-save'))
    expect(importSaveButton).toBeDefined()
    if (!importSaveButton) {
      return
    }

    await act(async () => {
      importSaveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Imported Enterprise Script Archive')
    expect(container.textContent).toContain('script-id:imported-script-1')
  })

  it('refreshes the library without leaving an enterprise archive that still has unsaved lanes', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })
    await flushPromises()

    await clickButton('Import From Source')
    await clickButton('Import Loadrunner Script Archive')
    const preservedInput = container.querySelector<HTMLInputElement>('input[aria-label="mock-enterprise-archive-state"]')!
    preservedInput.value = 'pending-lane-still-mounted'

    await clickButton('mock-enterprise-archive-refresh-only-save')
    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[data-testid="enterprise-archive-import"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="script-editor"]')).toBeNull()
    expect(container.querySelector<HTMLInputElement>('input[aria-label="mock-enterprise-archive-state"]')?.value)
      .toBe('pending-lane-still-mounted')
  })

  it('does not let a late non-navigating save replace the authoring surface opened after Back', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })
    await flushPromises()

    await clickButton('Import From Source')
    await clickButton('Import Loadrunner Script Archive')
    await clickButton('mock-enterprise-archive-close')
    await clickButton('Import From Source')
    await clickButton('Import JMeter')
    expect(container.querySelector('[data-testid="jmeter-import"]')).not.toBeNull()

    await act(async () => {
      publishLateEnterpriseSave?.()
    })
    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[data-testid="jmeter-import"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="script-editor"]')).toBeNull()
    expect(container.textContent).not.toContain('editor:Late Enterprise Save')
  })

  it('returns Enterprise Script Archive import close actions to the create options shell', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await clickButton('Import From Source')

    const enterpriseArchiveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import Loadrunner Script Archive'))
    expect(enterpriseArchiveButton).toBeDefined()
    if (!enterpriseArchiveButton) {
      return
    }

    await act(async () => {
      enterpriseArchiveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="enterprise-archive-import"]')).not.toBeNull()
    expect(container.textContent).not.toContain('Back to Create Script')

    const closeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-enterprise-archive-close'))
    expect(closeButton).toBeDefined()
    if (!closeButton) {
      return
    }

    await act(async () => {
      closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="enterprise-archive-import"]')).toBeNull()
    expect(container.textContent).toContain('Create Script')
    await clickButton('Import From Source')
    expect(container.textContent).toContain('Import JMeter')
  })

  it('returns from Enterprise Script Archive review editor options to the converted script review', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await clickButton('Import From Source')

    const enterpriseArchiveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import Loadrunner Script Archive'))
    expect(enterpriseArchiveButton).toBeDefined()
    if (!enterpriseArchiveButton) {
      return
    }

    await act(async () => {
      enterpriseArchiveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="enterprise-archive-import"]')).not.toBeNull()
    const preservedInput = container.querySelector('input[aria-label="mock-enterprise-archive-state"]') as HTMLInputElement | null
    expect(preservedInput).not.toBeNull()
    if (!preservedInput) {
      return
    }
    preservedInput.value = 'review-state-preserved'

    await clickButton('mock-enterprise-archive-review-option')

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Converted Enterprise Script Archive Draft')
    expect(container.textContent).toContain('Back to Converted Script')
    expect(container.querySelector('[data-testid="enterprise-archive-import"]')).not.toBeNull()

    await clickButton('Back to Converted Script')

    expect(container.querySelector('[data-testid="script-editor"]')).toBeNull()
    expect(container.querySelector('[data-testid="enterprise-archive-import"]')).not.toBeNull()
    expect((container.querySelector('input[aria-label="mock-enterprise-archive-state"]') as HTMLInputElement | null)?.value).toBe('review-state-preserved')
    expect(container.textContent).toContain('mock-enterprise-archive-review-option')
  })

  it('returns JMeter import close actions to the create options shell', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await clickButton('Import From Source')

    const jmeterButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import JMeter'))
    expect(jmeterButton).toBeDefined()
    if (!jmeterButton) {
      return
    }

    await act(async () => {
      jmeterButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="jmeter-import"]')).not.toBeNull()
    expect(container.textContent).not.toContain('Back to Create Script')

    const closeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-jmeter-close'))
    expect(closeButton).toBeDefined()
    if (!closeButton) {
      return
    }

    await act(async () => {
      closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="jmeter-import"]')).toBeNull()
    expect(container.textContent).toContain('Create Script')
    expect(container.textContent).toContain('Source-driven authoring')
    expect(container.textContent).not.toContain('Model From Existing Script')
    expect(container.textContent).toContain('Import Loadrunner Script Archive')
  })

  it('shows SOAP and Java-over-HTTP create options and routes them into the draft editor', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await clickButton('Start New Script')
    await clickButton('Blank & Assisted Editors')

    expect(container.textContent).toContain('Blank & Assisted Editors')
    expect(container.textContent).toContain('Web Services (SOAP)')
    expect(container.textContent).toContain('Java over HTTP')
    expect(container.textContent).toContain('embedded or sidecar helper execution')

    const soapButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Web Services (SOAP)'))
    expect(soapButton).toBeDefined()
    if (!soapButton) {
      return
    }

    await act(async () => {
      soapButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:blank')

    const closeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-close'))
    expect(closeButton).toBeDefined()
    if (!closeButton) {
      return
    }

    await act(async () => {
      closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const createButtonAgain = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Script Builder'))
    expect(createButtonAgain).toBeDefined()
    if (!createButtonAgain) {
      return
    }

    await act(async () => {
      createButtonAgain.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await clickButton('Start New Script')
    await clickButton('Blank & Assisted Editors')

    const javaButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Java over HTTP'))
    expect(javaButton).toBeDefined()
    if (!javaButton) {
      return
    }

    await act(async () => {
      javaButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
  })

  it('opens backend starter templates in the draft editor', async () => {
    apiMock.listAuthoringTemplates.mockResolvedValueOnce({
      templates: [{
        id: 'template-browser-canary',
        name: 'Browser Checkout Canary',
        protocol: 'browser',
        use_case: 'ci_smoke',
        description: 'Open checkout and prove the critical rendered path is alive.',
        script: {
          name: 'Browser Checkout Canary',
          version: '1.0.0',
          protocol: 'browser',
          config: { base_url: 'https://shop.example.com' },
          actions: [],
        },
        recommended_scenario: {
          duration: '90s',
          vusers: 1,
        },
      }],
      count: 1,
    })
    apiMock.listAuthoringComponents.mockResolvedValueOnce({
      components: [{
        id: 'component-browser-login',
        kind: 'login_transaction',
        name: 'Browser Login Transaction',
        protocols: ['browser'],
        summary: 'Browser login sequence with page-ready wait and post-login assertion.',
        tags: ['browser', 'login'],
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await flushPromises()
    await clickButton('Start New Script')

    expect(apiMock.listAuthoringTemplates).toHaveBeenCalledTimes(1)
    expect(apiMock.listAuthoringComponents).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Starter Templates')
    expect(container.textContent).toContain('Reusable Blocks')
    expect(container.textContent).toContain('Browser Checkout Canary')

    await clickButton('Reusable Blocks')
    expect(container.textContent).toContain('Browser Login Transaction')

    await clickButton('Starter Templates')

    const templateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Browser Checkout Canary'))
    expect(templateButton).toBeDefined()
    if (!templateButton) {
      return
    }

    await act(async () => {
      templateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Browser Checkout Canary')
  })

  it('composes a reusable block entry into the draft editor', async () => {
    apiMock.listAuthoringTemplates.mockResolvedValueOnce({ templates: [], count: 0 })
    apiMock.listAuthoringComponents.mockResolvedValueOnce({
      components: [{
        id: 'component-oauth-flow',
        kind: 'auth_flow',
        name: 'OAuth Bearer Token Flow',
        protocols: ['http', 'soap'],
        summary: 'Token acquisition with reusable Authorization header wiring.',
        tags: ['auth', 'oauth'],
        config: { http: { default_headers: { Authorization: 'Bearer {{var.access_token}}' } } },
        init: [{ name: 'Acquire Access Token', type: 'request', method: 'POST', target: '/oauth/token' }],
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()
    await flushPromises()
    await clickButton('Start New Script')
    await clickButton('Reusable Blocks')

    expect(container.textContent).toContain('Reusable Blocks')
    expect(container.textContent).toContain('OAuth Bearer Token Flow')

    const componentButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('OAuth Bearer Token Flow'))
    expect(componentButton).toBeDefined()
    if (!componentButton) {
      return
    }

    await act(async () => {
      componentButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:OAuth Bearer Token Flow')
  })

  it('surfaces resumable authoring sessions and approves them through the backend gate', async () => {
    apiMock.listAuthoringSessions.mockResolvedValue({
      sessions: [{
        id: 'session-1',
        project_id: 'project-1',
        source: 'enterprise_script_archive',
        name: 'Migrated checkout review',
        status: 'ready',
        draft: {
          name: 'Migrated checkout review',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://checkout.example.com' },
          actions: [],
        },
        review_state: { approval_health_status: 'needs_review', pending_repairs: ['correlation'] },
        approval_ready: true,
        next_actions: ['Review validation warnings before promotion.'],
        blocking_health_inputs: [],
        created_at: '2026-05-18T13:00:00Z',
        updated_at: '2026-05-18T13:05:00Z',
        last_accessed_at: '2026-05-18T13:05:00Z',
      }],
      count: 1,
    })
    apiMock.approveAuthoringSession.mockResolvedValueOnce({
      session: { id: 'session-1', name: 'Migrated checkout review', status: 'approved' },
      script: {
        id: 'approved-script-1',
        name: 'Migrated checkout review',
        protocol: 'http',
        version: '1.0.0',
        created_at: '2026-05-18T13:06:00Z',
      },
      authoring_health: { score: 100, status: 'ready', inputs: [] },
    })

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()
    await clickButton('Script Library')

    expect(apiMock.listAuthoringSessions).toHaveBeenCalledWith('project-1')
    expect(container.textContent).toContain('Authoring Sessions')
    expect(container.textContent).toContain('Migrated checkout review')
    expect(container.textContent).toContain('Enterprise Script Archive')
    expect(container.textContent).toContain('Last approval: needs_review')
    expect(container.textContent).toContain('1 pending repair')
    expect(container.textContent).toContain('approval ready')
    expect(container.textContent).toContain('Review validation warnings before promotion.')

    const resumeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Resume'))
    expect(resumeButton).toBeDefined()
    if (!resumeButton) {
      return
    }

    await act(async () => {
      resumeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Migrated checkout review')

    const closeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-close'))
    expect(closeButton).toBeDefined()
    if (!closeButton) {
      return
    }

    await act(async () => {
      closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const approveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Approve'))
    expect(approveButton).toBeDefined()
    if (!approveButton) {
      return
    }

    await act(async () => {
      approveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.approveAuthoringSession).toHaveBeenCalledWith('session-1')
    expect(apiMock.listScripts).toHaveBeenCalledTimes(2)
    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('script-id:approved-script-1')
    expect(container.textContent).toContain('Approved authoring session "Migrated checkout review". Reviewing the promoted script.')
  })

  it('surfaces authoring-session approval failures', async () => {
    apiMock.listAuthoringSessions.mockResolvedValue({
      sessions: [{
        id: 'session-1',
        project_id: 'project-1',
        source: 'enterprise_script_archive',
        name: 'Migrated checkout review',
        status: 'ready',
        draft: {
          name: 'Migrated checkout review',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://checkout.example.com' },
          actions: [],
        },
        approval_ready: true,
        blocking_health_inputs: [],
        created_at: '2026-05-18T13:00:00Z',
        updated_at: '2026-05-18T13:05:00Z',
      }],
      count: 1,
    })
    apiMock.approveAuthoringSession.mockRejectedValueOnce(new Error('approval gate failed'))

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const approveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Approve'))
    expect(approveButton).toBeDefined()
    if (!approveButton) {
      return
    }

    await act(async () => {
      approveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.approveAuthoringSession).toHaveBeenCalledWith('session-1')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('approval gate failed')
    expect(container.querySelector('[data-testid="script-editor"]')).toBeNull()
  })

  it('surfaces authoring-session discard failures', async () => {
    apiMock.listAuthoringSessions.mockResolvedValue({
      sessions: [{
        id: 'session-1',
        project_id: 'project-1',
        source: 'enterprise_script_archive',
        name: 'Migrated checkout review',
        status: 'draft',
        draft: {
          name: 'Migrated checkout review',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://checkout.example.com' },
          actions: [],
        },
        created_at: '2026-05-18T13:00:00Z',
        updated_at: '2026-05-18T13:05:00Z',
      }],
      count: 1,
    })
    apiMock.deleteAuthoringSession.mockRejectedValueOnce(new Error('discard failed'))

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()
    await clickButton('Discard')
    await flushPromises()

    expect(apiMock.deleteAuthoringSession).toHaveBeenCalledWith('session-1')
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('discard failed')
    expect(container.textContent).toContain('Migrated checkout review')
  })

  it('disables authoring-session approval when backend health inputs block promotion', async () => {
    apiMock.listAuthoringSessions.mockResolvedValue({
      sessions: [{
        id: 'session-blocked',
        project_id: 'project-1',
        source: 'ai_generated',
        name: 'Generated checkout review',
        status: 'ready',
        draft: {
          name: 'Generated checkout review',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://checkout.example.com' },
          actions: [],
        },
        review_state: { approval_health_status: 'blocked' },
        approval_ready: false,
        blocking_health_inputs: ['ai_guardrails', 'replay_readiness'],
        next_actions: ['Attach AI provenance, diff review, validation, and replay evidence.', 'Run or repair replay readiness before promotion.'],
        created_at: '2026-05-18T13:00:00Z',
        updated_at: '2026-05-18T13:05:00Z',
        last_accessed_at: '2026-05-18T13:05:00Z',
      }],
      count: 1,
    })

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()
    await clickButton('Script Library')

    expect(container.textContent).toContain('Generated checkout review')
    expect(container.textContent).toContain('approval blocked')
    expect(container.textContent).toContain('Blocking inputs: ai_guardrails, replay_readiness')
    expect(container.textContent).toContain('Attach AI provenance, diff review, validation, and replay evidence.')

    const approveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Approve'))
    expect(approveButton).toBeDefined()
    expect(approveButton?.disabled).toBe(true)
  })

  it('previews and applies guided correlation repairs for authoring sessions', async () => {
    apiMock.listAuthoringSessions.mockResolvedValue({
      sessions: [{
        id: 'session-1',
        project_id: 'project-1',
        source: 'har',
        name: 'HAR token repair',
        status: 'editing',
        draft: {
          name: 'HAR token repair',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://api.example.com' },
          actions: [
            { name: 'Get Token', method: 'GET', target: '/token' },
            { name: 'Submit Order', method: 'POST', target: '/orders', body: 'token=abc123' },
          ],
        },
        review_state: {},
        created_at: '2026-05-18T13:00:00Z',
        updated_at: '2026-05-18T13:05:00Z',
        last_accessed_at: '2026-05-18T13:05:00Z',
      }],
      count: 1,
    })
    apiMock.previewAuthoringCorrelation
      .mockResolvedValueOnce({
        session_id: 'session-1',
        applied: false,
        extractor: { name: 'order_token', source: 'body', type: 'jsonpath', expression: '$.token' },
        changes: [
          { kind: 'extractor_added', path: 'actions[0].extractors' },
          { kind: 'sample_replaced', path: 'actions[1].body', before: 'abc123', after: '{{var.order_token}}' },
        ],
        authoring_health: { score: 96, status: 'ready', inputs: [] },
      })
      .mockResolvedValueOnce({
        session_id: 'session-1',
        applied: true,
        extractor: { name: 'order_token', source: 'body', type: 'jsonpath', expression: '$.token' },
        changes: [
          { kind: 'extractor_added', path: 'actions[0].extractors' },
          { kind: 'sample_replaced', path: 'actions[1].body', before: 'abc123', after: '{{var.order_token}}' },
        ],
        authoring_health: { score: 96, status: 'ready', inputs: [] },
      })

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()
    await clickButton('Script Library')

    expect(container.textContent).toContain('Guided Correlation Repair')
    expect(container.textContent).toContain('2 actions available')

    const producer = container.querySelector('select[aria-label="Correlation producer for HAR token repair"]') as HTMLSelectElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!
      setter.call(producer, '1')
      producer.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const previewButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Preview Repair'))
    expect(previewButton).toBeDefined()
    if (!previewButton) {
      return
    }
    await act(async () => {
      previewButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.previewAuthoringCorrelation).toHaveBeenCalledWith('session-1', expect.objectContaining({
      name: 'correlated_value',
      expression: '$.token',
      found_action_index: 1,
      used_action_indexes: [2],
      replace_sample_value: true,
      apply: false,
    }))
    expect(container.textContent).toContain('Preview')
    expect(container.textContent).toContain('2 changes')
    expect(container.textContent).toContain('health ready · 96')
    expect(container.textContent).toContain('actions[1].body')

    const applyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Apply Repair'))
    expect(applyButton).toBeDefined()
    if (!applyButton) {
      return
    }
    await act(async () => {
      applyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.previewAuthoringCorrelation).toHaveBeenLastCalledWith('session-1', expect.objectContaining({
      name: 'correlated_value',
      apply: true,
    }))
    expect(container.textContent).toContain('Applied')
  })

  it('routes the merged sample import card into the profile-switchable sample surface', async () => {
    await act(async () => {
      root.render(<ScriptsPage initialMode="create-options" />)
    })

    await flushPromises()

    await clickButton('Import From Source')
    await flushPromises()

    const sampleImportButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Request / Response Sample'))
    expect(sampleImportButton).toBeDefined()
    if (!sampleImportButton) {
      return
    }

    await act(async () => {
      sampleImportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="sample-import"]')).not.toBeNull()
    expect(container.textContent).toContain('sample-import:default')
  })

  it('clears the script library and ignores a late response after the project changes', async () => {
    const projectOne = deferred<{ scripts: Array<ReturnType<typeof scriptRecord>> }>()
    apiMock.listScripts.mockImplementation((projectId: string) => {
      if (projectId === 'project-1') return projectOne.promise
      return Promise.resolve({ scripts: [scriptRecord('script-2', 'Catalog Script', 'project-2')] })
    })

    await act(async () => {
      root.render(<ScriptsPage />)
    })
    expect(apiMock.listScripts).toHaveBeenCalledWith('project-1')

    projectState.activeProject = { id: 'project-2', name: 'Catalog', created_at: '', updated_at: '' }
    await act(async () => {
      root.render(<ScriptsPage />)
    })
    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledWith('project-2')
    expect(container.textContent).toContain('Catalog Script')
    expect(container.textContent).not.toContain('Checkout Late Script')

    await act(async () => {
      projectOne.resolve({ scripts: [scriptRecord('script-1', 'Checkout Late Script', 'project-1')] })
    })
    await flushPromises()

    expect(container.textContent).toContain('Catalog Script')
    expect(container.textContent).not.toContain('Checkout Late Script')
  })

  it('renders the script library as read-only when manage_scripts is denied', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValueOnce({
      enabled: false,
      authenticated: true,
      resources: [
        {
          id: 'scripts_and_imports',
          name: 'Scripts, Imports, and Replay',
          description: '',
          operations: [{ id: 'manage_scripts', label: 'Manage scripts', allowed: false }],
        },
      ],
    })

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Existing Script')
    expect(container.textContent).not.toContain('+ Create Script')
    expect(container.textContent).not.toContain('Edit')
    expect(container.textContent).not.toContain('Delete')
    expect(container.textContent).toContain('Read-only')
  })

  it('loads the requested script when opened in edit mode from the parent route', async () => {
    apiMock.getScript.mockResolvedValueOnce({
      script: {
        id: 'script-42',
        name: 'Loaded Script',
        version: '2.0.0',
        protocol: 'http',
        config: { base_url: 'https://loaded.example.com' },
        actions: [],
      },
    })

    await act(async () => {
      root.render(<ScriptsPage initialMode="edit" initialEditId="script-42" />)
    })

    await flushPromises()

    expect(apiMock.getScript).toHaveBeenCalledWith('script-42')
    expect(container.querySelector('[data-testid="script-editor"]')).not.toBeNull()
    expect(container.textContent).toContain('editor:Loaded Script')
    expect(container.textContent).toContain('script-id:script-42')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

function scriptRecord(id: string, name: string, projectId: string) {
  return {
    id,
    name,
    project_id: projectId,
    protocol: 'http',
    version: '1.0.0',
    created_at: '2026-05-12T20:00:00Z',
    scenario_count: 0,
    associated_releases: [],
    scenario_run_releases: [],
    last_run_at: '',
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(next => {
    resolve = next
  })
  return { promise, resolve }
}

function setInputValue(input: HTMLInputElement, value: string) {
  const prototype = Object.getPrototypeOf(input) as HTMLInputElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function setSelectValue(select: HTMLSelectElement, value: string) {
  const prototype = Object.getPrototypeOf(select) as HTMLSelectElement
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

async function clickButton(label: string) {
  const button = Array.from(document.querySelectorAll('button')).find(element => element.textContent?.includes(label))
  expect(button).toBeDefined()
  if (!button) {
    return
  }
  await act(async () => {
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}
