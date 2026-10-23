// @vitest-environment jsdom
// @ts-expect-error Vitest runs in Node; the dashboard app intentionally omits Node globals.
import { createHash } from 'node:crypto'
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TelemetryPolicy from './TelemetryPolicy'
import TestExecutionStatusPanels from './TestExecutionStatusPanels'
import TestExecutionManifestCard from './TestExecutionManifestCard'
import RunExecutionDetails from './runReport/RunExecutionDetails'
import type { TestRunManifest, TestSummary } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const receipt = () => ({ availability: 'available' as const, policy: { schema_version: 'apexload.telemetry_policy.v1' as const, policy_id: 'system-telemetry-budgets' as const, revision: 1 as const, hash: 'e6fe795b839c0b8b2769d48105525f307343b41f20e1ca2e10dc19580fdfea25', captured_at: '2026-10-05T12:00:00Z', capture_boundary: 'execution-admission-before-staging' as const, classification_algorithm: 'legacy-mixed-diagnostic.v1' as const, expected_controller_publication_interval_ms: 3000, cadence_scope: 'local-controller-ticker' as const, sustained_breach_window_ms: 15000, coverage_rule: 'retained-classifications-only;gaps-unknown' as const, budgets: [{ stage: 'sample_age' as const, enabled: true, limit_ms: 11 }, { stage: 'generator_processing' as const, enabled: false, limit_ms: 0 }, { stage: 'transport' as const, enabled: true, limit_ms: 750 }, { stage: 'controller_fanout' as const, enabled: true, limit_ms: 250 }, { stage: 'browser_render' as const, enabled: true, limit_ms: 1e-12 }] } })

