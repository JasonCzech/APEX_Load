import type { TestSummary } from '../types'
import type { SharedTestListParams, TestListLoader } from '../hooks/useSharedTestList'

type TestListPage = Awaited<ReturnType<TestListLoader>>

const DEFAULT_RESULT_PAGE_SIZE = 1000
const MAX_RESULT_PAGES = 10_000

export interface ResultTestTailOptions {
  startOffset: number
  total: number
  pageSize?: number
  onPage: (tests: TestSummary[], total: number) => void
}

export async function loadResultTestTail(
  loadPage: TestListLoader,
  scope: SharedTestListParams,
  options: ResultTestTailOptions,
): Promise<number> {
  const pageSize = options.pageSize ?? DEFAULT_RESULT_PAGE_SIZE
  const seenPages = new Set<string>()
  let offset = options.startOffset
  let total = Math.max(options.total, offset)

  for (let pageNumber = 0; pageNumber < MAX_RESULT_PAGES; pageNumber += 1) {
    if (offset >= total) return total
    const page = await loadPage({ ...scope, limit: pageSize, offset })
    const pageTests = page.tests || []
    total = page.total ?? total
    const signature = pageTests.map(test => test.id).join('\u0000')
    if (pageTests.length > 0 && seenPages.has(signature)) {
      throw new Error('Result pagination repeated a page without advancing')
    }
    if (pageTests.length > 0) seenPages.add(signature)
    options.onPage(pageTests, total)
    if (pageTests.length === 0 || offset + pageTests.length >= total) return total
    offset += pageTests.length
  }

  throw new Error('Result pagination exceeded its safety limit')
}

export async function loadAllResultTests(
  loadPage: TestListLoader,
  scope: SharedTestListParams,
  pageSize = DEFAULT_RESULT_PAGE_SIZE,
): Promise<TestListPage> {
  const testsByID = new Map<string, TestSummary>()
  const first = await loadPage({ ...scope, limit: pageSize, offset: 0 })
  const firstTests = first.tests || []
  firstTests.forEach(test => testsByID.set(test.id, test))
  let total = Math.max(first.total ?? firstTests.length, firstTests.length)
  if (firstTests.length < total) {
    total = await loadResultTestTail(loadPage, scope, {
      startOffset: firstTests.length,
      total,
      pageSize,
      onPage: (tests, nextTotal) => {
        total = Math.max(nextTotal, testsByID.size)
        const previousSize = testsByID.size
        tests.forEach(test => testsByID.set(test.id, test))
        if (tests.length > 0 && testsByID.size === previousSize) {
          throw new Error('Result pagination repeated a page without advancing')
        }
      },
    })
  }
  const tests = Array.from(testsByID.values())
  return {
    tests,
    count: tests.length,
    total: Math.max(total, tests.length),
    limit: pageSize,
    offset: 0,
  }
}
