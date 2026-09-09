import DialogLayer from './ux/DialogLayer'
import { useId, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

import { useTestApi } from '../hooks/api/useTestApi'
import { ApiError } from '../hooks/apiClient'
import { useModalDialog } from '../hooks/useModalDialog'
import type { RuntimeAdjustmentPreview, RuntimeAdjustmentRequest, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { isValidDurationInput, parseDurationToSeconds } from '../utils/timeUtils'
import { maxVUsersForRole } from './observerGroupModel'
import { proportionalVUserTargets } from './runtimeAdjustmentModel'
import './RuntimeAdjustmentDialog.css'

interface Props {
  test: TestSummary
  onClose: () => void
  onApplied: (preview: RuntimeAdjustmentPreview) => void
}

interface GroupDraft {
  name: string
  selected: boolean
  role?: 'load' | 'observer'
  stageDriven: boolean
  duration: string
  vusers: number
  targetTps?: number
}

function formatDurationSeconds(value: number) {
  const seconds = Math.max(1, Math.round(value))
  if (seconds % 3600 === 0) return `${seconds / 3600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

function initialDrafts(test: TestSummary): GroupDraft[] {
  return test.groups.map(group => ({
    name: group.name,
    selected: true,
    role: group.role,
    stageDriven: Boolean(group.stages?.length),
    duration: group.target_duration || test.duration,
    vusers: test.vuser_ramps?.[group.name]?.target_vusers ?? group.vusers,
    targetTps: test.goal_targets?.[group.name]?.target_tps,
  }))
}

export default function RuntimeAdjustmentDialog({ test, onClose, onApplied }: Props) {
  const api = useTestApi()
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  const goalMode = test.scenario_mode === 'goal_oriented'
  const [groups, setGroups] = useState(() => initialDrafts(test))
  const [durationDirection, setDurationDirection] = useState<'extend' | 'shorten'>('extend')
  const [durationDelta, setDurationDelta] = useState('5m')
  const [loadPercent, setLoadPercent] = useState('10')
  const [transition, setTransition] = useState<'immediate' | 'gradual'>('gradual')
  const [rampDuration, setRampDuration] = useState('1m')
  const [preview, setPreview] = useState<RuntimeAdjustmentPreview | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useModalDialog({ dialogRef, initialFocusRef: closeRef, onClose: loading ? undefined : onClose })

  const request = useMemo<RuntimeAdjustmentRequest>(() => ({
    groups: groups.filter(group => group.selected).map(group => ({
      name: group.name,
      target_duration: group.duration,
      ...(goalMode
        ? { target_tps: group.targetTps }
        : { target_vusers: group.vusers }),
    })),
    transition,
    ...(transition === 'gradual' ? { ramp_duration: rampDuration } : {}),
  }), [goalMode, groups, rampDuration, transition])

  const requestValid = request.groups.length > 0
    && request.groups.every(group => isValidDurationInput(group.target_duration || ''))
    && request.groups.every(group => goalMode
      ? typeof group.target_tps === 'number' && Number.isFinite(group.target_tps) && group.target_tps > 0
      : typeof group.target_vusers === 'number'
        && Number.isInteger(group.target_vusers)
        && group.target_vusers >= 0
        && group.target_vusers <= maxVUsersForRole(groups.find(candidate => candidate.name === group.name)?.role))
    && (transition === 'immediate' || isValidDurationInput(rampDuration))

  const applyDurationDelta = () => {
    if (!isValidDurationInput(durationDelta)) return
    const delta = parseDurationToSeconds(durationDelta) * (durationDirection === 'extend' ? 1 : -1)
    setGroups(current => current.map(group => group.selected
      ? { ...group, duration: formatDurationSeconds(parseDurationToSeconds(group.duration) + delta) }
      : group))
    setPreview(null)
  }

  const applyLoadPercent = () => {
    const percent = Number(loadPercent)
    if (!Number.isFinite(percent)) return
    const factor = Math.max(0, 1 + percent / 100)
    const vuserTargets = goalMode ? null : proportionalVUserTargets(groups, factor)
    setGroups(current => current.map(group => {
      if (!group.selected || group.role === 'observer') return group
      if (goalMode) return { ...group, targetTps: Math.max(0.1, Math.round((group.targetTps || 0.1) * factor * 10) / 10) }
      return { ...group, vusers: vuserTargets?.get(group.name) ?? group.vusers }
    }))
    setPreview(null)
  }

  const runPreview = async (nextRequest: RuntimeAdjustmentRequest = request) => {
    setLoading(true)
    setError('')
    try {
      const result = await api.previewRuntimeAdjustment(test.id, nextRequest)
      setPreview(result)
    } catch (cause: unknown) {
      setError(getErrorMessage(cause, 'Unable to preview runtime adjustment'))
      setPreview(null)
    } finally {
      setLoading(false)
    }
  }

  const useSafeMaximum = () => {
    if (!preview?.safe_proposal) return
    const safe = preview.safe_proposal
    setGroups(current => current.map(group => {
      const replacement = safe.groups.find(candidate => candidate.name === group.name)
      return replacement ? {
        ...group,
        selected: true,
        duration: replacement.target_duration || group.duration,
        vusers: replacement.target_vusers ?? group.vusers,
        targetTps: replacement.target_tps ?? group.targetTps,
      } : group
    }))
    setTransition(safe.transition || transition)
    if (safe.ramp_duration) setRampDuration(safe.ramp_duration)
    setPreview(null)
    void runPreview({ ...safe, expected_revision: undefined })
  }

  const apply = async () => {
    if (!preview || preview.decision !== 'allowed') return
    setLoading(true)
    setError('')
    try {
      const result = await api.applyRuntimeAdjustment(test.id, { ...request, expected_revision: preview.revision })
      onApplied(result)
      onClose()
    } catch (cause: unknown) {
      setError(getErrorMessage(cause, 'Unable to apply runtime adjustment'))
      const refreshed = cause instanceof ApiError
        && typeof cause.payload === 'object'
        && cause.payload !== null
        && 'preview' in cause.payload
        ? (cause.payload as { preview?: RuntimeAdjustmentPreview }).preview
        : undefined
      setPreview(refreshed || null)
    } finally {
      setLoading(false)
    }
  }

  return createPortal(
    <DialogLayer><div className="runtime-adjustment-backdrop" role="presentation">
      <div ref={dialogRef} className="runtime-adjustment-dialog" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <header>
          <div><h2 id={titleId}>Change duration</h2><p>Adjust this running scenario without changing its saved definition.</p></div>
          <button ref={closeRef} type="button" className="btn btn-sm btn-ghost" onClick={onClose} disabled={loading} aria-label="Close runtime adjustment">×</button>
        </header>

        <section className="runtime-adjustment-tools" aria-label="Bulk runtime changes">
          <div className="runtime-adjustment-tool">
            <strong>Duration</strong>
            <select aria-label="Duration change direction" value={durationDirection} onChange={event => setDurationDirection(event.target.value as 'extend' | 'shorten')}>
              <option value="extend">Extend by</option><option value="shorten">Shorten by</option>
            </select>
            <input aria-label="Duration change amount" value={durationDelta} onChange={event => setDurationDelta(event.target.value)} />
            <button type="button" className="btn btn-sm btn-secondary" onClick={applyDurationDelta} disabled={!isValidDurationInput(durationDelta)}>Apply to selected</button>
          </div>
          <div className="runtime-adjustment-tool">
            <strong>{goalMode ? 'TPS goals' : 'Scenario load'}</strong>
            <input type="number" aria-label="Scenario load percentage" value={loadPercent} onChange={event => setLoadPercent(event.target.value)} />
            <span>%</span>
            <button type="button" className="btn btn-sm btn-secondary" onClick={applyLoadPercent}>Apply to load groups</button>
          </div>
        </section>

        <div className="runtime-adjustment-table-wrap">
          <table className="runtime-adjustment-table">
            <thead><tr><th scope="col">Use</th><th scope="col">Group</th><th scope="col">Target duration</th><th scope="col">{goalMode ? 'Target TPS' : 'Target VUsers'}</th><th scope="col">Planned end</th></tr></thead>
            <tbody>{groups.map((group, index) => {
              const result = preview?.groups.find(candidate => candidate.name === group.name)
              return <tr key={group.name}>
                <td><input type="checkbox" aria-label={`Select ${group.name}`} checked={group.selected} onChange={event => { setGroups(current => current.map((item, itemIndex) => itemIndex === index ? { ...item, selected: event.target.checked } : item)); setPreview(null) }} /></td>
                <td><strong>{group.name}</strong>{group.role === 'observer' && <small>Observer · max {maxVUsersForRole(group.role)} VUs</small>}{group.stageDriven && <small>Future stages may replace a manual load target.</small>}</td>
                <td><input aria-label={`${group.name} target duration`} value={group.duration} disabled={!group.selected} onChange={event => { setGroups(current => current.map((item, itemIndex) => itemIndex === index ? { ...item, duration: event.target.value } : item)); setPreview(null) }} /></td>
                <td><input type="number" min={goalMode ? 0.1 : 0} max={goalMode ? 100000 : maxVUsersForRole(group.role)} step={goalMode ? 0.1 : 1} aria-label={`${group.name} ${goalMode ? 'target TPS' : 'target VUsers'}`} value={goalMode ? group.targetTps ?? '' : group.vusers} disabled={!group.selected} onChange={event => { const value = Number(event.target.value); setGroups(current => current.map((item, itemIndex) => itemIndex === index ? goalMode ? { ...item, targetTps: value } : { ...item, vusers: value } : item)); setPreview(null) }} /></td>
                <td>{result?.effective_end_at ? new Date(result.effective_end_at).toLocaleString() : 'Preview required'}</td>
              </tr>
            })}</tbody>
          </table>
        </div>

        {!goalMode && <section className="runtime-adjustment-transition" aria-label="Load transition">
          <strong>Load transition</strong>
          <label><input type="radio" checked={transition === 'gradual'} onChange={() => { setTransition('gradual'); setPreview(null) }} /> Gradual</label>
          <label><input type="radio" checked={transition === 'immediate'} onChange={() => { setTransition('immediate'); setPreview(null) }} /> Immediate</label>
          <input aria-label="Runtime adjustment ramp duration" value={rampDuration} disabled={transition !== 'gradual'} onChange={event => { setRampDuration(event.target.value); setPreview(null) }} />
        </section>}

        {error && <p className="runtime-adjustment-error" role="alert">{error}</p>}
        {preview && <section className={`runtime-adjustment-impact ${preview.decision}`} aria-label="Runtime adjustment impact">
          <strong>{preview.decision === 'allowed' ? 'Ready to apply' : 'Adjustment conflicts with scheduled capacity'}</strong>
          <p>Scenario duration {preview.impact.scenario_duration} · peak {preview.impact.peak_vusers.toLocaleString()} VUsers · {preview.impact.recommended_generators} generators · ends {preview.impact.scenario_end_at ? new Date(preview.impact.scenario_end_at).toLocaleString() : 'n/a'}.</p>
          {preview.conflicts?.map(conflict => <p key={`${conflict.code}-${conflict.message}`}>{conflict.message}</p>)}
          {preview.decision === 'blocked' && preview.safe_proposal && <button type="button" className="btn btn-sm btn-secondary" onClick={useSafeMaximum}>Use safe maximum</button>}
        </section>}

        <footer>
          <button type="button" className="btn btn-sm btn-secondary" onClick={onClose} disabled={loading}>Cancel</button>
          <button type="button" className="btn btn-sm btn-secondary" onClick={() => void runPreview()} disabled={loading || !requestValid}>{loading ? 'Checking…' : 'Review changes'}</button>
          <button type="button" className="btn btn-sm btn-primary" onClick={() => void apply()} disabled={loading || preview?.decision !== 'allowed'}>Apply changes</button>
        </footer>
      </div>
    </div></DialogLayer>, document.body,
  )
}
