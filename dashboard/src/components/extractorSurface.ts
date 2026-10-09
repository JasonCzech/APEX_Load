// headersAndBodySurface mirrors the HTTP runtime's "all" extractor surface:
// header lines ending in CRLF, a blank line, then the body follows (the caller
// appends it). It matches LoadRunner's default search scope.
export function headersAndBodySurface(headers?: Record<string, string>): string {
  if (!headers) return '\r\n'
  return Object.entries(headers).map(([key, value]) => `${key}: ${value}\r\n`).join('') + '\r\n'
}
