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
  getDevWebSupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  importRAML: vi.fn(),
  getOpenAPISupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  getRAMLSupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  getAsyncAPISupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  getPostmanSupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  importPostman: vi.fn(),
  validatePostmanImport: vi.fn(),
  commitOpenAPIImport: vi.fn(),
  commitAsyncAPIImport: vi.fn(),
  commitAsyncAPIImportBatch: vi.fn(),
  commitGenericImportBatch: vi.fn(),
  savePostmanImport: vi.fn(),
  importWSDL: vi.fn(),
  importAsyncAPI: vi.fn(),
  listScripts: vi.fn(),
  createScript: vi.fn(),
  deleteScript: vi.fn(),
  importK6: vi.fn(),
  importCloudTest: vi.fn(),
  importEventHubsCapture: vi.fn(),
  importBrokerConfig: vi.fn(),
  getK6SupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  getCloudTestSupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  getEventHubsCaptureSupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  getBrokerConfigSupportMatrix: vi.fn().mockResolvedValue({ support_matrix: [], notes: [] }),
  applySecretBindings: vi.fn(),
  applyImportParameterization: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
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
  default: ({ replaySessionId, onComplete, onApplySuggestion }: { replaySessionId: string; onComplete?: (report: unknown, events: unknown[]) => void; onApplySuggestion?: (suggestion: unknown) => void }) => (
    <div data-testid="replay-progress">
      <span>Replay {replaySessionId}</span>
      <button
        type="button"
        onClick={() => onApplySuggestion?.({ id: 'unsupported', title: 'Change wait', description: 'Update wait timeout', draft_patch_type: 'update_timeout', fields: {} })}
      >
        Apply Unsupported Repair
      </button>
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
    apiMock.getDevWebSupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getOpenAPISupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getRAMLSupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getAsyncAPISupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getPostmanSupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getK6SupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getCloudTestSupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getEventHubsCaptureSupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getBrokerConfigSupportMatrix.mockResolvedValue({ support_matrix: [], notes: [] })
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [],
    })
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.migrateImportSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.discardImportSecretSession.mockResolvedValue(undefined)
    apiMock.deleteScript.mockResolvedValue(undefined)
    const protocolImportResult = (protocol: string) => ({
      scripts: [],
      script: { name: `Imported ${protocol}`, version: '1.0.0', protocol, config: { base_url: '' }, actions: [{ name: 'Action', type: 'request', method: 'GET', target: '/' }] },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { converted_actions: 1 },
      summary: {},
    })
    apiMock.importK6.mockResolvedValue(protocolImportResult('http'))
    apiMock.importEventHubsCapture.mockResolvedValue(protocolImportResult('eventhubs'))
    apiMock.importBrokerConfig.mockResolvedValue(protocolImportResult('jms-solace'))
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
    apiMock.commitGenericImportBatch.mockImplementation(async ({ scripts }: { scripts: any[] }) => ({
      count: scripts.length,
      scripts: scripts.map((script, index) => ({
        id: `script-saved-${index + 1}`,
        name: script.name,
        protocol: script.protocol,
        version: '1.0',
        project_id: script.project_id,
        folder_id: script.folder_id,
        created_at: '2026-01-01T00:00:00Z',
        script,
      })),
    }))
    const postmanResult = {
      import_session_id: 'postman-session-1',
      expires_at: '2099-07-26T12:30:00Z',
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
      import_session_id: 'openapi-session-1',
      expires_at: '2099-07-26T12:30:00Z',
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
      preflight: {
        valid: false,
        action_count: 1,
        expected_action_count: 2,
        data_source_count: 0,
        parameter_count: 0,
        browser_required: false,
        warnings: ['One HAR entry was filtered before replay.'],
        checks: [
          { id: 'har_accounting', label: 'HAR replay accounting', status: 'warn', detail: '1 of 2 requests converted.' },
        ],
      },
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
    apiMock.commitAsyncAPIImportBatch.mockImplementation(async ({ scripts }: { scripts: Array<{ name: string; protocol: string; version: string }> }) => ({
      scripts: scripts.map((script, index) => ({
        id: `script-asyncapi-batch-${index + 1}`,
        name: script.name,
        protocol: script.protocol,
        version: script.version,
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script,
      })),
      count: scripts.length,
    }))
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('ignores a conversion response after a conversion option changes', async () => {
    let resolveImport: ((value: unknown) => void) | undefined
    apiMock.importK6.mockImplementationOnce(() => new Promise(resolve => { resolveImport = resolve }))
    const file = new File(['export default function () {}'], 'load.js', { type: 'application/javascript' })
    await act(async () => {
      root.render(<ProtocolAssetImport kind="k6" initialFile={file} projectId="project-1" />)
    })
    await flushPromises()

    const convert = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Convert k6 Script'))
    await act(async () => {
      convert?.click()
      await Promise.resolve()
    })
    const nameInput = Array.from(container.querySelectorAll<HTMLInputElement>('.sample-import-grid input'))
      .find(input => input.type === 'text')
    await act(async () => {
      if (nameInput) setInputValue(nameInput, 'Changed while converting')
    })
    await act(async () => {
      resolveImport?.({
        script: { name: 'Stale k6 result', version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [] },
        scripts: [],
        source_secret_session_id: 'stale-k6-session',
        warnings: [],
        unsupported_elements: [],
        review_sections: [],
        stats: { converted_actions: 0 },
      })
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('Review Generated Script')
    expect(container.textContent).not.toContain('Stale k6 result')
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'stale-k6-session',
    })
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
    expect(container.textContent).toContain('1 unsupported')
    expect(container.textContent).not.toContain('schema reference external.yaml')
    expect(container.textContent).toContain('1 warning')
    expect(container.textContent).not.toContain('file bytes must be supplied before replay')
    const warningDetails = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === '1 warning')!
    await act(async () => warningDetails.click())
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('file bytes must be supplied before replay')
    await act(async () => {
      ;(document.body.querySelector('[role="dialog"] button') as HTMLButtonElement).click()
    })
    const unsupportedDetails = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === '1 unsupported')!
    await act(async () => unsupportedDetails.click())
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('schema reference external.yaml')
    expect(container.textContent).toContain('Needs attention')
    expect(container.textContent).toContain('Data source readiness')
    expect(container.textContent).toContain('missing file bytes')
    expect(container.textContent).toContain('Source Comparison')
    expect(container.textContent).toContain('POST /orders')
    expect(container.textContent).toContain('actions[0]')
  })

  it('does not render import coverage when a support matrix has no limitations', async () => {
    apiMock.getOpenAPISupportMatrix.mockResolvedValueOnce({
      support_matrix: [{ id: 'openapi-doc', area: 'Document', construct: 'OpenAPI 2.0 / 3.x / 3.1', status: 'supported' }],
      notes: [],
    })

    await act(async () => {
      root.render(<ProtocolAssetImport kind="openapi" projectId="project-1" />)
    })
    await flushPromises()

    expect(container.querySelector('[aria-label="Import OpenAPI coverage"]')).toBeNull()
    expect(container.textContent).not.toContain('Supported protocols')
  })

  it('keeps the OpenAPI protected candidate while forwarding credential and parameterization overrides', async () => {
    const sourceScript = {
      name: 'Protected Orders API',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Get Order',
        type: 'request',
        method: 'GET',
        target: '/orders/123',
        headers: { Authorization: 'Bearer {{secret.OPENAPI_TOKEN}}' },
      }],
    }
    const parameterizedScript = {
      ...sourceScript,
      actions: [{ ...sourceScript.actions[0], target: '/orders/{{param.ORDER_ID}}' }],
    }
    apiMock.importOpenAPI.mockResolvedValueOnce({
      import_session_id: 'openapi-protected-overrides',
      expires_at: '2099-07-26T12:30:00Z',
      scripts: [],
      script: sourceScript,
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { format: 'openapi', converted_actions: 1 },
      summary: { title: 'Protected Orders API' },
      parameterization: {
        suggestions: [{
          id: 'pz_openapi_order_id',
          kind: 'data_param',
          var_name: 'ORDER_ID',
          replacement: '{{param.ORDER_ID}}',
          literal_value: '123',
          confidence: 1,
          rule_id: 'fixture',
          rationale: 'Stable order identifier',
          auto_apply: true,
          high_risk: false,
        }],
        summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
      },
      secret_binding_requirements: [{
        id: 'cred-openapi-token',
        path: '/actions/0/headers/Authorization',
        label: 'Authorization header',
        context: 'Get Order',
        suggested_name: 'OPENAPI_TOKEN',
        replacement: '{{secret.OPENAPI_TOKEN}}',
      }],
    })
    apiMock.applyImportParameterization.mockResolvedValueOnce({
      script: parameterizedScript,
      diffs: [{
        suggestion_id: 'pz_openapi_order_id',
        kind: 'data_param',
        path: '/actions/0/target',
        before: '/orders/123',
        after: '/orders/{{param.ORDER_ID}}',
      }],
      applied_count: 1,
    })
    apiMock.applySecretBindings.mockImplementationOnce(async ({ script }: { script: typeof parameterizedScript }) => ({
      script,
      secret_binding_requirements: [],
    }))
    apiMock.commitOpenAPIImport.mockResolvedValueOnce({
      stored_script: {
        id: 'saved-protected-openapi',
        name: parameterizedScript.name,
        protocol: 'http',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: parameterizedScript,
      },
    })

    await act(async () => root.render(<ProtocolAssetImport kind="openapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], 'orders.yaml', { type: 'application/yaml' })] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert OpenAPI'))?.click()
    })
    await flushPromises()

    const applyParameterization = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Apply Selected'))
    expect(applyParameterization).toBeDefined()
    await act(async () => applyParameterization?.click())
    await flushPromises()

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save and Continue Importing with 1 secret to configure'))
    expect(save).toBeDefined()
    await act(async () => save?.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledWith(expect.objectContaining({
      script: expect.objectContaining({
        actions: [expect.objectContaining({ target: '/orders/{{param.ORDER_ID}}' })],
      }),
      bindings: [{ requirement_id: 'cred-openapi-token', secret_name: 'OPENAPI_TOKEN' }],
    }))
    expect(apiMock.commitOpenAPIImport).toHaveBeenCalledWith(expect.objectContaining({
      import_session_id: 'openapi-protected-overrides',
      secret_bindings: { 'cred-openapi-token': 'OPENAPI_TOKEN' },
      parameterization_selected_ids: ['pz_openapi_order_id'],
    }))
  })

  it('saves an OpenAPI preservation draft when zero bindings leave review requirements in place', async () => {
    const preservedScript = {
      name: 'Runnable Orders API',
      version: '1.0.0',
      protocol: 'http',
      metadata: {
        import: {
          source: 'openapi',
          preserved: { import_value_policy: 'preserve' },
        },
      },
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Get Order',
        type: 'request',
        method: 'GET',
        target: '/orders/123',
        headers: { Authorization: 'Bearer imported-runtime-token' },
      }],
    }
    apiMock.importOpenAPI.mockResolvedValueOnce({
      import_session_id: 'openapi-preserved-values',
      expires_at: '2099-07-26T12:30:00Z',
      scripts: [],
      script: preservedScript,
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { format: 'openapi', converted_actions: 1 },
      summary: { title: 'Runnable Orders API' },
      secret_binding_requirements: [{
        id: 'cred-openapi-token',
        path: '/actions/0/headers/Authorization',
        label: 'Authorization header',
        context: 'Get Order',
        suggested_name: 'OPENAPI_TOKEN',
        destination_value_preserved: true,
      }],
    })
    apiMock.commitOpenAPIImport.mockResolvedValueOnce({
      stored_script: {
        id: 'saved-preserved-openapi',
        name: preservedScript.name,
        protocol: 'http',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: preservedScript,
      },
    })

    await act(async () => root.render(<ProtocolAssetImport kind="openapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], 'orders.yaml', { type: 'application/yaml' })] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert OpenAPI'))?.click()
    })
    await flushPromises()

    expect(container.textContent).toContain('Preserved in script')
    expect(container.textContent).toContain('0 bindings need setup')
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === 'Save and Continue Importing')
    expect(save).toBeDefined()
    await act(async () => save?.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
    expect(apiMock.commitOpenAPIImport).toHaveBeenCalledWith(expect.objectContaining({
      import_session_id: 'openapi-preserved-values',
      script: expect.objectContaining({
        actions: [expect.objectContaining({
          headers: { Authorization: 'Bearer imported-runtime-token' },
        })],
      }),
    }))
    expect(container.textContent).not.toContain('Resolve every redacted credential binding before saving this script.')
  })

  it('saves a credential-preserving import before opening the persisted script editor', async () => {
    const onImported = vi.fn()
    const onEdit = vi.fn()
    const preservedScript = {
      name: 'Runnable Orders API',
      version: '1.0.0',
      protocol: 'http',
      metadata: {
        import: {
          source: 'openapi',
          preserved: {
            import_value_policy: 'preserve',
            server_import_attestation_v1: 'protected:openapi-preserved-editor',
          },
        },
      },
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Get Order',
        type: 'request',
        method: 'GET',
        target: '/orders/123',
        headers: { Authorization: 'Bearer imported-runtime-token' },
      }],
    }
    apiMock.importOpenAPI.mockResolvedValueOnce({
      import_session_id: 'openapi-preserved-editor',
      expires_at: '2099-07-26T12:30:00Z',
      import_draft_expires_at: '2099-07-26T12:30:00Z',
      scripts: [],
      script: preservedScript,
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { format: 'openapi', converted_actions: 1 },
      summary: { title: 'Runnable Orders API' },
      secret_binding_requirements: [{
        id: 'cred-openapi-token',
        path: '/actions/0/headers/Authorization',
        label: 'Authorization header',
        context: 'Get Order',
        suggested_name: 'OPENAPI_TOKEN',
        destination_value_preserved: true,
      }],
    })
    apiMock.commitOpenAPIImport.mockResolvedValueOnce({
      stored_script: {
        id: 'saved-preserved-openapi',
        name: preservedScript.name,
        protocol: 'http',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: preservedScript,
      },
    })

    await act(async () => root.render(
      <ProtocolAssetImport
        kind="openapi"
        projectId="project-1"
        onImported={onImported}
        onEdit={onEdit}
      />,
    ))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], 'orders.yaml', { type: 'application/yaml' })] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert OpenAPI'))
        ?.click()
    })
    await flushPromises()

    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === 'Save and Open in Visual Editor')
    expect(edit).toBeDefined()
    await act(async () => edit?.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.commitOpenAPIImport).toHaveBeenCalledWith(expect.objectContaining({
      import_session_id: 'openapi-preserved-editor',
      script: expect.objectContaining({
        actions: [expect.objectContaining({
          headers: { Authorization: 'Bearer imported-runtime-token' },
        })],
      }),
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'saved-preserved-openapi' })],
    }))
    expect(onEdit).not.toHaveBeenCalled()
  })

  it('keeps the Postman protected candidate while forwarding credential overrides', async () => {
    const sourceScript = {
      name: 'Protected Postman',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        body: '{"password":"{{secret.POSTMAN_PASSWORD}}"}',
      }],
    }
    apiMock.importPostman.mockResolvedValueOnce({
      import_session_id: 'postman-protected-overrides',
      expires_at: '2099-07-26T12:30:00Z',
      scripts: [],
      script: sourceScript,
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { format: 'postman', converted_actions: 1 },
      summary: { collection_name: 'Protected Postman' },
      secret_binding_requirements: [{
        id: 'cred-postman-password',
        path: '/actions/0/body',
        label: 'Password',
        context: 'Login',
        suggested_name: 'POSTMAN_PASSWORD',
        replacement: '{{secret.POSTMAN_PASSWORD}}',
      }],
    })
    apiMock.applySecretBindings.mockImplementationOnce(async ({ script }: { script: typeof sourceScript }) => ({
      script,
      secret_binding_requirements: [],
    }))
    apiMock.savePostmanImport.mockResolvedValueOnce({
      stored_script: {
        id: 'saved-protected-postman',
        name: sourceScript.name,
        protocol: 'http',
        version: '1.0.0',
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: sourceScript,
      },
    })

    await act(async () => root.render(<ProtocolAssetImport kind="postman" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], 'collection.json', { type: 'application/json' })] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert Postman Collection'))?.click()
    })
    await flushPromises()

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save and Continue Importing with 1 secret to configure'))
    expect(save).toBeDefined()
    await act(async () => save?.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.savePostmanImport).toHaveBeenCalledWith(expect.objectContaining({
      import_session_id: 'postman-protected-overrides',
      secret_bindings: { 'cred-postman-password': 'POSTMAN_PASSWORD' },
    }))
  })

  it.each([
    { kind: 'openapi' as const, convertLabel: 'Convert OpenAPI', fileName: 'target.yaml' },
    { kind: 'postman' as const, convertLabel: 'Convert Postman Collection', fileName: 'target.json' },
  ])('freezes the $kind replace target before deferred credential preparation', async ({ kind, convertLabel, fileName }) => {
    const reviewedScript = {
      name: `Protected ${kind} target`,
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Request', type: 'request', method: 'GET', target: '/',
        headers: { Authorization: 'Bearer {{secret.API_TOKEN}}' },
      }],
    }
    const importResult = {
      import_session_id: `${kind}-target-session`,
      expires_at: '2099-07-26T12:30:00Z',
      scripts: [],
      script: reviewedScript,
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [], correlations: [],
      stats: { format: kind, converted_actions: 1 },
      summary: {},
      secret_binding_requirements: [{
        id: `${kind}-target-token`,
        path: '/actions/0/headers/Authorization',
        label: 'Authorization',
        context: 'Bearer credential',
        suggested_name: 'API_TOKEN',
        replacement: '{{secret.API_TOKEN}}',
      }],
    }
    const existingScripts = ['target-a', 'target-b'].map((id, index) => ({
      id,
      name: `Target ${index === 0 ? 'A' : 'B'}`,
      protocol: 'http',
      version: '1.0.0',
      project_id: 'project-1',
      created_at: '2026-01-01T00:00:00Z',
      script: { ...reviewedScript, name: `Target ${index === 0 ? 'A' : 'B'}` },
    }))
    apiMock.listScripts.mockResolvedValueOnce({ scripts: existingScripts, count: existingScripts.length })
    if (kind === 'openapi') apiMock.importOpenAPI.mockResolvedValueOnce(importResult)
    else apiMock.importPostman.mockResolvedValueOnce(importResult)

    let resolveBindings!: (value: unknown) => void
    apiMock.applySecretBindings.mockReturnValueOnce(new Promise(resolve => { resolveBindings = resolve }))
    const storedResponse = {
      stored_script: {
        id: `${kind}-saved-target-a`,
        name: reviewedScript.name,
        protocol: reviewedScript.protocol,
        version: reviewedScript.version,
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script: reviewedScript,
      },
    }
    if (kind === 'openapi') apiMock.commitOpenAPIImport.mockResolvedValueOnce(storedResponse)
    else apiMock.savePostmanImport.mockResolvedValueOnce(storedResponse)

    await act(async () => root.render(<ProtocolAssetImport kind={kind} projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], fileName)] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes(convertLabel))?.click()
    })
    await flushPromises()

    const modeSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.closest('label')?.textContent?.includes('Mode'))!
    await act(async () => {
      modeSelect.value = 'replace'
      modeSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const targetSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.closest('label')?.querySelector('span')?.textContent === 'Script')!
    await act(async () => {
      targetSelect.value = 'target-a'
      targetSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Replace Script and Continue Importing with 1 secret to configure'))!
    act(() => save.click())
    await flushPromises()
    expect(apiMock.applySecretBindings).toHaveBeenCalledTimes(1)
    expect(targetSelect.disabled).toBe(true)

    // Challenge the state machine directly: even if an external DOM driver
    // dispatches a change while disabled, the deferred save remains bound to A.
    await act(async () => {
      targetSelect.disabled = false
      targetSelect.value = 'target-b'
      targetSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      resolveBindings({ script: reviewedScript, secret_binding_requirements: [] })
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    const saveMock = kind === 'openapi' ? apiMock.commitOpenAPIImport : apiMock.savePostmanImport
    expect(saveMock).toHaveBeenCalledWith(expect.objectContaining({
      mode: 'replace',
      target_script_id: 'target-a',
    }))
  })

  it.each([
    { kind: 'openapi' as const, convertLabel: 'Convert OpenAPI', fileName: 'large.yaml' },
    { kind: 'postman' as const, convertLabel: 'Convert Postman Collection', fileName: 'large.json' },
  ])('pages and searches the $kind replacement library on the server', async ({ kind, convertLabel, fileName }) => {
    const reviewedScript = {
      name: `Imported ${kind}`,
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{ name: 'Request', type: 'request', method: 'GET', target: '/' }],
    }
    const importResult = {
      scripts: [], script: reviewedScript, warnings: [], unsupported_elements: [], assumptions: [],
      review_sections: [], repair_suggestions: [], correlations: [], stats: { converted_actions: 1 }, summary: {},
    }
    if (kind === 'openapi') apiMock.importOpenAPI.mockResolvedValueOnce(importResult)
    else apiMock.importPostman.mockResolvedValueOnce(importResult)
    apiMock.listScripts.mockImplementation(async (_projectId, _folderId, options) => {
      const offset = options?.offset || 0
      const search = options?.search || ''
      const count = search ? 1 : 250
      const size = Math.min(options?.limit || count, Math.max(0, count - offset))
      return {
        scripts: Array.from({ length: size }, (_, index) => ({
          id: `target-${offset + index}`,
          name: search ? `Matching ${search}` : `Target ${offset + index}`,
          protocol: 'http', version: '1.0.0', project_id: 'project-1', created_at: '2026-01-01T00:00:00Z',
          script: { ...reviewedScript, name: `Target ${offset + index}` },
        })),
        count,
        limit: options?.limit,
        offset,
      }
    })

    await act(async () => root.render(<ProtocolAssetImport kind={kind} projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], fileName)] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes(convertLabel))?.click()
    })
    await flushPromises()
    const modeSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.closest('label')?.textContent?.includes('Mode'))!
    await act(async () => {
      modeSelect.value = 'replace'
      modeSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.listScripts).toHaveBeenLastCalledWith('project-1', undefined, {
      limit: 100, offset: 0, search: '',
    })
    const targetSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.closest('label')?.querySelector('span')?.textContent === 'Script')!
    expect(targetSelect.options).toHaveLength(101)
    const pager = container.querySelector<HTMLElement>(`[aria-label="${kind === 'openapi' ? 'OpenAPI' : 'Postman'} replacement script pages"]`)!
    expect(pager.textContent).toContain('Showing 1–100 of 250')
    await act(async () => {
      Array.from(pager.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Next')?.click()
    })
    await flushPromises()
    expect(apiMock.listScripts).toHaveBeenLastCalledWith('project-1', undefined, {
      limit: 100, offset: 100, search: '',
    })

    const search = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="search"]'))
      .find(input => input.closest('label')?.textContent?.includes('Find script'))!
    await act(async () => {
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set?.call(search, 'orders')
      search.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.listScripts).toHaveBeenLastCalledWith('project-1', undefined, {
      limit: 100, offset: 0, search: 'orders',
    })
    expect(targetSelect.options).toHaveLength(2)
  })

  it('does not retry an unavailable OpenAPI protected session through the legacy create path', async () => {
    apiMock.commitOpenAPIImport.mockRejectedValueOnce(Object.assign(
      new Error('import session not found or expired'),
      { status: 404 },
    ))

    await act(async () => root.render(<ProtocolAssetImport kind="openapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['{}'], 'orders.yaml', { type: 'application/yaml' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert OpenAPI'))
        ?.click()
    })
    await flushPromises()

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save and Continue Importing'))
    expect(save).toBeDefined()
    await act(async () => save?.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.commitOpenAPIImport).toHaveBeenCalledTimes(1)
    expect(apiMock.commitOpenAPIImport).toHaveBeenCalledWith(expect.objectContaining({
      import_session_id: 'openapi-session-1',
    }))
    expect(container.textContent).toContain('import session not found or expired')
  })

  it('retains the same OpenAPI protected candidate for an explicit retry after a transient failure', async () => {
    apiMock.commitOpenAPIImport.mockRejectedValueOnce(new Error('temporary network failure'))

    await act(async () => root.render(<ProtocolAssetImport kind="openapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['{}'], 'orders.yaml', { type: 'application/yaml' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert OpenAPI'))
        ?.click()
    })
    await flushPromises()

    const findSave = () => Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save and Continue Importing'))
    await act(async () => findSave()?.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.commitOpenAPIImport).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('temporary network failure')
    const firstPayload = apiMock.commitOpenAPIImport.mock.calls[0][0]

    await act(async () => findSave()?.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.commitOpenAPIImport).toHaveBeenCalledTimes(2)
    expect(apiMock.commitOpenAPIImport.mock.calls[1][0]).toEqual(firstPayload)
    expect(firstPayload).toEqual(expect.objectContaining({
      import_session_id: 'openapi-session-1',
      project_id: 'project-1',
    }))
  })

  it('does not retry an unavailable Postman protected session through the legacy create path', async () => {
    apiMock.savePostmanImport.mockRejectedValueOnce(Object.assign(
      new Error('import session not found or expired'),
      { status: 404 },
    ))

    await act(async () => root.render(<ProtocolAssetImport kind="postman" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['{}'], 'collection.json', { type: 'application/json' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert Postman Collection'))
        ?.click()
    })
    await flushPromises()

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save and Continue Importing'))
    expect(save).toBeDefined()
    await act(async () => save?.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.savePostmanImport).toHaveBeenCalledTimes(1)
    expect(apiMock.savePostmanImport).toHaveBeenCalledWith(expect.objectContaining({
      import_session_id: 'postman-session-1',
    }))
    expect(container.textContent).toContain('import session not found or expired')
  })

  it('invalidates the protected OpenAPI candidate after an arbitrary runtime edit', async () => {
    await act(async () => root.render(<ProtocolAssetImport kind="openapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], 'orders.yaml', { type: 'application/yaml' })] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert OpenAPI'))?.click()
    })
    await flushPromises()

    const thinkTimeInput = Array.from(container.querySelectorAll<HTMLLabelElement>('label'))
      .find(label => label.textContent?.includes('Think Time (ms)'))
      ?.querySelector<HTMLInputElement>('input')
    expect(thinkTimeInput).toBeDefined()
    await act(async () => {
      if (thinkTimeInput) setInputValue(thinkTimeInput, '1750')
    })
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save and Continue Importing'))
    await act(async () => save?.click())
    await flushPromises()

    expect(apiMock.commitOpenAPIImport).toHaveBeenCalledWith(expect.objectContaining({
      import_session_id: undefined,
    }))
  })

  it('renders DevWeb supported protocols and limitations from its support matrix', async () => {
    apiMock.getDevWebSupportMatrix.mockResolvedValueOnce({
      support_matrix: [
        { id: 'devweb-http-webrequest', area: 'HTTP', construct: 'load.WebRequest({...}).send()', status: 'supported', execution_status: 'executable' },
        { id: 'devweb-grpc-unary', area: 'gRPC', construct: 'client.invoke(service, method, payload)', status: 'partial', execution_status: 'review_required', review_required: true, notes: 'Proto files and metadata require review.' },
        { id: 'devweb-dynamic-javascript', area: 'JavaScript Runtime', construct: 'dynamic variables, loops, custom modules, async control flow', status: 'review_only', execution_status: 'not_executable', review_required: true },
      ],
      notes: ['Mixed HTTP/WebSocket/gRPC DevWeb files should be split by protocol before high-scale execution.'],
    })

    await act(async () => {
      root.render(<ProtocolAssetImport kind="devweb" projectId="project-1" />)
    })
    await flushPromises()

    const coverage = container.querySelector('[aria-label="Import DevWeb Script coverage"]') as HTMLElement | null
    expect(coverage).not.toBeNull()
    const coverageButton = coverage?.querySelector('button') as HTMLButtonElement | null
    expect(coverageButton).not.toBeNull()
    await act(async () => {
      coverageButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(coverage?.textContent).toContain('Supported protocols')
    expect(coverage?.textContent).toContain('HTTP')
    expect(coverage?.textContent).toContain('WebSocket')
    expect(coverage?.textContent).toContain('gRPC unary')
    expect(coverage?.textContent).toContain('Review required')
    expect(coverage?.textContent).toContain('client.invoke(service, method, payload)')
    expect(coverage?.textContent).toContain('Review-only')
    expect(coverage?.textContent).toContain('dynamic variables, loops, custom modules, async control flow')
  })

  it('renders Postman supported protocols and limitations from its support matrix', async () => {
    apiMock.getPostmanSupportMatrix.mockResolvedValueOnce({
      support_matrix: [
        { id: 'postman-requests', area: 'Operations', construct: 'HTTP requests', status: 'supported' },
        { id: 'postman-auth-oauth2', area: 'Security', construct: 'oauth2', status: 'partial', review_required: true, notes: 'Token acquisition must be configured before replay.' },
        { id: 'postman-scripts', area: 'Scripts', construct: 'pre-request / test scripts', status: 'review_only', review_required: true },
      ],
      notes: ['Postman import targets HTTP API replay; pre-request and test scripts are not executed.'],
    })

    await act(async () => {
      root.render(<ProtocolAssetImport kind="postman" projectId="project-1" />)
    })
    await flushPromises()

    const coverage = container.querySelector('[aria-label="Import Postman Collection coverage"]') as HTMLElement | null
    expect(coverage).not.toBeNull()
    const coverageButton = coverage?.querySelector('button') as HTMLButtonElement | null
    expect(coverageButton).not.toBeNull()
    await act(async () => {
      coverageButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(coverage?.textContent).toContain('Supported protocols')
    expect(coverage?.textContent).toContain('HTTP')
    expect(coverage?.textContent).toContain('oauth2')
    expect(coverage?.textContent).toContain('Token acquisition must be configured before replay.')
    expect(coverage?.textContent).toContain('pre-request / test scripts')
  })

  it('reviews OpenAPI correlations and replaces by operation id', async () => {
    const onImported = vi.fn()
    apiMock.getOpenAPISupportMatrix.mockResolvedValueOnce({
      support_matrix: [
        { id: 'openapi-operations', area: 'Operations', construct: 'HTTP operations', status: 'supported' },
        { id: 'openapi-chaining', area: 'Replay', construct: 'cross-operation chaining', status: 'supported', review_required: true, notes: 'Low-confidence matches require review.' },
      ],
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
      import_session_id: 'openapi-session-replace',
      expires_at: '2099-07-26T12:30:00Z',
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

    expect(container.textContent).toContain('Import Coverage')
    await openImportCoverage(container)
    expect(container.textContent).toContain('Supported protocols')
    expect(container.textContent).toContain('HTTP / REST')
    expect(container.textContent).toContain('Review required')
    expect(container.textContent).toContain('cross-operation chaining')
    await closeImportCoverage(container)

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

    const disabledReplace = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Replace Script'))!
    expect(disabledReplace.disabled).toBe(true)
    const disabledReasonId = disabledReplace.getAttribute('aria-describedby')
    expect(disabledReasonId).toBeTruthy()
    expect(document.getElementById(disabledReasonId!)?.textContent)
      .toContain('Select the existing script that this OpenAPI import should replace')

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
    expect(container.querySelector('.script-import-review-save-disabled-reason')).toBeNull()

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Replace Script'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.commitOpenAPIImport).toHaveBeenCalledWith(expect.objectContaining({
      import_session_id: 'openapi-session-replace',
      project_id: 'project-1',
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
    expect(container.textContent).toContain('Preflight')
    expect(container.textContent).toContain('HAR replay accounting')
  })

  it('rejects an oversized comparison HAR before it reaches conversion', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="har" projectId="project-1" />)
    })
    const fileInputs = container.querySelectorAll<HTMLInputElement>('input[type="file"]')
    const primary = new File(['{"log":{"entries":[]}}'], 'capture.har', { type: 'application/json' })
    const oversized = new File(['{}'], 'comparison.har', { type: 'application/json' })
    Object.defineProperty(oversized, 'size', { configurable: true, value: 51 * 1024 * 1024 })

    await act(async () => {
      Object.defineProperty(fileInputs[0], 'files', { configurable: true, value: [primary] })
      fileInputs[0].dispatchEvent(new Event('change', { bubbles: true }))
      Object.defineProperty(fileInputs[1], 'files', { configurable: true, value: [oversized] })
      fileInputs[1].dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('comparison.har is 51.0 MB; imports are limited')
    await act(async () => {
      Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert HAR'))
        ?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importHAR).toHaveBeenCalledWith(primary, expect.objectContaining({
      secondary_file: undefined,
      artifact_mode: 'design_har',
    }))
  })

  it('rejects unsupported dropped import files before conversion', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="har" projectId="project-1" />)
    })

    const zone = container.querySelector('.import-dropzone')
    expect(zone).not.toBeNull()
    if (!zone) return
    const file = new File(['not a har'], 'notes.txt', { type: 'text/plain' })
    const drop = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(drop, 'dataTransfer', { value: { files: [file] } })
    await act(async () => {
      zone.dispatchEvent(drop)
    })

    expect(container.textContent).toContain('notes.txt is not a supported har file')
    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert HAR'))
    expect(convertButton).toBeDefined()
    expect((convertButton as HTMLButtonElement | undefined)?.disabled).toBe(true)
    expect(apiMock.importHAR).not.toHaveBeenCalledWith(file, expect.anything())
  })

	  it('imports Salesforce HAR files through the native review flow', async () => {
	    const salesforceResult = await apiMock.importSalesforce()
	    apiMock.importHAR.mockResolvedValueOnce(salesforceResult)
	    await act(async () => {
	      root.render(<ProtocolAssetImport kind="har" projectId="project-1" />)
	    })

	    expect(container.textContent).toContain('Import HAR')

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

	    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert HAR'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

	    expect(apiMock.importHAR).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1', salesforce_native: true }))
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
      support_matrix: [
        { id: 'raml-resources', area: 'Resources', construct: 'nested resources + URI params', status: 'supported' },
        { id: 'raml-types', area: 'Types', construct: 'RAML data types / schemas', status: 'review_only', review_required: true, notes: 'Provide examples for body generation.' },
      ],
      notes: ['RAML import targets common HTTP/JSON API specifications.'],
    })
    await act(async () => {
      root.render(<ProtocolAssetImport kind="raml" projectId="project-1" />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Import RAML')
    // RAML support-matrix panel (G2)
    expect(container.textContent).toContain('Import Coverage')
    await openImportCoverage(container)
    expect(container.textContent).toContain('HTTP / REST')
    expect(container.textContent).toContain('RAML data types / schemas')
    await closeImportCoverage(container)

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
    expect(container.textContent).toContain('Import Coverage')
    await openImportCoverage(container)
    expect(container.textContent).toContain('Kafka')
    expect(container.textContent).toContain('JMS Solace')
    expect(container.textContent).toContain('avro schemaFormat')
    expect(container.textContent).toContain('MQTT operations are review-only.')
    await closeImportCoverage(container)
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

  it('auto-applies redacted AsyncAPI bindings by script_index before saving all scripts', async () => {
    const kafkaScript = {
      name: 'Orders Kafka',
      version: '1.0.0',
      protocol: 'kafka',
      config: { base_url: '', kafka: { brokers: ['broker:9092'], mock: true } },
      actions: [{ name: 'Publish Order', type: 'kafka_produce', target: 'orders.created' }],
    }
    const redactedJMSScript = {
      name: 'Orders JMS',
      version: '1.0.0',
      protocol: 'jms-solace',
      config: { base_url: '', jms: { provider: 'solace', password: '[REDACTED]', mock: true } },
      actions: [{ name: 'Publish Shipment', type: 'jms_publish', target: 'shipments' }],
    }
    const resolvedJMSScript = {
      ...redactedJMSScript,
      config: { base_url: '', jms: { provider: 'solace', password: '{{secret.JMS_PASSWORD}}', mock: true } },
    }
    apiMock.importAsyncAPI.mockResolvedValueOnce({
      scripts: [kafkaScript, redactedJMSScript],
      script: kafkaScript,
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { format: 'asyncapi', converted_actions: 2 },
      summary: { topics: ['orders.created', 'shipments'] },
      secret_binding_requirements: [{
        id: 'jms-password',
        path: '/config/jms/password',
        label: 'Orders JMS — Broker password',
        context: 'JMS broker credential',
        suggested_name: 'JMS_PASSWORD',
        script_index: 1,
        script_name: 'Orders JMS',
      }],
    })
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: resolvedJMSScript,
      secret_binding_requirements: [],
    })
    apiMock.commitAsyncAPIImportBatch
      .mockRejectedValueOnce(new Error('atomic script save unavailable'))
      .mockResolvedValueOnce({
        scripts: [
        {
          id: 'saved-kafka',
          name: 'Orders Kafka',
          protocol: 'kafka',
          version: '1.0.0',
          created_at: '2026-01-01T00:00:00Z',
          script: kafkaScript,
        },
        {
          id: 'saved-jms',
          name: 'Orders JMS',
          protocol: 'jms-solace',
          version: '1.0.0',
          created_at: '2026-01-01T00:00:00Z',
          script: resolvedJMSScript,
        },
        ],
        count: 2,
      })

    await act(async () => {
      root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" />)
    })
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')
    expect(fileInput).not.toBeNull()
    const file = new File(['asyncapi: 3.0.0'], 'orders.asyncapi.yaml', { type: 'application/yaml' })
    await act(async () => {
      Object.defineProperty(fileInput!, 'files', { configurable: true, value: [file] })
      fileInput!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convertButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert AsyncAPI'))
    await act(async () => convertButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()

    const jmsTab = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('jms-solace · Orders JMS'))
    await act(async () => jmsTab?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(container.querySelector('.secret-binding-review-script')?.textContent).toContain('Orders JMS')
    const primarySave = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save All 2 Scripts Atomically with 1 secret to configure'))
    const saveAll = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save All 2 Scripts with 1 secret to configure'))
    expect(primarySave?.disabled).toBe(false)
    expect(saveAll?.disabled).toBe(false)
    const runtimeControls = Array.from(container.querySelectorAll<HTMLInputElement | HTMLSelectElement>('.import-runtime-options input, .import-runtime-options select'))
    expect(runtimeControls.length).toBeGreaterThan(0)
    expect(runtimeControls.some(control => control.disabled)).toBe(false)
    const secretValue = container.querySelector<HTMLInputElement>('input[aria-label="Secret value for JMS_PASSWORD"]')!
    await act(async () => setInputValue(secretValue, 'write-only-jms-password'))
    await act(async () => saveAll?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledWith({
      script: redactedJMSScript,
      script_index: 1,
      bindings: [{ requirement_id: 'jms-password', secret_name: 'JMS_PASSWORD' }],
    })
    expect(container.textContent).toContain('{{secret.JMS_PASSWORD}}')
    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('write-only-jms-password')
    expect(container.textContent).toContain('Project-secret values prepared during review remain configured and will be reused')
    const firstOperationID = apiMock.commitAsyncAPIImportBatch.mock.calls[0][0].operation_id
    const retrySaveAll = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save All 2 Scripts'))!
    await act(async () => retrySaveAll.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
    expect(apiMock.commitAsyncAPIImport).not.toHaveBeenCalled()
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(2)
    expect(apiMock.commitAsyncAPIImportBatch.mock.calls[1][0].operation_id).toBe(firstOperationID)
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenLastCalledWith(expect.objectContaining({
      scripts: expect.arrayContaining([expect.objectContaining({
        name: 'Orders JMS',
        config: expect.objectContaining({
          jms: expect.objectContaining({ password: '{{secret.JMS_PASSWORD}}' }),
        }),
      })]),
    }))
  })

  it('saves an exact literal [REDACTED] when structural attestation says the destination value is preserved', async () => {
    const kafkaScript = {
      name: 'Orders Kafka',
      version: '1.0.0',
      protocol: 'kafka',
      config: { base_url: '', kafka: { brokers: ['broker:9092'], mock: true } },
      actions: [{ name: 'Publish Order', type: 'kafka_produce', target: 'orders.created' }],
    }
    const preservedJMSScript = {
      name: 'Orders JMS',
      version: '1.0.0',
      protocol: 'jms-solace',
      metadata: {
        import: {
          source: 'asyncapi',
          preserved: { import_value_policy: 'preserve' },
        },
      },
      config: { base_url: '', jms: { provider: 'solace', password: '[REDACTED]', mock: true } },
      actions: [{ name: 'Publish Shipment', type: 'jms_publish', target: 'shipments' }],
    }
    apiMock.importAsyncAPI.mockResolvedValueOnce({
      scripts: [kafkaScript, preservedJMSScript],
      script: kafkaScript,
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { format: 'asyncapi', converted_actions: 2 },
      summary: { topics: ['orders.created', 'shipments'] },
      secret_binding_requirements: [{
        id: 'jms-password',
        path: '/config/jms/password',
        label: 'Orders JMS — Broker password',
        context: 'JMS broker credential',
        suggested_name: 'JMS_PASSWORD',
        script_index: 1,
        script_name: 'Orders JMS',
        destination_value_preserved: true,
      }],
    })
    apiMock.commitAsyncAPIImportBatch.mockResolvedValueOnce({
      scripts: [
        {
          id: 'saved-kafka-preserved',
          name: kafkaScript.name,
          protocol: kafkaScript.protocol,
          version: kafkaScript.version,
          created_at: '2026-01-01T00:00:00Z',
          script: kafkaScript,
        },
        {
          id: 'saved-jms-preserved',
          name: preservedJMSScript.name,
          protocol: preservedJMSScript.protocol,
          version: preservedJMSScript.version,
          created_at: '2026-01-01T00:00:00Z',
          script: preservedJMSScript,
        },
      ],
      count: 2,
    })

    await act(async () => root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['asyncapi: 3.0.0'], 'orders.asyncapi.yaml')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert AsyncAPI'))?.click()
    })
    await flushPromises()

    const jmsTab = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tab"]'))
      .find(button => button.textContent?.includes('Orders JMS'))!
    await act(async () => jmsTab.click())
    expect(container.querySelector('.secret-binding-review-script')?.textContent).toContain('Orders JMS')
    expect(container.textContent).toContain('Preserved in script')
    expect(container.textContent).toContain('0 bindings need setup')
    const saveAll = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === 'Save All 2 Scripts')
    expect(saveAll).toBeDefined()
    await act(async () => saveAll?.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
    expect(apiMock.commitAsyncAPIImport).not.toHaveBeenCalled()
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(1)
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      scripts: expect.arrayContaining([expect.objectContaining({
        name: 'Orders JMS',
        config: expect.objectContaining({
          jms: expect.objectContaining({ password: '[REDACTED]' }),
        }),
      })]),
    }))
  })

  it('isolates write-only credential edits and Save Remaining readiness by AsyncAPI candidate', async () => {
    const first = { name: 'Kafka One', version: '1.0.0', protocol: 'kafka', config: { base_url: '', kafka: { brokers: ['one:9092'], mock: true, sasl: { password: '[REDACTED]' } } }, actions: [] }
    const second = { name: 'Kafka Two', version: '1.0.0', protocol: 'kafka', config: { base_url: '', kafka: { brokers: ['two:9092'], mock: true, sasl: { password: '[REDACTED]' } } }, actions: [] }
    apiMock.importAsyncAPI.mockResolvedValueOnce({
      scripts: [first, second], script: first,
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [], correlations: [],
      stats: { format: 'asyncapi', converted_actions: 0 }, summary: {},
      secret_binding_requirements: [
        { id: 'first-password', path: '/config/kafka/sasl/password', label: 'First password', context: 'Kafka SASL', suggested_name: 'KAFKA_PASSWORD', script_index: 0, script_name: first.name },
        { id: 'second-password', path: '/config/kafka/sasl/password', label: 'Second password', context: 'Kafka SASL', suggested_name: 'KAFKA_PASSWORD', script_index: 1, script_name: second.name },
      ],
    })

    await act(async () => root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['asyncapi: 3.0.0'], 'two.yaml')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convert = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert AsyncAPI'))!
    await act(async () => convert.click())
    await flushPromises()

    const names = container.querySelectorAll<HTMLInputElement>('.secret-binding-review input[type="text"]')
    const values = container.querySelectorAll<HTMLInputElement>('.secret-binding-review input[type="password"]')
    await act(async () => {
      setInputValue(names[0], 'CUSTOM_KAFKA_PASSWORD')
      setInputValue(values[0], 'write-only-tab-value')
    })
    const saveAllBeforeSwitch = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save All 2 Scripts'))!
    expect(saveAllBeforeSwitch.disabled).toBe(true)
    expect(saveAllBeforeSwitch.getAttribute('aria-describedby')).toBe('asyncapi-save-all-disabled-reason')
    expect(container.querySelector('#asyncapi-save-all-disabled-reason')?.textContent)
      .toContain('credential choice belongs to another generated script')
    expect(container.querySelector('.secret-binding-review-script')?.textContent).toContain('Kafka One')
    const secondTab = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tab"]')).find(button => button.textContent?.includes('Kafka Two'))!
    await act(async () => secondTab.click())

    expect(container.querySelector('.secret-binding-review-script')?.textContent).toContain('Kafka Two')
    expect(container.querySelector('.secret-binding-review-script')?.textContent).not.toContain('Kafka One')
    expect(container.querySelectorAll<HTMLInputElement>('.secret-binding-review input[type="text"]')[0].value).toBe('KAFKA_PASSWORD')
    expect(container.querySelectorAll<HTMLInputElement>('.secret-binding-review input[type="password"]')[0].value).toBe('')
    expect(container.textContent).not.toContain('write-only-tab-value')
  })

  it('keeps one archive-scoped AsyncAPI source session across credential lane preparation', async () => {
    const first = { name: 'Source Kafka One', version: '1.0.0', protocol: 'kafka', config: { base_url: '', kafka: { brokers: ['one:9092'], mock: true, sasl: { password: '[REDACTED]' } } }, actions: [] }
    const second = { name: 'Source Kafka Two', version: '1.0.0', protocol: 'kafka', config: { base_url: '', kafka: { brokers: ['two:9092'], mock: true, sasl: { password: '[REDACTED]' } } }, actions: [] }
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true, source_secret_migration_policy_enabled: true },
      secrets: [],
    })
    apiMock.importAsyncAPI.mockResolvedValueOnce({
      scripts: [first, second], script: first,
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [], correlations: [],
      stats: { format: 'asyncapi', converted_actions: 0 }, summary: {},
      source_secret_session_id: 'asyncapi-archive-source-session',
      source_secret_session_expires_at: '2099-01-01T00:00:00Z',
      source_secret_migration_allowed: true,
      secret_binding_requirements: [
        { id: 'first-source-password', path: '/config/kafka/sasl/password', label: 'First password', context: 'Kafka SASL', suggested_name: 'KAFKA_PASSWORD', script_index: 0, script_name: first.name, source_value_available: true },
        { id: 'second-source-password', path: '/config/kafka/sasl/password', label: 'Second password', context: 'Kafka SASL', suggested_name: 'KAFKA_PASSWORD', script_index: 1, script_name: second.name, source_value_available: true },
      ],
    })
    apiMock.applySecretBindings
      .mockResolvedValueOnce({ script: { ...first, config: { ...first.config, kafka: { ...first.config.kafka, sasl: { password: '{{secret.KAFKA_PASSWORD}}' } } } }, secret_binding_requirements: [] })
      .mockResolvedValueOnce({ script: { ...second, config: { ...second.config, kafka: { ...second.config.kafka, sasl: { password: '{{secret.KAFKA_PASSWORD_2}}' } } } }, secret_binding_requirements: [] })

    await act(async () => root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['asyncapi: 3.0.0'], 'source-secrets.yaml')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convert = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert AsyncAPI'))!
    await act(async () => convert.click())
    await flushPromises()

    const migrate = container.querySelector<HTMLInputElement>('.credentials-source-migration input')!
    await act(async () => migrate.click())
    const prepare = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Prepare Selected Credentials'))!
    await act(async () => prepare.click())
    await flushPromises()

    expect(apiMock.migrateImportSecrets).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'asyncapi-archive-source-session',
      secrets: [
        { requirement_id: 'first-source-password', secret_name: 'KAFKA_PASSWORD' },
        { requirement_id: 'second-source-password', secret_name: 'KAFKA_PASSWORD_2' },
      ],
    })
    expect(apiMock.discardImportSecretSession).not.toHaveBeenCalled()
    expect(container.querySelector('.secret-binding-review-script')?.textContent).toContain(second.name)
    expect(container.querySelector<HTMLInputElement>('.secret-binding-review input[type="text"]')?.value).toBe('KAFKA_PASSWORD_2')

    const atomicSave = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save All 2 Scripts Atomically'))!
    await act(async () => atomicSave.click())
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledTimes(2)
    expect(apiMock.commitAsyncAPIImport).not.toHaveBeenCalled()
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(1)
    expect(apiMock.discardImportSecretSession).not.toHaveBeenCalled()
  })

  it('paginates high-cardinality AsyncAPI script selectors with roving keyboard selection', async () => {
    const scripts = Array.from({ length: 51 }, (_unused, index) => ({
      name: `Async lane ${String(index + 1).padStart(2, '0')}`,
      version: '1.0.0',
      protocol: 'kafka',
      config: { base_url: '', kafka: { brokers: ['broker:9092'], mock: true } },
      actions: [],
    }))
    apiMock.importAsyncAPI.mockResolvedValueOnce({
      scripts,
      script: scripts[0],
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [], correlations: [],
      stats: { format: 'asyncapi', converted_actions: 0 }, summary: {}, secret_binding_requirements: [],
    })

    await act(async () => root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['asyncapi: 3.0.0'], 'many.yaml')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convert = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert AsyncAPI'))!
    await act(async () => convert.click())
    await flushPromises()

    expect(container.querySelectorAll<HTMLButtonElement>('.sample-import-script-tab')).toHaveLength(50)
    expect(container.textContent).toContain('Showing 1–50 of 51')
    const nextPage = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Next scripts'))!
    await act(async () => nextPage.click())
    expect(container.querySelectorAll<HTMLButtonElement>('.sample-import-script-tab')).toHaveLength(1)
    expect(container.textContent).toContain('Showing 51–51 of 51')
    const lastLane = container.querySelector<HTMLButtonElement>('.sample-import-script-tab')!
    expect(lastLane.getAttribute('aria-selected')).toBe('true')

    await act(async () => {
      lastLane.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }))
    })
    const selectedLane = Array.from(container.querySelectorAll<HTMLButtonElement>('.sample-import-script-tab'))
      .find(button => button.getAttribute('aria-selected') === 'true')!
    expect(selectedLane.textContent).toContain('Async lane 50')
    expect(document.activeElement).toBe(selectedLane)
  })

  it('keeps individually saved AsyncAPI lanes in review and never saves them twice', async () => {
    const onImported = vi.fn()
    apiMock.commitAsyncAPIImport.mockImplementation(async ({ script }: { script: { name: string; protocol: string; version: string } }) => ({
      stored_script: {
        id: script.name === 'Orders Kafka' ? 'saved-kafka-lane' : 'saved-jms-lane',
        name: script.name,
        protocol: script.protocol,
        version: script.version,
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script,
      },
    }))
    apiMock.commitAsyncAPIImportBatch.mockImplementation(async ({ scripts }: { scripts: Array<{ name: string; protocol: string; version: string }> }) => ({
      scripts: scripts.map(script => ({
        id: 'saved-jms-lane',
        name: script.name,
        protocol: script.protocol,
        version: script.version,
        project_id: 'project-1',
        created_at: '2026-01-01T00:00:00Z',
        script,
      })),
      count: scripts.length,
    }))

    await act(async () => root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" onImported={onImported} />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['asyncapi: 3.0.0'], 'orders.asyncapi.yaml')],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert AsyncAPI'))?.click()
    })
    await flushPromises()

    const saveSelected = () => Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save Selected Script'))
    await act(async () => saveSelected()?.click())
    await flushPromises()

    expect(apiMock.commitAsyncAPIImport).toHaveBeenCalledTimes(1)
    expect(apiMock.commitAsyncAPIImport).toHaveBeenLastCalledWith(expect.objectContaining({
      script: expect.objectContaining({ name: 'Orders Kafka' }),
    }))
    expect(onImported).toHaveBeenNthCalledWith(1, {
      scripts: [expect.objectContaining({ id: 'saved-kafka-lane' })],
      stayInImportReview: true,
    })
    const tabsAfterFirstSave = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tab"]'))
    expect(tabsAfterFirstSave[0].textContent).toContain('Saved')
    expect(tabsAfterFirstSave[1].getAttribute('aria-selected')).toBe('true')
    expect(container.textContent).toContain('Save Remaining 1 Script')
    expect(container.textContent).toContain('Open Orders Kafka in Visual Editor')

    await act(async () => tabsAfterFirstSave[0].click())
    expect(container.textContent).toContain('Open Orders Kafka in Visual Editor')
    expect(saveSelected()?.disabled).toBe(true)
    await act(async () => saveSelected()?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(apiMock.commitAsyncAPIImport).toHaveBeenCalledTimes(1)

    const saveRemaining = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save Remaining 1 Script'))
    expect(saveRemaining?.disabled).toBe(false)
    await act(async () => saveRemaining?.click())
    await flushPromises()

    expect(apiMock.commitAsyncAPIImport).toHaveBeenCalledTimes(1)
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(1)
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ name: 'Orders JMS' })],
    }))
    expect(onImported).toHaveBeenCalledTimes(2)
    expect(onImported.mock.calls[1][0]).toEqual({
      stayInImportReview: true,
      scripts: [
        expect.objectContaining({ id: 'saved-kafka-lane' }),
        expect.objectContaining({ id: 'saved-jms-lane' }),
      ],
    })
    expect(onImported.mock.calls[1][0].stayInImportReview).toBe(true)
  })

  it('blocks every AsyncAPI draft mutation and save after the credential-preserving draft expires', async () => {
    const first = {
      name: 'Expired Kafka',
      version: '1.0.0',
      protocol: 'kafka',
      config: { base_url: '', kafka: { brokers: ['broker:9092'], mock: true, sasl: { password: 'operator-known-password' } } },
      actions: [{ name: 'Publish', type: 'kafka_produce', target: 'orders/123' }],
      metadata: { import: { source: 'asyncapi', preserved: { import_value_policy: 'preserve' } } },
    }
    const second = {
      name: 'Expired JMS',
      version: '1.0.0',
      protocol: 'jms-solace',
      config: { base_url: '', jms: { provider: 'solace', mock: true } },
      actions: [{ name: 'Consume', type: 'jms_consume', target: 'orders' }],
    }
    apiMock.importAsyncAPI.mockResolvedValueOnce({
      scripts: [first, second],
      script: first,
      import_draft_expires_at: '2000-01-01T00:00:00Z',
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { format: 'asyncapi', converted_actions: 2 },
      summary: {},
      secret_binding_requirements: [{
        id: 'preserved-password',
        path: '/config/kafka/sasl/password',
        label: 'Kafka password',
        context: 'Kafka SASL',
        suggested_name: 'KAFKA_PASSWORD',
        script_index: 0,
        script_name: first.name,
        destination_value_preserved: true,
      }],
      parameterization: {
        suggestions: [{
          id: 'expired-order-id',
          kind: 'data_param',
          script_name: first.name,
          var_name: 'ORDER_ID',
          replacement: '{{param.ORDER_ID}}',
          literal_value: '123',
          confidence: 0.95,
          rule_id: 'path-id',
          rationale: 'Repeated path identifier',
          auto_apply: true,
          high_risk: false,
        }],
        summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
      },
    })

    await act(async () => root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['asyncapi: 3.0.0'], 'expired.yaml')],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert AsyncAPI'))?.click()
    })
    await flushPromises()

    expect(container.textContent).toContain('credential-preserving import draft has expired')
    expect(container.textContent).not.toContain('operator-known-password')
    expect(container.textContent).toContain('[protected imported value]')
    const saveSelected = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save Selected Script'))!
    const saveAll = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save All 2 Scripts'))!
    const applyParameterization = container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')!
    expect(saveSelected.disabled).toBe(true)
    expect(saveAll.disabled).toBe(true)
    expect(applyParameterization.disabled).toBe(true)
    expect(Array.from(container.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLButtonElement>(
      '.secret-binding-review input, .secret-binding-review select, .secret-binding-review button',
    )).every(control => control.disabled)).toBe(true)
    expect(Array.from(container.querySelectorAll<HTMLInputElement | HTMLSelectElement>(
      '.import-runtime-options input, .import-runtime-options select',
    )).every(control => control.disabled)).toBe(true)

    await act(async () => {
      saveSelected.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      saveAll.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      applyParameterization.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(apiMock.commitAsyncAPIImport).not.toHaveBeenCalled()
    expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
    expect(apiMock.applyImportParameterization).not.toHaveBeenCalled()
  })

  it('saves all generated AsyncAPI scripts through one atomic batch commit', async () => {
    const onImported = vi.fn()
    apiMock.commitAsyncAPIImportBatch.mockResolvedValueOnce({
      scripts: [
        {
          id: 'script-asyncapi-kafka',
          name: 'Orders Kafka',
          protocol: 'kafka',
          version: '1.0.0',
          project_id: 'project-1',
          created_at: '2026-01-01T00:00:00Z',
          script: { name: 'Orders Kafka', version: '1.0.0', protocol: 'kafka', config: { base_url: '' }, actions: [] },
        },
        {
          id: 'script-asyncapi-jms',
          name: 'Orders JMS',
          protocol: 'jms-solace',
          version: '1.0.0',
          project_id: 'project-1',
          created_at: '2026-01-01T00:00:00Z',
          script: { name: 'Orders JMS', version: '1.0.0', protocol: 'jms-solace', config: { base_url: '' }, actions: [] },
        },
      ],
      count: 2,
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

    const saveAll = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save All 2 Scripts'))
    expect(saveAll).toBeDefined()
    if (!saveAll) return
    await act(async () => {
      saveAll.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.commitAsyncAPIImport).not.toHaveBeenCalled()
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(1)
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [
        expect.objectContaining({ name: 'Orders Kafka', protocol: 'kafka' }),
        expect.objectContaining({ name: 'Orders JMS', protocol: 'jms-solace' }),
      ],
      operation_id: expect.stringMatching(/^asyncapi-batch-/),
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [
        expect.objectContaining({ id: 'script-asyncapi-kafka' }),
        expect.objectContaining({ id: 'script-asyncapi-jms' }),
      ],
    }))
  })

  it('saves imports into the folder the authoring surface is showing', async () => {
    // Before this was wired, every ProtocolAssetImport mount dropped folderId
    // even though the authoring surface already passed it to ScriptEditor and
    // EnterpriseArchiveImport. Imports silently landed at the project root, and
    // the AsyncAPI commit sent no folder field at all.
    await act(async () => {
      root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" folderId="folder-9" />)
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

    const saveAll = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save All 2 Scripts'))
    expect(saveAll).toBeDefined()
    if (!saveAll) return
    await act(async () => {
      saveAll.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      folder_id: 'folder-9',
    }))
  })

  it('keeps AsyncAPI Save All disabled until deferred parameterization finishes', async () => {
    const firstScript = {
      name: 'Deferred Kafka', version: '1.0.0', protocol: 'kafka', config: { base_url: '' },
      actions: [{ name: 'Publish', type: 'kafka_produce', target: 'orders/123' }],
    }
    const parameterizedScript = {
      ...firstScript,
      actions: [{ ...firstScript.actions[0], target: 'orders/{{param.ORDER_ID}}' }],
    }
    const secondScript = {
      name: 'Deferred JMS', version: '1.0.0', protocol: 'jms-solace', config: { base_url: '' },
      actions: [{ name: 'Consume', type: 'jms_consume', target: 'orders' }],
    }
    apiMock.importAsyncAPI.mockResolvedValueOnce({
      scripts: [firstScript, secondScript],
      script: firstScript,
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [], correlations: [],
      stats: { format: 'asyncapi', converted_actions: 2 },
      summary: { topics: ['orders'] },
      parameterization: {
        suggestions: [{
          id: 'order-id', kind: 'data_param', script_name: firstScript.name, var_name: 'ORDER_ID',
          replacement: '{{param.ORDER_ID}}', literal_value: '123', confidence: 0.95,
          rule_id: 'path-id', rationale: 'Repeated path identifier', auto_apply: true, high_risk: false,
        }],
        summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
      },
    })
    let resolveParameterization: ((value: unknown) => void) | undefined
    apiMock.applyImportParameterization.mockReturnValueOnce(new Promise(resolve => { resolveParameterization = resolve }))
    apiMock.commitAsyncAPIImportBatch.mockImplementation(async ({ scripts }: { scripts: Array<typeof firstScript> }) => ({
      scripts: scripts.map(script => ({
        id: `saved-${script.name}`,
        name: script.name,
        protocol: script.protocol,
        version: script.version,
        created_at: '2026-01-01T00:00:00Z',
        script,
      })),
      count: scripts.length,
    }))

    await act(async () => root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['asyncapi: 3.0.0'], 'deferred.yaml')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert AsyncAPI'))?.click()
    })
    await flushPromises()

    const apply = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Apply Selected'))!
    act(() => apply.click())
    await flushPromises()
    const pendingSaveAll = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save All 2 Scripts'))!
    expect(pendingSaveAll.disabled).toBe(true)
    act(() => pendingSaveAll.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(apiMock.commitAsyncAPIImportBatch).not.toHaveBeenCalled()

    await act(async () => {
      resolveParameterization?.({
        script: parameterizedScript,
        diffs: [{ suggestion_id: 'order-id', kind: 'data_param', path: '/actions/0/target', before: 'orders/123', after: 'orders/{{param.ORDER_ID}}' }],
        applied_count: 1,
      })
      await Promise.resolve()
      await Promise.resolve()
    })
    const readySaveAll = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save All 2 Scripts'))!
    expect(readySaveAll.disabled).toBe(false)
    await act(async () => readySaveAll.click())
    await flushPromises()

    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(1)
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      scripts: expect.arrayContaining([expect.objectContaining({
        actions: [expect.objectContaining({ target: 'orders/{{param.ORDER_ID}}' })],
      })]),
    }))
  })

  it('uses a synchronous Save All lock and reuses the operation id after an atomic failure', async () => {
    let rejectFirstSave!: (reason: unknown) => void
    apiMock.commitAsyncAPIImportBatch
      .mockReturnValueOnce(new Promise((_resolve, reject) => { rejectFirstSave = reject }))
      .mockImplementationOnce(async ({ scripts }: { scripts: Array<{ name: string; protocol: string; version: string }> }) => ({
        scripts: scripts.map((script, index) => ({
          id: `saved-after-retry-${index}`,
          name: script.name,
          protocol: script.protocol,
          version: script.version,
          created_at: '2026-01-01T00:00:00Z',
          script,
        })),
        count: scripts.length,
      }))

    await act(async () => root.render(<ProtocolAssetImport kind="asyncapi" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['asyncapi: 3.0.0'], 'orders.yaml')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert AsyncAPI'))?.click()
    })
    await flushPromises()
    const saveAll = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save All 2 Scripts'))!

    act(() => {
      saveAll.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      saveAll.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(1)
    expect(Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.trim() === 'Saving...')?.disabled).toBe(true)
    const firstOperationID = apiMock.commitAsyncAPIImportBatch.mock.calls[0][0].operation_id
    expect(firstOperationID).toMatch(/^asyncapi-batch-/)

    await act(async () => {
      rejectFirstSave(new Error('atomic batch unavailable'))
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })
    await flushPromises()

    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(1)
    expect(apiMock.deleteScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Retrying Save Remaining safely reuses the same save operation')

    const retry = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save All 2 Scripts'))!
    await act(async () => retry.click())
    await flushPromises()
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(2)
    expect(apiMock.commitAsyncAPIImportBatch.mock.calls[1][0].operation_id).toBe(firstOperationID)
  })

  it('reports an atomically completed AsyncAPI batch without navigation after Back fences the review', async () => {
    const onClose = vi.fn()
    const onImported = vi.fn()
    let resolveFirstSave!: (value: unknown) => void
    apiMock.commitAsyncAPIImportBatch.mockReturnValueOnce(new Promise(resolve => {
      resolveFirstSave = resolve
    }))

    await act(async () => root.render(
      <ProtocolAssetImport
        kind="asyncapi"
        projectId="project-1"
        onClose={onClose}
        onImported={onImported}
      />,
    ))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['asyncapi: 3.0.0'], 'orders.yaml')],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert AsyncAPI'))?.click()
    })
    await flushPromises()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Save All 2 Scripts'))?.click()
      await Promise.resolve()
    })
    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(1)

    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.trim() === 'Back')?.click()
    })
    expect(onClose).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveFirstSave({
        scripts: [
          {
            id: 'asyncapi-kafka', name: 'Orders Kafka', protocol: 'kafka', version: '1.0.0',
            created_at: '2026-01-01T00:00:00Z',
            script: { name: 'Orders Kafka', version: '1.0.0', protocol: 'kafka', config: { base_url: '' }, actions: [] },
          },
          {
            id: 'asyncapi-jms', name: 'Orders JMS', protocol: 'jms-solace', version: '1.0.0',
            created_at: '2026-01-01T00:00:00Z',
            script: { name: 'Orders JMS', version: '1.0.0', protocol: 'jms-solace', config: { base_url: '' }, actions: [] },
          },
        ],
        count: 2,
      })
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.commitAsyncAPIImportBatch).toHaveBeenCalledTimes(1)
    expect(apiMock.deleteScript).not.toHaveBeenCalled()
    expect(onImported).toHaveBeenCalledWith({
      scripts: [
        expect.objectContaining({ id: 'asyncapi-kafka' }),
        expect.objectContaining({ id: 'asyncapi-jms' }),
      ],
      stayInImportReview: true,
    })
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

  it('renders generic blocked-draft preflight for Postman imports', async () => {
    apiMock.importPostman.mockResolvedValueOnce({
      import_session_id: 'postman-blocked-session',
      expires_at: '2099-07-26T12:30:00Z',
      scripts: [],
      script: {
        name: 'Blocked Postman Draft',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://api.example.test' },
        actions: [{ name: 'Blocked request', type: 'request', method: 'GET', target: '/orders' }],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { format: 'postman', converted_actions: 1 },
      summary: {},
      preflight: {
        valid: false,
        action_count: 1,
        expected_action_count: 1,
        data_source_count: 0,
        parameter_count: 0,
        browser_required: false,
        warnings: ['Residual source behavior requires review.'],
        checks: [{
          id: 'import_review_required',
          label: 'Import behavior review',
          status: 'fail',
          detail: 'Replace unsupported source logic before replay.',
        }],
      },
    })

    await act(async () => {
      root.render(<ProtocolAssetImport kind="postman" projectId="project-1" />)
    })
    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const file = new File(['{"info":{"name":"Orders"},"item":[]}'], 'orders.postman_collection.json', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convertButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Convert Postman Collection'))
    await act(async () => convertButton?.click())
    await flushPromises()

    expect(container.textContent).toContain('Preflight')
    expect(container.textContent).toContain('Import behavior review')
    expect(container.textContent).toContain('Replace unsupported source logic before replay.')
  })

  it('rejects Postman collection files in the environment dropzone', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="postman" projectId="project-1" />)
    })

    const fileInputs = container.querySelectorAll('input[type="file"]')
    const environmentInput = fileInputs[1] as HTMLInputElement | undefined
    expect(environmentInput).toBeDefined()
    if (!environmentInput) return

    const collectionFile = new File(['{"info":{"name":"Orders"},"item":[]}'], 'orders.postman_collection.json', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(environmentInput, 'files', { configurable: true, value: [collectionFile] })
      environmentInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('is a Postman collection')
    expect(apiMock.validatePostmanImport).not.toHaveBeenCalled()
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

    const disabledReplace = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Replace Script'))!
    expect(disabledReplace.disabled).toBe(true)
    const disabledReasonId = disabledReplace.getAttribute('aria-describedby')
    expect(disabledReasonId).toBeTruthy()
    expect(document.getElementById(disabledReasonId!)?.textContent)
      .toContain('Select the existing script that this Postman import should replace')

    const scriptSelect = Array.from(container.querySelectorAll('select')).find(select => select.closest('label')?.textContent?.includes('Script')) as HTMLSelectElement | undefined
    expect(scriptSelect).toBeDefined()
    if (!scriptSelect) return
    await act(async () => {
      scriptSelect.value = 'script-existing'
      scriptSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.querySelector('.script-import-review-save-disabled-reason')).toBeNull()

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Replace Script'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.savePostmanImport).toHaveBeenCalledWith(expect.objectContaining({
      import_session_id: 'postman-session-1',
      project_id: 'project-1',
      mode: 'replace',
      target_script_id: 'script-existing',
      script: expect.objectContaining({ name: 'Imported Postman' }),
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'script-existing', name: 'Imported Postman' })],
    }))
  })

  it('clears a Postman replace target when the project scope changes', async () => {
    const storedScript = (id: string, projectID: string, name: string) => ({
      id,
      name,
      protocol: 'http',
      version: '1.0.0',
      project_id: projectID,
      created_at: '2026-01-01T00:00:00Z',
      script: {
        name,
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://api.example.test' },
        actions: [],
      },
    })
    apiMock.listScripts
      .mockResolvedValueOnce({ scripts: [storedScript('project-1-script', 'project-1', 'Project One')], count: 1 })
      .mockResolvedValueOnce({ scripts: [storedScript('project-2-script', 'project-2', 'Project Two')], count: 1 })

    await act(async () => {
      root.render(<ProtocolAssetImport kind="postman" projectId="project-1" />)
    })
    await flushPromises()

    const collectionInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const collectionFile = new File(['{"info":{"name":"Orders"},"item":[]}'], 'orders.postman_collection.json', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(collectionInput, 'files', { configurable: true, value: [collectionFile] })
      collectionInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convertButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Convert Postman Collection'))
    await act(async () => convertButton?.click())
    await flushPromises()

    const modeSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.closest('label')?.textContent?.includes('Mode'))
    await act(async () => {
      if (!modeSelect) return
      modeSelect.value = 'replace'
      modeSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const projectOneTarget = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.closest('label')?.textContent?.includes('Script'))
    await act(async () => {
      if (!projectOneTarget) return
      projectOneTarget.value = 'project-1-script'
      projectOneTarget.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(projectOneTarget?.value).toBe('project-1-script')

    await act(async () => {
      root.render(<ProtocolAssetImport kind="postman" projectId="project-2" />)
    })
    await flushPromises()
    await act(async () => convertButton?.click())
    await flushPromises()

    const projectTwoTarget = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(select => select.closest('label')?.textContent?.includes('Script'))
    expect(projectTwoTarget?.value).toBe('')
    expect(projectTwoTarget?.textContent).toContain('Project Two')
    expect(projectTwoTarget?.textContent).not.toContain('Project One')
  })

  it('deduplicates rapid saves of the same reviewed import', async () => {
    const onImported = vi.fn()
    let resolveSave!: (value: unknown) => void
    apiMock.savePostmanImport.mockReturnValueOnce(new Promise(resolve => {
      resolveSave = resolve
    }))

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

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.savePostmanImport).toHaveBeenCalledTimes(1)

    const nameInput = Array.from(container.querySelectorAll<HTMLInputElement>('.sample-import-grid input'))
      .find(input => input.type === 'text')
    await act(async () => {
      if (nameInput) setInputValue(nameInput, 'Changed after save started')
    })
    await act(async () => {
      resolveSave({
        scripts: [],
        stored_script: {
          id: 'script-postman',
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
        replaced: false,
      })
    })
    await flushPromises()
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'script-postman' })],
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
    expect(container.textContent).not.toContain('chrome_recorder_step:customStep')
    const unsupported = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === '1 unsupported')!
    await act(async () => unsupported.click())
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('chrome_recorder_step:customStep')
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

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const unsupportedRepair = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Apply Unsupported Repair'))
    expect(unsupportedRepair).toBeDefined()
    if (!unsupportedRepair) return
    await act(async () => {
      unsupportedRepair.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Replay suggestion type "update_timeout" is not supported')

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
    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      source_profile: 'recorder_export',
      folder_id: null,
      operation_id: expect.stringMatching(/^import-batch-/),
      scripts: [expect.objectContaining({
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
      })],
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'script-saved-1' })],
    }))
  })

  it('keeps polling the client replay id when the recorder replay start response is lost', async () => {
    apiMock.startDraftBrowserReplaySession.mockRejectedValueOnce(new Error('connection reset'))
    await act(async () => {
      root.render(<ProtocolAssetImport kind="recorder-export" projectId="project-1" />)
    })

    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['{"title":"Login","steps":[]}'], 'login.json', { type: 'application/json' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert Recording'))
        ?.click()
    })
    await flushPromises()

    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Run Replay Validation'))
        ?.click()
    })
    await flushPromises()

    const replayCalls = apiMock.startDraftBrowserReplaySession.mock.calls
    const payload = replayCalls[replayCalls.length - 1]?.[0]
    expect(payload.replay_session_id).toMatch(/^[0-9a-f-]{36}$/)
    expect(container.textContent).toContain(`Replay ${payload.replay_session_id}`)
    expect(container.textContent).toContain('Confirming the accepted replay session')
    expect(container.textContent).not.toContain('Failed to start browser replay validation')
  })

  it('clears rendered recorder replay evidence when the project scope changes', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="recorder-export" projectId="project-1" />)
    })

    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['{"title":"Login","steps":[]}'], 'login.json', { type: 'application/json' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert Recording'))
        ?.click()
    })
    await flushPromises()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Run Replay Validation'))
        ?.click()
    })
    await flushPromises()
    expect(container.textContent).toContain('Replay replay-recorder-1')

    await act(async () => {
      root.render(<ProtocolAssetImport kind="recorder-export" projectId="project-2" />)
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="replay-progress"]')).toBeNull()
    expect(container.textContent).not.toContain('Replay validation passed')
    expect(container.textContent).not.toContain('Replay replay-recorder-1')
  })

  it('does not dispatch duplicate recorder replay starts on rapid clicks', async () => {
    let resolveStart!: (value: unknown) => void
    apiMock.startDraftBrowserReplaySession.mockReturnValueOnce(new Promise(resolve => {
      resolveStart = resolve
    }))
    await act(async () => {
      root.render(<ProtocolAssetImport kind="recorder-export" projectId="project-1" />)
    })
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['{"title":"Login","steps":[]}'], 'login.json', { type: 'application/json' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert Recording'))
        ?.click()
    })
    await flushPromises()
    const replayButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Run Replay Validation'))!
    act(() => {
      replayButton.click()
      replayButton.click()
    })

    expect(apiMock.startDraftBrowserReplaySession).toHaveBeenCalledTimes(1)
    await act(async () => {
      resolveStart({
        replay_session_id: 'replay-recorder-dedupe',
        session: {
          id: 'replay-recorder-dedupe',
          status: 'queued',
          group: 'Login-replay',
          protocol: 'browser',
          current_event_index: -1,
          events: [],
        },
      })
      await Promise.resolve()
    })
  })

  it('invalidates recorder replay evidence after runtime and parameterization mutations', async () => {
    const sourceScript = {
      name: 'Recorder With Parameters', version: '1.0', protocol: 'browser',
      config: { base_url: 'https://shop.example.test' },
      actions: [{ name: 'Navigate', type: 'browser_navigate', target: '/orders/123' }],
    }
    const parameterizedScript = {
      ...sourceScript,
      actions: [{ ...sourceScript.actions[0], target: '/orders/{{param.ORDER_ID}}' }],
    }
    apiMock.importBrowserRecording.mockResolvedValueOnce({
      scripts: [], script: sourceScript, warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [], correlations: [],
      stats: { format: 'chrome_devtools_recorder', total_steps: 1, converted_actions: 1, skipped_steps: 0 }, summary: {},
      parameterization: {
        suggestions: [{
          id: 'order-id', kind: 'data_param', var_name: 'ORDER_ID', replacement: '{{param.ORDER_ID}}',
          literal_value: '123', confidence: 0.95, rule_id: 'path-id', rationale: 'Recorded order id',
          auto_apply: true, high_risk: false,
        }],
        summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
      },
    })
    apiMock.applyImportParameterization.mockResolvedValueOnce({
      script: parameterizedScript,
      diffs: [{ suggestion_id: 'order-id', path: 'actions[0].target', kind: 'data_param', before: '/orders/123', after: '/orders/{{param.ORDER_ID}}' }],
    })

    await act(async () => root.render(<ProtocolAssetImport kind="recorder-export" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], 'recorder.json')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert Recording'))?.click()
    })
    await flushPromises()

    const completeReplay = async () => {
      const run = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Run Replay Validation'))!
      await act(async () => run.click())
      await flushPromises()
      const complete = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Complete Replay'))!
      await act(async () => complete.click())
      expect(container.textContent).toContain('Replay validation passed')
    }

    await completeReplay()
    const thinkTimeInput = Array.from(container.querySelectorAll<HTMLLabelElement>('label'))
      .find(label => label.textContent?.includes('Think Time (ms)'))
      ?.querySelector<HTMLInputElement>('input')
    expect(thinkTimeInput).toBeDefined()
    await act(async () => {
      if (thinkTimeInput) setInputValue(thinkTimeInput, '2500')
    })
    expect(container.textContent).toContain('Runtime settings updated the draft')
    expect(container.textContent).not.toContain('Replay validation passed')

    await completeReplay()
    const applyParameterization = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Apply Selected'))!
    await act(async () => applyParameterization.click())
    await flushPromises()
    expect(apiMock.applyImportParameterization).toHaveBeenCalled()
    expect(container.textContent).toContain('Parameterization updated the draft')
    expect(container.textContent).not.toContain('Replay validation passed')
  })

  it('invalidates successful recorder replay evidence when deferred secret bindings change the draft', async () => {
    const sourceScript = {
      name: 'Recorder With Secret', version: '1.0', protocol: 'browser',
      config: { base_url: 'https://shop.example.test', auth: { token: '[REDACTED]' } },
      actions: [{ name: 'Navigate', type: 'browser_navigate', target: '/account' }],
    }
    apiMock.importBrowserRecording.mockResolvedValueOnce({
      scripts: [], script: sourceScript, warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [], correlations: [],
      stats: { format: 'chrome_devtools_recorder', total_steps: 1, converted_actions: 1, skipped_steps: 0 }, summary: {},
      secret_binding_requirements: [{
        id: 'recorder-token', path: '/config/auth/token', label: 'Recorder token', context: 'Browser authentication',
        suggested_name: 'RECORDER_TOKEN', script_index: 0, script_name: sourceScript.name,
      }],
    })
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: { ...sourceScript, config: { ...sourceScript.config, auth: { token: '{{secret.RECORDER_TOKEN}}' } } },
      secret_binding_requirements: [],
    })

    await act(async () => root.render(<ProtocolAssetImport kind="recorder-export" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], 'recorder.json')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert Recording'))?.click()
    })
    await flushPromises()

    const replay = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Run Replay Validation'))!
    await act(async () => replay.click())
    await flushPromises()
    const complete = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Complete Replay'))!
    await act(async () => complete.click())
    expect(container.textContent).toContain('Replay validation passed')

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save and Continue Importing with 1 secret to configure'))!
    await act(async () => save.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalled()
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Credential bindings updated the draft')
    expect(container.textContent).not.toContain('Replay validation passed')
    expect(container.textContent).toContain('Run replay validation successfully before saving')
  })

  it('saves one canonical recorder draft after selector repair and deferred credential binding', async () => {
    const sourceScript = {
      name: 'Recorder With Secret', version: '1.0', protocol: 'browser',
      config: { base_url: 'https://shop.example.test', auth: { token: '[REDACTED]' } },
      actions: [{
        name: 'Click Sign in', type: 'browser_click', selector: "[data-testid='login-btn']", target: "[data-testid='login-btn']",
        metadata: { source_selector_candidates: [
          { kind: 'css', value: "[data-testid='login-btn']", confidence: 95, reason: 'Original' },
          { kind: 'role', value: 'role=button[name="Sign in"]', confidence: 80, reason: 'Accessible fallback' },
        ], source_selector_confidence: 95 },
      }],
    }
    apiMock.importBrowserRecording.mockResolvedValueOnce({
      scripts: [], script: sourceScript, warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [], correlations: [],
      stats: { format: 'chrome_devtools_recorder', total_steps: 1, converted_actions: 1, skipped_steps: 0 }, summary: {},
      secret_binding_requirements: [{
        id: 'recorder-token', path: '/config/auth/token', label: 'Recorder token', context: 'Browser authentication',
        suggested_name: 'RECORDER_TOKEN', script_index: 0, script_name: sourceScript.name,
      }],
    })
    apiMock.applySecretBindings.mockImplementationOnce(async ({ script }: { script: typeof sourceScript }) => ({
      script: { ...script, config: { ...script.config, auth: { token: '{{secret.RECORDER_TOKEN}}' } } },
      secret_binding_requirements: [],
    }))

    await act(async () => root.render(<ProtocolAssetImport kind="recorder-export" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], 'recorder.json')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convert = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert Recording'))!
    await act(async () => convert.click())
    await flushPromises()

    const useFallback = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Use')!
    await act(async () => useFallback.click())
    const draftToggle = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.parentElement?.textContent?.includes('Save as draft needing replay review'))!
    await act(async () => draftToggle.click())
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save and Continue Importing with 1 secret to configure'))!
    await act(async () => save.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledWith(expect.objectContaining({
      script: expect.objectContaining({
        actions: [expect.objectContaining({ selector: 'role=button[name="Sign in"]' })],
      }),
    }))
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Credential bindings updated the draft')
    const resolvedDraftToggle = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.parentElement?.textContent?.includes('Save as draft needing replay review'))!
    await act(async () => resolvedDraftToggle.click())
    const resolvedSave = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save and Continue Importing'))!
    await act(async () => resolvedSave.click())
    await flushPromises()
    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      source_profile: 'recorder_export',
      folder_id: null,
      scripts: [expect.objectContaining({
        config: expect.objectContaining({ auth: { token: '{{secret.RECORDER_TOKEN}}' } }),
        actions: [expect.objectContaining({ selector: 'role=button[name="Sign in"]' })],
      })],
    }))
  })

  it('reuses a generic atomic save operation after an interrupted response', async () => {
    let rejectFirstSave!: (reason: unknown) => void
    apiMock.commitGenericImportBatch
      .mockReturnValueOnce(new Promise((_resolve, reject) => { rejectFirstSave = reject }))
      .mockImplementationOnce(async ({ scripts }: { scripts: Array<{ name: string; protocol: string; version: string }> }) => ({
        scripts: scripts.map(script => ({
          id: 'saved-k6-retry',
          name: script.name,
          protocol: script.protocol,
          version: script.version,
          created_at: '2026-01-01T00:00:00Z',
          script,
        })),
        count: scripts.length,
      }))

    await act(async () => root.render(<ProtocolAssetImport kind="k6" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['export default function() {}'], 'retry.js')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convert = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert k6 Script'))!
    await act(async () => convert.click())
    await flushPromises()
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save and Continue Importing'))!
    act(() => save.click())
    await flushPromises()
    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledTimes(1)
    const firstOperationID = apiMock.commitGenericImportBatch.mock.calls[0][0].operation_id

    await act(async () => {
      rejectFirstSave(new Error('response interrupted'))
      await Promise.resolve()
      await Promise.resolve()
    })
    const retry = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save and Continue Importing'))!
    await act(async () => retry.click())
    await flushPromises()

    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledTimes(2)
    expect(apiMock.commitGenericImportBatch.mock.calls[1][0].operation_id).toBe(firstOperationID)
    expect(apiMock.createScript).not.toHaveBeenCalled()
  })

  it('opens the repaired canonical recorder draft in the editor when no credentials are pending', async () => {
    const onEdit = vi.fn()
    await act(async () => root.render(<ProtocolAssetImport kind="recorder-export" projectId="project-1" onEdit={onEdit} />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['{}'], 'recorder.json')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convert = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert Recording'))!
    await act(async () => convert.click())
    await flushPromises()
    const useFallback = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Use')!
    await act(async () => useFallback.click())
    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Open Draft in Visual Editor'))!
    await act(async () => edit.click())

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      actions: expect.arrayContaining([expect.objectContaining({ selector: 'role=button[name="Sign in"]' })]),
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
    expect(container.textContent).toContain('1 review detail')
    expect(container.textContent).not.toContain('WSDL Operations')
    const reviewDetails = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === '1 review detail')!
    await act(async () => reviewDetails.click())
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('WSDL Operations')
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('GetAccount')
    expect(container.textContent).toContain('Source Comparison')
  })
  it('imports k6 scripts through the shared protocol asset flow', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="k6" projectId="project-1" />)
    })
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return
    const file = new File(['import http from "k6/http";'], 'script.js', { type: 'application/javascript' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert k6 Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importK6).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1' }))
    expect(container.textContent).toContain('Review Generated Script')
  })

  it('reviews and atomically saves all fixture-gated CloudTest Clip drafts with protected source sessions', async () => {
    const onImported = vi.fn()
    const clip = (name: string) => ({
      name,
      version: '1.0.0',
      protocol: 'http',
      project_id: 'project-1',
      generated_from: 'cloudtest',
      config: { base_url: '' },
      actions: [{ name: `${name} source review`, type: 'source_review', metadata: { execution_blocker: 'cloudtest_fixture_mapping_unproven' } }],
    })
    const scripts = [clip('Checkout'), clip('Browse')]
    apiMock.importCloudTest.mockResolvedValueOnce({
      scripts,
      script: scripts[0],
      import_session_ids: ['protected-cloudtest-source', ''],
      warnings: ['Fixture proof is required before execution.'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { clips: 2, compositions: 1, elements: 7, recognized_elements: 6, unknown_elements: 1 },
      summary: { fixture_gate: 'blocked' },
      scenario_drafts: [{
        name: 'Holiday sale',
        source_file: 'repository.xml',
        source_path: '/Repository[1]/Composition[1]',
        review_required: true,
        groups: [{ name: 'East', script_ref: 'Checkout', start_offset: '60s' }],
      }],
    })

    await act(async () => root.render(<ProtocolAssetImport kind="cloudtest" projectId="project-1" onImported={onImported} />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    const file = new File(['<Repository/>'], 'repository.xml', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const inspect = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect CloudTest Export'))!
    await act(async () => inspect.click())
    await flushPromises()

    expect(apiMock.importCloudTest).toHaveBeenCalledWith(file, { project_id: 'project-1' })
    expect(container.textContent).toContain('1 action(s) generated from CloudTest export elements.')
    expect(container.textContent).not.toContain('0 action(s) generated from CloudTest export elements.')
    expect(container.textContent).toContain('CloudTest Fixture-Gated Review')
    expect(container.textContent).toContain('2Clip drafts')
    expect(container.textContent).toContain('Holiday sale')

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save All 2 Clip Scripts Atomically'))!
    await act(async () => save.click())
    await flushPromises()

    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      source_profile: 'cloudtest',
      import_session_ids: ['protected-cloudtest-source', ''],
      scripts: expect.arrayContaining([
        expect.objectContaining({ name: 'Checkout' }),
        expect.objectContaining({ name: 'Browse' }),
      ]),
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: expect.arrayContaining([
        expect.objectContaining({ name: 'Checkout' }),
        expect.objectContaining({ name: 'Browse' }),
      ]),
    }))
  })

  it('does not publish or save stale credential bindings after a newer k6 conversion', async () => {
    const draft = (name: string, requirements: unknown[]) => ({
      scripts: [],
      script: { name, version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'Action', type: 'request', method: 'GET', target: '/' }] },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { converted_actions: 1 },
      summary: {},
      secret_binding_requirements: requirements,
    })
    const requirement = {
      id: 'auth', path: '/actions/0/headers/Authorization', label: 'Authorization', context: 'Bearer credential', suggested_name: 'API_TOKEN',
    }
    apiMock.importK6
      .mockResolvedValueOnce(draft('Generation A', [requirement]))
      .mockResolvedValueOnce(draft('Generation B', []))
    let resolveApply: ((value: unknown) => void) | undefined
    apiMock.applySecretBindings.mockReturnValueOnce(new Promise(resolve => { resolveApply = resolve }))

    await act(async () => root.render(<ProtocolAssetImport kind="k6" projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    const file = new File(['import http from "k6/http";'], 'script.js', { type: 'application/javascript' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convertButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert k6 Script'))!
    await act(async () => convertButton.click())
    await flushPromises()
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save and Continue Importing with 1 secret to configure'))!
    act(() => saveButton.click())
    await flushPromises()
    expect(apiMock.applySecretBindings).toHaveBeenCalledTimes(1)

    await act(async () => convertButton.click())
    await flushPromises()
    expect(container.textContent).toContain('Generation B')
    await act(async () => {
      resolveApply?.({
        script: { ...draft('Resolved A', []).script, config: { base_url: '', token: '{{secret.API_TOKEN}}' } },
        secret_binding_requirements: [],
      })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Generation B')
    expect(container.textContent).not.toContain('Resolved A')
    expect(apiMock.createScript).not.toHaveBeenCalled()
  })

  it('imports Event Hubs Capture files and hides the inert name/base-url inputs', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="eventhubs-capture" projectId="project-1" />)
    })
    await flushPromises()

    // L2: the Event Hubs Capture converter ignores name/base_url, so the inputs are not rendered.
    expect(container.textContent).not.toContain('Script Name')
    expect(container.textContent).not.toContain('Base URL Override')

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return
    const file = new File(['avro-bytes'], 'capture.avro', { type: 'application/octet-stream' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Capture'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importEventHubsCapture).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1' }))
  })

  it('imports broker configuration files and hides the inert base-url input', async () => {
    await act(async () => {
      root.render(<ProtocolAssetImport kind="broker-config" projectId="project-1" />)
    })
    await flushPromises()

    expect(container.textContent).not.toContain('Base URL Override')

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return
    const file = new File(['DEFINE QLOCAL(ORDERS)'], 'broker.mqsc', { type: 'text/plain' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Broker Config'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importBrokerConfig).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1' }))
  })

  it('re-converts a detected Salesforce HAR as a generic draft when Force Generic HAR is chosen', async () => {
    const salesforceResult = await apiMock.importSalesforce()
    apiMock.importHAR.mockResolvedValue(salesforceResult)

    await act(async () => {
      root.render(<ProtocolAssetImport kind="har" projectId="project-1" />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return
    const file = new File(['{"log":{"entries":[]}}'], 'salesforce.har', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert HAR'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    // Auto-detect path sends salesforce_native: true.
    expect(apiMock.importHAR).toHaveBeenLastCalledWith(file, expect.objectContaining({ salesforce_native: true }))

    const forceGenericButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Force Generic HAR'))
    expect(forceGenericButton).toBeDefined()
    if (!forceGenericButton) return
    await act(async () => {
      forceGenericButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    // Force Generic re-converts the same file with salesforce_native: false.
    expect(apiMock.importHAR).toHaveBeenLastCalledWith(file, expect.objectContaining({ salesforce_native: false }))
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}

async function openImportCoverage(container: HTMLElement) {
  const coverageButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Import Coverage')
  expect(coverageButton).toBeDefined()
  if (!coverageButton) return
  await act(async () => {
    coverageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

async function closeImportCoverage(container: HTMLElement) {
  const closeButton = container.querySelector('.import-coverage-modal-header button') as HTMLButtonElement | null
  if (!closeButton) return
  await act(async () => {
    closeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}
