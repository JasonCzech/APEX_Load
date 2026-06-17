export type HttpURLScheme = 'https://' | 'http://'

export const DEFAULT_HTTP_URL_SCHEME: HttpURLScheme = 'https://'

const HTTP_URL_PREFIX_RE = /^(https?):\/\/(.*)$/i
const ABSOLUTE_SCHEME_RE = /^[a-z][a-z0-9+.-]*:\/\//i

export function normalizeHttpURLInput(
  rawValue: string,
  currentScheme: HttpURLScheme = DEFAULT_HTTP_URL_SCHEME,
): { scheme: HttpURLScheme; address: string } {
  const trimmed = rawValue.trim()
  const match = HTTP_URL_PREFIX_RE.exec(trimmed)
  if (!match) {
    return { scheme: currentScheme, address: rawValue }
  }
  return {
    scheme: `${match[1].toLowerCase()}://` as HttpURLScheme,
    address: match[2],
  }
}

export function composeHttpURL(scheme: HttpURLScheme, address: string): string | null {
  const trimmed = address.trim()
  if (!trimmed || ABSOLUTE_SCHEME_RE.test(trimmed)) {
    return null
  }
  return `${scheme}${trimmed.replace(/^\/+/, '')}`
}
