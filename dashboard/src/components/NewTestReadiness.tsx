import { useEffect, useState } from 'react'
import type { StoredScenario } from '../types'
import { useApi } from '../hooks/useApi'
import { pageTimingCoverage, type BrowserSettingsSource } from './browserCaptureSettings'
import { targetOrigin } from './newTestPresentation'

// assertionCount is undefined when the script's assertions were not verified.
// documentCapture is the script's own page-timing setting, which a group's or
// the scenario's browser runtime section replaces.
type ScriptReview = { id: string; name: string; target: string; assertions: string; assertionCount?: number; protocol?: string; documentCapture?: boolean }
type ReviewedScenario = Pick<StoredScenario, 'id' | 'updated_at' | 'scripts' | 'data_pools' | 'runtime_settings'>
type ReviewedGroup = ReviewedScenario['scripts'][number]

const groupLabel = (group: ReviewedGroup) => group.name || group.script_name || group.script_id || `Group ${group.order + 1}`

// Where to turn page timings on for groups whose browser settings come from each level.
const PAGE_TIMING_ADVICE: Record<BrowserSettingsSource, string> = {
  script: "in the script's Runtime Settings › Browser",
  scenario: "in the scenario's runtime defaults, whose browser settings replace the scripts'",
  group: "in the group's runtime settings, whose browser settings replace the script's",
}

function pageTimingAdvice(without: { group: ReviewedGroup; source: BrowserSettingsSource }[]): string {
  const sources = (['script', 'scenario', 'group'] as const).filter(source => without.some(item => item.source === source))
  if (sources.length === 1) return `Turn them on ${PAGE_TIMING_ADVICE[sources[0]]}.`
  return `Turn them on ${sources.map(source => `${PAGE_TIMING_ADVICE[source]} (${without.filter(item => item.source === source).map(item => groupLabel(item.group)).join(', ')})`).join('; ')}.`
}

/**
 * The readiness review of a scenario's scripts, response-time acceptance,
 * data and monitoring. The scenario builder shows it in full; New Test shows
 * it once more as a one-line summary that opens to the same review.
 */
