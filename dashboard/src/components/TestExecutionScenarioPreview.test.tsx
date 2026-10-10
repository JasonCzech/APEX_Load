// @vitest-environment jsdom

import { act, type ReactNode, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestExecutionScenarioPreview from './TestExecutionScenarioPreview'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <div>{children}</div>
  const labelText = (label?: string | { value?: string }) => (typeof label === 'string' ? label : label?.value ?? '')

  return {
    ResponsiveContainer: Mock,
    AreaChart: Mock,
    Area: () => null,
    XAxis: ({ label, type, domain }: { label?: string | { value?: string }; type?: string; domain?: Array<string | number> }) => (
      <div data-testid="mock-x-axis" data-label={labelText(label)} data-type={type || ''} data-domain={domain?.join(',') || ''} />
    ),
    YAxis: ({ label }: { label?: string | { value?: string } }) => <div data-testid="mock-y-axis" data-label={labelText(label)} />,
    CartesianGrid: () => null,
    Tooltip: () => null,
    Legend: () => null,
    ReferenceArea: ({ x1, x2 }: { x1?: number | string; x2?: number | string }) => (
      <div data-testid="mock-reference-area" data-x1={String(x1 ?? '')} data-x2={String(x2 ?? '')} />
    ),
  }
})

describe('TestExecutionScenarioPreview', () => {
  let container: HTMLDivElement
  let root: Root
  let consoleErrorSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => undefined)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    consoleErrorSpy.mockRestore()
    vi.clearAllMocks()
  })

  it('renders the summary row without nested button warnings and keeps edit separate from expand', async () => {
    const onExpandedScenarioScriptChange = vi.fn()
    const onEditScript = vi.fn()

    function PreviewHarness() {
      const [expandedScenarioScript, setExpandedScenarioScript] = useState<string | null>(null)

      return (
        <TestExecutionScenarioPreview
          test={{
            id: 'test-1',
            name: 'Queued Scenario',
            status: 'QUEUED',
            scenario_mode: 'goal_oriented',
            total_vusers: 10,
            duration: '5m',
            start_delay: '30s',
            goal_config: {
              target_average_tps: 300,
              p95_latency_ms: 800,
              max_error_pct: 2.5,
              max_error_count: 25,
              min_vusers: 5,
              max_vusers: 25,
              stop_on_sla_breach: true,
              stop_on_max_errors: true,
              sla_breach_window: '45s',
            },
            runtime_settings: {
              pacing: { type: 'fixed_delay', delay_ms: 1200 },
              think_time: { type: 'fixed', mean_ms: 900 },
              browser: {
                device_profile: 'iphone_se',
                orientation: 'portrait',
                touch_mode: true,
                headless: false,
              },
              error_handling: { continue_on_error: true, snapshot_on_error: true },
            },
            created_at: '2026-05-12T20:00:00Z',
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              ramp_up: '30s',
              ramp_down: '15s',
              iterations: 3,
              initialization: 'at_runtime',
            }],
          } as never}
          startCapacityConflict={null}
          startShakeoutConflict={null}
          startFeedback=""
          onDismissStartConflict={vi.fn()}
          onConfirmStartAnyway={vi.fn()}
          onContinueAfterShakeoutFailure={vi.fn()}
          onCancelShakeoutRun={vi.fn()}
          queuedScenarioPreview={{
            points: [{ second: 0, group0: 10, total: 10 }],
            totalVUsers: 10, showVUsers: true, peakVUsers: 10,
            totalSeconds: 300,
            steadySeconds: 300,
            startDelaySeconds: 30,
            preBaselineSeconds: 0,
            postBaselineSeconds: 0,
          }}
          queuedScenarioRows={[{
            group: {
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              ramp_up: '30s',
              ramp_down: '15s',
              iterations: 3,
              initialization: 'at_runtime',
            },
            detail: {
              id: 'script-1',
              name: 'Checkout Script',
              protocol: 'http',
              script: { config: { http: { version: '2' } }, actions: [] },
            },
            actionCount: 0,
            initCount: 0,
            teardownCount: 0,
            dataSourceCount: 0,
          }]}
          snapshotSource="immutable_manifest"
          snapshotLoading={false}
          snapshotError=""
          expandedScenarioScript={expandedScenarioScript}
          onExpandedScenarioScriptChange={(value) => {
            onExpandedScenarioScriptChange(value)
            setExpandedScenarioScript(value)
          }}
          onEditScript={onEditScript}
          groupColors={['#0a84ff']}
        />
      )
    }

    await act(async () => {
      root.render(<PreviewHarness />)
    })

    const summaryRow = container.querySelector('.scenario-script-summary')
    const editButton = Array.from(container.querySelectorAll('button')).find((button) => button.textContent?.includes('Open Current Library Script'))

    expect(summaryRow?.getAttribute('role')).toBe('button')
    expect(editButton).toBeDefined()
    expect(container.textContent).toContain('Start Delay')
    expect(container.textContent).toContain('30s')
    expect(container.textContent).toContain('Target Avg TPS')
    expect(container.textContent).toContain('P95 Latency Ceiling')
    expect(container.textContent).toContain('800 ms')
    expect(container.textContent).toContain('Max Error %')
    expect(container.textContent).toContain('2.5%')
    expect(container.textContent).toContain('Max Error Count')
    expect(container.textContent).toContain('25')
    expect(container.textContent).toContain('VUser Guardrails')
    expect(container.textContent).toContain('5 - 25')
    expect(container.textContent).toContain('SLA Breach Window')
    expect(container.textContent).toContain('45s')
    expect(container.textContent).toContain('Stop On SLA Breach')
    expect(container.textContent).toContain('Stop On Max Errors')
    expect(container.textContent).toContain('Pacing')
    expect(container.textContent).toContain('Fixed delay · 1.2 seconds')
    expect(container.textContent).toContain('Think time')
    expect(container.textContent).toContain('fixed · 900ms')
    expect(container.textContent).toContain('Browser Runtime')
    expect(container.textContent).toContain('iphone_se · Portrait · Touch enabled · Headed')
    expect(container.textContent).toContain('Continue on error')
    expect(container.textContent).toContain('Enabled')
    expect(container.textContent).toContain('immutable queued snapshot')
    expect(container.textContent).toContain('Full script bodies are not included')
    expect(container.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-label')).toBe('Elapsed time')
    expect(container.querySelector('[data-testid="mock-y-axis"]')?.getAttribute('data-label')).toBe('VUsers')

    const nestingWarning = consoleErrorSpy.mock.calls.some(([message]: unknown[]) =>
      typeof message === 'string' && message.includes('validateDOMNesting'),
    )

    expect(nestingWarning).toBe(false)

    if (!summaryRow || !editButton) {
      return
    }

    await act(async () => {
      summaryRow.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onExpandedScenarioScriptChange).toHaveBeenCalledWith('Checkout')
    expect(container.textContent).toContain('3 (group override)')
    expect(container.textContent).toContain('HTTP/2 required (no fallback) · 100 conns/host')

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEditScript).toHaveBeenCalledWith('script-1')
    expect(onExpandedScenarioScriptChange).toHaveBeenCalledTimes(1)
  })

  it.each([
    { protocol: 'http', version: undefined, expected: 'Auto-negotiate (HTTP/2 or HTTP/1.1)' },
    { protocol: 'http/1.1', version: '', expected: 'HTTP/1.1 required' },
    { protocol: 'http/2', version: undefined, expected: 'HTTP/2 required (no fallback)' },
    { protocol: 'http/2', version: '1.1', expected: 'HTTP/1.1 required' },
    { protocol: 'http/1.1', version: 'HTTP/2', expected: 'HTTP/2 required (no fallback)' },
    { protocol: 'http/2', version: 'auto', expected: 'Unsupported script HTTP version' },
    { protocol: 'http/2', version: null, expected: 'Unsupported script HTTP version' },
  ])('shows the snapshot effective global policy for $protocol with $version', async ({ protocol, version, expected }) => {
    const group = { name: 'Version', script_id: 'script-1', script_name: 'Version script', protocol, vusers: 1 }
    const detail = { id: 'script-1', name: 'Version script', protocol, script: { config: { http: { version, max_conns_per_host: 7 } }, actions: [] } }
    await act(async () => root.render(<TestExecutionScenarioPreview
      test={{ id: 'version-test', name: 'Version', status: 'QUEUED', total_vusers: 1, duration: '1m', groups: [group] } as never}
      startCapacityConflict={null} startShakeoutConflict={null} startFeedback="" onDismissStartConflict={vi.fn()}
      onConfirmStartAnyway={vi.fn()} onContinueAfterShakeoutFailure={vi.fn()} onCancelShakeoutRun={vi.fn()}
      queuedScenarioPreview={{ points: [{ second: 0, group0: 1, total: 1 }], totalVUsers: 1, showVUsers: true, peakVUsers: 1, totalSeconds: 60, steadySeconds: 60, startDelaySeconds: 0, preBaselineSeconds: 0, postBaselineSeconds: 0 }}
      queuedScenarioRows={[{ group, detail: detail as never, actionCount: 0, initCount: 0, teardownCount: 0, dataSourceCount: 0 }]}
      snapshotSource="immutable_manifest" snapshotLoading={false} snapshotError="" expandedScenarioScript="Version"
      onExpandedScenarioScriptChange={vi.fn()} onEditScript={vi.fn()} groupColors={['#0a84ff']} />))
    const card = Array.from(container.querySelectorAll('.scenario-script-detail-card')).find(item => item.querySelector('.scenario-script-detail-label')?.textContent === 'HTTP Runtime')!
    expect(card.querySelector('.scenario-script-detail-value')?.textContent).toBe(`${expected} · 7 conns/host`)
    expect(detail.script.config.http.version).toBe(version)
  })

  it('resolves error handling section-wise, never inheriting the half the winning section omits', async () => {
    // The controller applies error_handling as a whole SECTION: mergeRuntimeSettings swaps the
    // run-level section for the group's outright, and applyRuntimeOverrides then assigns
    // script.Config.ErrorHandling = *overrides.ErrorHandling. dsl.ErrorHandlingConfig holds plain
    // bools, so this group executes continue_on_error=true / snapshot_on_error=FALSE — the run
    // level's snapshot_on_error:true is discarded, not merged in.
    await act(async () => {
      root.render(
        <TestExecutionScenarioPreview
          test={{
            id: 'test-eh',
            name: 'Section Wise',
            status: 'QUEUED',
            total_vusers: 10,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            runtime_settings: { error_handling: { snapshot_on_error: true } },
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              runtime_settings: { error_handling: { continue_on_error: true } },
            }],
          } as never}
          startCapacityConflict={null}
          startShakeoutConflict={null}
          startFeedback=""
          onDismissStartConflict={vi.fn()}
          onConfirmStartAnyway={vi.fn()}
          onContinueAfterShakeoutFailure={vi.fn()}
          onCancelShakeoutRun={vi.fn()}
          queuedScenarioPreview={{
            points: [{ second: 0, group0: 10, total: 10 }],
            totalVUsers: 10, showVUsers: true, peakVUsers: 10,
            totalSeconds: 300,
            steadySeconds: 300,
            startDelaySeconds: 0,
            preBaselineSeconds: 0,
            postBaselineSeconds: 0,
          }}
          queuedScenarioRows={[{
            group: {
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
              runtime_settings: { error_handling: { continue_on_error: true } },
            },
            detail: {
              id: 'script-1',
              name: 'Checkout Script',
              protocol: 'http',
              script: { config: { error_handling: { snapshot_on_error: true } }, actions: [] },
            },
            actionCount: 0,
            initCount: 0,
            teardownCount: 0,
            dataSourceCount: 0,
          }] as never}
          snapshotSource="immutable_manifest"
          snapshotLoading={false}
          snapshotError=""
          expandedScenarioScript="Checkout"
          onExpandedScenarioScriptChange={vi.fn()}
          onEditScript={vi.fn()}
          groupColors={['#0a84ff']}
        />,
      )
    })

    const cardValue = (selector: string, label: string) => Array.from(container.querySelectorAll(selector))
      .find(card => card.querySelector('.scenario-setting-label, .scenario-script-detail-label')?.textContent === label)
      ?.querySelector('.scenario-setting-value, .scenario-script-detail-value')
      ?.textContent

    // The group's section wins whole, so the omitted snapshot half executes false. Reporting the
    // run-level 'Enabled (scenario override)' here would claim snapshots the run never takes.
    expect(cardValue('.scenario-script-detail-card', 'Snapshot on error')).toBe('Disabled (group override)')

    // Same rule one level up: the run-level section is pinned but omits continue_on_error, so the
    // script's own value is discarded and continue executes false — not 'not exposed'.
    expect(cardValue('.scenario-setting-card', 'Continue on error')).toBe('Disabled')
    expect(cardValue('.scenario-setting-card', 'Snapshot on error')).toBe('Enabled')
  })

  it('discloses when the manifest preview was synthesized from retained run state', async () => {
    await act(async () => {
      root.render(
        <TestExecutionScenarioPreview
          test={{
            id: 'test-retained',
            name: 'Retained Scenario',
            status: 'QUEUED',
            total_vusers: 10,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
            }],
          } as never}
          startCapacityConflict={null}
          startShakeoutConflict={null}
          startFeedback=""
          onDismissStartConflict={vi.fn()}
          onConfirmStartAnyway={vi.fn()}
          onContinueAfterShakeoutFailure={vi.fn()}
          onCancelShakeoutRun={vi.fn()}
          queuedScenarioPreview={{
            points: [{ second: 0, group0: 10, total: 10 }],
            totalVUsers: 10, showVUsers: true, peakVUsers: 10,
            totalSeconds: 300,
            steadySeconds: 300,
            startDelaySeconds: 0,
            preBaselineSeconds: 0,
            postBaselineSeconds: 0,
          }}
          queuedScenarioRows={[]}
          snapshotSource="retained_manifest"
          snapshotLoading={false}
          snapshotError=""
          expandedScenarioScript={null}
          onExpandedScenarioScriptChange={vi.fn()}
          onEditScript={vi.fn()}
          groupColors={['#0a84ff']}
        />,
      )
    })

    expect(container.textContent).toContain('synthesized from retained controller state')
    expect(container.textContent).toContain('may reflect later changes')
    expect(container.textContent).not.toContain('Load shape and runtime values come from this run’s immutable queued snapshot')
  })

  it('shows pending queue metadata for queued starts waiting on capacity', async () => {
    await act(async () => {
      root.render(
        <TestExecutionScenarioPreview
          test={{
            id: 'test-pending',
            name: 'Pending Scenario',
            status: 'PENDING',
            total_vusers: 10,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            pending_since: '2026-05-12T20:01:00Z',
            queue_position: 2,
            pending_reason: 'waiting for project test slot (1 max concurrent tests)',
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
            }],
          } as never}
          startCapacityConflict={null}
          startShakeoutConflict={null}
          startFeedback=""
          onDismissStartConflict={vi.fn()}
          onConfirmStartAnyway={vi.fn()}
          onContinueAfterShakeoutFailure={vi.fn()}
          onCancelShakeoutRun={vi.fn()}
          queuedScenarioPreview={{
            points: [],
            totalVUsers: 10, showVUsers: true, peakVUsers: 10,
            totalSeconds: 300,
            steadySeconds: 300,
            startDelaySeconds: 0,
            preBaselineSeconds: 0,
            postBaselineSeconds: 0,
          }}
          queuedScenarioRows={[]}
          snapshotSource="summary"
          snapshotLoading={false}
          snapshotError="manifest not found"
          expandedScenarioScript={null}
          onExpandedScenarioScriptChange={vi.fn()}
          onEditScript={vi.fn()}
          groupColors={['#0a84ff']}
        />,
      )
    })

    expect(container.textContent).toContain('Queued for project capacity')
    expect(container.textContent).toContain('Queue position #2')
    expect(container.textContent).toContain('waiting for project test slot')
    expect(container.textContent).toContain('Waiting since')
    expect(container.textContent).toContain('immutable queued snapshot is unavailable')
  })

  it('shows script shakeout evidence and routes continue or cancel actions', async () => {
    const onContinueAfterShakeoutFailure = vi.fn()
    const onCancelShakeoutRun = vi.fn()

    await act(async () => {
      root.render(
        <TestExecutionScenarioPreview
          test={{
            id: 'test-shakeout',
            name: 'Shakeout Scenario',
            status: 'SHAKEOUT_REVIEW',
            script_shakeout: true,
            total_vusers: 10,
            duration: '5m',
            created_at: '2026-05-12T20:00:00Z',
            error: 'Script shakeout failed',
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
            }],
          } as never}
          startCapacityConflict={null}
          startShakeoutConflict={{
            passed: false,
            summary: 'Script shakeout failed; full load test has not started.',
            results: [{
              group_name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              status: 'failed',
              error: 'request Login returned 500',
            }],
          }}
          startFeedback="Review the evidence before continuing."
          onDismissStartConflict={vi.fn()}
          onConfirmStartAnyway={vi.fn()}
          onContinueAfterShakeoutFailure={onContinueAfterShakeoutFailure}
          onCancelShakeoutRun={onCancelShakeoutRun}
          queuedScenarioPreview={{
            points: [],
            totalVUsers: 10, showVUsers: true, peakVUsers: 10,
            totalSeconds: 300,
            steadySeconds: 300,
            startDelaySeconds: 0,
            preBaselineSeconds: 0,
            postBaselineSeconds: 0,
          }}
          queuedScenarioRows={[]}
          snapshotSource="immutable_manifest"
          snapshotLoading={false}
          snapshotError=""
          expandedScenarioScript={null}
          onExpandedScenarioScriptChange={vi.fn()}
          onEditScript={vi.fn()}
          groupColors={['#0a84ff']}
        />,
      )
    })

    expect(container.textContent).toContain('Script shakeout needs review')
    expect(container.textContent).toContain('Checkout · Checkout Script')
    expect(container.textContent).toContain('request Login returned 500')

    const cancelButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Cancel Run'))
    const continueButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Continue Full Load'))
    expect(cancelButton).toBeDefined()
    expect(continueButton).toBeDefined()

    await act(async () => {
      cancelButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      continueButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onCancelShakeoutRun).toHaveBeenCalledTimes(1)
    expect(onContinueAfterShakeoutFailure).toHaveBeenCalledTimes(1)
  })

  it('shades phase bands against a numeric time axis so boundaries off the sampled points still map', async () => {
    // 10m steady run with a resource baseline: sampled every 42s, so none of the phase
    // boundaries (300, 900, 960) is a sampled value. A category axis cannot map them.
    const totalSeconds = 1260
    const points: Array<Record<string, number>> = []
    for (let second = 0; second <= totalSeconds; second += 42) {
      points.push({ second, group0: 10, total: 10 })
    }

    await act(async () => {
      root.render(
        <TestExecutionScenarioPreview
          test={{
            id: 'test-bands',
            name: 'Baseline Scenario',
            status: 'QUEUED',
            total_vusers: 10,
            duration: '10m',
            resource_baseline: true,
            created_at: '2026-05-12T20:00:00Z',
            groups: [{
              name: 'Checkout',
              script_id: 'script-1',
              script_name: 'Checkout Script',
              protocol: 'http',
              vusers: 10,
            }],
          } as never}
          startCapacityConflict={null}
          startShakeoutConflict={null}
          startFeedback=""
          onDismissStartConflict={vi.fn()}
          onConfirmStartAnyway={vi.fn()}
          onContinueAfterShakeoutFailure={vi.fn()}
          onCancelShakeoutRun={vi.fn()}
          queuedScenarioPreview={{
            points,
            totalVUsers: 10, showVUsers: true, peakVUsers: 10,
            totalSeconds,
            steadySeconds: 600,
            startDelaySeconds: 0,
            preBaselineSeconds: 300,
            postBaselineSeconds: 300,
          }}
          queuedScenarioRows={[]}
          snapshotSource="immutable_manifest"
          snapshotLoading={false}
          snapshotError=""
          expandedScenarioScript={null}
          onExpandedScenarioScriptChange={vi.fn()}
          onEditScript={vi.fn()}
          groupColors={['#0a84ff']}
        />,
      )
    })

    const xAxis = container.querySelector('[data-testid="mock-x-axis"]')
    expect(xAxis?.getAttribute('data-type')).toBe('number')
    expect(xAxis?.getAttribute('data-domain')).toBe('0,dataMax')

    const bands = Array.from(container.querySelectorAll('[data-testid="mock-reference-area"]'))
      .map(band => [band.getAttribute('data-x1'), band.getAttribute('data-x2')])
    expect(bands).toEqual([['300', '900'], ['0', '300'], ['960', '1260']])
    const sampledSeconds = new Set(points.map(point => point.second))
    expect([300, 900, 960].some(boundary => sampledSeconds.has(boundary))).toBe(false)
  })
})
