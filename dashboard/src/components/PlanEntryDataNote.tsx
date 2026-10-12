import type { ScenarioDataPoolBinding } from '../types'
import { scenarioLiveTables } from './scenarioDataBindingModel'

/** What an entry's overrides change, and the scenario's live tables they leave in place. */
export default function PlanEntryDataNote({ bindings, sequential }: { bindings?: ScenarioDataPoolBinding[]; sequential: boolean }) {
  const live = scenarioLiveTables(bindings)
  return (
    <p className="setting-desc pp-field-help">
      An override replaces the scenario&apos;s binding for the same alias; its other bindings stay.
      {live && <> The scenario&apos;s live tables: {live.list}.</>}
      {live?.runScoped && sequential && <> Each entry runs with its own run-scoped table, so rows do not pass from one entry to the next; use a project live table for that.</>}
    </p>
  )
}
