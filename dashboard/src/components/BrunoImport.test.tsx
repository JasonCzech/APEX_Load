// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import { type Folder } from '../types'
import { storedZipFixture } from '../test/zipFixture'
import BrunoImport from './BrunoImport'
import { brunoDestinationFolderOptions } from './brunoFolderOptions'
import { nativeReviewGuidance } from './brunoNativeReviewGuidance'
import { aggregateBrunoSecretRequirements } from './brunoSecretRequirements'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getBrunoSupportMatrix: vi.fn(),
  getAuthEffectiveAccess: vi.fn(),
  listScripts: vi.fn(),
  listFolders: vi.fn(),
  createFolder: vi.fn(),
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

describe('nativeReviewGuidance', () => {
  it.each([
    ['bruno_form_template_review_required', 'Form body template encoding differs'],
    ['bruno_multipart_template_and_file_review_required', 'Multipart templates and files need translation'],
    ['bruno_multipart_template_review_required', 'Multipart template escaping differs'],
    ['bruno_multipart_file_review_required', 'Multipart file parts were omitted'],
    ['bruno_graphql_body_review_required', 'GraphQL body needs translation'],
    ['bruno_url_secret_encoding_review_required', 'Credential URL encoding cannot be preserved'],
    ['bruno_sensitive_header_resolution_review_required', 'Unresolved sensitive header template'],
    ['bruno_url_template_encoding_review_required', 'URL template encoding differs'],
    ['bruno_json_secret_escaping_review_required', 'JSON credential escaping is not guaranteed'],
    ['bruno_xml_unsupported_markup_review_required', 'Credential-bearing XML was redacted'],
    ['bruno_auth_review_required', 'Authentication mode is unsupported'],
    ['bruno_max_redirects_clamped', 'Redirect limit was reduced'],
    ['bruno_encode_url_false_review_required', 'Unencoded URL behavior is not guaranteed'],
    ['bruno_javascript_review_required', 'Unsupported Bruno JavaScript'],
  ])('provides specific guidance for converter review gate %s', (code, expectedTitle) => {
    const guidance = nativeReviewGuidance(code)

    expect(guidance.title).toBe(expectedTitle)
    expect(guidance.title).not.toBe('Imported behavior requires manual translation')
    expect(guidance.impact.length).toBeGreaterThan(80)
    expect(guidance.resolution).toContain('remove the script’s review gate')
  })

  it('keeps disclosure-safe fallback guidance for an unknown future finding', () => {
    expect(nativeReviewGuidance('bruno_future_review_required')).toEqual({
      title: 'Imported behavior requires manual translation',
      impact: 'APEX preserved the request as an incomplete draft but cannot guarantee behavior equivalent to the Bruno source. Replay remains blocked to prevent misleading test results.',
      resolution: 'Review the finding, translate or correct the unsupported behavior, verify the result, and then remove the script’s review gate.',
    })
  })
})

describe('aggregateBrunoSecretRequirements', () => {
  it('aggregates a high-cardinality shared credential in one deterministic result', () => {
    const requirementCount = 10_000
    const requirements = Array.from({ length: requirementCount }, (_, index) => {
      const path = `CTA/request-${String(index).padStart(5, '0')}.yml`
      return {
        id: 'shared-partner-username',
        path,
        key: 'partnerUserName',
        suggested_name: 'PARTNER_USERNAME',
        display_label: 'Partner username',
        source_variable: 'partnerUserName',
        unresolved_source_variable: true,
        affected_paths: [path],
        context: 'XML username element <mh:userName>',
        replacement: '{{secret.PARTNER_USERNAME}}',
        source_value_available: false,
        destination_value_preserved: false,
        preservation_unavailable_reason: 'source_value_unavailable' as const,
      }
    })

    const aggregated = aggregateBrunoSecretRequirements(requirements)

    expect(aggregated).toHaveLength(1)
    expect(aggregated[0].affected_paths).toHaveLength(requirementCount)
    expect(aggregated[0].affected_paths?.[0]).toBe('CTA/request-00000.yml')
    expect(aggregated[0].affected_paths?.[requirementCount - 1]).toBe('CTA/request-09999.yml')
  })
})

