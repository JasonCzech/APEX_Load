// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import BrunoImport from './BrunoImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getBrunoSupportMatrix: vi.fn(),
  listScripts: vi.fn(),
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
      source_format: 'yml',
      root_path: '',
      manifest: {
        version: '1',
        sha256: 'manifest',
        raw_archive_sha256: 'raw',
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
    const file = new File(['info:\n  name: SOAP request\n  type: http\nhttp:\n  method: POST\n'], 'request.yml', { type: 'application/yaml' })
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
})
