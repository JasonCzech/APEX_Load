import { analysisProtectedIdentityColumn } from './analysisCredentialColumn'
import type { AnalysisIdentityFilters, AnalysisIdentityMapping, AnalysisIdentitySelection } from '../types/analysis'

// Count UTF-8 bytes without encoding/copying or replacing lone surrogates. Scalar
// limits are UTF-8 bytes, matching the authoritative mapping/query contracts.
export function analysisIdentityTextError(value: string, limit: number, required = false): string | undefined {
  if (typeof value !== 'string') return 'Identity fields must be text.'
  if (required && value.length === 0) return 'A label name is required.'
  if (value.length > limit) return `Identity text must contain at most ${limit} UTF-8 bytes.`
  if (value.includes('\0')) return 'Identity text cannot contain a NUL character.'
  let bytes = 0
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i)
    if (code >= 0xd800 && code <= 0xdbff) {
      const low = value.charCodeAt(++i)
      if (!(low >= 0xdc00 && low <= 0xdfff)) return 'Identity text must contain valid Unicode.'
      bytes += 4
    } else if (code >= 0xdc00 && code <= 0xdfff) return 'Identity text must contain valid Unicode.'
    else bytes += code <= 0x7f ? 1 : code <= 0x7ff ? 2 : 3
    if (bytes > limit) return `Identity text must contain at most ${limit} UTF-8 bytes.`
  }
}

function columnError(path: string[]): string | undefined {
  if (!Array.isArray(path) || !path.length || path.length > 16) return 'Choose a scalar column with at most 16 path components.'
  for (const part of path) {
    const error = analysisIdentityTextError(part, 256, true)
    if (error) return error
  }
  if (analysisProtectedIdentityColumn(path)) return 'Protected credential columns cannot become analytical identity labels.'
}

// Structural admission precedes JSX iteration/serialization. Empty editable
// names and paths are allowed here; semantic errors remain editable below.
export function analysisIdentityMappingShapeError(identity?: AnalysisIdentityMapping): string | undefined {
  if (identity === undefined) return
  if (!identity || typeof identity !== 'object' || Array.isArray(identity)) return 'Identity mapping must be an object.'
  if (identity.dimensions !== undefined && !Array.isArray(identity.dimensions)) return 'Dimensions must be a list.'
  if ((identity.dimensions?.length ?? 0) > 16) return 'Map at most 16 dimensions.'
  for (const path of [identity.source, identity.host, identity.service]) {
    if (path !== undefined && (!Array.isArray(path) || path.length > 16)) return 'Identity paths must be lists with at most 16 components.'
    for (const part of path ?? []) { const error = analysisIdentityTextError(part, 256, true); if (error) return error }
  }
  for (const dimension of identity.dimensions ?? []) {
    if (!dimension || typeof dimension !== 'object' || Array.isArray(dimension)) return 'Dimension mappings must be objects.'
    const error = analysisIdentityTextError(dimension.name, 128)
    if (error) return error
    if (!Array.isArray(dimension.value) || dimension.value.length > 16) return 'Identity paths must be lists with at most 16 components.'
    for (const part of dimension.value) { const error = analysisIdentityTextError(part, 256, true); if (error) return error }
  }
}

export function analysisIdentityFilterShapeError(filters: AnalysisIdentityFilters): string | undefined {
  if (!filters || typeof filters !== 'object' || Array.isArray(filters)) return 'Identity filters must be an object.'
  if (!Array.isArray(filters.dimensions) || filters.dimensions.length > 16) return 'Select at most 16 dimension filters.'
  for (const text of [filters.source_id, filters.host, filters.service]) { const error = analysisIdentityTextError(text, 256); if (error) return error }
  for (const dimension of filters.dimensions) {
    if (!dimension || typeof dimension !== 'object' || Array.isArray(dimension)) return 'Dimension filters must be objects.'
    const error = analysisIdentityTextError(dimension.name, 128) ?? analysisIdentityTextError(dimension.value, 128)
    if (error) return error
  }
}

export function analysisIdentityMappingError(identity?: AnalysisIdentityMapping): string | undefined {
  const shape = analysisIdentityMappingShapeError(identity)
  if (shape) return shape
  if (identity === undefined) return
  for (const path of [identity.source, identity.host, identity.service]) {
    if (path !== undefined && (!Array.isArray(path) || path.length)) { const error = columnError(path); if (error) return error }
  }
  const seen = new Set<string>()
  for (const dimension of identity.dimensions ?? []) {
    if (!dimension || typeof dimension !== 'object') return 'Dimension mappings must be objects.'
    const error = analysisIdentityTextError(dimension.name, 128, true) ?? columnError(dimension.value)
    if (error) return error
    if (seen.has(dimension.name)) return 'Dimension names must be unique.'
    seen.add(dimension.name)
  }
}

