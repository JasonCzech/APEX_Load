import { useEffect, useMemo, useState } from 'react'

import { request } from '../hooks/apiClient'
import type { LiveMetrics } from '../types'
import { DOCUMENT_CAPTURE_SETTING_HINT } from './browserCaptureSettings'
import { formatBrowserDocumentValue } from './browserDocumentEvidenceModel'
import type { CommandGroup } from './commandCenterScenarioRows'
import { BROWSER_COMPARISON_METRICS, BROWSER_METRIC_LABELS, buildBrowserComparison } from './runReport/browserComparisonModel'
import { BROWSER_FINDINGS_POLL_MS, browserFindingsApi, browserScoreUnscoredText, type RunBrowserFindings } from './runReport/browserFindingsModel'
import type { BrowserInteractionAnalysis } from './runReport/BrowserInteractionsPanel'
import { formatJourneyFraction, journeyUnscoredText, type JourneyRiskReport } from './runReport/journeyRiskModel'
import { JOURNEY_RATE_WINDOW } from './testExecutionMetricsModel'

function formatRate(value: number | undefined): string {
  return value === undefined || !Number.isFinite(value) ? 'Not reported' : value.toLocaleString(undefined, { maximumFractionDigits: 1, minimumFractionDigits: 1 })
}

function formatClientMs(value: number | undefined): string {
  return value === undefined || !Number.isFinite(value) ? '—' : `${Math.round(value).toLocaleString()} ms`
}

/**
 * Browser groups as their users see them: journeys (iterations) per minute,
 * the requests their pages make, the pages' Web Vitals as they are published,
 * and provisional live browser intelligence (findings, interactions and
 * journeys) while the run is active or finalizing.
 */
