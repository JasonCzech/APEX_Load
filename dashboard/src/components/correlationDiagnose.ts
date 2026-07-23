import type { CorrelationStep, ScriptReplayActionEvent } from '../types/scripts'

// Build the recorded/replay observation lists for a record-vs-replay correlation
// diagnosis. The replay side comes from a finished replay's per-event snapshots;
// the recorded side comes either from a stored server-side baseline (preferred)
// or, as a fallback, a HAR the user uploads.

/**
 * Convert replay events into correlation steps, keyed by DSL action index so they
 * pair with the recorded baseline (whose observation IDs are also action indices).
 * Exported for tests.
 */
export function replayEventsToSteps(events: ScriptReplayActionEvent[] | undefined): CorrelationStep[] {
  if (!events) return []
  const steps: CorrelationStep[] = []
  for (const event of events) {
    if (event.phase && event.phase !== 'action') continue
    if (typeof event.action_index !== 'number') continue
    const snapshot = event.snapshot
    steps.push({
      id: event.action_index,
      request_url: snapshot?.request_url || event.target || undefined,
      request_body: snapshot?.request_body ?? event.body ?? undefined,
      request_headers: snapshot?.request_headers ?? event.headers ?? undefined,
      response_body: snapshot?.response_body ?? undefined,
      response_headers: snapshot?.response_headers ?? undefined,
      response_status: snapshot?.response_status ?? event.status_code ?? undefined,
      response_success: event.success,
      parent_action_name: event.parent_action_name,
      request_index: event.request_index,
      step_path: event.step_path ? [...event.step_path] : undefined,
    })
  }
  return steps
}

/**
 * A replay can emit several leaf events for one DSL action (sequences, loops,
 * retries). The current diagnosis transport keys observations by action index,
 * so sending those duplicates would make both pairing and repair targeting
 * ambiguous. Callers must fail closed until the transport carries a unique DSL
 * leaf identity.
 */
export function duplicateCorrelationStepIds(steps: CorrelationStep[]): number[] {
  const seen = new Set<number>()
  const duplicates = new Set<number>()
  for (const step of steps) {
    if (seen.has(step.id)) duplicates.add(step.id)
    seen.add(step.id)
  }
  return [...duplicates].sort((left, right) => left - right)
}

export function nestedCorrelationStepIds(steps: CorrelationStep[]): number[] {
  return [...new Set(steps
    .filter(step => !!step.parent_action_name || typeof step.request_index === 'number' || (step.step_path?.length ?? 0) > 0)
    .map(step => step.id))]
    .sort((left, right) => left - right)
}

interface HarHeader { name?: string; value?: string }
interface HarEntry {
  _resourceType?: string
  request?: { method?: string; url?: string; postData?: { text?: string; encoding?: string }; headers?: HarHeader[] }
  response?: { status?: number; content?: { text?: string; mimeType?: string; encoding?: string }; headers?: HarHeader[] }
}

export interface HarToStepsLimits {
  maxEntries?: number
  maxDecodedChars?: number
}

export class HarToStepsLimitError extends Error {
  readonly kind: 'entries' | 'decoded_content'

  constructor(kind: 'entries' | 'decoded_content', message: string) {
    super(message)
    this.name = 'HarToStepsLimitError'
    this.kind = kind
  }
}

const STATIC_RESOURCE_TYPES = new Set(['image', 'stylesheet', 'font', 'media', 'script', 'manifest'])
const STATIC_MIME_PREFIXES = ['image/', 'font/', 'text/css', 'audio/', 'video/', 'application/javascript', 'text/javascript']
const STATIC_URL_EXTENSIONS = ['.css', '.js', '.mjs', '.map', '.png', '.jpg', '.jpeg', '.gif', '.webp', '.svg', '.ico', '.woff', '.woff2', '.ttf', '.eot', '.otf', '.mp4', '.webm', '.mp3', '.wav']

