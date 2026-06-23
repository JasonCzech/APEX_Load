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
  }: {
    subView: string
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
    openEnterpriseArchiveImport: vi.fn(),
    openJMeterImport: vi.fn(),
    openRecorder: vi.fn(),
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
    ...overrides,
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

    expect(container.textContent).toContain('Create Script')
    expect(container.textContent).toContain('Model From Existing Script')
    expect(container.textContent).toContain('Checkout Model')
    expect(container.textContent).toContain('Start New Script')
    expect(container.textContent).not.toContain('Use Template')
    expect(container.textContent).not.toContain('Build Manually')
    expect(container.textContent).toContain('Import From Source')
    expect(container.textContent).not.toContain('Start From Source')
    expect(container.textContent).not.toContain('Import Existing')
    expect(container.textContent).toContain('Record Browser')
    expect(container.textContent).not.toContain('Browser Recorder')

    await clickButton('Checkout Model')
    expect(onStartExistingScript).toHaveBeenCalledWith(expect.objectContaining({ id: 'script-1' }))

    await clickButton('Record Browser')
    expect(actions.openRecorder).toHaveBeenCalled()
    expect(container.textContent).not.toContain('Browser Recorder')

    await clickButton('Import From Source')
    expect(container.textContent).toContain('Contracts & Collections')
    expect(container.textContent).toContain('Salesforce Family')
    expect(container.textContent).toContain('Traffic & Samples')
    expect(container.textContent).toContain('Legacy & Tool Exports')
    expect(container.textContent).toContain('OpenAPI / Swagger')
    expect(container.textContent).toContain('Postman Collection')
    expect(container.textContent).toContain('WSDL / SOAP')
    expect(container.textContent).toContain('Salesforce HAR')
    expect(container.textContent).toContain('RAML / Anypoint')
    expect(container.textContent).toContain('Agentforce Template')
    expect(container.textContent).toContain('HAR Traffic')
    expect(container.textContent).toContain('Request / Response Sample')
    expect(container.textContent).toContain('Import Java over HTTP')
    expect(container.textContent).toContain('Import Enterprise Script Archive')
    expect(container.textContent).toContain('Import JMeter')
    expect(container.textContent).toContain('DevWeb Source')
    expect(container.textContent).not.toContain('Import SOAP Assets')
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('OpenAPI / Swagger'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('Postman Collection'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('WSDL / SOAP'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('Salesforce HAR'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('RAML / Anypoint'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('HAR Traffic'))).toHaveLength(1)
    expect(Array.from(container.querySelectorAll('button')).filter(button => button.textContent?.includes('DevWeb Source'))).toHaveLength(1)

    await clickButton('OpenAPI / Swagger')
    expect(actions.openOpenAPIImport).toHaveBeenCalled()

    await clickButton('Postman Collection')
    expect(actions.openPostmanImport).toHaveBeenCalled()

    await clickButton('Salesforce HAR')
    expect(actions.openSalesforceImport).toHaveBeenCalled()

    await clickButton('RAML / Anypoint')
    expect(actions.openRAMLImport).toHaveBeenCalled()

    await clickButton('Import Enterprise Script Archive')
    expect(actions.openEnterpriseArchiveImport).toHaveBeenCalled()

    await clickButton('Script Library')
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

    expect(container.querySelector('.script-create-existing-table')).not.toBeNull()
    expect(container.textContent).toContain('3 of 3 scripts')
    expect(container.textContent).toContain('PI30')
    expect(container.textContent).toContain('PI31')
    const headers = Array.from(container.querySelectorAll('.script-create-existing-table th')).map(header => header.textContent || '')
    expect(headers.some(header => header.includes('Source'))).toBe(true)
    expect(headers.some(header => header.includes('Script'))).toBe(true)
    expect(headers.some(header => header.includes('Health'))).toBe(true)
    expect(headers.some(header => header.includes('Usage'))).toBe(true)
    expect(headers.some(header => header.includes('Associated Releases'))).toBe(true)
    expect(headers.some(header => header.includes('Create Date'))).toBe(true)
    expect(headers.some(header => header.includes('Last Run'))).toBe(true)
    expect(headers.some(header => header.includes('Actions'))).toBe(true)
    const firstSourceCell = container.querySelector('.script-create-existing-table tbody tr td:first-child')
    expect(firstSourceCell?.textContent?.trim()).toBe('Local')

    const searchInput = container.querySelector('input[aria-label="Search existing scripts"]') as HTMLInputElement | null
    expect(searchInput).not.toBeNull()
    if (!searchInput) return

    await act(async () => {
      setInputValue(searchInput, 'billing')
    })

    expect(container.textContent).toContain('1 of 3 scripts')
    expect(container.textContent).toContain('Billing Flow')
    expect(container.textContent).not.toContain('Checkout Model')

    await clickButton('Reset')

    const protocolSelect = container.querySelector('select[aria-label="Filter existing scripts by protocol"]') as HTMLSelectElement | null
    expect(protocolSelect).not.toBeNull()
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
    expect(usageSort).toBeDefined()
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
          openOpenAPIImport: () => setAuthoringState(createState('openapi-import')),
        }),
        onOpenCreateOptions: () => setAuthoringState(createState('create-options')),
      })
    }

    await act(async () => {
      root.render(<Harness />)
    })

    await clickButton('Import From Source')
    expect(container.textContent).toContain('Contracts & Collections')
    expect(container.textContent).not.toContain('Model From Existing Script')

    await clickButton('OpenAPI / Swagger')
    expect(container.textContent).toContain('subview:openapi-import')

    await clickButton('surface-create-options')

    expect(container.textContent).toContain('Contracts & Collections')
    expect(container.textContent).toContain('OpenAPI / Swagger')
    expect(container.textContent).not.toContain('Model From Existing Script')
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
    await clickButton('Start New Script')

    expect(apiMock.listAuthoringTemplates).toHaveBeenCalledTimes(1)
    expect(apiMock.listAuthoringComponents).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Starter Templates')
    expect(container.textContent).toContain('Blank & Assisted Editors')
    expect(container.textContent).toContain('Reusable Blocks')
    expect(container.querySelectorAll('.script-create-category-tabs [role="tab"]')).toHaveLength(3)
    expect(container.querySelectorAll('.script-create-table-list')).toHaveLength(1)
    expect(container.querySelector('.script-create-choice-groups .script-create-option')).toBeNull()
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
        onImported={onImported}
        onEditDraft={onEditDraft}
        onReturnToDraftSource={onReturnToDraftSource}
        onStartExistingScript={onStartExistingScript}
      >
        {children}
      </ScriptsAuthoringShell>
    )
  }

  async function clickButton(label: string) {
    const button = Array.from(container.querySelectorAll('button')).find(element => element.textContent?.includes(label))
    expect(button).toBeDefined()
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
