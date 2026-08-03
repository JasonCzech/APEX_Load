const QUEUE_ATTEMPT_STORAGE_PREFIX = 'apexload.newTest.queueAttempt.v2.'

export interface NewTestQueueAttemptScope {
  projectId: string
  applicationId: string
}

export interface NewTestQueueAttempt extends NewTestQueueAttemptScope {
  version: 2
  requestedTestId: string
  payloadFingerprint: string
  createdAt: number
}

function queueAttemptStorage(): Storage | undefined {
  if (typeof window === 'undefined') return undefined
  try {
    // Queue recovery must survive a browser/WebView restart. The requested ID
    // is the idempotency anchor that prevents a lost create/start response from
    // turning into a duplicate run on the next launch.
    return window.localStorage
  } catch {
    return undefined
  }
}

function legacyQueueAttemptStorage(): Storage | undefined {
  if (typeof window === 'undefined') return undefined
  try {
    return window.sessionStorage
  } catch {
    return undefined
  }
}

function normalizedScope(scope: NewTestQueueAttemptScope): NewTestQueueAttemptScope {
  return {
    projectId: scope.projectId.trim(),
    applicationId: scope.applicationId.trim(),
  }
}

function storageKey(scope: NewTestQueueAttemptScope): string {
  const normalized = normalizedScope(scope)
  return `${QUEUE_ATTEMPT_STORAGE_PREFIX}${encodeURIComponent(normalized.projectId)}.${encodeURIComponent(normalized.applicationId)}`
}

function canonicalJSONValue(value: unknown): unknown {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return value
  if (typeof value === 'number') return Number.isFinite(value) ? value : null
  if (Array.isArray(value)) {
    return value.map(item => {
      const normalized = canonicalJSONValue(item)
      return normalized === undefined ? null : normalized
    })
  }
  if (typeof value !== 'object') return undefined

  const source = value as Record<string, unknown>
  const normalized: Record<string, unknown> = {}
  Object.keys(source).sort().forEach(key => {
    const item = canonicalJSONValue(source[key])
    if (item !== undefined) normalized[key] = item
  })
  return normalized
}

function fallbackFingerprint(value: string): string {
  let left = 0x811c9dc5
  let right = 0x9e3779b9
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    left = Math.imul(left ^ code, 0x01000193)
    right = Math.imul(right ^ code, 0x85ebca6b)
    right ^= right >>> 13
  }
  const part = (input: number) => (input >>> 0).toString(16).padStart(8, '0')
  return `fallback:${value.length.toString(16)}:${part(left)}${part(right)}`
}

export async function fingerprintNewTestQueuePayload(payload: unknown): Promise<string> {
  const canonical = JSON.stringify(canonicalJSONValue(payload))
  const subtle = globalThis.crypto?.subtle
  if (subtle && typeof TextEncoder !== 'undefined') {
    try {
      const digest = await subtle.digest('SHA-256', new TextEncoder().encode(canonical))
      return `sha256:${Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')}`
    } catch {
      // The deterministic fallback preserves retry behavior in restricted WebViews.
    }
  }
  return fallbackFingerprint(canonical)
}

function isQueueAttempt(value: unknown): value is NewTestQueueAttempt {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const candidate = value as Partial<NewTestQueueAttempt>
  return candidate.version === 2
    && typeof candidate.requestedTestId === 'string'
    && candidate.requestedTestId.length > 0
    && typeof candidate.payloadFingerprint === 'string'
    && candidate.payloadFingerprint.length > 0
    && typeof candidate.projectId === 'string'
    && candidate.projectId.length > 0
    && typeof candidate.applicationId === 'string'
    && candidate.applicationId.length > 0
    && typeof candidate.createdAt === 'number'
    && Number.isFinite(candidate.createdAt)
}

export function readNewTestQueueAttempt(scope: NewTestQueueAttemptScope): NewTestQueueAttempt | null {
  const normalized = normalizedScope(scope)
  const storage = queueAttemptStorage()
  if (!normalized.projectId || !normalized.applicationId) return null
  const key = storageKey(normalized)
  const candidates = [storage, legacyQueueAttemptStorage()].filter((candidate): candidate is Storage => Boolean(candidate))
  for (const candidate of candidates) {
    try {
      const parsed: unknown = JSON.parse(candidate.getItem(key) || '')
      if (
        !isQueueAttempt(parsed)
        || parsed.projectId !== normalized.projectId
        || parsed.applicationId !== normalized.applicationId
      ) {
        candidate.removeItem(key)
        continue
      }
      if (candidate !== storage && storage) {
        storage.setItem(key, JSON.stringify(parsed))
        candidate.removeItem(key)
      }
      return parsed
    } catch {
      candidate.removeItem(key)
    }
  }
  return null
}

export function persistNewTestQueueAttempt(
  payloadFingerprint: string,
  requestedTestId: string,
  scope: NewTestQueueAttemptScope,
  now = Date.now(),
): NewTestQueueAttempt {
  const normalized = normalizedScope(scope)
  const attempt: NewTestQueueAttempt = {
    version: 2,
    requestedTestId,
    payloadFingerprint,
    projectId: normalized.projectId,
    applicationId: normalized.applicationId,
    createdAt: now,
  }
  const storage = queueAttemptStorage()
  if (storage && normalized.projectId && normalized.applicationId) {
    try {
      storage.setItem(storageKey(normalized), JSON.stringify(attempt))
    } catch {
      // The caller retains the same attempt in memory when storage is unavailable.
    }
  }
  return attempt
}

export function clearNewTestQueueAttempt(attempt: Pick<NewTestQueueAttempt, 'projectId' | 'applicationId' | 'requestedTestId'>): void {
  if (!attempt.projectId || !attempt.applicationId || !attempt.requestedTestId) return
  const key = storageKey(attempt)
  const storages = [queueAttemptStorage(), legacyQueueAttemptStorage()].filter((candidate): candidate is Storage => Boolean(candidate))
  storages.forEach(storage => {
    try {
      const parsed: unknown = JSON.parse(storage.getItem(key) || '')
      if (isQueueAttempt(parsed) && parsed.requestedTestId === attempt.requestedTestId) {
        storage.removeItem(key)
      }
    } catch {
      storage.removeItem(key)
    }
  })
}

export function newTestQueueAttemptStorageKey(scope: NewTestQueueAttemptScope): string {
  return storageKey(scope)
}
