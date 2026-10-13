import { useState } from 'react'

import type { SharedAssetScenarioUse, SharedAssetScriptUse } from '../types/sharedAssets'
import { scenarioDataStepHref, scenarioLibraryHref, scriptEditorHref } from './assetLinks'
import './SharedAssetUsedBy.css'

/** How many users a row lists before "Show all". */
const SHOWN = 5

type Props = {
  projectId: string
  /** What the list is, e.g. "Used by" or "Applied to". */
  label?: string
  /** Shown when nothing uses the asset. */
  none: string
} & (
  | { kind: 'scenario'; uses: SharedAssetScenarioUse[] }
  | { kind: 'script'; uses: SharedAssetScriptUse[] }
)

/**
 * What uses a shared asset: each scenario or script a link to where it is
 * changed. Scenarios open at their Data step and name the alias they bind; an
 * archived one cannot be edited until it is restored, so it points there.
 */
export default function SharedAssetUsedBy(props: Props) {
  const { projectId, label = 'Used by', none } = props
  const [expanded, setExpanded] = useState(false)
  if (props.uses.length === 0) return <p className="shared-asset-used-by is-empty">{none}</p>
  const total = props.uses.length
  const noun = props.kind === 'scenario' ? (total === 1 ? 'scenario' : 'scenarios') : (total === 1 ? 'script' : 'scripts')
  const items = props.kind === 'scenario'
    ? props.uses.map(use => ({ key: `${use.id}:${use.alias}`, href: use.archived ? '' : scenarioDataStepHref(use.id, projectId), name: use.name, detail: [`as ${use.alias}`, use.archived ? 'archived' : ''] }))
    : props.uses.map(use => ({ key: use.id, href: scriptEditorHref(use.id, projectId), name: use.name, detail: [use.archived ? 'archived' : ''] }))
  const archivedScenario = props.kind === 'scenario' && props.uses.some(use => use.archived)
  const detailOf = (parts: string[]) => parts.filter(Boolean).join(', ')
  const shown = expanded ? items : items.slice(0, SHOWN)
  return <div className="shared-asset-used-by">
    <span className="shared-asset-used-by-label">{`${label} ${total} ${noun}`}</span>
    <ul aria-label={`${label} ${total} ${noun}`}>
      {shown.map(item => <li key={item.key}>
        {item.href ? <a href={item.href}>{item.name}</a> : <span>{item.name}</span>}
        {detailOf(item.detail) && <span className="shared-asset-used-by-detail">({detailOf(item.detail)})</span>}
      </li>)}
    </ul>
    {archivedScenario && <span>Restore an archived scenario from the <a href={scenarioLibraryHref(projectId)}>Scenario library</a> to change it.</span>}
    {total > SHOWN && <button type="button" className="btn btn-ghost btn-sm" aria-expanded={expanded} onClick={() => setExpanded(current => !current)}>{expanded ? 'Show fewer' : `Show all ${total}`}</button>}
  </div>
}
