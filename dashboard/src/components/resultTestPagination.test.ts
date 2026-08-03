import { describe, expect, it, vi } from 'vitest'

import type { TestSummary } from '../types'
import { loadAllResultTests, loadResultTestTail } from './resultTestPagination'

function test(id: string): TestSummary {
  return {
    id,
    name: id,
    status: 'COMPLETE',
    duration: '1m',
    total_vusers: 1,
    created_at: '2026-08-03T00:00:00Z',
    groups: [],
  }
}

describe('loadAllResultTests', () => {
  it('drains every bounded page so old results remain reachable', async () => {
    const loadPage = vi.fn(async ({ offset = 0 }: { offset?: number }) => ({
      tests: offset === 0 ? [test('newest'), test('middle')] : [test('oldest')],
      count: offset === 0 ? 2 : 1,
      total: 3,
      limit: 2,
      offset,
    }))

    const result = await loadAllResultTests(loadPage, { project_id: 'project-a', summary: true }, 2)

    expect(loadPage).toHaveBeenNthCalledWith(1, { project_id: 'project-a', summary: true, limit: 2, offset: 0 })
    expect(loadPage).toHaveBeenNthCalledWith(2, { project_id: 'project-a', summary: true, limit: 2, offset: 2 })
    expect(result.tests?.map(item => item.id)).toEqual(['newest', 'middle', 'oldest'])
    expect(result).toMatchObject({ count: 3, total: 3, offset: 0 })
  })

  it('streams older pages independently after the first page is already visible', async () => {
    const pages: string[][] = []
    const loadPage = vi.fn(async ({ offset = 0 }: { offset?: number }) => ({
      tests: offset === 2 ? [test('older-a'), test('older-b')] : [test('oldest')],
      count: offset === 2 ? 2 : 1,
      total: 5,
      limit: 2,
      offset,
    }))

    await expect(loadResultTestTail(loadPage, { application_id: 'app-a', summary: true }, {
      startOffset: 2,
      total: 5,
      pageSize: 2,
      onPage: tests => pages.push(tests.map(item => item.id)),
    })).resolves.toBe(5)

    expect(loadPage).toHaveBeenNthCalledWith(1, { application_id: 'app-a', summary: true, limit: 2, offset: 2 })
    expect(loadPage).toHaveBeenNthCalledWith(2, { application_id: 'app-a', summary: true, limit: 2, offset: 4 })
    expect(pages).toEqual([['older-a', 'older-b'], ['oldest']])
  })

  it('rejects a repeated non-advancing page instead of looping forever', async () => {
    const loadPage = vi.fn(async ({ offset = 0 }: { offset?: number }) => ({
      tests: [test('same')],
      count: 1,
      total: 3,
      limit: 1,
      offset,
    }))

    await expect(loadAllResultTests(loadPage, {}, 1)).rejects.toThrow('repeated a page')
    expect(loadPage).toHaveBeenCalledTimes(2)
  })
})
