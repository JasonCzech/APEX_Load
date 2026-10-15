// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import { type Folder } from '../types'
import BrunoImport from './BrunoImport'
import { ImportDestinationContext } from './importDestinationContext'
import { brunoDestinationFolderOptions } from './brunoFolderOptions'
import { nativeReviewGuidance } from './brunoNativeReviewGuidance'
import { aggregateBrunoSecretRequirements } from './brunoSecretRequirements'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getBrunoSupportMatrix: vi.fn(),
  getAuthEffectiveAccess: vi.fn(),
  listFolders: vi.fn(),
  createFolder: vi.fn(),
  inspectBrunoImport: vi.fn(),
  getBrunoImportSessionSource: vi.fn(),
  commitBrunoImport: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  createScenarioDraft: vi.fn(),
  discardImportSecretSession: vi.fn(),
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

function retainedNativeSourceFixture() {
  return {
    import_session_id: 'session-1',
    expires_at: '2099-07-16T20:00:00Z',
    parameterization: null,
    result: {
      schema_version: 'apexload.bruno-import.v1',
      output_mode: 'native_requests' as const,
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
      missing_variables: ['env'],
      inspected_at: '2026-07-16T19:00:00Z',
    },
    artifacts: [
      { id: 'raw-1', artifact_kind: 'raw', sha256: 'raw', size_bytes: 4096 },
      { id: 'sanitized-1', artifact_kind: 'sanitized', sha256: 'sanitized', size_bytes: 4000 },
    ],
  }
}