// Exact Go Unicode White Space membership. U+FEFF is intentionally retained.
export function analysisIdentityIsGoWhitespace(text: string): boolean {
  for (const char of text) {
    const point = char.codePointAt(0)!
    if (!(point >= 0x09 && point <= 0x0d || point === 0x20 || point === 0x85 || point === 0xa0 || point === 0x1680 || point >= 0x2000 && point <= 0x200a || point === 0x2028 || point === 0x2029 || point === 0x202f || point === 0x205f || point === 0x3000)) return false
  }
  return true
}

export const emptyAnalysisIdentityFilters = (): AnalysisIdentityFilters => ({ source_id: '', host: '', service: '', dimensions: [] })

export function buildAnalysisIdentitySelection(filters: AnalysisIdentityFilters): AnalysisIdentitySelection {
  const shape = analysisIdentityFilterShapeError(filters)
  if (shape) throw new Error(shape)
  for (const text of [filters.source_id, filters.host, filters.service]) {
    const error = analysisIdentityTextError(text, 256)
    if (error) throw new Error(error)
    // Do not trim identities: filters select exact source values.
    if (text && analysisIdentityIsGoWhitespace(text)) throw new Error('An identity filter must contain nonblank text.')
  }
  const seen = new Set<string>()
  for (const dimension of filters.dimensions) {
    if (!dimension || typeof dimension !== 'object') throw new Error('Dimension filters must be objects.')
    const error = analysisIdentityTextError(dimension.name, 128, true) ?? analysisIdentityTextError(dimension.value, 128)
    if (error) throw new Error(error)
    if (seen.has(dimension.name)) throw new Error('Dimension filter names must be unique.')
    seen.add(dimension.name)
  }
  return {
    ...(filters.source_id ? { source_id: filters.source_id } : {}),
    ...(filters.host ? { host: filters.host } : {}),
    ...(filters.service ? { service: filters.service } : {}),
    // Object.fromEntries treats special keys as data properties. Empty values
    // remain present and are not confused with an absent filter.
    ...(filters.dimensions.length ? { dimensions: Object.fromEntries(filters.dimensions.map(row => [row.name, row.value])) } : {}),
  }
}

export function analysisIdentityFilterError(filters: AnalysisIdentityFilters): string | undefined {
  try { buildAnalysisIdentitySelection(filters) } catch (error) { return error instanceof Error ? error.message : String(error) }
}

export function restoreAnalysisIdentityFilters(selection: AnalysisIdentitySelection): AnalysisIdentityFilters {
  if (!selection || typeof selection !== 'object' || Array.isArray(selection)) throw new Error('Saved identity selection must be an object.')
  const raw: Record<string, string> = selection.dimensions === undefined ? {} : selection.dimensions
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) throw new Error('Saved dimension filters must be an object.')
  let count = 0
  for (const key in raw) if (Object.prototype.hasOwnProperty.call(raw, key) && ++count > 16) throw new Error('Saved dimension filters exceed the supported limit.')
  const dimensions = Object.entries(raw)
  const filters = { source_id: selection.source_id === undefined ? '' : selection.source_id, host: selection.host === undefined ? '' : selection.host, service: selection.service === undefined ? '' : selection.service, dimensions: dimensions.map(([name, value]) => ({ name, value })) }
  buildAnalysisIdentitySelection(filters)
  return filters
}

// Imported comparison aliases select exact named dimensions on the controller.
// Fold them into the editable dimension rows so reopening a saved definition
// cannot broaden its population. Managed-run selectors keep their own fields.
export function restoreAnalysisImportedIdentityFilters(selection: AnalysisIdentitySelection & { transaction?: string; protocol?: string; group?: string }): AnalysisIdentityFilters {
  const filters = restoreAnalysisIdentityFilters(selection)
  for (const field of ['transaction', 'protocol', 'group'] as const) {
    const value = selection[field]
    if (value === undefined || value === '') continue
    const error = analysisIdentityTextError(value, field === 'protocol' ? 64 : 128)
    if (error) throw new Error(error)
    const existing = filters.dimensions.find(row => row.name === field)
    if (existing && existing.value !== value) throw new Error('Conflicting imported identity filters.')
    if (!existing) filters.dimensions.push({ name: field, value })
  }
  buildAnalysisIdentitySelection(filters)
  return filters
}

// Only an explicit finite peer receipt enables identity schema 2. Missing or
// malformed older-peer capability never lowers a retained mapping/query version.
export function analysisImportedIdentityAvailable(version: unknown): boolean {
  return typeof version === 'number' && Number.isSafeInteger(version) && version >= 2
}

// Presence is an explicit request for imported identity semantics, even when
// a label is empty or a dataset receipt omits its mapping version. Managed-run
// monitor selectors belong to their existing separate contract.
export function analysisImportedSelectorsRequested(selection: unknown): boolean {
  if (!selection || typeof selection !== 'object' || Array.isArray(selection)) return false
  const run = Object.getOwnPropertyDescriptor(selection, 'run_id')
  if (run && 'value' in run && typeof run.value === 'string' && run.value && !Object.prototype.hasOwnProperty.call(selection, 'dataset_id')) return false
  return ['source_id', 'host', 'service', 'dimensions', 'transaction', 'protocol', 'group'].some(field => Object.prototype.hasOwnProperty.call(selection, field))
}

