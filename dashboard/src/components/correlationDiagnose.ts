import type {
  CorrelationStep,
  ScriptActionDefinition,
  ScriptDefinition,
  ScriptExtractorDefinition,
  ScriptReplayActionEvent,
} from '../types/scripts'
import type { ReferencePhase, StepPosition } from './referenceScopeModel'

export type { ReferencePhase, StepPosition } from './referenceScopeModel'
export type ScriptStepPhase = ReferencePhase
export type ScriptStepRef = StepPosition

export type ResolvedStepTarget =
  | {
    kind: 'action'
    step: ScriptStepRef
    action: ScriptActionDefinition
    extractors: ScriptExtractorDefinition[]
  }
  | {
    kind: 'sequence_request'
    step: ScriptStepRef
    action: ScriptActionDefinition
    requestIndex: number
    request: ScriptActionDefinition
    extractors: ScriptExtractorDefinition[]
  }

export interface SuggestedExtractorFix {
  actionIndex: number
  step?: ScriptStepRef
  stepLocationLabel?: string
  extractor: ScriptExtractorDefinition
  failingActionIndex?: number
  failingStep?: ScriptStepRef
  failingStepLocationLabel?: string
  variableName: string
  sampleValue?: string
  reason: string
}

export interface CorrelationDiffRow {
  actionIndex: number
  step?: ScriptStepRef
  stepLocationLabel?: string
  actionName: string
  method: string
  target: string
  statusCode?: number
  success: boolean
  extractedVariables: string[]
  observedExtractions: Record<string, string>
  unresolvedVariables: string[]
  suggestedFixes: SuggestedExtractorFix[]
}

const correlationStepRefMap = new WeakMap<CorrelationStep, ScriptStepRef>()

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function normalizeStepPhase(phase?: string | null): ReferencePhase {
  if (phase === 'init') return 'init'
  if (phase === 'teardown') return 'teardown'
  return 'action'
}

function isSupportedReplayPhase(phase?: string | null): boolean {
  return !phase || phase === 'init' || phase === 'action' || phase === 'actions' || phase === 'teardown'
}

function hasReplayObservationEvidence(event: ScriptReplayActionEvent): boolean {
  if (!event.phase || event.phase === 'action' || event.phase === 'actions') return true
  const snapshot = event.snapshot
  return Boolean(
    snapshot?.request_url ||
    snapshot?.request_body !== undefined ||
    snapshot?.request_headers ||
    snapshot?.response_body !== undefined ||
    snapshot?.response_headers ||
    typeof snapshot?.response_status === 'number' ||
    event.target ||
    event.body !== undefined ||
    event.headers ||
    typeof event.status_code === 'number' ||
    (event.extractions && Object.keys(event.extractions).length > 0),
  )
}

export function normalizeEventStepPath(
  event: Pick<ScriptReplayActionEvent, 'step_path' | 'request_index'>,
): string[] | undefined {
  if (Array.isArray(event.step_path) && event.step_path.length > 0) {
    return [...event.step_path]
  }
  if (typeof event.request_index === 'number' && Number.isInteger(event.request_index) && event.request_index >= 0) {
    return [`requests[${event.request_index}]`]
  }
  return undefined
}

export function eventStep(
  event: Pick<ScriptReplayActionEvent, 'phase' | 'action_index' | 'step_path' | 'request_index'> | null | undefined,
): ScriptStepRef | null {
  if (!event || !isSupportedReplayPhase(event.phase)) return null
  if (typeof event.action_index !== 'number' || !Number.isInteger(event.action_index) || event.action_index < 0) {
    return null
  }
  const stepPath = normalizeEventStepPath(event)
  return {
    phase: normalizeStepPhase(event.phase),
    actionIndex: event.action_index,
    ...(stepPath?.length ? { stepPath } : {}),
  }
}

export function sameStepRef(left?: ScriptStepRef | null, right?: ScriptStepRef | null): boolean {
  if (!left || !right) return false
  return (
    normalizeStepPhase(left.phase) === normalizeStepPhase(right.phase) &&
    left.actionIndex === right.actionIndex &&
    (left.stepPath || []).join('/') === (right.stepPath || []).join('/')
  )
}

