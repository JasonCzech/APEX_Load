import { describe, expect, it } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import {
  DEBUG_OPTION_OWNED_PATHS,
  captureOverrideEntries,
  draftSessionRequestBody,
  effectiveDebugValue,
  isDebugSessionOverridablePath,
  nativeReplaySessionOverridesNote,
  promoteSessionOverrides,
  resetSessionOverride,
  sessionOverrideCount,
  sessionOverrideProvenance,
  sessionRuntimeOverrides,
  setSessionOverride,
  storedSessionRequestBody,
  storedSessionSendsScriptBody,
} from './debugSessionOverrides'
import {
  DEBUG_OVERRIDE_FIELDS,
  debugOverrideFieldsFor,
  sessionHeaderOverrides,
  unrenderedSessionOverrides,
} from './debugSessionOverrideFields'
import { normalizeRuntimeOverrideFieldPath, setOverrideField } from './runtimeSettingsModel'

const HTTP_SCRIPT: ScriptDefinition = {
  name: 'Checkout',
  version: '1.0',
  protocol: 'http',
  config: {
    base_url: 'https://shop.example.test',
    http: { request_timeout_ms: 45000, keep_alive: false, default_headers: { 'X-Script': 'yes' } },
    log: { level: 'standard' },
  },
  actions: [{ name: 'Home', type: 'request', method: 'GET', target: '/' }],
}

describe('debug session overrides model', () => {
  it('lists each edited path in override_fields and leaves untouched siblings out', () => {
    const draft = setSessionOverride(undefined, 'http.request_timeout_ms', 5000)
    expect(draft?.override_fields).toEqual(['http.request_timeout_ms'])
    const row = sessionRuntimeOverrides(draft)
    expect(row).toEqual({ http: { request_timeout_ms: 5000 }, override_fields: ['http.request_timeout_ms'] })
  })

  it('refuses the paths Debug options own and the pacing the debugger skips', () => {
    for (const path of [...Object.keys(DEBUG_OPTION_OWNED_PATHS), 'log', 'think_time', 'error_handling', 'run_logic', 'pacing.type', 'pacing', 'iterations', 'log_level']) {
      expect(isDebugSessionOverridablePath(path)).toBe(false)
      expect(setSessionOverride(undefined, path, 'x')).toBeUndefined()
    }
    expect(isDebugSessionOverridablePath('log.max_response_bytes')).toBe(true)
    expect(isDebugSessionOverridablePath('think_time.mean_ms')).toBe(true)
  })

  it('never sends an owned path even when a draft carries one', () => {
    // A draft built outside the session editor (a profile, an old state) with log.level.
    const tainted = setOverrideField(setOverrideField(undefined, 'log.level', 'debug'), 'base_url', 'https://other.example.test')
    expect(sessionRuntimeOverrides(tainted)).toEqual({ base_url: 'https://other.example.test', override_fields: ['base_url'] })
    expect(sessionOverrideCount(tainted)).toBe(1)
  })

  it('resets one path, keeps the rest and drops an empty draft', () => {
    let draft = setSessionOverride(undefined, 'http.request_timeout_ms', 5000)
    draft = setSessionOverride(draft, 'http.connect_timeout_ms', 2000)
    draft = resetSessionOverride(draft, 'http.request_timeout_ms')
    expect(draft?.override_fields).toEqual(['http.connect_timeout_ms'])
    expect(resetSessionOverride(draft, 'http.connect_timeout_ms')).toBeUndefined()
  })

  it('sends no runtime_overrides at all for an empty session, so no v1 row reaches the server', () => {
    expect(sessionRuntimeOverrides(undefined)).toBeUndefined()
    expect(draftSessionRequestBody({ script: HTTP_SCRIPT, group_name: 'g' })).toEqual({ script: HTTP_SCRIPT, group_name: 'g' })
  })

  it('keys every overridden path to session_override provenance', () => {
    const draft = setSessionOverride(setSessionOverride(undefined, 'base_url', 'https://x.test'), 'http.keep_alive', true)
    expect(sessionOverrideProvenance(draft)).toEqual({ base_url: 'session_override', 'http.keep_alive': 'session_override' })
  })

  it('resolves a value session override → script → engine default', () => {
    const engine = { base_url: '', http: { request_timeout_ms: 60000, connect_timeout_ms: 30000 } }
    const draft = setSessionOverride(undefined, 'http.connect_timeout_ms', 1500)
    expect(effectiveDebugValue('http.connect_timeout_ms', { session: draft, script: HTTP_SCRIPT.config, engine })).toEqual({ value: 1500, provenance: 'session_override' })
    expect(effectiveDebugValue('http.request_timeout_ms', { session: draft, script: HTTP_SCRIPT.config, engine })).toEqual({ value: 45000, provenance: 'script' })
    expect(effectiveDebugValue('http.request_timeout_ms', { script: { base_url: '' }, engine })).toEqual({ value: 60000, provenance: 'engine_default' })
  })
})

