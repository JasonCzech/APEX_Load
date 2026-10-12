import type { ScriptActionDefinition } from '../types/scripts'

export default function NativeScriptActionFields({ action, onChange }: { action: ScriptActionDefinition; onChange: (params: Record<string, unknown>) => void }) {
  const params = action.params || {}
  const update = (patch: Record<string, unknown>) => onChange({ ...params, ...patch })
  if (action.type === 'rendezvous') {
    const distributed = params.scope === 'distributed_group'
    const target = params.count !== undefined ? 'count' : params.percent !== undefined ? 'percent' : 'all'
    return <section className="sub-section" aria-label="Rendezvous settings"><h4>Rendezvous</h4>
      <p>Local scope coordinates VUsers on one load generator. Distributed group scope coordinates a fixed admitted population across the group’s load generators.</p>
      <div className="form-grid">
        <label>Scope<select value={distributed ? 'distributed_group' : 'local'} onChange={event => update({ scope: event.target.value, ...(event.target.value === 'distributed_group' ? { key: params.key || action.name.replace(/\W+/g, '_'), timeout_ms: params.timeout_ms || 30000, on_timeout: params.on_timeout || 'fail' } : {}) })}><option value="local">Local load generator</option><option value="distributed_group">Distributed group</option></select></label>
        <label>Barrier key<input value={String(params.key || '')} onChange={event => update({ key: event.target.value })} maxLength={128} /></label>
        {distributed && <><label>Participants<select value={target} onChange={event => update({ count: event.target.value === 'count' ? 1 : undefined, percent: event.target.value === 'percent' ? 100 : undefined })}><option value="all">All admitted VUsers</option><option value="count">Fixed count</option><option value="percent">Percentage of admitted VUsers</option></select></label>
          {target !== 'all' && <label>{target === 'count' ? 'Participant count' : 'Participant percentage'}<input type="number" min={1} max={target === 'count' ? 100000 : 100} step={1} value={Number(params[target])} onChange={event => update({ [target]: Number(event.target.value) })} /></label>}
          <label>Maximum wait (ms)<input type="number" min={1} max={120000} step={1} value={Number(params.timeout_ms || 30000)} onChange={event => update({ timeout_ms: Number(event.target.value) })} /></label>
          <label>On timeout<select value={String(params.on_timeout || 'fail')} onChange={event => update({ on_timeout: event.target.value })}><option value="fail">Fail the action</option><option value="continue">Continue after timeout</option></select></label>
        </>}
      </div>
      {distributed && <p>Use a unique static key in sequential iteration actions. Init, teardown, parallel branches and changing group membership cannot use distributed rendezvous. A release coordinates progress; network arrivals at the target are not simultaneous.</p>}
    </section>
  }
  const inputs = params.inputs === undefined ? {} : params.inputs
  const inputError = !inputs || typeof inputs !== 'object' || Array.isArray(inputs)
  return <section className="sub-section" aria-label="Native JavaScript settings"><h4>Host JavaScript</h4>
    <p>Source is a literal function body receiving <code>input</code> and <code>apex</code>. Put resolved values in Inputs; template-like text in source stays literal. The isolated host worker has no page, filesystem, process, environment or network API.</p>
    <label>JavaScript source<textarea rows={12} value={typeof params.source === 'string' ? params.source : ''} onChange={event => update({ source: event.target.value })} spellCheck={false} /></label>
    <label>Inputs (JSON object)<textarea rows={5} aria-invalid={inputError} value={typeof inputs === 'string' ? inputs : JSON.stringify(inputs, null, 2)} onChange={event => { let value: unknown; try { value = JSON.parse(event.target.value) } catch { value = event.target.value } update({ inputs: value }) }} /></label>
    {inputError && <p role="alert">Inputs must be a JSON object. The invalid draft is retained and cannot execute.</p>}
    <div className="form-grid"><label>Result prefix<input maxLength={128} value={String(params.result_prefix || '')} onChange={event => update({ result_prefix: event.target.value })} /></label><label>Maximum execution time (ms)<input type="number" min={100} max={120000} step={1} value={Number(params.timeout_ms ?? 5000)} onChange={event => update({ timeout_ms: Number(event.target.value) })} /></label></div>
    <p>Limits: 128 KiB source, 64 KiB inputs and returned action output, four concurrent workers per execution process and one outstanding data call per worker. Declare aliases and operations under Runtime settings → Script configuration → Native script services. Unsupported execution hosts refuse launch.</p>
  </section>
}