function escapeRegExpLiteral(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function formatStepSegmentLabel(segment: string): string {
  const simple = /^(requests|then|else|loop|default|steps|try|catch|finally)\[(\d+)\]$/.exec(segment)
  if (simple) {
    const [, kind, raw] = simple
    const num = +raw + 1
    return kind === 'requests' ? `Request ${num}` : `${kind[0].toUpperCase()}${kind.slice(1)} #${num}`
  }
  const switchCase = /^case\[(\d+)\]\[(\d+)\]$/.exec(segment)
  if (switchCase) return `Case ${+switchCase[1] + 1} #${+switchCase[2] + 1}`
  const parallel = /^branches\[(\d+)\]\.steps\[(\d+)\]$/.exec(segment)
  if (parallel) return `Branch ${+parallel[1] + 1} #${+parallel[2] + 1}`
  return segment
}

export function formatStepLocation(step: ScriptStepRef): string {
  const phase = normalizeStepPhase(step.phase)
  const base = `${phase === 'init' ? 'Init ' : phase === 'teardown' ? 'Teardown ' : ''}#${step.actionIndex + 1}`
  return step.stepPath?.length ? [base, ...step.stepPath.map(formatStepSegmentLabel)].join(' · ') : base
}

function phaseActions(
  script: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'> | null | undefined,
  phase: ReferencePhase,
): ScriptActionDefinition[] {
  const list = script?.[phase === 'action' ? 'actions' : phase]
  return Array.isArray(list) ? list : []
}

function rawSequenceRequests(action: ScriptActionDefinition): unknown[] {
  return Array.isArray(action.params?.requests) ? action.params.requests : []
}

function rawControlStepsField(action: ScriptActionDefinition, field: string): unknown[] {
  for (const bag of [action.control, action.params] as Array<Record<string, unknown> | undefined>) {
    if (bag && Array.isArray(bag[field])) return bag[field] as unknown[]
  }
  return []
}

export function resolveStepTarget(
  script: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'> | null | undefined,
  step: ScriptStepRef | null | undefined,
): ResolvedStepTarget | null {
  if (!script || !step || !Number.isInteger(step.actionIndex) || step.actionIndex < 0) return null
  const phase = normalizeStepPhase(step.phase)
  const rootCandidate = phaseActions(script, phase)[step.actionIndex]
  if (!isRecord(rootCandidate)) return null
  let current = rootCandidate as unknown as ScriptActionDefinition
  const normalizedStep: ScriptStepRef = {
    phase,
    actionIndex: step.actionIndex,
    ...(step.stepPath?.length ? { stepPath: [...step.stepPath] } : {}),
  }
  const path = step.stepPath || []
  for (let i = 0; i < path.length; i++) {
    const segment = path[i]
    const reqMatch = /^(?:params\.)?requests\[(\d+)\]$/.exec(segment)
    if (reqMatch) {
      const requestIndex = +reqMatch[1]
      const rawRequest = rawSequenceRequests(current)[requestIndex]
      if (!isRecord(rawRequest)) return null
      const request = rawRequest as unknown as ScriptActionDefinition
      if (i === path.length - 1) {
        return {
          kind: 'sequence_request',
          step: normalizedStep,
          action: current,
          requestIndex,
          request,
          extractors: Array.isArray(request.extractors) ? request.extractors : [],
        }
      }
      current = request
      continue
    }
    const branchMatch = /^(then|else|loop|default|steps|try|catch|finally)\[(\d+)\]$/.exec(segment)
    if (branchMatch) {
      const [, kind, idx] = branchMatch
      const primary = rawControlStepsField(current, 'steps')
      const list = /^(then|loop|steps|try)$/.test(kind) && primary.length > 0 ? primary : rawControlStepsField(current, kind)
      const next = list[+idx]
      if (!isRecord(next)) return null
      current = next as unknown as ScriptActionDefinition
      continue
    }
    const groupMatch = /^(?:(case)\[(\d+)\]\[(\d+)\]|(cases)\[(\d+)\]\.steps\[(\d+)\]|(branches)\[(\d+)\]\.steps\[(\d+)\]|(branch)\[(\d+)\]\[(\d+)\])$/.exec(segment)
    if (groupMatch) {
      const isCase = Boolean(groupMatch[1] || groupMatch[4])
      const groupIdx = +(groupMatch[2] ?? groupMatch[5] ?? groupMatch[8] ?? groupMatch[11])
      const stepIdx = +(groupMatch[3] ?? groupMatch[6] ?? groupMatch[9] ?? groupMatch[12])
      const group = (isCase ? current.control?.cases : current.control?.branches)?.[groupIdx]
      const next = isRecord(group) && Array.isArray(group.steps) ? group.steps[stepIdx] : undefined
      if (!isRecord(next)) return null
      current = next as unknown as ScriptActionDefinition
      continue
    }
    return null
  }
  return {
    kind: 'action',
    step: normalizedStep,
    action: current,
    extractors: Array.isArray(current.extractors) ? current.extractors : [],
  }
}

const CONTAINER_ACTION_TYPES = new Set([
  'transaction', 'control_if', 'control_switch', 'control_loop', 'control_for_each',
  'control_try', 'control_parallel', 'http_sequence', 'ws_on_message',
])

function isContainerAction(action: ScriptActionDefinition): boolean {
  return (
    CONTAINER_ACTION_TYPES.has((action.type || '').toLowerCase()) ||
    rawSequenceRequests(action).some(isRecord) ||
    ['steps', 'else', 'default'].some(field => rawControlStepsField(action, field).some(isRecord)) ||
    (action.control?.cases?.length ?? 0) > 0 ||
    (action.control?.branches?.length ?? 0) > 0
  )
}

export function walkScriptSteps(
  script: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'> | null | undefined,
): ResolvedStepTarget[] {
  if (!script) return []
  const targets: ResolvedStepTarget[] = []
  const visitAction = (action: ScriptActionDefinition, step: ScriptStepRef) => {
    targets.push({ kind: 'action', step, action, extractors: Array.isArray(action.extractors) ? action.extractors : [] })
    const baseStepPath = step.stepPath || []
    rawSequenceRequests(action).forEach((rawRequest, requestIndex) => {
      if (!isRecord(rawRequest)) return
      const request = rawRequest as unknown as ScriptActionDefinition
      targets.push({
        kind: 'sequence_request',
        step: { phase: step.phase, actionIndex: step.actionIndex, stepPath: [...baseStepPath, `requests[${requestIndex}]`] },
        action,
        requestIndex,
        request,
        extractors: Array.isArray(request.extractors) ? request.extractors : [],
      })
    })
    const visitList = (items: unknown[], formatSegment: (idx: number) => string) => {
      items.forEach((child, idx) => {
        if (isRecord(child)) {
          visitAction(child as unknown as ScriptActionDefinition, {
            phase: step.phase,
            actionIndex: step.actionIndex,
            stepPath: [...baseStepPath, formatSegment(idx)],
          })
        }
      })
    }
    const type = (action.type || '').toLowerCase()
    const primarySteps = rawControlStepsField(action, 'steps')
    if (primarySteps.length > 0) {
      const prefix = type === 'control_if' ? 'then'
        : type === 'control_loop' || type === 'control_for_each' || type === 'ws_on_message' ? 'loop'
          : type === 'control_try' ? 'try' : 'steps'
      visitList(primarySteps, idx => `${prefix}[${idx}]`)
    }
    visitList(rawControlStepsField(action, 'else'), idx => `else[${idx}]`)
    const visitGroupList = (groups: unknown, formatSegment: (gIdx: number, sIdx: number) => string) => {
      if (Array.isArray(groups)) {
        groups.forEach((group, gIdx) => {
          if (isRecord(group) && Array.isArray(group.steps)) visitList(group.steps, sIdx => formatSegment(gIdx, sIdx))
        })
      }
    }
    visitGroupList(action.control?.cases, (c, s) => `case[${c}][${s}]`)
    for (const field of ['default', 'catch', 'finally'] as const) visitList(rawControlStepsField(action, field), idx => `${field}[${idx}]`)
    visitGroupList(action.control?.branches, (b, s) => `branches[${b}].steps[${s}]`)
  }
  for (const phase of ['init', 'action', 'teardown'] as const) {
    phaseActions(script, phase).forEach((action, actionIndex) => {
      if (isRecord(action)) visitAction(action, { phase, actionIndex })
    })
  }
  return targets
}

export function stepRefForCorrelationStep(step: CorrelationStep | null | undefined): ScriptStepRef | null {
  if (!step) return null
  const mapped = correlationStepRefMap.get(step)
  if (mapped) return mapped
  const stepPath = Array.isArray(step.step_path) && step.step_path.length > 0
    ? [...step.step_path]
    : typeof step.request_index === 'number' && step.request_index >= 0
      ? [`requests[${step.request_index}]`]
      : undefined
  if (typeof step.id !== 'number' || step.id < 0) return null
  return {
    phase: 'action',
    actionIndex: step.id,
    ...(stepPath?.length ? { stepPath } : {}),
  }
}

// Build the recorded/replay observation lists for a record-vs-replay correlation
// diagnosis. The replay side comes from a finished replay's per-event snapshots;
// the recorded side comes either from a stored server-side baseline (preferred)
// or, as a fallback, a HAR the user uploads.

/**
 * Convert replay events into correlation steps across init, action, and teardown
 * phases (and nested container step paths). When all observations are in the
 * action phase, IDs remain equal to action_index so duplicate and nested step
 * detection preserve top-level action indices; when observations span init or
 * teardown phases, sequential IDs are assigned per (phase, actionIndex) while
 * preserving stepRef metadata for repair targeting.
 * Exported for tests.
 */
export function replayEventsToSteps(events: ScriptReplayActionEvent[] | undefined): CorrelationStep[] {
  if (!events) return []
  const candidates: Array<{ event: ScriptReplayActionEvent; stepRef: ScriptStepRef }> = []
  let hasNonActionPhase = false
  for (const event of events) {
    if (!hasReplayObservationEvidence(event)) continue
    const stepRef = eventStep(event)
    if (!stepRef) continue
    if (stepRef.phase !== 'action') {
      hasNonActionPhase = true
    }
    candidates.push({ event, stepRef })
  }
  if (candidates.length === 0) return []

  const phaseActionKeyToId = new Map<string, number>()
  let nextId = 0

  return candidates.map(({ event, stepRef }) => {
    let id = stepRef.actionIndex
    if (hasNonActionPhase) {
      const phaseActionKey = `${stepRef.phase}:${stepRef.actionIndex}`
      const existing = phaseActionKeyToId.get(phaseActionKey)
      if (existing !== undefined) {
        id = existing
      } else {
        id = nextId++
        phaseActionKeyToId.set(phaseActionKey, id)
      }
    }
    const snapshot = event.snapshot
    const step: CorrelationStep = {
      id,
      request_url: snapshot?.request_url || event.target || undefined,
      request_body: snapshot?.request_body ?? event.body ?? undefined,
      request_headers: snapshot?.request_headers ?? event.headers ?? undefined,
      response_body: snapshot?.response_body ?? undefined,
      response_headers: snapshot?.response_headers ?? undefined,
      response_status: snapshot?.response_status ?? event.status_code ?? undefined,
      response_success: event.success,
      parent_action_name: event.parent_action_name,
      request_index: event.request_index,
      step_path: stepRef.stepPath ? [...stepRef.stepPath] : undefined,
    }
    correlationStepRefMap.set(step, stepRef)
    Object.defineProperties(step, {
      step: { value: stepRef, configurable: true },
      stepLocationLabel: { value: formatStepLocation(stepRef), configurable: true },
    })
    return step
  })
}

export function duplicateCorrelationStepIds(steps: CorrelationStep[]): number[] {
  const seen = new Set<number>()
  const duplicates = new Set<number>()
  for (const step of steps) {
    if (seen.has(step.id)) duplicates.add(step.id)
    seen.add(step.id)
  }
  return [...duplicates].sort((a, b) => a - b)
}

export function nestedCorrelationStepIds(steps: CorrelationStep[]): number[] {
  return [...new Set(steps
    .filter(step => !!step.parent_action_name || typeof step.request_index === 'number' || (step.step_path?.length ?? 0) > 0)
    .map(step => step.id))].sort((a, b) => a - b)
}

export function unmappedNestedCorrelationStepIds(steps: CorrelationStep[]): number[] {
  return [...new Set(steps
    .filter(step => !!step.parent_action_name && typeof step.request_index !== 'number' && (step.step_path?.length ?? 0) === 0)
    .map(step => step.id))].sort((a, b) => a - b)
}

function normalizeUrlForMatch(url?: string): string {
  if (!url) return ''
  const trimmed = url.trim().replace(/\{\{\s*[^}]+\s*\}\}/g, '')
  if (!trimmed) return ''
  try {
    const parsed = new URL(trimmed, 'https://example.invalid')
    return parsed.pathname.replace(/\/+$/, '') || '/'
  } catch {
    return trimmed.split('?')[0].replace(/\/+$/, '') || trimmed
  }
}