function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function selectProjectSecret(container: HTMLElement, index = 0) {
  const choices = Array.from(container.querySelectorAll<HTMLInputElement>(
    '.secret-binding-review-choice input[id$="-project-secret"]',
  ))
  choices[index]?.click()
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
        destination_value_preserved: true,
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
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [{
        id: 'projects_and_folders',
        name: 'Projects and Folders',
        description: 'Project governance defaults and result-folder organization.',
        operations: [{
          id: 'manage_projects',
          label: 'Create, update, delete projects and folders',
          allowed: true,
        }],
      }],
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
    apiMock.createFolder.mockResolvedValue({
      id: 'folder-created',
      project_id: 'project-1',
      parent_id: null,
      name: 'Imported Bruno APIs',
      category: 'scripts',
      created_at: '2026-07-16T19:00:00Z',
      updated_at: '2026-07-16T19:00:00Z',
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

  it('shows the actionable Windows protected-store requirement on inspect failure', async () => {
    const message = 'Protected imports on Windows require an artifact directory on a local fixed NTFS or ReFS volume. Network, removable, and unsupported filesystems are not accepted.'
    apiMock.inspectBrunoImport.mockRejectedValueOnce(new ApiError(message, 503, {
      error: message,
      code: 'protected_import_store_unsupported_filesystem',
      retryable: false,
    }))
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
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

    expect(container.textContent).toContain('local fixed NTFS or ReFS volume')
    expect(container.textContent).toContain('Network, removable, and unsupported filesystems are not accepted')
    expect(container.textContent).not.toContain('Controller reported a server error')
  })

  it('uses the shell-owned destination without rendering or loading a second folder control', async () => {
    const file = new File(['version: 1'], 'ambiguous.yml', { type: 'application/yaml' })
    await act(async () => {
      root.render(
        <BrunoImport
          initialFiles={[file]}
          projectId="project-1"
          folderId="folder-1"
          onFolderChange={vi.fn()}
          destinationFolderManagedExternally
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    const destinationLabels = Array.from(container.querySelectorAll('label'))
      .filter(label => label.querySelector('span')?.textContent === 'Destination folder')
    expect(destinationLabels).toHaveLength(0)
    expect(container.querySelector('.bruno-folder-create')).toBeNull()
    expect(apiMock.listFolders).not.toHaveBeenCalled()
    expect(apiMock.getAuthEffectiveAccess).not.toHaveBeenCalled()
  })

  it('expires and disables a protected Bruno commit while credential review is visible', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2030-01-01T00:00:00Z'))
    try {
      const inspection = inspectionFixture()
      inspection.expires_at = '2030-01-01T00:00:01Z'
      apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
      const file = new File(['meta { name: Expiring credential }'], 'collection.bru', { type: 'text/plain' })
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

      const commitButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Import Bruno collection'))
      expect(container.textContent).toContain('Bruno import session available until')
      expect(commitButton?.disabled).toBe(false)

      await act(async () => {
        vi.advanceTimersByTime(1_001)
        await Promise.resolve()
      })
      expect(container.textContent).toContain('This Bruno import session has expired')
      expect(commitButton?.disabled).toBe(true)
      expect(apiMock.commitBrunoImport).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows and enforces protected Bruno expiry even when no credentials require review', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2030-01-01T00:00:00Z'))
    try {
      const inspection = inspectionFixture()
      inspection.expires_at = '2030-01-01T00:00:01Z'
      inspection.result.secret_requirements = []
      apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
      const file = new File(['meta { name: Expiring import }'], 'collection.bru', { type: 'text/plain' })
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

      expect(container.querySelector('.secret-binding-review')).toBeNull()
      const commitButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Import Bruno collection'))
      expect(commitButton?.disabled).toBe(false)
      await act(async () => {
        vi.advanceTimersByTime(1_001)
        await Promise.resolve()
      })
      expect(container.textContent).toContain('This Bruno import session has expired')
      expect(commitButton?.disabled).toBe(true)
    } finally {
      vi.useRealTimers()
    }
  })

  it('commits an untouched credential review without bindings so the imported runtime value remains usable', async () => {
    apiMock.getScriptSecretStatus.mockResolvedValueOnce({
      script_id: 'script-1',
      required: [],
      configured: [],
      missing: [],
    })
    const file = new File(['meta { name: Preserved password }\nget { url: https://example.test }\n'], 'collection.bru', { type: 'text/plain' })
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" folderId="folder-1" />)
      await Promise.resolve()
    })

    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelector('.secret-binding-review-item .script-health-token')?.textContent)
      .toBe('Preserved in script')
    const commitButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Import Bruno collection'))
    expect(commitButton?.textContent).not.toContain('to configure')
    await act(async () => {
      commitButton?.click()
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.commitBrunoImport).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-1',
      import_session_id: 'session-1',
      mode: 'create',
      folder_id: 'folder-1',
      migrate_secrets: false,
      secret_bindings: {},
    }))
    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    expect(apiMock.getScriptSecretStatus).toHaveBeenCalledWith('script-1')
    expect(container.textContent).toContain('Canonical source active')
    expect(container.textContent).not.toContain('project secret still needs configuration')
  })

  it('notes local key custody without blocking the import', async () => {
    apiMock.getBrunoSupportMatrix.mockResolvedValueOnce({
      compatibility_version: '3.5.2',
      import_enabled: true,
      safe_execution_enabled: true,
      developer_execution_enabled: false,
      native_projection_enabled: false,
      retention: {
        available: true,
        key_custody: 'local_auto',
        advisory: 'Bruno source is encrypted with a local key this controller generated for itself, stored beside the data. Set APEXLOAD_WRAPPING_MASTER_KEY or APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID for operator-managed key custody.',
      },
    })
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
    })
    await act(async () => { await Promise.resolve() })

    expect(container.textContent).toContain('operator-managed key custody')
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    expect(inspectButton?.disabled).toBe(false)
  })

  it('blocks inspection with the operator action when protected source retention is unavailable', async () => {
    // Inspection publishes encrypted source artifacts, so with no envelope key
    // it can only fail — and the server masks that failure. Without this the
    // whole import form stays enabled and dead-ends at "Inspect source".
    apiMock.getBrunoSupportMatrix.mockResolvedValueOnce({
      compatibility_version: '3.5.2',
      import_enabled: true,
      safe_execution_enabled: true,
      developer_execution_enabled: false,
      native_projection_enabled: false,
      retention: {
        available: false,
        unavailable_reason: 'Bruno import requires protected source retention, which needs an envelope key. Set APEXLOAD_WRAPPING_MASTER_KEY (32 bytes, or base64-encoded 32 bytes) or configure APEXLOAD_WRAPPING_KEY_VAULT_KEY_ID, then restart the controller.',
      },
    })
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
    })
    await act(async () => { await Promise.resolve() })

    expect(container.textContent).toContain('Set APEXLOAD_WRAPPING_MASTER_KEY')
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    expect(inspectButton?.disabled).toBe(true)
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
    })
    expect(apiMock.inspectBrunoImport).not.toHaveBeenCalled()
  })

  it('saves a native request with its imported credential when migration and binding stay unselected', async () => {
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
      destination_value_preserved: true,
    }]
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    apiMock.commitBrunoImport.mockResolvedValueOnce({
      batch_id: 'batch-preserved',
      created: true,
      count: 1,
      idempotent_replay: false,
      _http_status: 201,
      scripts: [{
        ...candidate.script,
        id: candidate.planned_script_id,
        ref_key: 'SCR-PRESERVED',
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

    expect(container.querySelector('.secret-binding-review-item .script-health-token')?.textContent)
      .toBe('Preserved in script')
    const credentialGuidance = Array.from(container.querySelectorAll<HTMLLIElement>('.bruno-validation-guidance li'))
      .find(item => item.textContent?.includes('Credentials'))
    expect(credentialGuidance?.classList.contains('ready')).toBe(true)
    expect(credentialGuidance?.textContent).toContain('Credential handling is complete for 1 detected requirement.')
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 1 native script'))
    await act(async () => {
      saveButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.commitBrunoImport).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'create_native_batch',
      migrate_secrets: false,
      secret_bindings: {},
    }))
    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Native batch saved')
  })

  it('warns when credential choices are valid but a project secret is still unconfigured', async () => {
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
      source_value_available: false,
      destination_value_preserved: false,
    }]
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
      await Promise.resolve()
    })

    const credentialGuidance = Array.from(container.querySelectorAll<HTMLLIElement>('.bruno-validation-guidance li'))
      .find(item => item.textContent?.includes('Credentials'))
    expect(credentialGuidance?.classList.contains('warning')).toBe(true)
    expect(credentialGuidance?.textContent).toContain('Credential review is complete, but 1 project secret must still be configured before execution.')
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 1 native script'))
    expect(saveButton?.disabled).toBe(false)
    expect(saveButton?.textContent).toContain('with 1 secret to configure')
  })

  it('presents one shared unresolved XML variable with every affected native request', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    for (const [index, candidate] of inspection.result.scripts.entries()) {
      ;(candidate as typeof candidate & { secret_requirements: unknown[] }).secret_requirements = [{
        id: 'shared-partner-username',
        path: `CTA/${index === 0 ? 'first' : 'second'}.yml`,
        key: 'partnerUserName',
        suggested_name: 'PARTNER_USERNAME',
        display_label: 'Partner username',
        source_variable: 'partnerUserName',
        unresolved_source_variable: true,
        affected_paths: [`CTA/${index === 0 ? 'first' : 'second'}.yml`],
        context: 'XML username element <mh:userName>',
        replacement: '{{secret.PARTNER_USERNAME}}',
        source_value_available: false,
        destination_value_preserved: false,
        preservation_unavailable_reason: 'source_value_unavailable',
      }]
    }
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)

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
      await Promise.resolve()
    })

    expect(container.querySelectorAll('.secret-binding-review-item')).toHaveLength(1)
    expect(container.querySelector('.secret-binding-review-label-row strong')?.textContent).toBe('Partner username')
    expect(container.querySelector('.secret-binding-review-source code')?.textContent).toBe('{{partnerUserName}}')
    expect(container.querySelector('.credentials-location-details summary')?.textContent)
      .toBe('View 2 affected request files')
    expect(container.querySelectorAll('.credentials-location-list code')).toHaveLength(0)
    await act(async () => {
      const details = container.querySelector<HTMLDetailsElement>('.credentials-location-details')!
      details.open = true
      details.dispatchEvent(new Event('toggle'))
    })
    expect(Array.from(container.querySelectorAll('.credentials-location-list code')).map(node => node.textContent))
      .toEqual(['CTA/first.yml', 'CTA/second.yml'])
  })

  it('presents path-derived sensitive-header secrets by header origin and runtime purpose', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    const examples = [
      {
        id: 'a73758b339b71e1635eb25f9',
        path: 'CTA_Collection 6a/CTA/InquireAccountProfile_312.yml',
        header: 'X-CDF-AUTH-TOKEN',
        displayLabel: 'X-CDF auth token',
        suggestedName: 'CTA_COLLECTION_6A_CTA_INQUIREACCOUNTPROFILE_312_HEADER_X_CDF_AUTH_TOKEN_84921F7A1055',
        context: 'Sensitive Bruno request header "X-CDF-AUTH-TOKEN" references {{authToken}}, but the collection does not include a value for that source variable. A configured destination value is required when this request runs.',
        sourceVariable: 'authToken',
        sourceValueAvailable: false,
        destinationValuePreserved: false,
        preservationUnavailableReason: 'source_value_unavailable',
      },
      {
        id: '6b6c3b23a33b3293c01a677d',
        path: 'CTA_Collection 6a/CTA/ActivateSubscriber_v302.yml',
        header: 'idpctx-ixp-dark-mode-session',
        displayLabel: 'IDP context IXP dark mode session',
        suggestedName: 'CTA_COLLECTION_6A_CTA_ACTIVATESUBSCRIBER_V302_HEADER_IDPCTX_IXP_DARK_MODE_SESSION_161E2747AD92',
        context: 'Sensitive Bruno request header "idpctx-ixp-dark-mode-session" contains an imported value that can be preserved for this request.',
        sourceVariable: '',
        sourceValueAvailable: true,
        destinationValuePreserved: true,
        preservationUnavailableReason: undefined,
      },
      {
        id: '887c23d56055c1804ec9137f',
        path: 'CTA_Collection 6a/CTA/InquireCPNIContactInformation_224.yml',
        header: 'idpctx-session-id',
        displayLabel: 'IDP context session ID',
        suggestedName: 'CTA_COLLECTION_6A_CTA_INQUIRECPNICONTACTINFORMATION_224_HEADER_IDPCTX_SESSION_ID',
        context: 'Sensitive Bruno request header "idpctx-session-id" contains an imported value that can be preserved for this request.',
        sourceVariable: '',
        sourceValueAvailable: true,
        destinationValuePreserved: true,
        preservationUnavailableReason: undefined,
      },
    ]
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: true,
      },
      secrets: names.map(name => ({
        name,
        configured: name === examples[0].suggestedName,
        provider: 'encrypted_database',
        writable: true,
      })),
    }))
    ;(inspection.result.scripts[0] as typeof inspection.result.scripts[0] & { secret_requirements: unknown[] }).secret_requirements = examples.map(example => ({
      id: example.id,
      path: example.path,
      key: `native-header:/http/headers/${example.header}`,
      suggested_name: example.suggestedName,
      display_label: example.displayLabel,
      source_variable: example.sourceVariable || undefined,
      unresolved_source_variable: example.sourceValueAvailable === false,
      context: example.context,
      replacement: `{{secret.${example.suggestedName}}}`,
      affected_paths: [example.path],
      source_value_available: example.sourceValueAvailable,
      destination_value_preserved: example.destinationValuePreserved,
      preservation_unavailable_reason: example.preservationUnavailableReason,
    }))
    ;(inspection.result.scripts[1] as typeof inspection.result.scripts[1] & { secret_requirements: unknown[] }).secret_requirements = []
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)

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
      await Promise.resolve()
    })

    const cardLabels = Array.from(container.querySelectorAll('.secret-binding-review-label-row strong'))
      .map(node => node.textContent)
    expect(cardLabels).toEqual(examples.map(example => example.displayLabel))
    const cardContexts = Array.from(container.querySelectorAll('.secret-binding-review-location > p'))
      .map(node => node.textContent)
    expect(cardContexts[0]).toBe(
      'Origin: Bruno sensitive request header "X-CDF-AUTH-TOKEN". Runtime purpose: Supplies the credential value sent in the X-CDF-AUTH-TOKEN HTTP header when this request runs.',
    )
    expect(cardContexts[0]).not.toContain('does not include a value')
    expect(container.querySelector('.secret-binding-review-preservation-reasons')?.textContent)
      .toContain('Sensitive Bruno request header "X-CDF-AUTH-TOKEN" references {{authToken}}, but the collection does not include a value for that source variable.')
    expect(container.querySelector('.secret-binding-review-preservation-reasons')?.textContent)
      .toContain('A configured destination value is required when this request runs.')
    expect(container.querySelector('.secret-binding-review-preservation-reasons')?.textContent)
      .toContain('The selected project secret is already configured and will be used when the imported scripts run.')
    expect(container.textContent).not.toContain('Configure the generated project secret before running this request.')
    expect(container.textContent?.match(/does not include a value for that source variable/g)).toHaveLength(1)
    examples.slice(1).forEach(example => {
      expect(cardContexts).toContain(`${example.context} Runtime purpose: Supplies the credential value sent in the ${example.header} HTTP header when this request runs.`)
      expect(container.textContent?.match(new RegExp(example.context.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'g')))
        .toHaveLength(1)
    })
    examples.forEach(example => {
      expect(container.textContent).toContain(`{{secret.${example.suggestedName}}}`)
    })

    const reviewButton = container.querySelector<HTMLButtonElement>('.bruno-native-review-button')
    await act(async () => {
      reviewButton?.click()
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    const secretsSection = Array.from(document.body.querySelectorAll<HTMLElement>('.bruno-native-review-section'))
      .find(section => section.querySelector('h4')?.textContent === 'Secrets')
    const modalLabels = Array.from(secretsSection?.querySelectorAll('li > strong') || []).map(node => node.textContent)
    expect(modalLabels).toEqual(examples.map(example => example.displayLabel))
    examples.forEach((example, index) => {
      expect(secretsSection?.textContent).toContain(`Import context: ${example.context}`)
      expect(secretsSection?.textContent)
        .toContain(`Runtime purpose: Supplies the credential value sent in the ${example.header} HTTP header when this request runs.`)
      expect(secretsSection?.textContent)
        .toContain(`${index === 0 ? 'Project secret reference' : 'Optional project-secret suggestion'}: {{secret.${example.suggestedName}}}`)
    })
    expect(secretsSection?.textContent.match(/does not include a value for that source variable/g)).toHaveLength(1)
    expect(secretsSection?.textContent).not.toContain('No value for {{authToken}} was included in the collection')
    expect(secretsSection?.textContent)
      .toContain('Current destination: The selected project secret is already configured and will be used when the imported scripts run.')
    expect(secretsSection?.textContent).not.toContain('Configure the generated project secret')
  })

  it('shows an operator-renamed configured secret in Review instead of the importer suggestion', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    const candidate = inspection.result.scripts[0]
    inspection.result.scripts = [candidate]
    inspection.result.fidelity.executable_items = 1
    ;(candidate as typeof candidate & { secret_requirements: unknown[] }).secret_requirements = [{
      id: 'renamed-header-token',
      path: 'CTA/first.yml',
      key: 'native-header:/http/headers/X-CDF-AUTH-TOKEN',
      suggested_name: 'SUGGESTED_TOKEN',
      display_label: 'X-CDF auth token',
      source_variable: 'authToken',
      unresolved_source_variable: true,
      context: 'Sensitive Bruno request header "X-CDF-AUTH-TOKEN" references {{authToken}}, but the collection does not include a value for that source variable. A configured destination value is required when this request runs.',
      replacement: '{{secret.SUGGESTED_TOKEN}}',
      source_value_available: false,
      destination_value_preserved: false,
      preservation_unavailable_reason: 'source_value_unavailable',
    }]
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
      },
      secrets: names.map(name => ({
        name,
        configured: name === 'RENAMED_TOKEN',
        provider: 'encrypted_database',
        writable: true,
      })),
    }))
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)

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
    act(() => setInput(container.querySelector<HTMLInputElement>('.secret-binding-review-control input[type="text"]')!, 'RENAMED_TOKEN'))
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })
    await act(async () => {
      container.querySelector<HTMLButtonElement>('.bruno-native-review-button')?.click()
      await Promise.resolve()
    })

    const dialog = document.body.querySelector<HTMLElement>('.bruno-native-review-dialog')
    expect(dialog?.textContent).toContain('Project secret reference: {{secret.RENAMED_TOKEN}}')
    expect(dialog?.textContent).not.toContain('{{secret.SUGGESTED_TOKEN}}')
    expect(dialog?.textContent)
      .toContain('Current destination: The selected project secret is already configured and will be used when the imported scripts run.')
  })

  it('explains durable credential recovery and reuses the collision-safe secret on script-save retry', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    const candidate = inspection.result.scripts[0]
    inspection.result.scripts = [candidate]
    inspection.result.fidelity.executable_items = 1
    ;(candidate as typeof candidate & { secret_requirements: unknown[] }).secret_requirements = [{
      id: 'collision-header-token',
      path: 'CTA/first.yml',
      key: 'native-header:/http/headers/X-CDF-AUTH-TOKEN',
      suggested_name: 'COLLIDING_TOKEN',
      display_label: 'X-CDF auth token',
      source_variable: 'authToken',
      unresolved_source_variable: true,
      context: 'Sensitive Bruno request header "X-CDF-AUTH-TOKEN" references {{authToken}}, but the collection does not include a value for that source variable. A configured destination value is required when this request runs.',
      replacement: '{{secret.COLLIDING_TOKEN}}',
      source_value_available: false,
      destination_value_preserved: false,
      preservation_unavailable_reason: 'source_value_unavailable',
    }]
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
      },
      secrets: names.map(name => ({
        name,
        configured: name === 'COLLIDING_TOKEN',
        provider: 'encrypted_database',
        writable: true,
      })),
    }))
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    apiMock.commitBrunoImport
      .mockRejectedValueOnce(new Error('temporary script-store failure'))
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
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" folderId="folder-1" />)
      await Promise.resolve()
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const valueInput = container.querySelector<HTMLInputElement>('.secret-binding-review-control input[type="password"]')!
    act(() => setInput(valueInput, 'write-only-value'))
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 1 native script'))
    await act(async () => {
      saveButton?.click()
      await new Promise(resolve => window.setTimeout(resolve, 0))
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    expect(apiMock.createProjectSecrets).toHaveBeenCalledWith('project-1', [{
      name: 'COLLIDING_TOKEN_2',
      value: 'write-only-value',
    }])
    const commitError = container.querySelector<HTMLElement>('.bruno-commit-bar [role="alert"]')
    expect(commitError?.textContent).toContain('Save failed: temporary script-store failure')
    expect(container.textContent).toContain('Project-secret values configured during this review remain in Project Secrets independently of script saving')
    expect(container.textContent).toContain('retrying this save reuses those configured values')
    expect(container.textContent).toContain('applies to scripts, not external secret-store writes')
    await act(async () => {
      container.querySelector<HTMLButtonElement>('.bruno-native-review-button')?.click()
      await Promise.resolve()
    })
    const dialog = document.body.querySelector<HTMLElement>('.bruno-native-review-dialog')
    expect(dialog?.textContent).toContain('Project secret reference: {{secret.COLLIDING_TOKEN_2}}')
    expect(dialog?.textContent).not.toContain('{{secret.COLLIDING_TOKEN}}')
    expect(dialog?.textContent)
      .toContain('Current destination: The selected project secret is already configured and will be used when the imported scripts run.')

    await act(async () => {
      saveButton?.click()
      await new Promise(resolve => window.setTimeout(resolve, 0))
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
    expect(apiMock.commitBrunoImport).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('1 native APEX script saved atomically')
  })

  it('keeps shared credential state out of an unselected candidate preview', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    for (const candidate of inspection.result.scripts) {
      ;(candidate as typeof candidate & { secret_requirements: unknown[] }).secret_requirements = [{
        id: 'shared-header-token',
        path: candidate.source_path,
        key: 'native-header:/http/headers/X-CDF-AUTH-TOKEN',
        suggested_name: 'SHARED_TOKEN',
        display_label: 'X-CDF auth token',
        source_variable: 'authToken',
        unresolved_source_variable: true,
        context: 'Sensitive Bruno request header "X-CDF-AUTH-TOKEN" references {{authToken}}, but the collection does not include a value for that source variable. A configured destination value is required when this request runs.',
        replacement: '{{secret.SHARED_TOKEN}}',
        source_value_available: false,
        destination_value_preserved: false,
        preservation_unavailable_reason: 'source_value_unavailable',
      }]
    }
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)

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
    const candidateRows = Array.from(container.querySelectorAll<HTMLTableRowElement>('.bruno-native-table tbody tr'))
    await act(async () => {
      candidateRows[1].querySelector<HTMLInputElement>('input[type="checkbox"]')?.click()
      await Promise.resolve()
    })
    act(() => setInput(container.querySelector<HTMLInputElement>('.secret-binding-review-control input[type="text"]')!, 'SELECTED_ONLY_TOKEN'))
    await act(async () => {
      await Promise.resolve()
      candidateRows[1].querySelector<HTMLButtonElement>('.bruno-native-review-button')?.click()
      await Promise.resolve()
    })

    const dialog = document.body.querySelector<HTMLElement>('.bruno-native-review-dialog')
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent)
      .toContain('Preview detected script')
    expect(dialog?.textContent).toContain('Preview only — not selected for import.')
    expect(dialog?.textContent).toContain('Detected project-secret suggestion: {{secret.SHARED_TOKEN}}')
    expect(dialog?.textContent).not.toContain('SELECTED_ONLY_TOKEN')
    expect(dialog?.textContent).not.toContain('Current destination:')
  })

  it('paginates 10,000 native candidates while keeping the table DOM bounded and selection global', async () => {
    const candidateCount = 10_000
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    const template = inspection.result.scripts[0]
    const candidates = Array.from({ length: candidateCount }, (_, index) => {
      const ordinal = String(index).padStart(5, '0')
      return {
        ...template,
        candidate_id: `candidate-${ordinal}`,
        planned_script_id: `planned-${ordinal}`,
        source_path: `CTA/request-${ordinal}.yml`,
        source_uid: `request-${ordinal}`,
        sequence: index + 1,
        script: {
          ...template.script,
          name: `API ${ordinal}`,
          actions: template.script.actions.map(action => ({ ...action, name: `API ${ordinal}` })),
        },
        diagnostics: [],
        missing_variables: [],
        secret_requirements: [],
      }
    })
    inspection.result.scripts = candidates as unknown as typeof inspection.result.scripts
    inspection.result.fidelity.executable_items = candidateCount
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)

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

    const firstPageRows = Array.from(container.querySelectorAll<HTMLTableRowElement>('.bruno-native-table tbody tr'))
    expect(firstPageRows).toHaveLength(100)
    expect(firstPageRows[0].textContent).toContain('CTA/request-00000.yml')
    expect(firstPageRows[99].textContent).toContain('CTA/request-00099.yml')
    const pagination = container.querySelector<HTMLElement>('.bruno-native-table-pagination')!
    expect(pagination.getAttribute('aria-label')).toBe('Native scripts table pages')
    expect(pagination.textContent).toContain('Showing 1–100 of 10000 scripts. Page 1 of 100.')
    expect(pagination.querySelector('[role="group"]')?.getAttribute('aria-label'))
      .toBe('Native scripts page controls')
    const nextButton = Array.from(pagination.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Next'))!
    await act(async () => nextButton.click())

    const secondPageRows = Array.from(container.querySelectorAll<HTMLTableRowElement>('.bruno-native-table tbody tr'))
    expect(secondPageRows).toHaveLength(100)
    expect(secondPageRows[0].textContent).toContain('CTA/request-00100.yml')
    expect(secondPageRows[99].textContent).toContain('CTA/request-00199.yml')
    expect(pagination.textContent).toContain('Showing 101–200 of 10000 scripts. Page 2 of 100.')
    await act(async () => {
      secondPageRows[0].querySelector<HTMLInputElement>('input[type="checkbox"]')?.click()
      await Promise.resolve()
    })
    expect(container.querySelectorAll('.bruno-native-table tbody tr')).toHaveLength(100)
    expect(container.querySelector('.bruno-select-all input')?.getAttribute('aria-checked')).toBe('mixed')
    expect(Array.from(container.querySelectorAll('.bruno-native-table tbody tr'))[0]
      .querySelector<HTMLInputElement>('input[type="checkbox"]')?.checked).toBe(false)
    expect(container.querySelectorAll<HTMLInputElement>('.bruno-native-table tbody input[type="checkbox"]:checked'))
      .toHaveLength(99)
  })

  it('paginates 10,000 review findings and bounds both cards and save-blocker details', async () => {
    const findingCount = 10_000
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    const inspection = nativeInspectionFixture()
    const template = inspection.result.scripts[0]
    const candidates = Array.from({ length: findingCount }, (_, index) => {
      const ordinal = String(index).padStart(5, '0')
      return {
        ...template,
        candidate_id: `candidate-${ordinal}`,
        planned_script_id: `planned-${ordinal}`,
        source_path: `CTA/request-${ordinal}.yml`,
        source_uid: `request-${ordinal}`,
        sequence: index + 1,
        script: {
          ...template.script,
          name: `API ${ordinal}`,
          actions: template.script.actions.map(action => ({ ...action, name: `API ${ordinal}` })),
        },
        diagnostics: [{
          id: `finding-${ordinal}`,
          code: 'bruno_javascript_review_required',
          severity: 'warning',
          message: `Translate unsupported JavaScript ${ordinal}.`,
          acknowledgement_required: true,
        }],
        missing_variables: [],
        secret_requirements: [],
      }
    })
    inspection.result.scripts = candidates as unknown as typeof inspection.result.scripts
    inspection.result.fidelity.executable_items = findingCount
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)

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

    let cards = Array.from(container.querySelectorAll<HTMLElement>('.bruno-native-acknowledgement-card'))
    expect(cards).toHaveLength(100)
    expect(cards[0].textContent).toContain('CTA/request-00000.yml')
    expect(cards[99].textContent).toContain('CTA/request-00099.yml')
    const findingPagination = container.querySelector<HTMLElement>(
      'nav[aria-label="Review-gated conversion pages"]',
    )!
    expect(findingPagination.textContent).toContain('Showing findings 1–100 of 10000. Page 1 of 100.')
    expect(findingPagination.querySelector('[role="group"]')?.getAttribute('aria-label'))
      .toBe('Review finding page controls')
    const blockerItems = Array.from(container.querySelectorAll('#bruno-save-blockers li'))
    expect(blockerItems.filter(item => item.textContent?.startsWith('Review CTA/'))).toHaveLength(10)
    expect(blockerItems.length).toBeLessThanOrEqual(12)
    expect(container.querySelector('#bruno-save-blockers')?.textContent)
      .toContain('9990 additional review-gated conversion findings require acknowledgement')

    const nextButton = Array.from(findingPagination.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Next'))!
    act(() => nextButton.click())
    cards = Array.from(container.querySelectorAll<HTMLElement>('.bruno-native-acknowledgement-card'))
    expect(cards).toHaveLength(100)
    expect(cards[0].textContent).toContain('CTA/request-00100.yml')
    expect(cards[99].textContent).toContain('CTA/request-00199.yml')
    expect(findingPagination.textContent).toContain('Showing findings 101–200 of 10000. Page 2 of 100.')
  })

  it('submits a binding when the operator explicitly chooses an existing project secret', async () => {
    apiMock.getProjectSecretStatuses.mockImplementation(async (_projectId: string, names: string[]) => ({
      project_id: 'project-1',
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: true,
      },
      secrets: names.map(name => ({
        name,
        configured: name === 'EXISTING_PASSWORD',
        provider: 'encrypted_database',
        writable: true,
      })),
    }))
    apiMock.getScriptSecretStatus.mockResolvedValueOnce({
      script_id: 'script-1',
      required: ['EXISTING_PASSWORD'],
      configured: ['EXISTING_PASSWORD'],
      missing: [],
    })
    const file = new File(['meta { name: Existing binding }\nget { url: https://example.test }\n'], 'collection.bru', { type: 'text/plain' })
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

    const bindingName = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="text"]'))
      .find(input => input.value === 'PASSWORD')
    expect(bindingName).toBeDefined()
    await act(async () => {
      selectProjectSecret(container)
      setInput(bindingName!, 'EXISTING_PASSWORD')
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.querySelector('.secret-binding-review-item .script-health-token')?.textContent)
      .toBe('Configured')

    const commitButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Import Bruno collection'))
    await act(async () => {
      commitButton?.click()
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.commitBrunoImport).toHaveBeenCalledWith(expect.objectContaining({
      migrate_secrets: false,
      secret_bindings: {
        'request.yml:http.auth.password': 'EXISTING_PASSWORD',
      },
    }))
    expect(apiMock.createProjectSecrets).not.toHaveBeenCalled()
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
    expect(commitButton?.textContent).not.toContain('to configure')
    expect(commitButton?.disabled).toBe(false)

    const bindingInput = Array.from(container.querySelectorAll('input')).find(input => input.value === 'PASSWORD')
    expect(bindingInput).toBeDefined()
    act(() => {
      selectProjectSecret(container)
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
    expect(container.textContent).toContain('Recommended configuration')
    expect(container.textContent).toContain('Keep all 2 APIs selected')
    expect(container.textContent).toContain('Configure 1 unresolved variable before execution')
    expect(container.textContent).toContain('Read and acknowledge 1 finding before saving')
    expect(container.textContent).toContain('Execution blocked')
    expect(container.textContent).toContain('Review required')
    expect(container.textContent).toContain('Unsupported Bruno JavaScript')
    expect(container.textContent).toContain('Translate unsupported response JavaScript.')
    expect(container.textContent).toContain('Acknowledgement records awareness only')
    expect(container.textContent).not.toContain('finding-js-1')
    expect(container.textContent?.indexOf('CTA/first.yml')).toBeLessThan(container.textContent?.indexOf('CTA/second.yml'))
    const nameInput = container.querySelector<HTMLInputElement>('input[aria-label="Name for CTA/second.yml"]')
    expect(nameInput).not.toBeNull()
    act(() => setInput(nameInput!, ''))

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 2 native scripts'))
    expect(saveButton?.disabled).toBe(true)
    const acknowledgement = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.getAttribute('aria-label') === 'Acknowledge Unsupported Bruno JavaScript for CTA/second.yml')
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

  it('shows separate contextual cards for multiple review findings without exposing internal ids', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts[0].diagnostics = [{
      id: 'bruno-native-sensitive-internal-id',
      code: 'bruno_sensitive_header_resolution_review_required',
      severity: 'warning',
      message: 'Sensitive header "X-CDF-AUTH-TOKEN" contains a composed template that could not be fully resolved.',
      acknowledgement_required: true,
    }]
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })

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

    const cards = Array.from(container.querySelectorAll<HTMLElement>('.bruno-native-acknowledgement-card'))
    expect(cards).toHaveLength(2)
    const javascriptCard = cards.find(card => card.textContent?.includes('Unsupported Bruno JavaScript'))
    expect(javascriptCard?.textContent).toContain('CTA/second.yml')
    expect(javascriptCard?.textContent).toContain('Translate unsupported response JavaScript.')
    expect(javascriptCard?.textContent).toContain('Why replay is blocked')
    expect(javascriptCard?.textContent).toContain('assertions, variable updates, or other behavior will not run')
    expect(javascriptCard?.textContent).toContain('Required before replay')

    const sensitiveHeaderCard = cards.find(card => card.textContent?.includes('Unresolved sensitive header template'))
    expect(sensitiveHeaderCard?.textContent).toContain('CTA/first.yml')
    expect(sensitiveHeaderCard?.textContent).toContain('X-CDF-AUTH-TOKEN')
    expect(sensitiveHeaderCard?.textContent).toContain('incorrect or unsafe credential')
    expect(sensitiveHeaderCard?.textContent).toContain('Bind and verify the governed whole-header value')
    expect(container.textContent).not.toContain('finding-js-1')
    expect(container.textContent).not.toContain('bruno-native-sensitive-internal-id')
    expect(container.textContent).toContain('Each item below is a separate finding in an affected script')

    const javascriptAcknowledgement = container.querySelector<HTMLInputElement>(
      'input[aria-label="Acknowledge Unsupported Bruno JavaScript for CTA/second.yml"]',
    )
    const sensitiveHeaderAcknowledgement = container.querySelector<HTMLInputElement>(
      'input[aria-label="Acknowledge Unresolved sensitive header template for CTA/first.yml"]',
    )
    await act(async () => javascriptAcknowledgement?.click())
    expect(javascriptAcknowledgement?.checked).toBe(true)
    expect(sensitiveHeaderAcknowledgement?.checked).toBe(false)
    expect(container.querySelector('#bruno-save-blockers')?.textContent)
      .not.toContain('Review CTA/second.yml: Translate unsupported response JavaScript.')
    expect(container.querySelector('#bruno-save-blockers')?.textContent)
      .toContain('Review CTA/first.yml: Sensitive header "X-CDF-AUTH-TOKEN"')

    await act(async () => sensitiveHeaderAcknowledgement?.click())
    expect(sensitiveHeaderAcknowledgement?.checked).toBe(true)
    expect(container.querySelector('#bruno-save-blockers')?.textContent)
      .not.toContain('Review CTA/first.yml: Sensitive header "X-CDF-AUTH-TOKEN"')
  })

  it('opens per-script review details in a modal and restores focus when it closes', async () => {
    const inspection = nativeInspectionFixture()
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })

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

    expect(container.querySelector('.bruno-native-table details')).toBeNull()
    const reviewButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('.bruno-native-review-button'))
    expect(reviewButtons).toHaveLength(2)

    await act(async () => {
      reviewButtons[0].focus()
      reviewButtons[0].click()
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    let dialog = document.body.querySelector<HTMLElement>('[role="dialog"]')
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(dialog?.parentElement?.parentElement).toBe(document.body)
    expect(dialog?.getAttribute('aria-modal')).toBe('true')
    expect(dialog?.parentElement?.getAttribute('role')).toBe('presentation')
    expect(dialog?.parentElement?.hasAttribute('tabindex')).toBe(false)
    expect(document.getElementById(dialog?.getAttribute('aria-labelledby') || '')?.textContent).toBe('Review imported script')
    expect(dialog?.textContent).toContain('CTA/first.yml')
    expect(dialog?.textContent).toContain('Missing variables')
    expect(dialog?.textContent).toContain('env')
    expect(dialog?.textContent).toContain('Source mappings')
    expect(dialog?.textContent).toContain('Masked native script')
    expect(dialog?.textContent).toContain('First API')
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Close review for CTA/first.yml')

    const closeButton = Array.from(dialog?.querySelectorAll<HTMLButtonElement>('button') || [])
      .find(button => button.textContent === 'Close')
    await act(async () => closeButton?.click())
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(reviewButtons[0])

    await act(async () => {
      reviewButtons[1].focus()
      reviewButtons[1].click()
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    dialog = document.body.querySelector<HTMLElement>('[role="dialog"]')
    expect(dialog?.textContent).toContain('CTA/second.yml')
    expect(dialog?.textContent).toContain('Diagnostics')
    expect(dialog?.textContent).toContain('Translate unsupported response JavaScript.')
    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(reviewButtons[1])

    await act(async () => {
      reviewButtons[0].focus()
      reviewButtons[0].click()
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    dialog = document.body.querySelector<HTMLElement>('[role="dialog"]')
    const backdrop = dialog?.parentElement
    await act(async () => dialog?.click())
    expect(document.body.querySelector('[role="dialog"]')).toBe(dialog)
    await act(async () => backdrop?.click())
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(reviewButtons[0])
  })

  it('explains unresolved source credentials in the per-script Review modal', async () => {
    const inspection = nativeInspectionFixture()
    ;(inspection.result.scripts[0] as typeof inspection.result.scripts[0] & { secret_requirements: unknown[] }).secret_requirements = [{
      id: 'partner-username',
      path: 'CTA/first.yml',
      key: 'partnerUserName',
      suggested_name: 'PARTNER_USERNAME',
      display_label: 'Partner username',
      source_variable: 'partnerUserName',
      unresolved_source_variable: true,
      context: 'XML username element <mh:userName>',
      replacement: '{{secret.PARTNER_USERNAME}}',
      source_value_available: false,
      destination_value_preserved: false,
      preservation_unavailable_reason: 'source_value_unavailable',
    }]
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })

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
    const reviewButton = container.querySelector<HTMLButtonElement>('.bruno-native-review-button')
    await act(async () => {
      reviewButton?.click()
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const dialog = document.body.querySelector<HTMLElement>('[role="dialog"]')
    const secretsSection = Array.from(dialog?.querySelectorAll<HTMLElement>('.bruno-native-review-section') || [])
      .find(section => section.querySelector('h4')?.textContent === 'Secrets')
    expect(secretsSection?.querySelector('strong')?.textContent).toBe('Partner username')
    expect(secretsSection?.textContent).toContain('Source variable: {{partnerUserName}}')
    expect(secretsSection?.textContent).toContain('Project secret reference: {{secret.PARTNER_USERNAME}}')
    expect(secretsSection?.textContent)
      .toContain('No value for {{partnerUserName}} was included in the collection, so there is no imported value to preserve.')
    expect(secretsSection?.textContent).toContain('execution requires a configured value for that project secret')
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

  it('recommends native mode for an OpenCollection ZIP and creates the missing destination folder inline', async () => {
    apiMock.listFolders.mockResolvedValueOnce({ folders: [], count: 0 })
    apiMock.createFolder.mockResolvedValueOnce({
      id: 'folder-cta',
      project_id: 'project-1',
      parent_id: null,
      name: 'CTA Collection 6a',
      category: 'scripts',
      created_at: '2026-07-16T19:00:00Z',
      updated_at: '2026-07-16T19:00:00Z',
    })
    const archive = new File([storedZipFixture([
      'CTA_Collection 6a/CTA/request.yml',
      'CTA_Collection 6a/opencollection.yml',
    ])], 'CTA_Collection 6a.zip', { type: 'application/zip' })

    await act(async () => {
      root.render(<BrunoImport initialFiles={[archive]} projectId="project-1" />)
      await Promise.resolve()
      await new Promise(resolve => setTimeout(resolve, 0))
    })

    const nativeMode = Array.from(container.querySelectorAll<HTMLInputElement>('input[name="bruno-output-mode"]'))
      .find(input => input.parentElement?.textContent?.includes('Native scripts per API'))
    const destination = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))
    const folderName = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.parentElement?.textContent?.includes('New script folder name'))
    expect(nativeMode?.checked).toBe(true)
    expect(container.textContent).toContain('Source recommendation: Native scripts per API')
    expect(container.textContent).toContain('No script folders exist in this project')
    expect(destination?.disabled).toBe(true)
    expect(folderName?.value).toBe('CTA Collection 6a')

    const createButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Create and select folder'))
    await act(async () => {
      createButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.createFolder).toHaveBeenCalledWith('project-1', {
      name: 'CTA Collection 6a',
      category: 'scripts',
    })
    expect(destination?.disabled).toBe(false)
    expect(destination?.value).toBe('folder-cta')
    expect(container.textContent).toContain('Created and selected script folder “CTA Collection 6a”.')
  })

  it('blocks inspect, save, and destination changes while a replacement folder is being created', async () => {
    const inspection = nativeInspectionFixture()
    const candidate = inspection.result.scripts[0]
    inspection.result.scripts = [candidate]
    inspection.result.fidelity.executable_items = 1
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    let resolveFolder!: (folder: Folder) => void
    apiMock.createFolder.mockReturnValueOnce(new Promise<Folder>(resolve => {
      resolveFolder = resolve
    }))
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })

    function Harness() {
      const [folderId, setFolderId] = useState('folder-1')
      return (
        <BrunoImport
          initialFiles={[file]}
          projectId="project-1"
          folderId={folderId}
          onFolderChange={setFolderId}
        />
      )
    }

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
      await Promise.resolve()
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))!
    await act(async () => {
      inspectButton.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const destination = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))!
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 1 native script'))!
    expect(saveButton.disabled).toBe(false)

    const createButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Create and select folder'))!
    await act(async () => {
      createButton.click()
      await Promise.resolve()
    })

    expect(destination.disabled).toBe(true)
    expect(inspectButton.disabled).toBe(true)
    expect(saveButton.disabled).toBe(true)
    expect(container.textContent).toContain('Wait for destination folder creation to finish')
    expect(apiMock.commitBrunoImport).not.toHaveBeenCalled()

    await act(async () => {
      resolveFolder({
        id: 'folder-replacement',
        project_id: 'project-1',
        parent_id: null,
        name: 'Imported Bruno APIs',
        category: 'scripts',
        created_at: '2026-07-16T19:00:00Z',
        updated_at: '2026-07-16T19:00:00Z',
      })
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(destination.disabled).toBe(false)
    expect(destination.value).toBe('folder-replacement')
    expect(saveButton.disabled).toBe(false)
  })

  it('does not revive an earlier folder create across an A to B to A project switch', async () => {
    let setProject!: (projectId: string) => void
    let resolveFirst!: (folder: Folder) => void
    let resolveSecond!: (folder: Folder) => void
    apiMock.createFolder
      .mockReturnValueOnce(new Promise<Folder>(resolve => { resolveFirst = resolve }))
      .mockReturnValueOnce(new Promise<Folder>(resolve => { resolveSecond = resolve }))
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })

    function Harness() {
      const [projectId, updateProject] = useState('project-a')
      const [folderId, setFolderId] = useState('folder-1')
      setProject = updateProject
      return (
        <BrunoImport
          initialFiles={[file]}
          projectId={projectId}
          folderId={folderId}
          onFolderChange={setFolderId}
        />
      )
    }

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
      await Promise.resolve()
    })
    const createButton = () => Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Create and select folder'))!
    await act(async () => {
      createButton().click()
      await Promise.resolve()
      setProject('project-b')
      await Promise.resolve()
      setProject('project-a')
      await Promise.resolve()
      createButton().click()
      await Promise.resolve()
    })

    await act(async () => {
      resolveFirst({
        id: 'stale-folder', project_id: 'project-a', parent_id: null, name: 'Stale', category: 'scripts',
        created_at: '2026-07-16T19:00:00Z', updated_at: '2026-07-16T19:00:00Z',
      })
      await Promise.resolve()
    })
    const destination = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))!
    expect(destination.value).not.toBe('stale-folder')
    expect(destination.disabled).toBe(true)

    await act(async () => {
      resolveSecond({
        id: 'current-folder', project_id: 'project-a', parent_id: null, name: 'Current', category: 'scripts',
        created_at: '2026-07-16T19:00:00Z', updated_at: '2026-07-16T19:00:00Z',
      })
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(destination.value).toBe('current-folder')
    expect(destination.disabled).toBe(false)
  })

  it('pages large collection trees and lazily mounts descendants', async () => {
    const inspection = inspectionFixture()
    inspection.result.collection.items = Array.from({ length: 10_000 }, (_, index) => ({
      uid: `folder-${index}`,
      name: `Folder ${index}`,
      path: `folder-${index}`,
      type: 'folder',
      executable: false,
      has_scripts: false,
      has_tests: false,
      children: index === 0 ? [{
        uid: 'nested-secret-request',
        name: 'Nested request',
        path: 'folder-0/request.yml',
        type: 'http',
        executable: true,
        has_scripts: false,
        has_tests: false,
      }] : undefined,
    }))
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)

    await act(async () => {
      root.render(<BrunoImport initialFiles={[new File(['version: 1'], 'opencollection.yml')]} projectId="project-1" folderId="folder-1" />)
      await Promise.resolve()
      await Promise.resolve()
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Inspect source'))?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelectorAll('.bruno-tree-row')).toHaveLength(100)
    expect(container.textContent).toContain('Items 1–100 of 10000')
    expect(container.textContent).not.toContain('Nested request')
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent === 'Expand')?.click()
    })
    expect(container.textContent).toContain('Nested request')
  })

  it('explains that a project administrator must create the missing destination when folder management is denied', async () => {
    apiMock.listFolders.mockResolvedValueOnce({ folders: [], count: 0 })
    apiMock.getAuthEffectiveAccess.mockResolvedValueOnce({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [{
        id: 'projects_and_folders',
        name: 'Projects and Folders',
        description: 'Project governance defaults and result-folder organization.',
        operations: [{
          id: 'manage_projects',
          label: 'Create, update, delete projects and folders',
          allowed: false,
        }],
      }],
    })
    apiMock.inspectBrunoImport.mockResolvedValueOnce(nativeInspectionFixture())
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })

    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
      await Promise.resolve()
      await new Promise(resolve => setTimeout(resolve, 0))
    })

    expect(container.textContent).toContain('Your current role can import scripts but cannot create project folders')
    expect(Array.from(container.querySelectorAll('button'))
      .some(button => button.textContent?.includes('Create and select folder'))).toBe(false)

    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 2 native scripts'))
    const blockers = container.querySelector<HTMLElement>('#bruno-save-blockers')
    expect(saveButton?.disabled).toBe(true)
    expect(saveButton?.getAttribute('aria-describedby')).toBe('bruno-save-blockers')
    expect(blockers?.textContent).toContain('No destination folder is available, and your current role cannot create project folders')
    expect(blockers?.textContent).toContain('Review CTA/second.yml: Translate unsupported response JavaScript.')
    expect(blockers?.textContent).toContain('1 unresolved runtime variable does not prevent saving')
    expect(apiMock.createFolder).not.toHaveBeenCalled()
  })

  it('replaces a folder-create 403 with role-specific guidance', async () => {
    apiMock.listFolders.mockResolvedValueOnce({ folders: [], count: 0 })
    apiMock.createFolder.mockRejectedValueOnce(new ApiError('Current session is not allowed to perform this action', 403))
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })

    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
      await Promise.resolve()
      await new Promise(resolve => setTimeout(resolve, 0))
    })
    const createButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Create and select folder'))
    await act(async () => {
      createButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.createFolder).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Your current role can import scripts but cannot create project folders')
    expect(container.textContent).toContain('ask a Project Administrator to create one for this project')
    expect(container.textContent).not.toContain('Current session is not allowed to perform this action')
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

  it('refreshes without navigating when a native commit finishes after Back', async () => {
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
    expect(onImported).toHaveBeenCalledWith({
      scripts: [expect.objectContaining({ id: candidate.planned_script_id })],
      stayInImportReview: true,
    })
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
    const inspect = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspect?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.querySelector('#bruno-save-blockers')?.textContent)
      .toContain('Destination folders could not be loaded: folder service unavailable')
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

  it('clears and blocks a native save when a restored destination folder is stale', async () => {
    const onFolderChange = vi.fn()
    apiMock.listFolders.mockResolvedValueOnce({ folders: [], count: 0 })
    apiMock.inspectBrunoImport.mockResolvedValueOnce(nativeInspectionFixture())
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })

    await act(async () => {
      root.render(
        <BrunoImport
          initialFiles={[file]}
          projectId="project-1"
          folderId="removed-folder"
          onFolderChange={onFolderChange}
        />,
      )
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(onFolderChange).toHaveBeenCalledWith('')
    expect(container.textContent).toContain('previously selected destination folder no longer exists')

    await act(async () => {
      root.render(
        <BrunoImport
          initialFiles={[file]}
          projectId="project-1"
          folderId=""
          onFolderChange={onFolderChange}
        />,
      )
    })
    const inspect = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))!
    await act(async () => {
      inspect.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelector('#bruno-save-blockers')?.textContent).toMatch(/Select an existing destination folder|Create and select a destination folder|No destination folder is available/)
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save 2 native scripts'))
    expect(save?.disabled).toBe(true)
    expect(apiMock.commitBrunoImport).not.toHaveBeenCalled()
  })

  it('persists selected parameterization and an explicitly selected deferred secret binding', async () => {
    const file = new File(['info: {name: Deferred, type: http}'], 'deferred.yml')
    const onConfigureSecrets = vi.fn()
    apiMock.getScriptSecretStatus.mockResolvedValueOnce({
      script_id: 'script-1',
      required: ['EXPLICIT_PASSWORD'],
      configured: [],
      missing: ['EXPLICIT_PASSWORD'],
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

    const bindingName = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="text"]'))
      .find(input => input.value === 'PASSWORD')
    expect(bindingName).toBeDefined()
    act(() => {
      selectProjectSecret(container)
      setInput(bindingName!, 'EXPLICIT_PASSWORD')
    })
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
      secret_bindings: {
        'request.yml:http.auth.password': 'EXPLICIT_PASSWORD',
      },
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
    apiMock.getProjectSecretStatuses.mockImplementation(() => pendingStatus)

    await act(async () => {
      selectProjectSecret(container)
      await Promise.resolve()
    })
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
    await act(async () => {
      selectProjectSecret(container)
      await Promise.resolve()
    })
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

  it('uses the synchronization target folder and sends null when moving it to the project root', async () => {
    const sourceScript = {
      id: 'script-1',
      ref_key: 'SCR-1',
      project_id: 'project-1',
      folder_id: 'folder-1',
      name: 'Existing Bruno script',
      version: '1.0',
      protocol: 'bruno',
      created_at: '2026-07-16T19:00:00Z',
    }
    apiMock.listScripts.mockResolvedValueOnce({ scripts: [sourceScript], count: 1 })
    const inspection = inspectionFixture()
    ;(inspection as typeof inspection & { sync_preview: object }).sync_preview = {
      target_script_id: 'script-1',
      expected_updated_at: '2026-07-16T19:00:00Z',
      active_revision: 'revision-0',
      changes: {},
      conflicts: [],
    }
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    const file = new File(['info: {name: Sync, type: http}'], 'sync.yml')
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
      await Promise.resolve()
    })

    const syncTarget = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Synchronization target'))
    const destination = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))
    await act(async () => {
      if (syncTarget) {
        syncTarget.value = 'script-1'
        syncTarget.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    expect(destination?.value).toBe('folder-1')

    await act(async () => {
      if (destination) {
        destination.value = ''
        destination.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect source'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })
    const commitButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Synchronize Bruno source'))
    await act(async () => {
      commitButton?.click()
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.inspectBrunoImport).toHaveBeenCalledWith(
      expect.any(Array),
      expect.objectContaining({ sync_script_id: 'script-1' }),
    )
    expect(apiMock.commitBrunoImport).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'sync',
      sync_script_id: 'script-1',
      folder_id: null,
    }))
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
