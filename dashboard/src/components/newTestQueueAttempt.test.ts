// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import {
  clearNewTestQueueAttempt,
  fingerprintNewTestQueuePayload,
  newTestQueueAttemptStorageKey,
  persistNewTestQueueAttempt,
  readNewTestQueueAttempt,
} from './newTestQueueAttempt'

describe('newTestQueueAttempt', () => {
  const scope = { projectId: 'project-1', applicationId: 'app-1' }

  beforeEach(() => {
    window.localStorage.clear()
    window.sessionStorage.clear()
  })
  afterEach(() => {
    window.localStorage.clear()
    window.sessionStorage.clear()
  })

  it('fingerprints the complete payload canonically while preserving meaningful differences', async () => {
    const first = await fingerprintNewTestQueuePayload({
      release_id: 'release-1',
      groups: [{ vusers: 10, script_id: 'script-1' }],
      runtime_settings: { pacing: { delay_ms: 1_000, type: 'fixed_delay' } },
    })
    const reordered = await fingerprintNewTestQueuePayload({
      runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: 1_000 } },
      groups: [{ script_id: 'script-1', vusers: 10 }],
      release_id: 'release-1',
    })
    const changed = await fingerprintNewTestQueuePayload({
      release_id: 'release-1',
      groups: [{ vusers: 20, script_id: 'script-1' }],
      runtime_settings: { pacing: { delay_ms: 1_000, type: 'fixed_delay' } },
    })

    expect(reordered).toBe(first)
    expect(changed).not.toBe(first)
  })

  it('keeps an unresolved attempt until the owning run is explicitly resolved', () => {
    const createdAt = 1_700_000_000_000
    const attempt = persistNewTestQueueAttempt('payload-a', 'test-a', scope, createdAt)

    expect(readNewTestQueueAttempt(scope)).toEqual(attempt)
    expect(readNewTestQueueAttempt(scope)).toEqual(expect.objectContaining({ createdAt }))
    expect(window.localStorage.getItem(newTestQueueAttemptStorageKey(scope))).not.toBeNull()
  })

  it('migrates an unresolved session-only attempt into durable storage', () => {
    const key = newTestQueueAttemptStorageKey(scope)
    window.sessionStorage.setItem(key, JSON.stringify({
      version: 2,
      requestedTestId: 'test-legacy',
      payloadFingerprint: 'payload-legacy',
      projectId: scope.projectId,
      applicationId: scope.applicationId,
      createdAt: 1_700_000_000_000,
    }))

    expect(readNewTestQueueAttempt(scope)).toEqual(expect.objectContaining({ requestedTestId: 'test-legacy' }))
    expect(window.localStorage.getItem(key)).not.toBeNull()
    expect(window.sessionStorage.getItem(key)).toBeNull()
  })

  it('isolates attempts by project/application scope and clears only the matching request', () => {
    const first = persistNewTestQueueAttempt('payload-a', 'test-a', scope)
    const otherScope = { projectId: 'project-1', applicationId: 'app-2' }
    const second = persistNewTestQueueAttempt('payload-b', 'test-b', otherScope)

    expect(readNewTestQueueAttempt(scope)).toEqual(first)
    expect(readNewTestQueueAttempt(otherScope)).toEqual(second)

    clearNewTestQueueAttempt({ ...scope, requestedTestId: 'test-b' })
    expect(readNewTestQueueAttempt(scope)).toEqual(first)

    clearNewTestQueueAttempt(first)
    expect(readNewTestQueueAttempt(scope)).toBeNull()
    expect(readNewTestQueueAttempt(otherScope)).toEqual(second)
  })
})
