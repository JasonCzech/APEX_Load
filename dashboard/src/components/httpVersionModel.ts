import type { HTTPActionVersion } from '../types/scripts'

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

/** Match the engine's explicit global setting before a protocol alias default. */
export function inheritedHTTPActionVersion(protocol = 'http', scriptVersion?: unknown): HTTPActionVersion | null {
  if (scriptVersion !== undefined && typeof scriptVersion !== 'string') return null
  const raw = scriptVersion?.trim() || ''
  if (scriptVersion !== undefined && scriptVersion !== '') {
    // Preserve the existing nonempty scalar semantics; the editor never authors whitespace.
    if (!raw) return 'auto'
    switch (raw.toUpperCase()) {
      case '1.1': case 'HTTP/1.1': return '1.1'
      case '2': case 'HTTP/2': case 'HTTP/2.0': return '2'
      default: return null
    }
  }
  switch (protocol.trim().toLowerCase()) {
    case 'http/1.1': return '1.1'
    case 'http/2': return '2'
    default: return 'auto'
  }
}

export function formatScriptHTTPVersion(protocol = 'http', scriptVersion?: unknown): string {
  const policy = inheritedHTTPActionVersion(protocol, scriptVersion)
  return policy === null ? 'Unsupported script HTTP version' : formatHTTPVersion(policy === 'auto' ? undefined : policy)
}

/** Normalize only the displayed selection, leaving the authored config untouched. */
export function scriptHTTPVersionSettings(protocol: string, scriptVersion?: unknown) {
  const policy = inheritedHTTPActionVersion(protocol, scriptVersion)
  const defaultPolicy = inheritedHTTPActionVersion(protocol)
  return {
    valid: policy !== null,
    selection: policy === null ? 'invalid' : scriptVersion === undefined || scriptVersion === '' ? '' : policy === 'auto' ? 'existing-auto' : policy,
    defaultLabel: defaultPolicy === 'auto' ? HTTP_VERSION_OPTIONS[0].label : `Protocol default (${formatScriptHTTPVersion(protocol)})`,
    effectiveLabel: formatScriptHTTPVersion(protocol, scriptVersion),
  }
}