export default function NewTestReadiness({ scenario, projectId, compact = false, summary = false }: {
  scenario: ReviewedScenario; projectId: string; compact?: boolean; summary?: boolean
}) {
  const api = useApi()
  const [review, setReview] = useState<{ scope: string; scripts: ScriptReview[] } | null>(null)
  const [refresh, setRefresh] = useState(0)
  const scope = JSON.stringify([projectId, scenario.id, scenario.updated_at, scenario.scripts.map(group => group.script_id), refresh])
  useEffect(() => {
    let cancelled = false
    const groups = [...new Map(scenario.scripts.filter(group => group.script_id).map(group => [group.script_id, group])).values()]
    // Bounded sequential requests avoid a request burst for large scenarios.
    void (async () => {
      const scripts: ScriptReview[] = []
      for (const group of groups) {
        if (cancelled) return
        try {
          const stored = await api.getScript(group.script_id)
          if (!stored.script) throw new Error('Script detail unavailable')
          const definition = stored.script
          const targets = new Set<string>()
          const base = targetOrigin(definition.config?.base_url)
          if (base) targets.add(base)
          // Nested action structures vary by protocol; walk the action document
          // iteratively and only expose safe target origins and assertion counts.
          let assertions = 0
          const stack: unknown[] = [definition.init, definition.actions, definition.teardown]
          let visited = 0
          while (stack.length && visited++ < 10000) {
            const value = stack.pop()
            if (Array.isArray(value)) { stack.push(...value); continue }
            if (!value || typeof value !== 'object') continue
            for (const [name, child] of Object.entries(value)) {
              if ((name === 'url' || name === 'target') && typeof child === 'string') {
                const origin = targetOrigin(child)
                if (origin) targets.add(origin)
              } else if (name === 'assertions' && Array.isArray(child)) assertions += child.length
              else if (child && typeof child === 'object') stack.push(child)
            }
          }
          scripts.push({ id: group.script_id, name: group.script_name || group.name || group.script_id,
            target: targets.size ? [...targets].join(', ') : 'Target not exposed by this script — review configuration',
            assertions: stack.length ? 'Assertion inventory incomplete — review script' : assertions ? `${assertions} script assertions configured` : 'No script assertions configured',
            assertionCount: stack.length ? undefined : assertions,
            protocol: definition.protocol, documentCapture: definition.config?.browser?.capture_document_metrics === true,
          })
        } catch {
          scripts.push({ id: group.script_id, name: group.script_name || group.name || group.script_id,
            target: 'Target unavailable — review script access/configuration', assertions: 'Script assertions not verified' })
        }
      }
      if (!cancelled) setReview({ scope, scripts })
    })()
    return () => { cancelled = true }
  // scope includes the script identifiers; equivalent parent renders must not refetch.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [api, scope])
  const scripts = review?.scope === scope ? review.scripts : null
  const pageTimings = scripts ? pageTimingCoverage(scenario, scripts) : { browserGroups: [], without: [] }
  const withoutPageTimings = pageTimings.without
  const refreshButton = <button type="button" className="btn btn-secondary btn-sm" onClick={() => setRefresh(value => value + 1)}>Refresh review</button>
  const body = (
    <div className="new-test-readiness-body">
      {!scripts ? <p role="status">Checking script targets and assertions…</p> : scripts.length === 0 ? <p role="status">Assign scripts before reviewing targets and assertions.</p> : <ul>{scripts.map(script => <li key={script.id}><strong>{script.name}</strong><p>{script.target}</p><p>{script.assertions}</p></li>)}</ul>}
      <h4>Response-time acceptance</h4>
      <ul>{scenario.scripts.map(group => <li key={`${group.order}:${group.script_id}`}>{groupLabel(group)}: {group.transaction_response_time_sla_ms && group.transaction_response_time_sla_ms > 0 ? `transaction response-time SLA ${group.transaction_response_time_sla_ms} ms` : 'No group response-time SLA configured'}</li>)}</ul>
      <h4>Data requirements</h4>
      <p>{scenario.data_pools?.length ? scenario.data_pools.map(pool => pool.alias || pool.pool_name || pool.pool_id).join(', ') + ' — verify bindings and sufficient rows in the scenario builder.' : 'No scenario data pools configured. Script-local data requirements must be reviewed in Scripts.'}</p>
      {pageTimings.browserGroups.length > 0 && <>
        <h4>Browser evidence</h4>
        <p>{withoutPageTimings.length === 0
          ? 'Page timings and Web Vitals are captured for every browser group.'
          : `${withoutPageTimings.map(item => groupLabel(item.group)).join(', ')} ${withoutPageTimings.length === 1 ? 'captures' : 'capture'} no page timings or Web Vitals, so Command Center and the report's Browser tab will show no LCP, FCP, INP or CLS for ${withoutPageTimings.length === 1 ? 'it' : 'them'}. ${pageTimingAdvice(withoutPageTimings)}`}</p>
      </>}
      <h4>Monitoring</h4><p>Target monitoring coverage is not verified by launch preflight. Check connections, collectors and metric coverage in Monitoring.</p>
      {!compact && <div className="inline-create-row"><a className="btn btn-secondary" href="#/tests?view=library">Review scenarios</a><a className="btn btn-secondary" href="#/scripts">Review scripts</a><a className="btn btn-secondary" href="#/monitoring">Review monitoring</a></div>}
    </div>
  )
  if (summary) {
    const groups = scenario.scripts.length
    const slaGroups = scenario.scripts.filter(group => (group.transaction_response_time_sla_ms ?? 0) > 0).length
    const pools = scenario.data_pools?.length ?? 0
    const withoutAssertions = scripts?.filter(script => script.assertionCount === 0).length ?? 0
    const unverified = scripts?.filter(script => script.assertionCount === undefined).length ?? 0
    const line = !scripts ? 'Checking script targets and assertions…' : [
      `${scripts.length} script${scripts.length === 1 ? '' : 's'}`,
      withoutAssertions ? `${withoutAssertions} without assertions` : '',
      unverified ? `${unverified} not verified` : '',
      slaGroups ? `${slaGroups} of ${groups} group${groups === 1 ? '' : 's'} with a response-time SLA` : 'no response-time SLA',
      pools ? `${pools} data pool${pools === 1 ? '' : 's'}` : 'no data pools',
      withoutPageTimings.length ? `${withoutPageTimings.length} browser group${withoutPageTimings.length === 1 ? '' : 's'} without page timings` : '',
    ].filter(Boolean).join(' · ')
    return <details className="new-test-step new-test-readiness new-test-readiness-summary" aria-label="Run readiness review">
      <summary><span>Run readiness</span><small>{line}</small></summary>
      <div className="new-test-readiness-summary-actions"><p>Review targets and acceptance before execution. Unknown information is not a readiness pass.</p>{refreshButton}</div>
      {body}
    </details>
  }
  return <section className="new-test-step new-test-readiness" aria-label="Run readiness review">
    <div className="new-test-step-header"><div><h3>Run readiness review</h3><p>Review targets and acceptance before execution. Unknown information is not a readiness pass.</p></div>
      {refreshButton}
    </div>
    {body}
  </section>
}
