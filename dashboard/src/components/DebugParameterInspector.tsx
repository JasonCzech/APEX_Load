import { useRef, useEffect } from 'react'
import type { ScriptDefinition } from '../types/scripts'
import { RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE } from './runtimeVariableEvidence'
import { runtimeIdentityNameIsKnown, runtimeValueLabel } from './debugRuntimeState'
import type { BrowserReplayEvent, ReplayRuntimeSnapshot } from '../types/replay'

export function DebugParameterInspector({ script, name, values, event, matchesDraft, onFindUsages, onEdit, evidenceUnavailable = false, runtimeParameter, currentMode = false }: {
  runtimeParameter?: ReplayRuntimeSnapshot['parameters'][number]; currentMode?: boolean
  script: ScriptDefinition; name: string; values: Record<string, string> | null; evidenceUnavailable?: boolean
  event: BrowserReplayEvent | null | undefined; matchesDraft: boolean; onFindUsages?: () => void; onEdit: (name: string) => void
}) {
  const heading = useRef<HTMLHeadingElement>(null)
  useEffect(() => { heading.current?.focus() }, [name])
  const currentParameter = runtimeParameter?.name === name && runtimeIdentityNameIsKnown(name) ? runtimeParameter : undefined
  const parameter = script.parameters?.find(item => item.name === name)
  if (!parameter) return null
  // Only a captured param namespace is evidence for a parameter. An unqualified
  // seeded/extracted variable with the same name is a different namespace.
  const value = !evidenceUnavailable && event && values && Object.prototype.hasOwnProperty.call(values, `param.${name}`) ? values[`param.${name}`] : undefined
  return <section className="debug-parameter-inspector" aria-label={`Debug parameter ${name}`}>
    <h3 ref={heading} tabIndex={-1}>{name}</h3>
    <p>Current draft · {parameter.type} · {parameter.update === 'once' ? 'Once per virtual user' : parameter.update === 'each_occurrence' ? 'Every template reference' : 'Each iteration'}</p>
    <p>Source: {parameter.data_source ? `${parameter.data_source}.${parameter.column || ''}` : parameter.values?.length ? 'Inline values' : 'Generated'}</p>
    <button className="btn btn-sm btn-secondary" type="button" onClick={() => onEdit(name)}>Edit definition in Author</button>
    {onFindUsages && <button className="btn btn-sm btn-ghost" type="button" onClick={onFindUsages}>Find usages in source</button>}
    <h4>{currentMode ? 'Current parameter runtime state' : 'Retained parameter value'}</h4>
    {currentMode && !evidenceUnavailable ? <p role="status">{currentParameter ? `${currentParameter.state.replace(/_/g, ' ')} · ${currentParameter.update_policy.replace(/_/g, ' ')} · ${runtimeValueLabel(currentParameter)} · ${currentParameter.observed_event_id || 'No actual-use receipt'}` : 'Current parameter state unavailable. Not captured: no unambiguous held-boundary receipt.'}</p> : null}
    {event && !evidenceUnavailable && !currentMode && <p>{event.action_name || 'Selected event'} · iteration {event.iteration ?? 'not recorded'} · {event.phase}</p>}
    {event && !evidenceUnavailable && !currentMode && !matchesDraft && <p role="status">Draft changed since replay. Captured evidence belongs to the recorded revision.</p>}
    {evidenceUnavailable ? <p role="status">{RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE}</p> : currentMode ? null : value === undefined ? <p>Not captured for this parameter at the selected event. Inspect the captured request for resolved template values.</p> : <output>{value}</output>}
  </section>
}
