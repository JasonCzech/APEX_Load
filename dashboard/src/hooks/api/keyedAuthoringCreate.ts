import { ApiError, requestWithResponse } from '../apiClient'
import { AUTHORING_RECEIPT_VERSION_HEADER } from '../../components/authoringCreateIntent'

/**
 * The original outcome a receipt version 2 create recorded in its own
 * transaction. A replay returns it unchanged: it says which object the first
 * attempt created and at which revision, not what the object holds now.
 */
export interface AuthoringReceiptOutcome {
  receipt_version: 2
  resource_type: 'script' | 'scenario'
  resource_id: string
  project_id: string
  content_revision: string
  created_at: string
  authoring_projection?: Record<string, unknown>
  authoring_projection_omitted?: string
}

export type KeyedCreateOutcome<T> =
  // This request created the object.
  | { kind: 'created'; resource: T }
  // An earlier request with the key created it; the body is the object now
  // (receipt version 1: a legacy key or a controller without version 2).
  | { kind: 'replayed'; resource: T }
  // An earlier request with the key created it; this is its original outcome.
  | { kind: 'replayed-original'; outcome: AuthoringReceiptOutcome }

function isReceiptOutcome(value: unknown, resourceType: AuthoringReceiptOutcome['resource_type']): value is AuthoringReceiptOutcome {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const outcome = value as Record<string, unknown>
  return outcome.receipt_version === 2 && outcome.resource_type === resourceType
    && typeof outcome.resource_id === 'string' && outcome.resource_id !== ''
    && typeof outcome.project_id === 'string' && typeof outcome.content_revision === 'string'
}

/**
 * Sends one keyed create under receipt version 2. A controller that cannot
 * record original outcomes refuses version 2 before claiming the key, so the
 * same key is then sent under version 1. A controller older than receipt
 * versions ignores the header and answers as version 1 does.
 */
export async function keyedAuthoringCreate<T>(path: string, body: string, key: string, resourceType: AuthoringReceiptOutcome['resource_type']): Promise<KeyedCreateOutcome<T>> {
  const send = (version: '1' | '2') => requestWithResponse<unknown>(path, {
    method: 'POST', body,
    headers: { 'Idempotency-Key': key, [AUTHORING_RECEIPT_VERSION_HEADER]: version },
  })
  let response
  try {
    response = await send('2')
  } catch (cause) {
    if (!(cause instanceof ApiError && cause.code === 'idempotency_receipt_version_unavailable')) throw cause
    response = await send('1')
  }
  if (response.headers.get('Idempotent-Replayed') !== 'true') return { kind: 'created', resource: response.body as T }
  // The body decides as well as the echo: a proxy that hides the echo must
  // not turn a recorded outcome into a resource without an id.
  if (isReceiptOutcome(response.body, resourceType)) return { kind: 'replayed-original', outcome: response.body }
  if (response.headers.get(AUTHORING_RECEIPT_VERSION_HEADER) === '2' || !hasResourceId(response.body)) {
    throw new Error(`The controller replayed an unreadable ${resourceType} create outcome. Your draft is kept; check the library before saving again.`)
  }
  return { kind: 'replayed', resource: response.body as T }
}

function hasResourceId(value: unknown): boolean {
  return !!value && typeof value === 'object' && typeof (value as { id?: unknown }).id === 'string' && (value as { id: string }).id !== ''
}
