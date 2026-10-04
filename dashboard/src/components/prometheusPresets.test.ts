import { expect, it } from 'vitest'
import { buildPrometheusPreset, prometheusPresetAvailable, prometheusPresets } from './prometheusPresets'

it('requires discovered dependencies and an explicit safe application scope', () => {
  const memory = prometheusPresets.find(preset => preset.id === 'host_memory_percent')!
  expect(prometheusPresetAvailable(memory, ['node_memory_MemAvailable_bytes'])).toBe(false)
  expect(prometheusPresetAvailable(memory, [...memory.metrics, memory.metrics[0]])).toBe(true)
  expect(buildPrometheusPreset(memory, { job: '' })).toBeUndefined()
  expect(buildPrometheusPreset(memory, { job: 'checkout\nproduction' })).toBeUndefined()
  const value = buildPrometheusPreset(memory, { job: 'checkout"},job=~".*' })!
  expect(value.expression).toContain('job="checkout\\"},job=~\\".*"')
  expect(value.allowed_labels).toEqual(['job', 'instance'])
  expect(value.unit).toBe('%')
})

it('scopes all presets and retains approved labels, units and conservative freshness expressions', () => {
  for (const preset of prometheusPresets) {
    const query = buildPrometheusPreset(preset, { job: 'checkout', namespace: 'production' })!
    expect(query.expression).toContain('job="checkout",namespace="production"')
    expect(query.unit).toBeTruthy()
    expect(query.allowed_labels.length).toBeGreaterThan(0)
    expect(query.freshness_expression).toContain('min by (')
    expect(query.freshness_expression).toContain('timestamp(')
    if (query.terminal_expression) expect(query.terminal_expression).toContain('{{window.duration}}')
  }
  const pod = prometheusPresets.find(preset => preset.id === 'pod_cpu_cores')!
  expect(buildPrometheusPreset(pod, { job: 'kubelet' })).toBeUndefined()
  expect(buildPrometheusPreset(pod, { job: 'kubelet', namespace: 'checkout' })!.allowed_labels).toEqual(['namespace', 'pod'])
})

it('checks freshness of both memory ratio operands without merging their timestamp identities', () => {
  const memory = prometheusPresets.find(preset => preset.id === 'host_memory_percent')!
  const query = buildPrometheusPreset(memory, { job: 'checkout' })!
  expect(query.freshness_expression).toBe('min by (job, instance) (label_replace(timestamp(node_memory_MemAvailable_bytes{job="checkout"}), "apex_memory_operand", "0", "", "") or label_replace(timestamp(node_memory_MemTotal_bytes{job="checkout"}), "apex_memory_operand", "1", "", ""))')
  expect(query.allowed_labels).toEqual(['job', 'instance'])
  expect(query.max_sample_age_seconds).toBe(90)
})
