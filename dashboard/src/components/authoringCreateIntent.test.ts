// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest'
import { ApiError } from '../hooks/apiClient'
import {
  AuthoringCreateNeedsNewIntent, forgetAuthoringCreateIntent, isAuthoringCreateIntent, nextAuthoringCreateIntent,
  recallPendingAuthoringCreateIntent, recordAuthoringCreateIntent, sendUnderAuthoringCreateIntent, settleFailedAuthoringCreate,
  startingAuthoringCreateIntent, type AuthoringCreateIntent,
} from './authoringCreateIntent'

beforeEach(() => { window.localStorage.clear() })

const now = Date.parse('2026-10-21T12:00:00Z')

describe('authoring create intents', () => {
  it('reuses a pending key for any payload and rotates a refused key only for a changed payload', () => {
    const first = nextAuthoringCreateIntent(null, 'script_create', 'p1', 'digest-a', now)
    expect(isAuthoringCreateIntent(first)).toBe(true)
    expect(first).toMatchObject({ state: 'pending', purpose: 'script_create', projectId: 'p1', payloadDigest: 'digest-a' })
    expect(nextAuthoringCreateIntent(first, 'script_create', 'p1', 'digest-b', now).key).toBe(first.key)
    const refused = { ...first, state: 'refused' as const }
    expect(nextAuthoringCreateIntent(refused, 'script_create', 'p1', 'digest-a', now)).toMatchObject({ key: first.key, state: 'pending' })
    expect(nextAuthoringCreateIntent(refused, 'script_create', 'p1', 'digest-b', now).key).not.toBe(first.key)
    // Another project or purpose is another intent.
    expect(nextAuthoringCreateIntent(first, 'script_create', 'p2', 'digest-a', now).key).not.toBe(first.key)
    expect(nextAuthoringCreateIntent(first, 'scenario_create', 'p1', 'digest-a', now).key).not.toBe(first.key)
  })

  it('records the payload each send carries and gives a re-sent refused key a fresh lifetime', () => {
    const first = nextAuthoringCreateIntent(null, 'script_create', 'p1', 'digest-a', now)
    expect(nextAuthoringCreateIntent(first, 'script_create', 'p1', 'digest-b', now + 1000)).toMatchObject({ key: first.key, payloadDigest: 'digest-b', issuedAt: first.issuedAt })
    const resent = nextAuthoringCreateIntent({ ...first, state: 'refused' }, 'script_create', 'p1', 'digest-a', now + 60_000)
    expect(resent).toMatchObject({ key: first.key, state: 'pending', issuedAt: new Date(now + 60_000).toISOString() })
  })

  it('never frees a key whose earlier send went unanswered', () => {
    const intent = nextAuthoringCreateIntent(null, 'script_create', 'p1', 'digest', now)
    const lost = settleFailedAuthoringCreate(intent, new TypeError('Failed to fetch'), 'script')
    expect(lost).toMatchObject({ state: 'pending', uncertain: true })
    // A later 401 or 400 answers only that request.
    expect(settleFailedAuthoringCreate(lost, new ApiError('expired session', 401), 'script')).toMatchObject({ state: 'pending', uncertain: true })
    expect(settleFailedAuthoringCreate(lost, new ApiError('invalid', 400), 'script').state).toBe('pending')
  })

  it('keeps an expired pending key so the controller can say it expired, but does not reuse an expired refusal', () => {
    const first = nextAuthoringCreateIntent(null, 'scenario_create', 'p1', 'digest', now)
    const later = now + 25 * 60 * 60 * 1000
    expect(nextAuthoringCreateIntent(first, 'scenario_create', 'p1', 'digest', later).key).toBe(first.key)
    expect(nextAuthoringCreateIntent({ ...first, state: 'refused' }, 'scenario_create', 'p1', 'digest', later).key).not.toBe(first.key)
  })

  it('settles failures as pending while a commit is possible and refused only when nothing was created', () => {
    const intent = nextAuthoringCreateIntent(null, 'script_create', 'p1', 'digest', now)
    expect(settleFailedAuthoringCreate(intent, new TypeError('Failed to fetch'), 'script').state).toBe('pending')
    expect(settleFailedAuthoringCreate(intent, new ApiError('boom', 500), 'script').state).toBe('pending')
    expect(settleFailedAuthoringCreate(intent, new ApiError('busy', 409, { error: 'busy', code: 'idempotency_request_in_progress' }), 'script').state).toBe('pending')
    expect(settleFailedAuthoringCreate(intent, new ApiError('unknown', 503, { error: 'unknown', code: 'scenario_save_outcome_unknown' }), 'scenario').state).toBe('pending')
    expect(settleFailedAuthoringCreate(intent, new ApiError('invalid', 400), 'script').state).toBe('refused')
    expect(settleFailedAuthoringCreate(intent, new ApiError('forbidden', 403), 'script').state).toBe('refused')
    for (const [code, reason] of [['idempotency_key_reused', 'changed'], ['idempotency_key_expired', 'expired'], ['idempotency_resource_deleted', 'deleted']] as const) {
      let thrown: unknown
      try { settleFailedAuthoringCreate(intent, new ApiError(code, code === 'idempotency_resource_deleted' ? 410 : 409, { error: code, code }), 'script') } catch (cause) { thrown = cause }
      expect(thrown).toBeInstanceOf(AuthoringCreateNeedsNewIntent)
      expect((thrown as AuthoringCreateNeedsNewIntent).reason).toBe(reason)
    }
  })

  it('recalls a pending key for any payload it carried', () => {
    const first = nextAuthoringCreateIntent(null, 'script_create', 'p1', 'digest-a', now)
    const changed = nextAuthoringCreateIntent(first, 'script_create', 'p1', 'digest-b', now)
    recordAuthoringCreateIntent(changed, now)
    expect(recallPendingAuthoringCreateIntent('script_create', 'p1', 'digest-a', now)?.key).toBe(first.key)
    expect(recallPendingAuthoringCreateIntent('script_create', 'p1', 'digest-b', now)?.key).toBe(first.key)
    let bounded = changed
    for (let index = 0; index < 20; index++) bounded = nextAuthoringCreateIntent(bounded, 'script_create', 'p1', `digest-${index}`, now)
    expect(bounded.sentDigests).toHaveLength(8)
    // The first payload, the likeliest to have committed, stays pinned.
    expect(bounded.sentDigests?.[0]).toBe('digest-a')
    expect(bounded.sentDigests?.[7]).toBe('digest-19')
    expect(isAuthoringCreateIntent(bounded)).toBe(true)
  })

  it('prefers a recorded pending key for the same work over rotating a refused one', () => {
    const earlier = nextAuthoringCreateIntent(null, 'scenario_create', 'p1', 'same-work', now)
    recordAuthoringCreateIntent(earlier, now)
    const refused = { ...nextAuthoringCreateIntent(null, 'scenario_create', 'p1', 'other-work', now), state: 'refused' as const }
    expect(startingAuthoringCreateIntent(refused, 'scenario_create', 'p1', 'same-work', now)?.key).toBe(earlier.key)
    expect(startingAuthoringCreateIntent(refused, 'scenario_create', 'p1', 'other-work', now)).toBe(refused)
    expect(startingAuthoringCreateIntent(null, 'scenario_create', 'p1', 'unknown-work', now)).toBeNull()
    // Save as new never resumes a recorded key.
    expect(startingAuthoringCreateIntent(null, 'scenario_create', 'p1', 'same-work', now, true)).toBeNull()
  })

  it('records a key as uncertain before it is sent and judges a refusal by the sends before it', async () => {
    const owner = { current: null as AuthoringCreateIntent | null, update(next: AuthoringCreateIntent | null) { owner.current = next } }
    const intent = nextAuthoringCreateIntent(null, 'script_create', 'p1', 'digest', now)
    let seenInFlight: AuthoringCreateIntent | null = null
    await expect(sendUnderAuthoringCreateIntent(intent, owner, 'script', async () => {
      seenInFlight = recallPendingAuthoringCreateIntent('script_create', 'p1', 'digest')
      throw new ApiError('invalid', 400)
    }, async () => ({ done: true, value: undefined }))).rejects.toThrow('invalid')
    // A reload mid-request finds the key, and its retry's 4xx keeps it.
    expect(seenInFlight).toMatchObject({ key: intent.key, uncertain: true })
    expect(owner.current).toMatchObject({ key: intent.key, state: 'refused' })
    const recalled = seenInFlight!
    await expect(sendUnderAuthoringCreateIntent(recalled, owner, 'script', async () => { throw new ApiError('expired session', 401) }, async () => ({ done: true, value: undefined }))).rejects.toThrow('expired session')
    expect(owner.current).toMatchObject({ key: intent.key, state: 'pending', uncertain: true })
    // A failure after the create was answered keeps the key, whatever its status.
    await expect(sendUnderAuthoringCreateIntent(intent, owner, 'script', async () => 'created', async () => { throw new ApiError('gone', 404) })).rejects.toThrow('gone')
    expect(owner.current).toMatchObject({ key: intent.key, state: 'pending' })
    await sendUnderAuthoringCreateIntent(intent, owner, 'script', async () => 'created', async () => ({ done: true, value: undefined }))
    expect(owner.current).toBeNull()
    expect(recallPendingAuthoringCreateIntent('script_create', 'p1', 'digest')).toBeNull()
  })

  it('recalls only pending intents for the same payload, and forgets definitive ones', () => {
    const intent = nextAuthoringCreateIntent(null, 'script_create', 'p1', 'digest', now)
    recordAuthoringCreateIntent(intent, now)
    expect(recallPendingAuthoringCreateIntent('script_create', 'p1', 'digest', now)?.key).toBe(intent.key)
    expect(recallPendingAuthoringCreateIntent('script_create', 'p1', 'other', now)).toBeNull()
    // Past the replay lifetime the key is still recalled, so the controller
    // can answer it as expired; only a week later is it dropped.
    expect(recallPendingAuthoringCreateIntent('script_create', 'p1', 'digest', now + 25 * 60 * 60 * 1000)?.key).toBe(intent.key)
    expect(recallPendingAuthoringCreateIntent('script_create', 'p1', 'digest', now + 9 * 24 * 60 * 60 * 1000)).toBeNull()
    recordAuthoringCreateIntent({ ...intent, state: 'refused' }, now)
    expect(recallPendingAuthoringCreateIntent('script_create', 'p1', 'digest', now)).toBeNull()
    recordAuthoringCreateIntent(intent, now)
    forgetAuthoringCreateIntent(intent, now)
    expect(recallPendingAuthoringCreateIntent('script_create', 'p1', 'digest', now)).toBeNull()
    // The ledger keeps no draft content.
    recordAuthoringCreateIntent(intent, now)
    expect(window.localStorage.getItem('apexload.authoring.create-intents.v1')).not.toContain('Orders')
  })

  it('bounds the ledger and ignores malformed entries', () => {
    for (let index = 0; index < 40; index++) recordAuthoringCreateIntent(nextAuthoringCreateIntent(null, 'script_create', 'p1', `digest-${index}`, now), now)
    expect(JSON.parse(window.localStorage.getItem('apexload.authoring.create-intents.v1') || '[]')).toHaveLength(32)
    window.localStorage.setItem('apexload.authoring.create-intents.v1', '{"not":"a list"}')
    expect(recallPendingAuthoringCreateIntent('script_create', 'p1', 'digest-39', now)).toBeNull()
    expect(isAuthoringCreateIntent({ version: 1, key: 'short', purpose: 'script_create', projectId: '', payloadDigest: '', state: 'pending', issuedAt: '' })).toBe(false)
  })
})
