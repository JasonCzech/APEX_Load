import { describe, expect, it } from 'vitest'

import { type ScenarioRuntimeSettingsDraft, type StoredScript } from '../types'
import { applyEffectiveRuntimeSettingsToGroups, applyEffectiveRuntimeSettingsToGroupsWithReport, copyRuntimeSettingsForProtocol, materializeRuntimeSettingsForApply } from './runtimeSettingsApply'
import { resetOverrideField, runtimeDraftFromOverride, setOverrideField } from './runtimeSettingsModel'

describe('materializeRuntimeSettingsForApply', () => {
  const sourceConfig = {
    base_url: '',
    web_runtime: {
      network_simulation: { mode: 'custom', latency_ms: 25 },
      streaming: { mode: 'adaptive', buffer_size_seconds: 8 },
    },
  }

  it('ignores zero-only network simulation and streaming overlays like Go zero structs', () => {
    const result = materializeRuntimeSettingsForApply(
      undefined,
      sourceConfig,
      {
        log_level: '',
        auto_transactions: '',
        web_runtime: {
          network_simulation: {
            mode: '',
            preset: '',
            bandwidth_kbps: 0,
            latency_ms: 0,
            jitter_ms: 0,
            packet_loss_pct: 0,
          },
          streaming: {
            mode: '',
            buffer_size_seconds: 0,
            fixed_bitrate_kbps: 0,
            bandwidth_estimation: '',
          },
        },
      },
    )

    expect(result?.web_runtime?.network_simulation).toEqual({ mode: 'custom', latency_ms: 25 })
    expect(result?.web_runtime?.streaming).toEqual({ mode: 'adaptive', buffer_size_seconds: 8 })
  })

  it('replaces each web-runtime unit when any corresponding field is non-zero', () => {
    const result = materializeRuntimeSettingsForApply(
      undefined,
      sourceConfig,
      {
        log_level: '',
        auto_transactions: '',
        web_runtime: {
          network_simulation: { latency_ms: 0, jitter_ms: 2 },
          streaming: { buffer_size_seconds: 0, bandwidth_estimation: 'instant' },
        },
      },
    )

    expect(result?.web_runtime?.network_simulation).toEqual({ latency_ms: 0, jitter_ms: 2 })
    expect(result?.web_runtime?.streaming).toEqual({ buffer_size_seconds: 0, bandwidth_estimation: 'instant' })
  })

  it('applies a compact source-group iteration override as the final runtime layer', () => {
    const result = materializeRuntimeSettingsForApply(
      undefined,
      undefined,
      {
        log_level: '',
        auto_transactions: '',
        run_logic: { iterations: 3, workload_model: 'closed' },
      },
      undefined,
      7,
    )

    expect(result?.run_logic).toEqual({ iterations: 7, workload_model: 'closed' })
    expect(result?.iterations).toBeUndefined()
  })

  it('drops the per-JVM count when an override switches a shared script to dedicated', () => {
    const script = { base_url: '', java_vuser: { jvm_sharing: 'shared' as const, vusers_per_jvm: 120, classpath: ['/app.jar'] } }
    const dedicated = materializeRuntimeSettingsForApply(undefined, script, { log_level: '', auto_transactions: '', java_vuser: { jvm_sharing: 'dedicated' } })
    expect(dedicated?.java_vuser).toEqual({ jvm_sharing: 'dedicated', classpath: ['/app.jar'] })
    const resized = materializeRuntimeSettingsForApply(undefined, script, { log_level: '', auto_transactions: '', java_vuser: { vusers_per_jvm: 40 } })
    expect(resized?.java_vuser).toEqual({ jvm_sharing: 'shared', vusers_per_jvm: 40, classpath: ['/app.jar'] })
    // Thread mode shares JVMs too, so the per-JVM count carries over.
    const threads = materializeRuntimeSettingsForApply(undefined, script, { log_level: '', auto_transactions: '', java_vuser: { jvm_sharing: 'threads' } })
    expect(threads?.java_vuser).toEqual({ jvm_sharing: 'threads', vusers_per_jvm: 120, classpath: ['/app.jar'] })
  })

  describe('partial (override_fields) layers', () => {
    // Mirrors applyRuntimeOverridesPartial / mergeRuntimeSettingsPartial: a
    // partial layer replaces only the paths it lists.
    const script = {
      base_url: '',
      http: {
        request_timeout_ms: 300,
        default_headers: { 'X-Script': 'yes' },
        tls: { client_cert_file: 'script.crt', min_version: '1.2' },
        proxy: { url: 'http://script-proxy', username: 'script-user' },
        auth: { type: 'bearer', token: '{{secret.script_token}}' },
      },
      error_handling: { max_retries: 4, retry_delay_ms: 50 },
      log: { level: 'extended', on_error_only: true },
    }

    it('keeps the script headers, TLS, proxy and auth under a one-leaf scenario HTTP edit', () => {
      const scenario = setOverrideField(undefined, 'http.request_timeout_ms', 5000)
      const result = materializeRuntimeSettingsForApply(undefined, script, scenario)
      expect(result?.http).toEqual({ ...script.http, request_timeout_ms: 5000 })
    })

    it('reproduces the data loss of a whole-section v1 scenario row it replaces', () => {
      // The v1 row the editor used to write for the same edit (the engine-default
      // section with one changed field) wipes the script's HTTP settings.
      const result = materializeRuntimeSettingsForApply(undefined, script, { log_level: '', auto_transactions: '', http: { request_timeout_ms: 5000 } })
      expect(result?.http).toEqual({ request_timeout_ms: 5000 })
    })

    it('layers a partial group over a partial scenario default, leaf by leaf', () => {
      const scenario = setOverrideField(setOverrideField(undefined, 'http.request_timeout_ms', 5000), 'error_handling.continue_on_error', false)
      const group = setOverrideField(setOverrideField(undefined, 'http.tls.min_version', '1.3'), 'log.level', 'debug')
      const result = materializeRuntimeSettingsForApply(undefined, script, scenario, group)
      expect(result?.http).toEqual({ ...script.http, request_timeout_ms: 5000, tls: { client_cert_file: 'script.crt', min_version: '1.3' } })
      expect(result?.error_handling ?? {
        continue_on_error: result?.continue_on_error,
        max_retries: result?.max_retries,
        retry_delay_ms: result?.retry_delay_ms,
      }).toEqual({ continue_on_error: false, max_retries: 4, retry_delay_ms: 50 })
      expect(result?.log).toEqual({ level: 'debug', on_error_only: true })
    })

    it('applies a listed path with no value as an explicit clear and a reset path as inherited', () => {
      const cleared = setOverrideField(undefined, 'http.default_headers', undefined)
      expect(materializeRuntimeSettingsForApply(undefined, script, cleared)?.http).not.toHaveProperty('default_headers')
      const reset = resetOverrideField(cleared, 'http.default_headers')
      expect(materializeRuntimeSettingsForApply(undefined, script, reset)?.http?.default_headers).toEqual({ 'X-Script': 'yes' })
    })

    it('applies listed env keys and Java fields onto the lower layers', () => {
      const java = { base_url: '', java_vuser: { jvm_sharing: 'shared' as const, vusers_per_jvm: 120, classpath: ['/app.jar'] } }
      const group = setOverrideField(undefined, 'java_vuser.jvm_sharing', 'dedicated')
      expect(materializeRuntimeSettingsForApply(undefined, java, undefined, group)?.java_vuser).toEqual({ jvm_sharing: 'dedicated', classpath: ['/app.jar'] })
      const scenario = runtimeDraftFromOverride({ env: { A: '1', B: '2' } })
      const groupEnv = setOverrideField(undefined, 'env.B', '3')
      expect(materializeRuntimeSettingsForApply(undefined, undefined, scenario, groupEnv)?.env).toEqual({ A: '1', B: '3' })
    })

    it('drops the scenario env under a listed empty group env, as mergeRuntimeSettingsPartial does', () => {
      const scenario = runtimeDraftFromOverride({ env: { A: '1' } })
      const group = setOverrideField(undefined, 'env', undefined)
      expect(materializeRuntimeSettingsForApply(undefined, undefined, scenario, group)?.env ?? {}).toEqual({})
    })

    it('resets the script JVM settings under a listed java_vuser with no value, as applyJavaVuserOverrideField does', () => {
      const java = { base_url: '', java_vuser: { jvm_sharing: 'shared' as const, vusers_per_jvm: 120, classpath: ['/app.jar'] } }
      const group = setOverrideField(undefined, 'java_vuser', undefined)
      expect(materializeRuntimeSettingsForApply(undefined, java, undefined, group)?.java_vuser ?? {}).toEqual({})
    })

    it('folds a lower layer\'s compact alias into its section before a listed leaf lands', () => {
      const scenario = { log_level: 'standard', auto_transactions: '', iterations: 5 }
      const group = setOverrideField(undefined, 'run_logic.workload_model', 'open')
      const result = materializeRuntimeSettingsForApply(undefined, undefined, scenario, group)
      expect(result?.run_logic).toEqual({ iterations: 5, workload_model: 'open' })
    })
  })

  it('copies a SOAP override only to SOAP groups and never copies its WS-Security credentials', () => {
    const soap = { version: '1.2', ws_security: { username_token: { username: '[REDACTED]', password: '[REDACTED]', password_type: 'PasswordText' } } }
    const source = { log_level: '', auto_transactions: '', soap }
    expect(copyRuntimeSettingsForProtocol(source, 'soap').soap).toEqual({ version: '1.2', ws_security: { username_token: { password_type: 'PasswordText' } } })
    expect(copyRuntimeSettingsForProtocol(source, 'http').soap).toBeUndefined()
    expect(source.soap).toBe(soap)
    // A v1 SOAP section replaces the lower layers' whole, as mergeRuntimeSettings does.
    expect(materializeRuntimeSettingsForApply(undefined, undefined, { log_level: '', auto_transactions: '', soap: { version: '1.1' } }, { log_level: '', auto_transactions: '', soap: { version: '1.2' } })?.soap).toEqual({ version: '1.2' })
  })

  it('keeps a partial target\'s env.KEY removal when Apply to groups copies the source profile', () => {
    const scripts = [
      { id: 'src', protocol: 'http', script: { config: { base_url: '', http: { request_timeout_ms: 9000 } } } },
      { id: 'tgt', protocol: 'http', script: { config: { base_url: '' } } },
    ] as unknown as StoredScript[]
    const scenario: ScenarioRuntimeSettingsDraft = { log_level: '', auto_transactions: '', env: { FOO: 'scenario', BAR: 'scenario' } }
    let target = setOverrideField(undefined, 'env.BAR', 'x')
    target = setOverrideField(target, 'env.FOO', undefined)
    expect(target.override_fields).toEqual(expect.arrayContaining(['env.FOO', 'env.BAR']))
    const row = applyEffectiveRuntimeSettingsToGroups(
      [{ script_id: 'src' }, { script_id: 'tgt', runtimeSettings: target }],
      scripts, undefined, scenario, 0, [1],
    )[1].runtimeSettings
    expect(row?.override_fields).toEqual(expect.arrayContaining(['env.FOO', 'env.BAR']))
    expect(row?.override_fields).not.toContain('env')
    expect(row?.env?.BAR).toBe('x')
    expect(row?.env?.FOO).toBeUndefined()
    const effective = materializeRuntimeSettingsForApply(undefined, { base_url: '' }, scenario, row)
    expect(effective?.env).toEqual({ BAR: 'x' })
    expect(effective?.http?.request_timeout_ms).toBe(9000)
  })

  describe('Apply to groups with a SOAP UsernameToken (keepTargetSOAPCredentials)', () => {
    const sourceScript = {
      base_url: '',
      soap: { version: '1.2', ws_security: { username_token: { username: 'src-user', password: 'src-pass', password_type: 'PasswordDigest' } } },
    }
    const targetScript = {
      base_url: '',
      soap: { version: '1.1', ws_security: { username_token: { username: 'tgt-user', password: 'tgt-pass', password_type: 'PasswordText' } } },
    }
    const scripts = [
      { id: 'src', protocol: 'soap', script: { config: sourceScript } },
      { id: 'tgt', protocol: 'soap', script: { config: targetScript } },
    ] as unknown as StoredScript[]
    const apply = (target?: ScenarioRuntimeSettingsDraft, source?: ScenarioRuntimeSettingsDraft) =>
      applyEffectiveRuntimeSettingsToGroups(
        [{ script_id: 'src', runtimeSettings: source }, { script_id: 'tgt', runtimeSettings: target }],
        scripts, undefined, undefined, 0, [1],
      )[1].runtimeSettings
    const effectiveToken = (row?: ScenarioRuntimeSettingsDraft) =>
      materializeRuntimeSettingsForApply(undefined, targetScript, undefined, row)?.soap?.ws_security?.username_token

    it('keeps the target script credentials while copying the rest of the SOAP section', () => {
      const row = apply()
      expect(row?.override_fields).toBeDefined()
      expect(row?.override_fields).not.toContain('soap')
      expect(row?.override_fields).not.toContain('soap.ws_security.username_token.username')
      expect(row?.override_fields).not.toContain('soap.ws_security.username_token.password')
      expect(row?.override_fields).toEqual(expect.arrayContaining(['soap.version', 'soap.ws_security.username_token.password_type']))
      expect(row?.soap?.ws_security?.username_token?.username).toBeUndefined()
      expect(row?.soap?.ws_security?.username_token?.password).toBeUndefined()
      expect(effectiveToken(row)).toEqual({ username: 'tgt-user', password: 'tgt-pass', password_type: 'PasswordDigest' })
      expect(materializeRuntimeSettingsForApply(undefined, targetScript, undefined, row)?.soap?.version).toBe('1.2')
    })

    it('keeps a credential the target group row already set, and inherits the other', () => {
      const prior = setOverrideField(undefined, 'soap.ws_security.username_token.username', 'own-user')
      const row = apply(prior)
      expect(row?.soap?.ws_security?.username_token?.username).toBe('own-user')
      expect(row?.override_fields).toContain('soap.ws_security.username_token.username')
      expect(row?.override_fields).not.toContain('soap.ws_security.username_token.password')
      expect(effectiveToken(row)).toEqual({ username: 'own-user', password: 'tgt-pass', password_type: 'PasswordDigest' })
    })

    describe('a target with no SOAP credentials of its own', () => {
      const bareTargetScript = { base_url: '', soap: { version: '1.1' } }
      const bareScripts = [
        { id: 'src', protocol: 'soap', script: { config: sourceScript } },
        { id: 'bare', protocol: 'soap', script: { config: bareTargetScript } },
        { id: 'tgt', protocol: 'soap', script: { config: targetScript } },
      ] as unknown as StoredScript[]
      const targetRow: ScenarioRuntimeSettingsDraft = { log_level: 'debug', auto_transactions: '' }

      it('is left unchanged and reported instead of getting an empty UsernameToken', () => {
        const groups = [
          { script_id: 'src' },
          { script_id: 'bare', iterations: 3, runtimeSettings: targetRow },
          { script_id: 'tgt' },
        ]
        const { groups: next, report } = applyEffectiveRuntimeSettingsToGroupsWithReport(groups, bareScripts, undefined, undefined, 0, [1, 2])
        expect(report.missingSOAPCredentialTargets).toEqual([1])
        expect(next[1]).toBe(groups[1])
        // A target that has credentials of its own is still applied.
        expect(next[2].runtimeSettings?.soap?.version).toBe('1.2')
        expect(applyEffectiveRuntimeSettingsToGroups(groups, bareScripts, undefined, undefined, 0, [1])[1]).toBe(groups[1])
      })

      it('is applied when the scenario root supplies the credentials', () => {
        const scenario: ScenarioRuntimeSettingsDraft = {
          log_level: '',
          auto_transactions: '',
          soap: { version: '1.1', ws_security: { username_token: { username: 'root-user', password: '[REDACTED]' } } },
        }
        const groups = [{ script_id: 'src' }, { script_id: 'bare' }]
        const { groups: next, report } = applyEffectiveRuntimeSettingsToGroupsWithReport(groups, bareScripts, undefined, scenario, 0, [1])
        expect(report.missingSOAPCredentialTargets).toEqual([])
        // The scenario root is above the source script, so it also sets the copied version.
        expect(next[1]).not.toBe(groups[1])
        expect(next[1].runtimeSettings?.soap?.version).toBe('1.1')
      })

      it('is applied when the source runs without credentials', () => {
        const noCredScripts = [
          { id: 'src', protocol: 'soap', script: { config: { base_url: '', soap: { version: '1.2' } } } },
          { id: 'bare', protocol: 'soap', script: { config: bareTargetScript } },
        ] as unknown as StoredScript[]
        const { report } = applyEffectiveRuntimeSettingsToGroupsWithReport([{ script_id: 'src' }, { script_id: 'bare' }], noCredScripts, undefined, undefined, 0, [1])
        expect(report.missingSOAPCredentialTargets).toEqual([])
      })
    })

    it('stays a v1 row when the target row owns both credentials', () => {
      const prior: ScenarioRuntimeSettingsDraft = {
        log_level: '',
        auto_transactions: '',
        soap: { version: '1.1', ws_security: { username_token: { username: '[REDACTED]', password: '[REDACTED]' } } },
      }
      const row = apply(prior)
      expect(row?.override_fields).toBeUndefined()
      expect(row?.soap?.ws_security?.username_token).toEqual({ username: '[REDACTED]', password: '[REDACTED]', password_type: 'PasswordDigest' })
    })
  })
})

