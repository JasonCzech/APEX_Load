// Draft-owned intent keys for ordinary script and scenario creates (AUTH-1).
//
// One intent is one decision to create one object. Its key is sent as the
// create's Idempotency-Key and kept across a lost response, a retry and a
// browser reload, so the controller replays the object the first attempt
// created instead of making another. The key is rotated only by an explicit
// new intent or, after a definitive refusal, by a changed payload: while an
// earlier attempt may have committed, a changed payload is sent with the same
// key and the controller refuses it rather than create a second object.

import { ApiError } from '../hooks/apiClient'
import { draftFingerprint } from './scriptAuthoringSession'

export type AuthoringCreatePurpose = 'script_create' | 'scenario_create' | 'test_create'

export interface AuthoringCreateIntent {
  version: 1
  key: string
  purpose: AuthoringCreatePurpose
  projectId: string
  // payloadDigest identifies the resolved payload last sent with the key;
  // sentDigests, every payload sent with it (bounded), any of which an
  // earlier attempt may have created.
  payloadDigest: string
  sentDigests?: string[]
  // pending: sent, and no answer has proved that nothing was created with
  // it; the next save reuses the key.
  // refused: every send of the key was definitively refused before anything
  // could be created.
  state: 'pending' | 'refused'
  // uncertain: some send of the key ended without a definitive answer (a
  // lost response, a timeout, a server failure). A later refusal answers
  // only its own request, so the key then stays pending for good.
  uncertain?: boolean
  // issuedAt: when the controller's receipt for the key was first claimed;
  // its 24-hour replay lifetime runs from then.
  issuedAt: string
}

// A version 2 receipt replays the original outcome; version 1 the resource as
// it is now. The client asks for 2 and falls back to 1 only when a controller
// explicitly says it cannot record original outcomes.
export const AUTHORING_RECEIPT_VERSION_HEADER = 'X-APEX-Authoring-Receipt-Version'

// The controller guarantees a replay for 24 hours.
const INTENT_LIFETIME_MS = 24 * 60 * 60 * 1000
const LEDGER_KEY = 'apexload.authoring.create-intents.v1'
const LEDGER_LIMIT = 32
const SENT_DIGEST_LIMIT = 8
// A recorded key outlives its replay lifetime by a week, so reopening the
// same work then is answered as expired (an explicit decision), never as new.
const LEDGER_RETENTION_MS = INTENT_LIFETIME_MS + 7 * 24 * 60 * 60 * 1000

export function authoringPayloadDigest(payload: unknown): string {
  return draftFingerprint(JSON.stringify(payload ?? null))
}

