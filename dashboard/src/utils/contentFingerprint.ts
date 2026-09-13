// Deterministic content identity for edit detection; not an authorization signature.
function canonicalJSONValue(value: unknown): unknown {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return value
  if (typeof value === 'number') return Number.isFinite(value) ? value : null
  if (Array.isArray(value)) {
    return value.map(item => {
      const normalized = canonicalJSONValue(item)
      return normalized === undefined ? null : normalized
    })
  }
  if (typeof value !== 'object') return undefined

  const source = value as Record<string, unknown>
  const normalized: Record<string, unknown> = Object.create(null)
  Object.keys(source).sort().forEach(key => {
    const item = canonicalJSONValue(source[key])
    if (item !== undefined) normalized[key] = item
  })
  return normalized
}

function fallbackFingerprint(value: string): string {
  let left = 0x811c9dc5
  let right = 0x9e3779b9
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    left = Math.imul(left ^ code, 0x01000193)
    right = Math.imul(right ^ code, 0x85ebca6b)
    right ^= right >>> 13
  }
  const part = (input: number) => (input >>> 0).toString(16).padStart(8, '0')
  return `fallback:${value.length.toString(16)}:${part(left)}${part(right)}`
}

export async function fingerprintContent(payload: unknown): Promise<string> {
  const canonical = JSON.stringify(canonicalJSONValue(payload)) ?? 'null'
  const subtle = globalThis.crypto?.subtle
  if (subtle && typeof TextEncoder !== 'undefined') {
    try {
      const digest = await subtle.digest('SHA-256', new TextEncoder().encode(canonical))
      return `sha256:${Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('')}`
    } catch {
      // The deterministic fallback preserves retry behavior in restricted WebViews.
    }
  }
  return fallbackFingerprint(canonical)
}


// Persisted edit identities must remain comparable across HTTP, HTTPS and desktop
// contexts, including environments without WebCrypto. This is not an approval hash.
export async function fingerprintPortableContent(payload: unknown): Promise<string> {
  return fallbackFingerprint(JSON.stringify(canonicalJSONValue(payload)) ?? 'null')
}