describe('v1 empty scenario sections in the inherited baseline', () => {
  const script = { base_url: '', pacing: { type: 'fixed_delay', delay_ms: 100 }, error_handling: { snapshot_on_error: true } }

  it('a v1 {pacing:{}} scenario row pins pacing to its zero value', () => {
    const scenario = runtimeDraftFromOverride({ pacing: {} } as never)
    const effective = materializeRuntimeSettingsForApply(undefined, script as never, scenario)
    expect(effective?.pacing).toEqual({})
    expect(effective?.snapshot_on_error).toBe(true)
  })

  it('a v1 {error_handling:{}} scenario row pins error handling to its zero value', () => {
    const scenario = runtimeDraftFromOverride({ error_handling: {} } as never)
    const effective = materializeRuntimeSettingsForApply(undefined, script as never, scenario)
    expect(effective?.pacing).toEqual({ type: 'fixed_delay', delay_ms: 100 })
    expect(effective?.snapshot_on_error).toBeUndefined()
  })

  it('a v1 {pacing:{}} source group row is copied as pinned', () => {
    const source = runtimeDraftFromOverride({ pacing: {} } as never)
    const effective = materializeRuntimeSettingsForApply(undefined, script as never, undefined, source)
    expect(effective?.pacing).toEqual({})
  })
})
