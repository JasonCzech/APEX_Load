import { useState, useRef, useEffect } from 'react'
import type { ScriptDefinition } from '../types/scripts'
import type { BrowserReplayEvent } from '../types/replay'

export function DebugParameterInspector({ script, name, values, event, matchesDraft, onFindUsages, onEdit }: {
  script: ScriptDefinition; name: string; values: Record<string, string> | null
  event: BrowserReplayEvent | null | undefined; matchesDraft: boolean; onFindUsages?: () => void; onEdit: (name: string) => void
}) {
  const heading = useRef<HTMLHeadingElement>(null)
  useEffect(() => { heading.current?.focus() }, [name])
  const [revealed, setRevealed] = useState(false)
  const parameter = script.parameters?.find(item => item.name === name)
  if (!parameter) return null
  // Only a captured param namespace is evidence for a parameter. An unqualified
  // seeded/extracted variable with the same name is a different namespace.
  const value = event && values && Object.prototype.hasOwnProperty.call(values, `param.${name}`) ? values[`param.${name}`] : undefined
  return <section className="debug-parameter-inspector" aria-label={`Debug parameter ${name}`}>
    <h3 ref={heading} tabIndex={-1}>{name}</h3>
    <p>Current draft · {parameter.type} · {parameter.update === 'once' ? 'Once per virtual user' : parameter.update === 'each_occurrence' ? 'Every template reference' : 'Each iteration'}</p>
    <p>Source: {parameter.data_source ? `${parameter.data_source}.${parameter.column || ''}` : parameter.values?.length ? 'Inline values' : 'Generated'}</p>
    <button className="btn btn-sm btn-secondary" type="button" onClick={() => onEdit(name)}>Edit definition in Author</button>
    {onFindUsages && <button className="btn btn-sm btn-ghost" type="button" onClick={onFindUsages}>Find usages in source</button>}
    <h4>Captured parameter value</h4>
    {event && <p>{event.action_name || 'Selected event'} · iteration {event.iteration ?? 'not recorded'} · {event.phase}</p>}
    {event && !matchesDraft && <p role="status">Draft changed since replay. Captured evidence belongs to the recorded revision.</p>}
    {value === undefined ? <p>Not captured for this parameter at the selected event. Inspect the captured request for resolved template values.</p> : <><output>{revealed ? value : '••••••••'}</output><button type="button" onClick={() => setRevealed(value => !value)}>{revealed ? 'Hide captured value' : 'Reveal captured value'}</button></>}
  </section>
}
