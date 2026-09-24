import type { JMeterImportResult } from '../types/scriptImports'
import { messagingScheduleSummary } from './jmeterMessaging'

export default function JMeterMessagingPreview({ result }: { result: JMeterImportResult }) {
  const groups = result.scenario_draft?.groups || []
  if (!result.scripts.some(script => script.config?.jmeter)) return null
  const curves = groups.flatMap(group => messagingScheduleSummary(group).map(curve => ({ ...curve, group })))
  const planned = curves.reduce((sum, curve) => sum + curve.planned, 0)
  const stats = result.stats
  return <section className="lr-preserved" aria-label="Messaging fidelity">
    <h4>Messaging fidelity</h4>
    <p><strong>Converted drafts · replay blocked</strong></p>
    <p>{stats.converted_workload_samplers ?? result.scripts.length}/{stats.enabled_workload_samplers ?? result.scripts.length} enabled workload samplers mapped. {stats.blocked_workload_samplers ?? result.scripts.length} blocked; {stats.disabled_workload_samplers ?? 0} disabled workload samplers remain disabled.</p>
    <p>Single generator required. Strict compatibility is unverified. Reference target: JMeter 5.4.1, JDK 17, Kafka 3.3.1, DI-KafkaMeter 8c2dcc9, PNOP Event Hubs 0.2.8, Throughput Shaping Timer 2.6.</p>
    <p>Source timezone and property overrides require confirmation. Credentials and source defects remain execution blockers after warning acknowledgement.</p>
    {curves.length > 0 && <>
      <p>Duration: {Math.max(...curves.map(curve => curve.seconds)).toLocaleString()} seconds. Approximately {planned.toLocaleString()} planned samples, assuming one message per sampler, sufficient available threads, and no property overrides.</p>
      {curves.map(curve => <figure key={`${curve.group.source_group_id}-${curve.id}`}>
        <figcaption>{curve.group.name}: {curve.group.vusers} threads · {curve.group.ramp_up} thread ramp · peak {curve.peak} samples/s</figcaption>
        <svg viewBox="0 0 500 110" role="img" aria-label={`${curve.group.name} throughput schedule`} style={{ width: '100%', maxWidth: 600, height: 132 }}>
          <path d="M10 10V90H490" fill="none" stroke="currentColor" opacity="0.35" />
          <polyline points={curve.points} fill="none" stroke="currentColor" strokeWidth="2" />
          <text x="10" y="107" fontSize="10" fill="currentColor">0 s</text>
          <text x="440" y="107" fontSize="10" fill="currentColor">{curve.seconds} s</text>
        </svg>
      </figure>)}
    </>}
    {result.scripts.filter(script => script.protocol === 'kafka').map(script => <details key={script.name}>
      <summary>{script.name}: source and effective Kafka settings</summary>
      <p>These settings are preserved for review. Unsupported client behavior remains blocked.</p>
      <pre style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{JSON.stringify(script.actions.map(action => action.metadata?.jmeter_kafka).filter(Boolean), null, 2)}</pre>
    </details>)}
  </section>
}
