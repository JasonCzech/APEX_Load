// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'

import { analysisPoliciesApi } from './analysisPoliciesApi'

afterEach(() => { vi.unstubAllGlobals() })

describe('analysis policies API', () => {
  it('reads every page of a project\'s policies, stopping at the first short page', async () => {
    const policy = (index: number) => ({ id: `policy-${index}`, project_id: 'p1', kind: 'browser_policy', name: `P${index}`, revision: 1, definition: {}, deleted: false, created_at: '' })
    const pages = [Array.from({ length: 100 }, (_, index) => policy(index)), [policy(100), policy(101)]]
    const fetch = vi.fn(async () => new Response(JSON.stringify(pages.shift() ?? []), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetch)
    const listed = await analysisPoliciesApi.list('p1', 'browser_policy')
    expect(listed.map(item => item.id)).toEqual([...Array.from({ length: 100 }, (_, index) => `policy-${index}`), 'policy-100', 'policy-101'])
    expect(fetch).toHaveBeenCalledTimes(2)
    const offsets = fetch.mock.calls.map(call => new URL(String((call as unknown as [string])[0]), 'http://localhost').searchParams)
    expect(offsets.map(params => [params.get('kind'), params.get('limit'), params.get('offset')])).toEqual([['browser_policy', '100', '0'], ['browser_policy', '100', '100']])
  })
})
