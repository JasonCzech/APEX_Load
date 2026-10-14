import type { BrunoGeneratedValueGroup } from './brunoGeneratedValueGroups'
import { countLabel } from '../utils/numberFormat'

interface Props {
  groups: BrunoGeneratedValueGroup[]
  choices: Record<string, boolean>
  onChange: (choices: Record<string, boolean>) => void
  disabled: boolean
}

export default function BrunoGeneratedValues({ groups, choices, onChange, disabled }: Props) {
  if (groups.length === 0) return null
  return <section className="bruno-card bruno-generated-values" aria-labelledby="bruno-generated-values-title">
    <h3 id="bruno-generated-values-title">Generated values</h3>
    <p>The collection recorded these IDs and time stamps once, so every virtual user would send the same ones. Checked values are generated again each iteration.</p>
    <ul className="bruno-generated-list">
      {groups.map(group => <li key={group.key}>
        <label>
          <input type="checkbox" checked={choices[group.key] ?? group.suggested} disabled={disabled}
            onChange={event => onChange({ ...choices, [group.key]: event.target.checked })} />
          <span>{group.description}</span>
        </label>
        <small>{countLabel(group.requests, 'selected API')}{group.suggested ? '' : ' · Not checked: confirm the target does not expect this exact value'}</small>
      </li>)}
    </ul>
  </section>
}
