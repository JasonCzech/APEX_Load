import type {
  CorrelationRepairSuggestion,
  CorrelationStep,
  ScriptActionDefinition,
  ScriptDefinition,
  ScriptExtractorDefinition,
} from '../types/scripts'
import { headerListToCompatibilityMap } from './scriptActionHelpers'

export interface CorrelationRepairLocation {
  field: string
  start: number
  end: number
}

export interface CorrelationExtractorRepair {
  failingActionIndex: number
  requestLocations: CorrelationRepairLocation[]
  replacement: string
}

export interface CorrelationApplyResult {
  extractorAdded: boolean
  extractorAvailable: boolean
  requestRepaired: boolean
}

export function repairLocationsFromSuggestion(
  suggestion: CorrelationRepairSuggestion,
  replay: CorrelationStep[],
): CorrelationRepairLocation[] {
  if (typeof suggestion.failing_step_id !== 'number' || !replay.some(candidate => candidate.id === suggestion.failing_step_id)) return []
  const locations: CorrelationRepairLocation[] = []
  for (const location of suggestion.repair_locations || []) {
    const isRequestField = location.field === 'request_url' || location.field === 'request_body' || location.field.startsWith('request_headers.')
    // A suggestion is one repair transaction. If even one requested location
    // is unproven or malformed, do not apply a "safe" subset and leave another
    // stale use behind.
    if (location.editable_surface_exact !== true || !isRequestField || !Number.isInteger(location.start) || !Number.isInteger(location.end) || location.start < 0 || location.end <= location.start) return []
    if (!locations.some(candidate => candidate.field === location.field && candidate.start === location.start && candidate.end === location.end)) {
      locations.push({ field: location.field, start: location.start, end: location.end })
    }
  }
  return locations
}

function replaceRequestLocations(
  value: string,
  locations: Array<Pick<CorrelationRepairLocation, 'start' | 'end'>>,
  replacement: string,
): { value: string; changed: boolean; valid: boolean } {
  const unique = new Map<string, Pick<CorrelationRepairLocation, 'start' | 'end'>>()
  for (const location of locations) {
    if (!Number.isInteger(location.start) || !Number.isInteger(location.end) || location.start < 0 || location.end <= location.start || location.end > value.length) {
      return { value, changed: false, valid: false }
    }
    unique.set(`${location.start}:${location.end}`, location)
  }
  const ordered = [...unique.values()].sort((left, right) => right.start - left.start || right.end - left.end)
  if (ordered.length === 0) return { value, changed: false, valid: false }

  // Overlapping ranges are ambiguous. Fail the entire surface closed instead
  // of partially rewriting a request with shifted or conflicting offsets.
  for (let index = 1; index < ordered.length; index++) {
    if (ordered[index].end > ordered[index - 1].start) return { value, changed: false, valid: false }
  }

  let next = value
  for (const location of ordered) {
    next = next.slice(0, location.start) + replacement + next.slice(location.end)
  }
  return { value: next, changed: next !== value, valid: true }
}

