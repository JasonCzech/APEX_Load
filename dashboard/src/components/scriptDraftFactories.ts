import { type AuthoringComponent } from '../types'
import { type ScriptDefinition } from '../types/scripts'

function createBaseDraft(): ScriptDefinition {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'http',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
    },
    actions: [],
  }
}

export function createApiDraft(): ScriptDefinition {
  return createBaseDraft()
}

export function createSoapDraft(): ScriptDefinition {
  return {
    ...createBaseDraft(),
    protocol: 'soap',
    config: {
      ...createBaseDraft().config,
      soap: {
        version: '1.1',
        namespaces: {
          soapenv: 'http://schemas.xmlsoap.org/soap/envelope/',
        },
        fault_handling: {
          fail_on_fault: true,
        },
      },
    },
    actions: [
      {
        name: 'SOAP_Request',
        type: 'POST',
        method: 'POST',
        target: '',
        headers: {},
        body: '',
        extractors: [],
        assertions: [{ type: 'status_code', expected: 200 }],
        think_time_after: true,
        soap: {
          action: '',
          operation: '',
          body_mode: 'raw_xml',
        },
      },
    ],
  }
}

export function createJavaHttpDraft(): ScriptDefinition {
  return {
    ...createBaseDraft(),
    protocol: 'java_http',
    config: {
      ...createBaseDraft().config,
      java_http: {
        codec: 'java_serialized_object_base64',
        helper_mode: 'embedded',
        content_type: 'application/x-java-serialized-object',
        accept: 'application/x-java-serialized-object',
        body_encoding: 'base64',
        extractor_type: 'opaque',
        preserve_binary_result: true,
      },
    },
    actions: [
      {
        name: 'Java_HTTP_Request',
        type: 'POST',
        method: 'POST',
        target: '',
        headers: {},
        body: '',
        extractors: [],
        assertions: [{ type: 'status_code', expected: 200 }],
        think_time_after: true,
      },
    ],
  }
}

export function createWebDraft(): ScriptDefinition {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'browser',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
      browser: {
        engine: 'chromium',
        headless: true,
        client_metrics: true,
        capture_screenshots: 'per_action',
        download_resources: 'recorded',
        viewport: { width: 1440, height: 900 },
      },
    },
    actions: [],
  }
}

export function createJmsDraft(): ScriptDefinition {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'jms-solace',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
      jms: {
        provider: 'solace',
        broker_url: '',
        message_vpn: '',
        client_id: '',
        destination: '',
        destination_type: 'queue',
        reply_to: '',
        acknowledgement: 'auto',
        delivery_mode: 'persistent',
        timeout_ms: 5000,
        mock: true,
      },
    },
    actions: [],
  }
}

export function createIbmMqDraft(): ScriptDefinition {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'jms-ibmmq',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
      jms: {
        provider: 'ibmmq',
        broker_url: '',
        client_id: '',
        destination: '',
        destination_type: 'queue',
        reply_to: '',
        acknowledgement: 'auto',
        delivery_mode: 'persistent',
        timeout_ms: 5000,
        mock: true,
      },
    },
    actions: [],
  }
}

export function createServiceBusJmsDraft(): ScriptDefinition {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'jms-servicebus',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
      jms: {
        provider: 'servicebus',
        broker_url: '',
        client_id: '',
        destination: '',
        destination_type: 'queue',
        subscription: '',
        reply_to: '',
        acknowledgement: 'auto',
        delivery_mode: 'persistent',
        timeout_ms: 5000,
        mock: true,
      },
    },
    actions: [],
  }
}

export function createKafkaDraft(): ScriptDefinition {
  return {
    name: '',
    version: '1.0.0',
    protocol: 'kafka',
    config: {
      base_url: '',
      think_time: { type: 'fixed', mean_ms: 1000 },
      pacing: { type: 'none' },
      run_logic: { iterations: 0, auto_transactions: 'none' },
      error_handling: { continue_on_error: false },
      log: { level: 'standard' },
      kafka: {
        brokers: [],
        security_protocol: 'plaintext',
        topic: '',
        consumer_group: '',
        acks: 'all',
        compression: 'none',
        partition_strategy: 'key-hash',
        auto_offset_reset: 'latest',
        timeout_ms: 5000,
        mock: true,
      },
    },
    actions: [
      {
        name: 'Produce_Event',
        type: 'kafka_produce',
        target: 'events',
        headers: {},
        body: '{"id":"{{fn.uuid()}}"}',
        params: { key: '{{fn.uuid()}}' },
        extractors: [],
        assertions: [{ type: 'offset_advanced' }],
        think_time_after: true,
      },
      {
        name: 'Consume_Event',
        type: 'kafka_consume',
        target: 'events',
        headers: {},
        body: '',
        params: { timeout_ms: 5000 },
        extractors: [],
        assertions: [{ type: 'message_received' }],
        think_time_after: true,
      },
    ],
  }
}

