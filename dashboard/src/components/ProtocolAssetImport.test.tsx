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
  validateBrowserRecordingImport: vi.fn(),
  replayBrowserRecordingImport: vi.fn(),
  startDraftBrowserReplaySession: vi.fn(),
  importOpenAPI: vi.fn(),
  importRAML: vi.fn(),
  getOpenAPISupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  getRAMLSupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  getAsyncAPISupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  importPostman: vi.fn(),
  validatePostmanImport: vi.fn(),
  commitOpenAPIImport: vi.fn(),
  commitAsyncAPIImport: vi.fn(),
  savePostmanImport: vi.fn(),
  importWSDL: vi.fn(),
  importAsyncAPI: vi.fn(),
  listScripts: vi.fn(),
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

vi.mock('./BrowserReplayProgress', () => ({
  default: ({ replaySessionId, onComplete }: { replaySessionId: string; onComplete?: (report: unknown, events: unknown[]) => void }) => (
    <div data-testid="replay-progress">
      <span>Replay {replaySessionId}</span>
      <button
        type="button"
        onClick={() => onComplete?.({
          replay_session_id: replaySessionId,
          success: true,
          group: 'Recorder import',
          protocol: 'browser',
          started_at: '2026-06-22T00:00:00Z',
          completed_at: '2026-06-22T00:00:01Z',
          events: [{ event_id: 'evt-1', action_type: 'browser_click', success: true }],
          repair_suggestions: [],
        }, [])}
      >
        Complete Replay
      </button>
    </div>
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
    apiMock.listScripts.mockResolvedValue({ scripts: [], count: 0 })
    apiMock.getOpenAPISupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getRAMLSupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getAsyncAPISupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.startDraftBrowserReplaySession.mockResolvedValue({ replay_session_id: 'replay-recorder-1', replay_session: { id: 'replay-recorder-1' } })
    apiMock.createScript.mockResolvedValue({
      id: 'script-saved',
      name: 'Imported Chrome Recording',
      protocol: 'browser',
      version: '1.0',
      project_id: 'project-1',
      created_at: '2026-01-01T00:00:00Z',
      script: {
        name: 'Imported Chrome Recording',
        version: '1.0',
        protocol: 'browser',
        config: { base_url: 'https://shop.example.test' },
        actions: [],
      },
    })
    const postmanResult = {
      scripts: [],
      script: {
        name: 'Imported Postman',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://api.example.test' },
        actions: [
          { name: 'Get Orders', type: 'request', method: 'GET', target: '/orders' },
        ],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [{
        key: 'postman_requests',
        title: 'Postman Requests',
        severity: 'info',
        summary: 'Converted Postman requests into HTTP actions.',
        items: ['GET /orders'],
      }],
      repair_suggestions: [],
      correlations: [],
      stats: {
        format: 'postman',
        folders: 0,
        total_requests: 1,
        converted_actions: 1,
        variables_detected: 1,
        environment_values: 2,
        unsupported_bodies: 0,
        assertions: 1,
      },
      summary: {
        collection_name: 'Orders Collection',
      },
    }
    apiMock.importPostman.mockResolvedValue(postmanResult)
    apiMock.validatePostmanImport.mockResolvedValue(postmanResult)
    apiMock.savePostmanImport.mockResolvedValue({
      scripts: [],
      stored_script: {
        id: 'script-postman',
        name: 'Imported Postman',
        protocol: 'http',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: postmanResult.script,
      },
      replaced: false,
    })

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
      preflight: {
        valid: false,
        action_count: 1,
        expected_action_count: 1,
        data_source_count: 1,
        parameter_count: 0,
        browser_required: false,
        warnings: ['file data source "file" is a generated OpenAPI placeholder and needs uploaded bytes before replay'],
        checks: [
          { id: 'validation', label: 'Script validation', status: 'pass' },
          { id: 'data_sources', label: 'Data source readiness', status: 'fail', detail: 'missing file bytes' },
        ],
      },
      stats: {
        format: 'openapi',
        total_paths: 1,
        total_operations: 1,
        converted_actions: 1,
        request_bodies: 1,
        assertions: 1,
        extractors: 1,
        correlations: 0,
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
    apiMock.commitOpenAPIImport.mockResolvedValue({
      stored_script: {
        id: 'script-openapi',
        name: 'Imported Orders API',
        protocol: 'http',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: {
          name: 'Imported Orders API',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://api.example.test' },
          actions: [{ name: 'Create Order', type: 'request', method: 'POST', target: '/orders' }],
        },
      },
      replaced: false,
      dry_run: false,
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
      unsupported_elements: ['chrome_recorder_step:customStep (step 4) [warning]: manual review required'],
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
      summary: {
        import_mode: 'chrome_devtools_recorder',
        execution_mode: 'apexload_browser',
        step_mapping: [
          { source_index: 1, source_location: 'step 1', source_step_type: 'navigate', action_index: 0, name: 'Navigate to /login', type: 'browser_navigate', disposition: 'mapped' },
          { source_index: 2, source_location: 'step 2', source_step_type: 'click', action_index: 1, name: 'Click Sign in', type: 'browser_click', disposition: 'mapped' },
          { source_index: 3, source_location: 'step 3', source_step_type: 'scroll', action_index: 2, name: 'Scroll page', type: 'browser_evaluate_js', disposition: 'degraded', reason: 'Converted scroll to review-gated JavaScript.' },
          { source_index: 4, source_location: 'step 4', source_step_type: 'customStep', disposition: 'unsupported', reason: 'Chrome customStep requires manual review before replay.' },
        ],
      },
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
    apiMock.importAsyncAPI.mockResolvedValue({
      scripts: [
        {
          name: 'Orders Kafka',
          version: '1.0.0',
          protocol: 'kafka',
          config: {
            base_url: '',
            kafka: {
              brokers: ['override-a:9092'],
              mock: true,
              auto_offset_reset: 'earliest',
              serialization: 'avro',
              schema_registry_url: 'http://registry:8081',
              schema_id: 42,
              subject: 'orders-value',
            },
          },
          actions: [
            { name: 'Publish Order', type: 'kafka_produce', target: 'orders.created' },
            { name: 'Consume Order', type: 'kafka_consume', target: 'orders.created' },
          ],
          metadata: {
            import: {
              preserved: {
                replay_preflight: {
                  valid: true,
                  checks: [
                    { id: 'messaging_kafka', label: 'Kafka replay readiness', status: 'pass', detail: 'Mock broker enabled.' },
                  ],
                },
              },
            },
          },
        },
        {
          name: 'Orders JMS',
          version: '1.0.0',
          protocol: 'jms-solace',
          config: {
            base_url: '',
            jms: {
              provider: 'solace',
              broker_url: 'amqp.example.test:5671',
              mock: true,
              destination_type: 'topic',
              subscription: 'asyncapi-replay',
            },
          },
          actions: [
            { name: 'Publish Shipment', type: 'jms_publish', target: 'shipments' },
          ],
          metadata: {
            import: {
              preserved: {
                replay_preflight: {
                  valid: true,
                  checks: [
                    { id: 'messaging_jms', label: 'JMS replay readiness', status: 'pass', detail: 'Mock broker enabled.' },
                  ],
                },
              },
            },
          },
        },
      ],
      script: {
        name: 'Orders Kafka',
        version: '1.0.0',
        protocol: 'kafka',
        config: {
          base_url: '',
          kafka: {
            brokers: ['override-a:9092'],
            mock: true,
            auto_offset_reset: 'earliest',
            serialization: 'avro',
            schema_registry_url: 'http://registry:8081',
            schema_id: 42,
            subject: 'orders-value',
          },
        },
        actions: [
          { name: 'Publish Order', type: 'kafka_produce', target: 'orders.created' },
          { name: 'Consume Order', type: 'kafka_consume', target: 'orders.created' },
        ],
      },
      warnings: ['config.kafka.auto_offset_reset was set to earliest for import replay against real brokers.'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [{
        key: 'asyncapi_operations',
        title: 'AsyncAPI Operations',
        severity: 'info',
        summary: 'Channels converted into messaging actions.',
        items: ['Publish Order', 'Consume Order'],
      }],
      repair_suggestions: [],
      correlations: [],
      preflight: {
        valid: true,
        action_count: 2,
        expected_action_count: 2,
        data_source_count: 1,
        parameter_count: 0,
        browser_required: false,
        checks: [
          { id: 'messaging_kafka', label: 'Kafka replay readiness', status: 'pass', detail: 'Mock broker enabled.' },
        ],
      },
      stats: {
        format: 'asyncapi',
        asyncapi_version: '3.0.0',
        total_channels: 2,
        total_operations: 3,
        converted_actions: 3,
        produce_actions: 2,
        consume_actions: 1,
        request_reply_actions: 1,
        messages_with_correlation: 1,
        server_protocols: ['kafka', 'amqp'],
        brokers: ['override-a:9092'],
        schema_formats_detected: ['avro'],
        skipped_operations: ['mqtt sensor update'],
      },
      summary: {
        topics: ['orders.created', 'shipments'],
      },
    })
    apiMock.commitAsyncAPIImport.mockResolvedValue({
      stored_script: {
        id: 'script-asyncapi',
        name: 'Orders Kafka',
        protocol: 'kafka',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: {
          name: 'Orders Kafka',
          version: '1.0.0',
          protocol: 'kafka',
          config: { base_url: '', kafka: { brokers: ['override-a:9092'], mock: true } },
          actions: [{ name: 'Publish Order', type: 'kafka_produce', target: 'orders.created' }],
        },
      },
      replaced: false,
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
    expect(container.textContent).toContain('Needs attention')
    expect(container.textContent).toContain('Data source readiness')
    expect(container.textContent).toContain('missing file bytes')
    expect(container.textContent).toContain('Source Comparison')
    expect(container.textContent).toContain('POST /orders')
    expect(container.textContent).toContain('actions[0]')
  })

  it('reviews OpenAPI correlations and replaces by operation id', async () => {
    const onImported = vi.fn()
    apiMock.getOpenAPISupportMatrix.mockResolvedValueOnce({
      support_matrix: [{ id: 'openapi-chaining', area: 'Replay', construct: 'cross-operation chaining', status: 'supported' }],
      notes: ['Correlation is spec-inferred.'],
    })
    apiMock.listScripts.mockResolvedValueOnce({
      scripts: [{
        id: 'script-openapi-existing',
        name: 'Existing Orders API',
        protocol: 'http',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: {
          name: 'Existing Orders API',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://api.example.test' },
          actions: [],
        },
      }],
      count: 1,
    })
    apiMock.importOpenAPI.mockResolvedValueOnce({
      scripts: [],
      script: {
        name: 'Imported Orders API',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://api.example.test' },
        actions: [
          { name: 'Create Order', type: 'request', method: 'POST', target: '/orders', extractors: [{ name: 'order_id', source: 'body', type: 'jsonpath', expression: '$.id' }] },
          { name: 'Get Order', type: 'request', method: 'GET', target: '/orders/{{var.order_id}}' },
        ],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [{ name: 'order_id', source: 'body', expression: '$.id', extract_type: 'jsonpath', found_in_step: 1, used_in_steps: [2] }],
      preflight: {
        valid: true,
        action_count: 2,
        expected_action_count: 2,
        data_source_count: 0,
        parameter_count: 0,
        browser_required: false,
        checks: [{ id: 'variables', label: 'Variable readiness', status: 'pass', detail: 'All replay variables are seeded or extracted before use.' }],
      },
      stats: { format: 'openapi', converted_actions: 2, extractors: 1, correlations: 1 },
      summary: { title: 'Orders API' },
    })
    apiMock.commitOpenAPIImport.mockResolvedValueOnce({
      stored_script: {
        id: 'script-openapi-existing',
        name: 'Imported Orders API',
        protocol: 'http',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: {
          name: 'Imported Orders API',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://api.example.test' },
          actions: [{ name: 'Get Order', type: 'request', method: 'GET', target: '/orders/{{var.order_id}}' }],
        },
      },
      replaced: true,
    })

    await act(async () => {
      root.render(<ProtocolAssetImport kind="openapi" projectId="project-1" onImported={onImported} />)
    })
    await flushPromises()

    expect(container.textContent).toContain('OpenAPI import coverage')
    expect(container.textContent).toContain('cross-operation chaining')

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return

    const file = new File(['{"openapi":"3.0.3"}'], 'orders.yaml', { type: 'application/yaml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert OpenAPI'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('Ready')
    expect(container.textContent).toContain('Correlation Review')
    expect(container.textContent).toContain('order_id')
    expect(container.textContent).toContain('$.id')

    const modeSelect = Array.from(container.querySelectorAll('select')).find(select => select.closest('label')?.textContent?.includes('Mode')) as HTMLSelectElement | undefined
    expect(modeSelect).toBeDefined()
    if (!modeSelect) return
    await act(async () => {
      modeSelect.value = 'replace'
      modeSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const scriptSelect = Array.from(container.querySelectorAll('select')).find(select => select.closest('label')?.textContent?.includes('Script')) as HTMLSelectElement | undefined
    const replaceModeSelect = Array.from(container.querySelectorAll('select')).find(select => select.closest('label')?.textContent?.includes('Replace Mode')) as HTMLSelectElement | undefined
    expect(scriptSelect).toBeDefined()
    expect(replaceModeSelect).toBeDefined()
    if (!scriptSelect || !replaceModeSelect) return
    await act(async () => {
      scriptSelect.value = 'script-openapi-existing'
      scriptSelect.dispatchEvent(new Event('change', { bubbles: true }))
      replaceModeSelect.value = 'merge_by_operation_id'
      replaceModeSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Replace Script'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.commitOpenAPIImport).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'replace',
      target_script_id: 'script-openapi-existing',
      replace_mode: 'merge_by_operation_id',
      script: expect.objectContaining({ name: 'Imported Orders API' }),
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'script-openapi-existing', name: 'Imported Orders API' })],
    }))
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

  it('imports AsyncAPI specs with messaging controls, support matrix, and replay preflight', async () => {
    apiMock.getAsyncAPISupportMatrix.mockResolvedValueOnce({
      support_matrix: [
        { id: 'asyncapi-payload-avro', area: 'Messages', construct: 'avro schemaFormat', status: 'supported', review_required: false },
        { id: 'asyncapi-servers-mqtt', area: 'Servers', construct: 'mqtt servers', status: 'review_only', review_required: true },
      ],
      notes: ['MQTT operations are review-only.'],
    })
    await act(async () => {
      root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Import AsyncAPI')
    expect(container.textContent).toContain('AsyncAPI import coverage')
    expect(container.textContent).toContain('avro schemaFormat')
    expect(container.textContent).toContain('MQTT operations are review-only.')
    expect(container.textContent).not.toContain('Validate Only')
    expect(container.textContent).toContain('Broker Override')
    expect(container.textContent).not.toContain('Base URL Override')

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return

    const file = new File(['asyncapi: 3.0.0'], 'orders.asyncapi.yaml', { type: 'application/yaml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const brokerInput = Array.from(container.querySelectorAll('input')).find(input => input.closest('label')?.textContent?.includes('Broker Override')) as HTMLInputElement | undefined
    const channelInput = Array.from(container.querySelectorAll('input')).find(input => input.closest('label')?.textContent?.includes('Channel Filter')) as HTMLInputElement | undefined
    const registryInput = Array.from(container.querySelectorAll('input')).find(input => input.closest('label')?.textContent?.includes('Schema Registry URL')) as HTMLInputElement | undefined
    const schemaIDInput = Array.from(container.querySelectorAll('input')).find(input => input.closest('label')?.textContent?.includes('Schema ID')) as HTMLInputElement | undefined
    const subjectInput = Array.from(container.querySelectorAll('input')).find(input => input.closest('label')?.textContent?.includes('Subject')) as HTMLInputElement | undefined
    const serializationSelect = Array.from(container.querySelectorAll('select')).find(select => select.closest('label')?.textContent?.includes('Serialization')) as HTMLSelectElement | undefined
    expect(brokerInput).toBeDefined()
    expect(channelInput).toBeDefined()
    expect(registryInput).toBeDefined()
    expect(schemaIDInput).toBeDefined()
    expect(subjectInput).toBeDefined()
    expect(serializationSelect).toBeDefined()
    if (!brokerInput || !channelInput || !registryInput || !schemaIDInput || !subjectInput || !serializationSelect) return

    await act(async () => {
      setInputValue(brokerInput, 'override-a:9092')
      setInputValue(channelInput, 'orders.created')
      serializationSelect.value = 'avro'
      serializationSelect.dispatchEvent(new Event('change', { bubbles: true }))
      setInputValue(registryInput, 'http://registry:8081')
      setInputValue(schemaIDInput, '42')
      setInputValue(subjectInput, 'orders-value')
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert AsyncAPI'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importAsyncAPI).toHaveBeenCalledWith(file, expect.objectContaining({
      project_id: 'project-1',
      brokers: 'override-a:9092',
      channels: 'orders.created',
      mock: true,
      include_assertions: true,
      serialization: 'avro',
      schema_registry_url: 'http://registry:8081',
      schema_id: 42,
      subject: 'orders-value',
    }))
    expect(container.textContent).toContain('Preflight')
    expect(container.textContent).toContain('Kafka replay readiness')
    expect(container.textContent).toContain('AsyncAPI Replay Review')
    expect(container.textContent).toContain('request reply')
    expect(container.textContent).toContain('correlations')
    expect(container.textContent).toContain('mqtt sensor update')
    expect(container.textContent).toContain('Generated Scripts')
    expect(container.textContent).toContain('kafka · Orders Kafka')
    expect(container.textContent).toContain('jms-solace · Orders JMS')
    expect(container.textContent).toContain('Kafka brokers · serde · replay')
    expect(container.textContent).toContain('SASL Secret')
  })

  it('saves all generated AsyncAPI scripts through the commit route', async () => {
    const onImported = vi.fn()
    apiMock.commitAsyncAPIImport
      .mockResolvedValueOnce({
        stored_script: {
          id: 'script-asyncapi-kafka',
          name: 'Orders Kafka',
          protocol: 'kafka',
          version: '1.0.0',
          project_id: 'project-1',
          created_at: '2026-01-01T00:00:00Z',
          script: { name: 'Orders Kafka', version: '1.0.0', protocol: 'kafka', config: { base_url: '' }, actions: [] },
        },
      })
      .mockResolvedValueOnce({
        stored_script: {
          id: 'script-asyncapi-jms',
          name: 'Orders JMS',
          protocol: 'jms-solace',
          version: '1.0.0',
          project_id: 'project-1',
          created_at: '2026-01-01T00:00:00Z',
          script: { name: 'Orders JMS', version: '1.0.0', protocol: 'jms-solace', config: { base_url: '' }, actions: [] },
        },
      })

    await act(async () => {
      root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" onImported={onImported} />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return
    const file = new File(['asyncapi: 3.0.0'], 'orders.asyncapi.yaml', { type: 'application/yaml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert AsyncAPI'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const jmsTab = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('jms-solace · Orders JMS'))
    expect(jmsTab).toBeDefined()
    if (!jmsTab) return
    await act(async () => {
      jmsTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('JMS replay readiness')
    expect(container.textContent).toContain('JMS broker · destination · replay')
    expect(container.textContent).toContain('Destination Type')

    const saveAll = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save All Scripts'))
    expect(saveAll).toBeDefined()
    if (!saveAll) return
    await act(async () => {
      saveAll.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.commitAsyncAPIImport).toHaveBeenCalledTimes(2)
    expect(apiMock.commitAsyncAPIImport).toHaveBeenNthCalledWith(1, expect.objectContaining({
      mode: 'create',
      script: expect.objectContaining({ name: 'Orders Kafka', protocol: 'kafka' }),
    }))
    expect(apiMock.commitAsyncAPIImport).toHaveBeenNthCalledWith(2, expect.objectContaining({
      mode: 'create',
      script: expect.objectContaining({ name: 'Orders JMS', protocol: 'jms-solace' }),
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [
        expect.objectContaining({ id: 'script-asyncapi-kafka' }),
        expect.objectContaining({ id: 'script-asyncapi-jms' }),
      ],
    }))
  })

  it('passes optional Postman environment files to validation imports', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="postman" projectId="project-1" />)
    })

    expect(container.textContent).toContain('Environment File')

    const fileInputs = container.querySelectorAll('input[type="file"]')
    const collectionInput = fileInputs[0] as HTMLInputElement | undefined
    const environmentInput = fileInputs[1] as HTMLInputElement | undefined
    expect(collectionInput).toBeDefined()
    expect(environmentInput).toBeDefined()
    if (!collectionInput || !environmentInput) return

    const collectionFile = new File(['{"info":{"name":"Orders"},"item":[]}'], 'orders.postman_collection.json', { type: 'application/json' })
    const environmentFile = new File(['{"values":[]}'], 'local.postman_environment.json', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(collectionInput, 'files', { configurable: true, value: [collectionFile] })
      collectionInput.dispatchEvent(new Event('change', { bubbles: true }))
      Object.defineProperty(environmentInput, 'files', { configurable: true, value: [environmentFile] })
      environmentInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) return

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.validatePostmanImport).toHaveBeenCalledWith(collectionFile, expect.objectContaining({
      project_id: 'project-1',
      environment_file: environmentFile,
    }))
    expect(container.textContent).toContain('Validation Review')
    expect(container.textContent).toContain('environment values')
  })

  it('saves reviewed Postman imports by replacing an existing script', async () => {
    const onImported = vi.fn()
    apiMock.listScripts.mockResolvedValueOnce({
      scripts: [{
        id: 'script-existing',
        name: 'Existing Orders',
        protocol: 'http',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: {
          name: 'Existing Orders',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://api.example.test' },
          actions: [],
        },
      }],
      count: 1,
    })
    apiMock.savePostmanImport.mockResolvedValueOnce({
      stored_script: {
        id: 'script-existing',
        name: 'Imported Postman',
        protocol: 'http',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: {
          name: 'Imported Postman',
          version: '1.0.0',
          protocol: 'http',
          config: { base_url: 'https://api.example.test' },
          actions: [{ name: 'Get Orders', type: 'request', method: 'GET', target: '/orders' }],
        },
      },
      replaced: true,
    })

    await act(async () => {
      root.render(<ProtocolAssetImport kind="postman" projectId="project-1" onImported={onImported} />)
    })
    await flushPromises()

    const collectionInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(collectionInput).not.toBeNull()
    if (!collectionInput) return
    const collectionFile = new File(['{"info":{"name":"Orders"},"item":[]}'], 'orders.postman_collection.json', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(collectionInput, 'files', { configurable: true, value: [collectionFile] })
      collectionInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Postman Collection'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const modeSelect = Array.from(container.querySelectorAll('select')).find(select => select.closest('label')?.textContent?.includes('Mode')) as HTMLSelectElement | undefined
    expect(modeSelect).toBeDefined()
    if (!modeSelect) return
    await act(async () => {
      modeSelect.value = 'replace'
      modeSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const scriptSelect = Array.from(container.querySelectorAll('select')).find(select => select.closest('label')?.textContent?.includes('Script')) as HTMLSelectElement | undefined
    expect(scriptSelect).toBeDefined()
    if (!scriptSelect) return
    await act(async () => {
      scriptSelect.value = 'script-existing'
      scriptSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Replace Script'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.savePostmanImport).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'replace',
      target_script_id: 'script-existing',
      script: expect.objectContaining({ name: 'Imported Postman' }),
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'script-existing', name: 'Imported Postman' })],
    }))
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
    expect(container.textContent).toContain('Recorder Step Mapping')
    expect(container.textContent).toContain('1 degraded')
    expect(container.textContent).toContain('1 unsupported')
    expect(container.textContent).toContain('Chrome customStep requires manual review')
    expect(container.textContent).toContain('chrome_recorder_step:customStep')
  })

  it('requires replay validation before normal recorder import save and attaches evidence', async () => {
    const onImported = vi.fn()
    await act(async () => {
      root.render(<ProtocolAssetImport kind="recorder-export" projectId="project-1" onImported={onImported} />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return
    const file = new File(['{"title":"Login","steps":[]}'], 'login.json', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Recording'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const useFallback = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Use')
    expect(useFallback).toBeDefined()
    if (!useFallback) return
    await act(async () => {
      useFallback.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Selector updated in the draft')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save To Script Library'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Run replay validation successfully before saving')

    const replayButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Run Replay Validation'))
    expect(replayButton).toBeDefined()
    if (!replayButton) return
    await act(async () => {
      replayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.startDraftBrowserReplaySession).toHaveBeenCalledWith(expect.objectContaining({
      ignore_think_time: true,
      script: expect.objectContaining({
        protocol: 'browser',
        project_id: 'project-1',
        actions: expect.arrayContaining([
          expect.objectContaining({ selector: 'role=button[name="Sign in"]' }),
        ]),
      }),
    }))

    const completeReplay = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Complete Replay'))
    expect(completeReplay).toBeDefined()
    if (!completeReplay) return
    await act(async () => {
      completeReplay.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Replay validation passed')

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      actions: expect.arrayContaining([
        expect.objectContaining({
          selector: 'role=button[name="Sign in"]',
          metadata: expect.objectContaining({
            selector_promoted_from_import_review: true,
          }),
        }),
      ]),
      metadata: expect.objectContaining({
        import: expect.objectContaining({
          preserved: expect.objectContaining({
            replay_validation: expect.objectContaining({
              status: 'completed',
              success: true,
              replay_session_id: 'replay-recorder-1',
              events: 1,
              draft_override: false,
            }),
          }),
        }),
      }),
    }), 'project-1')
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'script-saved' })],
    }))
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

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}
