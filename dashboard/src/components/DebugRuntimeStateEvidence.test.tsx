import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import type { ReplayRuntimeSnapshot } from '../types/replay'
import { DebugVariableWatches } from './DebuggerSessionTools'
import { DebugRuntimeStateEvidence } from './DebugRuntimeStateEvidence'
import { DebugParameterInspector } from './DebugParameterInspector'
import type { ScriptDefinition } from '../types/scripts'
const snapshot = (): ReplayRuntimeSnapshot => ({ state: 'bounded', boundary: 'before_action', location: { phase: 'action', action_index: 1, event_index: 0, action_name: 'Read', action_type: 'request' }, variables: [{ name: '[redacted]', value_state: 'assigned', value: 'one' }, { name: '[redacted]', value_state: 'assigned', value: 'two' }, { name: 'empty', value_state: 'assigned', value: '' }, { name: 'masked', value_state: 'redacted', value: 'redacted-do-not-display' }, { name: 'missing', value_state: 'omitted' }], parameters: [{ name: 'counter', state: 'observed', update_policy: 'each_occurrence', value_state: 'observed', value: '42', observed_event_id: 'actual-use-receipt' }, { name: 'prepared', state: 'prepared', update_policy: 'once', value_state: 'assigned', value: 'cached' }, { name: 'future', state: 'not_evaluated', update_policy: 'each_occurrence', value_state: 'omitted' }], rows: [{ kind: 'foreach', name: 'item', source: 'orders', row_ordinal: 7, selection: 'foreach', state: 'selected' }], frames: [{ kind: 'phase', phase: 'action', action_index: -1 }, { kind: 'foreach', phase: 'action', action_index: 1, iteration_path: [2] }], omissions: { variables: 1, parameters: 0, rows: 0, frames: 0, values: 2 } })
const props = { events: [], seed: { stale: 'seed-secret' }, latest: { stale: 'historical-secret' }, selected: null, onSelect: () => {} }
describe('current paused watches and explicit qualified history', () => {
  it('defaults to unavailable current state without falling back to retained final values or seed', () => {
    const html = renderToStaticMarkup(<DebugVariableWatches {...props} />)
    expect(html).toContain('Current paused state'); expect(html).toContain('Current state unavailable')
    expect(html).not.toContain('historical-secret'); expect(html).not.toContain('seed-secret'); expect(html).not.toContain('Values as JSON')
  })
  it('only exposes historical values after explicit mode selection and qualifies completed final values', () => {
    const html = renderToStaticMarkup(<DebugVariableWatches {...props} mode="retained" completed />)
    expect(html).toContain('historical-secret'); expect(html).toContain('Final historical values'); expect(html).toContain('historical evidence, not current engine state')
    expect(html).toContain('Pin stale')
  })
  it('displays independent redacted-name entries, empty versus omitted, prepared versus observed, and original row/frame receipts', () => {
    const html = renderToStaticMarkup(<DebugRuntimeStateEvidence snapshot={snapshot()} />)
    for (const label of ['Current paused state', 'Before action', 'action 2', 'Bounded snapshot', 'one', 'two', '(empty)', 'Unavailable', 'Redacted', 'Ambiguous name', 'Parameter runtime receipts', 'each occurrence', 'actual-use-receipt', 'prepared', 'cached', 'not evaluated', 'Row selection receipts', '>7<', 'Active runtime frames', 'iteration 2', 'No actual-use receipt']) expect(html).toContain(label)
    expect(html).not.toContain('redacted-do-not-display')
    expect(html).toContain('do not predict the next reference')
  })
  it('an empty full replacement displays no prior keys, rows or frames', () => {
    const current = { ...snapshot(), variables: [], parameters: [], rows: [], frames: [] }
    const html = renderToStaticMarkup(<DebugVariableWatches {...props} snapshot={current} />)
    expect(html).toContain('No current variable bindings'); expect(html).toContain('No row selections'); expect(html).toContain('No active runtime frames')
    expect(html).not.toContain('historical-secret'); expect(html).not.toContain('actual-use-receipt')
  })
  it('privacy unavailability hides both current receipts and explicit historical mode', () => {
    for (const mode of ['current', 'retained', 'selected'] as const) {
      const html = renderToStaticMarkup(<DebugVariableWatches {...props} snapshot={snapshot()} mode={mode} evidenceUnavailable />)
      for (const detail of ['historical-secret', 'seed-secret', 'actual-use-receipt', 'cached', 'Values at']) expect(html).not.toContain(detail)
      expect(html).toContain('Runtime variable evidence unavailable')
    }
  })
  it('parameter inspection uses a parameter receipt independently of a same-named seeded variable or selected event', () => {
    const script = { name: 'Parameters', protocol: 'http', version: '1', config: { base_url: '' }, actions: [], parameters: [{ name: 'counter', type: 'counter', update: 'each_occurrence' }] } as ScriptDefinition
    const html = renderToStaticMarkup(<DebugParameterInspector script={script} name="counter" currentMode runtimeParameter={snapshot().parameters[0]} values={{ counter: 'unqualified-seed' }} event={null} matchesDraft onEdit={() => {}} />)
    expect(html).toContain('observed'); expect(html).toContain('42'); expect(html).toContain('actual-use-receipt'); expect(html).not.toContain('unqualified-seed')
    const absent = renderToStaticMarkup(<DebugParameterInspector script={script} name="counter" currentMode values={{ 'param.counter': 'ambiguous' }} event={null} matchesDraft onEdit={() => {}} />)
    expect(absent).toContain('no unambiguous held-boundary receipt'); expect(absent).not.toContain('ambiguous</')
  })
})

it('qualifies masked receipt names and refuses to attach them to an authored parameter identity', () => {
  const parameter: ReplayRuntimeSnapshot['parameters'][number] = { name: '[REDACTED]', state: 'prepared', update_policy: 'once', value_state: 'assigned', value: 'masked-identity-value' }
  const state = { ...snapshot(), variables: [{ name: '[OMITTED]', value_state: 'assigned' as const, value: 'independent-receipt' }], parameters: [parameter] }
  const html = renderToStaticMarkup(<DebugRuntimeStateEvidence snapshot={state} />)
  expect(html).toContain('Masked name'); expect(html).toContain('independent-receipt'); expect(html).toContain('masked-identity-value')
  const script = { name: 'Parameters', protocol: 'http', version: '1', config: { base_url: '' }, actions: [], parameters: [{ name: '[REDACTED]', type: 'list', update: 'once' }] } as ScriptDefinition
  const inspector = renderToStaticMarkup(<DebugParameterInspector script={script} name="[REDACTED]" currentMode runtimeParameter={parameter} values={null} event={null} matchesDraft onEdit={() => {}} />)
  expect(inspector).toContain('no unambiguous held-boundary receipt'); expect(inspector).not.toContain('masked-identity-value')
})
