import { useId } from 'react'

import { useDslActionCatalog } from './dslActionCatalog'
import { DATA_TABLE_CHOICES, dataBindingMismatches } from './scenarioDataBindingModel'
import { LIVE_TABLE_KIND_LABELS, type DataAliasStep, type DataAliasUsage } from './scriptDataUsageModel'
import type { SharedAssetsSection } from './sharedAssetsNavigation'
import type { AliasBinding, ScriptScenarioBindings } from './useScriptScenarioBindings'
import './ScriptDataUsagePanel.css'

/**
 * Each alias the script reads or changes: the columns and table steps it uses,
 * whether it expects a row from the binding each iteration, and which saved
 * scenarios bind it. Aliases live in the script; scenarios choose the tables.
 */
export default function ScriptDataUsagePanel({ usage, saved, bindings, onSelectStep, onOpenTable }: {
  usage: DataAliasUsage[]
  /** Whether the script is saved (only a saved script is in scenarios). */
  saved: boolean
  bindings: ScriptScenarioBindings
  onSelectStep: (step: DataAliasStep) => void
  /** Opens the pool or project live table a scenario binds, in Shared assets. */
  onOpenTable?: (section: SharedAssetsSection, resourceId: string) => void
}) {
  const id = useId()
  const { catalog } = useDslActionCatalog()
  const variants = catalog?.actions.find(action => action.type === 'live_table')?.variants || []
  const operationLabel = (value: string) => variants.find(variant => variant.value === value)?.label || value
  const known = saved && bindings.status === 'ready'
  return <section className="script-data-usage" aria-labelledby={id}>
    <h3 id={id}>Data this script uses</h3>
    <p>Most scripts need no table steps: a scenario binding can read, remove or lease a row every iteration, and steps read it as <code>{'{{data.alias.column}}'}</code>. The aliases live in this script; each scenario chooses the table behind them.</p>
    {!saved ? <p role="status">Save the script, then bind these aliases in a scenario that runs it.</p>
      : bindings.status === 'loading' ? <p role="status">Checking which scenarios bind these aliases…</p>
        : bindings.status === 'failed' ? <p role="status">Could not check the scenarios that run this script. <button type="button" className="btn btn-sm btn-secondary" onClick={bindings.retry}>Check again</button></p>
          : <p role="status">{bindings.scenarios === 0 ? 'No saved scenario runs this script yet.' : `${bindings.scenarios} saved ${bindings.scenarios === 1 ? 'scenario runs' : 'scenarios run'} this script.`}</p>}
    <ul className="script-data-aliases">{usage.map(item => {
      const bound = bindings.byAlias.get(item.alias) || []
      const need = { ...item, scripts: ['This script'] }
      const mismatches = bound.flatMap(entry => dataBindingMismatches(entry.binding, need, { columns: [], kind: entry.kind }).map(problem => `In ${entry.scenarioName}: ${problem}`))
      return <li key={item.alias} className="script-data-alias">
        <h4><code>{item.alias}</code></h4>
        <dl>
          <dt>Columns</dt>
          <dd>{item.columns.length ? item.columns.join(', ') : 'None named yet'}</dd>
          <dt>Table steps</dt>
          <dd>{item.steps.length || item.nativeOperations.length ? <ul className="script-data-steps">
            {item.steps.map((step, at) => <li key={at}><button type="button" className="link-button" onClick={() => onSelectStep(step)}>{step.name}</button> <small>{operationLabel(step.operation)}</small></li>)}
            {item.nativeOperations.length > 0 && <li>Native code: {item.nativeOperations.map(operationLabel).join(', ')}</li>}
          </ul> : 'None'}</dd>
          <dt>Each iteration</dt>
          <dd>{item.readsEachIteration ? <>Expects a row from the binding (<code>{`{{data.${item.alias}.…}}`}</code>)</> : 'Expects no row from the binding'}</dd>
          <dt>Bound in</dt>
          <dd>{bound.length ? <ul className="script-data-steps">{bound.map(entry => {
            const table = sharedTableOf(entry)
            return <li key={entry.scenarioId}>{entry.scenarioName}: {DATA_TABLE_CHOICES.find(choice => choice.value === entry.choice)!.label.toLowerCase()}{entry.kind ? ` (${LIVE_TABLE_KIND_LABELS[entry.kind]})` : ''}
              {table && onOpenTable && <> <button type="button" className="link-button" aria-label={`Open the ${table.label} ${entry.scenarioName} binds as ${item.alias}`} onClick={() => onOpenTable(table.section, table.resourceId)}>{`Open ${table.label}`}</button></>}
            </li>
          })}</ul>
            : !saved || known ? <strong>Bind in a scenario before running</strong> : 'Not known yet'}</dd>
        </dl>
        {mismatches.map(problem => <p key={problem} className="script-data-mismatch" role="status">{problem}</p>)}
      </li>
    })}</ul>
  </section>
}

/**
 * The shared table behind a binding, by the table it binds: a project live
 * table, or the pool a snapshot reads or a run table starts from. A run table
 * that starts empty has none.
 */
function sharedTableOf(entry: AliasBinding): { section: SharedAssetsSection; resourceId: string; label: string } | null {
  if (entry.choice === 'project') return entry.binding.project_table_id ? { section: 'live-tables', resourceId: entry.binding.project_table_id, label: 'live table' } : null
  // As the scenario's Data step decides whether a run table reads its pool.
  if (entry.choice === 'run' && ['empty', 'original_seed'].includes(entry.binding.live_options?.seed_policy || '')) return null
  return entry.binding.pool_id ? { section: 'test-data', resourceId: entry.binding.pool_id, label: 'pool' } : null
}