function headerArrayToMap(headers?: HarHeader[]): Record<string, string> | undefined {
  if (!Array.isArray(headers) || headers.length === 0) return undefined
  const map: Record<string, string> = {}
  const canonicalNames = new Map<string, string>()
  for (const header of headers) {
    if (!header.name) continue
    const normalized = header.name.toLowerCase()
    const existingName = canonicalNames.get(normalized)
    if (!existingName) {
      canonicalNames.set(normalized, header.name)
      map[header.name] = header.value ?? ''
      continue
    }
    // Correlation detection treats each newline-delimited value as one header
    // occurrence. Comma-folding is lossy (and invalid for Set-Cookie), while a
    // newline keeps every repeated occurrence independently diagnosable.
    map[existingName] += '\n' + (header.value ?? '')
  }
  return map
}

function isStaticAsset(entry: HarEntry): boolean {
  const method = (entry.request?.method || '').trim().toUpperCase()
  if (method && method !== 'GET' && method !== 'HEAD') return false
  const resourceType = (entry._resourceType || '').toLowerCase()
  if (resourceType && STATIC_RESOURCE_TYPES.has(resourceType)) return true
  const mime = (entry.response?.content?.mimeType || '').toLowerCase().split(';')[0].trim()
  if (STATIC_MIME_PREFIXES.some(prefix => mime.startsWith(prefix))) return true
  let path = entry.request?.url || ''
  try {
    const url = new URL(path, 'https://example.invalid')
    path = url.pathname
  } catch {
    // Keep the raw path for suffix checks.
  }
  const lowerPath = path.toLowerCase()
  return STATIC_URL_EXTENSIONS.some(ext => lowerPath.endsWith(ext))
}

function decodeHARText(text?: string, encoding?: string): string | undefined {
  if (text === undefined) return undefined
  if ((encoding || '').trim().toLowerCase() !== 'base64') return text
  try {
    const compact = text.replace(/\s+/g, '')
    const binary = atob(compact)
    const bytes = Uint8Array.from(binary, char => char.charCodeAt(0))
    return new TextDecoder().decode(bytes)
  } catch {
    return text
  }
}

/**
 * Parse a HAR document into correlation steps (the recorded side of the
 * fallback path), filtering out static assets so the sequence aligns with a
 * script's meaningful actions. Exported for tests.
 */
export function harToSteps(har: unknown, limits: HarToStepsLimits = {}): CorrelationStep[] {
  const log = (har as { log?: { entries?: unknown } } | null)?.log
  const entries = Array.isArray(log?.entries) ? (log!.entries as HarEntry[]) : []
  if (typeof limits.maxEntries === 'number' && entries.length > limits.maxEntries) {
    throw new HarToStepsLimitError('entries', `HAR contains more than ${limits.maxEntries} entries`)
  }
  const steps: CorrelationStep[] = []
  let decodedChars = 0
  const chargeDecodedContent = (value: string | undefined) => {
    if (value === undefined) return value
    decodedChars += value.length
    if (typeof limits.maxDecodedChars === 'number' && decodedChars > limits.maxDecodedChars) {
      throw new HarToStepsLimitError('decoded_content', `HAR decoded content exceeds ${limits.maxDecodedChars} characters`)
    }
    return value
  }
  const chargeHeaders = (headers?: HarHeader[]) => {
    if (!headers) return
    for (const header of headers) {
      chargeDecodedContent(header.name)
      chargeDecodedContent(header.value)
    }
  }
  let id = 0
  for (const entry of entries) {
    if (isStaticAsset(entry)) continue
    const requestBody = chargeDecodedContent(decodeHARText(entry.request?.postData?.text, entry.request?.postData?.encoding))
    const responseBody = chargeDecodedContent(decodeHARText(entry.response?.content?.text, entry.response?.content?.encoding))
    chargeDecodedContent(entry.request?.url)
    chargeHeaders(entry.request?.headers)
    chargeHeaders(entry.response?.headers)
    steps.push({
      id: id++,
      request_url: entry.request?.url,
      request_body: requestBody,
      request_headers: headerArrayToMap(entry.request?.headers),
      response_body: responseBody,
      response_headers: headerArrayToMap(entry.response?.headers),
      response_status: entry.response?.status,
    })
  }
  return steps
}
