import type { ScriptExtractorScope } from '../types/scripts'

// describeExtractorScope summarizes which responses an HTTP extractor
// searches, or returns '' when it searches the final response only.
export function describeExtractorScope(scope?: ScriptExtractorScope): string {
  if (!scope) return ''
  const parts: string[] = []
  const included = (value?: string) => (value || '').trim().toLowerCase() === 'include'
  if (included(scope.redirects)) parts.push('redirect hops')
  if (included(scope.resources)) parts.push('resources')
  const searched = parts.length > 0 ? `Also searches ${parts.join(' and ')}` : 'Final response only'
  const filters: string[] = []
  if (scope.request_url) filters.push(`URL ${scope.request_url}`)
  if (scope.content_type) filters.push(`type ${scope.content_type}`)
  if (scope.header_names?.length) filters.push(`headers ${scope.header_names.join(', ')}`)
  return filters.length > 0 ? `${searched}; only ${filters.join(', ')}` : searched
}

// describeExtractorDFEs names the data-format transforms an extractor applies
// to each response body before searching, or returns ''.
export function describeExtractorDFEs(dfes?: string[]): string {
  return dfes && dfes.length > 0 ? `Decodes the body first: ${dfes.join(' → ')}` : ''
}
