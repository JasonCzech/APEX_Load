// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ProtocolAssetImport from './ProtocolAssetImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  importDevWeb: vi.fn(),
  importHAR: vi.fn(),
  importSalesforce: vi.fn(),
  importBrowserRecording: vi.fn(),
  importOpenAPI: vi.fn(),
  importRAML: vi.fn(),
  getRAMLSupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  importPostman: vi.fn(),
  importWSDL: vi.fn(),
  importAsyncAPI: vi.fn(),
  createScript: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('./LinkedCodePane', () => ({
  default: ({ value, ariaLabel }: { value: string; ariaLabel?: string }) => (
    <pre aria-label={ariaLabel}>{value}</pre>
  ),
}))

describe('ProtocolAssetImport', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    if (!HTMLElement.prototype.scrollIntoView) {
      Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
        configurable: true,
        value: vi.fn(),
      })
    }

    apiMock.importOpenAPI.mockResolvedValue({
      scripts: [],
      script: {
        name: 'Imported Orders API',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://api.example.test' },
        actions: [
          { name: 'Create Order', type: 'request', method: 'POST', target: '/orders' },
        ],
      },
      warnings: [],
      unsupported_elements: [
        'OpenAPI schema $ref at POST /orders [warning]: schema reference external.yaml could not be resolved',
      ],
      assumptions: [],
      review_sections: [
        {
          key: 'import_diagnostics_warning',
          title: 'Import Diagnostics',
          severity: 'warning',
          summary: '1 import diagnostic requires review.',
          items: ['OpenAPI multipart file at POST /orders [warning]: file bytes must be supplied before replay'],
        },
      ],
      repair_suggestions: [],
      correlations: [],
      stats: {
        format: 'openapi',
        total_paths: 1,
        total_operations: 1,
        converted_actions: 1,
        request_bodies: 1,
        assertions: 1,
        parameters_detected: 0,
      },
      summary: {
        title: 'Orders API',
        operation_names: ['Create Order'],
      },
      source_review: {
        source_files: [{ path: 'openapi.json', language: 'json', content: '{"paths":{"/orders":{"post":{}}}}' }],
        mappings: [{ id: 'openapi-post', source_file: 'openapi.json', start_line: 1, function: 'POST /orders', target_path: 'actions[0]', target_name: 'Create Order', target_kind: 'action', status: 'converted' }],
      },
    })
    apiMock.importHAR.mockResolvedValue({
      scripts: [],
      script: {
        name: 'Imported HAR',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://shop.example.test' },
        actions: [
          { name: 'GET api orders', type: 'request', method: 'GET', target: '/api/orders' },
        ],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: {
        format: 'har',
        total_requests: 2,
        converted_actions: 1,
        filtered_assets: 1,
        assertions: 1,
        extractors: 0,
        correlations_detected: 0,
      },
      summary: {
        artifact_mode: 'design_har_pair',
        artifact_sensitivity: 'scrubbed',
        retention_days: 7,
        governance: {
          artifact_mode: 'design_har_pair',
          sensitivity: 'scrubbed',
          retention_days: 7,
          redaction_enabled: true,
          body_capture_required: true,
          scope: 'http_family_only',
        },
        har_diff: {
          artifact_mode: 'design_har_pair',
          compared: true,
          baseline_correlations: 1,
          comparison_correlations: 1,
          dynamic_candidates: 1,
          static_candidates: 0,
          unmatched_candidates: 0,
          classifications: [
            { name: 'corr_cartid', classification: 'dynamic', diff_confidence: 0.95, reason: 'changed between recordings' },
          ],
        },
      },
      source_review: {
        source_files: [{ path: 'sample.har', language: 'json', content: '{"log":{"entries":[]}}' }],
        mappings: [{ id: 'har-entry', source_file: 'sample.har', start_line: 1, function: 'har_entry', target_path: 'actions[0]', target_name: 'GET api orders', target_kind: 'action', status: 'converted' }],
      },
    })
    apiMock.importSalesforce.mockResolvedValue({
      scripts: [],
      script: {
        name: 'Imported Salesforce',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://acme.my.salesforce.com' },
        actions: [
          { name: 'Aura Bootstrap', type: 'request', method: 'POST', target: '/aura' },
        ],
        metadata: {
          import: {
            preserved: {
              agentforce_review: {
                detected: true,
                streaming: true,
                lifecycle: [{ step: 'session_start', method: 'POST', path: '/einstein/ai-agent/v1/sessions' }],
                session_vars: [{ name: 'agent_session_id', source: 'body', type: 'jsonpath', expression: '$.sessionId' }],
              },
            },
          },
        },
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [{
        key: 'salesforce_review',
        title: 'Salesforce Native Review',
        severity: 'info',
        summary: 'Salesforce traffic detected.',
        items: ['Aura and Lightning endpoints were detected.'],
      }],
      repair_suggestions: [],
      correlations: [{ name: 'aura_token', source: 'body', expression: 'aura.token', found_in_seq: 1 }],
      stats: {
        format: 'har',
        total_requests: 2,
        converted_actions: 1,
        filtered_assets: 0,
        assertions: 1,
        extractors: 1,
        correlations_detected: 1,
      },
      summary: {
        artifact_mode: 'salesforce_native_http',
        salesforce_review: {
          detected: true,
          domains: [{ host: 'acme.my.salesforce.com', kind: 'lightning', count: 2 }],
          aura_endpoints: [{ method: 'POST', path: '/aura', host: 'acme.my.salesforce.com', count: 2 }],
          lightning_endpoints: [{ method: 'GET', path: '/lightning', host: 'acme.my.salesforce.com', count: 1 }],
          apex_rest_endpoints: [{ method: 'GET', path: '/services/apexrest/Acct', host: 'acme.my.salesforce.com', count: 3 }],
          composite_endpoints: [{ method: 'POST', path: '/services/data/v60.0/composite', host: 'acme.my.salesforce.com', count: 2 }],
          parameterizations: [{ name: 'param_recordid', source: 'aura_message', expression: 'actions[0].params.recordId', param_key: 'recordId', sample_value: '001D000000IqhSLIAZ', kind: 'record_id', column: 'recordid', template: '{{data.salesforce_records.recordid}}' }],
          transactions: [{ name: 'Salesforce Login', source: 'har', request_count: 2, aura_actions: 1 }],
          correlation_confidence: 'high',
          har_match_coverage: { har_provided: true, imported_requests: 2, matched_requests: 2, coverage_pct: 100 },
          next_actions: ['Review Salesforce correlation extractors before approval.'],
        },
      },
    })
    apiMock.importRAML.mockResolvedValue({
      scripts: [],
      script: {
        name: 'Imported RAML',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://api.example.test' },
        init: [{ name: 'Get OAuth2 Token', type: 'http_oauth2_token', target: '', params: { token_url: 'https://auth.example.test/token' } }],
        actions: [
          { name: 'GET /orders', type: 'request', method: 'GET', target: '/orders' },
        ],
        metadata: {
          import: {
            preserved: {
              mulesoft_review: {
                detected: true,
                domains: [{ host: 'orders.us-e2.cloudhub.io', kind: 'cloudhub', count: 1 }],
                policy_findings: [{ policy: 'client_id_enforcement', detail: 'Anypoint Client ID Enforcement detected.' }],
                auth_findings: [{ type: 'oauth2_client_credentials', detail: 'OAuth2 client-credentials flow wired via http_oauth2_token.' }],
              },
            },
          },
        },
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [{
        key: 'raml_import',
        title: 'RAML Import Review',
        severity: 'info',
        summary: 'RAML resources and methods were converted to HTTP DSL actions.',
        items: ['OAuth2 security schemes create an http_oauth2_token init action when an accessTokenUri is present.'],
      }],
      repair_suggestions: [],
      correlations: [],
      stats: {
        format: 'raml',
        total_resources: 1,
        total_methods: 1,
        converted_actions: 1,
        request_bodies: 0,
        auth_schemes_detected: ['oauth_2_0'],
      },
      summary: {
        title: 'Orders API',
        version: 'v1',
        base_url: 'https://api.example.test',
        resources: ['/orders'],
        auth_schemes: ['oauth_2_0'],
        policies: [{ policy: 'client_id_enforcement', detail: 'Anypoint Client ID Enforcement detected.' }],
      },
    })
    apiMock.importBrowserRecording.mockResolvedValue({
      scripts: [],
      script: {
        name: 'Imported Chrome Recording',
        version: '1.0',
        protocol: 'browser',
        config: { base_url: 'https://shop.example.test' },
        actions: [
          { name: 'Navigate to /login', type: 'browser_navigate', target: '/login' },
          {
            name: 'Click Sign in',
            type: 'browser_click',
            selector: "[data-testid='login-btn']",
            target: "[data-testid='login-btn']",
            metadata: {
              source_selector_candidates: [
                { kind: 'css', value: "[data-testid='login-btn']", confidence: 95, reason: 'Chrome Recorder CSS selector.' },
                { kind: 'role', value: 'role=button[name="Sign in"]', confidence: 72, reason: 'Chrome Recorder ARIA selector with explicit role.' },
              ],
              source_selector_confidence: 95,
            },
          },
        ],
      },
      warnings: [],
      unsupported_elements: ['chrome_recorder_step:scroll (step 4) [warning]: not convertible'],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: {
        format: 'chrome_devtools_recorder',
        total_steps: 4,
        converted_actions: 2,
        skipped_steps: 1,
      },
      summary: { import_mode: 'chrome_devtools_recorder', execution_mode: 'apexload_browser' },
    })
    apiMock.importWSDL.mockResolvedValue({
      scripts: [],
      script: {
        name: 'Account SOAP',
        version: '1.0.0',
        protocol: 'soap',
        config: {
          base_url: 'https://soap.example.test/account',
          soap: {
            version: '1.2',
            namespaces: { tns: 'urn:accounts' },
            fault_handling: { fail_on_fault: true },
          },
        },
        actions: [
          {
            name: 'GetAccount',
            type: 'request',
            method: 'POST',
            target: '/',
            headers: { 'Content-Type': 'application/soap+xml', SOAPAction: 'urn:GetAccount' },
            body: '<soapenv:Envelope><soapenv:Body><tns:GetAccount /></soapenv:Body></soapenv:Envelope>',
            soap: { action: 'urn:GetAccount', operation: 'GetAccount', body_mode: 'generated' },
          },
        ],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [
        {
          key: 'wsdl_operations',
          title: 'WSDL Operations',
          severity: 'info',
          summary: 'Generated SOAP envelopes for review and replay.',
          items: ['GetAccount'],
        },
      ],
      repair_suggestions: [],
      correlations: [],
      stats: {
        format: 'wsdl',
        services: 1,
        port_types: 1,
        bindings: 1,
        operations_detected: 1,
        converted_actions: 1,
        soap_actions_detected: ['urn:GetAccount'],
      },
      summary: {
        service_name: 'AccountService',
        target_namespace: 'urn:accounts',
        base_url: 'https://soap.example.test/account',
        soap_version: '1.2',
        operations: ['GetAccount'],
      },
      source_review: {
        source_files: [{ path: 'service.wsdl', language: 'xml', content: '<definitions />' }],
        mappings: [{ id: 'wsdl-op', source_file: 'service.wsdl', start_line: 1, function: 'GetAccount', target_path: 'actions[0]', target_name: 'GetAccount', target_kind: 'action', status: 'converted' }],
      },
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('renders import diagnostics returned by protocol asset converters', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="openapi" projectId="project-1" />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['{"openapi":"3.0.3"}'], 'orders.yaml', { type: 'application/yaml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert OpenAPI'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importOpenAPI).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1' }))
    expect(container.textContent).toContain('Review Generated Script')
    expect(container.textContent).toContain('Unsupported Elements (1)')
    expect(container.textContent).toContain('schema reference external.yaml')
    expect(container.textContent).toContain('Import Review (1)')
    expect(container.textContent).toContain('file bytes must be supplied before replay')
    expect(container.textContent).toContain('Source Comparison')
    expect(container.textContent).toContain('POST /orders')
    expect(container.textContent).toContain('actions[0]')
  })

  it('passes the HAR include static assets toggle to the API', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="har" projectId="project-1" />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return

    const file = new File(['{"log":{"entries":[]}}'], 'capture.har', { type: 'application/json' })
    const comparisonFile = new File(['{"log":{"entries":[]}}'], 'capture-2.har', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const fileInputs = container.querySelectorAll('input[type="file"]')
    const comparisonInput = fileInputs[1] as HTMLInputElement | undefined
    expect(comparisonInput).toBeDefined()
    if (!comparisonInput) return
    await act(async () => {
      Object.defineProperty(comparisonInput, 'files', {
        configurable: true,
        value: [comparisonFile],
      })
      comparisonInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const includeStatic = Array.from(container.querySelectorAll('input[type="checkbox"]')).find(input => input.parentElement?.textContent?.includes('Include static assets')) as HTMLInputElement | undefined
    expect(includeStatic).toBeDefined()
    if (!includeStatic) return

    await act(async () => {
      includeStatic.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert HAR'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importHAR).toHaveBeenCalledWith(file, expect.objectContaining({
      project_id: 'project-1',
      include_static_assets: true,
      secondary_file: comparisonFile,
      artifact_mode: 'design_har_pair',
      retention_days: 7,
      redact_sensitive: true,
    }))
    expect(container.textContent).toContain('filtered assets')
    expect(container.textContent).toContain('HAR Artifact Review')
    expect(container.textContent).toContain('dynamic candidates')
  })

  it('imports Salesforce HAR files through the native review flow', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="salesforce" projectId="project-1" />)
    })

    expect(container.textContent).toContain('Import Salesforce HAR')

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return

    const file = new File(['{"log":{"entries":[]}}'], 'salesforce.har', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Salesforce HAR'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importSalesforce).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1' }))
    expect(container.textContent).toContain('Salesforce Native Review')
    expect(container.textContent).toContain('100%')
    expect(container.textContent).toContain('acme.my.salesforce.com')
    expect(container.textContent).toContain('Salesforce Login')
    expect(container.textContent).toContain('Review Salesforce correlation extractors before approval.')
    // Broadened API surface (G3)
    expect(container.textContent).toContain('API surface')
    expect(container.textContent).toContain('Apex REST')
    expect(container.textContent).toContain('Composite')
    // Aura message parameterization candidates (G3)
    expect(container.textContent).toContain('Aura parameterization candidates')
    expect(container.textContent).toContain('recordId')
    // Agentforce panel from preserved metadata (G4)
    expect(container.textContent).toContain('Agentforce Agent API Review')
    expect(container.textContent).toContain('streaming (SSE)')
  })

  it('imports RAML files through the shared protocol asset flow', async () => {
    apiMock.getRAMLSupportMatrix.mockResolvedValue({
      support_matrix: [{ id: 'raml-resources', area: 'Resources', construct: 'nested resources + URI params', status: 'supported' }],
      notes: ['RAML import targets common HTTP/JSON API specifications.'],
    })
    await act(async () => {
      root.render(<ProtocolAssetImport kind="raml" projectId="project-1" />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Import RAML')
    // RAML support-matrix panel (G2)
    expect(container.textContent).toContain('RAML import coverage')

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return

    const file = new File(['#%RAML 1.0\ntitle: Orders API'], 'orders.raml', { type: 'text/yaml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert RAML'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importRAML).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1' }))
    expect(container.textContent).toContain('RAML Import Review')
    expect(container.textContent).toContain('/orders')
    expect(container.textContent).toContain('oauth_2_0')
    expect(container.textContent).toContain('http_oauth2_token')
    // RAML Anypoint policy hint (G4) + MuleSoft panel from preserved metadata (G4)
    expect(container.textContent).toContain('client_id_enforcement')
    expect(container.textContent).toContain('MuleSoft / Anypoint Review')
    expect(container.textContent).toContain('orders.us-e2.cloudhub.io')
  })

  it('converts browser recordings and renders ranked selector candidates', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="recorder-export" projectId="project-1" />)
    })

    expect(container.textContent).toContain('Import Browser Recording')

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return

    const file = new File(['{"title":"Login","steps":[]}'], 'login.json', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Recording'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importBrowserRecording).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1' }))
    expect(container.textContent).toContain('Review Generated Script')
    expect(container.textContent).toContain('Selector Candidates')
    expect(container.textContent).toContain("[data-testid='login-btn']")
    expect(container.textContent).toContain('role=button[name="Sign in"]')
    expect(container.textContent).toContain('95')
    expect(container.textContent).toContain('chrome_recorder_step:scroll')
  })

  it('opts into the Agentforce replay-readiness transform and renders applied changes', async () => {
    apiMock.importOpenAPI.mockResolvedValueOnce({
      scripts: [],
      script: {
        name: 'Agent API',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://api.salesforce.com' },
        init: [{ name: 'Acquire Agent API Token', type: 'http_oauth2_token', target: '', params: { save_as: 'agent_access_token' } }],
        actions: [
          { name: 'Start Session', type: 'request', method: 'POST', target: '/einstein/ai-agent/v1/agents/0Xx/sessions' },
        ],
        metadata: {
          import: {
            preserved: {
              agentforce_review: {
                detected: true,
                streaming: true,
                lifecycle: [{ step: 'session_start', method: 'POST', path: '/einstein/ai-agent/v1/sessions' }],
              },
              agentforce_transform: [
                { step: 'token', action_name: 'Acquire Agent API Token', detail: 'Prepended an http_oauth2_token init action saving {{var.agent_access_token}}.' },
                { step: 'session', action_name: 'Send Message', detail: 'Rewrote the session id in the request path to {{var.agent_session_id}}.' },
              ],
            },
          },
        },
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { format: 'openapi', converted_actions: 1 },
      summary: { title: 'Agent API' },
    })

    await act(async () => {
      root.render(<ProtocolAssetImport kind="openapi" projectId="project-1" />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return

    const file = new File(['{"openapi":"3.0.3"}'], 'agent.yaml', { type: 'application/yaml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const transformToggle = Array.from(container.querySelectorAll('input[type="checkbox"]')).find(input => input.parentElement?.textContent?.includes('Make Agentforce replay-ready')) as HTMLInputElement | undefined
    expect(transformToggle).toBeDefined()
    if (!transformToggle) return
    await act(async () => {
      transformToggle.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert OpenAPI'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importOpenAPI).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1', agentforce_transform: true }))
    expect(container.textContent).toContain('Applied replay-readiness changes (2)')
    expect(container.textContent).toContain('Prepended an http_oauth2_token init action')
    expect(container.textContent).toContain('Rewrote the session id in the request path')
  })

  it('converts WSDL files with SOAP-specific options and renders the SOAP review', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="wsdl" projectId="project-1" />)
    })

    expect(container.textContent).toContain('Import WSDL')
    expect(container.textContent).toContain('SOAP Version')

    const soapVersionSelect = container.querySelector('select') as HTMLSelectElement | null
    expect(soapVersionSelect).not.toBeNull()
    if (!soapVersionSelect) return

    await act(async () => {
      soapVersionSelect.value = '1.2'
      soapVersionSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return

    const file = new File(['<definitions />'], 'account.wsdl', { type: 'text/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert WSDL'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importWSDL).toHaveBeenCalledWith(file, expect.objectContaining({
      project_id: 'project-1',
      soap_version: '1.2',
    }))
    expect(container.textContent).toContain('Review Generated Script')
    expect(container.textContent).toContain('WSDL Operations')
    expect(container.textContent).toContain('GetAccount')
    expect(container.textContent).toContain('Source Comparison')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}
