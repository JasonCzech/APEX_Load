import type { ReplayPreflight, ScriptActionDefinition, ScriptDefinition } from '../types/scripts'

type ScriptWithPhases = ScriptDefinition & {
  init?: ScriptActionDefinition[]
  teardown?: ScriptActionDefinition[]
}

const REPLAY_TEMPLATE_REF_PATTERN = /\{\{\s*(var|env|data|param)\.([a-zA-Z0-9_.-]+)/g

export function replayPreflightStatusLabel(preflight?: ReplayPreflight | null) {
  if (!preflight) return 'Not checked'
  if (!preflight.valid) return 'Needs attention'
  if ((preflight.checks || []).some(check => check.status === 'warn')) return 'Ready with notes'
  return 'Ready'
}

function orderedReplayActions(script: ScriptDefinition): ScriptActionDefinition[] {
  const phasedScript = script as ScriptWithPhases
  return [
    ...(phasedScript.init || []),
    ...(script.actions || []),
    ...(phasedScript.teardown || []),
  ]
}

function sortedValues(values: Set<string>): string[] {
  return Array.from(values).sort()
}

function collectTemplateRefs(action: ScriptActionDefinition, kind: string): string[] {
  const payload = JSON.stringify(action)
  const refs = new Set<string>()
  REPLAY_TEMPLATE_REF_PATTERN.lastIndex = 0
  let match = REPLAY_TEMPLATE_REF_PATTERN.exec(payload)
  while (match) {
    if (match[1] === kind && match[2]) {
      refs.add(match[2])
    }
    match = REPLAY_TEMPLATE_REF_PATTERN.exec(payload)
  }
  return sortedValues(refs)
}

function buildDataWarnings(script: ScriptDefinition): string[] {
  const sourceNames = new Set<string>()
  const warnings: string[] = []

  ;(script.data_sources || []).forEach(source => {
    const name = source.name?.trim()
    if (name) sourceNames.add(name)
    if (!source.path?.trim() && !source.inline_data?.trim()) {
      warnings.push(`Data source ${name || 'unnamed'} needs a file path or inline data.`)
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

export function buildLocalReplayPreflight(script: ScriptDefinition, variables?: Record<string, string>): ReplayPreflight {
  const orderedActions = orderedReplayActions(script)
  const availableVariables = new Set<string>([
    ...Object.keys(variables || {}).filter(Boolean),
    ...(script.parameters || []).map(parameter => parameter.name).filter(Boolean),
  ])
  const missingVariables = new Set<string>()
  const requiredSecrets = new Set<string>()

  orderedActions.forEach(action => {
    collectTemplateRefs(action, 'var').forEach(name => {
      if (!availableVariables.has(name)) {
        missingVariables.add(name)
      }
    })
    collectTemplateRefs(action, 'env').forEach(name => {
      requiredSecrets.add(name)
    })
    ;(action.extractors || []).forEach(extractor => {
      if (extractor.name) availableVariables.add(extractor.name)
    })
  })

  const dataWarnings = buildDataWarnings(script)
  const missingVariableList = sortedValues(missingVariables)
  const requiredSecretList = sortedValues(requiredSecrets)
  const browserRequired = script.protocol === 'browser'
  const prerequisites = runtimePrerequisites(script, browserRequired)
  const hasRequiredFields = Boolean(script.name && orderedActions.length > 0)

  return {
    valid: hasRequiredFields && missingVariableList.length === 0 && dataWarnings.length === 0,
    action_count: orderedActions.length,
    expected_action_count: orderedActions.length,
    data_source_count: script.data_sources?.length || 0,
    parameter_count: script.parameters?.length || 0,
    browser_required: browserRequired,
    missing_variables: missingVariableList,
    required_secrets: requiredSecretList,
    runtime_prerequisites: prerequisites,
    warnings: dataWarnings,
    checks: [
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
        detail: requiredSecretList.length > 0 ? requiredSecretList.join(', ') : 'No environment secret references detected.',
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
    ],
  }
}
