import {
  type AuthoringComponent,
} from '../types'
import {
  type ScriptActionDefinition,
  type ScriptCondition,
  type ScriptConfig,
  type ScriptDataSource,
  type ScriptDefinition,
  type ScriptParameterDefinition,
} from '../types/scripts'
import { dslActionNeedsExplicitTarget } from './dslActionCatalog'
import { headerListToCompatibilityMap } from './scriptActionHelpers'
import { canonicalThinkTimeType, thinkTimeConfigForType } from './thinkTimeConfig'
import { formatMillisecondsAsSecondsDuration } from './runtimeTiming'

export type AuthoringIssueSeverity = 'error' | 'warning'
export type AuthoringIssuePhase = 'init' | 'action' | 'teardown'

export interface AuthoringIssue {
  id: string
  severity: AuthoringIssueSeverity
  message: string
  phase?: AuthoringIssuePhase
  actionIndex?: number
  stepPath?: string[]
}

export interface ActionTemplateDefinition {
  key: string
  label: string
  description: string
  action: ScriptActionDefinition
}

export {
  buildImportedDraftDiffSummary,
  type ImportedDraftDiffSummary,
} from './scriptEditorImportModel'

export interface MigrationAssistantSuggestion {
  key: 'normalize_action_names' | 'normalize_think_time' | 'apply_gentle_pacing' | 'apply_per_action_transactions'
  title: string
  description: string
  applyLabel: string
}

export interface MigrationAssistantResult {
  script: ScriptDefinition
  summary: string
}

export interface AuthoringComponentApplyResult {
  script: ScriptDefinition
  summary: string
}

export type ScriptRepairSuggestionKey =
  | 'parameterize_hard_coded_tokens'
  | 'create_missing_data_source'
  | 'add_default_assertions'
  | 'stabilize_browser_waits'
  | 'stabilize_browser_selectors'

export interface ScriptRepairSuggestion {
  key: ScriptRepairSuggestionKey
  title: string
  description: string
  applyLabel: string
}

export interface ScriptRepairResult {
  script: ScriptDefinition
  summary: string
}

const PARAM_TOKEN = /{{\s*param\.([a-zA-Z0-9_]+)\s*}}/g
const DATA_TOKEN = /{{\s*data\.([a-zA-Z0-9_]+)\.([a-zA-Z0-9_]+)\s*}}/g
const TOKENISH_KEY = /(access[_-]?token|auth[_-]?token|api[_-]?key|client[_-]?secret|password|passwd|bearer|session[_-]?id|csrf|xsrf|jwt)/i
const TOKENISH_JSON_KEY_SOURCE = String.raw`access[_-]?token|auth[_-]?token|api[_-]?key|client[_-]?secret|password|passwd|bearer|session[_-]?id|csrf|xsrf|jwt`

function hardCodedSensitiveJSONFieldPattern(global = false): RegExp {
  return new RegExp(
    String.raw`"([^"]*(?:${TOKENISH_JSON_KEY_SOURCE})[^"]*)"\s*:\s*"([^"]+)"`,
    global ? 'gi' : 'i',
  )
}

function hasHardCodedSensitiveJSONField(value: string): boolean {
  const pattern = hardCodedSensitiveJSONFieldPattern(true)
  let match = pattern.exec(value)
  while (match) {
    if (!match[2].includes('{{')) return true
    match = pattern.exec(value)
  }
  return false
}

function cloneScript<T>(value: T): T {
  if (value === undefined) return value
  return JSON.parse(JSON.stringify(value)) as T
}

function normalizeTokenName(value: string): string {
  return value.trim().toLowerCase()
}

function pushIssue(issues: AuthoringIssue[], issue: AuthoringIssue) {
  issues.push(issue)
}

function actionIssuePhasePrefix(phase: AuthoringIssuePhase): string {
  return phase === 'action' ? 'action' : phase
}

function scriptActionEntries(script: ScriptDefinition): Array<{ phase: AuthoringIssuePhase; actionIndex: number; action: ScriptActionDefinition }> {
  const entries: Array<{ phase: AuthoringIssuePhase; actionIndex: number; action: ScriptActionDefinition }> = []
  ;([
    ['init', script.init || []],
    ['action', script.actions || []],
    ['teardown', script.teardown || []],
  ] as Array<[AuthoringIssuePhase, ScriptActionDefinition[]]>).forEach(([phase, actions]) => {
    actions.forEach((action, actionIndex) => {
      entries.push({ phase, actionIndex, action })
    })
  })
  return entries
}

function collectActionStrings(action: ScriptActionDefinition): string[] {
  return [
    action.target || '',
    action.selector || '',
    action.value || '',
    action.body || '',
    ...Object.values(action.headers || {}),
    ...(action.header_list || []).map(header => header.value),
  ]
}

