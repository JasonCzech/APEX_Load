import { fingerprintContent } from '../utils/contentFingerprint'
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

export const fingerprintNewTestQueuePayload = fingerprintContent

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
