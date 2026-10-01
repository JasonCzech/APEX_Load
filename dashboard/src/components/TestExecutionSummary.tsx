import { useMemo } from 'react'
import type { StatisticalFinding } from '../types/analysis'
import { grpcSummaryLines } from '../utils/grpcDiagnostics'
import GRPCDiagnosticsPanel from './GRPCDiagnosticsPanel'
import WebSocketDiagnosticsPanel from './WebSocketDiagnosticsPanel'
import { websocketSummaryLines } from '../utils/websocketDiagnostics'

import { GoalConfig, LiveDependencyMap, LiveErrorPattern, MetricsHistoryPoint, RunComparisonBaseline, TransactionResponseTimeSLAResult, TxMetrics } from '../types'
import TransactionResponseTimeSLATable from './TransactionResponseTimeSLATable'

interface MetricsSummary {
 properties?: Record<string, TxMetrics>
  statistical_findings?: StatisticalFinding[]
  p50_ms?: number
  p90_ms?: number
  p95_ms?: number
  p99_ms?: number
  dependency_health?: Record<string, TxMetrics>
  dependency_map?: LiveDependencyMap
  error_patterns?: LiveErrorPattern[]
  soap_operations?: Record<string, TxMetrics>
  transaction_response_time_sla?: TransactionResponseTimeSLAResult[]
}

interface Props {
  transactions: TxMetrics[]
  metrics: MetricsSummary | null | undefined
  baseline?: RunComparisonBaseline
  history?: MetricsHistoryPoint[]
  goalConfig?: GoalConfig
}

