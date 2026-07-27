/**
 * HTTP version labels shared by runtime editors and read-only execution views.
 *
 * Keep this module dependency-free: execution views should not inherit the
 * Script Editor's runtime-settings dependency graph just to format one value.
 */
export const HTTP_VERSION_OPTIONS = [
  { value: '', label: 'Auto-negotiate (HTTP/2 or HTTP/1.1)' },
  { value: '1.1', label: 'Require HTTP/1.1' },
  { value: '2', label: 'Require HTTP/2 (no fallback)' },
] as const

export function formatHTTPVersion(version?: string): string {
  switch (version?.trim()) {
    case undefined:
    case '':
      return HTTP_VERSION_OPTIONS[0].label
    case '1.1':
      return 'HTTP/1.1 required'
    case '2':
      return 'HTTP/2 required (no fallback)'
    default:
      return `Unsupported HTTP version ${version?.trim()}`
  }
}