describe('debug and replay request bodies', () => {
  const REVISION = `scr-v1-${'c'.repeat(64)}`
  const draft = setSessionOverride(setSessionOverride(undefined, 'http.request_timeout_ms', 5000), 'base_url', 'https://staging.example.test')

  it('sends an unmodified stored script as runtime_overrides only, never its body', () => {
    const body = storedSessionRequestBody(
      { script: { ...HTTP_SCRIPT, project_id: 'p1' }, group_name: 'Checkout-debug', log_level: 'info', runtime_overrides: { base_url: 'stale' } },
      { script: HTTP_SCRIPT, unsavedEdits: false, baselineRevision: REVISION, capture: 'diagnostic', networkTrace: true, overrides: draft },
    )
    expect(body).not.toHaveProperty('script')
    expect(body.expected_content_revision).toBe(REVISION)
    expect(body.group_name).toBe('Checkout-debug')
    expect(body.log_level).toBe('info')
    // The debugger's diagnostic capture travels as overrides, with the session's own fields.
    expect(body.runtime_overrides?.override_fields).toEqual(['log.extended_options', 'results.message_detail', 'http.request_timeout_ms', 'base_url'])
    expect(body.runtime_overrides?.log?.extended_options).toEqual(['advanced_trace'])
    expect(body.runtime_overrides?.results?.message_detail).toMatchObject({ mode: 'sampled', max_items: 100 })
    expect(body.runtime_overrides?.http).toEqual({ request_timeout_ms: 5000 })
    expect(body.runtime_overrides?.base_url).toBe('https://staging.example.test')
  })

  it('sends neither a body nor runtime_overrides for a stored script with script capture and no session overrides', () => {
    const body = storedSessionRequestBody({ script: HTTP_SCRIPT, group_name: 'g' }, { script: HTTP_SCRIPT, unsavedEdits: false, baselineRevision: REVISION, capture: 'script', networkTrace: true })
    expect(body).toEqual({ group_name: 'g', expected_content_revision: REVISION })
  })

  it('keeps the body of a clean stored script when no baseline revision can pin the run', () => {
    for (const baselineRevision of [undefined, null, '', '  ']) {
      const captured = { ...HTTP_SCRIPT, name: 'Checkout (captured)' }
      const body = storedSessionRequestBody({ script: captured, group_name: 'g' }, { script: HTTP_SCRIPT, unsavedEdits: false, baselineRevision, capture: 'diagnostic', networkTrace: true, overrides: draft })
      expect(body.script).toBe(captured)
      expect(body).not.toHaveProperty('expected_content_revision')
      expect(body.runtime_overrides?.override_fields).toEqual(['http.request_timeout_ms', 'base_url'])
    }
  })

  it('reports a body-sending start exactly when the request builder keeps the body', () => {
    for (const [unsavedEdits, baselineRevision] of [[false, REVISION], [false, undefined], [false, null], [false, '  '], [true, REVISION], [true, undefined]] as const) {
      const body = storedSessionRequestBody({ script: HTTP_SCRIPT, group_name: 'g' }, { script: HTTP_SCRIPT, unsavedEdits, baselineRevision, capture: 'script', networkTrace: true })
      expect(storedSessionSendsScriptBody({ unsavedEdits, baselineRevision })).toBe('script' in body)
    }
    expect(storedSessionSendsScriptBody({ unsavedEdits: false, baselineRevision: undefined })).toBe(true)
    expect(storedSessionSendsScriptBody({ unsavedEdits: false, baselineRevision: REVISION })).toBe(false)
  })

  it('notes on native browser replay only while session overrides exist', () => {
    expect(nativeReplaySessionOverridesNote(undefined)).toBeUndefined()
    const one = setSessionOverride(undefined, 'base_url', 'https://staging.example.test')
    expect(nativeReplaySessionOverridesNote(one)).toBe('1 session runtime override is not applied here. Session runtime overrides apply to debug runs only. Native browser replay uses the script’s runtime settings.')
    expect(nativeReplaySessionOverridesNote(setSessionOverride(one, 'think_time.mean_ms', 10))).toMatch(/^2 session runtime overrides are not applied here\./)
  })

  it('keeps the draft body when the script has unsaved edits and adds only the session overrides', () => {
    const edited = { ...HTTP_SCRIPT, name: 'Checkout (edited)' }
    const body = storedSessionRequestBody({ script: edited, group_name: 'g' }, { script: edited, unsavedEdits: true, baselineRevision: REVISION, capture: 'diagnostic', networkTrace: true, overrides: draft })
    expect(body.script).toBe(edited)
    // The draft body is what runs, so nothing pins it to a stored revision.
    expect(body).not.toHaveProperty('expected_content_revision')
    expect(body.runtime_overrides?.override_fields).toEqual(['http.request_timeout_ms', 'base_url'])
  })

  it('adds the session overrides to a draft (no script id) body', () => {
    const body = draftSessionRequestBody({ script: HTTP_SCRIPT, group_name: 'g' }, draft)
    expect(body.script).toBe(HTTP_SCRIPT)
    expect(body.runtime_overrides?.override_fields).toEqual(['http.request_timeout_ms', 'base_url'])
  })

  it('expresses capture as overrides only where captureScript would change the script', () => {
    expect(captureOverrideEntries({ ...HTTP_SCRIPT, protocol: 'browser' }, 'diagnostic', true)).toEqual([])
    expect(captureOverrideEntries(HTTP_SCRIPT, 'script', true)).toEqual([])
    expect(captureOverrideEntries(HTTP_SCRIPT, 'errors', true).map(([path]) => path)).toEqual(['results.message_detail'])
    const traced = { ...HTTP_SCRIPT, config: { ...HTTP_SCRIPT.config, log: { level: 'standard', extended_options: ['advanced_trace'] } } }
    expect(captureOverrideEntries(traced, 'diagnostic', false)).toContainEqual(['log.extended_options', []])
  })
})

