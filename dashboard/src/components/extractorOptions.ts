// The choices an extractor or a text check offers per protocol: where it
// reads from and how. The DSL validator enforces the same lists.

export function extractorSourceOptions(protocol: string): Array<[string, string]> {
  switch (protocol) {
    case 'jms-solace':
    case 'jms-servicebus':
    case 'jms-ibmmq':
      return [['body', 'Body'], ['header', 'Header'], ['property', 'Property'], ['message_id', 'Message ID'], ['correlation_id', 'Correlation ID']]
    case 'database':
      return [['body', 'Result Body'], ['rows', 'Rows'], ['scalar', 'Scalar'], ['row_count', 'Row Count'], ['rows_affected', 'Rows Affected'], ['last_insert_id', 'Last Insert ID']]
    case 'network':
      return [['body', 'Body'], ['response', 'Response'], ['records', 'Records'], ['record_count', 'Record Count'], ['entry_count', 'Entry Count'], ['target', 'Target']]
    case 'grpc':
      return [['body', 'Message Body'], ['metadata', 'Metadata'], ['trailer', 'Trailer'], ['status', 'Status']]
    case 'websocket':
      return [['body', 'Message Body'], ['header', 'Handshake Header'], ['message', 'Message'], ['close_code', 'Close Code']]
    default:
      return [['body', 'Body'], ['header', 'Header'], ['all', 'Headers and body'], ['cookies', 'Set-Cookie lines'], ['url', 'Final URL'], ['status', 'Status code']]
  }
}

export function extractorTypeOptions(protocol: string, source = ''): Array<[string, string]> {
  if (HTTP_EXTRACTOR_TEXT_SOURCES[source] && !isNonHTTPExtractorProtocol(protocol)) return HTTP_EXTRACTOR_TEXT_SOURCES[source]
  if (protocol === 'database' || protocol === 'network' || protocol === 'grpc') {
    return [['jsonpath', 'JSONPath'], ['jmespath', 'JMESPath'], ['regex', 'Regex'], ['boundary', 'Boundary']]
  }
  if (protocol.startsWith('jms') || protocol === 'websocket') {
    return [['jsonpath', 'JSONPath'], ['regex', 'Regex'], ['boundary', 'Boundary']]
  }
  // HTTP family: include SSE modes for consuming text/event-stream responses
  // (Agentforce streaming) — sse_concat joins data: frames, sse_event picks a named event.
  return [['jsonpath', 'JSONPath'], ['jmespath', 'JMESPath'], ['regex', 'Regex'], ['boundary', 'Boundary'], ['regex_count', 'Regex count'], ['regex_ordinal', 'Regex ordinal'], ['xpath', 'XPath'], ['css', 'CSS'], ['sse_concat', 'SSE concat'], ['sse_event', 'SSE event']]
}

// displayedExtractorType shows a stored type under the option its runtime
// spelling shares: json_path is jsonpath, and a header extractor's "header"
// type is the named-header lookup ("").
export function displayedExtractorType(source: string | undefined, type: string | undefined): string {
  if (type === 'json_path') return 'jsonpath'
  if (type === 'header' && source === 'header') return ''
  return type ?? ''
}

// HTTP extractor sources that are searched as text accept only the text
// extractor types (the DSL validator enforces the same lists).
const HTTP_TEXT_TYPES: Array<[string, string]> = [['regex', 'Regex'], ['boundary', 'Boundary'], ['regex_count', 'Regex count'], ['regex_ordinal', 'Regex ordinal']]
const HTTP_EXTRACTOR_TEXT_SOURCES: Record<string, Array<[string, string]>> = {
  header: [['', 'Named header'], ...HTTP_TEXT_TYPES],
  all: HTTP_TEXT_TYPES,
  cookies: HTTP_TEXT_TYPES,
  url: [['', 'Whole value'], ...HTTP_TEXT_TYPES],
  status: [['', 'Whole value'], ...HTTP_TEXT_TYPES],
}

function isNonHTTPExtractorProtocol(protocol: string) {
  return protocol === 'database' || protocol === 'network' || protocol === 'grpc' || protocol === 'websocket' || protocol.startsWith('jms')
}

// withCurrentOption keeps a stored value visible when it is not one of the
// offered options, instead of the select silently showing the first option.
export function withCurrentOption(options: Array<[string, string]>, current: string | undefined): Array<[string, string]> {
  const value = current ?? ''
  if (options.some(([option]) => option === value)) return options
  if (value === '' && options.some(([option]) => option === 'body')) return options
  return [...options, [value, value ? `Unsupported: ${value}` : 'Not set']]
}

export function extractorExpressionPlaceholder(protocol: string, source: string) {
  if (protocol === 'database') return source === 'scalar' ? '$.scalar' : '$.rows[0].id'
  if (protocol === 'network') return source === 'record_count' ? '$.record_count' : '$.response'
  if (protocol === 'grpc') return '$.id'
  if (protocol === 'websocket') return '$.type'
  if (protocol.startsWith('jms')) return '$.orderId'
  if (source === 'header') return 'Location'
  if (source === 'cookies') return 'sid=|||;'
  if (source === 'url') return 'code=([^&]+)'
  if (source === 'status') return 'Leave empty for the status code'
  return '$.data.id'
}

/** Where an HTTP text check looks. */
export const TEXT_ASSERTION_SOURCES: Array<[string, string]> = [['', 'Body'], ['headers', 'Headers'], ['all', 'Headers and body']]
