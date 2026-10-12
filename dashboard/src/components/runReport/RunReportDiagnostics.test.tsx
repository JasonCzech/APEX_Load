// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import type { RunArchiveReport } from '../../types'
import RunReportDiagnostics from './RunReportDiagnostics'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('shows the HTTP version mix, both ratios, the finding and per-version phases', () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const report = { network_phases: {
    samples: 3, overall: { count: 3, avg_dns_ms: 1, avg_connect_ms: 20, avg_tls_ms: 6, avg_ttfb_ms: 40 },
    by_http_version: { '2': { count: 2, avg_connect_ms: 30, avg_tls_ms: 9, avg_ttfb_ms: 45 }, '3': { count: 1, avg_connect_ms: 12, avg_ttfb_ms: 30 } },
    version_mix: {
      actions: 4, by_version: { '2': 2, '3': 1, unknown: 1 }, by_selection: { upgraded: 2, exact_policy: 1, not_advertised: 1 },
      http3_policies: 4, upgrade_eligible: 2, fell_back: 1, quic_attempted: 3, quic_failed: 2, attempts: 5,
      fallback_ratio: { numerator: 1, denominator: 2, value: 0.5 }, quic_failure_ratio: { numerator: 2, denominator: 3, value: 2 / 3 },
      finding: { breaches: [], summary: '50.0% of upgrade-eligible auto+h3 actions fell back to TCP.' },
    },
  } } as unknown as RunArchiveReport
  act(() => root.render(<RunReportDiagnostics report={report} />))
  const mix = container.querySelector('table[aria-label="HTTP version mix"]')!
  expect(Array.from(mix.querySelectorAll('tbody tr')).map(row => row.textContent)).toEqual(['HTTP/22' + '50.0%', 'HTTP/31' + '25.0%', 'No response (version unknown)1' + '25.0%'])
  expect(container.textContent).toContain('Fell back to TCP50.00% (1 of 2 upgrade-eligible auto+h3 actions)')
  expect(container.textContent).toContain('QUIC failures66.67% (2 of 3 QUIC attempts)')
  expect(container.textContent).toContain('HTTP attempts (HTTP/3 actions)5')
  expect(container.textContent).toContain('Routing: Upgraded to HTTP/3 (2), Exact HTTP/3 policy (1), TCP: HTTP/3 not yet advertised (1)')
  expect(container.querySelector('[role="note"]')?.textContent).toBe('Protocol Mix: 50.0% of upgrade-eligible auto+h3 actions fell back to TCP.')
  const phases = container.querySelector('table[aria-label="Network phases by HTTP version"]')!
  expect(Array.from(phases.querySelectorAll('tbody th')).map(cell => cell.textContent)).toEqual(['HTTP/2', 'HTTP/3'])
  expect(container.querySelector('details[open]')?.querySelector('summary')?.textContent).toBe('HTTP versions')
  act(() => root.unmount())
})

it('renders an archive from before the version mix', () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  act(() => root.render(<RunReportDiagnostics report={{ network_phases: { samples: 1, overall: { count: 1, avg_ttfb_ms: 10 } } } as unknown as RunArchiveReport} />))
  expect(container.textContent).toContain('Network phases')
  expect(container.textContent).not.toContain('HTTP versions')
  act(() => root.unmount())
})

it('lists each load generator UDP limits and drops, and warns only for its own pod', () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const report = { host_network: [
    { source: 'gen-0', open_files: { available: true, soft: 65536 }, udp_buffers: { available: true, receive_max_bytes: 212992, send_max_bytes: 7340032 },
      udp_drops_at_end: { scope: 'pod', receive_buffer_errors: 12, send_buffer_errors: 0, in_errors: 15 }, udp_drops_delta: { receive_buffer_errors: 12, send_buffer_errors: 0, in_errors: 15 },
      warning: 'UDP packets were dropped on gen-0 during the run.' },
    { source: 'controller', udp_drops_at_end: { scope: 'network_namespace', receive_buffer_errors: 50, send_buffer_errors: 0, in_errors: 0 }, udp_drops_delta: { receive_buffer_errors: 45, send_buffer_errors: 0, in_errors: 0 } },
  ] } as unknown as RunArchiveReport
  act(() => root.render(<RunReportDiagnostics report={report} />))
  const rows = Array.from(container.querySelectorAll('table[aria-label="Load generator UDP"] tbody tr')).map(row => row.textContent)
  expect(rows[0]).toContain('gen-0')
  expect(rows[0]).toContain('65,536')
  expect(rows[0]).toContain('15')
  expect(rows[1]).toContain('45 (shared namespace)')
  expect(Array.from(container.querySelectorAll('[role="note"]')).map(note => note.textContent)).toEqual(['UDP packets were dropped on gen-0 during the run.'])
  act(() => root.unmount())
})

it('lists the HTTP/3 preflight checks and opens on a failure', () => {
  const container = document.createElement('div')
  const root = createRoot(container)
  const report = { http3_preflight: { policy: 'report', omitted: 3, results: [
    { source: 'gen-b', region: 'west', group: 'buyers', origin: 'https://api.example', policy: '3', ok: false, error_class: 'quic_handshake_timeout', error: 'timeout' },
    { source: 'gen-a', group: 'buyers', origin: 'https://{{param.host}}', policy: '3', ok: false, skipped: 'dynamic target: the origin is known only during the run' },
    { source: 'gen-a', group: 'buyers', origin: 'https://shop.example', policy: 'auto+h3', ok: true, handshake_ms: 4.2, address: '10.0.0.5:443' },
    { source: 'gen-a', group: 'buyers', origin: 'https://dead.example', policy: '3', ok: false, error_class: 'timeout', error: 'not_sent: timeout: no recent network activity' },
  ], unchecked: [{ source: 'gen-c', reason: 'generator does not support the HTTP/3 preflight' }] } } as unknown as RunArchiveReport
  act(() => root.render(<RunReportDiagnostics report={report} />))
  const table = container.querySelector('table[aria-label="HTTP/3 preflight"]')
  expect(table?.closest('details')?.open).toBe(true)
  const rows = Array.from(table?.querySelectorAll('tbody tr') ?? []).map(row => row.textContent)
  expect(rows[0]).toContain('gen-b (west)')
  expect(rows[0]).toContain('Failed (quic_handshake_timeout: timeout)')
  expect(rows[1]).toContain('Not checked: dynamic target')
  expect(rows[2]).toContain('Handshake completed in 4.2 ms to 10.0.0.5:443')
  expect(rows[3]).toContain('Failed (not_sent: timeout: no recent network activity)')
  expect(rows[4]).toContain('Not checked: generator does not support the HTTP/3 preflight')
  expect(container.textContent).toContain('Policy: report.')
  expect(container.textContent).toContain('3 more checks are not listed')
  act(() => root.unmount())
})
