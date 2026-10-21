// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'

import { keyedAuthoringCreate } from './keyedAuthoringCreate'

afterEach(() => { vi.unstubAllGlobals() })

const json = (body: unknown, status: number, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } })
const sentHeaders = (fetch: ReturnType<typeof vi.fn>, call: number) => new Headers((fetch.mock.calls[call] as unknown as [string, RequestInit])[1].headers)

describe('keyed authoring create', () => {
  it('asks for the original-outcome contract and tells a create, a legacy replay and an original replay apart', async () => {
    const outcome = { receipt_version: 2, resource_type: 'script', resource_id: 'script-1', project_id: 'default', content_revision: 'rev-1', created_at: '' }
    const responses = [
      json({ id: 'script-1' }, 201, { 'X-APEX-Authoring-Receipt-Version': '2' }),
      json({ id: 'script-1', name: 'now' }, 201, { 'Idempotent-Replayed': 'true', 'X-APEX-Authoring-Receipt-Version': '1' }),
      json(outcome, 201, { 'Idempotent-Replayed': 'true', 'X-APEX-Authoring-Receipt-Version': '2' }),
      // A controller older than receipt versions answers without the echo.
      json({ id: 'script-1' }, 201, { 'Idempotent-Replayed': 'true' }),
    ]
    const fetch = vi.fn(async () => responses.shift()!)
    vi.stubGlobal('fetch', fetch)
    expect(await keyedAuthoringCreate('/scripts', '{}', 'script-create-key-0001', 'script')).toEqual({ kind: 'created', resource: { id: 'script-1' } })
    expect(await keyedAuthoringCreate('/scripts', '{}', 'script-create-key-0001', 'script')).toEqual({ kind: 'replayed', resource: { id: 'script-1', name: 'now' } })
    expect(await keyedAuthoringCreate('/scripts', '{}', 'script-create-key-0001', 'script')).toEqual({ kind: 'replayed-original', outcome })
    expect(await keyedAuthoringCreate('/scripts', '{}', 'script-create-key-0001', 'script')).toMatchObject({ kind: 'replayed' })
    const headers = sentHeaders(fetch, 0)
    expect(headers.get('Idempotency-Key')).toBe('script-create-key-0001')
    expect(headers.get('X-APEX-Authoring-Receipt-Version')).toBe('2')
  })

  it('falls back to the legacy contract only when the controller explicitly refuses version 2', async () => {
    const responses = [
      json({ error: 'unavailable', code: 'idempotency_receipt_version_unavailable' }, 503),
      json({ id: 'scenario-1' }, 201, { 'X-APEX-Authoring-Receipt-Version': '1' }),
      json({ error: 'unavailable', code: 'idempotency_unavailable' }, 503),
    ]
    const fetch = vi.fn(async () => responses.shift()!)
    vi.stubGlobal('fetch', fetch)
    expect(await keyedAuthoringCreate('/scenarios', '{}', 'scenario-create-key-01', 'scenario')).toEqual({ kind: 'created', resource: { id: 'scenario-1' } })
    expect(sentHeaders(fetch, 1).get('X-APEX-Authoring-Receipt-Version')).toBe('1')
    expect(sentHeaders(fetch, 1).get('Idempotency-Key')).toBe('scenario-create-key-01')
    await expect(keyedAuthoringCreate('/scenarios', '{}', 'scenario-create-key-01', 'scenario')).rejects.toMatchObject({ status: 503, code: 'idempotency_unavailable' })
    expect(fetch).toHaveBeenCalledTimes(3)
  })

  it('refuses an unreadable original outcome rather than guess which object it names', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ receipt_version: 2, resource_type: 'scenario', resource_id: 'x' }, 201, { 'Idempotent-Replayed': 'true', 'X-APEX-Authoring-Receipt-Version': '2' })))
    await expect(keyedAuthoringCreate('/scripts', '{}', 'script-create-key-0001', 'script')).rejects.toThrow('unreadable')
    // A replay without the echo still never yields a resource without an id.
    vi.stubGlobal('fetch', vi.fn(async () => json({ name: 'no id' }, 201, { 'Idempotent-Replayed': 'true' })))
    await expect(keyedAuthoringCreate('/scripts', '{}', 'script-create-key-0001', 'script')).rejects.toThrow('unreadable')
  })

  it('reads a recorded outcome by its shape when the contract echo is not visible', async () => {
    const outcome = { receipt_version: 2, resource_type: 'scenario', resource_id: 'scenario-1', project_id: 'default', content_revision: 'rev-1', created_at: '' }
    vi.stubGlobal('fetch', vi.fn(async () => json(outcome, 201, { 'Idempotent-Replayed': 'true' })))
    expect(await keyedAuthoringCreate('/scenarios', '{}', 'scenario-create-key-01', 'scenario')).toEqual({ kind: 'replayed-original', outcome })
  })
})
