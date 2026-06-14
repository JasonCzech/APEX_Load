// Small pure helpers used by the Script editor. Extracted from ScriptEditor.tsx
// to keep the component file focused on rendering and state. These functions
// have no React or component-state dependencies.

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

export function defaultExtractorForProtocol(protocol: string) {
  switch (protocol) {
    case 'database':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.rows[0].id' }
    case 'network':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.response' }
    case 'grpc':
      return { name: '', source: 'body', type: 'jsonpath', expression: '$.id' }
    case 'jms-ibmmq':
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
    case 'database':
      return { type: 'row_count', expected: 1 }
    case 'network':
      return { type: 'body_contains', value: '' }
    case 'grpc':
      return { type: 'grpc_status', expected: 0 }
    case 'jms-ibmmq':
    case 'jms-solace':
      return { type: 'message_received' }
    case 'websocket':
      return { type: 'message_received' }
    default:
      return { type: 'status_code', expected: 200 }
  }
}
