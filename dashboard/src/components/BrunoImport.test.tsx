// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import { type Folder } from '../types'
import BrunoImport from './BrunoImport'
import { brunoDestinationFolderOptions } from './brunoFolderOptions'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getBrunoSupportMatrix: vi.fn(),
  listScripts: vi.fn(),
  listFolders: vi.fn(),
  inspectBrunoImport: vi.fn(),
  commitBrunoImport: vi.fn(),
  listBrunoRevisions: vi.fn(),
  previewBrunoProjection: vi.fn(),
  rollbackBrunoRevision: vi.fn(),
  commitBrunoProjection: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
  applyImportParameterization: vi.fn(),
  getScriptSecretStatus: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function inspectionFixture() {
  return {
    import_session_id: 'session-1',
    expires_at: '2099-07-16T20:00:00Z',
    parameterization: {
      suggestions: [{
        id: 'pz_bruno_tenant',
        kind: 'data_param',
        var_name: 'TENANT',
        replacement: '{{param.TENANT}}',
        sample_value: 'acme',
        confidence: 0.95,
        rule_id: 'test.bruno.tenant',
        rationale: 'Repeated tenant selector',
        auto_apply: true,
        high_risk: false,
        locations: [{ section: 'actions', action_index: 0, action_name: 'Run SOAP request', field: 'target' }],
      }],
      summary: {
        total: 1,
        by_kind: { data_param: 1 },
        auto_apply_count: 1,
        review_count: 0,
        high_risk_count: 0,
        pre_applied_count: 0,
      },
    },
    result: {
      schema_version: 'apexload.bruno-import.v1',
      output_mode: 'canonical' as const,
      source_format: 'yml',
      root_path: '',
      manifest: {
        version: '1',
        sha256: 'manifest',
        bundle_sha256: 'bundle',
        total_bytes: 4096,
        files: [{ path: 'request.yml', role: 'request', size_bytes: 4096, sha256: 'file', mode: 420 }],
      },
      collection: {
        name: 'SOAP request',
        items: [{
          uid: 'request-1',
          name: 'Execute policy check',
          path: 'request.yml',
          type: 'http',
          executable: true,
          has_scripts: true,
          has_tests: true,
        }],
      },
      environments: [],
      capabilities: {
        request_types: ['http'],
        has_javascript: true,
        has_npm_dependencies: false,
        has_local_modules: false,
        has_file_bodies: false,
        has_certificates: false,
        has_bruno_apps: false,
        required_sandbox: 'safe',
      },
      secret_requirements: [{
        id: 'request.yml:http.auth.password',
        path: 'request.yml',
        key: 'password',
        suggested_name: 'PASSWORD',
        replacement: '{{secret.PASSWORD}}',
        context: 'http.auth.password',
        source_value_available: true,
      }],
      security_findings: [{
        severity: 'warning',
        code: 'source_credentials_sanitized',
        summary: 'One credential was removed from the executable bundle.',
      }],
      fidelity: {
        status: 'lossless',
        preserved_files: 1,
        executable_items: 1,
        preserved_apps: 0,
        dropped_items: 0,
        compatibility: '3.5.2',
      },
      native_projection: { eligible: false, reasons: ['JavaScript tests require canonical execution'] },
      missing_variables: ['env'],
      script: {
        name: 'SOAP request',
        version: '1.0',
        protocol: 'bruno',
        project_id: 'project-1',
        config: { base_url: '', bruno: { sandbox: 'safe' } },
        actions: [{ name: 'Run SOAP request', type: 'bruno_run', target: '' }],
      },
      inspected_at: '2026-07-16T19:00:00Z',
    },
    artifacts: [
      { id: 'raw-1', artifact_kind: 'raw', sha256: 'raw', size_bytes: 4096 },
      { id: 'sanitized-1', artifact_kind: 'sanitized', sha256: 'sanitized', size_bytes: 4000 },
    ],
  }
}

function nativeInspectionFixture() {
  const base = inspectionFixture()
  return {
    ...base,
    parameterization: null,
    result: {
      ...base.result,
      output_mode: 'native_requests' as const,
      source_format: 'open_collection_yaml',
      secret_requirements: [],
      security_findings: [],
      missing_variables: [],
      script: undefined,
      scripts: [
        {
          candidate_id: 'candidate-1',
          planned_script_id: 'planned-1',
          source_path: 'CTA/first.yml',
          source_uid: 'request-1',
          sequence: 10,
          method: 'POST',
          target: 'https://{{var.env}}/first',
          source_mappings: [
            { path: 'CTA/folder.yml', role: 'folder', sha256: 'folder-sha' },
            { path: 'CTA/first.yml', role: 'request', sha256: 'request-sha' },
          ],
          script: {
            name: 'First API',
            version: '1.0',
            protocol: 'http',
            project_id: 'project-1',
            config: { base_url: '', http: { traceparent: false } },
            actions: [{ name: 'First API', type: 'http', method: 'POST', target: 'https://{{var.env}}/first' }],
          },
          diagnostics: [],
          missing_variables: ['env'],
        },
        {
          candidate_id: 'candidate-2',
          planned_script_id: 'planned-2',
          source_path: 'CTA/second.yml',
          source_uid: 'request-2',
          sequence: 20,
          method: 'POST',
          target: 'https://{{var.env}}/second',
          script: {
            name: 'Second API',
            version: '1.0',
            protocol: 'http',
            project_id: 'project-1',
            config: { base_url: '', http: { traceparent: false } },
            actions: [{ name: 'Second API', type: 'http', method: 'POST', target: 'https://{{var.env}}/second' }],
          },
          diagnostics: [{
            id: 'finding-js-1',
            code: 'bruno_javascript_review_required',
            severity: 'warning',
            message: 'Translate unsupported response JavaScript.',
            acknowledgement_required: true,
          }],
        },
      ],
      fidelity: {
        ...base.result.fidelity,
        executable_items: 2,
      },
    },
  }
}

