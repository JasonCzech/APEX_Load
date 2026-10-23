// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'

import { telemetryPolicySegmentsApi } from './telemetryPolicySegmentsApi'

afterEach(() => { vi.unstubAllGlobals() })

const json = (body: unknown, status: number) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

describe('telemetry policy segments API (TEL-B)', () => {
  it('tells a change made (201) from a retry answered with the change it made (200)', async () => {
    const responses = [json({ sequence: 2 }, 201), json({ sequence: 2 }, 200)]
    const fetch = vi.fn(async () => responses.shift()!)
    vi.stubGlobal('fetch', fetch)
    const body = { request_id: 'request-1', telemetry_budget_policy_id: 'tight-budgets', expected_sequence: 1 }
    expect(await telemetryPolicySegmentsApi.append('run-1', body)).toEqual({ segment: { sequence: 2 }, replayed: false })
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toMatch(/\/api\/v1\/tests\/run-1\/telemetry-policy-segments$/)
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual(body)
    expect(await telemetryPolicySegmentsApi.append('run-1', body)).toEqual({ segment: { sequence: 2 }, replayed: true })
  })
})
