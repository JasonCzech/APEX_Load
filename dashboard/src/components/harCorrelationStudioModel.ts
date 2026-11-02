import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'

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

export function flattenScriptActions(script: ScriptDefinition): FlattenedActionEntry[] {
  const entries: FlattenedActionEntry[] = []
  let seq = 0

  const walk = (list: ScriptActionDefinition[] | undefined, section: 'Init' | 'Actions' | 'Teardown', prefix: string) => {
    if (!Array.isArray(list)) return
    list.forEach((action, index) => {
      if (!action || typeof action !== 'object') return
      const currentPath = `${prefix}[${index}]`
      const params = action.params as Record<string, unknown> | undefined
      const subRequests = params && Array.isArray(params.requests) ? (params.requests as ScriptActionDefinition[]) : null
      if (subRequests && subRequests.length > 0) {
        subRequests.forEach((subReq, subIdx) => {
          if (!subReq || typeof subReq !== 'object') return
          seq += 1
          entries.push({
            section,
            path: `${currentPath}.params.requests[${subIdx}]`,
            seq,
            action: subReq,
          })
        })
      } else if (!action.control) {
        seq += 1
        entries.push({ section, path: currentPath, seq, action })
      }
      if (action.control && typeof action.control === 'object') {
        const ctrl = action.control as unknown as Record<string, unknown>
        if (Array.isArray(ctrl.steps)) walk(ctrl.steps as ScriptActionDefinition[], section, `${currentPath}.control.steps`)
        if (Array.isArray(ctrl.else)) walk(ctrl.else as ScriptActionDefinition[], section, `${currentPath}.control.else`)
        if (Array.isArray(ctrl.default)) walk(ctrl.default as ScriptActionDefinition[], section, `${currentPath}.control.default`)
        if (Array.isArray(ctrl.cases)) {
          ;(ctrl.cases as Array<{ steps?: ScriptActionDefinition[] }>).forEach((branch, branchIdx) => {
            if (Array.isArray(branch?.steps)) {
              walk(branch.steps, section, `${currentPath}.control.cases[${branchIdx}].steps`)
            }
          })
        }
      }
    })
  }

  walk(script.init, 'Init', 'init')
  walk(script.actions, 'Actions', 'actions')
  walk(script.teardown, 'Teardown', 'teardown')
  return entries
}

export function findBlockedUnexportedBodyActions(script: ScriptDefinition | null | undefined): BlockedHarActionItem[] {
  if (!script) return []
  const items: BlockedHarActionItem[] = []
  for (const entry of flattenScriptActions(script)) {
    const meta = entry.action.metadata as Record<string, unknown> | undefined
    if (!meta) continue
    const blockers = Array.isArray(meta.execution_blockers) ? meta.execution_blockers : []
    const findings = Array.isArray(meta.import_review_findings) ? meta.import_review_findings : []
    const hasCode = meta.har_request_body_not_exported === true
      || blockers.includes('har_request_body_not_exported')
      || findings.some(f => f && typeof f === 'object' && (f as Record<string, unknown>).code === 'har_request_body_not_exported')
    if (!hasCode) continue
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
