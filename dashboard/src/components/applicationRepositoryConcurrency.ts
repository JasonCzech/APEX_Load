export const REPOSITORY_CATALOG_REQUEST_CONCURRENCY = 8

export type RepositoryCatalogTaskLimiter = <T>(task: () => Promise<T>) => Promise<T>

export function createRepositoryCatalogTaskLimiter(
  concurrency = REPOSITORY_CATALOG_REQUEST_CONCURRENCY,
): RepositoryCatalogTaskLimiter {
  const limit = Math.max(1, Math.floor(concurrency))
  let active = 0
  const queued: Array<() => void> = []

  return <T,>(task: () => Promise<T>) => new Promise<T>((resolve, reject) => {
    const run = () => {
      active += 1
      task().then(resolve, reject).finally(() => {
        active -= 1
        queued.shift()?.()
      })
    }
    if (active < limit) run()
    else queued.push(run)
  })
}
