export interface SOAPFaultSummary {
  version: '1.1' | '1.2' | 'unknown'
  code: string
  reason: string
}

export function formatSOAPFaultSummary(summary: SOAPFaultSummary): string {
  return `SOAP ${summary.version}\nCode: ${summary.code}\nReason: ${summary.reason}`
}

export function extractSOAPFaultSummaryFromBody(body?: string): SOAPFaultSummary | null {
  if (!body || !body.includes('Fault')) return null

  const soap11Code = body.match(/<faultcode>\s*([^<]+)\s*<\/faultcode>/i)?.[1]?.trim()
  const soap11Reason = body.match(/<faultstring>\s*([^<]+)\s*<\/faultstring>/i)?.[1]?.trim()
  if (soap11Code && soap11Reason) {
    return { version: '1.1', code: soap11Code, reason: soap11Reason }
  }

  const soap12Code = body.match(/<[^>]*:Value>\s*([^<]+)\s*<\/[^>]*:Value>/i)?.[1]?.trim()
    || body.match(/<Value>\s*([^<]+)\s*<\/Value>/i)?.[1]?.trim()
  const soap12Reason = body.match(/<[^>]*:Text\b[^>]*>\s*([^<]+)\s*<\/[^>]*:Text>/i)?.[1]?.trim()
    || body.match(/<Text\b[^>]*>\s*([^<]+)\s*<\/Text>/i)?.[1]?.trim()
  if (soap12Code && soap12Reason) {
    return { version: '1.2', code: soap12Code, reason: soap12Reason }
  }

  return null
}