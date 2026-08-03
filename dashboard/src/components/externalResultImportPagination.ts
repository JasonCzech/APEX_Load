import type { ExternalResultImport } from '../types'

export type ExternalResultImportPageParams = {
  project_id?: string
  application_id?: string
  release_id?: string
  provider?: string
  limit: number
  offset: number
}

export type ExternalResultImportPage = {
  imports: ExternalResultImport[]
  count: number
  limit: number
  offset: number
  has_more?: boolean
}

type ExternalResultImportScopeParams = Omit<ExternalResultImportPageParams, 'limit' | 'offset'>

type LoadOptions = {
  isCurrent?: () => boolean
  pageSize?: number
}

const DEFAULT_PAGE_SIZE = 1000

export async function loadAllExternalResultImports(
  loadPage: (params: ExternalResultImportPageParams) => Promise<ExternalResultImportPage>,
  scope: ExternalResultImportScopeParams,
  { isCurrent = () => true, pageSize = DEFAULT_PAGE_SIZE }: LoadOptions = {},
): Promise<ExternalResultImport[] | null> {
  const imports: ExternalResultImport[] = []
  let offset = 0

  while (isCurrent()) {
    const page = await loadPage({ ...scope, limit: pageSize, offset })
    if (!isCurrent()) return null

    const pageImports = page.imports || []
    imports.push(...pageImports)
    if (!page.has_more) return imports
    if (pageImports.length === 0) {
      throw new Error('External result import pagination did not advance')
    }
    offset += pageImports.length
  }

  return null
}
