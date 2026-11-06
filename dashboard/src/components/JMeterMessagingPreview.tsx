import type { JMeterImportResult } from '../types/scriptImports'
import { messagingScheduleSummary } from './jmeterMessaging'
import { countLabel } from '../utils/numberFormat'

export default function JMeterMessagingPreview({ result }: { result: JMeterImportResult }) {
  const groups = result.scenario_draft?.groups || []
  if (!result.scripts.some(script => script.config?.jmeter)) return null
  const curves = groups.flatMap(group => messagingScheduleSummary(group).map(curve => ({ ...curve, group })))
  const planned = curves.reduce((sum, curve) => sum + curve.planned, 0)
  const stats = result.stats
  const enabled = stats.enabled_workload_samplers ?? result.scripts.length
  const converted = stats.converted_workload_samplers ?? result.scripts.length
  const blocked = stats.blocked_workload_samplers ?? result.scripts.length
  const disabled = stats.disabled_workload_samplers ?? 0
  // Newer servers split the enabled samplers three ways, disjointly; an
  // older one sends only converted and blocked, which overlap.
  const split = stats.replay_ready_workload_samplers !== undefined || stats.review_gated_workload_samplers !== undefined || stats.unconverted_workload_samplers !== undefined
  const ready = stats.replay_ready_workload_samplers ?? 0
  const gated = stats.review_gated_workload_samplers ?? 0
  const unconverted = stats.unconverted_workload_samplers ?? 0
  const allReady = split && enabled > 0 && ready === enabled
  return <section className="lr-preserved" aria-label="Messaging fidelity">
    <h4>Messaging fidelity</h4>
    <p><strong>{allReady ? 'Converted drafts · replay ready' : 'Converted drafts · replay blocked'}</strong></p>
    {split
      ? <>
        <p>Of {countLabel(enabled, 'enabled workload sampler')}:</p>
        <ul aria-label="Workload sampler status">
          <li><strong>{ready}</strong> ready to replay</li>
          <li><strong>{gated}</strong> converted, held for review</li>
          <li><strong>{unconverted}</strong> not converted</li>
        </ul>
        <p>{countLabel(disabled, 'disabled workload sampler')} {disabled === 1 ? 'remains' : 'remain'} disabled.</p>
      </>
      // The converter counts a sampler as converted when it has a draft, reviewable or not, and as blocked when that draft cannot replay: the counts overlap.
      : <p>{converted} of {countLabel(enabled, 'enabled workload sampler')} {converted === 1 ? 'has' : 'have'} a converted draft; {blocked} of {enabled} {blocked === 1 ? 'is' : 'are'} blocked from replay. {countLabel(disabled, 'disabled workload sampler')} {disabled === 1 ? 'remains' : 'remain'} disabled.</p>}
    <p>JMeter messaging replay is not available yet: APEX has not verified its Kafka and Event Hubs clients against the JMeter plugins this plan uses, so these scripts are preserved for review and cannot run. Single generator required. Reference target: JMeter 5.4.1, JDK 17, Kafka 3.3.1, DI-KafkaMeter 8c2dcc9, PNOP Event Hubs 0.2.8, Throughput Shaping Timer 2.6.</p>
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
