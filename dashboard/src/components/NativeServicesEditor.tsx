import type { NativeTableDeclaration } from './nativeServicesModel'
import { nativeDeclarationErrors, NATIVE_TABLE_OPERATIONS, NATIVE_JAVA_EXAMPLES } from './nativeServicesModel'

export default function NativeServicesEditor({ declarations = [], onChange }: { declarations?: NativeTableDeclaration[]; onChange: (value: NativeTableDeclaration[]) => void }) {
  const update = (index: number, patch: Partial<NativeTableDeclaration>) => onChange(declarations.map((value, position) => position === index ? { ...value, ...patch } : value))
  return <section className="sub-section" aria-label="Native script services">
    <h3>Native script services</h3>
    <p>Declare the live-table aliases and operations used by native Java or host JavaScript. Bind each alias in the scenario’s Data step. Declarations grant guest operations only; the runtime manages renewal and cleanup separately.</p>
    {nativeDeclarationErrors(declarations).map(error => <p key={error} role="alert">{error}</p>)}
    {declarations.map((declaration, index) => <fieldset key={index}><legend>Live-table declaration {index + 1}</legend>
      <label>Alias<input maxLength={64} value={declaration.alias} onChange={event => update(index, { alias: event.target.value })} /></label>
      <div className="settings-grid">{NATIVE_TABLE_OPERATIONS.map(operation => <label key={operation}><input type="checkbox" checked={declaration.operations.includes(operation)} onChange={event => update(index, { operations: event.target.checked ? [...declaration.operations, operation] : declaration.operations.filter(value => value !== operation) })} />{operation.replace(/_/g, ' ')}</label>)}</div>
      <button type="button" className="btn btn-ghost" onClick={() => onChange(declarations.filter((_, position) => position !== index))}>Remove declaration {index + 1}</button>
    </fieldset>)}
    <button type="button" className="btn btn-secondary" disabled={declarations.length >= 16} onClick={() => onChange([...declarations, { alias: '', operations: [] }])}>Add live-table declaration</button>
    <details><summary>Native Java examples</summary>
      <p>Compile one example as <code>Actions.java</code> with the native <code>apexload.data</code> SDK. Declare the listed operations for <code>work</code> and bind a native queue with an <code>id</code> column. Use the runner’s init/action/end lifecycle; one-shot full lifecycle and background-thread callbacks are unsupported. These examples do not add permissions automatically.</p>
      {NATIVE_JAVA_EXAMPLES.map(example => <details key={example.name}><summary>{example.name}</summary><p>Required operations: {example.operations.join(', ')}.</p><textarea aria-label={`Java ${example.name.toLowerCase()} example`} rows={14} readOnly value={example.source} spellCheck={false} /></details>)}
    </details>
    <p>Java requires a runner advertising native callbacks. Host JavaScript requires an isolated worker with enforced memory limits; unsupported execution hosts refuse launch. Page JavaScript has no access to these services.</p>
  </section>
}
