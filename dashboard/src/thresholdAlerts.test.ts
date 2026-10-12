import { describe, expect, it } from 'vitest'

import {
  acknowledgeThresholdAlert,
  appendProjectThresholdAlert,
  formatThresholdAlertSummary,
  thresholdAlertMetricLabel,
  isThresholdAlertTerminalStatus,
  MAX_THRESHOLD_ALERTS,
  resolveProjectThresholdAlertsBefore,
  resolveProjectThresholdAlertsForTest,
  resolveThresholdAlertsBefore,
  thresholdAlertFromMessage,
  thresholdAlertsForProject,
  upsertThresholdAlert,
  type ThresholdAlertEntry,
} from './thresholdAlerts'
import type { WSMessage } from './types'

function breachMessage(overrides: Record<string, unknown> = {}): WSMessage {
  return {
    type: 'threshold_alert',
    test_id: 'test-1',
    test_name: 'Checkout',
    timestamp: '2026-07-30T12:00:00Z',
    metric: 'p99_ms',
    value: 1830,
    threshold: 1500,
    ...overrides,
  } as unknown as WSMessage
}

describe('thresholdAlerts', () => {
  it('parses a threshold_alert message and rejects malformed payloads', () => {
    const entry = thresholdAlertFromMessage(breachMessage())
    expect(entry).toEqual(expect.objectContaining({
      key: 'test-1:p99_ms:',
      testId: 'test-1',
      testName: 'Checkout',
      metric: 'p99_ms',
      value: 1830,
      threshold: 1500,
      acknowledged: false,
      active: true,
    }))

    const stop = thresholdAlertFromMessage(breachMessage({ metric: 'error_pct', action: 'stop_requested' }))
    expect(stop?.action).toBe('stop_requested')

    const throughput = thresholdAlertFromMessage(breachMessage({
      metric: 'throughput_gap_pct',
      value: 180,
      threshold: 250,
      target_tps: 250,
      gap_pct: 28,
    }))
    expect(throughput).toEqual(expect.objectContaining({ targetTps: 250, gapPct: 28 }))

    const perTransaction = thresholdAlertFromMessage(breachMessage({
      metric: 'avg_server_processing_ms',
      transaction: 'checkout_submit',
      dependency_type: 'http',
      dependency_target: 'https://api.example.test',
    }))
    expect(perTransaction?.key).toBe('test-1:avg_server_processing_ms:checkout_submit')
    expect(perTransaction?.dependencyTarget).toBe('https://api.example.test')

    expect(thresholdAlertFromMessage(breachMessage({ test_id: undefined }))).toBeNull()
    expect(thresholdAlertFromMessage(breachMessage({ metric: '' }))).toBeNull()
    expect(thresholdAlertFromMessage(breachMessage({ value: 'high' }))).toBeNull()
    expect(thresholdAlertFromMessage(breachMessage({ threshold: undefined }))).toBeNull()
  })

  it('upsert dedupes by test, metric, and transaction while preserving firstSeenAt and acknowledged', () => {
    const first = thresholdAlertFromMessage(breachMessage())
    expect(first).toBeTruthy()
    if (!first) return
    let entries = upsertThresholdAlert([], first)
    entries = acknowledgeThresholdAlert(entries, first.key)

    const repeat = thresholdAlertFromMessage(breachMessage({ value: 2100, timestamp: '2026-07-30T12:00:05Z' }))
    expect(repeat).toBeTruthy()
    if (!repeat) return
    entries = upsertThresholdAlert(entries, repeat)

    expect(entries).toHaveLength(1)
    expect(entries[0].value).toBe(2100)
    expect(entries[0].firstSeenAt).toBe('2026-07-30T12:00:00Z')
    expect(entries[0].lastSeenAt).toBe('2026-07-30T12:00:05Z')
    expect(entries[0].acknowledged).toBe(true)

    const otherTransaction = thresholdAlertFromMessage(breachMessage({ metric: 'avg_transfer_ms', transaction: 'cart_update' }))
    expect(otherTransaction).toBeTruthy()
    if (!otherTransaction) return
    entries = upsertThresholdAlert(entries, otherTransaction)
    expect(entries).toHaveLength(2)
    expect(entries[0].key).toBe('test-1:avg_transfer_ms:cart_update')
  })

  it('resolves alerts after one missed metrics sample and starts a re-breach as unacknowledged', () => {
    const entry = thresholdAlertFromMessage(breachMessage())
    expect(entry).toBeTruthy()
    if (!entry) return
    const acknowledged = acknowledgeThresholdAlert([entry], entry.key)

    // The alert was present at 12:00:00, so the matching sample does not
    // resolve it. A later cutoff proves at least one sample omitted the alert.
    expect(resolveThresholdAlertsBefore(acknowledged, 'test-1', '2026-07-30T12:00:00Z')).toBe(acknowledged)
    const resolved = resolveThresholdAlertsBefore(acknowledged, 'test-1', '2026-07-30T12:00:01Z')
    expect(resolved[0]).toEqual(expect.objectContaining({ active: false, acknowledged: true }))

    const rebreach = thresholdAlertFromMessage(breachMessage({ timestamp: '2026-07-30T12:00:05Z', value: 2200 }))
    expect(rebreach).toBeTruthy()
    if (!rebreach) return
    const restarted = upsertThresholdAlert(resolved, rebreach)
    expect(restarted[0]).toEqual(expect.objectContaining({
      active: true,
      acknowledged: false,
      firstSeenAt: '2026-07-30T12:00:05Z',
      value: 2200,
    }))
  })

  it('resolves only the target run within its project and recognizes terminal run states', () => {
    const first = thresholdAlertFromMessage(breachMessage())
    const second = thresholdAlertFromMessage(breachMessage({ test_id: 'test-2' }))
    expect(second).toBeTruthy()
    expect(first).toBeTruthy()
    if (!first || !second) return
    const byProject = { 'project-1': [first, second] }

    const afterCutoff = resolveProjectThresholdAlertsBefore(byProject, 'project-1', 'test-1', '2026-07-30T12:00:01Z')
    expect(afterCutoff['project-1'].find(entry => entry.testId === 'test-1')?.active).toBe(false)
    expect(afterCutoff['project-1'].find(entry => entry.testId === 'test-2')?.active).toBe(true)

    const terminal = resolveProjectThresholdAlertsForTest(afterCutoff, 'project-1', 'test-2')
    expect(terminal['project-1'].every(entry => !entry.active)).toBe(true)
    expect(isThresholdAlertTerminalStatus('COMPLETE')).toBe(true)
    expect(isThresholdAlertTerminalStatus('FAILED')).toBe(true)
    expect(isThresholdAlertTerminalStatus('ABORTED')).toBe(true)
    expect(isThresholdAlertTerminalStatus(' complete ')).toBe(true)
    expect(isThresholdAlertTerminalStatus('STOPPING')).toBe(false)
  })

  it('caps stored alerts at MAX_THRESHOLD_ALERTS', () => {
    let entries: ThresholdAlertEntry[] = []
    for (let i = 0; i < MAX_THRESHOLD_ALERTS + 5; i++) {
      const entry = thresholdAlertFromMessage(breachMessage({ test_id: `test-${i}` }))
      expect(entry).toBeTruthy()
      if (!entry) continue
      entries = upsertThresholdAlert(entries, entry)
    }
    expect(entries).toHaveLength(MAX_THRESHOLD_ALERTS)
    expect(entries[0].testId).toBe(`test-${MAX_THRESHOLD_ALERTS + 4}`)
  })

  it('evicts the stalest entry by lastSeenAt when over the cap, never the fresh upsert', () => {
    let entries: ThresholdAlertEntry[] = []
    // Distinct, increasing lastSeenAt per entry: test-0 is the chronologically
    // stalest even though later ticks keep reordering the list.
    for (let i = 0; i < MAX_THRESHOLD_ALERTS; i++) {
      const entry = thresholdAlertFromMessage(breachMessage({
        test_id: `test-${i}`,
        timestamp: `2026-07-30T12:00:${String(i % 60).padStart(2, '0')}.${String(i).padStart(3, '0')}Z`,
      }))
      expect(entry).toBeTruthy()
      if (!entry) continue
      entries = upsertThresholdAlert(entries, entry)
    }
    const overflow = thresholdAlertFromMessage(breachMessage({ test_id: 'test-overflow', timestamp: '2026-07-30T12:05:00Z' }))
    expect(overflow).toBeTruthy()
    if (!overflow) return
    entries = upsertThresholdAlert(entries, overflow)

    expect(entries).toHaveLength(MAX_THRESHOLD_ALERTS)
    expect(entries[0].testId).toBe('test-overflow')
    expect(entries.some(entry => entry.testId === 'test-0')).toBe(false)
    expect(entries.some(entry => entry.testId === `test-${MAX_THRESHOLD_ALERTS - 1}`)).toBe(true)
  })

  it('appendProjectThresholdAlert scopes entries by project', () => {
    const entry = thresholdAlertFromMessage(breachMessage())
    expect(entry).toBeTruthy()
    if (!entry) return
    const byProject = appendProjectThresholdAlert({}, 'project-1', entry)
    expect(thresholdAlertsForProject(byProject, 'project-1')).toHaveLength(1)
    expect(thresholdAlertsForProject(byProject, 'project-2')).toHaveLength(0)
    expect(thresholdAlertsForProject(byProject)).toHaveLength(0)
    expect(appendProjectThresholdAlert(byProject, '', entry)).toBe(byProject)
  })

  it('formatThresholdAlertSummary labels each backend metric including stop_requested', () => {
    const p99 = thresholdAlertFromMessage(breachMessage())
    expect(p99 && formatThresholdAlertSummary(p99)).toBe('P99 latency 1830 ms exceeded threshold 1500 ms')

    const stop = thresholdAlertFromMessage(breachMessage({ metric: 'error_pct', value: 7.5, threshold: 5, action: 'stop_requested' }))
    expect(stop && formatThresholdAlertSummary(stop)).toBe('Error rate 7.50% exceeded threshold 5% — auto-stop requested')

    const throughput = thresholdAlertFromMessage(breachMessage({
      metric: 'throughput_gap_pct',
      value: 180,
      threshold: 250,
      target_tps: 250,
      gap_pct: 28,
    }))
    expect(throughput && formatThresholdAlertSummary(throughput)).toBe('Throughput 180 TPS is below target 250 TPS (28% gap)')

    // A goal run is judged over its 10 s window, in both directions.
    const over = thresholdAlertFromMessage(breachMessage({
      metric: 'throughput_over_pct',
      value: 27.8,
      threshold: 20,
      target_tps: 20,
      gap_pct: 39,
      window: '10 s avg',
    }))
    expect(over && formatThresholdAlertSummary(over)).toBe('Throughput 27.80 TPS (10 s avg) is above target 20 TPS (39% over)')
    expect(thresholdAlertMetricLabel('throughput_over_pct')).toBe('Throughput above target')

    const perTransaction = thresholdAlertFromMessage(breachMessage({ metric: 'avg_server_processing_ms', value: 420, threshold: 350, transaction: 'checkout_submit' }))
    expect(perTransaction && formatThresholdAlertSummary(perTransaction)).toBe('Avg server processing 420 ms exceeded threshold 350 ms for transaction checkout_submit')
  })
})
