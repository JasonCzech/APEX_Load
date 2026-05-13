export type SampleFormat = 'raw_http' | 'curl' | 'rest_json' | 'har'

export const RAW_HTTP_REQUEST_DELIMITER = '\n\n### STORMRUNNER REQUEST ###\n\n'
export const RAW_HTTP_RESPONSE_DELIMITER = '\n\n### STORMRUNNER RESPONSE ###\n\n'

export interface LoadedSample {
  name: string
  text: string
}

export interface MergeResult {
  content: string
  mergedCount: number
}

function isObjectRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

export function sampleAcceptForFormat(format: SampleFormat) {
  switch (format) {
    case 'har':
      return '.har,.json'
    case 'rest_json':
      return '.json,.txt'
    case 'curl':
      return '.sh,.curl,.txt'
    case 'raw_http':
      return '.http,.txt,.req,.request'
    default:
      return '.txt,.json'
  }
}

export function supportsMultiPrimary(format: SampleFormat) {
  return format === 'raw_http' || format === 'rest_json' || format === 'har'
}

export function supportsMultiSecondary(format: SampleFormat) {
  return format === 'raw_http'
}

export function formatFileLabel(names: string[]) {
  if (names.length === 0) return ''
  if (names.length === 1) return names[0]
  return `${names.length} files loaded`
}

export function mergePrimarySamples(format: SampleFormat, loaded: LoadedSample[]): MergeResult {
  if (loaded.length === 0) {
    return { content: '', mergedCount: 0 }
  }
  if (format === 'raw_http') {
    return {
      content: loaded.map(item => item.text).join(RAW_HTTP_REQUEST_DELIMITER),
      mergedCount: loaded.length,
    }
  }
  if (format === 'rest_json') {
    const requests = loaded.flatMap(item => normalizeRestRequests(JSON.parse(item.text)))
    return {
      content: JSON.stringify({ requests }, null, 2),
      mergedCount: requests.length,
    }
  }
  if (format === 'har') {
    const entries = loaded.flatMap(item => normalizeHarEntries(JSON.parse(item.text)))
    return {
      content: JSON.stringify({ log: { entries } }, null, 2),
      mergedCount: entries.length,
    }
  }
  return { content: loaded[0].text, mergedCount: 1 }
}

export function mergeSecondarySamples(format: SampleFormat, loaded: LoadedSample[]): MergeResult {
  if (loaded.length === 0) {
    return { content: '', mergedCount: 0 }
  }
  if (format === 'raw_http') {
    return {
      content: loaded.map(item => item.text).join(RAW_HTTP_RESPONSE_DELIMITER),
      mergedCount: loaded.length,
    }
  }
  return { content: loaded[0].text, mergedCount: 1 }
}

export function describeMergedFiles(format: SampleFormat, target: 'primary' | 'secondary', fileNames: string[], mergedCount: number) {
  if (fileNames.length === 0) {
    return ''
  }
  if (target === 'primary') {
    if (format === 'rest_json') {
      return `${fileNames.length} file(s) merged into ${mergedCount} request sample(s)`
    }
    if (format === 'har') {
      return `${fileNames.length} file(s) merged into ${mergedCount} HAR entr${mergedCount === 1 ? 'y' : 'ies'}`
    }
    if (format === 'raw_http' && fileNames.length > 1) {
      return `${fileNames.length} request files will be paired by order` 
    }
  }
  if (target === 'secondary' && format === 'raw_http' && fileNames.length > 1) {
    return `${fileNames.length} response files will be paired by order`
  }
  return fileNames.length === 1 ? fileNames[0] : `${fileNames.length} files loaded`
}

function normalizeRestRequests(payload: unknown): unknown[] {
  if (Array.isArray(payload)) {
    return payload
  }
  if (isObjectRecord(payload) && Array.isArray(payload.requests)) {
    return payload.requests
  }
  return [payload]
}

function normalizeHarEntries(payload: unknown): unknown[] {
  if (isObjectRecord(payload)) {
    const log = payload.log
    if (isObjectRecord(log) && Array.isArray(log.entries)) {
      return log.entries
    }
    if (Array.isArray(payload.entries)) {
      return payload.entries
    }
    if ('request' in payload) {
      return [payload]
    }
  }
  return []
}