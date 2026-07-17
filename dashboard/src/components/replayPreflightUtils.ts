import type { ReplayPreflight, ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import type { ProjectSecretStatus } from '../types/secrets'

type ScriptWithPhases = ScriptDefinition & {
  init?: ScriptActionDefinition[]
  teardown?: ScriptActionDefinition[]
}

const REPLAY_TEMPLATE_REF_PATTERN = /\{\{\s*(var|env|data|param|secret)\.([a-zA-Z0-9_.-]+)/g
const REPLAY_PREFLIGHT_MAX_CONTROL_DEPTH = 32
const DEDICATED_PROJECT_SECRET_FIELDS = new Set([
  'dsn_secret',
  'sasl_password_secret',
  'connection_string_secret',
  'ca_cert_secret',
])
const NON_EXECUTABLE_SCRIPT_ROOT_FIELDS = new Set([
  'id',
  'ref_key',
  'name',
  'version',
  'protocol',
  'project_id',
  'folder_id',
  'generated_by',
  'generated_from',
  'metadata',
  'created_at',
  'updated_at',
])

function canonicalProjectSecretName(value: string): string {
  return value.trim().toUpperCase().replace(/[.-]/g, '_')
}

export function replayPreflightStatusLabel(preflight?: ReplayPreflight | null) {
  if (!preflight) return 'Not checked'
  if (!preflight.valid) return 'Needs attention'
  if ((preflight.checks || []).some(check => check.status === 'warn')) return 'Ready with notes'
  return 'Ready'
}

function phaseReplayActions(script: ScriptDefinition): ScriptActionDefinition[] {
  const phasedScript = script as ScriptWithPhases
  return [
    ...(phasedScript.init || []),
    ...(script.actions || []),
    ...(phasedScript.teardown || []),
  ]
}

function controlShellAction(action: ScriptActionDefinition): ScriptActionDefinition {
  if (!action.control) return action
  return {
    ...action,
    control: {
      condition: action.control.condition,
      count: action.control.count,
      max_iterations: action.control.max_iterations,
    },
  }
}

function actionDefinitionCount(actions: ScriptActionDefinition[], depth: number): number {
  return actions.reduce((count, action) => {
    switch (action.type?.trim().toLowerCase()) {
      case 'control_if':
        if (!action.control || depth >= REPLAY_PREFLIGHT_MAX_CONTROL_DEPTH) return count
        return count
          + actionDefinitionCount(action.control.steps || [], depth + 1)
          + actionDefinitionCount(action.control.else || [], depth + 1)
      case 'control_loop':
        if (!action.control || depth >= REPLAY_PREFLIGHT_MAX_CONTROL_DEPTH) return count
        return count + actionDefinitionCount(action.control.steps || [], depth + 1)
      default:
        return count + 1
    }
  }, 0)
}

function expectedReplayActionCount(actions: ScriptActionDefinition[], depth: number): number {
  return actions.reduce((count, action) => {
    switch (action.type?.trim().toLowerCase()) {
      case 'control_if': {
        if (!action.control || depth >= REPLAY_PREFLIGHT_MAX_CONTROL_DEPTH) return count
        const thenCount = expectedReplayActionCount(action.control.steps || [], depth + 1)
        const elseCount = expectedReplayActionCount(action.control.else || [], depth + 1)
        return count + Math.max(thenCount, elseCount)
      }
      case 'control_loop': {
        if (!action.control || depth >= REPLAY_PREFLIGHT_MAX_CONTROL_DEPTH) return count
        const bodyCount = expectedReplayActionCount(action.control.steps || [], depth + 1)
        const passes = action.control.count && action.control.count > 0 ? action.control.count : 1
        return count + (bodyCount * passes)
      }
      default:
        return count + 1
    }
  }, 0)
}

function sortedValues(values: Set<string>): string[] {
  return Array.from(values).sort()
}

function collectTemplateRefs(value: unknown, kind: string): string[] {
  const payload = JSON.stringify(value)
  const refs = new Set<string>()
  REPLAY_TEMPLATE_REF_PATTERN.lastIndex = 0
  let match = REPLAY_TEMPLATE_REF_PATTERN.exec(payload)
  while (match) {
    if (match[1] === kind && match[2]) {
      refs.add(kind === 'secret' ? canonicalProjectSecretName(match[2]) : match[2])
    }
    match = REPLAY_TEMPLATE_REF_PATTERN.exec(payload)
  }
  return sortedValues(refs)
}

function isReviewOnlyAction(value: Record<string, unknown>): boolean {
  if (!('name' in value) || !('target' in value)) return false
  const type = typeof value.type === 'string' ? value.type.trim().toLowerCase() : ''
  return type === 'source_review' || type === 'review_required'
}

function collectDedicatedProjectSecretRefs(value: unknown, refs: Set<string>, root = false, dedicatedField = false): void {
  if (Array.isArray(value)) {
    value.forEach(item => collectDedicatedProjectSecretRefs(item, refs, false, dedicatedField))
    return
  }
  if (value && typeof value === 'object') {
    const record = value as Record<string, unknown>
    if (isReviewOnlyAction(record)) return
    Object.entries(record).forEach(([key, child]) => {
      const normalizedKey = key.trim().toLowerCase()
      if (root && NON_EXECUTABLE_SCRIPT_ROOT_FIELDS.has(normalizedKey)) return
      collectDedicatedProjectSecretRefs(child, refs, false, DEDICATED_PROJECT_SECRET_FIELDS.has(normalizedKey))
    })
    return
  }
  if (!dedicatedField || typeof value !== 'string' || value.includes('{{')) return
  const name = canonicalProjectSecretName(value)
  if (/^[A-Z0-9_]{1,249}$/.test(name)) refs.add(name)
}

export function reconcileReplayPreflightSecretStatus(
  preflight: ReplayPreflight,
  statuses: readonly ProjectSecretStatus[],
  statusKnown: boolean,
  statusUnavailable = false,
): ReplayPreflight {
  const required = Array.from(new Set((preflight.required_secrets || []).map(canonicalProjectSecretName).filter(Boolean))).sort()
  if (!statusKnown) {
    return {
      ...preflight,
      valid: required.length === 0 ? preflight.valid : false,
      required_secrets: required,
      // A local draft preflight deliberately treats every reference as missing.
      // Until the project-scoped status probe returns, do not present that
      // pessimistic placeholder as an authoritative missing-secret result.
      missing_secrets: [],
      checks: (preflight.checks || []).map(check => check.id === 'secrets' && required.length > 0
        ? {
            ...check,
            status: 'warn' as const,
            detail: statusUnavailable
              ? 'Project secret status is temporarily unavailable.'
              : 'Checking project secret configuration…',
          }
        : check),
    }
  }

  const configured = new Set(statuses.filter(status => status.configured).map(status => canonicalProjectSecretName(status.name)))
  const missing = required.filter(name => !configured.has(name))
  const checks = (preflight.checks || []).map(check => {
    if (check.id !== 'secrets') return check
    if (required.length === 0) {
      return { ...check, status: 'pass' as const, detail: 'No project secret references detected.' }
    }
    if (missing.length === 0) {
      return {
        ...check,
        status: 'pass' as const,
        detail: `${required.length} project secret${required.length === 1 ? '' : 's'} configured.`,
      }
    }
    return {
      ...check,
      status: 'fail' as const,
      detail: `Configure missing project secret${missing.length === 1 ? '' : 's'}: ${missing.join(', ')}.`,
    }
  })
  return {
    ...preflight,
    // Once project-scoped status is authoritative, a missing value is a hard
    // runtime prerequisite. The draft remains saveable, but launch must stop.
    valid: missing.length === 0 ? preflight.valid : false,
    required_secrets: required,
    missing_secrets: missing,
    checks,
  }
}

function sequenceChildExtractors(action: ScriptActionDefinition) {
  if (action.type?.trim().toLowerCase() !== 'http_sequence') return []
  const requests = action.params && Array.isArray(action.params.requests)
    ? action.params.requests
    : []
  return requests.flatMap(request => Array.isArray(request?.extractors) ? request.extractors : [])
}

type ReadinessVarContext = {
  available: Set<string>
  missing: Set<string>
  secrets: Set<string>
  loopDepth: number
  foreachVars: Set<string>
}

// isReadinessVarAvailable reports whether a {{var.<name>}} reference resolves to a
// variable the runtime will provide: a seeded/param/extracted variable, the
// engine-injected {{var.loop_index}} inside any control_loop body, or a foreach
// row binding {{var.<V>.<col>}} inside a control_loop whose for_each.var is V.
function isReadinessVarAvailable(name: string, ctx: ReadinessVarContext): boolean {
  if (ctx.available.has(name)) return true
  if (ctx.loopDepth > 0 && (name === 'loop_index' || name.startsWith('loop_index.'))) return true
  const prefix = name.includes('.') ? name.slice(0, name.indexOf('.')) : name
  return ctx.foreachVars.has(prefix)
}

// collectReadinessRefs walks the action tree in execution order, tracking which
// loop-injected variables are in scope so a control body does not false-flag
// {{var.loop_index}} or foreach row bindings ({{var.<V>.<col>}}) as missing.
// Extractor outputs accumulate forward, matching replay's single-pass variable
// resolution. Container actions contribute only their shell (condition) refs here;
// their bodies are walked recursively with the loop scope applied.
function collectReadinessRefs(actions: ScriptActionDefinition[], ctx: ReadinessVarContext, depth: number): void {
  if (depth > REPLAY_PREFLIGHT_MAX_CONTROL_DEPTH) return
  actions.forEach(action => {
    if (isReviewOnlyAction(action as unknown as Record<string, unknown>)) return
    const type = action.type?.trim().toLowerCase()
    const isLoop = type === 'control_loop'
    const isIf = type === 'control_if'
    const refSource = isLoop || isIf ? controlShellAction(action) : action
    sequenceChildExtractors(action).forEach(extractor => {
      if (extractor.name) ctx.available.add(extractor.name)
    })
    // A control_loop's own while-condition is evaluated each pass with loop_index
    // already set, so scan the shell with the loop scope applied.
    const refCtx: ReadinessVarContext = isLoop ? { ...ctx, loopDepth: ctx.loopDepth + 1 } : ctx
    collectTemplateRefs(refSource, 'var').forEach(name => {
      if (!isReadinessVarAvailable(name, refCtx)) ctx.missing.add(name)
    })
    collectTemplateRefs(refSource, 'secret').forEach(name => ctx.secrets.add(name))
    ;(action.extractors || []).forEach(extractor => {
      if (extractor.name) ctx.available.add(extractor.name)
    })
    if ((isLoop || isIf) && action.control) {
      const childCtx: ReadinessVarContext = {
        available: ctx.available,
        missing: ctx.missing,
        secrets: ctx.secrets,
        loopDepth: isLoop ? ctx.loopDepth + 1 : ctx.loopDepth,
        foreachVars: ctx.foreachVars,
      }
      const foreachVar = isLoop ? action.control.for_each?.var?.trim() : ''
      if (foreachVar) {
        childCtx.foreachVars = new Set(ctx.foreachVars)
        childCtx.foreachVars.add(foreachVar)
      }
      collectReadinessRefs(action.control.steps || [], childCtx, depth + 1)
      if (isIf) collectReadinessRefs(action.control.else || [], childCtx, depth + 1)
    }
  })
}

function buildDataWarnings(script: ScriptDefinition): string[] {
  const sourceNames = new Set<string>()
  const warnings: string[] = []

  ;(script.data_sources || []).forEach(source => {
    const name = source.name?.trim()
    if (name) sourceNames.add(name)
    const hasInlineData = Boolean(source.inline_data?.trim())
    const hasPath = Boolean(source.path?.trim())
    const hasStorageRef = Boolean(source.storage_ref?.trim())
    if (!hasPath && !hasInlineData && !hasStorageRef) {
      const requirement = source.type === 'file' ? 'an uploaded governed asset' : 'a file path or inline data'
      warnings.push(`Data source ${name || 'unnamed'} needs ${requirement}.`)
    }
  })

  ;(script.parameters || []).forEach(parameter => {
    const sourceName = parameter.data_source?.trim()
    if (sourceName && !sourceNames.has(sourceName)) {
      warnings.push(`Parameter ${parameter.name || 'unnamed'} references missing data source ${sourceName}.`)
    }
  })

  return warnings.sort()
}

function runtimePrerequisites(script: ScriptDefinition, browserRequired: boolean): string[] {
  if (!browserRequired) {
    return [`Adapter: ${script.protocol}`]
  }
  return [
    `Browser engine: ${script.config.browser?.engine || 'chromium'}`,
    `Screenshot capture: ${script.config.browser?.capture_screenshots || 'per_action'}`,
  ]
}

function browserEvidenceCaptureCheck(script: ScriptDefinition) {
  const capture = script.config.browser?.capture_screenshots?.trim() || 'per_action'
  const captureDisabled = ['off', 'none', 'disabled'].includes(capture.toLowerCase())
  return {
    id: 'evidence_capture',
    label: 'Evidence capture',
    status: captureDisabled ? 'warn' : 'pass',
    detail: captureDisabled
      ? 'Screenshot capture is disabled; enable per_action or on_failure before using replay as promotion evidence.'
      : 'Replay will capture screenshot, DOM, console, and network evidence for browser review.',
  }
}

export function buildLocalReplayPreflight(script: ScriptDefinition, variables?: Record<string, string>): ReplayPreflight {
  const phaseActions = phaseReplayActions(script)
  const definitionCount = actionDefinitionCount(phaseActions, 0)
  const expectedCount = expectedReplayActionCount(phaseActions, 0)
  const availableVariables = new Set<string>([
    ...Object.keys(variables || {}).filter(Boolean),
    ...(script.parameters || []).map(parameter => parameter.name).filter(Boolean),
  ])
  const missingVariables = new Set<string>()
  const requiredSecrets = new Set<string>(collectTemplateRefs(script.config, 'secret'))
  collectDedicatedProjectSecretRefs(script, requiredSecrets, true)

  collectReadinessRefs(phaseActions, {
    available: availableVariables,
    missing: missingVariables,
    secrets: requiredSecrets,
    loopDepth: 0,
    foreachVars: new Set<string>(),
  }, 0)

  const dataWarnings = buildDataWarnings(script)
  const missingVariableList = sortedValues(missingVariables)
  const requiredSecretList = sortedValues(requiredSecrets)
  const browserRequired = script.protocol === 'browser'
  const prerequisites = runtimePrerequisites(script, browserRequired)
  const hasRequiredFields = Boolean(script.name && definitionCount > 0)

  const checks = [
    {
      id: 'validation',
      label: 'Script validation',
      status: hasRequiredFields ? 'pass' : 'fail',
      detail: hasRequiredFields ? 'Ready' : 'Script name and actions are required.',
    },
    {
      id: 'variables',
      label: 'Variable readiness',
      status: missingVariableList.length > 0 ? 'fail' : 'pass',
      detail: missingVariableList.length > 0 ? missingVariableList.join(', ') : 'All replay variables are seeded or extracted before use.',
    },
    {
      id: 'secrets',
      label: 'Secret bindings',
      status: requiredSecretList.length > 0 ? 'warn' : 'pass',
      detail: requiredSecretList.length > 0 ? requiredSecretList.join(', ') : 'No project secret references detected.',
    },
    {
      id: 'data_sources',
      label: 'Data source readiness',
      status: dataWarnings.length > 0 ? 'fail' : (script.data_sources?.length ? 'warn' : 'pass'),
      detail: dataWarnings.length > 0 ? dataWarnings.join('; ') : `${script.data_sources?.length || 0} data source${(script.data_sources?.length || 0) === 1 ? '' : 's'} configured.`,
    },
    {
      id: 'runtime',
      label: 'Runtime prerequisites',
      status: browserRequired ? 'warn' : 'pass',
      detail: prerequisites.join('; '),
    },
  ]
  if (browserRequired) checks.push(browserEvidenceCaptureCheck(script))

  return {
    valid: hasRequiredFields && missingVariableList.length === 0 && dataWarnings.length === 0,
    action_count: definitionCount,
    expected_action_count: expectedCount,
    data_source_count: script.data_sources?.length || 0,
    parameter_count: script.parameters?.length || 0,
    browser_required: browserRequired,
    missing_variables: missingVariableList,
    required_secrets: requiredSecretList,
    missing_secrets: requiredSecretList,
    runtime_prerequisites: prerequisites,
    warnings: dataWarnings,
    checks,
  }
}