export default function TestExecutionSummary({ transactions, metrics, baseline, history, goalConfig }: Props) {
  const previousTransactions = baseline?.transactions || {}
  const previousDependencyHealth = baseline?.dependency_health || {}
  const dependencyHealthValues = useMemo(() => Object.values(metrics?.dependency_health ?? {}), [metrics?.dependency_health])
  const topServerTelemetry = useMemo(() => transactions
    .filter((tx) => tx.server_telemetry && !tx.server_telemetry.grpc)
    .sort((left, right) => (right.server_telemetry?.avg_server_processing_ms || 0) - (left.server_telemetry?.avg_server_processing_ms || 0))
    .slice(0, 4), [transactions])
  const topHTTPDecomposition = useMemo(() => transactions
    .filter((tx) => hasHTTPTimingDecomposition(tx))
    .sort((left, right) => totalHTTPDecompositionMs(right) - totalHTTPDecompositionMs(left))
    .slice(0, 4), [transactions])
  const topHTTPDependencyHealth = useMemo(() => dependencyHealthValues
    .filter((tx) => tx.dependency_type?.startsWith('http') === true)
    .sort((left, right) => ((right.p95_ms ?? right.p99_ms ?? 0) - (left.p95_ms ?? left.p99_ms ?? 0)))
    .slice(0, 4), [dependencyHealthValues])
  const dependencyMap = metrics?.dependency_map
  const dependencySource = useMemo(() => dependencyMap?.nodes.find((node) => node.kind === 'source'), [dependencyMap?.nodes])
  const dependencyNodes = useMemo(() => dependencyMap?.nodes.filter((node) => node.kind === 'dependency') ?? [], [dependencyMap?.nodes])
  const errorPatterns = useMemo(() => metrics?.error_patterns?.slice(0, 4) ?? [], [metrics?.error_patterns])
  // Messaging panels cover the queue/stream protocols whose telemetry is
  // protocol-neutral (JMS family, Kafka, Event Hubs). Dead-letter and topic
  // fan-out below stay JMS-specific (their builders self-filter by jms_ prefix).
  const messagingTransactions = useMemo(() => transactions.filter(
    (tx) =>
      (tx.dependency_type?.startsWith('jms_') ||
        tx.dependency_type?.startsWith('kafka') ||
        tx.dependency_type?.startsWith('eventhubs')) &&
      tx.server_telemetry,
  ), [transactions])
  const jmsPayloadProfiles = useMemo(() => buildJMSPayloadProfiles(messagingTransactions), [messagingTransactions])
  const jmsMessagingProfiles = useMemo(() => buildJMSMessagingProfiles(messagingTransactions), [messagingTransactions])
  const jmsPayloadDistribution = useMemo(() => buildJMSPayloadDistribution(messagingTransactions), [messagingTransactions])
  const jmsPayloadHistogram = useMemo(() => buildJMSPayloadHistogram(messagingTransactions), [messagingTransactions])
  const jmsDeadLetterActivity = useMemo(() => buildJMSDeadLetterActivity(messagingTransactions), [messagingTransactions])
  const jmsDeadLetterProfiles = useMemo(() => buildJMSDeadLetterProfiles(jmsDeadLetterActivity), [jmsDeadLetterActivity])
  const jmsTopicFanOut = useMemo(() => buildJMSTopicFanOut(messagingTransactions), [messagingTransactions])
  const jmsTopicFanOutProfiles = useMemo(() => buildJMSTopicFanOutProfiles(jmsTopicFanOut), [jmsTopicFanOut])
  const jmsLagTrend = useMemo(() => buildJMSConsumerLagTrend(history), [history])
  // Event Hubs producer hotspots: streaming-ingestion signals (throttle / E2E
  // latency / partition skew) that only the eventhubs protocol emits. Messaging
  // server-telemetry is dependency-scoped, so it lives in dependency_health
  // (keyed by event hub), mirroring topHTTPDependencyHealth.
  const eventHubsProducers = useMemo(() => dependencyHealthValues
    .filter((tx) => tx.dependency_type?.startsWith('eventhubs') && hasEventHubsProducerTelemetry(tx))
    .sort((left, right) => eventHubsThrottleTotal(right) - eventHubsThrottleTotal(left))
    .slice(0, 6), [dependencyHealthValues])
  // Cross-protocol reliability: redelivery / dead-letter / duplicate / commit /
  // abort for any messaging dependency that reported a reliability signal (P4.1).
  const messagingReliability = useMemo(() => dependencyHealthValues
    .filter((tx) => isMessagingDependency(tx) && hasReliabilitySignal(tx))
    .sort((left, right) => messagingReliabilityWeight(right) - messagingReliabilityWeight(left))
    .slice(0, 8), [dependencyHealthValues])
  const topJMSPayloads = useMemo(() => [...messagingTransactions]
    .sort((left, right) => combinedPayloadKB(right) - combinedPayloadKB(left))
    .slice(0, 4), [messagingTransactions])
  const topJMSMessaging = useMemo(() => [...messagingTransactions]
    .filter((tx) => hasJMSMessagingTelemetry(tx))
    .sort((left, right) => totalObservedMessages(right) - totalObservedMessages(left))
    .slice(0, 4), [messagingTransactions])
  const protocolStreamTransactions = useMemo(() => transactions
    .filter((tx) => isProtocolStreamTransaction(tx) && hasProtocolMessagingTelemetry(tx))
  , [transactions])
  const protocolStreamProfiles = useMemo(() => buildProtocolStreamProfiles(protocolStreamTransactions), [protocolStreamTransactions])
  const topProtocolStreams = useMemo(() => [...protocolStreamTransactions]
    .sort((left, right) => totalObservedMessages(right) - totalObservedMessages(left))
    .slice(0, 4), [protocolStreamTransactions])
  const soapOperations = useMemo(() => Object.values(metrics?.soap_operations ?? {}), [metrics?.soap_operations])
  const topSOAPOperations = useMemo(() => [...soapOperations]
    .sort((left, right) => ((right.server_telemetry?.avg_server_processing_ms ?? right.avg_ms ?? 0) - (left.server_telemetry?.avg_server_processing_ms ?? left.avg_ms ?? 0)))
    .slice(0, 4), [soapOperations])
  const soapOperationProfiles = useMemo(() => buildSOAPOperationProfiles(soapOperations), [soapOperations])

  return (
    <>
      {Object.keys(metrics?.properties || {}).length > 0 && <section aria-label="Response property analytics"><h3>Response property analytics</h3><p>Approved categories only. Each observation can contribute to multiple properties.</p><table><thead><tr><th>Property category</th><th>Count</th><th>Errors</th><th>Mean ms</th><th>P95 ms</th></tr></thead><tbody>{Object.entries(metrics?.properties || {}).slice(0, 128).map(([name, stat]) => <tr key={name}><td>{name}</td><td>{stat.count}</td><td>{stat.errors}</td><td>{stat.avg_ms.toFixed(2)}</td><td>{stat.p95_ms?.toFixed(2) ?? 'Unavailable'}</td></tr>)}</tbody></table></section>}
      {(metrics?.statistical_findings?.length ?? 0) > 0 && <section className="tx-section" aria-label="Statistical findings">
        <h3>Statistical findings</h3><p>Advisory deviations from comparable load windows. These findings do not stop the run or establish a cause.</p>
        {metrics?.statistical_findings?.map(finding => <article key={finding.metric_name}><h4>{finding.metric_name}</h4><p>{finding.current.toFixed(2)} observed · {finding.z_score.toFixed(2)} standard deviations from {finding.baseline_buckets} comparable buckets (mean {finding.baseline_mean.toFixed(2)}, standard deviation {finding.baseline_stddev.toFixed(2)}).</p><p>Baseline: {new Date(finding.baseline_start).toLocaleTimeString()}–{new Date(finding.baseline_end).toLocaleTimeString()}</p></article>)}
      </section>}
      <GRPCDiagnosticsPanel transactions={transactions} />
      <WebSocketDiagnosticsPanel transactions={transactions} />
      {topServerTelemetry.length > 0 && (
        <div className="tx-section server-telemetry-section">
          <div className="server-telemetry-header">
            <div>
              <h3 className="chart-title">Live Server Telemetry</h3>
              <p className="server-telemetry-subtitle">
                Server-processing and transfer hotspots from the current run.
                {baseline?.source_test_name ? ` Baseline: ${baseline.source_test_name}${baseline.policy ? ` (${formatBaselinePolicy(baseline.policy)})` : ''}` : ' No previous run baseline available.'}
              </p>
            </div>
            {baseline?.completed_at && (
              <div className="server-telemetry-baseline-badge">
                Prev run {new Date(baseline.completed_at).toLocaleString()}
              </div>
            )}
          </div>
          <div className="server-telemetry-grid">
            {topServerTelemetry.map((tx) => {
              const previous = previousTransactions[tx.name]
              return (
                <div key={tx.name} className="server-telemetry-card">
                  <div className="server-telemetry-card-header">
                    <span className="server-telemetry-name">{tx.name}</span>
                    <span className="server-telemetry-target">{tx.dependency_target || tx.dependency_type || 'transaction'}</span>
                  </div>
                  <div className="server-telemetry-stat-grid">
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Server</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(tx.server_telemetry?.avg_server_processing_ms, ' ms')}{formatTelemetryDelta(tx.server_telemetry?.avg_server_processing_ms, previous?.server_telemetry?.avg_server_processing_ms, ' ms')}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Transfer</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(tx.server_telemetry?.avg_transfer_ms, ' ms')}{formatTelemetryDelta(tx.server_telemetry?.avg_transfer_ms, previous?.server_telemetry?.avg_transfer_ms, ' ms')}</span>
                    </div>
                    {tx.server_telemetry?.avg_first_event_ms !== undefined && (
                      <div className="server-telemetry-stat">
                        <span className="server-telemetry-label">First Event</span>
                        <span className="server-telemetry-value">
                          {formatTelemetryValue(tx.server_telemetry.avg_first_event_ms, ' ms')}
                          {tx.server_telemetry.max_first_event_ms !== undefined ? ` / ${formatTelemetryValue(tx.server_telemetry.max_first_event_ms, ' ms')}` : ''}
                          {formatTelemetryDelta(tx.server_telemetry.avg_first_event_ms, previous?.server_telemetry?.avg_first_event_ms, ' ms')}
                        </span>
                      </div>
                    )}
                    {tx.server_telemetry?.event_count !== undefined && (
                      <div className="server-telemetry-stat">
                        <span className="server-telemetry-label">Events</span>
                        <span className="server-telemetry-value">{tx.server_telemetry.event_count}</span>
                      </div>
                    )}
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Payload</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(tx.server_telemetry?.avg_request_payload_kb, ' KB', 2)} / {formatTelemetryValue(tx.server_telemetry?.avg_response_payload_kb, ' KB', 2)}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Reuse</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(tx.server_telemetry?.connection_reused_pct, '%', 0)}{formatTelemetryDelta(tx.server_telemetry?.connection_reused_pct, previous?.server_telemetry?.connection_reused_pct, '%', 0)}</span>
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}

      {topSOAPOperations.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live SOAP Operations</h3>
          <p className="server-telemetry-subtitle">Operation-level hotspots grouped by extracted SOAP operation tags from live results.</p>
          {soapOperationProfiles.length > 0 && (
            <div className="server-telemetry-grid">
              {soapOperationProfiles.map((profile) => (
                <div key={profile.label} className="server-telemetry-card">
                  <div className="server-telemetry-card-header">
                    <span className="server-telemetry-name">{profile.label}</span>
                    <span className="server-telemetry-target">{profile.operation.name}</span>
                  </div>
                  <div className="server-telemetry-stat-grid">
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Signal</span>
                      <span className="server-telemetry-value">{profile.metricValue}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Count</span>
                      <span className="server-telemetry-value">{profile.operation.count}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Avg</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(profile.operation.avg_ms, ' ms')}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Server</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(profile.operation.server_telemetry?.avg_server_processing_ms, ' ms')}</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
          <table className="tx-table">
            <thead>
              <tr>
                <th>Operation</th>
                <th>Count</th>
                <th>Errors</th>
                <th>Avg (ms)</th>
                <th>Server (ms)</th>
                <th>Transfer (ms)</th>
                <th>Target</th>
              </tr>
            </thead>
            <tbody>
              {topSOAPOperations.map((operation) => (
                <tr key={operation.name}>
                  <td className="tx-name">{operation.name}</td>
                  <td>{operation.count}</td>
                  <td className={operation.errors > 0 ? 'error-cell' : ''}>{operation.errors}</td>
                  <td>{operation.avg_ms?.toFixed(1)}</td>
                  <td>{formatTelemetryValue(operation.server_telemetry?.avg_server_processing_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(operation.server_telemetry?.avg_transfer_ms, ' ms')}</td>
                  <td>{operation.dependency_target || operation.dependency_type || 'soap'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {topHTTPDecomposition.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live HTTP Response Decomposition</h3>
          <p className="server-telemetry-subtitle">Average DNS, TCP, TLS, server, and transfer phases from live HTTP timing telemetry.</p>
          <div className="server-telemetry-grid">
            {topHTTPDecomposition.map((tx) => {
              const segments = buildHTTPDecompositionSegments(tx)
              return (
                <div key={`http-decomposition-${tx.name}`} className="server-telemetry-card">
                  <div className="server-telemetry-card-header">
                    <span className="server-telemetry-name">{tx.name}</span>
                    <span className="server-telemetry-target">{tx.dependency_target || tx.dependency_type || 'http'}</span>
                  </div>
                  <div className="server-telemetry-stat-grid">
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Stacked Phase View</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(totalHTTPDecompositionMs(tx), ' ms')}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">TTFB</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(tx.avg_ttfb_ms, ' ms')}</span>
                    </div>
                  </div>
                  <div aria-label={`HTTP decomposition chart for ${tx.name}`} style={{ display: 'grid', gap: '0.6rem' }}>
                    <div style={{ display: 'flex', height: '14px', borderRadius: '999px', overflow: 'hidden', background: 'rgba(148, 163, 184, 0.18)' }}>
                      {segments.map((segment) => (
                        <span
                          key={`${tx.name}-${segment.label}`}
                          title={`${segment.label} ${formatTelemetryValue(segment.value, ' ms')}`}
                          style={{
                            width: `${segment.widthPct}%`,
                            minWidth: segment.widthPct > 0 ? '8px' : '0',
                            background: segment.color,
                          }}
                        />
                      ))}
                    </div>
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.75rem' }}>
                      {segments.map((segment) => (
                        <span key={`${tx.name}-${segment.label}-legend`} className="server-telemetry-target">
                          {segment.label} {formatTelemetryValue(segment.value, ' ms')}
                        </span>
                      ))}
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
          <table className="tx-table">
            <thead>
              <tr>
                <th>Action</th>
                <th>DNS</th>
                <th>TCP</th>
                <th>TLS</th>
                <th>Server</th>
                <th>Transfer</th>
                <th>TTFB</th>
                <th>Total</th>
                <th>Target</th>
              </tr>
            </thead>
            <tbody>
              {topHTTPDecomposition.map((tx) => (
                <tr key={tx.name}>
                  <td className="tx-name">{tx.name}</td>
                  <td>{formatTelemetryValue(tx.avg_dns_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(tx.avg_connect_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(tx.avg_tls_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_server_processing_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_transfer_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(tx.avg_ttfb_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(totalHTTPDecompositionMs(tx), ' ms')}</td>
                  <td>{tx.dependency_target || tx.dependency_type || 'http'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {errorPatterns.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live Error Patterns</h3>
          <p className="server-telemetry-subtitle">Recurring live failures grouped by normalized signature, with first and latest sightings for quick triage.</p>
          <div className="error-pattern-grid">
            {errorPatterns.map((pattern) => (
              <div key={pattern.signature} className="error-pattern-card">
                <div className="error-pattern-card-header">
                  <span className="error-pattern-sample">{pattern.sample}</span>
                  <span className="dependency-map-status danger">{pattern.count} hits</span>
                </div>
                <div className="error-pattern-meta">
                  <span>First {formatTimestamp(pattern.first_seen)}</span>
                  <span>Latest {formatTimestamp(pattern.latest_seen)}</span>
                  <span>{formatPercentage(pattern.error_pct || 0)} errors</span>
                </div>
                <div className="error-pattern-tags">
                  {(pattern.actions || []).slice(0, 3).map((action) => <span key={action}>{action}</span>)}
                  {(pattern.affected_groups || []).slice(0, 3).map((group) => <span key={`group-${group}`}>{group}</span>)}
                </div>
                {pattern.snapshot_url && <div className="error-pattern-link">Snapshot query: {pattern.snapshot_url}</div>}
              </div>
            ))}
          </div>
        </div>
      )}

      {dependencyMap && dependencyNodes.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live Dependency Map</h3>
          <p className="server-telemetry-subtitle">Runtime dependency topology built from live transaction metadata, protocol classification, latency, and errors.</p>
          <div className="dependency-map">
            <div className="dependency-map-source">
              <span className="dependency-map-node-title">{dependencySource?.label || 'APEX Load VUsers'}</span>
              <span className="dependency-map-node-meta">{dependencyMap.edges.length} downstream links</span>
            </div>
            <div className="dependency-map-nodes">
              {dependencyNodes.map((node) => (
                <div key={node.id} className={`dependency-map-node ${node.status || 'unknown'}`}>
                  <div className="dependency-map-node-header">
                    <span className="dependency-map-node-title">{node.label}</span>
                    <span className={`dependency-map-status ${node.status || 'unknown'}`}>{formatDependencyStatus(node.status)}</span>
                  </div>
                  <div className="dependency-map-node-meta">{node.dependency_type || 'unknown'} dependency</div>
                  <div className="dependency-map-stats">
                    <span>{node.transactions || 0} tx</span>
                    <span>{formatPercentage(node.error_pct || 0)} errors</span>
                    <span>{formatTelemetryValue(node.p95_ms, ' ms')} P95</span>
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {topHTTPDependencyHealth.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live HTTP Dependency Health</h3>
          <p className="server-telemetry-subtitle">Downstream host latency and error aggregation computed directly from live dependency-target results.</p>
          <div className="server-telemetry-grid">
            {topHTTPDependencyHealth.map((tx) => (
              <div key={`http-dependency-${tx.name}`} className="server-telemetry-card">
                {(() => {
                  const previous = previousDependencyHealth[tx.name] || previousDependencyHealth[tx.dependency_target || '']
                  const sla = evaluateSLAScore(tx, goalConfig)
                  return (
                    <>
                <div className="server-telemetry-card-header">
                  <span className="server-telemetry-name">{tx.dependency_target || tx.name}</span>
                  <span className="server-telemetry-target">{tx.dependency_type || 'http'}</span>
                </div>
                <div className="server-telemetry-stat-grid">
                  <div className="server-telemetry-stat">
                    <span className="server-telemetry-label">P95</span>
                    <span className="server-telemetry-value">{formatTelemetryValue(tx.p95_ms, ' ms')}{formatTelemetryDelta(tx.p95_ms, previous?.p95_ms, ' ms')}</span>
                  </div>
                  <div className="server-telemetry-stat">
                    <span className="server-telemetry-label">P99</span>
                    <span className="server-telemetry-value">{formatTelemetryValue(tx.p99_ms, ' ms')}{formatTelemetryDelta(tx.p99_ms, previous?.p99_ms, ' ms')}</span>
                  </div>
                  <div className="server-telemetry-stat">
                    <span className="server-telemetry-label">Error Rate</span>
                    <span className="server-telemetry-value">{formatPercentage(transactionErrorPct(tx))}{formatTelemetryDelta(transactionErrorPct(tx), transactionErrorPct(previous), '%')}</span>
                  </div>
                  <div className="server-telemetry-stat">
                    <span className="server-telemetry-label">SLA</span>
                    <span className="server-telemetry-value">{formatSLALabel(sla)}</span>
                  </div>
                </div>
                    </>
                  )
                })()}
              </div>
            ))}
          </div>
          <table className="tx-table">
            <thead>
              <tr>
                <th>Target</th>
                <th>Count</th>
                <th>Error Rate</th>
                <th>P95</th>
                <th>P99</th>
                <th>SLA</th>
                <th>Server</th>
                <th>Transfer</th>
              </tr>
            </thead>
            <tbody>
              {topHTTPDependencyHealth.map((tx) => {
                const previous = previousDependencyHealth[tx.name] || previousDependencyHealth[tx.dependency_target || '']
                const sla = evaluateSLAScore(tx, goalConfig)
                return (
                <tr key={tx.name}>
                  <td className="tx-name">{tx.dependency_target || tx.name}</td>
                  <td>{tx.count}</td>
                  <td className={tx.errors > 0 ? 'error-cell' : ''}>{formatPercentage(transactionErrorPct(tx))}{formatTelemetryDelta(transactionErrorPct(tx), transactionErrorPct(previous), '%')}</td>
                  <td>{formatTelemetryValue(tx.p95_ms, ' ms')}{formatTelemetryDelta(tx.p95_ms, previous?.p95_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(tx.p99_ms, ' ms')}{formatTelemetryDelta(tx.p99_ms, previous?.p99_ms, ' ms')}</td>
                  <td className={sla?.breached ? 'error-cell' : ''}>{formatSLALabel(sla)}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_server_processing_ms, ' ms')}{formatTelemetryDelta(tx.server_telemetry?.avg_server_processing_ms, previous?.server_telemetry?.avg_server_processing_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_transfer_ms, ' ms')}{formatTelemetryDelta(tx.server_telemetry?.avg_transfer_ms, previous?.server_telemetry?.avg_transfer_ms, ' ms')}</td>
                </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {topJMSPayloads.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live Messaging Payload Hotspots</h3>
          <p className="server-telemetry-subtitle">Payload-heavy messaging actions (JMS, Kafka, Event Hubs) ranked from average request and response sizes. The distribution below reflects live size-class counts from the same telemetry stream.</p>
          {jmsPayloadProfiles.length > 0 && (
            <div className="server-telemetry-grid">
              {jmsPayloadProfiles.map((profile) => (
                <div key={profile.label} className="server-telemetry-card">
                  <div className="server-telemetry-card-header">
                    <span className="server-telemetry-name">{profile.label}</span>
                    <span className="server-telemetry-target">{profile.transaction.name}</span>
                  </div>
                  <div className="server-telemetry-stat-grid">
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Signal</span>
                      <span className="server-telemetry-value">{profile.metricValue}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Payload</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(profile.transaction.server_telemetry?.avg_request_payload_kb, ' KB', 2)} / {formatTelemetryValue(profile.transaction.server_telemetry?.avg_response_payload_kb, ' KB', 2)}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Transfer</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(profile.transaction.server_telemetry?.avg_transfer_ms, ' ms')}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Count</span>
                      <span className="server-telemetry-value">{profile.transaction.count}</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
          <table className="tx-table">
            <thead>
              <tr>
                <th>Action</th>
                <th>Count</th>
                <th>Avg Request</th>
                <th>Avg Response</th>
                <th>Combined</th>
                <th>Transfer (ms)</th>
                <th>Target</th>
              </tr>
            </thead>
            <tbody>
              {topJMSPayloads.map((tx) => (
                <tr key={tx.name}>
                  <td className="tx-name">{tx.name}</td>
                  <td>{tx.count}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_request_payload_kb, ' KB', 2)}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_response_payload_kb, ' KB', 2)}</td>
                  <td>{formatTelemetryValue(combinedPayloadKB(tx), ' KB', 2)}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_transfer_ms, ' ms')}</td>
                  <td>{tx.dependency_target || tx.dependency_type || 'jms'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {jmsPayloadDistribution.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live Messaging Payload Distribution</h3>
          <p className="server-telemetry-subtitle">Request and response size classes aggregated from live messaging telemetry (JMS, Kafka, Event Hubs).</p>
          <table className="tx-table">
            <thead>
              <tr>
                <th>Size Class</th>
                <th>Request Messages</th>
                <th>Response Messages</th>
              </tr>
            </thead>
            <tbody>
              {jmsPayloadDistribution.map((bucket) => (
                <tr key={bucket.label}>
                  <td className="tx-name">{bucket.label}</td>
                  <td>{bucket.requestCount}</td>
                  <td>{bucket.responseCount}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {jmsPayloadHistogram.length > 0 && (
    <div className="tx-section">
      <h3 className="chart-title">Live Messaging Payload Histogram</h3>
      <p className="server-telemetry-subtitle">Finer payload bands aggregated from exact live messaging request and response sizes (JMS, Kafka, Event Hubs).</p>
      <table className="tx-table">
        <thead>
          <tr>
            <th>Payload Band</th>
            <th>Request Messages</th>
            <th>Response Messages</th>
          </tr>
        </thead>
        <tbody>
          {jmsPayloadHistogram.map((bucket) => (
            <tr key={bucket.label}>
              <td className="tx-name">{bucket.label}</td>
              <td>{bucket.requestCount}</td>
              <td>{bucket.responseCount}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
    )}

      {jmsDeadLetterActivity.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live JMS Dead-Letter Activity</h3>
          <p className="server-telemetry-subtitle">Observed dead-letter destinations derived from explicitly classified JMS dead-letter actions in the current run.</p>
          {jmsDeadLetterProfiles.length > 0 && (
            <div className="server-telemetry-grid">
              {jmsDeadLetterProfiles.map((profile) => (
                <div key={profile.label} className="server-telemetry-card">
                  <div className="server-telemetry-card-header">
                    <span className="server-telemetry-name">{profile.label}</span>
                    <span className="server-telemetry-target">{profile.bucket.target}</span>
                  </div>
                  <div className="server-telemetry-stat-grid">
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Signal</span>
                      <span className="server-telemetry-value">{profile.metricValue}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Messages</span>
                      <span className="server-telemetry-value">{profile.bucket.messageCount}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Errors</span>
                      <span className="server-telemetry-value">{profile.bucket.errorCount}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Actions</span>
                      <span className="server-telemetry-value">{profile.bucket.actionCount}</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
          <table className="tx-table">
            <thead>
              <tr>
                <th>Destination</th>
                <th>Messages</th>
                <th>Errors</th>
                <th>Error Rate</th>
                <th>Actions</th>
                <th>Avg (ms)</th>
                <th>Peak P99 (ms)</th>
              </tr>
            </thead>
            <tbody>
              {jmsDeadLetterActivity.map((bucket) => (
                <tr key={bucket.target}>
                  <td className="tx-name">{bucket.target}</td>
                  <td>{bucket.messageCount}</td>
                  <td className={bucket.errorCount > 0 ? 'error-cell' : ''}>{bucket.errorCount}</td>
                  <td>{formatJMSDeadLetterErrorRate(bucket)}</td>
                  <td>{bucket.actionCount}</td>
                  <td>{formatTelemetryValue(bucket.avgMs, ' ms')}</td>
                  <td>{formatTelemetryValue(bucket.peakP99Ms, ' ms')}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {jmsTopicFanOut.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live JMS Topic Fan-Out</h3>
          <p className="server-telemetry-subtitle">Observed delivery spread by topic target, derived from sent versus received message totals across live JMS topic actions.</p>
          {jmsTopicFanOutProfiles.length > 0 && (
            <div className="server-telemetry-grid">
              {jmsTopicFanOutProfiles.map((profile) => (
                <div key={profile.label} className="server-telemetry-card">
                  <div className="server-telemetry-card-header">
                    <span className="server-telemetry-name">{profile.label}</span>
                    <span className="server-telemetry-target">{profile.topic.target}</span>
                  </div>
                  <div className="server-telemetry-stat-grid">
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Signal</span>
                      <span className="server-telemetry-value">{profile.metricValue}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Published</span>
                      <span className="server-telemetry-value">{profile.topic.sentCount}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Delivered</span>
                      <span className="server-telemetry-value">{profile.topic.receivedCount}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Actions</span>
                      <span className="server-telemetry-value">{profile.topic.actionCount}</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
          <table className="tx-table">
            <thead>
              <tr>
                <th>Topic</th>
                <th>Published</th>
                <th>Delivered</th>
                <th>Observed Fan-Out</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {jmsTopicFanOut.map((topic) => (
                <tr key={topic.target}>
                  <td className="tx-name">{topic.target}</td>
                  <td>{topic.sentCount}</td>
                  <td>{topic.receivedCount}</td>
                  <td>{formatJMSTopicFanOut(topic)}</td>
                  <td>{topic.actionCount}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {eventHubsProducers.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Event Hubs (Producer)</h3>
          <p className="server-telemetry-subtitle">Ingestion-pressure signals from live Event Hubs producer telemetry: throttle / ServerBusy / quota responses, producer→observed-consume end-to-end latency, and per-partition produce skew (coefficient of variation).</p>
          <table className="tx-table">
            <thead>
              <tr>
                <th>Event Hub</th>
                <th>Throttled</th>
                <th>Too Large</th>
                <th>E2E Latency (avg / max)</th>
                <th>Partition Skew</th>
                <th>Top Partitions</th>
              </tr>
            </thead>
            <tbody>
              {eventHubsProducers.map((tx) => {
                const st = tx.server_telemetry
                const partitions = topEventHubsPartitions(tx)
                return (
                  <tr key={tx.name}>
                    <td className="tx-name">{tx.dependency_target || tx.name}</td>
                    <td>{eventHubsThrottleTotal(tx)}</td>
                    <td>{st?.total_event_too_large_count ?? 0}</td>
                    <td>{formatTelemetryValue(st?.avg_end_to_end_latency_ms, ' ms')} / {formatTelemetryValue(st?.max_end_to_end_latency_ms, ' ms')}</td>
                    <td>{formatTelemetryValue(st?.partition_skew_pct, '%', 1)}</td>
                    <td>{partitions.length > 0 ? partitions.map((p) => `${p.partition}:${p.events}`).join('  ') : '—'}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {messagingReliability.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Messaging Reliability</h3>
          <p className="server-telemetry-subtitle">Per-dependency reliability signals across JMS, Kafka, and Event Hubs: redelivery, dead-letter, duplicate, out-of-order, and transaction commit/abort counts, with rates as a percent of received messages.</p>
          <table className="tx-table">
            <thead>
              <tr>
                <th>Dependency</th>
                <th>Protocol</th>
                <th>Redelivered</th>
                <th>Dead-Lettered</th>
                <th>Duplicate</th>
                <th>Out-of-Order</th>
                <th>Commit / Abort</th>
              </tr>
            </thead>
            <tbody>
              {messagingReliability.map((tx) => {
                const st = tx.server_telemetry
                const withRate = (count?: number, rate?: number) =>
                  (count ?? 0) > 0 ? `${count ?? 0}${(rate ?? 0) > 0 ? ` (${(rate ?? 0).toFixed(1)}%)` : ''}` : '—'
                return (
                  <tr key={tx.name}>
                    <td className="tx-name">{tx.dependency_target || tx.name}</td>
                    <td>{formatProtocolLabel(tx.dependency_type)}</td>
                    <td>{withRate(st?.redelivery_count, st?.redelivery_rate_pct)}</td>
                    <td>{withRate(st?.dead_lettered_count, st?.dead_letter_rate_pct)}</td>
                    <td>{withRate(st?.duplicate_count, st?.duplicate_rate_pct)}</td>
                    <td>{(st?.out_of_order_count ?? 0) > 0 ? st?.out_of_order_count : '—'}</td>
                    <td>{st?.commit_count ?? 0} / {st?.abort_count ?? 0}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {topJMSMessaging.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live Messaging</h3>
          <p className="server-telemetry-subtitle">Live message throughput, request-reply latency, and consumer lag from aggregated messaging telemetry (JMS, Kafka, Event Hubs).</p>
          {jmsMessagingProfiles.length > 0 && (
            <div className="server-telemetry-grid">
              {jmsMessagingProfiles.map((profile) => (
                <div key={profile.label} className="server-telemetry-card">
                  <div className="server-telemetry-card-header">
                    <span className="server-telemetry-name">{profile.label}</span>
                    <span className="server-telemetry-target">{profile.transaction.name}</span>
                  </div>
                  <div className="server-telemetry-stat-grid">
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Signal</span>
                      <span className="server-telemetry-value">{profile.metricValue}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Sent / Recv</span>
                      <span className="server-telemetry-value">{profile.transaction.server_telemetry?.messages_sent ?? 0} / {profile.transaction.server_telemetry?.messages_received ?? 0}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Rates</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(profile.transaction.server_telemetry?.avg_messages_sent_per_sec, ' msg/s')} / {formatTelemetryValue(profile.transaction.server_telemetry?.avg_messages_received_per_sec, ' msg/s')}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Latency</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(profile.transaction.server_telemetry?.avg_message_latency_ms, ' ms')}</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
          <table className="tx-table">
            <thead>
              <tr>
                <th>Action</th>
                <th>Sent</th>
                <th>Received</th>
                <th>Avg Send Rate</th>
                <th>Avg Receive Rate</th>
                <th>Msg Latency</th>
                <th>Lag</th>
                <th>Offset Lag</th>
                <th>Target</th>
              </tr>
            </thead>
            <tbody>
              {topJMSMessaging.map((tx) => (
                <tr key={tx.name}>
                  <td className="tx-name">{tx.name}</td>
                  <td>{tx.server_telemetry?.messages_sent ?? 0}</td>
                  <td>{tx.server_telemetry?.messages_received ?? 0}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_messages_sent_per_sec, ' msg/s')}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_messages_received_per_sec, ' msg/s')}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_message_latency_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_consumer_lag_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_consumer_offset_lag, ' msgs')}</td>
                  <td>{tx.dependency_target || tx.dependency_type || 'jms'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {topProtocolStreams.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Live WebSocket and gRPC Streams</h3>
          <p className="server-telemetry-subtitle">Message throughput, call latency, and protocol status from live WebSocket and gRPC actions.</p>
          {protocolStreamProfiles.length > 0 && (
            <div className="server-telemetry-grid">
              {protocolStreamProfiles.map((profile) => (
                <div key={profile.label} className="server-telemetry-card">
                  <div className="server-telemetry-card-header">
                    <span className="server-telemetry-name">{profile.label}</span>
                    <span className="server-telemetry-target">{profile.transaction.name}</span>
                  </div>
                  <div className="server-telemetry-stat-grid">
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Signal</span>
                      <span className="server-telemetry-value">{profile.metricValue}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Protocol</span>
                      <span className="server-telemetry-value">{formatProtocolLabel(profile.transaction.dependency_type)}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Sent / Recv</span>
                      <span className="server-telemetry-value">{profile.transaction.server_telemetry?.messages_sent ?? 0} / {profile.transaction.server_telemetry?.messages_received ?? 0}</span>
                    </div>
                    <div className="server-telemetry-stat">
                      <span className="server-telemetry-label">Rates</span>
                      <span className="server-telemetry-value">{formatTelemetryValue(profile.transaction.server_telemetry?.avg_messages_sent_per_sec, ' msg/s')} / {formatTelemetryValue(profile.transaction.server_telemetry?.avg_messages_received_per_sec, ' msg/s')}</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
          <table className="tx-table">
            <thead>
              <tr>
                <th>Action</th>
                <th>Protocol</th>
                <th>Sent</th>
                <th>Received</th>
                <th>Avg Send Rate</th>
                <th>Avg Receive Rate</th>
                <th>Latency</th>
                <th>Status / Error</th>
                <th>Target</th>
              </tr>
            </thead>
            <tbody>
              {topProtocolStreams.map((tx) => (
                <tr key={tx.name}>
                  <td className="tx-name">{tx.name}</td>
                  <td>{formatProtocolLabel(tx.dependency_type)}</td>
                  <td>{tx.server_telemetry?.messages_sent ?? 0}</td>
                  <td>{tx.server_telemetry?.messages_received ?? 0}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_messages_sent_per_sec, ' msg/s')}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_messages_received_per_sec, ' msg/s')}</td>
                  <td>{formatTelemetryValue(tx.server_telemetry?.avg_message_latency_ms ?? tx.avg_ms, ' ms')}</td>
                  <td>{formatProtocolStatus(tx)}</td>
                  <td>{tx.dependency_target || tx.dependency_type || 'protocol stream'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {jmsLagTrend && (
        <div className="tx-section server-telemetry-section">
          <div className="server-telemetry-header">
            <div>
              <h3 className="chart-title">JMS Consumer Lag Trend</h3>
              <p className="server-telemetry-subtitle">Visible-window consumer lag from live JMS history points.</p>
            </div>
            {jmsLagTrend.alert && (
              <div className="server-telemetry-baseline-badge">{jmsLagTrend.alert}</div>
            )}
          </div>
          <div className="server-telemetry-grid">
            <div className="server-telemetry-card">
              <div className="server-telemetry-card-header">
                <span className="server-telemetry-name">Latest Avg Lag</span>
                <span className="server-telemetry-target">Current window</span>
              </div>
              <div className="server-telemetry-stat-grid">
                <div className="server-telemetry-stat">
                  <span className="server-telemetry-label">Lag</span>
                  <span className="server-telemetry-value">{formatTelemetryValue(jmsLagTrend.latestAvgLag, ' ms')}</span>
                </div>
                <div className="server-telemetry-stat">
                  <span className="server-telemetry-label">Peak</span>
                  <span className="server-telemetry-value">{formatTelemetryValue(jmsLagTrend.latestPeakLag, ' ms')}</span>
                </div>
              </div>
            </div>
            <div className="server-telemetry-card">
              <div className="server-telemetry-card-header">
                <span className="server-telemetry-name">Window Peak</span>
                <span className="server-telemetry-target">Visible trend</span>
              </div>
              <div className="server-telemetry-stat-grid">
                <div className="server-telemetry-stat">
                  <span className="server-telemetry-label">Avg Lag Peak</span>
                  <span className="server-telemetry-value">{formatTelemetryValue(jmsLagTrend.peakAvgLag, ' ms')}</span>
                </div>
                <div className="server-telemetry-stat">
                  <span className="server-telemetry-label">Window Avg</span>
                  <span className="server-telemetry-value">{formatTelemetryValue(jmsLagTrend.windowAvgLag, ' ms')}</span>
                </div>
              </div>
            </div>
          </div>
          <table className="tx-table">
            <thead>
              <tr>
                <th>Time</th>
                <th>Avg Lag</th>
                <th>Peak Lag</th>
              </tr>
            </thead>
            <tbody>
              {jmsLagTrend.points.map((point) => (
                <tr key={`${point.time}-${point.avgLag}-${point.peakLag}`}>
                  <td className="tx-name">{point.time}</td>
                  <td>{formatTelemetryValue(point.avgLag, ' ms')}</td>
                  <td>{formatTelemetryValue(point.peakLag, ' ms')}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <TransactionResponseTimeSLATable rows={metrics?.transaction_response_time_sla ?? []} />

      {transactions.length > 0 && (
        <div className="tx-section">
          <h3 className="chart-title">Transaction Breakdown</h3>
          <table className="tx-table">
            <thead>
              <tr>
                <th>Transaction</th>
                <th>Count</th>
                <th>Errors</th>
                <th>Avg (ms)</th>
                <th>P50 (ms)</th>
                <th>P90 (ms)</th>
                <th>P95 (ms)</th>
                <th>P99 (ms)</th>
                <th>SLA</th>
                <th>Server Telemetry</th>
              </tr>
            </thead>
            <tbody>
              {transactions.map((tx) => {
                const previous = previousTransactions[tx.name]
                const sla = evaluateSLAScore(tx, goalConfig)
                return (
                <tr key={tx.name}>
                  <td className="tx-name">{tx.name}</td>
                  <td>{tx.count}</td>
                  <td className={tx.errors > 0 ? 'error-cell' : ''}>{tx.errors}</td>
                  <td>{formatTelemetryValue(tx.avg_ms, ' ms')}{formatTelemetryDelta(tx.avg_ms, previous?.avg_ms, ' ms')}</td>
                  <td>{tx.p50_ms?.toFixed(1)}</td>
                  <td>{tx.p90_ms?.toFixed(1)}</td>
                  <td>{formatTelemetryValue(tx.p95_ms, ' ms')}{formatTelemetryDelta(tx.p95_ms, previous?.p95_ms, ' ms')}</td>
                  <td>{formatTelemetryValue(tx.p99_ms, ' ms')}{formatTelemetryDelta(tx.p99_ms, previous?.p99_ms, ' ms')}</td>
                  <td className={sla?.breached ? 'error-cell' : ''}>{formatSLALabel(sla)}</td>
                  <td>{renderServerTelemetry(tx, previous)}</td>
                </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {metrics && (
        <div className="percentile-bar">
          <div className="pct-item">
            <span className="pct-label">P50</span>
            <span className="pct-value">{metrics.p50_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P90</span>
            <span className="pct-value">{metrics.p90_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P95</span>
            <span className="pct-value">{metrics.p95_ms?.toFixed(1)} ms</span>
          </div>
          <div className="pct-item">
            <span className="pct-label">P99</span>
            <span className="pct-value">{metrics.p99_ms?.toFixed(1)} ms</span>
          </div>
        </div>
      )}
    </>
  )
}

interface SOAPOperationProfile {
  label: string
  metricValue: string
  operation: TxMetrics
}

interface JMSPayloadProfile {
  label: string
  metricValue: string
  transaction: TxMetrics
}

interface JMSMessagingProfile {
  label: string
  metricValue: string
  transaction: TxMetrics
}

interface ProtocolStreamProfile {
  label: string
  metricValue: string
  transaction: TxMetrics
}

interface JMSDeadLetterProfile {
  label: string
  metricValue: string
  bucket: JMSDeadLetterBucket
}

interface JMSTopicFanOutProfile {
  label: string
  metricValue: string
  topic: JMSTopicFanOutBucket
}

interface JMSPayloadDistributionBucket {
  label: string
  requestCount: number
  responseCount: number
}

interface JMSPayloadHistogramBucket {
	label: string
	requestCount: number
	responseCount: number
}

interface JMSTopicFanOutBucket {
  target: string
  sentCount: number
  receivedCount: number
  actionCount: number
  fanOutRatio?: number
}

interface JMSDeadLetterBucket {
  target: string
  messageCount: number
  errorCount: number
  actionCount: number
  avgMs: number
  peakP99Ms: number
}

interface JMSConsumerLagTrendPoint {
  time: string
  avgLag: number
  peakLag: number
}

interface JMSConsumerLagTrend {
  alert?: string
  latestAvgLag: number
  latestPeakLag: number
  peakAvgLag: number
  windowAvgLag: number
  points: JMSConsumerLagTrendPoint[]
}

function buildSOAPOperationProfiles(operations: TxMetrics[]): SOAPOperationProfile[] {
  if (operations.length === 0) {
    return []
  }

  const profiles: SOAPOperationProfile[] = []
  const seen = new Set<string>()
  const candidates: Array<SOAPOperationProfile | null> = [
    buildSOAPOperationProfile('Busiest Operation', operations, (operation) => operation.count, (operation) => `${operation.count} calls`),
    buildSOAPOperationProfile('Slowest Avg Operation', operations, (operation) => operation.avg_ms, (operation) => formatTelemetryValue(operation.avg_ms, ' ms')),
    buildSOAPOperationProfile('Highest Error Rate', operations, (operation) => operation.count > 0 ? operation.errors / operation.count : -1, (operation) => `${((operation.count > 0 ? operation.errors / operation.count : 0) * 100).toFixed(1)}%`),
    buildSOAPOperationProfile('Heaviest Server Stage', operations, (operation) => operation.server_telemetry?.avg_server_processing_ms ?? -1, (operation) => formatTelemetryValue(operation.server_telemetry?.avg_server_processing_ms, ' ms')),
  ]

  for (const candidate of candidates) {
    if (!candidate || seen.has(candidate.operation.name)) {
      continue
    }
    seen.add(candidate.operation.name)
    profiles.push(candidate)
  }

  if (profiles.length === 0) {
    const fallback = [...operations].sort((left, right) => right.avg_ms - left.avg_ms)[0]
    if (fallback) {
      profiles.push({
        label: 'SOAP Operation Profile',
        metricValue: formatTelemetryValue(fallback.avg_ms, ' ms'),
        operation: fallback,
      })
    }
  }

  return profiles
}

function buildSOAPOperationProfile(
  label: string,
  operations: TxMetrics[],
  metric: (operation: TxMetrics) => number,
  format: (operation: TxMetrics) => string,
): SOAPOperationProfile | null {
  const candidate = [...operations].sort((left, right) => metric(right) - metric(left))[0]
  if (!candidate || metric(candidate) < 0) {
    return null
  }
  return {
    label,
    metricValue: format(candidate),
    operation: candidate,
  }
}

function buildJMSPayloadProfiles(transactions: TxMetrics[]): JMSPayloadProfile[] {
  if (transactions.length === 0) {
    return []
  }

  const candidates: Array<JMSPayloadProfile | null> = [
    buildJMSPayloadProfile('Largest Request Payload', transactions, (tx) => tx.server_telemetry?.avg_request_payload_kb ?? -1, (tx) => formatTelemetryValue(tx.server_telemetry?.avg_request_payload_kb, ' KB', 2)),
    buildJMSPayloadProfile('Largest Response Payload', transactions, (tx) => tx.server_telemetry?.avg_response_payload_kb ?? -1, (tx) => formatTelemetryValue(tx.server_telemetry?.avg_response_payload_kb, ' KB', 2)),
    buildJMSPayloadProfile('Heaviest Round Trip', transactions, (tx) => combinedPayloadKB(tx), (tx) => formatTelemetryValue(combinedPayloadKB(tx), ' KB', 2)),
    buildJMSPayloadProfile('Slowest Transfer Stage', transactions, (tx) => tx.server_telemetry?.avg_transfer_ms ?? -1, (tx) => formatTelemetryValue(tx.server_telemetry?.avg_transfer_ms, ' ms')),
  ]

  return candidates.filter((candidate): candidate is JMSPayloadProfile => Boolean(candidate))
}

function buildJMSPayloadProfile(
  label: string,
  transactions: TxMetrics[],
  metric: (transaction: TxMetrics) => number,
  format: (transaction: TxMetrics) => string,
): JMSPayloadProfile | null {
  const candidate = [...transactions].sort((left, right) => metric(right) - metric(left))[0]
  if (!candidate || metric(candidate) < 0) {
    return null
  }
  return {
    label,
    metricValue: format(candidate),
    transaction: candidate,
  }
}

function buildJMSMessagingProfiles(transactions: TxMetrics[]): JMSMessagingProfile[] {
  if (transactions.length === 0) {
    return []
  }

  const candidates: Array<JMSMessagingProfile | null> = [
    buildJMSMessagingProfile('Highest Message Latency', transactions, (tx) => tx.server_telemetry?.avg_message_latency_ms ?? -1, (tx) => formatTelemetryValue(tx.server_telemetry?.avg_message_latency_ms, ' ms')),
    buildJMSMessagingProfile('Fastest Producer Rate', transactions, (tx) => tx.server_telemetry?.avg_messages_sent_per_sec ?? -1, (tx) => formatTelemetryValue(tx.server_telemetry?.avg_messages_sent_per_sec, ' msg/s')),
    buildJMSMessagingProfile('Fastest Consumer Rate', transactions, (tx) => tx.server_telemetry?.avg_messages_received_per_sec ?? -1, (tx) => formatTelemetryValue(tx.server_telemetry?.avg_messages_received_per_sec, ' msg/s')),
    buildJMSMessagingProfile('Largest Message Volume', transactions, (tx) => totalObservedMessages(tx), (tx) => `${totalObservedMessages(tx)} msgs`),
  ]

  return candidates.filter((candidate): candidate is JMSMessagingProfile => Boolean(candidate))
}

function buildJMSMessagingProfile(
  label: string,
  transactions: TxMetrics[],
  metric: (transaction: TxMetrics) => number,
  format: (transaction: TxMetrics) => string,
): JMSMessagingProfile | null {
  const candidate = [...transactions].sort((left, right) => metric(right) - metric(left))[0]
  if (!candidate || metric(candidate) <= 0) {
    return null
  }
  return {
    label,
    metricValue: format(candidate),
    transaction: candidate,
  }
}

function buildProtocolStreamProfiles(transactions: TxMetrics[]): ProtocolStreamProfile[] {
  if (transactions.length === 0) {
    return []
  }

  const candidates: Array<ProtocolStreamProfile | null> = [
    buildProtocolStreamProfile('Highest Message Latency', transactions, (tx) => tx.server_telemetry?.avg_message_latency_ms ?? tx.avg_ms ?? -1, (tx) => formatTelemetryValue(tx.server_telemetry?.avg_message_latency_ms ?? tx.avg_ms, ' ms')),
    buildProtocolStreamProfile('Fastest Send Rate', transactions, (tx) => tx.server_telemetry?.avg_messages_sent_per_sec ?? -1, (tx) => formatTelemetryValue(tx.server_telemetry?.avg_messages_sent_per_sec, ' msg/s')),
    buildProtocolStreamProfile('Fastest Receive Rate', transactions, (tx) => tx.server_telemetry?.avg_messages_received_per_sec ?? -1, (tx) => formatTelemetryValue(tx.server_telemetry?.avg_messages_received_per_sec, ' msg/s')),
    buildProtocolStreamProfile('Largest Message Volume', transactions, (tx) => totalObservedMessages(tx), (tx) => `${totalObservedMessages(tx)} msgs`),
  ]

  return candidates.filter((candidate): candidate is ProtocolStreamProfile => Boolean(candidate))
}

function buildProtocolStreamProfile(
  label: string,
  transactions: TxMetrics[],
  metric: (transaction: TxMetrics) => number,
  format: (transaction: TxMetrics) => string,
): ProtocolStreamProfile | null {
  const candidate = [...transactions].sort((left, right) => metric(right) - metric(left))[0]
  if (!candidate || metric(candidate) <= 0) {
    return null
  }
  return {
    label,
    metricValue: format(candidate),
    transaction: candidate,
  }
}

function buildJMSPayloadDistribution(transactions: TxMetrics[]): JMSPayloadDistributionBucket[] {
  const requestCounts: Record<string, number> = {}
  const responseCounts: Record<string, number> = {}

  transactions.forEach((transaction) => {
    Object.entries(transaction.server_telemetry?.request_size_classes ?? {}).forEach(([label, count]) => {
      requestCounts[label] = (requestCounts[label] ?? 0) + count
    })
    Object.entries(transaction.server_telemetry?.response_size_classes ?? {}).forEach(([label, count]) => {
      responseCounts[label] = (responseCounts[label] ?? 0) + count
    })
  })

  const orderedLabels = ['small', 'medium', 'large']
  const labels = orderedLabels.filter((label) => requestCounts[label] || responseCounts[label])
  return labels.map((label) => ({
    label,
    requestCount: requestCounts[label] ?? 0,
    responseCount: responseCounts[label] ?? 0,
  }))
}

function buildJMSPayloadHistogram(transactions: TxMetrics[]): JMSPayloadHistogramBucket[] {
  const requestCounts: Record<string, number> = {}
  const responseCounts: Record<string, number> = {}

  transactions.forEach((transaction) => {
    Object.entries(transaction.server_telemetry?.request_payload_histogram ?? {}).forEach(([label, count]) => {
      requestCounts[label] = (requestCounts[label] ?? 0) + count
    })
    Object.entries(transaction.server_telemetry?.response_payload_histogram ?? {}).forEach(([label, count]) => {
      responseCounts[label] = (responseCounts[label] ?? 0) + count
    })
  })

  const orderedLabels = ['0-1 KB', '1-4 KB', '4-16 KB', '16-64 KB', '64+ KB']
  const labels = orderedLabels.filter((label) => requestCounts[label] || responseCounts[label])
  return labels.map((label) => ({
    label,
    requestCount: requestCounts[label] ?? 0,
    responseCount: responseCounts[label] ?? 0,
  }))
}

function buildJMSDeadLetterActivity(transactions: TxMetrics[]): JMSDeadLetterBucket[] {
  const buckets = new Map<string, JMSDeadLetterBucket & { totalDurationMs: number }>()

  transactions.forEach((transaction) => {
    if (!transaction.dependency_type?.startsWith('jms_dead_letter_')) {
      return
    }
    const target = transaction.dependency_target || transaction.name
    const existing = buckets.get(target) ?? {
      target,
      messageCount: 0,
      errorCount: 0,
      actionCount: 0,
      avgMs: 0,
      peakP99Ms: 0,
      totalDurationMs: 0,
    }
    existing.messageCount += transaction.count
    existing.errorCount += transaction.errors
    existing.actionCount += 1
    existing.totalDurationMs += transaction.avg_ms * transaction.count
    existing.peakP99Ms = Math.max(existing.peakP99Ms, transaction.p99_ms)
    buckets.set(target, existing)
  })

  return [...buckets.values()]
    .filter((bucket) => bucket.messageCount > 0)
    .map((bucket) => ({
      target: bucket.target,
      messageCount: bucket.messageCount,
      errorCount: bucket.errorCount,
      actionCount: bucket.actionCount,
      avgMs: bucket.messageCount > 0 ? bucket.totalDurationMs / bucket.messageCount : 0,
      peakP99Ms: bucket.peakP99Ms,
    }))
    .sort((left, right) => {
      if (left.messageCount !== right.messageCount) {
        return right.messageCount - left.messageCount
      }
      if (left.errorCount !== right.errorCount) {
        return right.errorCount - left.errorCount
      }
      return left.target.localeCompare(right.target)
    })
}

function buildJMSDeadLetterProfiles(buckets: JMSDeadLetterBucket[]): JMSDeadLetterProfile[] {
  if (buckets.length === 0) {
    return []
  }

  const candidates: Array<JMSDeadLetterProfile | null> = [
    buildJMSDeadLetterProfile('Busiest Dead-Letter Destination', buckets, (bucket) => bucket.messageCount, (bucket) => `${bucket.messageCount} msgs`),
    buildJMSDeadLetterProfile('Highest Dead-Letter Error Rate', buckets, (bucket) => deadLetterErrorRate(bucket), (bucket) => formatJMSDeadLetterErrorRate(bucket)),
    buildJMSDeadLetterProfile('Most Distinct DLQ Actions', buckets, (bucket) => bucket.actionCount, (bucket) => `${bucket.actionCount} actions`),
  ]

  return candidates.filter((candidate): candidate is JMSDeadLetterProfile => Boolean(candidate))
}

function buildJMSDeadLetterProfile(
  label: string,
  buckets: JMSDeadLetterBucket[],
  metric: (bucket: JMSDeadLetterBucket) => number,
  format: (bucket: JMSDeadLetterBucket) => string,
): JMSDeadLetterProfile | null {
  const candidate = [...buckets].sort((left, right) => metric(right) - metric(left))[0]
  if (!candidate || metric(candidate) <= 0) {
    return null
  }
  return {
    label,
    metricValue: format(candidate),
    bucket: candidate,
  }
}

function buildJMSTopicFanOut(transactions: TxMetrics[]): JMSTopicFanOutBucket[] {
  const buckets = new Map<string, JMSTopicFanOutBucket>()

  transactions.forEach((transaction) => {
    if (transaction.dependency_type !== 'jms_topic') {
      return
    }
    const target = transaction.dependency_target || transaction.name
    const existing = buckets.get(target) ?? {
      target,
      sentCount: 0,
      receivedCount: 0,
      actionCount: 0,
    }
    existing.sentCount += transaction.server_telemetry?.messages_sent ?? 0
    existing.receivedCount += transaction.server_telemetry?.messages_received ?? 0
    existing.actionCount += 1
    buckets.set(target, existing)
  })

  return [...buckets.values()]
    .filter((bucket) => bucket.sentCount > 0 || bucket.receivedCount > 0)
    .map((bucket) => ({
      ...bucket,
      fanOutRatio: bucket.sentCount > 0 && bucket.receivedCount > 0
        ? bucket.receivedCount / bucket.sentCount
        : undefined,
    }))
    .sort((left, right) => {
      const leftRatio = left.fanOutRatio ?? -1
      const rightRatio = right.fanOutRatio ?? -1
      if (leftRatio !== rightRatio) {
        return rightRatio - leftRatio
      }
      if (left.receivedCount !== right.receivedCount) {
        return right.receivedCount - left.receivedCount
      }
      return left.target.localeCompare(right.target)
    })
}

function buildJMSTopicFanOutProfiles(topics: JMSTopicFanOutBucket[]): JMSTopicFanOutProfile[] {
  if (topics.length === 0) {
    return []
  }

  const candidates: Array<JMSTopicFanOutProfile | null> = [
    buildJMSTopicFanOutProfile('Widest Observed Fan-Out', topics, (topic) => topic.fanOutRatio ?? -1, (topic) => formatJMSTopicFanOut(topic)),
    buildJMSTopicFanOutProfile('Highest Delivery Volume', topics, (topic) => topic.receivedCount, (topic) => `${topic.receivedCount} copies`),
  ]

  return candidates.filter((candidate): candidate is JMSTopicFanOutProfile => Boolean(candidate))
}

function buildJMSTopicFanOutProfile(
  label: string,
  topics: JMSTopicFanOutBucket[],
  metric: (topic: JMSTopicFanOutBucket) => number,
  format: (topic: JMSTopicFanOutBucket) => string,
): JMSTopicFanOutProfile | null {
  const candidate = [...topics].sort((left, right) => metric(right) - metric(left))[0]
  if (!candidate || metric(candidate) <= 0) {
    return null
  }
  return {
    label,
    metricValue: format(candidate),
    topic: candidate,
  }
}

function buildJMSConsumerLagTrend(history: MetricsHistoryPoint[] | undefined): JMSConsumerLagTrend | null {
  const points = (history ?? [])
    .filter((point) => (point.jmsConsumerLagMs ?? 0) > 0 || (point.jmsPeakConsumerLagMs ?? 0) > 0)
    .map((point) => ({
      time: point.time,
      avgLag: point.jmsConsumerLagMs ?? 0,
      peakLag: point.jmsPeakConsumerLagMs ?? 0,
    }))

  if (points.length === 0) {
    return null
  }

  const visiblePoints = points.slice(-6)
  const latest = visiblePoints[visiblePoints.length - 1]
  const peakAvgLag = visiblePoints.reduce((maxLag, point) => Math.max(maxLag, point.avgLag), 0)
  const windowAvgLag = visiblePoints.reduce((sum, point) => sum + point.avgLag, 0) / visiblePoints.length

  return {
    alert: visiblePoints.length > 1 && latest.avgLag > 0 && latest.avgLag >= peakAvgLag
      ? 'Current lag is at the visible-window peak.'
      : undefined,
    latestAvgLag: latest.avgLag,
    latestPeakLag: latest.peakLag,
    peakAvgLag,
    windowAvgLag,
    points: visiblePoints,
  }
}

function combinedPayloadKB(transaction: TxMetrics): number {
  return (transaction.server_telemetry?.avg_request_payload_kb ?? 0) + (transaction.server_telemetry?.avg_response_payload_kb ?? 0)
}

function totalObservedMessages(transaction: TxMetrics): number {
  return (transaction.server_telemetry?.messages_sent ?? 0) + (transaction.server_telemetry?.messages_received ?? 0)
}

function isProtocolStreamTransaction(transaction: TxMetrics): boolean {
  return transaction.dependency_type === 'websocket' || transaction.dependency_type === 'grpc'
}

function hasProtocolMessagingTelemetry(transaction: TxMetrics): boolean {
  return totalObservedMessages(transaction) > 0
    || (transaction.server_telemetry?.avg_message_latency_ms ?? 0) > 0
    || (transaction.server_telemetry?.avg_messages_sent_per_sec ?? 0) > 0
    || (transaction.server_telemetry?.avg_messages_received_per_sec ?? 0) > 0
}

function hasJMSMessagingTelemetry(transaction: TxMetrics): boolean {
  return totalObservedMessages(transaction) > 0
    || (transaction.server_telemetry?.avg_message_latency_ms ?? 0) > 0
    || (transaction.server_telemetry?.avg_messages_sent_per_sec ?? 0) > 0
    || (transaction.server_telemetry?.avg_messages_received_per_sec ?? 0) > 0
}

// eventHubsThrottleTotal sums the streaming backpressure counters (throttle +
// ServerBusy + quota) a transaction's producer telemetry reported.
function eventHubsThrottleTotal(transaction: TxMetrics): number {
  const st = transaction.server_telemetry
  if (!st) {
    return 0
  }
  return (st.total_throttled_requests ?? 0) + (st.total_server_busy_count ?? 0) + (st.total_quota_exceeded_count ?? 0)
}

// hasEventHubsProducerTelemetry reports whether a transaction carries any of the
// producer-side ingestion signals worth surfacing in the Event Hubs panel.
function hasEventHubsProducerTelemetry(transaction: TxMetrics): boolean {
  const st = transaction.server_telemetry
  if (!st) {
    return false
  }
  return eventHubsThrottleTotal(transaction) > 0
    || (st.total_event_too_large_count ?? 0) > 0
    || (st.avg_end_to_end_latency_ms ?? 0) > 0
    || (st.partition_skew_pct ?? 0) > 0
    || ((st.messages_sent ?? 0) > 0)
}

// topEventHubsPartitions returns the busiest partitions by produce volume for the
// per-partition breakdown, sorted descending.
function topEventHubsPartitions(transaction: TxMetrics, limit = 6): Array<{ partition: string; events: number }> {
  const events = transaction.server_telemetry?.partition_events
  if (!events) {
    return []
  }
  return Object.entries(events)
    .map(([partition, count]) => ({ partition, events: count }))
    .sort((left, right) => right.events - left.events)
    .slice(0, limit)
}

// isMessagingDependency reports whether a dependency belongs to a queue/stream
// protocol (JMS family, Kafka, Event Hubs).
function isMessagingDependency(transaction: TxMetrics): boolean {
  const dt = transaction.dependency_type
  return !!dt && (dt.startsWith('jms_') || dt.startsWith('kafka') || dt.startsWith('eventhubs'))
}

// hasReliabilitySignal reports whether a dependency reported any messaging
// reliability event worth surfacing.
function hasReliabilitySignal(transaction: TxMetrics): boolean {
  const st = transaction.server_telemetry
  if (!st) {
    return false
  }
  return (st.redelivery_count ?? 0) > 0
    || (st.dead_lettered_count ?? 0) > 0
    || (st.duplicate_count ?? 0) > 0
    || (st.out_of_order_count ?? 0) > 0
    || (st.send_failure_count ?? 0) > 0
    || (st.commit_count ?? 0) > 0
    || (st.abort_count ?? 0) > 0
}

// messagingReliabilityWeight ranks dependencies by total problem signal (failures
// first), so the busiest reliability hotspots sort to the top.
function messagingReliabilityWeight(transaction: TxMetrics): number {
  const st = transaction.server_telemetry
  if (!st) {
    return 0
  }
  return (st.dead_lettered_count ?? 0) + (st.redelivery_count ?? 0) + (st.duplicate_count ?? 0)
    + (st.out_of_order_count ?? 0) + (st.send_failure_count ?? 0) + (st.abort_count ?? 0)
}

function formatProtocolLabel(dependencyType?: string): string {
  if (dependencyType === 'grpc') {
    return 'gRPC'
  }
  if (dependencyType === 'websocket') {
    return 'WebSocket'
  }
  return dependencyType || 'Protocol'
}

function formatProtocolStatus(transaction: TxMetrics): string {
  const errorPct = transactionErrorPct(transaction)
  if (errorPct > 0) {
    return `${errorPct.toFixed(1)}% errors`
  }
  return transaction.dependency_type === 'grpc' ? 'OK' : 'Open'
}

function formatJMSTopicFanOut(topic: JMSTopicFanOutBucket): string {
  if (topic.fanOutRatio === undefined) {
    return topic.receivedCount > 0 ? 'consumer-only' : 'publisher-only'
  }
  return `${topic.fanOutRatio.toFixed(2)}x`
}

function deadLetterErrorRate(bucket: JMSDeadLetterBucket): number {
  if (bucket.messageCount <= 0) {
    return 0
  }
  return bucket.errorCount / bucket.messageCount
}

function formatJMSDeadLetterErrorRate(bucket: JMSDeadLetterBucket): string {
  return `${(deadLetterErrorRate(bucket) * 100).toFixed(1)}%`
}

function hasHTTPTimingDecomposition(transaction: TxMetrics): boolean {
  return transaction.dependency_type?.startsWith('http') === true
    && ((transaction.avg_dns_ms ?? 0) > 0
      || (transaction.avg_connect_ms ?? 0) > 0
      || (transaction.avg_tls_ms ?? 0) > 0
      || (transaction.avg_ttfb_ms ?? 0) > 0
      || (transaction.server_telemetry?.avg_server_processing_ms ?? 0) > 0
      || (transaction.server_telemetry?.avg_transfer_ms ?? 0) > 0)
}

function totalHTTPDecompositionMs(transaction: TxMetrics): number {
  return (transaction.avg_dns_ms ?? 0)
    + (transaction.avg_connect_ms ?? 0)
    + (transaction.avg_tls_ms ?? 0)
    + (transaction.server_telemetry?.avg_server_processing_ms ?? 0)
    + (transaction.server_telemetry?.avg_transfer_ms ?? 0)
}

function transactionErrorPct(transaction?: TxMetrics): number {
  if (!transaction || transaction.count <= 0) {
    return 0
  }
  return (transaction.errors / transaction.count) * 100
}

interface SLAEvaluation {
  breached: boolean
  reasons: string[]
}

function evaluateSLAScore(transaction: TxMetrics, goalConfig?: GoalConfig): SLAEvaluation | null {
  if (!goalConfig) {
    return null
  }

  const reasons: string[] = []
  if (goalConfig.p95_latency_ms !== undefined && transaction.p95_ms !== undefined && transaction.p95_ms > goalConfig.p95_latency_ms) {
    reasons.push('P95')
  }
  if (goalConfig.max_error_pct !== undefined && transactionErrorPct(transaction) > goalConfig.max_error_pct) {
    reasons.push('Errors')
  }
  if (
    goalConfig.max_avg_server_processing_ms !== undefined
    && transaction.server_telemetry?.avg_server_processing_ms !== undefined
    && transaction.server_telemetry.avg_server_processing_ms > goalConfig.max_avg_server_processing_ms
  ) {
    reasons.push('Server')
  }
  if (
    goalConfig.max_avg_transfer_ms !== undefined
    && transaction.server_telemetry?.avg_transfer_ms !== undefined
    && transaction.server_telemetry.avg_transfer_ms > goalConfig.max_avg_transfer_ms
  ) {
    reasons.push('Transfer')
  }

  const thresholdsConfigured =
    goalConfig.p95_latency_ms !== undefined
    || goalConfig.max_error_pct !== undefined
    || goalConfig.max_avg_server_processing_ms !== undefined
    || goalConfig.max_avg_transfer_ms !== undefined

  if (!thresholdsConfigured) {
    return null
  }

  return {
    breached: reasons.length > 0,
    reasons,
  }
}

function formatSLALabel(evaluation: SLAEvaluation | null): string {
  if (!evaluation) {
    return 'No SLA'
  }
  if (!evaluation.breached) {
    return 'Passing'
  }
  return `Breach: ${evaluation.reasons.join(', ')}`
}

function buildHTTPDecompositionSegments(transaction: TxMetrics): Array<{ label: string; value: number; widthPct: number; color: string }> {
  const segments = [
    { label: 'DNS', value: transaction.avg_dns_ms ?? 0, color: '#0f766e' },
    { label: 'TCP', value: transaction.avg_connect_ms ?? 0, color: '#1d4ed8' },
    { label: 'TLS', value: transaction.avg_tls_ms ?? 0, color: '#b45309' },
    { label: 'Server', value: transaction.server_telemetry?.avg_server_processing_ms ?? 0, color: '#b91c1c' },
    { label: 'Transfer', value: transaction.server_telemetry?.avg_transfer_ms ?? 0, color: '#7c3aed' },
  ].filter((segment) => segment.value > 0)

  const total = segments.reduce((sum, segment) => sum + segment.value, 0)
  if (total <= 0) {
    return []
  }

  return segments.map((segment) => ({
    ...segment,
    widthPct: (segment.value / total) * 100,
  }))
}

function renderServerTelemetry(current: TxMetrics, previous?: TxMetrics) {
  if (!current.server_telemetry) {
    return 'n/a'
  }
  const telemetry = current.server_telemetry
  if (telemetry.grpc) return grpcSummaryLines(telemetry.grpc).join(" | ")
  if (telemetry.websocket) return websocketSummaryLines(telemetry.websocket).join(' | ')
  const previousTelemetry = previous?.server_telemetry
  const parts = [
    `Server ${formatTelemetryValue(telemetry.avg_server_processing_ms, ' ms')}${formatTelemetryDelta(telemetry.avg_server_processing_ms, previousTelemetry?.avg_server_processing_ms, ' ms')}`,
    `Transfer ${formatTelemetryValue(telemetry.avg_transfer_ms, ' ms')}${formatTelemetryDelta(telemetry.avg_transfer_ms, previousTelemetry?.avg_transfer_ms, ' ms')}`,
    ...(telemetry.avg_first_event_ms !== undefined ? [`First event ${formatTelemetryValue(telemetry.avg_first_event_ms, ' ms')}${telemetry.max_first_event_ms !== undefined ? ` / max ${formatTelemetryValue(telemetry.max_first_event_ms, ' ms')}` : ''}${formatTelemetryDelta(telemetry.avg_first_event_ms, previousTelemetry?.avg_first_event_ms, ' ms')}`] : []),
    ...(telemetry.event_count !== undefined ? [`Events ${telemetry.event_count}`] : []),
    `Payload ${formatTelemetryValue(telemetry.avg_request_payload_kb, ' KB', 2)} / ${formatTelemetryValue(telemetry.avg_response_payload_kb, ' KB', 2)}`,
    `Reuse ${formatTelemetryValue(telemetry.connection_reused_pct, '%', 0)}${formatTelemetryDelta(telemetry.connection_reused_pct, previousTelemetry?.connection_reused_pct, '%', 0)}`,
  ]
  return parts.join(' | ')
}

function formatTelemetryValue(value?: number, suffix = ' ms', digits = 1): string {
  if (value === undefined || Number.isNaN(value)) {
    return 'n/a'
  }
  return `${value.toFixed(digits)}${suffix}`
}

function formatPercentage(value?: number, digits = 1): string {
  if (value === undefined || Number.isNaN(value)) {
    return 'n/a'
  }
  return `${value.toFixed(digits)}%`
}

function formatDependencyStatus(status?: string): string {
  switch (status) {
    case 'healthy':
      return 'Healthy'
    case 'warning':
      return 'Warning'
    case 'danger':
      return 'Danger'
    default:
      return 'Unknown'
  }
}

function formatBaselinePolicy(policy?: string): string {
  switch (policy) {
    case 'pinned':
      return 'pinned'
    case 'latest_successful':
      return 'latest successful'
    case 'latest_finished':
      return 'latest finished'
    default:
      return policy?.replace(/_/g, ' ') || 'automatic'
  }
}

function formatTimestamp(value?: string): string {
  if (!value) {
    return 'n/a'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return 'n/a'
  }
  return date.toLocaleTimeString()
}

function formatTelemetryDelta(current?: number, previous?: number, suffix = ' ms', digits = 1): string {
  if (current === undefined || previous === undefined || Number.isNaN(current) || Number.isNaN(previous)) {
    return ''
  }
  const delta = current - previous
  if (Math.abs(delta) < Math.pow(10, -digits)) {
    return ' (steady)'
  }
  return ` (${delta > 0 ? '+' : ''}${delta.toFixed(digits)}${suffix} vs prev)`
}
