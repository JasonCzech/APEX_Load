export type SourceCodeLanguage = 'json' | 'xml' | 'yaml'

export function detectSourceCodeLanguage(sourcePath: string, source: string): SourceCodeLanguage {
  const extension = sourcePath.trim().toLowerCase().split('.').pop()
  if (extension === 'json') return 'json'
  if (extension === 'xml') return 'xml'
  if (extension === 'yaml' || extension === 'yml') return 'yaml'

  const trimmed = source.trimStart()
  if (trimmed.startsWith('<')) return 'xml'
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) return 'json'
  return 'yaml'
}
