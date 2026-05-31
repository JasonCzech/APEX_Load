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

function baseDraftForComponent(component: AuthoringComponent): ScriptDefinition {
  for (const protocol of component.protocols) {
    const normalized = protocol.toLowerCase()
    if (normalized === 'soap') return createSoapDraft()
    if (normalized === 'java_http') return createJavaHttpDraft()
    if (normalized === 'browser' || normalized === 'trueclient_mobile_web' || normalized === 'trueclient2_web') {
      return createWebDraft()
    }
    if (normalized.startsWith('jms')) return createJmsDraft()
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