export function applyCorrelationRepairToAction(action: ScriptActionDefinition, repair: CorrelationExtractorRepair): ScriptActionDefinition {
  const locations = repair.requestLocations || []
  if (locations.length === 0) return action

  // Build and validate every prospective edit before committing any of them.
  // A server response can contain several spans across several request fields;
  // applying only the valid subset would leave a request that looks repaired
  // but still sends one or more stale values.
  if (locations.some(location => location.field !== 'request_url' &&
    location.field !== 'request_body' &&
    !location.field.startsWith('request_headers.'))) return action

  const locationsFor = (field: string) => locations.filter(location => location.field === field)
  let nextTarget = action.target
  let nextBody = action.body
  let nextHeaders = action.headers
  let nextHeaderList = action.header_list
  let changed = false

  const urlLocations = locationsFor('request_url')
  if (urlLocations.length > 0) {
    if (typeof action.target !== 'string') return action
    const replaced = replaceRequestLocations(action.target, urlLocations, repair.replacement)
    if (!replaced.valid) return action
    nextTarget = replaced.value
    changed ||= replaced.changed
  }

  const bodyLocations = locationsFor('request_body')
  if (bodyLocations.length > 0) {
    if (typeof action.body !== 'string') return action
    const replaced = replaceRequestLocations(action.body, bodyLocations, repair.replacement)
    if (!replaced.valid) return action
    nextBody = replaced.value
    changed ||= replaced.changed
  }

  const headerLocations = locations.filter(location => location.field.startsWith('request_headers.'))
  const locationsByHeader = new Map<string, CorrelationRepairLocation[]>()
  for (const location of headerLocations) {
    // The server aggregates repeated headers by the trimmed, case-folded name
    // (rawAuthoredReplayRequestHeaders): entries whose names differ only in
    // surrounding whitespace share one aggregate. Normalize identically here,
    // otherwise the per-occurrence offset windows drift from the server's and
    // a range can map cleanly into the wrong occurrence.
    const name = location.field.slice('request_headers.'.length).trim().toLowerCase()
    if (!name) return action
    const headerRanges = locationsByHeader.get(name) || []
    headerRanges.push(location)
    locationsByHeader.set(name, headerRanges)
  }

  if (locationsByHeader.size > 0) {
    const headers = action.headers ? { ...action.headers } : undefined
    const headerList = action.header_list ? [...action.header_list] : undefined
    let headersChanged = false
    let headerListChanged = false

    for (const [headerName, headerRanges] of locationsByHeader) {
      const matchingIndexes = (headerList || [])
        .map((header, index) => header.name.trim().toLowerCase() === headerName ? index : -1)
        .filter(index => index >= 0)

      if (matchingIndexes.length > 0 && headerList) {
        let offset = 0
        const rangesByIndex = new Map<number, Array<Pick<CorrelationRepairLocation, 'start' | 'end'>>>()
        let mappedRangeCount = 0
        for (const index of matchingIndexes) {
          const value = headerList[index].value
          const start = offset
          const end = start + value.length
          for (const range of headerRanges) {
            if (range.start >= start && range.end <= end) {
              const localRanges = rangesByIndex.get(index) || []
              localRanges.push({ start: range.start - start, end: range.end - start })
              rangesByIndex.set(index, localRanges)
              mappedRangeCount += 1
            }
          }
          offset = end + 1 // repeated wire observations are newline-delimited
        }
        // A range in the delimiter or outside the aggregate cannot be mapped
        // back to an authored header entry safely.
        if (mappedRangeCount !== headerRanges.length) return action
        for (const [index, localRanges] of rangesByIndex) {
          const replaced = replaceRequestLocations(headerList[index].value, localRanges, repair.replacement)
          if (!replaced.valid) return action
          if (replaced.changed) {
            headerList[index] = { ...headerList[index], value: replaced.value }
            headerListChanged = true
          }
        }
        continue
      }

      const matchingHeaderKeys = Object.keys(headers || {}).filter(name => name.trim().toLowerCase() === headerName)
      if (!headers || matchingHeaderKeys.length !== 1) return action
      const key = matchingHeaderKeys[0]
      const replaced = replaceRequestLocations(headers[key], headerRanges, repair.replacement)
      if (!replaced.valid) return action
      if (replaced.changed) {
        headers[key] = replaced.value
        headersChanged = true
      }
    }

    if (headerListChanged && headerList) {
      nextHeaders = headerListToCompatibilityMap(headerList, headers)
    } else if (headersChanged) nextHeaders = headers
    if (headerListChanged) nextHeaderList = headerList
    changed ||= headersChanged || headerListChanged
  }

  if (!changed) return action
  return {
    ...action,
    target: nextTarget,
    ...(nextBody !== action.body ? { body: nextBody } : {}),
    ...(nextHeaders !== action.headers ? { headers: nextHeaders } : {}),
    ...(nextHeaderList !== action.header_list ? { header_list: nextHeaderList } : {}),
  }
}

export function addCorrelationExtractorIfMissing(
  action: ScriptActionDefinition,
  extractor: ScriptExtractorDefinition,
): ScriptActionDefinition {
  if ((action.extractors || []).some(existing => existing.name === extractor.name)) return action
  return { ...action, extractors: [...(action.extractors || []), extractor] }
}