describe('promoting session overrides', () => {
  it('writes each overridden path into a copy of script.config', () => {
    let draft = setSessionOverride(undefined, 'http.request_timeout_ms', 5000)
    draft = setSessionOverride(draft, 'http.keep_alive', true)
    draft = setSessionOverride(draft, 'http.default_headers.X-Debug.Trace', 'on')
    draft = setSessionOverride(draft, 'soap.version', '1.2')
    const before = structuredClone(HTTP_SCRIPT.config)
    const result = promoteSessionOverrides(HTTP_SCRIPT.config, draft)
    expect(HTTP_SCRIPT.config).toEqual(before)
    expect(result.promoted).toEqual(['http.request_timeout_ms', 'http.keep_alive', 'http.default_headers.X-Debug.Trace', 'soap.version'])
    expect(result.skipped).toEqual([])
    expect(result.config.http).toEqual({ request_timeout_ms: 5000, keep_alive: true, default_headers: { 'X-Script': 'yes', 'X-Debug.Trace': 'on' } })
    expect(result.config.soap).toEqual({ version: '1.2' })
    expect(result.config.base_url).toBe('https://shop.example.test')
  })

  it('removes the script value for an explicit empty override and skips paths outside ScriptConfig', () => {
    let draft = setOverrideField(undefined, 'http.request_timeout_ms', undefined)
    draft = setOverrideField(draft, 'env.REGION', 'eu')
    const result = promoteSessionOverrides(HTTP_SCRIPT.config, draft)
    expect(result.config.http?.request_timeout_ms).toBeUndefined()
    expect(result.promoted).toEqual(['http.request_timeout_ms'])
    expect(result.skipped).toEqual(['env.REGION'])
  })
})