function targetActionOf(target: ResolvedStepTarget): ScriptActionDefinition {
  return target.kind === 'sequence_request' ? target.request : target.action
}

const HTTP_METHODS = new Set(['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'])

function actionHttpMethod(action?: Pick<ScriptActionDefinition, 'method' | 'type'> | null): string {
  const raw = (action?.method || action?.type || 'GET').trim().toUpperCase()
  return HTTP_METHODS.has(raw) ? raw : 'GET'
}

function eventHttpMethod(event: ScriptReplayActionEvent, fallbackAction?: ScriptActionDefinition | null): string {
  const raw = (event.snapshot?.request_method || event.method || event.action_type || '').trim().toUpperCase()
  return HTTP_METHODS.has(raw) ? raw : actionHttpMethod(fallbackAction)
}

function matchTargetByUrlAndMethod(
  candidates: ResolvedStepTarget[],
  event: ScriptReplayActionEvent,
): ResolvedStepTarget | null {
  const leaves = candidates.filter(c => c.kind === 'sequence_request' || !isContainerAction(c.action))
  const pool = leaves.length > 0 ? leaves : candidates
  if (pool.length === 0) return null
  const eventUrl = event.snapshot?.request_url || event.target || ''
  const normUrl = normalizeUrlForMatch(eventUrl)
  const eventMethod = eventHttpMethod(event)
  const urlMatches = (action: ScriptActionDefinition) =>
    action.target === eventUrl || normalizeUrlForMatch(action.target) === normUrl
  if (normUrl) {
    const exact = pool.find(c => actionHttpMethod(targetActionOf(c)) === eventMethod && urlMatches(targetActionOf(c)))
    if (exact) return exact
    const urlOnly = pool.find(c => urlMatches(targetActionOf(c)))
    if (urlOnly) return urlOnly
  }
  return (event.action_name && pool.find(c => targetActionOf(c).name === event.action_name)) || pool[0] || null
}

