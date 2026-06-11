// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ProtocolAssetImport from './ProtocolAssetImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  importDevWeb: vi.fn(),
  importHAR: vi.fn(),
  importBrowserRecording: vi.fn(),
  importOpenAPI: vi.fn(),
  importPostman: vi.fn(),
  importWSDL: vi.fn(),
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
