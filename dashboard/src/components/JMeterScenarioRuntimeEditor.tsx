import { useEffect, useState } from 'react'

import type { JMeterScenarioDraft } from '../types/scriptImports'
import { isValidDurationInput } from '../utils/timeUtils'
import DurationField from './DurationField'
import ImportReviewSectionHeader from './ImportReviewSectionHeader'
import { countLabel } from '../utils/numberFormat'

export default function JMeterScenarioRuntimeEditor({ scenario, onApply, disabled = false }: {
  scenario?: JMeterScenarioDraft
  onApply: (scenario: JMeterScenarioDraft) => void
  disabled?: boolean
}) {
  const [mode, setMode] = useState<'duration' | 'iterations'>(scenario?.termination_mode === 'iterations' ? 'iterations' : 'duration')
  const [duration, setDuration] = useState(scenario?.duration || '5m')
  const [iterations, setIterations] = useState(Math.max(1, scenario?.groups.find(group => group.iterations)?.iterations || 1))
  const [message, setMessage] = useState('')
  useEffect(() => {
    setMode(scenario?.termination_mode === 'iterations' ? 'iterations' : 'duration')
    setDuration(scenario?.duration || '5m')
    setIterations(Math.max(1, scenario?.groups.find(group => group.iterations)?.iterations || 1))
    setMessage('')
  }, [scenario])
  if (!scenario) return <p className="import-review-empty">No scenario draft was generated. Configure runtime behavior after saving the scripts.</p>
  const durationValid = mode !== 'duration' || isValidDurationInput(duration)
  const apply = () => {
    const next: JMeterScenarioDraft = {
      ...scenario,
      termination_mode: mode,
      duration: mode === 'duration' ? duration.trim() || '5m' : scenario.duration,
      groups: scenario.groups.map(group => ({
        ...group,
        iterations: mode === 'iterations' ? Math.max(1, Math.floor(iterations || 1)) : undefined,
      })),
    }
    onApply(next)
    setMessage(mode === 'iterations' ? `Applied ${countLabel(Math.max(1, Math.floor(iterations || 1)), 'iteration')} to every imported thread group.` : `Applied a finite duration of ${next.duration}.`)
  }
  return <section className="jmeter-runtime-editor" aria-label="JMeter runtime settings">
    <ImportReviewSectionHeader title="Imported scenario settings" description="Choose the finite completion rule used when the generated scenario draft is saved. This resolves unbounded thread-group blockers in the current review." />
    <div className="import-review-toolbar">
      <label>Completion rule<select value={mode} disabled={disabled} onChange={event => setMode(event.target.value as typeof mode)}><option value="duration">Run for a duration</option><option value="iterations">Finish per-user iterations</option></select></label>
      {mode === 'duration' ? <DurationField label="Duration" value={duration} disabled={disabled} required onChange={setDuration} /> : <label>Iterations per VUser<input type="number" min={1} step={1} value={iterations} disabled={disabled} onChange={event => setIterations(Math.max(1, Math.floor(Number(event.target.value) || 1)))} /></label>}
      <button type="button" className="btn btn-primary" disabled={disabled || !durationValid || scenario.groups.length === 0} onClick={apply}>Apply runtime rule</button>
    </div>
    {scenario.groups.length === 0 && <p role="alert">No imported thread groups are available. Re-inspect the source before applying a runtime rule.</p>}
    <ul>{scenario.groups.map(group => <li key={group.source_group_id || group.name}><strong>{group.name}</strong>: {mode === 'iterations' ? `${group.vusers} VUsers × ${Math.max(1, Math.floor(iterations || 1))} iterations` : `${group.vusers} VUsers · ${duration || 'duration required'}`} · ramp-up {group.ramp_up || 'not specified'}</li>)}</ul>
    {message && <p role="status">{message}</p>}
  </section>
}
