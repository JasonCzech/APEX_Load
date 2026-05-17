import {
  type ScriptActionDefinition,
  type ScriptConfig,
  type ScriptDataSource,
  type ScriptDefinition,
  type ScriptParameterDefinition,
} from '../types/scripts'

export type AuthoringIssueSeverity = 'error' | 'warning'

export interface AuthoringIssue {
  id: string
  severity: AuthoringIssueSeverity
  message: string
  actionIndex?: number
}

export interface ActionTemplateDefinition {
  key: string
  label: string
  description: string
  action: ScriptActionDefinition
}

export interface ImportedDraftDiffSummary {
  changed: boolean
  configChanged: boolean
  actionChanges: number
  parameterChanges: number
  dataSourceChanges: number
  details: string[]
}

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

const PARAM_TOKEN = /{{\s*param\.([a-zA-Z0-9_]+)\s*}}/g
const DATA_TOKEN = /{{\s*data\.([a-zA-Z0-9_]+)\.([a-zA-Z0-9_]+)\s*}}/g

function cloneScript<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

function normalizeTokenName(value: string): string {
  return value.trim().toLowerCase()
}

function pushIssue(issues: AuthoringIssue[], issue: AuthoringIssue) {
  issues.push(issue)
}

function collectActionStrings(action: ScriptActionDefinition): string[] {
  return [
    action.target || '',
    action.selector || '',
    action.value || '',
    action.body || '',
    ...Object.values(action.headers || {}),
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

function actionNeedsExplicitTarget(action: ScriptActionDefinition): boolean {
  if (action.type.startsWith('browser_')) {
    return action.type !== 'browser_screenshot'
  }
  return !['jms_ack', 'jms_commit', 'jms_rollback'].includes(action.type)
}

export function collectAuthoringIssues(script: ScriptDefinition): AuthoringIssue[] {
  const issues: AuthoringIssue[] = []
  const parameterNames = new Set((script.parameters || []).map((parameter) => normalizeTokenName(parameter.name || '')).filter(Boolean))
  const dataSources = new Set((script.data_sources || []).map((source) => normalizeTokenName(source.name || '')).filter(Boolean))

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

  const seenActionNames = new Map<string, number[]>()
  script.actions.forEach((action, actionIndex) => {
    const normalizedName = normalizeTokenName(action.name || '')
    if (normalizedName) {
      seenActionNames.set(normalizedName, [...(seenActionNames.get(normalizedName) || []), actionIndex])
    }
  })

  script.actions.forEach((action, actionIndex) => {
    if (!action.name.trim()) {
      pushIssue(issues, {
        id: `action:${actionIndex}:name`,
        severity: 'error',
        actionIndex,
        message: 'Action name is required.',
      })
    }

    if (actionNeedsExplicitTarget(action) && !action.target.trim()) {
      pushIssue(issues, {
        id: `action:${actionIndex}:target`,
        severity: 'error',
        actionIndex,
        message: 'Action target is required for this protocol step.',
      })
    }

    if (action.type === 'browser_fill' || action.type === 'browser_select') {
      if (!String(action.value || '').trim()) {
        pushIssue(issues, {
          id: `action:${actionIndex}:value`,
          severity: 'warning',
          actionIndex,
          message: `${action.type} is missing a value.`,
        })
      }
    }

    if (action.type.startsWith('browser_') && action.type !== 'browser_navigate' && action.type !== 'browser_screenshot' && !action.selector?.trim() && !action.target.trim()) {
      pushIssue(issues, {
        id: `action:${actionIndex}:selector`,
        severity: 'error',
        actionIndex,
        message: `${action.type} needs a selector or target.`,
      })
    }

    ;(action.extractors || []).forEach((extractor, extractorIndex) => {
      if (!extractor.name.trim() || !extractor.expression.trim()) {
        pushIssue(issues, {
          id: `action:${actionIndex}:extractor:${extractorIndex}`,
          severity: 'warning',
          actionIndex,
          message: `Extractor ${extractorIndex + 1} is missing a name or expression.`,
        })
      }
    })

    for (const value of collectActionStrings(action)) {
      for (const match of value.matchAll(PARAM_TOKEN)) {
        if (!parameterNames.has(normalizeTokenName(match[1] || ''))) {
          pushIssue(issues, {
            id: `action:${actionIndex}:param:${match[1]}`,
            severity: 'error',
            actionIndex,
            message: `Referenced parameter "${match[1]}" is not defined.`,
          })
        }
      }

      for (const match of value.matchAll(DATA_TOKEN)) {
        if (!dataSources.has(normalizeTokenName(match[1] || ''))) {
          pushIssue(issues, {
            id: `action:${actionIndex}:data:${match[1]}`,
            severity: 'warning',
            actionIndex,
            message: `Referenced data source "${match[1]}" is not defined.`,
          })
        }
      }
    }
  })

  for (const [name, actionIndexes] of seenActionNames.entries()) {
    if (actionIndexes.length <= 1) continue
    actionIndexes.forEach((actionIndex) => {
      pushIssue(issues, {
        id: `action:${actionIndex}:duplicate-name:${name}`,
        severity: 'warning',
        actionIndex,
        message: `Action name "${script.actions[actionIndex]?.name || name}" is duplicated.`,
      })
    })
  }

  return issues
}

export function groupIssuesByAction(issues: AuthoringIssue[], actionCount: number): AuthoringIssue[][] {
  const grouped = Array.from({ length: actionCount }, () => [] as AuthoringIssue[])
  issues.forEach((issue) => {
    if (typeof issue.actionIndex === 'number' && issue.actionIndex >= 0 && issue.actionIndex < grouped.length) {
      grouped[issue.actionIndex].push(issue)
    }
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
          action: { name: `POST_${sequence}`, type: 'POST', method: 'POST', target: '/orders', headers: { 'Content-Type': 'application/json' }, body: '{\n  \"id\": \"{{param.order_id}}\"\n}', extractors: [], assertions: [{ type: 'status_code', expected: 200 }], think_time_after: true },
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

function compareConfig(left: ScriptConfig, right: ScriptConfig): boolean {
  return JSON.stringify(left || {}) !== JSON.stringify(right || {})
}

function compareNamedList(left: Array<{ name?: string | null }>, right: Array<{ name?: string | null }>): number {
  const leftNames = new Set(left.map((item) => normalizeTokenName(item.name || '')).filter(Boolean))
  const rightNames = new Set(right.map((item) => normalizeTokenName(item.name || '')).filter(Boolean))
  let delta = 0
  leftNames.forEach((name) => {
    if (!rightNames.has(name)) delta += 1
  })
  rightNames.forEach((name) => {
    if (!leftNames.has(name)) delta += 1
  })
  return delta
}

export function buildImportedDraftDiffSummary(baseline: ScriptDefinition | null | undefined, current: ScriptDefinition): ImportedDraftDiffSummary {
  if (!baseline) {
    return {
      changed: false,
      configChanged: false,
      actionChanges: 0,
      parameterChanges: 0,
      dataSourceChanges: 0,
      details: [],
    }
  }

  const details: string[] = []
  const configChanged = compareConfig(baseline.config, current.config)
  if (configChanged) {
    details.push('Runtime or protocol configuration has changed.')
  }

  let actionChanges = Math.abs(baseline.actions.length - current.actions.length)
  const renamedActions: string[] = []
  const changedActionTargets: string[] = []
  const comparedActionCount = Math.min(baseline.actions.length, current.actions.length)
  for (let index = 0; index < comparedActionCount; index += 1) {
    const before = baseline.actions[index]
    const after = current.actions[index]
    if (before.name !== after.name) {
      actionChanges += 1
      renamedActions.push(`${before.name || `Action ${index + 1}`} -> ${after.name || `Action ${index + 1}`}`)
    } else if (
      before.type !== after.type
      || before.target !== after.target
      || before.body !== after.body
      || JSON.stringify(before.headers || {}) !== JSON.stringify(after.headers || {})
    ) {
      actionChanges += 1
      changedActionTargets.push(after.name || `Action ${index + 1}`)
    }
  }

  if (renamedActions.length > 0) {
    details.push(`Renamed ${renamedActions.length} action${renamedActions.length === 1 ? '' : 's'}: ${renamedActions.slice(0, 3).join(', ')}${renamedActions.length > 3 ? ', ...' : ''}`)
  }
  if (changedActionTargets.length > 0) {
    details.push(`Updated ${changedActionTargets.length} action target or body definition${changedActionTargets.length === 1 ? '' : 's'}.`)
  }
  if (baseline.actions.length !== current.actions.length) {
    details.push(`Action count changed from ${baseline.actions.length} to ${current.actions.length}.`)
  }

  const parameterChanges = compareNamedList(baseline.parameters || [], current.parameters || [])
  if (parameterChanges > 0) {
    details.push(`Parameter definitions changed across ${parameterChanges} named entry${parameterChanges === 1 ? '' : 'ies'}.`)
  }

  const dataSourceChanges = compareNamedList(baseline.data_sources || [], current.data_sources || [])
  if (dataSourceChanges > 0) {
    details.push(`Data sources changed across ${dataSourceChanges} named entry${dataSourceChanges === 1 ? '' : 'ies'}.`)
  }

  return {
    changed: configChanged || actionChanges > 0 || parameterChanges > 0 || dataSourceChanges > 0,
    configChanged,
    actionChanges,
    parameterChanges,
    dataSourceChanges,
    details,
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

  if (script.config.think_time?.ignore || (script.config.think_time?.mean_ms || 0) <= 0) {
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
    summary: `Applied fixed-delay pacing at ${nextScript.config.pacing.delay_ms} ms per iteration.`,
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
