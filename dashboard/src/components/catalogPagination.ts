export const CATALOG_PAGE_SIZE = 100
export const MAX_CATALOG_PAGES = 100

export type OffsetCatalogPage<T> = {
  items: T[]
  hasMore?: boolean
}

type DrainOptions = {
  pageSize?: number
  maxPages?: number
}

/**
 * Drain an offset-paginated catalog without allowing a stalled or unbounded
 * backend response to trap the dashboard in an infinite request loop.
 */
export async function drainBoundedOffsetCatalog<T>(
  fetchPage: (limit: number, offset: number) => Promise<OffsetCatalogPage<T>>,
  keyFor: (item: T) => string,
  label: string,
  options: DrainOptions = {},
): Promise<T[]> {
  const pageSize = options.pageSize ?? CATALOG_PAGE_SIZE
  const maxPages = options.maxPages ?? MAX_CATALOG_PAGES
  const byKey = new Map<string, T>()
  let offset = 0

  for (let pageNumber = 0; pageNumber < maxPages; pageNumber += 1) {
    const page = await fetchPage(pageSize, offset)
    const items = page.items || []
    if (items.length === 0) {
      if (page.hasMore) {
        throw new Error(`${label} pagination did not advance; retry after the service recovers.`)
      }
      return [...byKey.values()]
    }

    let advanced = false
    items.forEach(item => {
      const key = keyFor(item).trim()
      if (!key) return
      if (!byKey.has(key)) advanced = true
      byKey.set(key, item)
    })
    if (!advanced) {
      throw new Error(`${label} pagination did not advance; retry after the service recovers.`)
    }

    if (page.hasMore === false || (page.hasMore === undefined && items.length < pageSize)) {
      return [...byKey.values()]
    }
    offset += items.length
  }

  throw new Error(`${label} pagination exceeded the supported ${maxPages * pageSize}-item safety limit.`)
}
