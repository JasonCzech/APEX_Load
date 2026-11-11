import { useId, type ReactNode } from 'react'
import type { DashboardPhaseLatencyThreshold, PhaseLatencyPhase } from '../types/settings'
import { MAX_PHASE_LATENCY_RULES } from './settingsPageModel'
import './PhaseLatencyThresholdEditor.css'

const PHASE_OPTIONS: ReadonlyArray<{ value: PhaseLatencyPhase; label: string }> = [
  { value: 'dns', label: 'DNS' },
  { value: 'connect', label: 'Connect' },
  { value: 'tls', label: 'TLS' },
  { value: 'server', label: 'Server (TTFB − setup)' },
  { value: 'download', label: 'Download' },
]
const PERCENTILE_OPTIONS: ReadonlyArray<DashboardPhaseLatencyThreshold['percentile']> = [50, 90, 95, 99]

type FieldAccessibility = (field: string) => { 'aria-invalid'?: true; 'aria-describedby'?: string }

interface Props {
  rules: DashboardPhaseLatencyThreshold[] | undefined
  onChange: (rules: DashboardPhaseLatencyThreshold[]) => void
  fieldAccessibility?: FieldAccessibility
  renderFieldIssues?: (field: string) => ReactNode
}

const optionalNumber = (value: string): number | undefined => {
  if (value.trim() === '') return undefined
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : Number.NaN
}

/**
 * Phase-aware latency thresholds (L5): "connect P95 above 500 ms", alert-only
 * unless a stop sample count is set. Lives in Settings › Thresholds beside
 * the P99 threshold it extends.
 */
export default function PhaseLatencyThresholdEditor({ rules = [], onChange, fieldAccessibility = () => ({}), renderFieldIssues = () => null }: Props) {
  const idPrefix = useId()
  const update = (index: number, patch: Partial<DashboardPhaseLatencyThreshold>) => {
    onChange(rules.map((rule, i) => (i === index ? { ...rule, ...patch } : rule)))
  }
  const used = new Set(rules.map(rule => `${rule.phase}:${rule.percentile}`))
  const nextRule = (): DashboardPhaseLatencyThreshold => {
    for (const percentile of [95, 99, 90, 50] as const) {
      for (const { value } of PHASE_OPTIONS) {
        if (!used.has(`${value}:${percentile}`)) return { phase: value, percentile, max_ms: 500 }
      }
    }
    return { phase: 'connect', percentile: 95, max_ms: 500 }
  }
  return (
    <div className="phase-latency-rules">
      <div className="setting-row">
        <div className="setting-info">
          <span className="setting-label">Phase latency rules</span>
          <span className="setting-desc">
            Optional: alert when one network phase&apos;s percentile breaches a limit, for example connect P95 above 500 ms. The percentile is over the transactions that spent time in that phase. Set stop samples to auto-stop after that many consecutive breaching samples (about one per second on distributed runs, one per metrics interval locally). A sample whose load generators do not report per-phase percentiles is no data: it never breaches and restarts the count.
          </span>
        </div>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          onClick={() => onChange([...rules, nextRule()])}
          disabled={rules.length >= MAX_PHASE_LATENCY_RULES}
        >
          Add phase rule
        </button>
      </div>
      {renderFieldIssues('thresholds.phase_latency')}
      {rules.length === 0 ? (
        <p className="setting-desc">No phase latency rules yet.</p>
      ) : (
        <ul className="phase-latency-rule-list" aria-label="Phase latency rules">
          {rules.map((rule, index) => {
            const field = `thresholds.phase_latency.${index}`
            const name = `Phase rule ${index + 1}`
            const phaseId = `${idPrefix}-${index}-phase`
            const percentileId = `${idPrefix}-${index}-percentile`
            const maxMsId = `${idPrefix}-${index}-max-ms`
            const stopSamplesId = `${idPrefix}-${index}-stop-samples`
            return (
              <li key={index} className="setting-row phase-latency-rule">
                <label className="settings-inline-field" htmlFor={phaseId}>
                  <span>Phase</span>
                  <select id={phaseId} {...fieldAccessibility(`${field}.phase`)} aria-label={`${name} phase`} className="settings-input settings-select" value={rule.phase} onChange={e => update(index, { phase: e.target.value as PhaseLatencyPhase })}>
                    {PHASE_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
                  </select>
                </label>
                <label className="settings-inline-field" htmlFor={percentileId}>
                  <span>Percentile</span>
                  <select id={percentileId} {...fieldAccessibility(`${field}.percentile`)} aria-label={`${name} percentile`} className="settings-input settings-select" value={rule.percentile} onChange={e => update(index, { percentile: Number(e.target.value) as DashboardPhaseLatencyThreshold['percentile'] })}>
                    {PERCENTILE_OPTIONS.map(p => <option key={p} value={p}>P{p}</option>)}
                  </select>
                </label>
                <label className="settings-inline-field" htmlFor={maxMsId}>
                  <span>Max (ms)</span>
                  <input
                    id={maxMsId}
                    {...fieldAccessibility(`${field}.max_ms`)}
                    aria-label={`${name} max (ms)`}
                    className="settings-input settings-input-number"
                    type="number"
                    min={1}
                    step={1}
                    value={Number.isFinite(rule.max_ms) ? rule.max_ms : ''}
                    onChange={e => update(index, { max_ms: optionalNumber(e.target.value) ?? Number.NaN })}
                  />
                </label>
                <label className="settings-inline-field" htmlFor={stopSamplesId}>
                  <span>Stop after samples</span>
                  <input
                    id={stopSamplesId}
                    {...fieldAccessibility(`${field}.stop_consecutive_samples`)}
                    aria-label={`${name} stop after breached samples`}
                    className="settings-input settings-input-number"
                    type="number"
                    min={0}
                    step={1}
                    placeholder="Alert only"
                    value={rule.stop_consecutive_samples ?? ''}
                    onChange={e => update(index, { stop_consecutive_samples: optionalNumber(e.target.value) })}
                  />
                </label>
                <button type="button" className="btn btn-ghost btn-sm" aria-label={`Remove ${name.toLowerCase()}`} onClick={() => onChange(rules.filter((_, i) => i !== index))}>
                  Remove
                </button>
                {renderFieldIssues(`${field}.phase`)}
                {renderFieldIssues(`${field}.percentile`)}
                {renderFieldIssues(`${field}.max_ms`)}
                {renderFieldIssues(`${field}.stop_consecutive_samples`)}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
