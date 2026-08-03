import type { TestSummary } from '../types'
import type { SharedTestListParams, TestListLoader } from '../hooks/useSharedTestList'

type TestListPage = Awaited<ReturnType<TestListLoader>>

export const DEFAULT_TEST_CATALOG_PAGE_SIZE = 1000
const MAX_TEST_CATALOG_PAGES = 10_000

export interface TestSummaryTailOptions {
  startOffset: number
  total: number
  pageSize?: number
  seenTestIds?: Iterable<string>
  onPage: (tests: TestSummary[], total: number) => void
}

function responseTotal(page: TestListPage): number | undefined {
  return typeof page.total === 'number' && Number.isFinite(page.total) && page.total >= 0
    ? Math.floor(page.total)
    : undefined
}

export async function loadTestSummaryTail(
  loadPage: TestListLoader,
  scope: SharedTestListParams,
  options: TestSummaryTailOptions,
): Promise<number> {
  const pageSize = options.pageSize ?? DEFAULT_TEST_CATALOG_PAGE_SIZE
  if (!Number.isInteger(pageSize) || pageSize < 1) {
    throw new Error('Test catalog page size must be a positive integer')
  }

  const seenTestIDs = new Set(options.seenTestIds || [])
  let offset = options.startOffset
  const expectedTotal = Math.max(options.total, offset)

  for (let pageNumber = 0; pageNumber < MAX_TEST_CATALOG_PAGES; pageNumber += 1) {
    if (offset >= expectedTotal) return expectedTotal
    const page = await loadPage({ ...scope, limit: pageSize, offset })
    const tests = page.tests || []
    const nextTotal = responseTotal(page)
    if (nextTotal !== undefined && nextTotal !== expectedTotal) {
      throw new Error('Test catalog changed while paginated results were loading')
    }
    for (const test of tests) {
      if (seenTestIDs.has(test.id)) {
        throw new Error('Test catalog repeated an item across paginated results')
      }
      seenTestIDs.add(test.id)
    }
    options.onPage(tests, expectedTotal)
    offset += tests.length
    if (tests.length === 0 && offset < expectedTotal) {
      throw new Error('Test catalog pagination ended before every reported result was loaded')
    }
  }

  throw new Error('Test catalog pagination exceeded its safety limit')
}

export async function loadAllTestSummaries(
  loadPage: TestListLoader,
  scope: SharedTestListParams,
  pageSize = DEFAULT_TEST_CATALOG_PAGE_SIZE,
): Promise<TestListPage> {
  if (!Number.isInteger(pageSize) || pageSize < 1) {
    throw new Error('Test catalog page size must be a positive integer')
  }

  const testsByID = new Map<string, TestSummary>()
  let offset = 0
  let expectedTotal: number | undefined

  for (let pageNumber = 0; pageNumber < MAX_TEST_CATALOG_PAGES; pageNumber += 1) {
    const page = await loadPage({ ...scope, limit: pageSize, offset })
    const tests = page.tests || []
    const nextTotal = responseTotal(page)
    if (nextTotal !== undefined) {
      if (expectedTotal !== undefined && nextTotal !== expectedTotal) {
        throw new Error('Test catalog changed while paginated results were loading')
      }
      expectedTotal = nextTotal
    }

    for (const test of tests) {
      if (testsByID.has(test.id)) {
        throw new Error('Test catalog repeated an item across paginated results')
      }
      testsByID.set(test.id, test)
    }

    offset += tests.length
    const reachedReportedTotal = expectedTotal !== undefined && offset >= expectedTotal
    const reachedUnreportedEnd = expectedTotal === undefined && tests.length < pageSize
    if (tests.length === 0 || reachedReportedTotal || reachedUnreportedEnd) {
      if (expectedTotal !== undefined && testsByID.size !== expectedTotal) {
        throw new Error('Test catalog pagination ended before every reported result was loaded')
      }
      const allTests = Array.from(testsByID.values())
      return {
        tests: allTests,
        count: allTests.length,
        total: expectedTotal ?? allTests.length,
        limit: pageSize,
        offset: 0,
      }
    }
  }

  throw new Error('Test catalog pagination exceeded its safety limit')
}