function nativeInspectionFixture() {
  const base = retainedNativeSourceFixture()
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
      safe_execution_enabled: false,
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
    apiMock.getBrunoImportSessionSource.mockResolvedValue('request:\n  url: https://example.test/source\n  body: <source-xml/>')
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
    apiMock.inspectBrunoImport.mockResolvedValue(nativeInspectionFixture())
    apiMock.commitBrunoImport.mockResolvedValue({
      batch_id: 'native-batch', created: true, idempotent_replay: false, count: 2, _http_status: 201,
      scripts: nativeInspectionFixture().result.scripts.map(candidate => ({ ...candidate.script, id: candidate.planned_script_id })),
    })
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.resetAllMocks()
  })

  it('shows the surface destination where its own would be when the surface manages it', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    apiMock.inspectBrunoImport.mockResolvedValueOnce(nativeInspectionFixture())
    await act(async () => {
      root.render(
        <ImportDestinationContext.Provider value={<div data-testid="surface-destination">Save destination</div>}>
          <BrunoImport initialFiles={[file]} projectId="project-1" destinationFolderManagedExternally />
        </ImportDestinationContext.Provider>,
      )
      await Promise.resolve()
    })
    await vi.waitFor(() => expect(container.querySelector('[data-testid="surface-destination"]')).not.toBeNull())
    expect(Array.from(container.querySelectorAll('select')).some(select => select.parentElement?.textContent?.includes('Destination folder'))).toBe(false)
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
      .find(button => button.textContent?.includes('Inspect & review'))
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
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
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
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    const credentialGuidance = Array.from(container.querySelectorAll<HTMLLIElement>('.bruno-validation-guidance li'))
      .find(item => item.textContent?.includes('Credentials'))
    expect(credentialGuidance?.classList.contains('warning')).toBe(true)
    expect(credentialGuidance?.textContent).toContain('Destination choices are valid; 1 project secret must still be configured before execution.')
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
    expect(saveButton?.disabled).toBe(false)
    expect(saveButton?.textContent).toBe('Save 1 draft')
    expect(container.textContent).toContain('Credentials: 1 not configured')
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
      .find(button => button.textContent?.includes('Inspect & review'))
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
      .find(button => button.textContent?.includes('Inspect & review'))
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
      .find(button => button.textContent?.includes('Inspect & review'))
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
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const valueInput = container.querySelector<HTMLInputElement>('.secret-binding-review-control input[type="password"]')!
    act(() => setInput(valueInput, 'write-only-value'))
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
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
    expect(container.textContent).toContain('1 native APEX script created. The batch was saved atomically.')
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
      .find(button => button.textContent?.includes('Inspect & review'))
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
      .find(button => button.textContent?.includes('Inspect & review'))
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
    expect(pagination.textContent).toContain('Showing 1–100 of 10000 matching scripts. Page 1 of 100.')
    expect(pagination.querySelector('[role="group"]')?.getAttribute('aria-label'))
      .toBe('Native scripts page controls')
    const nextButton = Array.from(pagination.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Next'))!
    await act(async () => nextButton.click())

    const secondPageRows = Array.from(container.querySelectorAll<HTMLTableRowElement>('.bruno-native-table tbody tr'))
    expect(secondPageRows).toHaveLength(100)
    expect(secondPageRows[0].textContent).toContain('CTA/request-00100.yml')
    expect(secondPageRows[99].textContent).toContain('CTA/request-00199.yml')
    expect(pagination.textContent).toContain('Showing 101–200 of 10000 matching scripts. Page 2 of 100.')
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
      .find(button => button.textContent?.includes('Inspect & review'))
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

  it('defaults to native conversion for OpenCollection YAML and saves an acknowledged per-request batch', async () => {
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

    expect(container.querySelector('input[name="bruno-output-mode"]')).toBeNull()
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
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.inspectBrunoImport).toHaveBeenCalledWith([file], expect.objectContaining({
      project_id: 'project-1',
      output_mode: 'native_requests',
    }))
    const summaryTab = container.querySelector<HTMLButtonElement>('#bruno-inspection-tab-review')
    const sourceApisTab = container.querySelector<HTMLButtonElement>('#bruno-api-selection-toggle')
    const credentialsTab = container.querySelector<HTMLButtonElement>('#bruno-inspection-tab-credentials')
    const summaryPanel = container.querySelector<HTMLElement>('#bruno-inspection-panel-review')
    const sourceApisPanel = container.querySelector<HTMLElement>('#bruno-api-selection')
    expect(summaryTab?.getAttribute('aria-selected')).toBe('true')
    expect(summaryTab?.tabIndex).toBe(0)
    expect(sourceApisTab?.getAttribute('aria-expanded')).toBe('false')
    expect(summaryPanel?.hidden).toBe(false)
    expect(sourceApisPanel?.hidden).toBe(true)

    await act(async () => sourceApisTab?.click())
    expect(summaryTab?.getAttribute('aria-selected')).toBe('true')
    expect(summaryTab?.tabIndex).toBe(0)
    expect(sourceApisTab?.getAttribute('aria-expanded')).toBe('true')
    expect(sourceApisTab?.tabIndex).toBe(0)
    expect(summaryPanel?.hidden).toBe(false)
    expect(sourceApisPanel?.hidden).toBe(false)

    summaryTab?.focus()
    await act(async () => {
      summaryTab?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
    })
    expect(credentialsTab?.getAttribute('aria-selected')).toBe('true')
    expect(credentialsTab?.tabIndex).toBe(0)
    expect(document.activeElement).toBe(credentialsTab)
    expect(container.textContent).toContain('2 API requests found → 2 native scripts')
    expect(container.textContent).toContain('Recommended configuration')
    expect(container.textContent).toContain('Keep all 2 APIs selected')
    expect(container.textContent).toContain('Configure 1 unresolved variable before execution')
    expect(container.textContent).toContain('Resolve 1 finding before saving')
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
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
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
      stayInImportReview: true,
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
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    const cards = Array.from(container.querySelectorAll<HTMLElement>('.bruno-native-acknowledgement-card'))
    expect(cards).toHaveLength(2)
    const issueButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('.bruno-native-finding-row'))
    expect(issueButtons).toHaveLength(2)
    expect(container.querySelector('.bruno-native-acknowledgements')!.compareDocumentPosition(container.querySelector('.bruno-summary-grid')!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(cards.filter(card => !card.hidden)).toHaveLength(1)
    act(() => issueButtons[1].click())
    expect(cards[1].hidden).toBe(false)
    expect(cards[0].hidden).toBe(true)
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
    expect(container.querySelector('#bruno-save-blockers')?.textContent ?? '')
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
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelector('.bruno-native-table details')).not.toBeNull()
    await act(async () => { container.querySelector<HTMLButtonElement>('#bruno-api-selection-toggle')?.click() })
    const reviewButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('.bruno-native-review-button'))
    expect(reviewButtons).toHaveLength(2)
    expect(container.querySelectorAll('.bruno-native-inspect-button')).toHaveLength(0)

    const sourceSortButton = container.querySelector<HTMLButtonElement>('button[aria-label="Sort by Source"]')
    await act(async () => sourceSortButton?.click())
    expect(container.querySelector('.bruno-native-table tbody tr')?.textContent).toContain('CTA/first.yml')
    await act(async () => sourceSortButton?.click())
    expect(container.querySelector('.bruno-native-table tbody tr')?.textContent).toContain('CTA/second.yml')
    await act(async () => sourceSortButton?.click())

    await act(async () => {
      reviewButtons[0].focus()
      reviewButtons[0].click()
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    await act(async () => {
      Array.from(document.body.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find(button => button.textContent === 'Inspect sanitized source')?.click()
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    let dialog = document.body.querySelector<HTMLElement>('[role="dialog"]')
    expect(dialog?.textContent).toContain('CTA/first.yml')
    expect(dialog?.textContent).toContain('<source-xml/>')
    expect(dialog?.querySelector('.bruno-native-source-viewer .cm-editor')).not.toBeNull()
    expect(dialog?.querySelector('.bruno-native-source-viewer .cm-lineWrapping')).toBeNull()
    expect(dialog?.querySelector('[aria-label="YAML source API"]')).not.toBeNull()
    expect(dialog?.querySelector('.apex-embedded-xml-tag')).not.toBeNull()
    expect(apiMock.getBrunoImportSessionSource).toHaveBeenCalledWith(
      'session-1',
      'project-1',
      'CTA/first.yml',
    )
    const inspectCloseButton = Array.from(dialog?.querySelectorAll<HTMLButtonElement>('button') || [])
      .find(button => button.textContent === 'Close')
    await act(async () => inspectCloseButton?.click())

    await act(async () => {
      reviewButtons[0].focus()
      reviewButtons[0].click()
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    dialog = document.body.querySelector<HTMLElement>('[role="dialog"]')
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
    expect(dialog?.textContent).toContain('Masked APEX Load DSL script')
    expect(dialog?.textContent).toContain('First API')
    expect(dialog?.querySelector('[aria-label="Source mappings JSON"] .cm-editor')).not.toBeNull()
    expect(dialog?.querySelector('[aria-label="Masked APEX Load DSL script JSON"] .cm-editor')).not.toBeNull()
    expect(dialog?.querySelector('[aria-label="Masked APEX Load DSL script JSON"] .apex-dsl-section-key')).not.toBeNull()
    expect(dialog?.querySelectorAll('.bruno-native-review-section pre.bruno-json')).toHaveLength(0)
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
    expect(dialog?.textContent).toContain('Original inspection findings are retained below.')
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
      .find(button => button.textContent?.includes('Inspect & review'))
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

  it('lets a non-admin editor create the required script folder and save a native collection', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts = [inspection.result.scripts[0]]
    inspection.result.fidelity.executable_items = 1
    apiMock.getAuthEffectiveAccess.mockResolvedValueOnce({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [{
        id: 'scripts_and_imports',
        name: 'Scripts, Imports, and Replay',
        description: 'Script access',
        operations: [
          { id: 'manage_projects', label: 'Manage projects', allowed: false },
          { id: 'create_script_folders', label: 'Create script-library folders', allowed: true },
        ],
      }],
    })
    apiMock.listFolders.mockResolvedValueOnce({ folders: [], count: 0 })
    apiMock.createFolder.mockResolvedValueOnce({
      id: 'folder-editor-import',
      project_id: 'project-1',
      parent_id: null,
      name: 'Editor Imports',
      category: 'scripts',
      created_at: '2026-07-16T19:00:00Z',
      updated_at: '2026-07-16T19:00:00Z',
    })
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })

    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
      await new Promise(resolve => setTimeout(resolve, 0))
    })

    const folderName = Array.from(container.querySelectorAll<HTMLInputElement>('input'))
      .find(input => input.parentElement?.textContent?.includes('New script folder name'))
    expect(folderName).toBeDefined()
    expect(container.textContent).not.toContain('cannot create project folders')
    act(() => setInput(folderName!, 'Editor Imports'))
    const createButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Create and select folder'))
    await act(async () => {
      createButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
    expect(apiMock.createFolder).toHaveBeenCalledWith('project-1', { name: 'Editor Imports', category: 'scripts' })
    expect(saveButton?.disabled).toBe(false)
    expect(container.querySelector('#bruno-save-blockers')).toBeNull()
  })

  it('creates the suggested script folder on save instead of blocking when none exists yet', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts = [inspection.result.scripts[0]]
    inspection.result.fidelity.executable_items = 1
    apiMock.listFolders.mockResolvedValueOnce({ folders: [], count: 0 })
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    const file = new File(['version: 1'], 'Activate_Subscriber.yml', { type: 'application/yaml' })

    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
      await new Promise(resolve => setTimeout(resolve, 0))
    })
    expect(container.querySelector<HTMLSelectElement>('#bruno-destination-folder')?.selectedOptions[0]?.textContent)
      .toBe('New folder “Activate Subscriber” (created on save)')
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Saving creates the script folder “Activate Subscriber”')
    expect(container.querySelector('#bruno-save-blockers')).toBeNull()
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
    expect(saveButton?.disabled).toBe(false)
    expect(apiMock.createFolder).not.toHaveBeenCalled()
    await act(async () => {
      saveButton?.click()
      await new Promise(resolve => setTimeout(resolve, 0))
    })

    expect(apiMock.createFolder).toHaveBeenCalledWith('project-1', { name: 'Activate Subscriber', category: 'scripts' })
    expect(apiMock.commitBrunoImport).toHaveBeenCalledWith(expect.objectContaining({ folder_id: 'folder-created' }))
  })

  it('reuses a folder already named after the collection instead of creating a duplicate', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts = [inspection.result.scripts[0]]
    inspection.result.fidelity.executable_items = 1
    apiMock.listFolders.mockResolvedValueOnce({ folders: [{ id: 'folder-existing', project_id: 'project-1', parent_id: null, name: 'Activate Subscriber', category: 'scripts', created_at: '2026-07-16T19:00:00Z', updated_at: '2026-07-16T19:00:00Z' }], count: 1 })
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    const file = new File(['version: 1'], 'Activate_Subscriber.yml', { type: 'application/yaml' })
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
      await new Promise(resolve => setTimeout(resolve, 0))
    })
    expect(container.querySelector<HTMLSelectElement>('#bruno-destination-folder')?.value).toBe('folder-existing')
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => { inspectButton?.click(); await Promise.resolve(); await Promise.resolve() })
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
    await act(async () => { saveButton?.click(); await new Promise(resolve => setTimeout(resolve, 0)) })
    expect(apiMock.createFolder).not.toHaveBeenCalled()
    expect(apiMock.commitBrunoImport).toHaveBeenCalledWith(expect.objectContaining({ folder_id: 'folder-existing' }))
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
      .find(button => button.textContent?.includes('Inspect & review'))!
    await act(async () => {
      inspectButton.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const destination = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.parentElement?.textContent?.includes('Destination folder'))!
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))!
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
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
    const blockers = container.querySelector<HTMLElement>('#bruno-save-blockers')
    expect(saveButton?.disabled).toBe(true)
    expect(saveButton?.getAttribute('aria-describedby')).toBe('bruno-save-blockers')
    expect(blockers?.textContent).toContain('No destination folder is available, and your current role cannot create project folders')
    expect(blockers?.textContent).toContain('Review CTA/second.yml: Translate unsupported response JavaScript.')
    expect(blockers?.textContent).toContain('1 unresolved runtime variable does not prevent saving')
    expect(apiMock.createFolder).not.toHaveBeenCalled()
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
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
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
      .find(button => button.textContent?.includes('Inspect & review'))
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
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
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
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
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
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
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
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
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
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain(message)
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
    expect(saveButton?.disabled).toBe(true)
    await act(async () => saveButton?.click())
    expect(apiMock.commitBrunoImport).not.toHaveBeenCalled()
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
      .find(button => button.textContent?.includes('Inspect & review'))!
    await act(async () => {
      inspect.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelector('#bruno-save-blockers')?.textContent).toMatch(/Select an existing destination folder|Create and select a destination folder|No destination folder is available/)
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
    expect(save?.disabled).toBe(true)
    expect(apiMock.commitBrunoImport).not.toHaveBeenCalled()
  })

  it('shows a mixed select-all state for a partial native selection', async () => {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    apiMock.inspectBrunoImport.mockResolvedValueOnce(nativeInspectionFixture())
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" />)
      await Promise.resolve()
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect & review'))
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

  async function renderAndInspect(inspection: ReturnType<typeof nativeInspectionFixture>, folderId?: string) {
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" folderId={folderId} />)
      await Promise.resolve()
    })
    const inspectButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      inspectButton?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
  }

  function saveDraftButton() {
    return Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => (button.textContent?.includes('Save draft') || /^Save \d+ drafts?$/.test(button.textContent || '')))
  }

  function previouslyImportedFixture() {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts[1].diagnostics = []
    inspection.result.scripts.forEach((candidate, index) => {
      ;(candidate as typeof candidate & { existing_script: unknown }).existing_script = {
        script_id: `existing-${index + 1}`,
        name: `Existing ${index + 1}`,
        ref_key: `LOC-SCR-000${index + 1}-v1`,
        folder_id: 'folder-1',
        updated_at: `2026-10-14T09:0${index}:00.123456789Z`,
        matches: index === 1 ? 2 : 1,
        same_collection: true,
        edited_since_import: index === 1,
      }
    })
    return inspection
  }

  // A desktop drop still being read when Save starts yields to the save: the
  // save keeps its result and the page does not stay busy, whether the read
  // lands during the save or after it.
  it.each([['during', false], ['after', true]])('ignores a desktop drop that lands %s a save', async (_when, saveFirst) => {
    type DesktopWindow = typeof window & { go?: unknown; runtime?: unknown }
    const desktopWindow = window as DesktopWindow
    let drop: ((x: number, y: number, paths: string[]) => void) | undefined
    let finishRead: (raw: string) => void = () => undefined
    desktopWindow.go = { main: { App: { ReadDroppedDirectory: vi.fn(() => new Promise<string>(resolve => { finishRead = resolve })) } } }
    desktopWindow.runtime = { OnFileDrop: (callback: (x: number, y: number, paths: string[]) => void) => { drop = callback } }
    const originalElementFromPoint = document.elementFromPoint
    try {
      const inspection = previouslyImportedFixture()
      let finishCommit: (receipt: unknown) => void = () => undefined
      apiMock.commitBrunoImport.mockImplementationOnce(() => new Promise(resolve => { finishCommit = resolve }))
      await renderAndInspect(inspection, 'folder-1')
      const changeSource = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Change source or destination')
      await act(async () => { changeSource?.click() })
      const heading = Array.from(container.querySelectorAll('h3')).find(item => item.textContent === '1. Source bundle')!
      ;(document as unknown as { elementFromPoint: () => Element }).elementFromPoint = () => heading
      await act(async () => { drop?.(10, 10, ['/Users/me/Other Collection']) })
      await act(async () => {
        saveDraftButton()?.click()
        await Promise.resolve()
      })
      const read = async () => { await act(async () => { finishRead(JSON.stringify({ files: [{ path: 'Other Collection/opencollection.yml', base64: btoa('x') }] })) }) }
      const save = async () => {
        await act(async () => {
          finishCommit({
            batch_id: 'native-batch', created: true, idempotent_replay: false, count: 2, _http_status: 201,
            updated_script_ids: ['existing-1', 'existing-2'],
            scripts: inspection.result.scripts.map((candidate, index) => ({ ...candidate.script, id: `existing-${index + 1}` })),
          })
          await Promise.resolve()
        })
        await vi.waitFor(() => expect(container.textContent).toContain('Saved 2 scripts to the script library.'))
      }
      if (saveFirst) { await save(); await read() } else { await read(); await save() }
      expect(container.textContent).toContain('Saved 2 scripts to the script library.')
      expect(container.textContent).not.toContain('Other Collection/opencollection.yml')
    } finally {
      ;(document as unknown as { elementFromPoint: typeof document.elementFromPoint }).elementFromPoint = originalElementFromPoint
      delete desktopWindow.go
      delete desktopWindow.runtime
    }
  })

  it('updates previously imported scripts as a new version by default', async () => {
    const inspection = previouslyImportedFixture()
    apiMock.commitBrunoImport.mockResolvedValueOnce({
      batch_id: 'native-batch', created: true, idempotent_replay: false, count: 2, _http_status: 201,
      updated_script_ids: ['existing-1', 'existing-2'],
      scripts: inspection.result.scripts.map((candidate, index) => ({ ...candidate.script, id: `existing-${index + 1}` })),
    })
    await renderAndInspect(inspection, 'folder-1')

    expect(container.textContent).toContain('2 of 2 already imported. 2 will be updated as a new version; choose Create a copy or Skip on any row. 1 of those changed after import; updating replaces their steps and settings but keeps their names and folders.')
    expect(container.textContent).toContain('Already imported: 2 · 2 will be updated as a new version · 1 changed since import')
    expect(container.textContent).toContain('Imported before as Existing 1 (LOC-SCR-0001-v1)')
    expect(container.textContent).toContain('2 earlier scripts match; the most recent one from this collection is offered')
    expect(container.querySelectorAll('.bruno-native-existing-warning')).toHaveLength(1)
    expect(container.textContent).toContain('Saving updates 2 existing scripts in place as a new version, atomically. No folder is used.')
    const choice = container.querySelector<HTMLSelectElement>('select[aria-label="On save for CTA/first.yml"]')
    expect(choice?.value).toBe('update')
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Name for CTA/first.yml"]')?.value).toBe('Existing 1')

    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const payload = apiMock.commitBrunoImport.mock.calls[0][0]
    expect(payload).not.toHaveProperty('folder_id')
    expect(payload.update_targets).toEqual({
      'candidate-1': { script_id: 'existing-1', expected_updated_at: '2026-10-14T09:00:00.123456789Z' },
      'candidate-2': { script_id: 'existing-2', expected_updated_at: '2026-10-14T09:01:00.123456789Z' },
    })
    expect(payload.name_overrides).toEqual({ 'candidate-1': 'Existing 1', 'candidate-2': 'Existing 2' })
    expect(container.textContent).toContain('2 existing scripts updated as a new version. The batch was saved atomically.')
    expect(container.textContent).not.toContain('will be updated as a new version')
  })

  it('saves an update-only re-import even when folders cannot be loaded', async () => {
    apiMock.listFolders.mockRejectedValue(new Error('folders unavailable'))
    const inspection = previouslyImportedFixture()
    apiMock.commitBrunoImport.mockResolvedValueOnce({
      batch_id: 'native-batch', created: true, idempotent_replay: false, count: 2, _http_status: 201,
      updated_script_ids: ['existing-1', 'existing-2'],
      scripts: inspection.result.scripts.map((candidate, index) => ({ ...candidate.script, id: `existing-${index + 1}` })),
    })
    await renderAndInspect(inspection)

    expect(container.textContent).not.toContain('Destination folders could not be loaded')
    expect(saveDraftButton()?.disabled).toBe(false)
    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(apiMock.commitBrunoImport.mock.calls[0][0]).not.toHaveProperty('folder_id')
  })

  it('creates a copy or skips a previously imported row on request', async () => {
    const inspection = previouslyImportedFixture()
    apiMock.commitBrunoImport.mockResolvedValueOnce({
      batch_id: 'native-batch', created: true, idempotent_replay: false, count: 1, _http_status: 201,
      updated_script_ids: [],
      scripts: [{ ...inspection.result.scripts[0].script, id: 'planned-1' }],
    })
    await renderAndInspect(inspection, 'folder-1')

    const first = container.querySelector<HTMLSelectElement>('select[aria-label="On save for CTA/first.yml"]')!
    const second = container.querySelector<HTMLSelectElement>('select[aria-label="On save for CTA/second.yml"]')!
    await act(async () => {
      first.value = 'copy'
      first.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      second.value = 'skip'
      second.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Select Second API"]')?.checked).toBe(false)
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Name for CTA/first.yml"]')?.value).toBe('First API')

    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const payload = apiMock.commitBrunoImport.mock.calls[0][0]
    expect(payload.folder_id).toBe('folder-1')
    expect(payload.candidate_ids).toEqual(['candidate-1'])
    expect(payload).not.toHaveProperty('update_targets')
    expect(container.textContent).toContain('1 native APEX script created. The batch was saved atomically.')
  })

  it('defaults a match on the request path alone to a copy', async () => {
    const inspection = previouslyImportedFixture()
    inspection.result.scripts.forEach(candidate => {
      ;(candidate as typeof candidate & { existing_script: { same_collection: boolean } }).existing_script.same_collection = false
    })
    await renderAndInspect(inspection, 'folder-1')

    expect(container.querySelector<HTMLSelectElement>('select[aria-label="On save for CTA/first.yml"]')?.value).toBe('copy')
    expect(container.textContent).toContain('Matched by request path only, so it may belong to another collection.')
    expect(container.textContent).toContain('2 match scripts only by request path and default to Create a copy. To update an earlier script instead, choose Update existing on its row.')
    expect(container.textContent).toContain('Path-only matches: 2 · Selected APIs are saved as new scripts')
    expect(container.textContent).not.toContain('already imported.')
    expect(container.textContent).toContain('New scripts will be saved atomically to the selected folder.')
    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const payload = apiMock.commitBrunoImport.mock.calls[0][0]
    expect(payload).not.toHaveProperty('update_targets')
    expect(payload.folder_id).toBe('folder-1')
  })

  it('fixes an invalid header on save instead of asking for acknowledgement', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts[1].diagnostics = [{
      id: 'finding-cookie',
      code: 'bruno_invalid_header_value',
      severity: 'warning',
      message: 'The Cookie header contains characters HTTP does not allow, starting at a double quote ("), so the request cannot be sent as imported.',
      acknowledgement_required: true,
      header_repair: {
        action_index: 0,
        header_index: 1,
        header_name: 'Cookie',
        options: ['trim', 'remove'],
        kept_characters: 11,
        removed_characters: 40,
        invalid_character: 'a double quote (")',
      },
    } as never]
    await renderAndInspect(inspection, 'folder-1')

    expect(container.textContent).toContain('Header contains characters HTTP does not allow')
    expect(saveDraftButton()?.disabled).toBe(true)
    const trim = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="radio"]'))
      .find(input => input.parentElement?.textContent?.includes('Keep the first 11 characters and remove the 40 from a double quote (") onward'))
    expect(trim).toBeDefined()
    await act(async () => trim?.click())
    expect(container.textContent).toContain('This header is fixed when you save, so no acknowledgement is needed.')
    expect(saveDraftButton()?.disabled).toBe(false)

    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const payload = apiMock.commitBrunoImport.mock.calls[0][0]
    expect(payload.header_repairs).toEqual({ 'finding-cookie': 'trim' })
    expect(payload.acknowledged_finding_ids).toEqual([])
  })
  it('generates recorded IDs and time stamps each iteration unless a row is unchecked', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts[1].diagnostics = []
    const suggestion = (id: string, ruleId: string, varName: string, rationale: string, autoApply = true) => ({
      id, kind: 'generated', var_name: varName, replacement: `{{var.${varName}}}`, confidence: autoApply ? 0.85 : 0.6,
      rule_id: ruleId, rationale, auto_apply: autoApply, high_risk: false,
    })
    const trace = 'The idp-trace-id header sends the same recorded hex identifier every time. Generate a new random hex value of the same length each iteration.'
    const stamp = 'The dateTimeStamp body field sends the same recorded time every time. Generate the current time in the same format each iteration.'
    const session = 'The idpctx-session-id header sends the same recorded UUID every time. Generate a new UUID each iteration. Leave it if the target expects this exact value, such as a conversation, account or session it already holds.'
    inspection.result.scripts.forEach((candidate, index) => {
      ;(candidate as Record<string, unknown>).parameterization = {
        suggestions: [
          suggestion(`pz_trace_${index}`, 'generated.hex', 'idp_trace_id', trace),
          suggestion(`pz_span_${index}`, 'generated.hex', 'idp_trace_id_2', trace),
          suggestion(`pz_stamp_${index}`, 'generated.timestamp', 'datetimestamp', stamp),
          ...(index === 0 ? [suggestion('pz_session_0', 'generated.uuid', 'idpctx_session_id', session, false)] : []),
        ],
        summary: { total: 0, by_kind: {}, auto_apply_count: 0, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
      }
    })
    await renderAndInspect(inspection, 'folder-1')

    const panel = container.querySelector('.bruno-generated-values')!
    expect(panel.textContent).toContain('Generated values')
    const rows = Array.from(panel.querySelectorAll('li'))
    // One row per field across both requests; the session row waits for review.
    expect(rows.map(row => [row.querySelector('span')?.textContent, row.querySelector<HTMLInputElement>('input')?.checked, row.querySelector('small')?.textContent])).toEqual([
      [stamp, true, '2 selected APIs'],
      [trace, true, '2 selected APIs'],
      [session, false, '1 selected API · Not checked: confirm the target does not expect this exact value'],
    ])
    await act(async () => rows[0].querySelector<HTMLInputElement>('input')?.click())

    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const payload = apiMock.commitBrunoImport.mock.calls[0][0]
    expect(payload.parameterization_selections).toEqual({
      [inspection.result.scripts[0].candidate_id]: ['pz_trace_0', 'pz_span_0'],
      [inspection.result.scripts[1].candidate_id]: ['pz_trace_1', 'pz_span_1'],
    })
  })

  it('starts a re-import from how the updated scripts supplied each input, and saves the bindings', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts[1].diagnostics = []
    const withInputs = {
      ...inspection,
      shared_values: {
        candidates: [
          { id: 'env-id', name: 'env', source_variable: 'env', kind: 'source_variable', occurrence_count: 1 },
          { id: 'ban-id', name: 'ban', source_variable: 'ban', kind: 'source_variable', occurrence_count: 1 },
        ],
        summary: {},
      },
      previous_inputs: {
        'env-id': { mode: 'data', alias: 'environments', column: 'host' },
        'ban-id': { mode: 'value', value: 'BAN-0042' },
      },
    }
    withInputs.result.scripts[0].missing_variables = ['env', 'ban']
    await renderAndInspect(withInputs as never, 'folder-1')

    const review = container.querySelector('.bruno-data-review')!
    expect(review.textContent).toContain('{{data.environments.host}}')
    expect(review.querySelectorAll('.import-input-previous')).toHaveLength(2)
    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const payload = apiMock.commitBrunoImport.mock.calls[0][0]
    expect(payload.input_bindings).toEqual({ 'env-id': { mode: 'data', alias: 'environments', column: 'host' } })
    expect(payload.shared_values).toEqual({ 'ban-id': 'BAN-0042' })
  })

  it('puts a left-out header back or leaves it out without an acknowledgement', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts[1].diagnostics = [{
      id: 'finding-left-out',
      code: 'bruno_invalid_header_value',
      severity: 'warning',
      message: 'The Cookie header contains characters HTTP does not allow, starting at a double quote ("), so the header was left out of the script.',
      acknowledgement_required: true,
      header_repair: {
        action_index: 0,
        header_index: 1,
        header_name: 'Cookie',
        options: ['trim', 'remove'],
        kept_characters: 11,
        removed_characters: 40,
        invalid_character: 'a double quote (")',
        omitted: true,
      },
    } as never]
    await renderAndInspect(inspection, 'folder-1')

    expect(container.textContent).toContain('Header left out: characters HTTP does not allow')
    expect(container.querySelector('.bruno-native-finding-queue h5')?.textContent).toContain('Header left out: characters HTTP does not allow')
    expect(container.textContent).toContain('Requests run without it.')
    expect(container.textContent).not.toContain('Don’t fix; keep the header as imported')
    expect(container.querySelector('input[aria-label^="Acknowledge Header left out"]')).toBeNull()
    expect(container.textContent).toContain('Choose one to continue.')
    expect(container.textContent).toContain('Choose whether to put it back or leave it out under Review-gated conversions.')
    expect(container.textContent).toContain('Put the header back with its first 11 characters, stopping before a double quote (")')
    expect(saveDraftButton()?.disabled).toBe(true)
    const leaveOut = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="radio"]'))
      .find(input => input.parentElement?.textContent?.includes('Leave the Cookie header out'))
    await act(async () => leaveOut?.click())
    expect(container.textContent).toContain('The header stays out of the script.')
    expect(container.textContent).toContain('· left out')
    expect(saveDraftButton()?.disabled).toBe(false)

    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const payload = apiMock.commitBrunoImport.mock.calls[0][0]
    expect(payload.header_repairs).toEqual({ 'finding-left-out': 'remove' })
    expect(payload.acknowledged_finding_ids).toEqual([])
  })

  it('reports skipped files, converted hooks and headers kept as imported', async () => {
    apiMock.listFolders.mockResolvedValue({ folders: [], count: 0 })
    const inspection = nativeInspectionFixture()
    inspection.result.scripts[1].diagnostics = []
    Object.assign(inspection.result, {
      collection: { ...inspection.result.collection, name: 'CTA_Collection', name_declared: true },
      capabilities: { ...inspection.result.capabilities, has_javascript: false, converted_hooks: 6, unconverted_hooks: 0 },
      skipped_files: [
        { path: 'CTA/.DS_Store', reason: 'operating_system_file' },
        { path: 'CTA/.gitignore', reason: 'version_control_file' },
      ],
      skipped_file_count: 5,
      kept_headers: [{ name: 'Cookie', requests: 1 }, { name: 'idpctx-session-id', requests: 2 }],
    })
    await renderAndInspect(inspection)

    expect(container.textContent).toContain('CTA_Collection · 2 requests · 5 files skipped')
    expect(container.textContent).toContain('…and 3 more')
    expect(container.textContent).toContain('Destination: New folder “CTA_Collection”, created on save')
    expect(container.textContent).toContain('6 JavaScript hooks converted to native steps and checks')
    expect(container.textContent).not.toContain('Conversion limits')
    expect(container.textContent).toContain('operating-system file')
    expect(container.textContent).toContain('version-control file')
    expect(container.textContent).toContain('Kept as imported: 2 headers')
    expect(container.textContent).toContain('idpctx-session-id 2 requests')
    expect(container.textContent).toContain('Cookie 1 request')
  })

  it('opens the DSL preview at the request and lists the converted runtime settings', async () => {
    const inspection = nativeInspectionFixture()
    Object.assign(inspection.result.scripts[0].script, {
      metadata: { import: { source: 'bruno', preserved: { effective_auth: 'basic', effective_fields: { encode_url: true }, collection_sha256: 'collection-hash' } } },
      config: { base_url: '', http: { cookies_enabled: true, clear_cookies_per_iteration: true, traceparent: false }, run_logic: { auto_transactions: 'none' } },
      actions: [{ name: 'First API', type: 'http', method: 'POST', target: 'https://{{var.env}}/first', timeout_ms: 30000, max_redirects: 5 }],
    })
    inspection.result.scripts[0].diagnostics = [{
      id: 'timeout-defaulted', code: 'bruno_unbounded_timeout_defaulted', severity: 'info',
      message: 'Bruno timeout 0 became the APEX default.', acknowledgement_required: false,
    } as never]
    await renderAndInspect(inspection, 'folder-1')

    await act(async () => container.querySelector<HTMLButtonElement>('#bruno-inspection-tab-runtime')?.click())
    const runtime = container.querySelector<HTMLElement>('#bruno-inspection-panel-runtime')
    expect(runtime?.textContent).toContain('30,000 ms (Bruno’s 0, no limit, became the APEX default)')
    expect(runtime?.textContent).toContain('Up to 5')
    expect(runtime?.textContent).toContain('Encoded, as Bruno does')
    expect(runtime?.textContent).toContain('Kept within an iteration, cleared between iterations')
    expect(runtime?.textContent).toContain('AuthenticationBasic')
    expect(runtime?.textContent).toContain('One per iteration, named “First API”, excluding think time')
    // The transaction takes the name the script is saved under.
    const nameInput = container.querySelector<HTMLInputElement>('input[aria-label="Name for CTA/first.yml"]')!
    await act(async () => setInput(nameInput, 'Orders lookup'))
    expect(runtime?.textContent).toContain('One per iteration, named “Orders lookup”, excluding think time')

    await act(async () => container.querySelector<HTMLButtonElement>('#bruno-inspection-tab-dsl')?.click())
    const preview = container.querySelector<HTMLElement>('[aria-label="Bruno DSL preview"]')
    expect(preview?.textContent).toContain('"actions"')
    expect(preview?.textContent).not.toContain('collection-hash')
    expect(container.querySelector('.bruno-provenance summary')?.textContent).toBe('Import provenance and hashes')

    await act(async () => container.querySelector<HTMLButtonElement>('#bruno-inspection-tab-source')?.click())
    const mappings = container.querySelector<HTMLTableElement>('.bruno-source-mapping-table')
    expect(mappings?.textContent).toContain('CTA/folder.yml')
    expect(mappings?.textContent).not.toContain('folder-sha')
  })

  it('says when the requests’ own tests expect an error response', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts.forEach(candidate => Object.assign(candidate.script.actions[0], {
      assertions: [{ type: 'status_code', expected: 500 }, { type: 'header_contains', value: 'text/xml' }],
    }))
    await renderAndInspect(inspection, 'folder-1')
    expect(container.querySelector('.bruno-intent')?.textContent)
      .toBe('2 of 2 requests expect an error response (HTTP 500). A run passes when the target answers with that error.')
  })

  it('says when an imported auth mode needs translation', async () => {
    const inspection = nativeInspectionFixture()
    Object.assign(inspection.result.scripts[0].script, {
      metadata: { import: { source: 'bruno', preserved: { effective_auth: 'unsupported' } } },
    })
    await renderAndInspect(inspection, 'folder-1')
    await act(async () => container.querySelector<HTMLButtonElement>('#bruno-inspection-tab-runtime')?.click())
    expect(container.querySelector('#bruno-inspection-panel-runtime')?.textContent)
      .toContain('AuthenticationNot supported; needs translation before replay')
  })

  it('reports project secrets the server says still need values after saving', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts[1].diagnostics = []
    apiMock.commitBrunoImport.mockResolvedValueOnce({
      batch_id: 'native-batch', created: true, idempotent_replay: false, count: 2, _http_status: 201,
      unconfigured_secret_names: ['PARTNER_PASSWORD'],
      scripts: inspection.result.scripts.map(candidate => ({ ...candidate.script, id: candidate.planned_script_id })),
    })
    await renderAndInspect(inspection, 'folder-1')
    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Save: Saved')
    expect(container.textContent).toContain('Credentials: 1 not configured')
    expect(container.textContent).toContain('Replay: Blocked')
    const unset = container.querySelector<HTMLElement>('.bruno-unset-secrets')
    expect(unset?.textContent).toContain('PARTNER_PASSWORD')
    expect(Array.from(unset?.querySelectorAll('button') || []).some(button => button.textContent === 'Open Secrets')).toBe(true)
  })

  it('creates one scenario draft from the saved scripts and opens it in the builder', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts[1].diagnostics = []
    apiMock.createScenarioDraft.mockResolvedValueOnce({ id: 'draft-1', name: 'SOAP request', revision: 1 })
    const file = new File(['version: 1'], 'opencollection.yml', { type: 'application/yaml' })
    apiMock.inspectBrunoImport.mockResolvedValueOnce(inspection)
    await act(async () => {
      root.render(<BrunoImport initialFiles={[file]} projectId="project-1" folderId="folder-1" />)
      await Promise.resolve()
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Inspect & review'))?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    const create = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Create a scenario from these 2 scripts')
    expect(create).toBeDefined()
    await act(async () => {
      create!.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(apiMock.createScenarioDraft).toHaveBeenCalledOnce()
    const [projectId, draft] = apiMock.createScenarioDraft.mock.calls[0]
    expect(projectId).toBe('project-1')
    expect(draft.name).toBe('SOAP request')
    expect(draft.document.form.groups.map((group: { script_id: string; vusers: number }) => [group.script_id, group.vusers])).toEqual([['planned-1', 1], ['planned-2', 1]])
    expect(window.location.hash).toContain('draftId=draft-1')
    // The link names the draft's project, which owns it.
    expect(window.location.hash).toContain('projectId=project-1')
    window.location.hash = ''
  })

  it('says project secrets need checking when a save could not report them', async () => {
    const inspection = nativeInspectionFixture()
    inspection.result.scripts[1].diagnostics = []
    apiMock.commitBrunoImport.mockResolvedValueOnce({
      batch_id: 'native-batch', created: false, idempotent_replay: true, count: 2, _http_status: 200,
      scripts: inspection.result.scripts.map(candidate => ({ ...candidate.script, id: candidate.planned_script_id })),
    })
    await renderAndInspect(inspection, 'folder-1')
    await act(async () => {
      saveDraftButton()?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(container.textContent).toContain('Credentials: check project secrets')
    // Its unresolved input still blocks replay; unknown secrets never read as ready.
    expect(container.textContent).toContain('Replay: Blocked')
    expect(container.querySelector('.bruno-unset-secrets')?.textContent).toContain('could not be checked')
  })
})

describe('BrunoImport in the desktop app', () => {
  type DesktopWindow = typeof window & {
    go?: { main?: { App?: Record<string, unknown> } }
    runtime?: { OnFileDrop?: (callback: (x: number, y: number, paths: string[]) => void, useDropTarget: boolean) => void }
  }
  let container: HTMLDivElement
  let root: Root
  let drop: ((x: number, y: number, paths: string[]) => void) | undefined
  const originalElementFromPoint = document.elementFromPoint

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getBrunoSupportMatrix.mockResolvedValue({ compatibility_version: '3.5.2', import_enabled: true, safe_execution_enabled: false, developer_execution_enabled: false, native_projection_enabled: false })
    apiMock.getAuthEffectiveAccess.mockResolvedValue({ enabled: true, authenticated: true, project_id: 'project-1', resources: [] })
    apiMock.listFolders.mockResolvedValue({ folders: [], count: 0 })
    ;(window as DesktopWindow).go = { main: { App: {
      ReadDroppedDirectory: vi.fn(async () => JSON.stringify({ files: [
        { path: 'Orders Collection/opencollection.yml', base64: btoa('opencollection: 1.0.0') },
        { path: 'Orders Collection/orders/get-order.yml', base64: btoa('info: {}') },
      ] })),
    } } }
    ;(window as DesktopWindow).runtime = { OnFileDrop: callback => { drop = callback } }
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    delete (window as DesktopWindow).go
    delete (window as DesktopWindow).runtime
    ;(document as unknown as { elementFromPoint: typeof document.elementFromPoint }).elementFromPoint = originalElementFromPoint
    drop = undefined
    vi.resetAllMocks()
  })

  // The shell turns off the page's own drops, so a dropped collection folder
  // arrives as a native path and is read through the bridge, like a folder pick.
  it('takes a collection folder dropped on the source card', async () => {
    await act(async () => {
      root.render(<BrunoImport projectId="project-1" />)
      await Promise.resolve()
    })
    const heading = Array.from(container.querySelectorAll('h3')).find(item => item.textContent === '1. Source bundle')!
    expect(heading.closest('section')?.textContent).toContain('or drop one here')
    ;(document as unknown as { elementFromPoint: () => Element }).elementFromPoint = () => heading
    expect(drop).toBeTypeOf('function')
    await act(async () => {
      drop?.(10, 10, ['/Users/me/Orders Collection'])
    })
    await vi.waitFor(() => expect(container.querySelector('.bruno-stat')?.textContent).toContain('2 files'))
    expect(container.textContent).toContain('Orders Collection/orders/get-order.yml')
  })

  // A slow drop that finishes after a later one does not replace it.
  it('keeps the latest dropped collection', async () => {
    let finishSlow: (raw: string) => void = () => undefined
    ;(window as DesktopWindow).go!.main!.App!.ReadDroppedDirectory = vi.fn((path: string) => path.endsWith('Slow')
      ? new Promise<string>(resolve => { finishSlow = resolve })
      : Promise.resolve(JSON.stringify({ files: [{ path: 'Fast/opencollection.yml', base64: btoa('opencollection: 1.0.0') }] })))
    await act(async () => {
      root.render(<BrunoImport projectId="project-1" />)
      await Promise.resolve()
    })
    const heading = Array.from(container.querySelectorAll('h3')).find(item => item.textContent === '1. Source bundle')!
    ;(document as unknown as { elementFromPoint: () => Element }).elementFromPoint = () => heading
    await act(async () => { drop?.(10, 10, ['/Users/me/Slow']) })
    await act(async () => { drop?.(10, 10, ['/Users/me/Fast']) })
    await vi.waitFor(() => expect(container.textContent).toContain('Fast/opencollection.yml'))
    await act(async () => { finishSlow(JSON.stringify({ files: [{ path: 'Slow/opencollection.yml', base64: btoa('x') }] })) })
    expect(container.textContent).toContain('Fast/opencollection.yml')
    expect(container.textContent).not.toContain('Slow/opencollection.yml')
  })

  // A drop with nothing to import says so on the card it was dropped on.
  it('reports an empty drop on the source card', async () => {
    ;(window as DesktopWindow).go!.main!.App!.ReadDroppedDirectory = vi.fn(async () => JSON.stringify({ files: [] }))
    await act(async () => {
      root.render(<BrunoImport projectId="project-1" />)
      await Promise.resolve()
    })
    const heading = Array.from(container.querySelectorAll('h3')).find(item => item.textContent === '1. Source bundle')!
    ;(document as unknown as { elementFromPoint: () => Element }).elementFromPoint = () => heading
    await act(async () => {
      drop?.(10, 10, ['/Users/me/Empty Collection'])
    })
    await vi.waitFor(() => expect(heading.closest('section')?.querySelector('[role="alert"]')?.textContent).toContain('Nothing to import was found there'))
  })
})
