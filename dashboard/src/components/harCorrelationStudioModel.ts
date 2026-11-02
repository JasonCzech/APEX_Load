import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { importActionEntries } from './importReviewIssues'
import { actionExecutionBlockers } from './scriptActionHelpers'

export const HAR_REQUEST_BODY_NOT_EXPORTED = 'har_request_body_not_exported'

export interface BlockedHarActionItem {
  uid: string
  path: string
  section: 'Init' | 'Actions' | 'Teardown'
  name: string
  recordedBytes?: number
}

export interface FlattenedActionEntry {
  section: 'Init' | 'Actions' | 'Teardown'
  path: string
  seq: number
  action: ScriptActionDefinition
}

function sectionForPath(path: string): FlattenedActionEntry['section'] {
  if (path.startsWith('init[')) return 'Init'
  if (path.startsWith('teardown[')) return 'Teardown'
  return 'Actions'
}

// Containers (an HTTP sequence or a control step) send no request of their
// own, so the studio counts and searches only the requests inside them.
function isRequestContainer(action: ScriptActionDefinition): boolean {
  const requests = action.params?.requests
  return Boolean(action.control) || (Array.isArray(requests) && requests.length > 0)
}

// The executable requests of a script in replay order, from the same walk the
// Author and Debug problem lists use, so every surface counts the same steps.
export function flattenScriptActions(script: ScriptDefinition): FlattenedActionEntry[] {
  let seq = 0
  return importActionEntries(script)
    .filter(entry => !isRequestContainer(entry.action))
    .map(entry => ({ section: sectionForPath(entry.path), path: entry.path, seq: ++seq, action: entry.action }))
}

// The server marks a request whose body the browser left out of the HAR export
// with this execution blocker (pkg/convert/sample).
export function hasUnexportedBodyBlocker(action: ScriptActionDefinition | undefined): boolean {
  return Boolean(action) && actionExecutionBlockers(action!).includes(HAR_REQUEST_BODY_NOT_EXPORTED)
}

// The HAR entry a converted request came from, stamped at interactive import.
export function harEntryIndex(action: ScriptActionDefinition | undefined): number | undefined {
  const value = action?.metadata?.['har.entry_index']
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : undefined
}

export function findBlockedUnexportedBodyActions(script: ScriptDefinition | null | undefined): BlockedHarActionItem[] {
  if (!script) return []
  const items: BlockedHarActionItem[] = []
  for (const entry of flattenScriptActions(script)) {
    if (!hasUnexportedBodyBlocker(entry.action)) continue
    const meta = entry.action.metadata || {}
    const uid = typeof meta.import_action_uid === 'string' ? meta.import_action_uid.trim() : ''
    const rawLen = meta['har.request.recorded_content_length']
    const recordedBytes = typeof rawLen === 'number' && Number.isFinite(rawLen) && rawLen > 0 ? rawLen : undefined
    items.push({
      uid,
      path: entry.path,
      section: entry.section,
      name: entry.action.name || entry.action.target || entry.path,
      recordedBytes,
    })
  }
  return items
}
