import { describe, expect, it } from 'vitest'

import { materializeRuntimeSettingsForApply } from './runtimeSettingsApply'

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
  })
})
