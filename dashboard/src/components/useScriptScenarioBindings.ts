import { useCallback, useEffect, useMemo, useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { ScenarioDataPoolBinding } from '../types'
import { dataTableChoice, type DataTableChoice } from './scenarioDataBindingModel'
import type { LiveTableKind } from './scriptDataUsageModel'
import { useProjectLiveTables } from './useProjectLiveTables'

/** How one saved scenario binds an alias. */
export interface AliasBinding {
  scenarioId: string
  scenarioName: string
  binding: ScenarioDataPoolBinding
  choice: DataTableChoice
  /** The table's kind, for a live table whose kind is known. */
  kind?: LiveTableKind
}

export interface ScriptScenarioBindings {
  status: 'idle' | 'loading' | 'ready' | 'failed'
  /** How many saved scenarios run this script. */
  scenarios: number
  byAlias: Map<string, AliasBinding[]>
  retry: () => void
}

const EMPTY = new Map<string, AliasBinding[]>()

/**
 * How the project's saved scenarios that run this script bind its data
 * aliases. Loads while `enabled` (the script is saved and uses shared data).
 */
export function useScriptScenarioBindings(scriptId: string | undefined, projectId: string | undefined, enabled: boolean): ScriptScenarioBindings {
  const api = useApi()
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<{ key: string; status: 'loading' | 'ready' | 'failed'; bindings: { scenarioId: string; scenarioName: string; binding: ScenarioDataPoolBinding }[]; scenarios: number } | null>(null)
  const key = `${scriptId}|${projectId}|${attempt}`
  const active = enabled && Boolean(scriptId)
  useEffect(() => {
    if (!active) return undefined
    let cancelled = false
    setState({ key, status: 'loading', bindings: [], scenarios: 0 })
    api.listScenarios(projectId).then(response => {
      if (cancelled) return
      const using = (response.scenarios || []).filter(scenario => scenario.scripts?.some(ref => ref.script_id === scriptId))
      setState({ key, status: 'ready', scenarios: using.length, bindings: using.flatMap(scenario => (scenario.data_pools || []).map(binding => ({ scenarioId: scenario.id, scenarioName: scenario.name, binding }))) })
    }).catch(() => { if (!cancelled) setState({ key, status: 'failed', bindings: [], scenarios: 0 }) })
    return () => { cancelled = true }
  }, [api, active, key, projectId, scriptId])
  const current = active && state?.key === key ? state : null
  const projectTables = useProjectLiveTables(projectId, Boolean(current?.bindings.some(entry => entry.binding.scope === 'project')))
  const retry = useCallback(() => setAttempt(value => value + 1), [])
  const byAlias = useMemo(() => {
    if (!current) return EMPTY
    const result = new Map<string, AliasBinding[]>()
    for (const { scenarioId, scenarioName, binding } of current.bindings) {
      const choice = dataTableChoice(binding)
      const kind = choice === 'run' ? binding.live_options?.profile || 'native_queue' : choice === 'project' ? projectTables.tables.find(table => table.id === binding.project_table_id)?.profile : undefined
      result.set(binding.alias, [...(result.get(binding.alias) || []), { scenarioId, scenarioName, binding, choice, kind }])
    }
    return result
  }, [current, projectTables.tables])
  if (!current) return { status: active ? 'loading' : 'idle', scenarios: 0, byAlias, retry }
  return { status: current.status, scenarios: current.scenarios, byAlias, retry }
}
