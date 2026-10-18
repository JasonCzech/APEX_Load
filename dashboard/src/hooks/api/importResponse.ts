// Negotiation is per request so older controllers and external clients keep
// working. The compact wire contract carries drafts once in scripts[].
export function canonicalImportURL(path: string): string {
  return `${path}${path.includes('?') ? '&' : '?'}response_format=canonical-v1`
}

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function hasOwn(value: object, key: PropertyKey): boolean {
  return Object.prototype.hasOwnProperty.call(value, key)
}

function diagnosticText(value: unknown): string {
  if (!isObject(value)) return ''
  let prefix = typeof value.element === 'string' && value.element.trim() ? value.element.trim() : 'import element'
  if (typeof value.count === 'number' && value.count > 1) {
    prefix += ` (${value.count} occurrences`
    if (Array.isArray(value.locations) && value.locations.length) prefix += `; examples: ${value.locations.join(', ')}`
    prefix += ')'
  } else if (value.location) prefix += ` at ${value.location}`
  return `${prefix} [${value.severity}]${value.detail ? `: ${value.detail}` : ''}`
}

// Restore UI convenience fields as references to the canonical values. No
// script body is copied, and older controller responses are returned unchanged.
export function restoreImportAliases<T>(value: T): T {
  if (!isObject(value)) return value
  if (value.archive_collection === true && Array.isArray(value.archives)) {
    return { ...value, archives: value.archives.map(item => isObject(item) && item.response
      ? { ...item, response: restoreImportAliases(item.response) } : item) }
  }
  if (value.response_format !== 'canonical-v1' || !Array.isArray(value.scripts)) return value
  const result: Record<string, unknown> = { ...value }
  const index = value.primary_script_index
  if (typeof index === 'number' && Number.isInteger(index) && index >= 0 && index < value.scripts.length) {
    result.script = value.scripts[index]
  }
  if (value.unsupported_from_diagnostics === true && Array.isArray(value.diagnostics)) {
    result.unsupported_elements = value.diagnostics.map(diagnosticText)
  }
  const previewIndex = value.salesforce_preview_script_index
  if (typeof previewIndex === 'number' && Number.isInteger(previewIndex) && previewIndex >= 0 && previewIndex < value.scripts.length
    && isObject(value.summary) && isObject(value.summary.salesforce_review) && isObject(value.summary.salesforce_review.native_preview)
    && !hasOwn(value.summary.salesforce_review.native_preview, 'script')) {
    result.summary = { ...value.summary, salesforce_review: { ...value.summary.salesforce_review,
      native_preview: { ...value.summary.salesforce_review.native_preview, script: value.scripts[previewIndex] } } }
  }
  if (isObject(value.result) && isObject(value.result_aliases)) {
    const facts = { ...value.result }
    for (const [key, target] of Object.entries(value.result_aliases)) {
      if (typeof target === 'string' && hasOwn(result, target) && !hasOwn(facts, key)) {
        Object.defineProperty(facts, key, { value: result[target], enumerable: true, configurable: true, writable: true })
      }
    }
    result.result = facts
  }
  return result as T
}