function resolveEventStepTarget(
  script: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'> | null | undefined,
  walked: ResolvedStepTarget[],
  event: ScriptReplayActionEvent,
): { step: ScriptStepRef; target: ResolvedStepTarget | null } | null {
  const rawStep = eventStep(event)
  if (!script || walked.length === 0) return rawStep ? { step: rawStep, target: null } : null
  if (rawStep) {
    const sameIndex = walked.filter(c => c.step.phase === rawStep.phase && c.step.actionIndex === rawStep.actionIndex)
    const direct = resolveStepTarget(script, rawStep)
    if (direct) {
      if (direct.kind === 'sequence_request' || !isContainerAction(direct.action) || (rawStep.stepPath?.length ?? 0) > 0) {
        return { step: direct.step, target: direct }
      }
      const matchedNested = matchTargetByUrlAndMethod(sameIndex.filter(c => (c.step.stepPath?.length ?? 0) > 0), event)
      return matchedNested ? { step: matchedNested.step, target: matchedNested } : { step: direct.step, target: direct }
    }
    const matchedSame = matchTargetByUrlAndMethod(sameIndex, event)
    if (matchedSame) return { step: matchedSame.step, target: matchedSame }
  }
  const preferredPhase = isSupportedReplayPhase(event.phase) ? normalizeStepPhase(event.phase) : 'action'
  const samePhase = walked.filter(c => c.step.phase === preferredPhase)
  const matchedPhase = matchTargetByUrlAndMethod(samePhase.length > 0 ? samePhase : walked, event)
  return matchedPhase ? { step: matchedPhase.step, target: matchedPhase } : rawStep ? { step: rawStep, target: null } : null
}

