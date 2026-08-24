export const BULK_MUTATION_CONCURRENCY = 6

/**
 * Promise.allSettled semantics with a fixed-size worker pool. Results retain
 * input order so callers can safely associate each outcome with its item.
 */
export async function boundedAllSettled<T, R>(
  items: readonly T[],
  task: (item: T, index: number) => Promise<R>,
  concurrency = BULK_MUTATION_CONCURRENCY,
): Promise<PromiseSettledResult<R>[]> {
  const results = new Array<PromiseSettledResult<R>>(items.length)
  const normalizedConcurrency = Number.isFinite(concurrency) ? Math.floor(concurrency) : 1
  const workerCount = Math.min(items.length, Math.max(1, normalizedConcurrency))
  let nextIndex = 0

  const worker = async () => {
    while (nextIndex < items.length) {
      const index = nextIndex
      nextIndex += 1
      try {
        results[index] = { status: 'fulfilled', value: await task(items[index], index) }
      } catch (reason) {
        results[index] = { status: 'rejected', reason }
      }
    }
  }

  await Promise.all(Array.from({ length: workerCount }, () => worker()))
  return results
}
