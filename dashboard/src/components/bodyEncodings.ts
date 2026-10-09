// Request body wire encodings the HTTP runtime understands (DSL body_encoding).
export const BODY_ENCODING_OPTIONS: ReadonlyArray<{ value: string; label: string; note: string }> = [
  { value: '', label: 'Plain text', note: '' },
  { value: 'base64', label: 'Base64-encoded wire bytes', note: 'This text is standard Base64. The HTTP runtime decodes it before sending the request body.' },
  { value: 'c_escaped', label: 'C-escaped bytes (\\xNN)', note: 'Escapes such as \\x00, \\n and \\\\ name raw bytes. The runtime resolves parameters first, sends their values unchanged, then decodes the escapes (LoadRunner BodyBinary).' },
  { value: 'utf-16le', label: 'UTF-16LE text', note: 'The runtime resolves parameters, then sends the text as UTF-16 little-endian without a byte-order mark (LoadRunner BodyUnicode).' },
  { value: 'windows-1252', label: 'Windows-1252 text', note: 'The runtime resolves parameters, then sends the text in the Windows-1252 code page. Characters outside it fail the request.' },
]

export function normalizedBodyEncoding(encoding?: string): string {
  return (encoding || '').trim().toLowerCase()
}

export function knownBodyEncoding(encoding?: string) {
  const normalized = normalizedBodyEncoding(encoding)
  return BODY_ENCODING_OPTIONS.find(option => option.value === normalized)
}

// bodyEncodingLabel names the encoding for summaries; an unknown value is
// shown as authored.
export function bodyEncodingLabel(encoding?: string): string {
  return knownBodyEncoding(encoding)?.label || `Unknown encoding: ${encoding}`
}