function normalizeActionName(value: string, fallback: string): string {
  const normalized = value
    .trim()
    .replace(/[^a-zA-Z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')
  return normalized || fallback
}

function buildUniqueActionName(baseName: string, seen: Set<string>): string {
  let nextName = baseName
  let suffix = 2
  while (seen.has(nextName.toLowerCase())) {
    nextName = `${baseName}_${suffix}`
    suffix += 1
  }
  seen.add(nextName.toLowerCase())
  return nextName
}

// Pre-catalog heuristic, used only while the action catalog has not loaded (or
// if GET /api/v1/dsl/actions never resolves). The excluded types carry no
// `target` field in the catalog (pkg/dsl/catalog_engine.go source-function/
// control/sequence specs, catalog_*.go health/transaction ops) — keep this list
// in sync so an importer-produced target-less action is never wrongly flagged
// "target required" during the catalog-load window.
function legacyActionNeedsExplicitTarget(actionType: string): boolean {
  if (actionType.startsWith('browser_')) {
    return actionType !== 'browser_screenshot'
  }
  return ![
    'jms_ack',
    'jms_commit',
    'jms_rollback',
    'db_begin',
    'db_commit',
    'db_rollback',
    'db_health',
    'database_begin',
    'database_commit',
    'database_rollback',
    'database_health',
    'network_health',
    // Importer-produced composite/source/control steps: targets live in
    // params.requests (http_sequence) or are absent by design (source_*,
    // custom_call, control flow, rendezvous, error_message, review_required).
    'http_sequence',
    'source_transaction',
    'source_runtime',
    'source_timer',
    'source_metric',
    'source_parameter',
    'source_message',
    'source_json',
    'source_xml',
    'source_database',
    'source_secret',
    'source_external',
    'source_review',
    'custom_call',
    'control_if',
	'control_switch',
    'control_loop',
	'control_parallel',
    'control_break',
    'control_continue',
    'rendezvous',
    'error_message',
    'review_required',
  ].includes(actionType)
}

function actionNeedsExplicitTarget(action: ScriptActionDefinition): boolean {
  return dslActionNeedsExplicitTarget(action.type || '', legacyActionNeedsExplicitTarget)
}

function deepMerge(base: Record<string, unknown>, patch?: Record<string, unknown>): Record<string, unknown> {
  if (!patch) return cloneScript(base)
  const next = cloneScript(base)
  Object.entries(patch).forEach(([key, value]) => {
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      const current = next[key]
      next[key] = deepMerge(
        current && typeof current === 'object' && !Array.isArray(current) ? current as Record<string, unknown> : {},
        value as Record<string, unknown>,
      )
      return
    }
    next[key] = cloneScript(value)
  })
  return next
}

function normalizedNames<T extends { name?: string | null }>(items?: T[]): Set<string> {
  return new Set((items || []).map(item => normalizeTokenName(item.name || '')).filter(Boolean))
}

function cloneActionForInsertion(action: ScriptActionDefinition, phase?: 'init' | 'teardown'): ScriptActionDefinition {
  const next = cloneScript(action)
  if (!phase) return next
  return {
    ...next,
    metadata: {
      ...(next.metadata || {}),
      phase,
    },
  }
}

function collectMissingDataReferences(script: ScriptDefinition): Map<string, Set<string>> {
  const knownSources = normalizedNames(script.data_sources || [])
  const missing = new Map<string, Set<string>>()

  scriptActionEntries(script).forEach(({ action }) => {
    collectActionStrings(action).forEach(value => {
      for (const match of value.matchAll(DATA_TOKEN)) {
        const sourceName = match[1] || ''
        const columnName = match[2] || ''
        if (!sourceName || knownSources.has(normalizeTokenName(sourceName))) continue
        if (!missing.has(sourceName)) missing.set(sourceName, new Set())
        if (columnName) missing.get(sourceName)?.add(columnName)
      }
    })
  })

  return missing
}

function isHttpLikeAction(action: ScriptActionDefinition): boolean {
  return Boolean(action.method || ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'request'].includes(action.type))
}

function isUnstableSelector(selector?: string): boolean {
  const value = selector?.trim()
  if (!value) return false
  return /nth-child|nth-of-type|>\s*[^>]+>|#[a-z]+_[a-z0-9]{8,}|#[a-z]+-\d{5,}/i.test(value)
}