describe('session override fields', () => {
  it('offers only canonical override paths, never one a Debug option owns', () => {
    for (const field of DEBUG_OVERRIDE_FIELDS) {
      expect(normalizeRuntimeOverrideFieldPath(field.path)).toBe(field.path)
      expect(isDebugSessionOverridablePath(field.path)).toBe(true)
    }
  })

  it('offers SOAP fields only to SOAP scripts and HTTP transport only to HTTP-like scripts', () => {
    const soapPaths = debugOverrideFieldsFor('protocol_behavior_session', 'soap').map(field => field.path)
    expect(soapPaths).toContain('soap.version')
    expect(soapPaths).toContain('soap.fault_handling.fail_on_fault')
    expect(debugOverrideFieldsFor('protocol_behavior_session', 'http').map(field => field.path)).not.toContain('soap.version')
    expect(debugOverrideFieldsFor('transport_connections', 'browser')).toEqual([])
    expect(debugOverrideFieldsFor('target_identity', 'browser').map(field => field.path)).toEqual(['base_url'])
    expect(debugOverrideFieldsFor('security_proxy_auth', 'soap').map(field => field.path)).toContain('soap.ws_security.username_token.password')
  })

  it('offers a PAC script URL beside the PAC script proxy mode, so a PAC override can pass validation', () => {
    const fields = debugOverrideFieldsFor('security_proxy_auth', 'http')
    const proxyMode = fields.find(field => field.path === 'http.proxy_mode')
    expect(proxyMode?.options?.map(option => option.value)).toContain('pac')
    expect(fields.find(field => field.path === 'http.proxy.pac_url')).toMatchObject({ kind: 'url', label: 'PAC URL' })
    const draft = setSessionOverride(setSessionOverride(undefined, 'http.proxy_mode', 'pac'), 'http.proxy.pac_url', 'https://proxy.example.test/proxy.pac')
    expect(unrenderedSessionOverrides(draft, 'http')).toEqual([])
    const body = draftSessionRequestBody({ script: HTTP_SCRIPT }, draft)
    expect(body.runtime_overrides?.http).toEqual({ proxy_mode: 'pac', proxy: { pac_url: 'https://proxy.example.test/proxy.pac' } })
  })

  it('lists header overrides by name and reports overrides no control renders', () => {
    let draft = setSessionOverride(undefined, 'http.default_headers.X-Debug', '1')
    draft = setSessionOverride(draft, 'http.page_timeout_ms', 9000)
    expect(sessionHeaderOverrides(draft)).toEqual([{ name: 'X-Debug', path: 'http.default_headers.X-Debug', value: '1' }])
    expect(unrenderedSessionOverrides(draft, 'http')).toEqual(['http.page_timeout_ms'])
    expect(unrenderedSessionOverrides(draft, 'browser')).toEqual(['http.default_headers.X-Debug', 'http.page_timeout_ms'])
  })
})