export default function CommandCenterBrowserGroups({ testId, groups, metrics, rateWindow, requestsCounted, inRunRequestRate }: {
  /** When set, polls provisional live browser intelligence for this run. */
  testId?: string
  groups: CommandGroup[]
  metrics?: LiveMetrics
  /** The window the page's rates cover, e.g. "10 s avg". */
  rateWindow: string
  /** The run counts requests; otherwise a group's request rate is not reported, not zero. */
  requestsCounted: boolean
  /** The run's Requests/s, which other groups share, includes these groups' page requests. */
  inRunRequestRate: boolean
}) {
  const vitals = useMemo(() => buildBrowserComparison(metrics?.browser_documents, 'live-history'), [metrics?.browser_documents])
  const client = metrics?.browser
  const [liveIntelligence, setLiveIntelligence] = useState<{
    findings: RunBrowserFindings | null
    interactions: BrowserInteractionAnalysis | null
    journeys: JourneyRiskReport | null
  }>({ findings: null, interactions: null, journeys: null })

  useEffect(() => {
    setLiveIntelligence({ findings: null, interactions: null, journeys: null })
    if (!testId) {
      return undefined
    }
    let cancelled = false
    let timer: number | undefined
    const controller = new AbortController()
    const load = async () => {
      const [findings, interactions, journeys] = await Promise.all([
        browserFindingsApi.get(testId, controller.signal).catch(() => null),
        request<BrowserInteractionAnalysis>(`/tests/${encodeURIComponent(testId)}/archive/browser-interactions`, { signal: controller.signal }).catch(() => null),
        request<JourneyRiskReport>(`/tests/${encodeURIComponent(testId)}/archive/journeys`, { signal: controller.signal }).catch(() => null),
      ])
      if (cancelled || controller.signal.aborted) return
      setLiveIntelligence({ findings, interactions, journeys })
      const stillProvisional = Boolean(
        findings?.provisional || findings?.result?.provisional ||
        interactions?.provisional || journeys?.provisional,
      )
      if (stillProvisional) {
        timer = window.setTimeout(() => { void load() }, BROWSER_FINDINGS_POLL_MS)
      }
    }
    void load()
    return () => {
      cancelled = true
      controller.abort()
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [testId])

  const hasLiveIntelligence = Boolean(
    liveIntelligence.findings?.result ||
    (liveIntelligence.interactions && liveIntelligence.interactions.status === 'ready') ||
    (liveIntelligence.journeys && liveIntelligence.journeys.availability === 'available'),
  )
  const isProvisionalIntelligence = Boolean(
    liveIntelligence.findings?.provisional || liveIntelligence.findings?.result?.provisional ||
    liveIntelligence.interactions?.provisional || liveIntelligence.journeys?.provisional,
  )
  const isLiveTruncated = Boolean(
    liveIntelligence.findings?.live_truncated || liveIntelligence.findings?.result?.live_truncated ||
    liveIntelligence.interactions?.live_truncated || liveIntelligence.journeys?.live_truncated,
  )
  return (
    <section className="command-panel command-browser-groups" aria-label="Browser groups">
      <div className="command-panel-header"><h3>Browser groups</h3></div>
      <div className="command-data-table-wrap">
        <table className="command-data-table" aria-label="Browser group journeys">
          <thead><tr>
            <th scope="col">Group</th>
            <th scope="col" className="numeric">Journeys/min <small>({JOURNEY_RATE_WINDOW})</small></th>
            <th scope="col" className="numeric">Page requests/s <small>({rateWindow})</small></th>
            <th scope="col" className="numeric">Active VUsers</th>
          </tr></thead>
          <tbody>{groups.map(group => <tr key={group.key}>
            <td><strong>{group.name}</strong></td>
            <td className="numeric">{formatRate(group.journeysPerMin)}</td>
            <td className="numeric">{formatRate(requestsCounted ? group.requestRate : undefined)}</td>
            <td className="numeric">{group.current.toLocaleString()}</td>
          </tr>)}</tbody>
        </table>
      </div>
      <p>
        A journey is one iteration of the group&apos;s script, passed or failed. Page requests are every document, script, image and style its pages load{inRunRequestRate ? '; the run’s Requests/s includes them and shows their share' : ''}.
        {groups.some(group => group.journeysPerMin === undefined) ? ' Journeys are reported once a group has run for 15 s, and not by generators that predate them.' : ''}
      </p>
      <h4>Web Vitals</h4>
      {vitals.state === 'ready' && vitals.rows.length > 0 ? (
        <div className="command-data-table-wrap">
          <table className="command-data-table" aria-label="Live Web Vitals">
            <caption className="command-table-caption">p75 over the main-frame page loads published so far, by group, engine and device profile.{vitals.incomplete ? ' Some page evidence was dropped, so these values are incomplete.' : ''}</caption>
            <thead><tr>
              <th scope="col">Group</th><th scope="col">Engine</th><th scope="col">Device profile</th>
              {BROWSER_COMPARISON_METRICS.map(metric => <th scope="col" className="numeric" key={metric}>{BROWSER_METRIC_LABELS[metric]}</th>)}
            </tr></thead>
            <tbody>{vitals.rows.map(row => <tr key={row.key}>
              <td>{row.group || '—'}{row.region ? ` · ${row.region}` : ''}</td>
              <td>{row.engine}</td>
              <td>{row.deviceProfile || 'Desktop (no profile)'}{row.cohortLabel ? ` · ${row.cohortLabel}` : ''}</td>
              {BROWSER_COMPARISON_METRICS.map(metric => {
                const cell = row.metrics[metric]
                return <td className="numeric" key={metric}>{!cell ? '—' : cell.p75 === null ? 'No final pages' : formatBrowserDocumentValue(cell.p75, cell.unit)}</td>
              })}
            </tr>)}</tbody>
          </table>
        </div>
      ) : client && client.samples > 0 ? (
        <p>
          Client metrics, all browser groups, last sample: FCP {formatClientMs(client.avg_fcp_ms)} · LCP {formatClientMs(client.avg_lcp_ms)} · INP {formatClientMs(client.avg_inp_ms)} · CLS {client.avg_cls_score === undefined ? '—' : client.avg_cls_score.toFixed(3)} · TTFB {formatClientMs(client.avg_ttfb_ms)}. These are averages across the groups.{' '}
          {vitals.state === 'invalid' || vitals.state === 'unsupported'
            ? vitals.issue
            : vitals.state === 'ready'
              // The summary's presence means page timing capture is on.
              ? 'Page timings are captured, but no page load has been measured yet.'
              : <>For p75 by group, turn on page timings and Web Vitals in the browser script&apos;s or scenario group&apos;s Runtime Settings › Browser.</>}
        </p>
      ) : (
        <p>{vitals.state === 'invalid' || vitals.state === 'unsupported'
          ? vitals.issue
          : vitals.state === 'ready'
            // The summary's presence means capture is on.
            ? `Page timings are captured, but no page load has been measured yet.${vitals.incomplete ? ' Some page evidence was dropped.' : ''}`
            : `No page timings yet. ${DOCUMENT_CAPTURE_SETTING_HINT}`}</p>
      )}
      {hasLiveIntelligence && (
        <div className="command-browser-live-intelligence" data-provisional={isProvisionalIntelligence ? 'true' : 'false'}>
          <h4>{isProvisionalIntelligence ? 'Live browser intelligence (provisional — non-verdict)' : 'Browser intelligence (finalized archive)'}</h4>
          {isProvisionalIntelligence && (
            <p role="note">
              Provisional live views refresh during the run and are replaced by the finalized archive verdict.{isLiveTruncated ? ' Live browser evidence reached its in-memory bound (live_truncated).' : ''}
            </p>
          )}
          <ul>
            {liveIntelligence.findings?.result && (
              <li data-testid="live-browser-findings-summary">
                <strong>Findings ({liveIntelligence.findings.result.policy.name || liveIntelligence.findings.result.policy.id} rev {liveIntelligence.findings.result.policy.revision}):</strong>{' '}
                {liveIntelligence.findings.result.score.score !== undefined
                  ? `Score ${liveIntelligence.findings.result.score.score.toFixed(1)}/100 (${liveIntelligence.findings.result.score.severity.replace(/_/g, ' ')})`
                  : browserScoreUnscoredText(liveIntelligence.findings.result.score.unscored_reason)}{' '}
                · {liveIntelligence.findings.result.counts.active || 0} active findings
                {isProvisionalIntelligence ? ' · baseline not evaluated live' : ''}
              </li>
            )}
            {liveIntelligence.interactions && liveIntelligence.interactions.status === 'ready' && (
              <li data-testid="live-browser-interactions-summary">
                <strong>Interactions ({liveIntelligence.interactions.version}):</strong>{' '}
                {liveIntelligence.interactions.coverage.interactions} gestures · {(liveIntelligence.interactions.patterns || []).length} patterns · {(liveIntelligence.interactions.heatmaps || []).length} heatmaps
              </li>
            )}
            {liveIntelligence.journeys && liveIntelligence.journeys.availability === 'available' && (
              <li data-testid="live-browser-journeys-summary">
                <strong>Journeys ({liveIntelligence.journeys.policy.name || liveIntelligence.journeys.policy.id} rev {liveIntelligence.journeys.policy.revision}):</strong>{' '}
                weighted failure index {liveIntelligence.journeys.weighted_failure_index !== undefined ? formatJourneyFraction(liveIntelligence.journeys.weighted_failure_index) : journeyUnscoredText(liveIntelligence.journeys.index_unscored)}{' '}
                · {(liveIntelligence.journeys.journeys || []).map(j => `${j.name}: ${j.completed} completed, ${j.failed} failed, ${j.censored} censored`).join('; ')}
              </li>
            )}
          </ul>
        </div>
      )}
    </section>
  )
}
