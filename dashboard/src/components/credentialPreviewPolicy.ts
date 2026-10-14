// Disclosure-only dashboard mirror of pkg/credential. Keep the policy here so
// every client preview can share one conservative classifier without importing
// credential-bearing source data or duplicating regular expressions.
const sensitiveCredentialNames = new Set([
  'accesskey', 'accesskeyid', 'accesstoken', 'accountkey', 'acktoken', 'apikey',
  'auth', 'authentication', 'authorization', 'basicauth',
  'clientsecret', 'connectionstring', 'cookie', 'credential', 'credentials', 'csrf',
  'csrftoken', 'deliverytag', 'dsn', 'idsecret', 'idtoken',
  'jmssettlementtoken', 'locktoken', 'password', 'pass', 'passcode', 'passphrase', 'passwd',
  'privatekey', 'proxyauthorization', 'pwd', 'receipthandle',
  'refreshtoken', 'sas', 'saskey', 'sassignature', 'secret',
  'secretaccesskey', 'sharedaccesskey', 'sharedaccesssignature',
  'session', 'sessionid', 'sessiontoken', 'sid',
  'settlementtoken', 'signature', 'token', 'xsrf', 'xsrftoken',
])

// A credential component followed by one of these final words describes the
// credential rather than holding it (session_feature_flags, token_type).
// URL words stay out: a sas_url can carry the credential itself.
const credentialPropertyWords = new Set([
  'count', 'duration', 'enabled', 'expiration', 'expires', 'expiry',
  'feature', 'features', 'flag', 'flags', 'length', 'method', 'mode',
  'timeout', 'ttl', 'type', 'version',
])

const broadSensitiveHeaderNames = new Set([
  'authorization',
  'proxy-authorization',
  'cookie',
  'set-cookie',
  'x-api-key',
  'api-key',
])

const sensitiveHeaderFragments = [
  'authorization',
  'api-key',
  'apikey',
  'token',
  'secret',
  'credential',
  'csrf',
]

/** Keep this classifier in parity with pkg/credential.SensitiveName. */
export function normalizeCredentialPreviewName(value: string): string {
  let decoded = value.trim()
  for (let pass = 0; pass < 3; pass += 1) {
    try {
      const next = decodeURIComponent(decoded.replace(/\+/g, '%20'))
      if (next === decoded) break
      decoded = next
    } catch {
      break
    }
  }
  return decoded.toLowerCase().replace(/[^a-z0-9]+/g, '')
}

export function isSensitiveCredentialPreviewName(name: string): boolean {
  if (sensitiveCredentialNames.has(normalizeCredentialPreviewName(name))) return true
  const parts = name.trim().split(/[^A-Za-z0-9]+/).filter(Boolean)
  const describesCredential = parts.length > 1 && credentialPropertyWords.has(normalizeCredentialPreviewName(parts[parts.length - 1]))
  for (let start = 0; start < parts.length; start += 1) {
    let combined = ''
    for (let end = start; end < parts.length && end < start + 4; end += 1) {
      combined += normalizeCredentialPreviewName(parts[end])
      if (sensitiveCredentialNames.has(combined) && !(describesCredential && end < parts.length - 1)) return true
    }
  }
  return false
}

/** Keep this classifier in parity with pkg/credential.SensitiveHeader. */
export function isSensitiveCredentialPreviewHeader(name: string): boolean {
  const lower = name.trim().toLowerCase()
  if (broadSensitiveHeaderNames.has(lower)) return true
  if (sensitiveHeaderFragments.some(fragment => lower.includes(fragment))) return true
  if (lower === 'key' || lower.endsWith('-key') || lower.endsWith('_key')) return true
  return lower.split(/[-_. ]+/).some(part => part === 'auth' || part === 'key')
}

export function isSensitiveCredentialPreviewKey(name: string): boolean {
  return isSensitiveCredentialPreviewName(name) || isSensitiveCredentialPreviewHeader(name)
}
