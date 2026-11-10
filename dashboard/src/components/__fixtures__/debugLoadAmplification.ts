// Shared by the Debug vs load model and panel tests.
import type { BrowserReplayEvent, BrowserReplayReport, ScriptDebugSession } from '../../types/replay'
import type { RunArchivePhaseStat, RunArchiveTimeAnatomy } from '../../types/timeAnatomy'
import type { DebugCompletionSnapshot } from '../debugCompletionModel'
import { captureDebugTransactionDefinitions } from '../debugTransactionModel'
import type { AmplificationLoadReport } from '../debugLoadAmplificationModel'

const at = (ms: number) => new Date(Date.UTC(2026, 10, 1, 12, 0, 0, ms)).toISOString()

function event(index: number, fields: Partial<BrowserReplayEvent> & { start: number; duration: number }): BrowserReplayEvent {
  const { start, duration, ...rest } = fields
  return {
    event_id: `e${index}`, event_index: index, phase: 'action', action_name: 'Request', action_type: 'request', target: '/', success: true,
    started_at: at(start), completed_at: at(start + duration), duration_ms: duration, ...rest,
  }
}

export function amplificationSnapshot(): DebugCompletionSnapshot {
  const events: BrowserReplayEvent[] = [
    event(0, { action_index: 0, request_index: 0, step_path: ['requests[0]'], action_name: 'Login', source_step_index: 3, start: 0, duration: 100, protocol_evidence: { timings: { dns_ms: 5, connect_ms: 10, tls_ms: 15, ttfb_ms: 50, duration_ms: 100 } } }),
    event(1, { action_index: 0, request_index: 1, step_path: ['requests[1]'], action_name: 'Search', start: 200, duration: 100, protocol_evidence: { timings: { ttfb_ms: 40, duration_ms: 100 } } }),
    event(2, { action_index: 0, request_index: 2, step_path: ['requests[2]'], action_name: 'Search', start: 400, duration: 50, protocol_evidence: { timings: { ttfb_ms: 30, duration_ms: 50 } } }),
    event(3, { action_index: 1, action_name: 'Home', start: 1000, duration: 40, protocol_evidence: { timings: { dns_ms: 2, connect_ms: 4, ttfb_ms: 20, duration_ms: 40 } } }),
    event(4, { action_index: 2, action_name: 'Redirected', start: 1100, duration: 60, protocol_evidence: { timings: { redirect_count: 1, ttfb_ms: 10, duration_ms: 60 } } }),
    event(5, { action_index: 3, action_name: 'Asset', start: 1200, duration: 30, network_entries: [{ url: 'https://x/a.js' } as NonNullable<BrowserReplayEvent['network_entries']>[number]] }),
    event(6, { action_index: 4, action_name: 'Pause', action_type: 'think_time', start: 1300, duration: 500 }),
  ]
  const report: BrowserReplayReport = { replay_session_id: 'run-1', group: 'Shop', protocol: 'http', started_at: at(0), completed_at: at(2000), success: true, events }
  const session: ScriptDebugSession = { id: 'run-1', status: 'completed', group: 'Shop', protocol: 'http', log_level: 'info', current_event_index: 6, started_at: at(0), completed_at: at(2000), report }
  const transactions = captureDebugTransactionDefinitions({
    name: 'Shop', version: '1.0', protocol: 'http', config: { base_url: '' },
    actions: [
      { name: 'Checkout flow', type: 'http_sequence', target: '', params: { requests: [{ name: 'Login', target: '/login' }, { name: 'Search', target: '/s' }, { name: 'Search', target: '/s' }] } },
      { name: 'Home', type: 'request', target: '/' },
      { name: 'Redirected', type: 'request', target: '/r' },
      { name: 'Asset', type: 'request', target: '/a.js' },
    ],
  })
  return { key: 'run-1', scriptName: 'Shop', draft: false, report, session, transactions }
}

const stat = (p95: number): RunArchivePhaseStat => ({ samples: 10, avg_ms: p95 / 2, p50_ms: p95 / 2, p90_ms: p95 * 0.9, p95_ms: p95, p99_ms: p95 * 1.5, max_ms: p95 * 2 })
const phases = (dns: number, connect: number, tls: number, server: number, download: number) => ({
  dns: stat(dns), connect: stat(connect), tls: stat(tls), server: stat(server), download: stat(download), unsplit: stat(0), total: stat(dns + connect + tls + server + download),
})
const request = (action: string, step: number | undefined, occurrence: number, values: ReturnType<typeof phases>) => ({
  key: `${step ?? 0}|${action}#${occurrence}`, action_name: action, ...(step ? { source_step_index: step } : {}), occurrence, samples: 10, failed: 0, presence_pct: 100,
  offset_ms: stat(0), phases: values, connections: { new: 0, reused: 10, unknown: 0, inconsistent: 0 },
})

export function amplificationReport(): AmplificationLoadReport {
  const anatomy = {
    schema: 'apexload.time_anatomy.v1', phase_model: 'sequential_clipped_v1', join_rule: '',
    transactions: [{
      name: 'Checkout flow', instances: 10, joined_instances: 10, failed_instances: 0, interrupted_instances: 0,
      duration: stat(600), decomposition: { instances: 10, avg_duration_ms: 300, phases: { dns_ms: 0, connect_ms: 0, tls_ms: 0, server_ms: 0, download_ms: 0, unsplit_ms: 0 }, non_request_ms: 0, overlap_ms: 0, avg_requests_per_instance: 3 },
      requests: [
        request('Login', 3, 1, phases(10, 20, 15, 124, 55)),
        // Steps differ from the debug run's (none): matched by name and occurrence.
        request('Search', 7, 1, phases(0, 0, 0, 80, 70)),
        request('Search', 8, 2, phases(0, 0, 0, 66, 34)),
      ],
    }],
  } as unknown as RunArchiveTimeAnatomy
  return {
    time_anatomy: anatomy,
    network_phases: { by_action: { Home: { count: 10, avg_dns_ms: 2, avg_connect_ms: 8, avg_tls_ms: 0, avg_ttfb_ms: 50 } } },
  }
}
