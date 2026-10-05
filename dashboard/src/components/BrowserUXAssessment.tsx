import type { BrowserDeviceLiveMetrics, BrowserLiveMetrics } from '../types'
import { buildBrowserUXInsight } from './browserBreakdownModel'

export default function BrowserUXAssessment({ summary, devices }: {
  summary?: BrowserLiveMetrics
  devices?: Record<string, BrowserDeviceLiveMetrics>
}) {
  const insight = buildBrowserUXInsight(summary, devices)
  if (!insight) return null
  return (
    <section className={`browser-ux-card ${insight.severity}`} aria-label="Browser legacy summary assessment" data-model-version={insight.version}>
      <div className="browser-ux-header">
        <div>
          <span className="results-compare-label">UX Intelligence</span>
          <h4 className="browser-ux-title">{insight.label}{insight.score !== null ? ` · ${insight.score}/100 legacy estimate` : ''}</h4>
        </div>
        {insight.worstDevice && <span className="archive-chip">Weakest complete summary: {insight.worstDevice}</span>}
      </div>
      <p className="archive-section-copy">{insight.summary}</p>
      <p className="browser-ux-qualification">{insight.coverage.observed}/{insight.coverage.required} required fields available · {insight.version}. Field availability is not document or time coverage.</p>
      <p className="browser-ux-qualification">{insight.applicability}</p>
      {insight.unrankedDevices > 0 && (
        <details className="browser-ux-qualification">
          <summary>{insight.unrankedDevices} device summar{insight.unrankedDevices === 1 ? 'y' : 'ies'} unranked because required evidence is unavailable</summary>
          <ul>{insight.unrankedDeviceReasons.map(({ profile, reason }) => <li key={profile}>{profile}: {reason}</li>)}</ul>
        </details>
      )}
      <div className="browser-ux-signal-grid">
        {insight.signals.map(signal => (
          <div key={signal.label} className={`browser-ux-signal ${signal.severity}`} data-availability={signal.status}>
            <div className="browser-ux-signal-row">
              <span className="browser-ux-signal-label">{signal.label}</span>
              <span className="browser-ux-signal-value">{signal.value}</span>
            </div>
            <span className="browser-ux-signal-detail">{signal.detail}</span>
            <span className="browser-ux-signal-detail">{signal.score === null ? `Unscored: ${signal.reason}` : `Component ${signal.score}/100 · contribution ${signal.contribution?.toFixed(2)}/100`}</span>
          </div>
        ))}
      </div>
    </section>
  )
}