function newIntentKey(purpose: AuthoringCreatePurpose): string {
  const random = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`
  return `${purpose.replace('_', '-')}-${random}`
}

export function isAuthoringCreateIntent(value: unknown): value is AuthoringCreateIntent {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const intent = value as Record<string, unknown>
  return intent.version === 1
    && typeof intent.key === 'string' && /^[\x21-\x7e]{16,512}$/.test(intent.key)
    && (intent.purpose === 'script_create' || intent.purpose === 'scenario_create' || intent.purpose === 'test_create')
    && typeof intent.projectId === 'string' && typeof intent.payloadDigest === 'string'
    && (intent.state === 'pending' || intent.state === 'refused')
    && (intent.uncertain === undefined || typeof intent.uncertain === 'boolean')
    && (intent.sentDigests === undefined || (Array.isArray(intent.sentDigests) && intent.sentDigests.length <= SENT_DIGEST_LIMIT && intent.sentDigests.every(digest => typeof digest === 'string')))
    && typeof intent.issuedAt === 'string' && Number.isFinite(Date.parse(intent.issuedAt))
}

function expired(intent: AuthoringCreateIntent, now: number): boolean {
  return now - Date.parse(intent.issuedAt) >= INTENT_LIFETIME_MS
}

/**
 * The intent the next create sends, recording the payload it sends. A pending
 * intent for the same purpose and project is always reused: its earlier
 * attempt may have committed, and a changed payload must then be refused
 * under the same key, never created again under a new one (an expired one is
 * answered as expired, never as new). A refused intent created nothing, so it
 * is reused for the same payload (with a fresh replay lifetime, since its
 * receipt was released) and rotated for a changed one.
 */
export function nextAuthoringCreateIntent(
  current: AuthoringCreateIntent | null | undefined,
  purpose: AuthoringCreatePurpose,
  projectId: string,
  payloadDigest: string,
  now = Date.now(),
): AuthoringCreateIntent {
  if (current && current.purpose === purpose && current.projectId === projectId) {
    if (current.state === 'pending') {
      // The first payload stays pinned: it is the one most likely to have
      // committed. Later ones rotate.
      const [first, ...later] = current.sentDigests ?? [current.payloadDigest]
      const rest = [...later.filter(digest => digest !== payloadDigest && digest !== first), ...(payloadDigest === first ? [] : [payloadDigest])]
      return { ...current, payloadDigest, sentDigests: [first, ...rest.slice(-(SENT_DIGEST_LIMIT - 1))] }
    }
    if (current.payloadDigest === payloadDigest && !expired(current, now)) return { ...current, state: 'pending', issuedAt: new Date(now).toISOString() }
  }
  return { version: 1, key: newIntentKey(purpose), purpose, projectId, payloadDigest, state: 'pending', issuedAt: new Date(now).toISOString() }
}

/** Why a create needs an explicit new intent before it can be saved again. */
export type AuthoringCreateDecisionReason = 'changed' | 'expired' | 'deleted'

/**
 * AuthoringCreateNeedsNewIntent: the controller will not create with this
 * draft's key. The draft is kept; only an explicit "save as new" may create
 * another object.
 */
export class AuthoringCreateNeedsNewIntent extends Error {
  readonly reason: AuthoringCreateDecisionReason
  constructor(reason: AuthoringCreateDecisionReason, noun: string) {
    super(reason === 'changed'
      ? `An earlier save of this draft may already have created the ${noun}, and the draft has changed since. Your draft is kept here. Check the library; to create another ${noun}, choose Save as new.`
      : reason === 'expired'
        ? `This draft's earlier save can no longer be confirmed (more than 24 hours old). Your draft is kept here. Check the library; to create the ${noun} now, choose Save as new.`
        : `The ${noun} this draft created earlier was deleted and will not be recreated. Your draft is kept here; to create it again, choose Save as new.`)
    this.name = 'AuthoringCreateNeedsNewIntent'
    this.reason = reason
  }
}

/**
 * What a failed create leaves behind. Throws AuthoringCreateNeedsNewIntent
 * when the key can never create the object; otherwise returns the intent to
 * keep: pending while some attempt may have committed (network loss, timeout,
 * a server failure, a request still in progress), refused only while every
 * send of the key was definitively refused.
 */
export function settleFailedAuthoringCreate(intent: AuthoringCreateIntent, cause: unknown, noun: string): AuthoringCreateIntent {
  const uncertain = { ...intent, state: 'pending' as const, uncertain: true }
  if (!(cause instanceof ApiError)) return uncertain
  switch (cause.code) {
    case 'idempotency_key_reused': throw new AuthoringCreateNeedsNewIntent('changed', noun)
    case 'idempotency_key_expired': throw new AuthoringCreateNeedsNewIntent('expired', noun)
    case 'idempotency_resource_deleted': throw new AuthoringCreateNeedsNewIntent('deleted', noun)
    case 'idempotency_request_in_progress':
    case 'scenario_save_outcome_unknown':
    case 'script_save_outcome_unknown':
    case 'test_create_outcome_unknown': return uncertain
  }
  // A 4xx answers only this request, before anything it sent could commit.
  // It proves nothing about an earlier send whose answer was lost, so a key
  // with such a send stays pending. Server and gateway failures may follow a
  // commit.
  if (cause.status >= 400 && cause.status < 500) return intent.uncertain ? { ...intent, state: 'pending' } : { ...intent, state: 'refused' }
  return uncertain
}

// ---- Reload ledger -------------------------------------------------------
//
// A new script draft is not checkpointed, so its intent survives a reload
// only by payload: reopening the same work and saving it again finds the
// pending intent and replays instead of duplicating. Entries hold no draft
// content, only the key, purpose, project and payload digest.