// Read only the two finite metadata descriptors. Unknown accessors imply a
// requested identity boundary and are never executed during capability checks.
export function analysisMappingRequiresImportedIdentity(mapping: unknown): boolean {
  if (!mapping || typeof mapping !== 'object' || Array.isArray(mapping)) return false
  const version = Object.getOwnPropertyDescriptor(mapping, 'version')
  const identity = Object.getOwnPropertyDescriptor(mapping, 'identity')
  return !!identity && (!Object.prototype.hasOwnProperty.call(identity, 'value') || identity.value !== undefined) || !!version && (!Object.prototype.hasOwnProperty.call(version, 'value') || typeof version.value === 'number' && version.value >= 2)
}


// Inspect every selected source-value path, including version 1 mappings. The
// closed own projection never reads accessors or inherited optional fields and
// admits finite population/geometry before copying/classifying any path. Output
// metric/dimension names are data and are not credential-column selectors.
export function analysisMappingDisclosureError(mapping: unknown): string | undefined {
  const invalid = 'Mapped source columns must use finite scalar paths.'
  const protectedColumn = 'Protected credential columns cannot become analytical timestamps, measurement values, metric names or identity labels.'
  const protectedSentinel = Symbol('protected source column')
  if (mapping === undefined) return
  const plain = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value) && (Object.getPrototypeOf(value) === Object.prototype || Object.getPrototypeOf(value) === null)
  const own = (value: object, key: string) => {
    const descriptor = Object.getOwnPropertyDescriptor(value, key)
    if (descriptor === undefined) return undefined
    if (!Object.prototype.hasOwnProperty.call(descriptor, 'value')) throw new Error(invalid)
    return descriptor.value as unknown
  }
  const count = (value: unknown, maximum: number): number => {
    if (!Array.isArray(value) || Object.getPrototypeOf(value) !== Array.prototype) throw new Error(invalid)
    const length = own(value, 'length')
    if (typeof length !== 'number' || !Number.isSafeInteger(length) || length < 0 || length > maximum) throw new Error(invalid)
    return length
  }
  const path = (value: unknown): string[] => {
    if (value === undefined) return []
    const length = count(value, 16), selected: string[] = []
    for (let index = 0; index < length; index++) {
      const part = own(value as object, String(index))
      if (typeof part !== 'string' || analysisIdentityTextError(part, 256, true)) throw new Error(invalid)
      selected.push(part)
    }
    if (length && analysisProtectedIdentityColumn(selected)) throw protectedSentinel
    return selected
  }
  try {
    if (!plain(mapping)) return invalid
    path(own(mapping, 'timestamp'))
    const measures = own(mapping, 'measures')
    if (measures !== undefined) {
      const length = count(measures, 64)
      for (let index = 0; index < length; index++) {
        const measure = own(measures as object, String(index))
        if (!plain(measure)) return invalid
        path(own(measure, 'value')); path(own(measure, 'name_column')); path(own(measure, 'sample_count_column')); path(own(measure, 'sample_sum_column'))
        const histogram = own(measure, 'histogram')
        if (histogram !== undefined) {
          if (!plain(histogram)) return invalid
          path(own(histogram, 'bound_column'))
          const counts = own(histogram, 'count_columns')
          if (counts !== undefined) for (let column = 0, size = count(counts, 128); column < size; column++) path(own(counts as object, String(column)))
        }
      }
    }
    const identity = own(mapping, 'identity')
    if (identity === undefined) return
    if (!plain(identity)) return invalid
    const admitted: AnalysisIdentityMapping = {}
    for (const name of ['source', 'host', 'service'] as const) {
      const value = own(identity, name)
      if (value !== undefined) admitted[name] = path(value)
    }
    const dimensions = own(identity, 'dimensions')
    if (dimensions !== undefined) {
      const length = count(dimensions, 16)
      admitted.dimensions = []
      for (let index = 0; index < length; index++) {
        const dimension = own(dimensions as object, String(index))
        if (!plain(dimension)) return invalid
        const name = own(dimension, 'name')
        if (typeof name !== 'string' || analysisIdentityTextError(name, 128)) return invalid
        admitted.dimensions.push({ name, value: path(own(dimension, 'value')) })
      }
    }
    return analysisIdentityMappingError(admitted)
  } catch (error) { return error === protectedSentinel ? protectedColumn : invalid }
}

// Mapping format 3 adds precision consent and retains identity schema 2.
export function analysisMappingUsesIdentity2(mapping?: { version: number }): boolean {
  return mapping?.version === 2 || mapping?.version === 3
}