describe('TelemetryPolicy consumers', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { vi.stubGlobal('crypto', { subtle: { digest: async (_algorithm: string, data: Uint8Array) => Uint8Array.from(createHash('sha256').update(data).digest()).buffer } }); container = document.createElement('div'); document.body.append(container); root = createRoot(container) })
  afterEach(async () => { await act(async () => root.unmount()); container.remove(); vi.unstubAllGlobals() })
  it('shows retained provenance, disabled budgets, exact tiny positive values and limits of the receipt', async () => {
    await act(async () => root.render(<TelemetryPolicy evidence={receipt()} />))
    expect(container.textContent).toContain('revision 1')
    expect(container.textContent).toContain(receipt().policy.hash)
    expect(container.textContent).toContain('3,000 ms')
    expect(container.textContent).toContain('11 ms')
    expect(container.textContent).toContain('Disabled (explicit zero)')
    expect(container.textContent).toContain('1e-12 ms')
    expect(container.textContent).toContain('Missing intervals remain unknown')
    expect(container.textContent).toContain('does not establish')
    expect(container.textContent).not.toContain('Telemetry passed')
    await act(async () => root.render(<TelemetryPolicy evidence={{ availability: 'available', policy: { secret: 'do-not-render' } }} />))
    expect(container.textContent).toContain('invalid or unsupported')
    expect(container.textContent).not.toContain('do-not-render')
    expect(container.querySelector('[aria-label="Retained telemetry stage budgets"]')).toBeNull()
  })
  it('labels the system receipt and a project budget policy the run chose', async () => {
    await act(async () => root.render(<TelemetryPolicy evidence={receipt()} />))
    expect(container.textContent).toContain('System threshold settings (system-telemetry-budgets · revision 1)')
    expect(container.textContent).not.toContain('Definition digest')
    const chosen = { availability: 'available' as const, policy: { ...receipt().policy, schema_version: 'apexload.telemetry_policy.v2', policy_id: 'tight-budgets', revision: 3, definition_sha256: 'ab'.repeat(32), hash: 'f034a3ac9bc132b32ff6487f8f2598e799676832958d96049f4b2dd7914a265b' } }
    await act(async () => root.render(<TelemetryPolicy evidence={chosen} />))
    expect(container.textContent).toContain('Project policy tight-budgets · revision 3 (chosen by the run)')
    expect(container.textContent).toContain('Definition digest' + 'ab'.repeat(32))
    expect(container.textContent).toContain('11 ms')
  })
  it('shows explicit unavailable legacy evidence without default or zero budgets', async () => {
    await act(async () => root.render(<TelemetryPolicy />))
    expect(container.textContent).toContain('No immutable policy receipt')
    expect(container.querySelector('code')).toBeNull()
    expect(container.textContent).not.toContain('Disabled')
  })
  it('passes live metric policy through the actual status panel', async () => {
    const test = { id: 'run', name: 'Run', status: 'RUNNING', groups: [], total_vusers: 1, duration: '1m', created_at: '2026-10-05T12:00:00Z' } as TestSummary
    await act(async () => root.render(<TestExecutionStatusPanels isGoalOriented={false} goalDescription="" test={test} metrics={{ telemetry_policy: receipt() }} isMultiGroup={false} groupMetrics={[]} groupColors={[]} />))
    expect(container.querySelector('[aria-label="Telemetry budget policy"]')?.textContent).toContain('11 ms')
  })
  it('passes report execution manifest policy through the actual retained record component', async () => {
    const test = { id: 'run', name: 'Run', status: 'COMPLETE', groups: [], total_vusers: 1, duration: '1m', created_at: '2026-10-05T12:00:00Z' } as TestSummary
    const configuration = { status: 'COMPLETE', total_vusers: 1, duration: '1m', groups: [] }
    const manifest = { schema_version: 'v1', test_id: 'run', test_name: 'Run', status: 'COMPLETE', source: 'archive', generated_at: '2026-10-05T12:00:00Z', identity: {}, initial_configuration: configuration, current_configuration: configuration, load_generators: {}, telemetry_policy: receipt() } as TestRunManifest
    await act(async () => root.render(<RunExecutionDetails test={test} report={null} evidenceOnly manifestModel={{ manifest, manifestError: '', manifestLoading: false, reloadManifest: async () => {} }} />))
    expect(container.querySelector('[aria-label="Telemetry budget policy"]')?.textContent).toContain('11 ms')
  })
  it.each([false, true])('passes manifest policy through both layout variants: compact=%s', async compact => {
    const configuration = { status: 'COMPLETE', total_vusers: 1, duration: '1m', groups: [] }
    const manifest = { schema_version: 'v1', test_id: 'run', test_name: 'Run', status: 'COMPLETE', source: 'archive', generated_at: '2026-10-05T12:00:00Z', identity: {}, initial_configuration: configuration, current_configuration: configuration, load_generators: {}, telemetry_policy: receipt() } as TestRunManifest
    await act(async () => root.render(<TestExecutionManifestCard compact={compact} manifest={manifest} error="" loading={false} onReload={async () => {}} />))
    expect(container.querySelector('[aria-label="Telemetry budget policy"]')?.textContent).toContain('11 ms')
  })
  it('never displays a pending candidate or a stale digest completion after replacement', async () => {
    const pending: { bytes: Uint8Array; resolve: (value: ArrayBuffer) => void }[] = []
    vi.stubGlobal('crypto', { subtle: { digest: (_algorithm: string, bytes: Uint8Array) => new Promise<ArrayBuffer>(resolve => pending.push({ bytes, resolve })) } })
    await act(async () => root.render(<TelemetryPolicy evidence={receipt()} />))
    expect(container.textContent).toContain('Verifying')
    expect(container.querySelector('code')).toBeNull()
    const changed = receipt()
    changed.policy.budgets[0].limit_ms = 999
    await act(async () => root.render(<TelemetryPolicy evidence={changed} />))
    expect(pending).toHaveLength(2)
    await act(async () => pending[0].resolve(Uint8Array.from(createHash('sha256').update(pending[0].bytes).digest()).buffer))
    expect(container.textContent).toContain('Verifying')
    expect(container.querySelector('code')).toBeNull()
    await act(async () => pending[1].resolve(Uint8Array.from(createHash('sha256').update(pending[1].bytes).digest()).buffer))
    expect(container.textContent).toContain('invalid or unsupported')
    expect(container.querySelector('[aria-label="Retained telemetry stage budgets"]')).toBeNull()
    expect(container.textContent).not.toContain('999 ms')
  })
  it('immediately hides a previously verified receipt when a new response arrives', async () => {
    await act(async () => root.render(<TelemetryPolicy evidence={receipt()} />))
    expect(container.textContent).toContain('11 ms')
    vi.stubGlobal('crypto', { subtle: { digest: () => new Promise(() => {}) } })
    await act(async () => root.render(<TelemetryPolicy evidence={receipt()} />))
    expect(container.textContent).toContain('Verifying')
    expect(container.querySelector('code')).toBeNull()
    expect(container.textContent).not.toContain('11 ms')
  })

  it.each([
    [0, 'ef50f5aacb9f510ad18e1b730aa74b6b24f3fbd3244a93720111b3968d3ef8bc'],
    [17, '9946392432f9533e924fa82684a8094a9c55beaf6296d588500be28a66027629'],
    [999, '9ad5e50459df03837f8214c31cfe9833b0d7be94d9725d449ec7182e2e7e4c7f'],
  ])('does not turn digest-valid retained age budget %s into an unqualified health verdict', async (limit, hash) => {
    const value = receipt()
    value.policy.budgets[0].limit_ms = limit as number
    value.policy.budgets[0].enabled = (limit as number) > 0
    value.policy.hash = hash as string
    await act(async () => root.render(<TestExecutionStatusPanels isGoalOriented={false} goalDescription="" test={{ id: 'run', status: 'RUNNING', groups: [], total_vusers: 1 } as never} metrics={{ telemetry: { sample_age_ms: 2500 }, telemetry_policy: value }} isMultiGroup={false} groupMetrics={[]} groupColors={[]} />))
    expect(container.querySelector('[aria-label="Telemetry budget policy"] code')?.textContent).toBe(hash)
    const card = container.querySelector('.telemetry-status-card')
    expect(card?.querySelector('.goal-status-badge')?.textContent).toBe('Age observed')
    expect(card?.querySelector('.goal-status-badge')?.className).toContain('neutral')
    expect(card?.textContent).not.toContain('Healthy')
    expect(card?.textContent).not.toContain('Stale')
  })

})
