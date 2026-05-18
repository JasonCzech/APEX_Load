// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ScriptsPage from './ScriptsPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  listScripts: vi.fn(),
  getScript: vi.fn(),
  deleteScript: vi.fn(),
  listTests: vi.fn(),
  listAuthoringTemplates: vi.fn(),
  listAuthoringSessions: vi.fn(),
  approveAuthoringSession: vi.fn(),
  deleteAuthoringSession: vi.fn(),
  previewAuthoringCorrelation: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
  }),
}))

vi.mock('./ScriptEditor', () => ({
  default: ({ initialScript, scriptId, onSave, onClose }: { initialScript?: { name?: string }, scriptId?: string | null, onSave?: (script: { id: string; name: string }) => void, onClose?: () => void }) => (
    <div data-testid="script-editor">
      <span>editor:{initialScript?.name || 'blank'}</span>
      <span>script-id:{scriptId || 'none'}</span>
      <button onClick={() => onSave?.({ id: 'saved-script-1', name: initialScript?.name || 'Saved Script' })}>mock-save</button>
      <button onClick={onClose}>mock-close</button>
    </div>
  ),
}))

vi.mock('./LoadRunnerImport', () => ({
  default: ({ onImported, onClose }: { onImported?: (result: { scripts: Array<{ id: string; name: string; protocol: string; script?: { name: string; version: string; protocol: string; config: { base_url: string }; actions: unknown[] } }>; scenario?: { name?: string } | null }) => void, onClose?: () => void }) => (
    <div data-testid="loadrunner-import">
      <button onClick={() => onImported?.({
        scripts: [{
          id: 'imported-script-1',
          name: 'Imported LoadRunner Script',
          protocol: 'http',
          script: {
            name: 'Imported LoadRunner Script',
            version: '1.0.0',
            protocol: 'http',
            config: { base_url: 'https://import.example.com' },
            actions: [],
          },
        }],
      })}>mock-loadrunner-save</button>
      <button onClick={onClose}>mock-loadrunner-close</button>
    </div>
  ),
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

vi.mock('./JavaHttpImport', () => ({
  default: ({ onEdit }: { onEdit?: (script: { name: string, version: string, protocol: string, config: { base_url: string }, actions: unknown[] }) => void }) => (
    <div data-testid="java-http-import">
      <button onClick={() => onEdit?.({
        name: 'Java Import Draft',
        version: '1.0.0',
        protocol: 'java_http',
        config: { base_url: 'https://java.example.com' },
        actions: [],
      })}
      >mock-java-import-edit</button>
    </div>
  ),
}))

describe('ScriptsPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.listScripts.mockResolvedValue({
      scripts: [
        {
          id: 'script-1',
          name: 'Existing Script',
          protocol: 'http',
          version: '1.0.0',
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
    apiMock.deleteScript.mockResolvedValue({})
    apiMock.listTests.mockResolvedValue({ tests: [] })
    apiMock.listAuthoringTemplates.mockResolvedValue({ templates: [], count: 0 })
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
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('routes sample import edit handoff through the draft editor and into persisted edit mode after save', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenCalledWith('project-1')

    expect(container.textContent).toContain('Scripts')
    expect(container.textContent).toContain('Existing Script')
    expect(container.textContent).toContain('Needs Review · 72')

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Create Script')

    const sampleButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Build From Sample'))
    expect(sampleButton).toBeDefined()
    if (!sampleButton) {
      return
    }

    await act(async () => {
      sampleButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

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

  it('routes jmeter import edit handoff through the draft editor', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

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
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

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
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const loadRunnerButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import LoadRunner'))
    expect(loadRunnerButton).toBeDefined()
    if (!loadRunnerButton) {
      return
    }

    await act(async () => {
      loadRunnerButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="loadrunner-import"]')).not.toBeNull()

    const importSaveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-loadrunner-save'))
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
    expect(container.textContent).toContain('editor:Imported LoadRunner Script')
    expect(container.textContent).toContain('script-id:imported-script-1')
  })

  it('returns LoadRunner import close actions to the create options shell', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const loadRunnerButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import LoadRunner'))
    expect(loadRunnerButton).toBeDefined()
    if (!loadRunnerButton) {
      return
    }

    await act(async () => {
      loadRunnerButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="loadrunner-import"]')).not.toBeNull()
    expect(container.textContent).toContain('Back to Create Script')

    const closeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('mock-loadrunner-close'))
    expect(closeButton).toBeDefined()
    if (!closeButton) {
      return
    }

    await act(async () => {
      closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="loadrunner-import"]')).toBeNull()
    expect(container.textContent).toContain('Create Script')
    expect(container.textContent).toContain('Import JMeter')
  })

  it('returns JMeter import close actions to the create options shell', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const jmeterButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import JMeter'))
    expect(jmeterButton).toBeDefined()
    if (!jmeterButton) {
      return
    }

    await act(async () => {
      jmeterButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="jmeter-import"]')).not.toBeNull()
    expect(container.textContent).toContain('Back to Create Script')

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
    expect(container.textContent).toContain('Import LoadRunner')
  })

  it('shows SOAP and Java-over-HTTP create options and routes them into the draft editor', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

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

    const createButtonAgain = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButtonAgain).toBeDefined()
    if (!createButtonAgain) {
      return
    }

    await act(async () => {
      createButtonAgain.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

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

    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.listAuthoringTemplates).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Starter Templates')
    expect(container.textContent).toContain('Browser Checkout Canary')

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

  it('surfaces resumable authoring sessions and approves them through the backend gate', async () => {
    apiMock.listAuthoringSessions.mockResolvedValue({
      sessions: [{
        id: 'session-1',
        project_id: 'project-1',
        source: 'loadrunner',
        name: 'Migrated checkout review',
        status: 'ready',
        draft: {
          name: 'Migrated checkout review',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://checkout.example.com' },
          actions: [],
        },
        review_state: {},
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

    expect(apiMock.listAuthoringSessions).toHaveBeenCalledWith('project-1')
    expect(container.textContent).toContain('Authoring Sessions')
    expect(container.textContent).toContain('Migrated checkout review')
    expect(container.textContent).toContain('Loadrunner')

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

    expect(container.textContent).toContain('Guided Correlation Repair')
    expect(container.textContent).toContain('2 actions available')

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

  it('routes the Java-over-HTTP import option into the dedicated Java import surface', async () => {
    await act(async () => {
      root.render(<ScriptsPage />)
    })

    await flushPromises()

    const createButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('+ Create Script'))
    expect(createButton).toBeDefined()
    if (!createButton) {
      return
    }

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const javaImportButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Import Java over HTTP'))
    expect(javaImportButton).toBeDefined()
    if (!javaImportButton) {
      return
    }

    await act(async () => {
      javaImportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="java-http-import"]')).not.toBeNull()
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
