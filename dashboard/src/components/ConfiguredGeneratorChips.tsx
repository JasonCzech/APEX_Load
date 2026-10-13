import type { SystemInventoryGenerator } from '../types'
import { generatorDisplayName } from './generatorNames'

// The statuses Monitoring counts as online.
const REACHABLE_STATUSES = new Set(['connected', 'healthy', 'ready'])

interface Props {
  targets: string[]
  inventory: SystemInventoryGenerator[] | undefined
  rowClassName: string
  chipClassName: string
  labelClassName: string
}

/**
 * The generators automatic placement may use, named as people know them. The
 * list is the configured fleet, so one the live inventory cannot reach says so.
 */
export default function ConfiguredGeneratorChips({ targets, inventory, rowClassName, chipClassName, labelClassName }: Props) {
  if (targets.length === 0) return null
  return (
    <div className={rowClassName} role="group" aria-label="Configured generators">
      <span className={labelClassName}>Configured generators</span>
      {targets.map(target => {
        const status = inventory?.find(item => item.target === target)?.status
        const unreachable = inventory?.length ? !REACHABLE_STATUSES.has(status || '') : false
        return <span key={target} className={chipClassName} title={target}>{generatorDisplayName(target, inventory)}{unreachable ? ' · unreachable' : ''}</span>
      })}
    </div>
  )
}