function sanitizeRepairName(value: string, fallback: string): string {
  const normalized = value
    .trim()
    .replace(/{{.*?}}/g, '')
    .replace(/[^a-zA-Z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .toLowerCase()
  return normalized || fallback
}

// collectControlFlowIssues mirrors the server control-flow rules (validate.go):
// a control_loop needs a positive count, a condition, or a for_each source; a
// control_if needs then-steps or else-steps, while control_switch requires
// ordered, conditioned, non-empty cases. Nested control blocks are checked
// recursively and reported against the enclosing top-level action.
function collectControlFlowIssues(action: ScriptActionDefinition, phase: AuthoringIssuePhase, actionIndex: number, issues: AuthoringIssue[]): void {
  const conditionPresent = (condition?: ScriptCondition): boolean =>
    !!condition && (!!(condition.expr || '').trim() || !!(condition.left || '').trim())
  const phasePrefix = actionIssuePhasePrefix(phase)

  const recurse = (node: ScriptActionDefinition, pathKey: string, pathLabel: string, stepPath: string[] = []): void => {
    const emit = (issue: AuthoringIssue) => pushIssue(issues, { ...issue, ...(stepPath.length ? { stepPath } : {}) })
    if (stepPath.length > 32) { emit({ id: `${phasePrefix}:${actionIndex}:depth`, severity: 'error', phase, actionIndex, message: 'Control nesting exceeds the authoring limit of 32 levels.' }); return }
    if (stepPath.length) {
      if (!node.name?.trim()) emit({ id: `${phasePrefix}:${actionIndex}:${pathKey}:name`, severity: 'error', phase, actionIndex, message: 'Action name is required.' })
      if (actionNeedsExplicitTarget(node) && !node.target?.trim()) emit({ id: `${phasePrefix}:${actionIndex}:${pathKey}:target`, severity: 'error', phase, actionIndex, message: 'Action target is required for this protocol step.' })
    }
    const control = node.control || {}
    if (node.type === 'control_loop') {
      const hasForEachSource = !!(control.for_each && (control.for_each.source || '').trim())
      const hasCount = (control.count ?? 0) > 0
      if (!hasForEachSource && !hasCount && !conditionPresent(control.condition)) {
        emit({
          id: `${phasePrefix}:${actionIndex}:control:${pathKey}:loop-mode`,
          severity: 'error',
          phase,
          actionIndex,
          message: `${pathLabel}control_loop needs a count, a condition, or a for-each data source.`,
        })
      }
      if (control.for_each && !(control.for_each.source || '').trim()) {
        emit({
          id: `${phasePrefix}:${actionIndex}:control:${pathKey}:foreach-source`,
          severity: 'error',
          phase,
          actionIndex,
          message: `${pathLabel}for-each loop requires a data source.`,
        })
      }
    } else if (node.type === 'control_if') {
      const stepCount = (control.steps || []).length
      const elseCount = (control.else || []).length
      if (stepCount === 0 && elseCount === 0) {
        emit({
          id: `${phasePrefix}:${actionIndex}:control:${pathKey}:if-empty`,
          severity: 'error',
          phase,
          actionIndex,
          message: `${pathLabel}control_if needs then-steps or else-steps.`,
        })
      }
	} else if (node.type === 'control_switch') {
		const cases = control.cases || []
		if (cases.length === 0) {
			emit({
				id: `${phasePrefix}:${actionIndex}:control:${pathKey}:switch-empty`,
				severity: 'error', phase, actionIndex,
				message: `${pathLabel}control_switch needs at least one case.`,
			})
		}
		cases.forEach((switchCase, caseIndex) => {
			if (!conditionPresent(switchCase.condition)) {
				emit({
					id: `${phasePrefix}:${actionIndex}:control:${pathKey}:case-${caseIndex}-condition`,
					severity: 'error', phase, actionIndex,
					message: `${pathLabel}control_switch case ${caseIndex + 1} needs a condition.`,
				})
			}
			if ((switchCase.steps || []).length === 0) {
				emit({
					id: `${phasePrefix}:${actionIndex}:control:${pathKey}:case-${caseIndex}-steps`,
					severity: 'error', phase, actionIndex,
					message: `${pathLabel}control_switch case ${caseIndex + 1} needs at least one step.`,
				})
			}
		})
	} else if (node.type === 'control_parallel') {
		const branches = control.branches || []
		if (branches.length < 2) {
			emit({
				id: `${phasePrefix}:${actionIndex}:control:${pathKey}:parallel-branches`,
				severity: 'error', phase, actionIndex,
				message: `${pathLabel}control_parallel needs at least two named branches.`,
			})
		}
		const names = new Set<string>()
		branches.forEach((branch, branchIndex) => {
			const name = (branch.name || '').trim()
			if (!name || names.has(name) || (branch.steps || []).length === 0) {
				emit({
					id: `${phasePrefix}:${actionIndex}:control:${pathKey}:parallel-${branchIndex}`,
					severity: 'error', phase, actionIndex,
					message: `${pathLabel}parallel branch ${branchIndex + 1} needs a unique name and at least one step.`,
				})
			}
			names.add(name)
		})
    }
    const children: Array<{ action: ScriptActionDefinition; segment: string }> = [
      ...(control.steps || []).map((action, index) => ({ action, segment: `${node.type === 'control_loop' ? 'loop' : 'then'}[${index}]` })),
      ...(control.else || []).map((action, index) => ({ action, segment: `else[${index}]` })),
      ...(control.cases || []).flatMap((branch, branchIndex) => (branch.steps || []).map((action, index) => ({ action, segment: `case[${branchIndex}][${index}]` }))),
      ...(control.default || []).map((action, index) => ({ action, segment: `default[${index}]` })),
      ...(control.branches || []).flatMap((branch, branchIndex) => (branch.steps || []).map((action, index) => ({ action, segment: `branches[${branchIndex}].steps[${index}]` }))),
    ]
    children.forEach(({ action: child, segment }, childIndex) => {
      if (child) recurse(child, `${pathKey}.${childIndex}`, `${pathLabel}${child.name || `Step ${childIndex + 1}`} → `, [...stepPath, segment])
    })
  }

	if (action.type === 'control_if' || action.type === 'control_switch' || action.type === 'control_loop' || action.type === 'control_parallel') {
    recurse(action, '0', '')
  }
}

export function collectAuthoringIssues(script: ScriptDefinition): AuthoringIssue[] {
  const issues: AuthoringIssue[] = []
  const parameterNames = new Set((script.parameters || []).map((parameter) => normalizeTokenName(parameter.name || '')).filter(Boolean))
  const dataSources = new Set((script.data_sources || []).map((source) => normalizeTokenName(source.name || '')).filter(Boolean))
  const actionEntries = scriptActionEntries(script)

  if (!script.name.trim()) {
    pushIssue(issues, {
      id: 'script:name',
      severity: 'error',
      message: 'Script name is required before validation, replay, or save.',
    })
  }

  const authType = script.config.http?.auth?.type
  if ((authType === 'basic' || authType === 'ntlm') && (!script.config.http?.auth?.username || !script.config.http?.auth?.password)) {
    pushIssue(issues, {
      id: 'script:http-auth',
      severity: 'warning',
      message: `${authType.toUpperCase()} authentication is missing a username or password.`,
    })
  }
  if (authType === 'bearer' && !script.config.http?.auth?.token) {
    pushIssue(issues, {
      id: 'script:bearer-auth',
      severity: 'warning',
      message: 'Bearer authentication is enabled without a token value.',
    })
  }

  const seenActionNames = new Map<string, Array<{ phase: AuthoringIssuePhase; actionIndex: number; action: ScriptActionDefinition }>>()
  actionEntries.forEach(({ phase, actionIndex, action }) => {
    const normalizedName = normalizeTokenName(action.name || '')
    if (normalizedName) {
      seenActionNames.set(normalizedName, [...(seenActionNames.get(normalizedName) || []), { phase, actionIndex, action }])
    }
  })

  actionEntries.forEach(({ phase, actionIndex, action }) => {
    const phasePrefix = actionIssuePhasePrefix(phase)
    const actionName = action.name || ''
    const actionTarget = action.target || ''
    const actionType = action.type || ''
    if (!actionName.trim()) {
      pushIssue(issues, {
        id: `${phasePrefix}:${actionIndex}:name`,
        severity: 'error',
        phase,
        actionIndex,
        message: 'Action name is required.',
      })
    }

    if (actionNeedsExplicitTarget(action) && !actionTarget.trim()) {
      pushIssue(issues, {
        id: `${phasePrefix}:${actionIndex}:target`,
        severity: 'error',
        phase,
        actionIndex,
        message: 'Action target is required for this protocol step.',
      })
    }

    if (actionType === 'browser_fill' || actionType === 'browser_select') {
      if (!String(action.value || '').trim()) {
        pushIssue(issues, {
          id: `${phasePrefix}:${actionIndex}:value`,
          severity: 'warning',
          phase,
          actionIndex,
          message: `${actionType} is missing a value.`,
        })
      }
    }

    if (actionType.startsWith('browser_') && actionType !== 'browser_navigate' && actionType !== 'browser_screenshot' && !action.selector?.trim() && !actionTarget.trim()) {
      pushIssue(issues, {
        id: `${phasePrefix}:${actionIndex}:selector`,
        severity: 'error',
        phase,
        actionIndex,
        message: `${actionType} needs a selector or target.`,
      })
    }

    ;(action.extractors || []).forEach((extractor, extractorIndex) => {
      if (!extractor.name.trim() || !extractor.expression.trim()) {
        pushIssue(issues, {
          id: `${phasePrefix}:${actionIndex}:extractor:${extractorIndex}`,
          severity: 'warning',
          phase,
          actionIndex,
          message: `Extractor ${extractorIndex + 1} is missing a name or expression.`,
        })
      }
    })

    collectControlFlowIssues(action, phase, actionIndex, issues)

    for (const value of collectActionStrings(action)) {
      for (const match of value.matchAll(PARAM_TOKEN)) {
        if (!parameterNames.has(normalizeTokenName(match[1] || ''))) {
          pushIssue(issues, {
            id: `${phasePrefix}:${actionIndex}:param:${match[1]}`,
            severity: 'error',
            phase,
            actionIndex,
            message: `Referenced parameter "${match[1]}" is not defined.`,
          })
        }
      }

      for (const match of value.matchAll(DATA_TOKEN)) {
        if (!dataSources.has(normalizeTokenName(match[1] || ''))) {
          pushIssue(issues, {
            id: `${phasePrefix}:${actionIndex}:data:${match[1]}`,
            severity: 'warning',
            phase,
            actionIndex,
            message: `Referenced data source "${match[1]}" is not defined.`,
          })
        }
      }
    }
  })

  for (const [name, entries] of seenActionNames.entries()) {
    if (entries.length <= 1) continue
    entries.forEach(({ phase, actionIndex, action }) => {
      const phasePrefix = actionIssuePhasePrefix(phase)
      pushIssue(issues, {
        id: `${phasePrefix}:${actionIndex}:duplicate-name:${name}`,
        severity: 'warning',
        phase,
        actionIndex,
        message: `Action name "${action.name || name}" is duplicated.`,
      })
    })
  }

  return issues
}

export function applyAuthoringComponentToScript(
  script: ScriptDefinition,
  component: AuthoringComponent,
  insertAfterActionIndex?: number | null,
): AuthoringComponentApplyResult {
  const nextScript = cloneScript(script)
  const inserted: string[] = []

  nextScript.config = deepMerge(nextScript.config as unknown as Record<string, unknown>, component.config) as unknown as ScriptConfig
  if (component.config && Object.keys(component.config).length > 0) {
    inserted.push('runtime/config settings')
  }

  const sourceNames = normalizedNames(nextScript.data_sources || [])
  const nextDataSources = [...(nextScript.data_sources || [])]
  ;(component.data_sources || []).forEach(source => {
    const normalized = normalizeTokenName(source.name || '')
    if (!normalized || sourceNames.has(normalized)) return
    nextDataSources.push(cloneScript(source))
    sourceNames.add(normalized)
  })
  if (nextDataSources.length !== (nextScript.data_sources || []).length) {
    nextScript.data_sources = nextDataSources
    inserted.push(`${nextDataSources.length - (script.data_sources || []).length} data source${nextDataSources.length - (script.data_sources || []).length === 1 ? '' : 's'}`)
  }

  const parameterNames = normalizedNames(nextScript.parameters || [])
  const nextParameters = [...(nextScript.parameters || [])]
  ;(component.parameters || []).forEach(parameter => {
    const normalized = normalizeTokenName(parameter.name || '')
    if (!normalized || parameterNames.has(normalized)) return
    nextParameters.push(cloneScript(parameter))
    parameterNames.add(normalized)
  })
  if (nextParameters.length !== (nextScript.parameters || []).length) {
    nextScript.parameters = nextParameters
    inserted.push(`${nextParameters.length - (script.parameters || []).length} parameter${nextParameters.length - (script.parameters || []).length === 1 ? '' : 's'}`)
  }

  const actionNameSet = normalizedNames(nextScript.actions || [])
  const buildActions = (actions: ScriptActionDefinition[] | undefined, phase?: 'init' | 'teardown') =>
    (actions || []).map(action => {
      const nextAction = cloneActionForInsertion(action, phase)
      const fallback = `${nextAction.type || 'action'}_${nextScript.actions.length + 1}`
      nextAction.name = buildUniqueActionName(normalizeActionName(nextAction.name || '', fallback), actionNameSet)
      return nextAction
    })

  const initActions = buildActions(component.init, 'init')
  const mainActions = buildActions(component.actions)
  const teardownActions = buildActions(component.teardown, 'teardown')
  const allActions = [...initActions, ...mainActions, ...teardownActions]
  if (allActions.length > 0) {
    // When a valid insertion point is supplied (editing an existing script), splice the main
    // actions in right after the selected action so the block lands where the user is working.
    // init/teardown actions stay appended — they carry a metadata.phase tag and are grouped by
    // phase in the outline regardless of array position. With no/invalid index, append as before.
    const canTarget =
      typeof insertAfterActionIndex === 'number' &&
      Number.isInteger(insertAfterActionIndex) &&
      insertAfterActionIndex >= 0 &&
      insertAfterActionIndex < nextScript.actions.length &&
      mainActions.length > 0
    if (canTarget) {
      const base = [...nextScript.actions]
      base.splice((insertAfterActionIndex as number) + 1, 0, ...mainActions)
      nextScript.actions = [...base, ...initActions, ...teardownActions]
    } else {
      nextScript.actions = [...nextScript.actions, ...allActions]
    }
    if (initActions.length > 0) inserted.push(`${initActions.length} init action${initActions.length === 1 ? '' : 's'}`)
    if (mainActions.length > 0) inserted.push(`${mainActions.length} action${mainActions.length === 1 ? '' : 's'}`)
    if (teardownActions.length > 0) inserted.push(`${teardownActions.length} teardown action${teardownActions.length === 1 ? '' : 's'}`)
  }

  return {
    script: nextScript,
    summary: inserted.length > 0
      ? `Inserted ${component.name}: ${inserted.join(', ')}.`
      : `${component.name} was already represented in this draft.`,
  }
}

export function groupIssuesByAction(issues: AuthoringIssue[], actionCount: number): AuthoringIssue[][] {
  const grouped = Array.from({ length: actionCount }, () => [] as AuthoringIssue[])
  issues.forEach((issue) => {
    const phase = issue.phase || 'action'
    if (phase === 'action' && typeof issue.actionIndex === 'number' && issue.actionIndex >= 0 && issue.actionIndex < grouped.length) {
      grouped[issue.actionIndex].push(issue)
    }
  })
  return grouped
}

export type AuthoringIssueBucketsByPhase = Record<AuthoringIssuePhase, AuthoringIssue[][]>

export function groupIssuesByPhaseAction(issues: AuthoringIssue[], script: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'>): AuthoringIssueBucketsByPhase {
  const grouped: AuthoringIssueBucketsByPhase = {
    init: Array.from({ length: script.init?.length || 0 }, () => [] as AuthoringIssue[]),
    action: Array.from({ length: script.actions?.length || 0 }, () => [] as AuthoringIssue[]),
    teardown: Array.from({ length: script.teardown?.length || 0 }, () => [] as AuthoringIssue[]),
  }
  issues.forEach((issue) => {
    const phase = issue.phase || 'action'
    if (typeof issue.actionIndex !== 'number' || issue.actionIndex < 0) return
    grouped[phase]?.[issue.actionIndex]?.push(issue)
  })
  return grouped
}

export function buildProtocolStarterTemplates(protocol: string, nextIndex: number): ActionTemplateDefinition[] {
  const sequence = nextIndex + 1
  switch (protocol) {
    case 'browser':
      return [
        {
          key: 'browser-navigate',
          label: 'Navigate',
          description: 'Start a browser journey with a page visit.',
          action: { name: `Navigate_${sequence}`, type: 'browser_navigate', target: '/login', selector: '', value: '', capture: { screenshot: false, full_page: false, html: false }, think_time_after: true },
        },
        {
          key: 'browser-fill',
          label: 'Fill Form Field',
          description: 'Pre-wire a selector plus a parameter-friendly value.',
          action: { name: `Fill_${sequence}`, type: 'browser_fill', target: '#email', selector: '#email', value: '{{param.user_email}}', capture: { screenshot: false, full_page: false, html: false }, think_time_after: true },
        },
        {
          key: 'browser-click',
          label: 'Click CTA',
          description: 'Click a primary control and continue the flow.',
          action: { name: `Click_${sequence}`, type: 'browser_click', target: '#submit', selector: '#submit', value: '', capture: { screenshot: false, full_page: false, html: false }, think_time_after: true },
        },
        {
          key: 'browser-screenshot',
          label: 'Capture Screenshot',
          description: 'Drop a quick visual checkpoint into the draft.',
          action: { name: `Screenshot_${sequence}`, type: 'browser_screenshot', target: '', selector: 'body', value: '', capture: { screenshot: true, full_page: true, html: false }, think_time_after: true },
        },
      ]
    case 'soap':
      return [
        {
          key: 'soap-request',
          label: 'SOAP Request',
          description: 'Seed a SOAP envelope request with a response assertion.',
          action: { name: `SOAP_Request_${sequence}`, type: 'POST', method: 'POST', target: '/service', headers: { 'Content-Type': 'text/xml; charset=utf-8' }, body: '<soapenv:Envelope></soapenv:Envelope>', extractors: [], assertions: [{ type: 'status_code', expected: 200 }], think_time_after: true, soap: { action: '', operation: '', body_mode: 'raw_xml' } },
        },
      ]
    case 'java_http':
      return [
        {
          key: 'java-http-post',
          label: 'Serialized POST',
          description: 'Create an opaque Java-over-HTTP transport step.',
          action: { name: `Java_HTTP_Request_${sequence}`, type: 'POST', method: 'POST', target: '/gateway', headers: { 'Content-Type': 'application/x-java-serialized-object' }, body: '', extractors: [], assertions: [{ type: 'status_code', expected: 200 }], think_time_after: true },
        },
      ]
    case 'jms-solace':
    case 'jms-servicebus':
    case 'jms-ibmmq':
      return [
        {
          key: 'jms-publish',
          label: 'Publish Message',
          description: 'Push a message to a queue or topic.',
          action: { name: `Publish_${sequence}`, type: 'jms_publish', target: 'orders.in', headers: {}, body: '{"event":"created"}', params: {}, metadata: {}, extractors: [], assertions: [{ type: 'message_received' }], think_time_after: true },
        },
        {
          key: 'jms-request-reply',
          label: 'Request / Reply',
          description: 'Seed a request-reply exchange with correlation metadata.',
          action: { name: `Request_Reply_${sequence}`, type: 'jms_request_reply', target: 'orders.rpc', headers: {}, body: '{"command":"ping"}', params: {}, metadata: { reply_to: 'orders.reply' }, extractors: [], assertions: [{ type: 'message_received' }], think_time_after: true },
        },
      ]
    case 'websocket':
      return [
        {
          key: 'websocket-connect',
          label: 'Connect',
          description: 'Open a persistent WebSocket connection.',
          action: { name: `WS_Connect_${sequence}`, type: 'ws_connect', target: '/ws', headers: {}, params: {}, extractors: [], assertions: [], think_time_after: true },
        },
        {
          key: 'websocket-send',
          label: 'Send Message',
          description: 'Send a text message on the open connection.',
          action: { name: `WS_Send_${sequence}`, type: 'ws_send', target: '', body: '{"type":"ping"}', headers: {}, params: { type: 'text' }, extractors: [], assertions: [], think_time_after: true },
        },
        {
          key: 'websocket-receive',
          label: 'Receive Message',
          description: 'Wait for a message and assert it arrived.',
          action: { name: `WS_Receive_${sequence}`, type: 'ws_receive', target: '', headers: {}, params: { timeout_ms: 30000 }, extractors: [], assertions: [{ type: 'message_received' }], think_time_after: true },
        },
      ]
    case 'grpc':
      return [
        {
          key: 'grpc-unary',
          label: 'Unary Call',
          description: 'Invoke one reflected gRPC method with a JSON protobuf body.',
          action: { name: `gRPC_Unary_${sequence}`, type: 'grpc_unary', target: 'package.Service/Method', body: '{}', headers: {}, params: { timeout_ms: 30000 }, extractors: [], assertions: [{ type: 'grpc_status', expected: 0 }], think_time_after: true },
        },
        {
          key: 'grpc-bidi',
          label: 'Bidirectional Stream',
          description: 'Seed a bidirectional streaming call with one JSON message.',
          action: { name: `gRPC_Stream_${sequence}`, type: 'grpc_bidi_stream', target: 'package.Service/Stream', body: '', headers: {}, params: { max_messages: 1, messages: ['{}'] }, extractors: [], assertions: [{ type: 'grpc_status', expected: 0 }], think_time_after: true },
        },
      ]
    case 'database':
      return [
        {
          key: 'database-query',
          label: 'Query Rows',
          description: 'Run a parameterized SELECT and capture row/scalar evidence.',
          action: { name: `DB_Query_${sequence}`, type: 'db_query', target: 'select id, name from users where id = ?', body: '', headers: {}, params: { binds: ['{{param.user_id}}'], max_rows: 10 }, extractors: [{ name: 'first_name', source: 'body', type: 'jsonpath', expression: '$.rows[0].name' }], assertions: [{ type: 'row_count', expected: 1 }], think_time_after: true },
        },
        {
          key: 'database-transaction',
          label: 'Transaction Block',
          description: 'Seed a transaction boundary that can wrap later query/exec actions.',
          action: { name: `DB_Begin_${sequence}`, type: 'db_begin', target: '', headers: {}, params: {}, extractors: [], assertions: [], think_time_after: true },
        },
        {
          key: 'database-exec',
          label: 'Exec Statement',
          description: 'Execute a write with bind parameters and affected-row checks.',
          action: { name: `DB_Exec_${sequence}`, type: 'db_exec', target: 'update users set last_seen = current_timestamp where id = ?', body: '', headers: {}, params: { binds: ['{{param.user_id}}'] }, extractors: [], assertions: [{ type: 'rows_affected', expected: 1 }], think_time_after: true },
        },
      ]
    case 'network':
      return [
        {
          key: 'network-dns',
          label: 'DNS Lookup',
          description: 'Resolve a deterministic host and retain record evidence.',
          action: { name: `DNS_Query_${sequence}`, type: 'dns_query', target: 'localhost', headers: {}, params: { record_type: 'A' }, extractors: [{ name: 'record_count', source: 'record_count', type: 'jsonpath', expression: '$.record_count' }], assertions: [{ type: 'record_count', expected: 1 }], think_time_after: true },
        },
        {
          key: 'network-tcp',
          label: 'TCP Request',
          description: 'Send bytes to an endpoint and capture the response transcript.',
          action: { name: `TCP_Request_${sequence}`, type: 'tcp_request', target: 'localhost:8080', body: 'ping', headers: {}, params: { read: true, read_bytes: 4096 }, extractors: [{ name: 'tcp_response', source: 'response', type: 'jsonpath', expression: '$.response' }], assertions: [{ type: 'body_contains', value: 'pong' }], think_time_after: true },
        },
        {
          key: 'network-ftp',
          label: 'FTP Command',
          description: 'Run a protocol command and preserve command/response evidence.',
          action: { name: `FTP_Command_${sequence}`, type: 'ftp_command', target: 'localhost:21', body: 'NOOP', headers: {}, params: {}, extractors: [], assertions: [{ type: 'body_contains', value: 'ok' }], think_time_after: true },
        },
      ]
    case 'https':
    case 'http':
    default:
      return [
        {
          key: 'http-get',
          label: 'GET Request',
          description: 'Start with a simple resource fetch.',
          action: { name: `GET_${sequence}`, type: 'GET', method: 'GET', target: '/health', headers: {}, body: '', extractors: [], assertions: [{ type: 'status_code', expected: 200 }], think_time_after: true },
        },
        {
          key: 'http-post-json',
          label: 'POST JSON',
          description: 'Seed a JSON write request with content headers.',
          action: { name: `POST_${sequence}`, type: 'POST', method: 'POST', target: '/orders', headers: { 'Content-Type': 'application/json' }, body: '{\n  "id": "{{param.order_id}}"\n}', extractors: [], assertions: [{ type: 'status_code', expected: 200 }], think_time_after: true },
        },
        {
          key: 'http-poll',
          label: 'Poll Status',
          description: 'Add a follow-up request pattern for eventual consistency.',
          action: { name: `Poll_${sequence}`, type: 'GET', method: 'GET', target: '/orders/{{param.order_id}}', headers: {}, body: '', extractors: [], assertions: [{ type: 'status_code', expected: 200 }], think_time_after: true },
        },
      ]
  }
}

export function buildMigrationAssistantSuggestions(script: ScriptDefinition): MigrationAssistantSuggestion[] {
  if (!script.metadata?.import) {
    return []
  }

  const suggestions: MigrationAssistantSuggestion[] = []
  const seenNames = new Set<string>()
  const namesNeedNormalization = script.actions.some((action, index) => {
    const fallback = `${action.type || 'action'}_${index + 1}`
    const normalized = normalizeActionName(action.name || '', fallback)
    const deduped = buildUniqueActionName(normalized, seenNames)
    return deduped !== (action.name || '')
  })

  if (namesNeedNormalization) {
    suggestions.push({
      key: 'normalize_action_names',
      title: 'Normalize Action Names',
      description: 'Clean up imported action names so the outline, replay output, and saved diffs stay readable.',
      applyLabel: 'Normalize Names',
    })
  }

  const thinkTime = script.config.think_time
  const thinkTimeType = canonicalThinkTimeType(thinkTime?.type)
  if (
    thinkTimeType !== 'none' &&
    thinkTimeType !== 'recorded' &&
    (thinkTime?.ignore || (thinkTime?.mean_ms || 0) <= 0)
  ) {
    suggestions.push({
      key: 'normalize_think_time',
      title: 'Normalize Think Time',
      description: 'Re-enable draft think time with a stable fixed default so replay and runtime review match.',
      applyLabel: 'Apply Think Time Defaults',
    })
  }

  if (!script.config.pacing || script.config.pacing.type === 'none') {
    suggestions.push({
      key: 'apply_gentle_pacing',
      title: 'Apply Gentle Pacing',
      description: 'Seed the draft with a predictable fixed delay instead of running flat out during early review.',
      applyLabel: 'Apply Gentle Pacing',
    })
  }

  if (!script.config.run_logic?.auto_transactions || script.config.run_logic.auto_transactions === 'none') {
    suggestions.push({
      key: 'apply_per_action_transactions',
      title: 'Apply Per-Action Transactions',
      description: 'Wrap action timing consistently so imported runs produce usable transaction slices right away.',
      applyLabel: 'Apply Per-Action Transactions',
    })
  }

  return suggestions
}

export function buildScriptRepairSuggestions(script: ScriptDefinition, issues: AuthoringIssue[] = collectAuthoringIssues(script)): ScriptRepairSuggestion[] {
  const suggestions: ScriptRepairSuggestion[] = []
  const hasMissingData = collectMissingDataReferences(script).size > 0 || issues.some(issue => issue.id.includes(':data:'))
  const hasMissingAssertions = script.actions.some(action => isHttpLikeAction(action) && (!action.assertions || action.assertions.length === 0))
  const hasTokenLiterals = script.actions.some(action => {
    const headerValues = [
      ...Object.entries(action.headers || {}),
      ...(action.header_list || []).map(header => [header.name, header.value] as const),
    ]
    if (headerValues.some(([key, value]) => TOKENISH_KEY.test(key) && String(value || '').trim() && !String(value).includes('{{'))) return true
    if (headerValues.some(([, value]) => /^Bearer\s+\S+/i.test(String(value || '')) && !String(value).includes('{{'))) return true
    return hasHardCodedSensitiveJSONField(action.body || '')
  })
  const hasMissingBrowserWaits = script.actions.some(action =>
    action.type.startsWith('browser_')
    && (action.type === 'browser_click' || action.type === 'browser_fill' || action.type === 'browser_navigate')
    && !action.wait_for,
  )
  const hasUnstableBrowserSelectors = script.actions.some(action => action.type.startsWith('browser_') && isUnstableSelector(action.selector || action.target))

  if (hasMissingData) {
    suggestions.push({
      key: 'create_missing_data_source',
      title: 'Create Missing Data Sources',
      description: 'Add CSV seed data sources for unresolved {{data.source.column}} references so validation and scenario fit checks have concrete rows to inspect.',
      applyLabel: 'Create Data Sources',
    })
  }
  if (hasTokenLiterals) {
    suggestions.push({
      key: 'parameterize_hard_coded_tokens',
      title: 'Parameterize Hard-Coded Tokens',
      description: 'Remove bearer/session/token literals from the draft and replace them with project-secret references.',
      applyLabel: 'Externalize Tokens',
    })
  }
  if (hasMissingAssertions) {
    suggestions.push({
      key: 'add_default_assertions',
      title: 'Add Default Assertions',
      description: 'Add response assertions to request actions that currently replay without pass/fail evidence.',
      applyLabel: 'Add Assertions',
    })
  }
  if (hasMissingBrowserWaits) {
    suggestions.push({
      key: 'stabilize_browser_waits',
      title: 'Add Browser Waits',
      description: 'Attach page-ready waits to browser navigation and interaction steps that are likely to race during replay.',
      applyLabel: 'Add Waits',
    })
  }
  if (hasUnstableBrowserSelectors) {
    suggestions.push({
      key: 'stabilize_browser_selectors',
      title: 'Review Selectors',
      description: 'Review brittle selectors against captured DOM or the application. Replacement selectors must exist in the target page.',
      applyLabel: 'Review Selectors',
    })
  }

  return suggestions
}

export function applyMigrationAssistantSuggestion(script: ScriptDefinition, key: MigrationAssistantSuggestion['key']): MigrationAssistantResult {
  switch (key) {
    case 'normalize_action_names':
      return normalizeImportedActionNames(script)
    case 'normalize_think_time':
      return normalizeImportedThinkTime(script)
    case 'apply_gentle_pacing':
      return applyGentlePacing(script)
    case 'apply_per_action_transactions':
      return applyPerActionTransactions(script)
  }
}

export function applyScriptRepairSuggestion(script: ScriptDefinition, key: ScriptRepairSuggestionKey): ScriptRepairResult {
  switch (key) {
    case 'create_missing_data_source':
      return createMissingDataSources(script)
    case 'parameterize_hard_coded_tokens':
      return parameterizeHardCodedTokens(script)
    case 'add_default_assertions':
      return addDefaultAssertions(script)
    case 'stabilize_browser_waits':
      return stabilizeBrowserWaits(script)
    case 'stabilize_browser_selectors':
      return stabilizeBrowserSelectors(script)
  }
}

function createMissingDataSources(script: ScriptDefinition): ScriptRepairResult {
  const nextScript = cloneScript(script)
  const missing = collectMissingDataReferences(nextScript)
  if (missing.size === 0) {
    return { script: nextScript, summary: 'No missing data-source references were found.' }
  }

  nextScript.data_sources = [...(nextScript.data_sources || [])]
  missing.forEach((columns, sourceName) => {
    const resolvedColumns = Array.from(columns).filter(Boolean)
    const finalColumns = resolvedColumns.length > 0 ? resolvedColumns : ['value']
    nextScript.data_sources?.push({
      name: sourceName,
      type: 'csv',
      path: '',
      strategy: 'sequential',
      delimiter: ',',
      first_row_header: true,
      columns: [],
      inline_data: `${finalColumns.join(',')}\n${finalColumns.map(column => `sample_${column}`).join(',')}`,
    })
  })

  return {
    script: nextScript,
    summary: `Created ${missing.size} missing data source${missing.size === 1 ? '' : 's'} from unresolved data references.`,
  }
}

function parameterizeHardCodedTokens(script: ScriptDefinition): ScriptRepairResult {
  const nextScript = cloneScript(script)
  const secretNames = new Map<string, string>()
  const usedSecretNames = new Set<string>()
  let replacements = 0

  const secretNameFor = (base: string, rawValue: string) => {
    const identity = `${base}\u0000${rawValue}`
    const existing = secretNames.get(identity)
    if (existing) return existing
    let name = base
    let suffix = 2
    while (usedSecretNames.has(name)) {
      name = `${base}_${suffix}`
      suffix += 1
    }
    usedSecretNames.add(name)
    secretNames.set(identity, name)
    return name
  }

  const externalizeHeader = (name: string, value: string) => {
    const rawValue = String(value || '').trim()
    if (!rawValue || rawValue.includes('{{')) return value
    if (!TOKENISH_KEY.test(name) && !/^Bearer\s+\S+/i.test(rawValue)) return value
    const base = sanitizeRepairName(name.replace(/^authorization$/i, 'access_token'), 'access_token')
    const literal = rawValue.replace(/^Bearer\s+/i, '')
    const secretName = secretNameFor(base, literal)
    replacements += 1
    return /^Bearer\s+\S+/i.test(rawValue) ? `Bearer {{secret.${secretName}}}` : `{{secret.${secretName}}}`
  }

  nextScript.actions = nextScript.actions.map(action => {
    const updated = {
      ...action,
      headers: { ...(action.headers || {}) },
      header_list: action.header_list?.map(header => ({ ...header })),
    }
    const orderedHeaderNames = new Set(
      (updated.header_list || []).map(header => header.name.trim().toLowerCase()).filter(Boolean),
    )
    Object.entries(updated.headers || {})
      .filter(([key]) => !orderedHeaderNames.has(key.trim().toLowerCase()))
      .forEach(([key, value]) => {
        updated.headers![key] = externalizeHeader(key, value)
      })
    if (updated.header_list?.length) {
      updated.header_list = updated.header_list.map(header => ({
        ...header,
        value: externalizeHeader(header.name, header.value),
      }))
      updated.headers = headerListToCompatibilityMap(updated.header_list, updated.headers)
    }

    if (updated.body && TOKENISH_KEY.test(updated.body)) {
      updated.body = updated.body.replace(hardCodedSensitiveJSONFieldPattern(true), (match, keyName, tokenValue) => {
        if (tokenValue.includes('{{')) return match
        const base = sanitizeRepairName(keyName, 'token_value')
        const secretName = secretNameFor(base, tokenValue)
        replacements += 1
        return `"${keyName}":"{{secret.${secretName}}}"`
      })
    }

    return updated
  })

  return {
    script: nextScript,
    summary: replacements > 0
      ? `Externalized ${replacements} hard-coded token value${replacements === 1 ? '' : 's'} as project-secret references.`
      : 'No hard-coded token literals needed repair.',
  }
}

function addDefaultAssertions(script: ScriptDefinition): ScriptRepairResult {
  const nextScript = cloneScript(script)
  let updatedCount = 0
  nextScript.actions = nextScript.actions.map(action => {
    if (!isHttpLikeAction(action) || (action.assertions || []).length > 0) return action
    updatedCount += 1
    const expected = action.method === 'POST' || action.type === 'POST' ? 201 : 200
    return {
      ...action,
      assertions: [{ type: 'status_code', expected }],
    }
  })
  return {
    script: nextScript,
    summary: updatedCount > 0
      ? `Added default response assertions to ${updatedCount} request action${updatedCount === 1 ? '' : 's'}.`
      : 'Every request action already had assertions.',
  }
}

function stabilizeBrowserWaits(script: ScriptDefinition): ScriptRepairResult {
  const nextScript = cloneScript(script)
  let updatedCount = 0
  nextScript.actions = nextScript.actions.map(action => {
    if (!action.type.startsWith('browser_') || action.wait_for) return action
    if (action.type !== 'browser_click' && action.type !== 'browser_fill' && action.type !== 'browser_navigate') return action
    updatedCount += 1
    return {
      ...action,
      wait_for: action.type === 'browser_navigate'
        ? { type: 'load_state', value: 'networkidle', timeout_ms: 10000 }
        : { type: 'selector', value: action.selector || action.target || 'body', timeout_ms: 10000 },
    }
  })
  return {
    script: nextScript,
    summary: updatedCount > 0
      ? `Added replay waits to ${updatedCount} browser action${updatedCount === 1 ? '' : 's'}.`
      : 'Browser waits were already present.',
  }
}

function stabilizeBrowserSelectors(script: ScriptDefinition): ScriptRepairResult {
  // A generated attribute name is not evidence that an element exists.
  // Retain this key for callers with saved suggestions, but never mutate selectors.
  return { script, summary: 'Review selectors against the captured DOM. No selectors changed; choose a verified element before editing.' }
}

export function normalizeImportedActionNames(script: ScriptDefinition): MigrationAssistantResult {
  const nextScript = cloneScript(script)
  const renamed: string[] = []
  const seen = new Set<string>()

  nextScript.actions = nextScript.actions.map((action, index) => {
    const fallback = `${action.type || 'action'}_${index + 1}`
    const nextName = buildUniqueActionName(normalizeActionName(action.name || '', fallback), seen)
    if (nextName !== action.name) {
      renamed.push(`${action.name || fallback} -> ${nextName}`)
    }
    return {
      ...action,
      name: nextName,
    }
  })

  return {
    script: nextScript,
    summary: renamed.length > 0
      ? `Normalized ${renamed.length} imported action name${renamed.length === 1 ? '' : 's'}: ${renamed.slice(0, 3).join(', ')}${renamed.length > 3 ? ', ...' : ''}`
      : 'Action names were already normalized.',
  }
}

export function normalizeImportedThinkTime(script: ScriptDefinition): MigrationAssistantResult {
  const nextScript = cloneScript(script)
  const thinkTimeType = canonicalThinkTimeType(nextScript.config.think_time?.type)
  if (thinkTimeType === 'none' || thinkTimeType === 'recorded') {
    nextScript.config.think_time = thinkTimeConfigForType(nextScript.config.think_time, thinkTimeType)
    return {
      script: nextScript,
      summary: thinkTimeType === 'none'
        ? 'Think time is explicitly disabled and its policy controls were preserved.'
        : 'Recorded think time already preserves source timing and policy controls.',
    }
  }
  nextScript.config.think_time = {
    ...(nextScript.config.think_time || { type: 'fixed', mean_ms: 1000 }),
    type: nextScript.config.think_time?.type || 'fixed',
    mean_ms: (nextScript.config.think_time?.mean_ms || 0) > 0 ? (nextScript.config.think_time?.mean_ms || 1000) : 1000,
    ignore: false,
  }

  return {
    script: nextScript,
    summary: 'Applied imported think time defaults with think time enabled.',
  }
}

export function applyGentlePacing(script: ScriptDefinition): MigrationAssistantResult {
  const nextScript = cloneScript(script)
  nextScript.config.pacing = {
    ...(nextScript.config.pacing || { type: 'fixed_delay', delay_ms: 1000 }),
    type: 'fixed_delay',
    delay_ms: nextScript.config.pacing?.delay_ms && nextScript.config.pacing.delay_ms > 0
      ? nextScript.config.pacing.delay_ms
      : 1000,
  }

  return {
    script: nextScript,
    summary: `Applied fixed-delay pacing at ${formatMillisecondsAsSecondsDuration(nextScript.config.pacing.delay_ms)} per iteration.`,
  }
}

export function applyPerActionTransactions(script: ScriptDefinition): MigrationAssistantResult {
  const nextScript = cloneScript(script)
  nextScript.config.run_logic = {
    ...(nextScript.config.run_logic || {}),
    auto_transactions: 'per_action',
  }

  return {
    script: nextScript,
    summary: 'Enabled per-action transactions for imported draft review.',
  }
}

export function summarizeParameters(parameters: ScriptParameterDefinition[]): string[] {
  return parameters.map((parameter) => `${parameter.name || 'unnamed'} • ${parameter.type || 'list'}`)
}

export function summarizeDataSources(dataSources: ScriptDataSource[]): string[] {
  return dataSources.map((source) => `${source.name || 'unnamed'} • ${source.type || 'csv'}`)
}