export function createWebSocketDraft(): ScriptDefinition {
  return {
    ...createBaseDraft(),
    protocol: 'websocket',
    config: {
      ...createBaseDraft().config,
      websocket: {
        read_timeout_ms: 30000,
        write_timeout_ms: 10000,
        max_message_size: 1048576,
        subprotocol: '',
        origin: '',
        compression: false,
        reconnect: true,
      },
    },
    actions: [
      {
        name: 'Connect_WebSocket',
        type: 'ws_connect',
        target: '/ws',
        headers: {},
        body: '',
        params: { timeout_ms: 30000 },
        extractors: [],
        assertions: [],
        think_time_after: true,
      },
    ],
  }
}

export function createGrpcDraft(): ScriptDefinition {
  return {
    ...createBaseDraft(),
    protocol: 'grpc',
    config: {
      ...createBaseDraft().config,
      grpc: {
        use_reflection: true,
        plain_text: false,
        timeout_ms: 30000,
        max_recv_msg_size: 4194304,
        metadata: {},
        authority: '',
      },
    },
    actions: [
      {
        name: 'Unary_Call',
        type: 'grpc_unary',
        target: 'package.Service/Method',
        headers: {},
        body: '{}',
        params: { timeout_ms: 30000 },
        extractors: [],
        assertions: [{ type: 'grpc_status', expected: 0 }],
        think_time_after: true,
      },
    ],
  }
}

export function createDatabaseDraft(): ScriptDefinition {
  return {
    ...createBaseDraft(),
    protocol: 'database',
    config: {
      ...createBaseDraft().config,
      database: {
        driver: 'sqlite',
        dsn: ':memory:',
        query_timeout_ms: 30000,
        max_open_conns: 1,
        max_rows: 100,
        read_only: true,
        rollback_on_teardown: true,
      },
    },
    actions: [
      {
        name: 'Query_Database',
        type: 'db_query',
        target: 'select 1 as ok',
        headers: {},
        body: '',
        params: { binds: [] },
        extractors: [{ name: 'ok', source: 'scalar', type: 'jsonpath', expression: '$.scalar' }],
        assertions: [{ type: 'row_count', expected: 1 }],
        think_time_after: true,
      },
    ],
  }
}

export function createNetworkDraft(): ScriptDefinition {
  return {
    ...createBaseDraft(),
    protocol: 'network',
    config: {
      ...createBaseDraft().config,
      network: {
        subprotocol: 'tcp',
        host: 'localhost',
        port: 80,
        timeout_ms: 30000,
        tls: false,
      },
    },
    actions: [
      {
        name: 'Network_Health',
        type: 'network_health',
        target: 'localhost:80',
        headers: {},
        body: '',
        params: {},
        extractors: [],
        assertions: [],
        think_time_after: true,
      },
    ],
  }
}

function baseDraftForComponent(component: AuthoringComponent): ScriptDefinition {
  for (const protocol of component.protocols) {
    const normalized = protocol.toLowerCase()
    if (normalized === 'soap') return createSoapDraft()
    if (normalized === 'java_http') return createJavaHttpDraft()
    if (normalized === 'websocket') return createWebSocketDraft()
    if (normalized === 'grpc') return createGrpcDraft()
    if (normalized === 'database') return createDatabaseDraft()
    if (normalized === 'network') return createNetworkDraft()
    if (normalized === 'kafka') return createKafkaDraft()
    if (normalized === 'browser' || normalized === 'trueclient_mobile_web' || normalized === 'trueclient2_web') {
      return createWebDraft()
    }
    if (normalized.startsWith('jms')) {
      if (normalized === 'jms-ibmmq') return createIbmMqDraft()
      if (normalized === 'jms-servicebus') return createServiceBusJmsDraft()
      return createJmsDraft()
    }
  }
  return createApiDraft()
}

// composeComponentDraft seeds a new draft script from a Component Library entry.
// The dashboard ScriptDefinition has no init/teardown phases, so the component's
// init, actions, and teardown fragments are folded into a single actions array in
// that order, keeping everything visible and editable in the draft editor.
export function composeComponentDraft(component: AuthoringComponent): ScriptDefinition {
  const base = baseDraftForComponent(component)
  const draft: ScriptDefinition = {
    ...base,
    name: component.name,
    config: { ...base.config, ...(component.config ?? {}) } as ScriptDefinition['config'],
    actions: [
      ...(component.init ?? []),
      ...(component.actions ?? []),
      ...(component.teardown ?? []),
    ],
  }
  if (component.data_sources?.length) {
    draft.data_sources = component.data_sources
  }
  if (component.parameters?.length) {
    draft.parameters = component.parameters
  }
  return draft
}