function readLedger(now: number): AuthoringCreateIntent[] {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(LEDGER_KEY) || '[]')
    return Array.isArray(parsed) ? parsed.filter(isAuthoringCreateIntent).filter(intent => intent.state === 'pending' && now - Date.parse(intent.issuedAt) < LEDGER_RETENTION_MS) : []
  } catch { return [] }
}

function writeLedger(entries: AuthoringCreateIntent[]): void {
  try {
    if (entries.length === 0) window.localStorage.removeItem(LEDGER_KEY)
    else window.localStorage.setItem(LEDGER_KEY, JSON.stringify(entries.slice(-LEDGER_LIMIT)))
  } catch { /* The in-memory intent still protects this session. */ }
}

/**
 * The intent a create starts from: the editor's own, unless it has none or
 * holds a refused key for other content, when a pending key recorded for
 * exactly this payload takes precedence (the same work, sent before).
 */
export function startingAuthoringCreateIntent(current: AuthoringCreateIntent | null | undefined, purpose: AuthoringCreatePurpose, projectId: string, payloadDigest: string, now = Date.now(), fresh = false): AuthoringCreateIntent | null {
  // An explicit new intent (Save as new) never resumes a recorded key.
  if (fresh) return null
  if (current && !(current.state === 'refused' && current.payloadDigest !== payloadDigest)) return current
  return recallPendingAuthoringCreateIntent(purpose, projectId, payloadDigest, now) ?? current ?? null
}

/**
 * Sends one create under an intent. The intent is recorded as uncertain
 * before the request leaves (a reload mid-request must keep the key), and a
 * refusal is judged against whether any earlier send was unanswered. The
 * intent ends only when finish succeeds; any failure after the create was
 * answered (a failed follow-up read, a superseded result) keeps it pending.
 */
export async function sendUnderAuthoringCreateIntent<T, R>(
  intent: AuthoringCreateIntent,
  owner: { update: (next: AuthoringCreateIntent | null) => void },
  noun: string,
  send: (key: string) => Promise<T>,
  finish: (outcome: T) => Promise<{ done: boolean; value: R }>,
): Promise<R> {
  const wasUncertain = Boolean(intent.uncertain)
  const inFlight: AuthoringCreateIntent = { ...intent, state: 'pending', uncertain: true }
  const keep = (next: AuthoringCreateIntent) => { owner.update(next); recordAuthoringCreateIntent(next) }
  keep(inFlight)
  let outcome: T
  try {
    outcome = await send(intent.key)
  } catch (cause) {
    try {
      keep(settleFailedAuthoringCreate({ ...intent, uncertain: wasUncertain }, cause, noun))
    } catch (decision) {
      // A key that can never create the object stays until the user
      // explicitly saves as new.
      keep(inFlight)
      throw decision
    }
    throw cause
  }
  let done = false
  try {
    const finished = await finish(outcome)
    done = finished.done
    return finished.value
  } finally {
    if (done) {
      owner.update(null)
      forgetAuthoringCreateIntent(intent)
    } else {
      keep(inFlight)
    }
  }
}

/** The pending intent recorded for a key that carried exactly this payload, if any. */
export function recallPendingAuthoringCreateIntent(purpose: AuthoringCreatePurpose, projectId: string, payloadDigest: string, now = Date.now()): AuthoringCreateIntent | null {
  return readLedger(now).find(intent => intent.purpose === purpose && intent.projectId === projectId
    && (intent.payloadDigest === payloadDigest || (intent.sentDigests ?? []).includes(payloadDigest))) ?? null
}

/** Records a pending intent, or forgets it once its outcome is definitive. */
export function recordAuthoringCreateIntent(intent: AuthoringCreateIntent, now = Date.now()): void {
  const others = readLedger(now).filter(entry => entry.key !== intent.key)
  writeLedger(intent.state === 'pending' ? [...others, intent] : others)
}

export function forgetAuthoringCreateIntent(intent: AuthoringCreateIntent | null | undefined, now = Date.now()): void {
  if (!intent) return
  writeLedger(readLedger(now).filter(entry => entry.key !== intent.key))
}
