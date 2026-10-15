import type { SharedDataPool } from '../types/dataPools'
import { brunoInputBindingComplete, type BrunoInputBinding as ImportInputBinding } from './brunoReviewState'
import { aliasForPool, dataPoolColumnNames } from './scenarioDataBindingModel'
import './ImportInputSupply.css'

// How a re-import's earlier scripts supplied an input.
export interface PreviousInput {
  mode: string
  value?: string
  alias?: string
  column?: string
  generator?: string
  digits?: number
  /** The project secret a credential input read, {{secret.NAME}}. */
  reference?: string
}

export type SupplyMode = 'value' | 'data' | 'generated' | 'secret'

const DEFAULT_DIGITS = 10

// Supplies one input a source left unset: one value for every virtual user, a
// data pool column each user reads its own row of, or a value generated each
// iteration. A credential takes a project secret or a data pool column, never
// a value kept in the script, and stays unsupplied until the operator picks
// one. Bruno's review and every other importer's shared values panel use it,
// so the choice reads the same everywhere.
export default function ImportInputSupply({ variable, value, binding, previous, pools, canBind, disabled, onValue, onMode, onBinding, valueInputId, credential = false, projectSecrets = [] }: {
  variable: string
  /** The input looks like a credential: a project secret or data pool only. */
  credential?: boolean
  /** The project's secret names, to say when a credential reads one already there. */
  projectSecrets?: readonly string[]
  /** The value field's id, for a caller that labels or finds it. */
  valueInputId?: string
  value: string
  binding?: ImportInputBinding
  previous?: PreviousInput
  pools: SharedDataPool[]
  canBind: boolean
  disabled: boolean
  onValue: (value: string) => void
  onMode: (mode: SupplyMode) => void
  onBinding: (binding: ImportInputBinding) => void
}) {
  // Without a binding a credential is not supplied yet ('value' with no field).
  const mode: SupplyMode = binding?.mode ?? 'value'
  const secretReference = binding?.mode === 'secret' ? `{{secret.${binding.secret || 'NAME'}}}` : ''
  // A name the project already holds sends that secret's value; the earlier
  // import of these scripts choosing it is the operator's own choice.
  const readsExistingSecret = binding?.mode === 'secret' && !!binding.secret && projectSecrets.includes(binding.secret)
    && previous?.reference !== secretReference
  const pool = binding?.mode === 'data' ? pools.find(item => aliasForPool(item.name, []) === binding.alias) : undefined
  const fromLastImport = previous && (previous.mode === 'value'
    ? previous.value === value && mode === 'value'
    : previous.mode === mode && previous.alias === binding?.alias && previous.column === binding?.column && previous.generator === binding?.generator
      && (previous.generator !== 'digits' || previous.digits === binding?.digits)
      && (previous.mode !== 'secret' || previous.reference === `{{secret.${binding?.secret}}}`))
  return <div className="import-input-supply">
    {canBind && <label>Supply {variable}
      <select disabled={disabled} value={mode} onChange={event => onMode(event.target.value as SupplyMode)}>
        {credential
          ? <><option value="value">Not supplied yet</option><option value="secret">A project secret</option></>
          : <option value="value">One value for every user</option>}
        <option value="data">A different value per user, from a data pool</option>
        {!credential && <option value="generated">A value generated each iteration</option>}
      </select>
    </label>}
    {mode === 'secret' && binding && <>
      {/* Dots and hyphens are the secret store's aliases for underscores. */}
      <label>Project secret for {variable}<input disabled={disabled} value={binding.secret || ''} onChange={event => onBinding({ mode: 'secret', secret: event.target.value.toUpperCase().replace(/[.-]/g, '_') })} /></label>
      <small>Reads <code>{secretReference}</code>. Add the secret under Project secrets before a run; a credential is never kept in the script.</small>
      {!brunoInputBindingComplete(binding) && <small className="import-input-supply-error" role="alert">A project secret name uses capital letters, digits and underscores; {variable} is not supplied until it has one.</small>}
      {readsExistingSecret && <small className="import-input-supply-warning" role="status">The project already has {binding.secret}: these scripts will send its value. Name another secret if {variable} is a different credential.</small>}
    </>}
    {mode === 'value' && !credential && <label>Value for {variable}<input id={valueInputId} disabled={disabled} value={value} onChange={event => onValue(event.target.value)} /></label>}
    {mode === 'data' && binding && <>
      <label>Data pool for {variable}
        <select disabled={disabled} value={pool?.id || ''} onChange={event => {
          const chosen = pools.find(item => item.id === event.target.value)
          onBinding(chosen
            ? { mode: 'data', alias: aliasForPool(chosen.name, []), column: dataPoolColumnNames(chosen)[0] || '' }
            : { mode: 'data', alias: '', column: '' })
        }}>
          <option value="">{pools.length ? 'Name the alias and column myself' : 'No data pools in this project'}</option>
          {pools.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
        </select>
      </label>
      {pool
        ? <label>Column for {variable}
          <select disabled={disabled} value={binding.column || ''} onChange={event => onBinding({ ...binding, column: event.target.value })}>
            {dataPoolColumnNames(pool).map(column => <option key={column} value={column}>{column}</option>)}
          </select>
        </label>
        : <>
          <label>Alias for {variable}<input disabled={disabled} value={binding.alias || ''} onChange={event => onBinding({ ...binding, alias: event.target.value.trim() })} /></label>
          <label>Column for {variable}<input disabled={disabled} value={binding.column || ''} onChange={event => onBinding({ ...binding, column: event.target.value.trim() })} /></label>
        </>}
      <small>Reads <code>{`{{data.${binding.alias || 'alias'}.${binding.column || 'column'}}}`}</code>. Bind the alias to a pool in the scenario&apos;s Data step.</small>
    </>}
    {mode === 'generated' && binding && <>
      <label>Generate for {variable}
        <select disabled={disabled} value={binding.generator || 'uuid'} onChange={event => {
          const generator = event.target.value as NonNullable<ImportInputBinding['generator']>
          onBinding({ mode: 'generated', generator, ...(generator === 'digits' ? { digits: binding.digits || DEFAULT_DIGITS } : {}) })
        }}>
          <option value="uuid">A new UUID</option>
          <option value="timestamp">The current time (ISO 8601)</option>
          <option value="digits">Random digits</option>
        </select>
      </label>
      {binding.generator === 'digits' && <label>Digits in {variable}<input type="number" min={1} max={64} disabled={disabled} value={binding.digits ?? DEFAULT_DIGITS}
        onChange={event => onBinding({ ...binding, digits: Number(event.target.value) })} /></label>}
    </>}
    {fromLastImport && <small className="import-input-previous">As the last import of these scripts supplied it.</small>}
  </div>
}