function sameExtractorIdentity(left: ScriptExtractorDefinition, right: ScriptExtractorDefinition): boolean {
  return left.source === right.source &&
    left.type === right.type &&
    left.expression === right.expression &&
    (left.convert || '') === (right.convert || '') &&
    (left.on_miss || '') === (right.on_miss || '') &&
    (left.save_offset || 0) === (right.save_offset || 0) &&
    (left.save_length || 0) === (right.save_length || 0) &&
    (left.mode || '') === (right.mode || '')
}

export function resolveCorrelationExtractorName(
  sourceAction: ScriptActionDefinition,
  scope: ScriptActionDefinition[],
  requested: ScriptExtractorDefinition,
): ScriptExtractorDefinition {
  const occupants = (name: string) => scope.flatMap(action =>
    (action.extractors || [])
      .filter(existing => existing.name === name)
      .map(existing => ({ action, extractor: existing })),
  )
  const canUse = (name: string) => {
    const matches = occupants(name)
    return matches.length === 0 || matches.every(match =>
      match.action === sourceAction && sameExtractorIdentity(match.extractor, requested),
    )
  }

  if (canUse(requested.name)) return requested

  for (let suffix = 2; ; suffix++) {
    const name = `${requested.name}_${suffix}`
    if (canUse(name)) return { ...requested, name }
  }
}

// http_sequence actions execute full child actions from params.requests
// (engine httpSequenceChildActions); those children can define their own
// extractors and share the VUser variable namespace with every other action.
function httpSequenceChildScopeActions(action: ScriptActionDefinition): ScriptActionDefinition[] {
  if (action.type !== 'http_sequence') return []
  const requests = action.params?.requests
  if (!Array.isArray(requests)) return []
  return requests.filter((entry): entry is ScriptActionDefinition => typeof entry === 'object' && entry !== null)
}

// Extractor values share one VUser variable namespace, including actions nested
// under control-flow steps, else branches, and http_sequence child requests.
// Return the original action objects so source-action identity remains
// meaningful during conflict checks.
export function correlationExtractorScope(
  script: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'>,
): ScriptActionDefinition[] {
  const scope: ScriptActionDefinition[] = []
  const pending = [...(script.init || []), ...script.actions, ...(script.teardown || [])].reverse()

  while (pending.length > 0) {
    const action = pending.pop()!
    scope.push(action)
    const nested = [...(action.control?.steps || []), ...(action.control?.else || []), ...httpSequenceChildScopeActions(action)]
    for (let index = nested.length - 1; index >= 0; index--) pending.push(nested[index])
  }

  return scope
}

// The diagnosis UI stores this exact action snapshot when a replay starts and
// compares it with the current editable draft before allowing raw-offset repair.
// Keep configuration and metadata out: only the surfaces repair can mutate and
// the action identity/order that maps replay step ids are relevant.
export function correlationEditableActionsKey(script: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'>): string {
  return JSON.stringify({
    init: script.init || [],
    actions: script.actions || [],
    teardown: script.teardown || [],
  })
}

export function currentCorrelationEditableActionsKey(
  script: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'>,
  jsonDirty: boolean,
  jsonParseMessage: string | null,
): string | null {
  // Structured repairs update `script`, not an unsaved JSON editor buffer.
  // Even a syntactically valid dirty draft must therefore fail closed.
  if (jsonDirty || jsonParseMessage) return null
  return correlationEditableActionsKey(script)
}

export function resolveCorrelationExtractorAndRepair(
  sourceAction: ScriptActionDefinition,
  scope: ScriptActionDefinition[],
  requested: ScriptExtractorDefinition,
  repair?: CorrelationExtractorRepair,
): { extractor: ScriptExtractorDefinition; repair?: CorrelationExtractorRepair } {
  const extractor = resolveCorrelationExtractorName(sourceAction, scope, requested)
  return {
    extractor,
    repair: repair && extractor.name !== requested.name
      ? { ...repair, replacement: `{{var.${extractor.name}}}` }
      : repair,
  }
}