interface ProducedTokenCandidate {
  step: ScriptStepRef
  stepLocationLabel: string
  variableName: string
  value: string
  extractor: ScriptExtractorDefinition
  alreadyExtracted: boolean
}

const TOKEN_KEY_PATTERN = /(token|csrf|xsrf|session|sid|nonce|state|ticket|auth|jwt|bearer|key|secret|id)$/i

function sanitizeVariableName(raw: string): string {
  const cleaned = raw
    .trim()
    .replace(/[^A-Za-z0-9_]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .toLowerCase()
  return cleaned || 'correlation_value'
}

function collectJsonTokenCandidates(
  value: unknown,
  pathPrefix: string,
  into: Array<{ variableName: string; value: string; expression: string; priority: boolean }>,
  depth = 0,
) {
  if (depth > 4 || !isRecord(value)) return
  for (const [key, child] of Object.entries(value)) {
    const jsonPath = pathPrefix ? `${pathPrefix}.${key}` : `$.${key}`
    if (typeof child === 'string' && child.trim().length >= 2) {
      into.push({
        variableName: sanitizeVariableName(key),
        value: child.trim(),
        expression: jsonPath,
        priority: TOKEN_KEY_PATTERN.test(key),
      })
    } else if (typeof child === 'number' && Number.isFinite(child) && TOKEN_KEY_PATTERN.test(key)) {
      into.push({
        variableName: sanitizeVariableName(key),
        value: String(child),
        expression: jsonPath,
        priority: true,
      })
    } else if (isRecord(child)) {
      collectJsonTokenCandidates(child, jsonPath, into, depth + 1)
    }
  }
}

function extractProducedTokens(
  step: ScriptStepRef,
  stepLocationLabel: string,
  event: ScriptReplayActionEvent,
  existingExtractors: ScriptExtractorDefinition[],
): ProducedTokenCandidate[] {
  const candidates: ProducedTokenCandidate[] = []
  const existingNames = new Set(existingExtractors.map(ext => ext.name))
  const existingExpressions = new Set(existingExtractors.map(ext => `${ext.source}:${ext.type}:${ext.expression}`))
  const mkExt = (
    name: string,
    source: ScriptExtractorDefinition['source'],
    type: ScriptExtractorDefinition['type'],
    expression: string,
  ): ScriptExtractorDefinition => ({ name, source, type, expression, on_miss: 'fail' })

  const addCandidate = (variableName: string, value: string, extractor: ScriptExtractorDefinition) => {
    if (!value) return
    const alreadyExtracted =
      existingNames.has(variableName) ||
      existingExpressions.has(`${extractor.source}:${extractor.type}:${extractor.expression}`)
    if (candidates.some(c => c.variableName === variableName && c.value === value)) return
    candidates.push({ step, stepLocationLabel, variableName, value, extractor, alreadyExtracted })
  }

  const responseBody = event.snapshot?.response_body
  if (typeof responseBody === 'string' && responseBody.trim()) {
    try {
      const parsed = JSON.parse(responseBody)
      const jsonEntries: Array<{ variableName: string; value: string; expression: string; priority: boolean }> = []
      collectJsonTokenCandidates(parsed, '$', jsonEntries)
      jsonEntries.sort((a, b) => Number(b.priority) - Number(a.priority))
      for (const entry of jsonEntries) {
        addCandidate(entry.variableName, entry.value, mkExt(entry.variableName, 'body', 'jsonpath', entry.expression))
      }
    } catch {
      for (const match of responseBody.matchAll(/<input[^>]*name=["']([^"']+)["'][^>]*value=["']([^"']+)["'][^>]*>/gi)) {
        const [, rawName, val] = match
        const varName = sanitizeVariableName(rawName)
        addCandidate(
          varName,
          val,
          mkExt(varName, 'body', 'regex', `name=["']${escapeRegExpLiteral(rawName)}["'][^>]*value=["']([^"']+)["']`),
        )
      }
    }
  }

  const responseHeaders = event.snapshot?.response_headers
  if (responseHeaders) {
    for (const [headerName, headerValue] of Object.entries(responseHeaders)) {
      if (!headerValue) continue
      if (headerName.toLowerCase() === 'set-cookie') {
        for (const line of headerValue.split('\n')) {
          const firstPair = line.split(';')[0]?.trim() || ''
          const eqIdx = firstPair.indexOf('=')
          if (eqIdx <= 0) continue
          const cookieName = firstPair.slice(0, eqIdx).trim()
          const cookieVal = firstPair.slice(eqIdx + 1).trim()
          const varName = sanitizeVariableName(cookieName)
          addCandidate(varName, cookieVal, mkExt(varName, 'header', 'regex', `${escapeRegExpLiteral(cookieName)}=([^;\\s]+)`))
        }
      } else if (TOKEN_KEY_PATTERN.test(headerName)) {
        const varName = sanitizeVariableName(headerName.replace(/^x-/i, ''))
        addCandidate(varName, headerValue.trim(), mkExt(varName, 'header', 'header', headerName))
      }
    }
  }

  if (event.extractions) {
    for (const [name, value] of Object.entries(event.extractions)) {
      addCandidate(name, value, existingExtractors.find(ext => ext.name === name) ?? mkExt(name, 'body', 'jsonpath', `$.${name}`))
    }
  }

  return candidates
}

function collectTemplateVarNames(texts: Array<string | undefined>): string[] {
  const vars = new Set<string>()
  const pattern = /\{\{\s*var\.([A-Za-z0-9_.-]+)\s*\}\}/g
  for (const text of texts) {
    if (!text) continue
    for (const match of text.matchAll(pattern)) {
      vars.add(match[1])
    }
  }
  return [...vars]
}

/**
 * Walk all 3 lifecycle phases (init, actions, teardown) and nested container
 * steps (transaction, control_if, control_switch, control_loop, control_for_each,
 * control_try, control_parallel, and http_sequence child requests) to diagnose
 * correlation state and suggest extractor fixes across the full script lifecycle.
 */
export function diagnoseCorrelations(
  script: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'> | null | undefined,
  events: ScriptReplayActionEvent[] | undefined,
): CorrelationDiffRow[] {
  if (!events || events.length === 0) return []
  const walked = walkScriptSteps(script)
  const rows: CorrelationDiffRow[] = []
  const producedSoFar: ProducedTokenCandidate[] = []
  const availableVariables = new Set<string>()

  for (const event of events) {
    if (!hasReplayObservationEvidence(event)) continue
    const resolved = resolveEventStepTarget(script, walked, event)
    if (!resolved) continue

    const { step, target } = resolved
    const stepLocationLabel = formatStepLocation(step)
    const actionDef = target ? targetActionOf(target) : undefined
    const existingExtractors = target
      ? target.kind === 'sequence_request'
        ? target.request.extractors || []
        : target.action.extractors || []
      : []

    const extractedVariables = [
      ...new Set([
        ...existingExtractors.map(ext => ext.name).filter(Boolean),
        ...Object.keys(event.extractions || {}),
      ]),
    ]
    const observedExtractions: Record<string, string> = { ...(event.extractions || {}) }

    const requestUrl = event.snapshot?.request_url || event.target || actionDef?.target || ''
    const requestBody = event.snapshot?.request_body ?? event.body ?? actionDef?.body
    const requestHeaders: Record<string, string> = {
      ...(actionDef?.headers || {}),
      ...(event.headers || {}),
      ...(event.snapshot?.request_headers || {}),
    }
    const enabledHeaderList = Array.isArray(actionDef?.header_list)
      ? actionDef!.header_list!.filter(h => isRecord(h) && h.enabled !== false)
      : []

    const authoredAndRuntimeTexts: Array<string | undefined> = [
      actionDef?.target,
      actionDef?.body,
      ...(actionDef?.headers ? Object.values(actionDef.headers) : []),
      ...enabledHeaderList.map(h => h.value),
      requestUrl,
      requestBody,
      ...Object.values(requestHeaders),
    ]

    const referencedVars = collectTemplateVarNames(authoredAndRuntimeTexts)
    const unresolvedSet = new Set<string>(event.diagnosis?.unresolved_variables || [])
    for (const varName of referencedVars) {
      if (!availableVariables.has(varName)) {
        unresolvedSet.add(varName)
      }
    }

    const suggestedFixes: SuggestedExtractorFix[] = []
    const combinedRequestSurface = [
      requestUrl,
      requestBody || '',
      ...Object.entries(requestHeaders).map(([k, v]) => `${k}: ${v}`),
      ...enabledHeaderList.map(h => `${h.name}: ${h.value}`),
    ].join('\n')

    const pushSuggestedFix = (
      producer: ProducedTokenCandidate,
      variableName: string,
      extractor: ScriptExtractorDefinition,
      preposition: 'before' | 'for',
    ) => {
      suggestedFixes.push({
        actionIndex: producer.step.actionIndex,
        step: producer.step,
        stepLocationLabel: producer.stepLocationLabel,
        extractor,
        failingActionIndex: step.actionIndex,
        failingStep: step,
        failingStepLocationLabel: stepLocationLabel,
        variableName,
        sampleValue: producer.value,
        reason: `Extract ${variableName} from ${producer.stepLocationLabel} ${preposition} ${stepLocationLabel}.`,
      })
    }

    const reversedProducers = [...producedSoFar].reverse()
    for (const unresolvedVar of unresolvedSet) {
      const matchingProducer =
        reversedProducers.find(candidate => candidate.variableName === unresolvedVar) ??
        reversedProducers.find(candidate => !candidate.alreadyExtracted && TOKEN_KEY_PATTERN.test(candidate.variableName))
      if (matchingProducer && !matchingProducer.alreadyExtracted) {
        pushSuggestedFix(
          matchingProducer,
          unresolvedVar,
          { ...matchingProducer.extractor, name: unresolvedVar },
          'before',
        )
      }
    }

    for (const producer of producedSoFar) {
      if (producer.alreadyExtracted) continue
      if (suggestedFixes.some(fix => sameStepRef(fix.step, producer.step) && fix.variableName === producer.variableName)) {
        continue
      }
      const valueUsedInRequest = producer.value.length >= 2 && combinedRequestSurface.includes(producer.value)
      const status = event.snapshot?.response_status ?? event.status_code
      const authFailureWithToken =
        (!event.success || status === 401 || status === 403) &&
        TOKEN_KEY_PATTERN.test(producer.variableName) &&
        !referencedVars.includes(producer.variableName)

      if (valueUsedInRequest || authFailureWithToken) {
        pushSuggestedFix(producer, producer.variableName, producer.extractor, 'for')
      }
    }

    const stepProduced = extractProducedTokens(step, stepLocationLabel, event, existingExtractors)
    for (const item of stepProduced) {
      if (item.alreadyExtracted && !(item.variableName in observedExtractions)) {
        observedExtractions[item.variableName] = item.value
      }
      producedSoFar.push(item)
    }
    for (const varName of extractedVariables) {
      availableVariables.add(varName)
    }

    rows.push({
      actionIndex: step.actionIndex,
      step,
      stepLocationLabel,
      actionName: event.action_name || actionDef?.name || `${eventHttpMethod(event, actionDef)} ${requestUrl}`,
      method: eventHttpMethod(event, actionDef),
      target: requestUrl,
      statusCode: event.snapshot?.response_status ?? event.status_code,
      success: event.success,
      extractedVariables,
      observedExtractions,
      unresolvedVariables: [...unresolvedSet],
      suggestedFixes,
    })
  }

  return rows
}

interface HarHeader { name?: string; value?: string }
interface HarEntry {
  _resourceType?: string
  request?: { method?: string; url?: string; postData?: { text?: string; encoding?: string }; headers?: HarHeader[] }
  response?: { status?: number; content?: { text?: string; mimeType?: string; encoding?: string }; headers?: HarHeader[] }
}

export interface HarToStepsLimits {
  maxEntries?: number
  maxDecodedChars?: number
}

export class HarToStepsLimitError extends Error {
  readonly kind: 'entries' | 'decoded_content'

  constructor(kind: 'entries' | 'decoded_content', message: string) {
    super(message)
    this.name = 'HarToStepsLimitError'
    this.kind = kind
  }
}

const STATIC_RESOURCE_TYPES = new Set(['image', 'stylesheet', 'font', 'media', 'script', 'manifest'])
const STATIC_MIME_PREFIXES = ['image/', 'font/', 'text/css', 'audio/', 'video/', 'application/javascript', 'text/javascript']
const STATIC_URL_EXTENSIONS = ['.css', '.js', '.mjs', '.map', '.png', '.jpg', '.jpeg', '.gif', '.webp', '.svg', '.ico', '.woff', '.woff2', '.ttf', '.eot', '.otf', '.mp4', '.webm', '.mp3', '.wav']

function headerArrayToMap(headers?: HarHeader[]): Record<string, string> | undefined {
  if (!Array.isArray(headers) || headers.length === 0) return undefined
  const map: Record<string, string> = {}
  const canonicalNames = new Map<string, string>()
  for (const header of headers) {
    if (!header.name) continue
    const normalized = header.name.toLowerCase()
    const existingName = canonicalNames.get(normalized)
    if (!existingName) {
      canonicalNames.set(normalized, header.name)
      map[header.name] = header.value ?? ''
      continue
    }
    // Correlation detection treats each newline-delimited value as one header
    // occurrence. Comma-folding is lossy (and invalid for Set-Cookie), while a
    // newline keeps every repeated occurrence independently diagnosable.
    map[existingName] += '\n' + (header.value ?? '')
  }
  return map
}

function isStaticAsset(entry: HarEntry): boolean {
  const method = (entry.request?.method || '').trim().toUpperCase()
  if (method && method !== 'GET' && method !== 'HEAD') return false
  const resourceType = (entry._resourceType || '').toLowerCase()
  if (resourceType && STATIC_RESOURCE_TYPES.has(resourceType)) return true
  const mime = (entry.response?.content?.mimeType || '').toLowerCase().split(';')[0].trim()
  if (STATIC_MIME_PREFIXES.some(prefix => mime.startsWith(prefix))) return true
  let path = entry.request?.url || ''
  try {
    const url = new URL(path, 'https://example.invalid')
    path = url.pathname
  } catch {
    // Keep the raw path for suffix checks.
  }
  const lowerPath = path.toLowerCase()
  return STATIC_URL_EXTENSIONS.some(ext => lowerPath.endsWith(ext))
}

function decodeHARText(text?: string, encoding?: string): string | undefined {
  if (text === undefined) return undefined
  if ((encoding || '').trim().toLowerCase() !== 'base64') return text
  try {
    const compact = text.replace(/\s+/g, '')
    const binary = atob(compact)
    const bytes = Uint8Array.from(binary, char => char.charCodeAt(0))
    return new TextDecoder().decode(bytes)
  } catch {
    return text
  }
}

/**
 * Parse a HAR document into correlation steps (the recorded side of the
 * fallback path), filtering out static assets so the sequence aligns with a
 * script's meaningful actions. Exported for tests.
 */
export function harToSteps(har: unknown, limits: HarToStepsLimits = {}): CorrelationStep[] {
  const log = (har as { log?: { entries?: unknown } } | null)?.log
  const entries = Array.isArray(log?.entries) ? (log!.entries as HarEntry[]) : []
  if (typeof limits.maxEntries === 'number' && entries.length > limits.maxEntries) {
    throw new HarToStepsLimitError('entries', `HAR contains more than ${limits.maxEntries} entries`)
  }
  const steps: CorrelationStep[] = []
  let decodedChars = 0
  const chargeDecodedContent = (value: string | undefined) => {
    if (value === undefined) return value
    decodedChars += value.length
    if (typeof limits.maxDecodedChars === 'number' && decodedChars > limits.maxDecodedChars) {
      throw new HarToStepsLimitError('decoded_content', `HAR decoded content exceeds ${limits.maxDecodedChars} characters`)
    }
    return value
  }
  const chargeHeaders = (headers?: HarHeader[]) => {
    if (!headers) return
    for (const header of headers) {
      chargeDecodedContent(header.name)
      chargeDecodedContent(header.value)
    }
  }
  let id = 0
  for (const entry of entries) {
    if (isStaticAsset(entry)) continue
    const requestBody = chargeDecodedContent(decodeHARText(entry.request?.postData?.text, entry.request?.postData?.encoding))
    const responseBody = chargeDecodedContent(decodeHARText(entry.response?.content?.text, entry.response?.content?.encoding))
    chargeDecodedContent(entry.request?.url)
    chargeHeaders(entry.request?.headers)
    chargeHeaders(entry.response?.headers)
    steps.push({
      id: id++,
      request_url: entry.request?.url,
      request_body: requestBody,
      request_headers: headerArrayToMap(entry.request?.headers),
      response_body: responseBody,
      response_headers: headerArrayToMap(entry.response?.headers),
      response_status: entry.response?.status,
    })
  }
  return steps
}
