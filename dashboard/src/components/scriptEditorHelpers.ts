// Small pure helpers used by the Script editor. Extracted from ScriptEditor.tsx
// to keep the component file focused on rendering and state. These functions
// have no React or component-state dependencies.

import type { JMSConfig } from '../types/scripts'

export function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

export function sanitizeDownloadName(value: string | undefined, fallback: string) {
  const normalized = (value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return normalized || fallback
}

export type JmsProvider = 'solace' | 'servicebus' | 'ibmmq'
export type JmsProtocol = 'jms-solace' | 'jms-servicebus' | 'jms-ibmmq'

export function jmsProviderForProtocol(protocol: string): JmsProvider | null {
  switch (protocol.trim().toLowerCase()) {
    case 'jms-solace':
      return 'solace'
    case 'jms-servicebus':
      return 'servicebus'
    case 'jms-ibmmq':
      return 'ibmmq'
    default:
      return null
  }
}

export function jmsProtocolForProvider(provider: string): JmsProtocol | null {
  switch (provider.trim().toLowerCase()) {
    case 'solace':
      return 'jms-solace'
    case 'servicebus':
      return 'jms-servicebus'
    case 'ibmmq':
      return 'jms-ibmmq'
    default:
      return null
  }
}

export function jmsConfigForProtocol(protocol: string, current: JMSConfig = {}): JMSConfig | null {
  const provider = jmsProviderForProtocol(protocol)
  if (!provider) return null
  const next: JMSConfig = {
    ...current,
    // Protocol and provider are one execution contract. A provider retained
    // from a previously selected JMS flavor could dispatch to the wrong broker.
    provider,
    destination_type: current.destination_type || 'queue',
    acknowledgement: current.acknowledgement || 'auto',
    delivery_mode: current.delivery_mode || 'persistent',
    timeout_ms: current.timeout_ms || 5000,
    mock: current.mock ?? true,
  }
  if (provider !== 'solace') delete next.message_vpn
  return next
}

export function defaultExtractorForProtocol(protocol: string) {
  switch (protocol) {
    case 'database':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.rows[0].id' }
    case 'network':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.response' }
    case 'grpc':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.id' }
    case 'jms-ibmmq':
    case 'jms-servicebus':
    case 'jms-solace':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.id' }
    case 'websocket':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.id' }
    default:
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.data.id' }
  }
}

export function defaultAssertionForProtocol(protocol: string) {
  switch (protocol) {
    case 'browser':
      return { type: 'element_text_contains', value: '', expected: '' }
    case 'database':
      return { type: 'row_count', expected: 1 }
    case 'network':
      return { type: 'body_contains', value: '' }
    case 'grpc':
      return { type: 'grpc_status', expected: 0 }
    case 'jms-ibmmq':
    case 'jms-servicebus':
    case 'jms-solace':
      return { type: 'message_received' }
    case 'websocket':
      return { type: 'message_received' }
    default:
      return { type: 'status_code', expected: 200 }
  }
}
