import { useState } from 'react'
import type { ScriptDefinition, ScriptActionDefinition, ScriptConfig } from '../types/scripts'
import { effectiveThinkTimeLabel, runtimeStopConditions } from './runtimeWorkspaceModel'
import { formatPacingTiming } from './runtimeTiming'

export interface DebugRuntimeContext { ignoreThinkTime: boolean; logLevel: string; breakOnFailure: boolean }

function actionTiming(actions: ScriptActionDefinition[], prefix: string, config: ScriptConfig): { path: string; label: string }[] {
  return actions.flatMap((action, index) => {
    const path = `${prefix}[${index}]`
    return [
      ...(action.think_time && Object.keys(action.think_time).length ? [{ path, label: !action.think_time_after ? 'Inactive: this action does not wait after completion' : effectiveThinkTimeLabel({ base_url: '', think_time: action.think_time.type ? { ...action.think_time, multiplier: action.think_time.multiplier || config.think_time?.multiplier, limit_ms: action.think_time.limit_ms || config.think_time?.limit_ms, ignore: action.think_time.ignore || config.think_time?.ignore } : config.think_time }) }] : []),
      ...actionTiming(action.control?.steps || [], `${path}.control.steps`, config),
      ...actionTiming(action.control?.else || [], `${path}.control.else`, config),
      ...actionTiming(action.control?.default || [], `${path}.control.default`, config),
      ...(action.control?.cases || []).flatMap((item, i) => actionTiming(item.steps || [], `${path}.control.cases[${i}].steps`, config)),
    ]
  })
}

export function RuntimeContextSummary({ script, debug }: { script: ScriptDefinition; debug?: DebugRuntimeContext }) {
  const [scope, setScope] = useState<'script' | 'debug'>('script')
  const isDebug = scope === 'debug' && debug
  const overrides = [...actionTiming(script.init || [], 'init', script.config), ...actionTiming(script.actions, 'actions', script.config), ...actionTiming(script.teardown || [], 'teardown', script.config)]
  return <div className="runtime-effective-summary" role="region" aria-label="Effective runtime context">
    {debug ? <label>Preview context<select aria-label="Runtime preview context" value={scope} onChange={event => setScope(event.target.value as 'script' | 'debug')}><option value="script">Script defaults</option><option value="debug">Current Debug options</option></select></label> : <strong>Script defaults</strong>}
    {isDebug ? <>
      <p><strong>{debug.ignoreThinkTime ? 'No think-time delay — overridden for Debug' : effectiveThinkTimeLabel(script.config)}</strong> · Debug capture: {debug.logLevel} · {debug.breakOnFailure ? 'Break on failure' : 'Do not break on failure'}.</p>
      <p>Replay executes one virtual user through init, actions, and teardown. Load-test pacing, arrival rate, and iteration budgets do not schedule this replay. Script error handling and protocol settings still apply. Editors below change script defaults; Debug options change this replay context.</p>
    </> : <>
      <p>{effectiveThinkTimeLabel(script.config)} · {formatPacingTiming(script.config.pacing) || 'No pacing delay'}.</p>
      <p>Stop at the first applicable limit: {runtimeStopConditions(script.config).join(' · ')}.</p>
      <p>No scenario or run is selected here. Their runtime editors show resolved overrides. Values below edit the script defaults.</p>
    </>}
    <p>Think time applies after actions with Think Time After enabled; pacing schedules iterations. Action-local think-time settings may override script timing{(isDebug && debug.ignoreThinkTime) ? ', but Debug currently skips all think-time delays' : ''}.</p>
    {overrides.length > 0 && <details><summary>{overrides.length} action timing overrides</summary><ul>{overrides.map(item => <li key={item.path}><code>{item.path}</code>: {(isDebug && debug.ignoreThinkTime) ? 'Skipped for Debug' : item.label}</li>)}</ul></details>}
  </div>
}