describe('BrunoImport', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getBrunoSupportMatrix.mockResolvedValue({
      compatibility_version: '3.5.2',
      import_enabled: true,
      safe_execution_enabled: true,
      developer_execution_enabled: false,
      native_projection_enabled: false,
      secret_store: {
        provider: 'encrypted_database',
        writable: true,
        fallback: 'binding_only',
        source_secret_migration_policy_enabled: true,
      },
    })
    apiMock.listScripts.mockResolvedValue({ scripts: [], count: 0 })
    apiMock.listFolders.mockResolvedValue({
      folders: [{
        id: 'folder-1',
        project_id: 'project-1',
        parent_id: null,
        name: 'Imported APIs',
        category: 'scripts',
        created_at: '2026-07-16T19:00:00Z',
        updated_at: '2026-07-16T19:00:00Z',
      }],
      count: 1,
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
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 1 })
    apiMock.migrateImportSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.applyImportParameterization.mockResolvedValue({
      script: {
        ...inspectionFixture().result.script,
        actions: [{ name: 'Run SOAP request', type: 'bruno_run', target: '{{param.TENANT}}' }],
      },
      diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'pz_bruno_tenant' }],
      applied_count: 1,
    })
    apiMock.getScriptSecretStatus.mockResolvedValue({
      script_id: 'script-1',
      required: ['PASSWORD'],
      configured: ['PASSWORD'],
      missing: [],
    })
    apiMock.inspectBrunoImport.mockResolvedValue(inspectionFixture())
    apiMock.commitBrunoImport.mockResolvedValue({
      created: true,
      script: {
        id: 'script-1',
        ref_key: 'SCR-1',
        name: 'SOAP request',
        version: '1.0',
        protocol: 'bruno',
        project_id: 'project-1',
        created_at: '2026-07-16T19:00:00Z',
        config: { base_url: '', bruno: { source_revision: 'revision-1' } },
        actions: [{ name: 'Run SOAP request', type: 'bruno_run', target: '' }],
      },
      revision: {
        id: 'revision-1',
        script_id: 'script-1',
        project_id: 'project-1',
        raw_artifact_id: 'raw-1',
        sanitized_artifact_id: 'sanitized-1',
        source_format: 'yml',
        compatibility_version: '3.5.2',
        active: true,
        created_at: '2026-07-16T19:00:00Z',
      },
    })
    apiMock.listBrunoRevisions.mockResolvedValue({
      revisions: [{
        id: 'revision-1',
        script_id: 'script-1',
        project_id: 'project-1',
        raw_artifact_id: 'raw-1',
        sanitized_artifact_id: 'sanitized-1',
        source_format: 'yml',
        compatibility_version: '3.5.2',
        active: true,
        created_at: '2026-07-16T19:00:00Z',
      }],
      count: 1,
    })
    apiMock.previewBrunoProjection.mockResolvedValue({
      script_id: 'script-1',
      source_revision_id: 'revision-1',
      proof_status: 'not_run',
      eligible: false,
    })
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.clearAllMocks()
  })

  it('inspects, binds, and commits a standalone Bruno YAML request without opening the HTTP editor', async () => {
    const file = new File(['meta { name: SOAP request }\nget { url: https://example.test }\n'], 'collection.bru', { type: 'text/plain' })
    const onImported = vi.fn()
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" onImported={onImported} />)
    })
    await act(async () => { await Promise.resolve() })

    const inspectButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })

    expect(apiMock.inspectBrunoImport).toHaveBeenCalledWith([file], expect.objectContaining({
      project_id: 'project-1',
      sandbox: 'safe',
      run_options: expect.objectContaining({ selection: 'collection', recursive: true }),
    }))
    expect(container.textContent).toContain('Execute policy check')
    expect(container.textContent).toContain('source_credentials_sanitized')
    expect(container.textContent).toContain('Preflight requirements')
    expect(container.textContent).toContain('lossless')
    const importHeader = container.querySelector('.bruno-import-header')
    expect(importHeader?.nextElementSibling?.classList.contains('credentials-card')).toBe(true)
    expect(container.textContent).not.toContain('Secret migration and bindings')

    const commitButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Import Bruno collection'))
    expect(commitButton?.textContent).toContain('with 1 secret to configure')
    expect(commitButton?.disabled).toBe(false)

    const bindingInput = Array.from(container.querySelectorAll('input')).find(input => input.value === 'PASSWORD')
    expect(bindingInput).toBeDefined()
    act(() => {
      setInput(bindingInput!, 'INVALID/NAME')
    })
    expect(bindingInput?.getAttribute('aria-invalid')).toBe('true')
    expect(commitButton?.disabled).toBe(true)
    await act(async () => commitButton?.click())
    expect(apiMock.commitBrunoImport).not.toHaveBeenCalled()
    act(() => setInput(bindingInput!, 'PASSWORD'))
    expect(commitButton?.disabled).toBe(false)

    const migrationToggle = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.parentElement?.textContent?.includes('Import detected credential values'))
    expect(migrationToggle?.checked).toBe(false)
    await act(async () => migrationToggle?.click())
    expect(migrationToggle?.checked).toBe(true)
    expect(commitButton?.textContent).not.toContain('to configure')

    const optionalValue = container.querySelector<HTMLInputElement>('input[type="password"]')
    expect(optionalValue).not.toBeNull()
    act(() => setInput(optionalValue!, 'manual-secret-value'))

    await act(async () => {
      commitButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.commitBrunoImport).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-1',
      import_session_id: 'session-1',
      mode: 'create',
      migrate_secrets: true,
      secret_bindings: expect.objectContaining({ 'request.yml:http.auth.password': 'PASSWORD' }),
    }))
    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'PASSWORD',
      value: 'manual-secret-value',
    }])
    expect(apiMock.migrateImportSecrets).not.toHaveBeenCalled()
    expect(container.textContent).not.toContain('manual-secret-value')
    expect(container.textContent).toContain('Canonical source active')
    expect(container.querySelector('.credentials-card')).toBeNull()
    expect(container.querySelector('input[type="password"]')).toBeNull()
    expect(Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .some(input => input.parentElement?.textContent?.includes('Import detected credential values'))).toBe(false)
    expect(onImported).not.toHaveBeenCalled()

    const openButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open imported script'))
    await act(async () => {
      openButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'script-1', protocol: 'bruno' })],
    }))
  })

  it('preselects native mode for OpenCollection YAML and saves an acknowledged per-request batch', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const onImported = vi.fn()
    apiMock.inspectBrunoImport.mockResolvedValueOnce(nativeInspectionFixture())
    apiMock.commitBrunoImport.mockResolvedValueOnce({
      batch_id: 'batch-1',
      created: true,
      count: 2,
      idempotent_replay: false,
      _http_status: 201,
      scripts: nativeInspectionFixture().result.scripts.map((candidate, index) => ({
        ...candidate.script,
        id: candidate.planned_script_id,
        ref_key: `SCR-${index + 1}`,
        folder_id: 'folder-1',
        created_at: '2026-07-16T19:00:00Z',
      })),
    })

    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" onImported={onImported} />)
      await Promise.resolve()
    })

    const mode = Array.from(container.querySelectorAll<HTMLInputElement>('input[name="bruno-output-mode"]'))
      .find(input => input.parentElement?.textContent?.includes('Native scripts per API'))
    expect(mode?.checked).toBe(true)
    expect(container.textContent).not.toContain('Synchronization target')

    const folder = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))
    await act(async () => {
      if (folder) {
        folder.value = 'folder-1'
        folder.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.inspectBrunoImport).toHaveBeenCalledWith([file], expect.objectContaining({
      project_id: 'project-1',
      output_mode: 'native_requests',
      run_options: undefined,
    }))
    expect(container.textContent).toContain('2 API requests found → 2 native scripts')
    expect(container.textContent).toContain('Execution blocked')
    expect(container.textContent).toContain('Review required')
    expect(container.textContent?.indexOf('CTA/first.yml')).toBeLessThan(container.textContent?.indexOf('CTA/second.yml'))
    const nameInput = container.querySelector<HTMLInputElement>('input[aria-label="Name for CTA/second.yml"]')
    expect(nameInput).not.toBeNull()
    act(() => setInput(nameInput!, ''))

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 2 native scripts'))
    expect(saveButton?.disabled).toBe(true)
    const acknowledgement = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.parentElement?.textContent?.includes('finding-js-1'))
    await act(async () => acknowledgement?.click())
    expect(saveButton?.disabled).toBe(true)

    act(() => setInput(nameInput!, 'Renamed Second API'))
    expect(saveButton?.disabled).toBe(false)

    await act(async () => {
      saveButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.commitBrunoImport).toHaveBeenCalledWith({
      project_id: 'project-1',
      import_session_id: 'session-1',
      mode: 'create_native_batch',
      folder_id: 'folder-1',
      candidate_ids: ['candidate-1', 'candidate-2'],
      name_overrides: {
        'candidate-1': 'First API',
        'candidate-2': 'Renamed Second API',
      },
      migrate_secrets: false,
      secret_bindings: {},
      acknowledged_finding_ids: ['finding-js-1'],
    })
    expect(onImported).toHaveBeenCalledWith({
      scripts: [
        expect.objectContaining({ id: 'planned-1', protocol: 'http' }),
        expect.objectContaining({ id: 'planned-2', protocol: 'http' }),
      ],
    })
    expect(container.textContent).toContain('Native batch saved')
  })

  it('detects native mode from standalone OpenCollection request content', async () => {
    const file = new File([
      'info:\n  name: Routed request\n  type: http\nhttp:\n  method: POST\n',
    ], 'request.yml', { type: 'application/yaml' })
    await act(async () => {
      root.render(
        <BrunoImport
          initialFiles={[file]}
          projectId="project-1"
        />,
      )
      await Promise.resolve()
      await new Promise(resolve => setTimeout(resolve, 0))
    })

    const nativeMode = Array.from(container.querySelectorAll<HTMLInputElement>('input[name="bruno-output-mode"]'))
      .find(input => input.parentElement?.textContent?.includes('Native scripts per API'))
    expect(nativeMode?.checked).toBe(true)
    expect(container.textContent).toContain('Destination folder')
    expect(container.textContent).not.toContain('Synchronization target')
  })

  it('blocks inspection until asynchronous content mode detection resolves', async () => {
    let resolveText!: (value: string) => void
    const pendingText = new Promise<string>(resolve => { resolveText = resolve })
    const request = new File(['pending'], 'request.yml', { type: 'application/yaml' })
    vi.spyOn(request, 'slice').mockReturnValue({ text: () => pendingText } as Blob)

    await act(async () => {
      root.render(<BrunoImport initialFiles={[request]} projectId="project-1" />)
      await Promise.resolve()
    })
    const detectingButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Detecting source type'))
    expect(detectingButton?.disabled).toBe(true)
    await act(async () => detectingButton?.click())
    expect(apiMock.inspectBrunoImport).not.toHaveBeenCalled()

    await act(async () => {
      resolveText('info:\n  name: Native request\n  type: http\n')
      await pendingText
      await Promise.resolve()
    })
    const nativeMode = Array.from(container.querySelectorAll<HTMLInputElement>('input[name="bruno-output-mode"]'))
      .find(input => input.parentElement?.textContent?.includes('Native scripts per API'))
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Inspect source')
    expect(nativeMode?.checked).toBe(true)
    expect(inspectButton?.disabled).toBe(false)
  })

  it('manual mode selection cancels pending content detection', async () => {
    let resolveText!: (value: string) => void
    const pendingText = new Promise<string>(resolve => { resolveText = resolve })
    const request = new File(['pending'], 'request.yml', { type: 'application/yaml' })
    vi.spyOn(request, 'slice').mockReturnValue({ text: () => pendingText } as Blob)

    await act(async () => {
      root.render(<BrunoImport initialFiles={[request]} projectId="project-1" />)
      await Promise.resolve()
    })
    const nativeMode = Array.from(container.querySelectorAll<HTMLInputElement>('input[name="bruno-output-mode"]'))
      .find(input => input.parentElement?.textContent?.includes('Native scripts per API'))
    await act(async () => nativeMode?.click())
    expect(nativeMode?.checked).toBe(true)

    await act(async () => {
      resolveText('title: Not Bruno\n')
      await pendingText
      await Promise.resolve()
    })
    expect(nativeMode?.checked).toBe(true)
    expect(container.textContent).not.toContain('Detecting source type')
  })

  it('clicking the already-selected canonical mode cancels pending content detection', async () => {
    let resolveText!: (value: string) => void
    const pendingText = new Promise<string>(resolve => { resolveText = resolve })
    const request = new File(['pending'], 'request.yml', { type: 'application/yaml' })
    vi.spyOn(request, 'slice').mockReturnValue({ text: () => pendingText } as Blob)

    await act(async () => {
      root.render(<BrunoImport initialFiles={[request]} projectId="project-1" />)
      await Promise.resolve()
    })
    const canonicalMode = Array.from(container.querySelectorAll<HTMLInputElement>('input[name="bruno-output-mode"]'))
      .find(input => input.parentElement?.textContent?.includes('Canonical Bruno collection'))
    expect(canonicalMode?.checked).toBe(true)
    await act(async () => canonicalMode?.click())
    expect(container.textContent).not.toContain('Detecting source type')

    await act(async () => {
      resolveText('info:\n  name: Native request\n  type: http\n')
      await pendingText
      await Promise.resolve()
    })
    expect(canonicalMode?.checked).toBe(true)
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Inspect source')
    expect(inspectButton?.disabled).toBe(false)
  })

  it('clearing files cancels pending content detection', async () => {
    let resolveText!: (value: string) => void
    const pendingText = new Promise<string>(resolve => { resolveText = resolve })
    const request = new File(['pending'], 'request.yml', { type: 'application/yaml' })
    vi.spyOn(request, 'slice').mockReturnValue({ text: () => pendingText } as Blob)

    await act(async () => {
      root.render(<BrunoImport initialFiles={[request]} projectId="project-1" />)
      await Promise.resolve()
    })
    const clearButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Clear')
    await act(async () => clearButton?.click())

    await act(async () => {
      resolveText('info:\n  name: Stale request\n  type: http\n')
      await pendingText
      await Promise.resolve()
    })
    expect(container.textContent).not.toContain('request.yml')
    expect(container.textContent).not.toContain('Detecting source type')
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Inspect source')
    expect(inspectButton?.disabled).toBe(true)
  })

  it('ignores stale asynchronous file detection after a newer selection', async () => {
    let resolveSlowText!: (value: string) => void
    const slowText = new Promise<string>(resolve => { resolveSlowText = resolve })
    const slowRequest = new File(['pending'], 'slow.yml', { type: 'application/yaml' })
    vi.spyOn(slowRequest, 'slice').mockReturnValue({ text: () => slowText } as Blob)
    const latestClassic = new File(['meta { name: Latest }'], 'latest.bru', { type: 'text/plain' })

    await act(async () => {
      root.render(<BrunoImport projectId="project-1" />)
      await Promise.resolve()
    })
    const chooser = container.querySelector<HTMLInputElement>('input[type="file"][accept]')
    expect(chooser).not.toBeNull()

    await act(async () => {
      Object.defineProperty(chooser!, 'files', { configurable: true, value: [slowRequest] })
      chooser!.dispatchEvent(new Event('change', { bubbles: true }))
      Object.defineProperty(chooser!, 'files', { configurable: true, value: [latestClassic] })
      chooser!.dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
    })
    await act(async () => {
      resolveSlowText('info:\n  name: Stale\n  type: http\n')
      await slowText
      await Promise.resolve()
    })

    const canonicalMode = Array.from(container.querySelectorAll<HTMLInputElement>('input[name="bruno-output-mode"]'))
      .find(input => input.parentElement?.textContent?.includes('Canonical Bruno collection'))
    expect(canonicalMode?.checked).toBe(true)
    expect(container.textContent).toContain('latest.bru')
    expect(container.textContent).not.toContain('slow.yml')
  })

  it('renders ancestry and stable identities for duplicate destination folders', () => {
    const folder = (id: string, name: string, parentID: string | null): Folder => ({
      id,
      project_id: 'project-1',
      parent_id: parentID,
      name,
      category: 'scripts',
      created_at: '2026-07-16T19:00:00Z',
      updated_at: '2026-07-16T19:00:00Z',
    })
    const options = brunoDestinationFolderOptions([
      folder('root-a', 'Team A', null),
      folder('root-b', 'Team B', null),
      folder('imports-a', 'Imported APIs', 'root-a'),
      folder('imports-b', 'Imported APIs', 'root-b'),
      folder('duplicate-1', 'Same', 'root-a'),
      folder('duplicate-2', 'Same', 'root-a'),
      folder('orphan', 'Orphan', 'missing'),
    ])
    const labels = new Map(options.map(option => [option.folder.id, option.label]))

    expect(labels.get('imports-a')).toBe('Team A / Imported APIs')
    expect(labels.get('imports-b')).toBe('Team B / Imported APIs')
    expect(labels.get('duplicate-1')).toBe('Team A / Same · duplicate-1')
    expect(labels.get('duplicate-2')).toBe('Team A / Same · duplicate-2')
    expect(labels.get('orphan')).toBe('[missing parent: missing] / Orphan')
  })

  it('does not navigate to a native script when a commit finishes after Back', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    const candidate = inspection.result.scripts[0]
    inspection.result.scripts = [candidate]
    inspection.result.fidelity.executable_items = 1
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    const committedResponse = {
      batch_id: 'batch-late',
      created: true,
      count: 1,
      idempotent_replay: false,
      _http_status: 201,
      scripts: [{
        ...candidate.script,
        id: candidate.planned_script_id,
        ref_key: 'SCR-LATE',
        folder_id: 'folder-1',
        created_at: '2026-07-16T19:00:00Z',
      }],
    }
    let resolveCommit!: (response: typeof committedResponse) => void
    const pendingCommit = new Promise<typeof committedResponse>(resolve => {
      resolveCommit = resolve
    })
    apiMock.commitBrunoImport.mockImplementationOnce(() => pendingCommit)
    const onImported = vi.fn()
    const onClose = vi.fn()

    await act(async () => {
      root.render(
        <BrunoImport
          initialFiles={[file]}
          projectId="project-1"
          onImported={onImported}
          onClose={onClose}
        />,
      )
      await Promise.resolve()
    })
    const folder = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))
    await act(async () => {
      if (folder) {
        folder.value = 'folder-1'
        folder.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 1 native script'))
    await act(async () => {
      saveButton?.click()
      await Promise.resolve()
    })
    expect(apiMock.commitBrunoImport).toHaveBeenCalledTimes(1)

    const backButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Back')
    await act(async () => backButton?.click())
    expect(onClose).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveCommit(committedResponse)
      await pendingCommit
      await Promise.resolve()
    })
    expect(onImported).not.toHaveBeenCalled()
    expect(container.textContent).not.toContain('Native batch saved')
  })

  it('retains a native inspection and retries the same session after partial secret migration', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    const candidate = inspection.result.scripts[0]
    inspection.result.scripts = [candidate]
    inspection.result.fidelity.executable_items = 1
    ;(candidate as typeof candidate & { secret_requirements: unknown[] }).secret_requirements = [{
      id: 'native-password',
      path: 'CTA/first.yml:http.auth.password',
      key: 'password',
      suggested_name: 'PASSWORD',
      context: 'Bruno HTTP auth password',
      replacement: '{{secret.PASSWORD}}',
      source_value_available: true,
    }]
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    apiMock.commitBrunoImport
      .mockRejectedValueOnce(new ApiError('credential provider failed after one write', 503, {
        partial: true,
        import_session_consumed: false,
        manual_configuration_required: true,
        committed_names: ['PASSWORD'],
        remaining_names: ['SECOND_TOKEN'],
      }))
      .mockResolvedValueOnce({
        batch_id: 'batch-retry',
        created: true,
        count: 1,
        idempotent_replay: false,
        _http_status: 201,
        scripts: [{
          ...candidate.script,
          id: candidate.planned_script_id,
          ref_key: 'SCR-RETRY',
          folder_id: 'folder-1',
          created_at: '2026-07-16T19:00:00Z',
        }],
      })

    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
    })

    const folder = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))
    await act(async () => {
      if (folder) {
        folder.value = 'folder-1'
        folder.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    const migrationToggle = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.parentElement?.textContent?.includes('Import detected credential values'))
    expect(migrationToggle).toBeDefined()
    await act(async () => migrationToggle?.click())

    const firstSave = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 1 native script'))
    expect(firstSave?.disabled).toBe(false)
    await act(async () => {
      firstSave?.click()
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('protected import session was retained')
    expect(container.textContent).toContain('Retained credentials: PASSWORD')
    expect(container.textContent).toContain('CTA/first.yml')
    const retrySave = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 1 native script'))
    expect(retrySave?.disabled).toBe(false)

    await act(async () => {
      retrySave?.click()
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.commitBrunoImport).toHaveBeenCalledTimes(2)
    const [firstPayload] = apiMock.commitBrunoImport.mock.calls[0]
    const [retryPayload] = apiMock.commitBrunoImport.mock.calls[1]
    expect(firstPayload).toEqual(expect.objectContaining({
      mode: 'create_native_batch',
      import_session_id: 'session-1',
      migrate_secrets: true,
    }))
    expect(retryPayload).toEqual(firstPayload)
    expect(container.textContent).toContain('Native batch saved')
  })

  it('rejects an incomplete native batch receipt without clearing the inspection', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    inspection.result.scripts = [inspection.result.scripts[0]]
    inspection.result.fidelity.executable_items = 1
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    apiMock.commitBrunoImport.mockResolvedValueOnce({
      batch_id: 'batch-incomplete',
      created: true,
      idempotent_replay: false,
      _http_status: 201,
      count: 0,
      scripts: [],
    })
    const onImported = vi.fn()

    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" onImported={onImported} />)
      await Promise.resolve()
    })
    const folder = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))
    await act(async () => {
      if (folder) {
        folder.value = 'folder-1'
        folder.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 1 native script'))
    await act(async () => {
      saveButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('returned 0 of 1 selected scripts')
    expect(container.textContent).toContain('CTA/first.yml')
    expect(onImported).not.toHaveBeenCalled()
  })

  it.each([
    {
      name: 'missing replay marker',
      mutate: (receipt: Record<string, unknown>) => { delete receipt.idempotent_replay },
      message: 'must include boolean created and idempotent_replay',
    },
    {
      name: 'both outcomes true',
      mutate: (receipt: Record<string, unknown>) => { receipt.idempotent_replay = true },
      message: 'exactly one of creation or idempotent replay',
    },
    {
      name: 'created outcome on HTTP 200',
      mutate: (receipt: Record<string, unknown>) => { receipt._http_status = 200 },
      message: 'HTTP 200 response must identify an idempotent replay',
    },
    {
      name: 'replay outcome on HTTP 201',
      mutate: (receipt: Record<string, unknown>) => {
        receipt.created = false
        receipt.idempotent_replay = true
      },
      message: 'HTTP 201 response must identify a newly created batch',
    },
  ])('rejects a native receipt with $name', async ({ mutate, message }) => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    const candidate = inspection.result.scripts[0]
    inspection.result.scripts = [candidate]
    inspection.result.fidelity.executable_items = 1
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    const receipt: Record<string, unknown> = {
      batch_id: 'batch-invalid',
      created: true,
      idempotent_replay: false,
      _http_status: 201,
      count: 1,
      scripts: [{
        ...candidate.script,
        id: candidate.planned_script_id,
        folder_id: 'folder-1',
      }],
    }
    mutate(receipt)
    apiMock.commitBrunoImport.mockResolvedValueOnce(receipt)
    const onImported = vi.fn()

    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" onImported={onImported} />)
      await Promise.resolve()
    })
    const folder = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))
    await act(async () => {
      if (folder) {
        folder.value = 'folder-1'
        folder.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 1 native script'))
    await act(async () => {
      saveButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain(message)
    expect(onImported).not.toHaveBeenCalled()
    expect(container.textContent).toContain('CTA/first.yml')
  })

  it.each([
    {
      name: 'missing planned identity',
      mutate: (inspection: ReturnType<typeof nativeInspectionFixture>) => {
        inspection.result.scripts[0].planned_script_id = ''
      },
      message: 'missing planned_script_id',
    },
    {
      name: 'duplicate planned identity',
      mutate: (inspection: ReturnType<typeof nativeInspectionFixture>) => {
        inspection.result.scripts[1].planned_script_id = inspection.result.scripts[0].planned_script_id
      },
      message: 'duplicate planned_script_id',
    },
  ])('blocks commit for a native inspection with $name', async ({ mutate, message }) => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    mutate(inspection)
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)

    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
    })
    const folder = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))
    await act(async () => {
      if (folder) {
        folder.value = 'folder-1'
        folder.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain(message)
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 2 native scripts'))
    expect(saveButton?.disabled).toBe(true)
    await act(async () => saveButton?.click())
    expect(apiMock.commitBrunoImport).not.toHaveBeenCalled()
  })

  it('surfaces destination-folder loading errors and retries them', async () => {
    apiMock.listFolders.mockRejectedValueOnce(new Error('folder service unavailable'))
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('folder service unavailable')
    const retry = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Retry')
    await act(async () => {
      retry?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.textContent).not.toContain('folder service unavailable')
    expect(container.textContent).toContain('Imported APIs')
  })

  it('persists selected parameterization and offers deferred secret configuration after commit', async () => {
    const file = new File(['info: {name: Deferred, type: http}'], 'deferred.yml')
    const onConfigureSecrets = vi.fn()
    apiMock.getScriptSecretStatus.mockResolvedValueOnce({
      script_id: 'script-1',
      required: ['PASSWORD'],
      configured: [],
      missing: ['PASSWORD'],
    })
    await act(async () => {
      root.render(
        <BrunoImport
          initialFiles={[file]}
          projectId="project-1"
          onConfigureSecrets={onConfigureSecrets}
        />,
      )
      await Promise.resolve()
    })

    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Parameterization Review (1)')
    const applyButton = container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')
    await act(async () => {
      applyButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(apiMock.applyImportParameterization).toHaveBeenCalledWith(expect.objectContaining({
      selected_ids: ['pz_bruno_tenant'],
    }))

    const commitButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Import Bruno collection'))
    expect(commitButton?.textContent).toContain('with 1 secret to configure')
    await act(async () => {
      commitButton?.click()
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.commitBrunoImport).toHaveBeenCalledWith(expect.objectContaining({
      parameterization_selected_ids: ['pz_bruno_tenant'],
      migrate_secrets: false,
    }))
    expect(container.textContent).toContain('1 project secret still needs configuration.')
    const configureButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Configure secrets')
    expect(configureButton).toBeDefined()
    await act(async () => configureButton?.click())
    expect(onConfigureSecrets).toHaveBeenCalledWith('project-1')
  })

  it('ignores an inspection response after the selected source changes', async () => {
    const firstFile = new File(['info: {name: First, type: http}'], 'first.yml')
    const secondFile = new File(['info: {name: Second, type: http}'], 'second.yml')
    let resolveInspection!: (response: ReturnType<typeof inspectionFixture>) => void
    const pendingInspection = new Promise<ReturnType<typeof inspectionFixture>>(resolve => {
      resolveInspection = resolve
    })
    apiMock.inspectBrunoImport.mockImplementationOnce(() => pendingInspection)

    await act(async () => {
      root.render(<BrunoImport initialFiles={[firstFile]} projectId="project-1" />)
    })
    await act(async () => { await Promise.resolve() })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => { inspectButton?.click() })
    expect(apiMock.inspectBrunoImport).toHaveBeenCalledWith([firstFile], expect.any(Object))

    await act(async () => {
      root.render(<BrunoImport initialFiles={[secondFile]} projectId="project-1" />)
      await Promise.resolve()
    })
    await act(async () => {
      resolveInspection(inspectionFixture())
      await pendingInspection
    })
    await act(async () => { await Promise.resolve() })

    expect(container.textContent).toContain('second.yml')
    expect(container.textContent).not.toContain('Execute policy check')
    const currentInspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    expect(currentInspectButton?.disabled).toBe(false)
  })

  it('does not commit an inspected source after its credential probe is superseded', async () => {
    const firstFile = new File(['info: {name: First, type: http}'], 'first.yml')
    const secondFile = new File(['info: {name: Second, type: http}'], 'second.yml')
    await act(async () => {
      root.render(<BrunoImport initialFiles={[firstFile]} projectId="project-1" />)
    })
    await act(async () => { await Promise.resolve() })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => { inspectButton?.click() })
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })

    const commitButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Import Bruno collection'))
    expect(commitButton?.disabled).toBe(false)

    const statusResponse = {
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: true,
      },
      secrets: [],
    }
    let resolveStatus!: (response: typeof statusResponse) => void
    const pendingStatus = new Promise<typeof statusResponse>(resolve => {
      resolveStatus = resolve
    })
    apiMock.getProjectSecretStatuses.mockImplementationOnce(() => pendingStatus)

    await act(async () => { commitButton?.click() })
    await act(async () => { await Promise.resolve() })

    await act(async () => {
      root.render(<BrunoImport initialFiles={[secondFile]} projectId="project-1" />)
      await Promise.resolve()
    })
    await act(async () => {
      resolveStatus(statusResponse)
      await pendingStatus
    })
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.commitBrunoImport).not.toHaveBeenCalled()
    expect(container.textContent).toContain('second.yml')
    const currentInspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    expect(currentInspectButton?.disabled).toBe(false)
  })

  it('surfaces safe partial-migration metadata and discards a consumed Bruno import session', async () => {
    apiMock.commitBrunoImport.mockRejectedValueOnce(new ApiError('provider write failed', 502, {
      partial: true,
      import_session_consumed: true,
      manual_configuration_required: true,
      remaining_names: ['SECOND_TOKEN'],
      migrated: [{ name: 'PASSWORD', provider: 'encrypted_database' }],
    }))
    const file = new File(['info: {name: Partial, type: http}'], 'partial.yml')
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
    })
    await act(async () => { await Promise.resolve() })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    const migrationToggle = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.parentElement?.textContent?.includes('Import detected credential values'))
    await act(async () => migrationToggle?.click())
    const commitButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Import Bruno collection'))
    await act(async () => {
      commitButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.commitBrunoImport).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Remaining credentials: SECOND_TOKEN')
    expect(container.textContent).toContain('Re-inspect the Bruno source')
    expect(container.textContent).not.toContain('PASSWORD was created')
    expect(container.querySelector('.credentials-card')).toBeNull()
    const freshInspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    expect(freshInspectButton?.disabled).toBe(false)
  })

  it('shows a governed preflight block when developer execution is unavailable', async () => {
    const file = new File(['info: {name: Modules, type: http}'], 'request.yml')
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
    })
    await act(async () => { await Promise.resolve() })

    const sandbox = Array.from(container.querySelectorAll('select')).find(select => select.options[0]?.textContent?.includes('Safe'))
    await act(async () => {
      if (sandbox) {
        sandbox.value = 'developer'
        sandbox.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    expect(container.textContent).toContain('Developer execution is disabled')
    expect(container.textContent).toContain('Import remains available')
  })

  it('disables inspection when an operator disables Bruno import', async () => {
    apiMock.getBrunoSupportMatrix.mockResolvedValueOnce({
      compatibility_version: '3.5.2',
      import_enabled: false,
      safe_execution_enabled: true,
      developer_execution_enabled: false,
      native_projection_enabled: false,
      secret_store: {
        provider: 'encrypted_database',
        writable: true,
        fallback: 'binding_only',
        source_secret_migration_policy_enabled: true,
      },
    })
    const file = new File(['info: {name: Disabled, type: http}'], 'disabled.yml')
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
      await Promise.resolve()
    })

    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    expect(inspectButton?.disabled).toBe(true)
    expect(container.textContent).toContain('Bruno import is disabled by an operator.')
    await act(async () => inspectButton?.click())
    expect(apiMock.inspectBrunoImport).not.toHaveBeenCalled()
  })

  it('clears a canonical synchronization target when the project changes', async () => {
    const sourceScript = {
      id: 'script-1',
      ref_key: 'SCR-1',
      project_id: 'project-1',
      name: 'Existing Bruno script',
      version: '1.0',
      protocol: 'bruno',
      created_at: '2026-07-16T19:00:00Z',
    }
    apiMock.listScripts
      .mockResolvedValueOnce({ scripts: [sourceScript], count: 1 })
      .mockResolvedValueOnce({ scripts: [], count: 0 })
    const file = new File(['info: {name: Sync, type: http}'], 'sync.yml')
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
      await Promise.resolve()
    })

    const syncTarget = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Synchronization target'))
    expect(syncTarget).toBeDefined()
    await act(async () => {
      if (syncTarget) {
        syncTarget.value = 'script-1'
        syncTarget.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    expect(syncTarget?.value).toBe('script-1')

    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-2" />)
      await Promise.resolve()
      await Promise.resolve()
    })
    const currentSyncTarget = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Synchronization target'))
    expect(currentSyncTarget?.value).toBe('')
  })

  it('shows a mixed select-all state for a partial native selection', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    apiMock.inspectBrunoImport.mockResolvedValueOnce(nativeInspectionFixture())
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    const firstCandidate = container.querySelector<HTMLInputElement>('input[aria-label="Select First API"]')
    expect(firstCandidate?.checked).toBe(true)
    await act(async () => firstCandidate?.click())
    const selectAll = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.parentElement?.textContent?.includes('Select all'))
    expect(selectAll?.checked).toBe(false)
    expect(selectAll?.indeterminate).toBe(true)
    expect(selectAll?.getAttribute('aria-checked')).toBe('mixed')
  })
})
