import { useId } from 'react'

import type { ScriptActionDefinition as Action, ScriptAssertionDefinition as Assertion, ScriptExtractorDefinition as Extractor } from '../types/scripts'
import { checkKindOf, checkKinds, checkResult, savedValueResult, statusExpectation, type CheckField, type CheckResult, type ChecksReplay } from './actionChecksModel'
import { describeExtractorDFEs, describeExtractorScope } from './extractorScope'
import { boundaryExpression, parseBoundaryExpression } from './extractorMatch'
import { TEXT_ASSERTION_SOURCES, displayedExtractorType, extractorExpressionPlaceholder, extractorSourceOptions, extractorTypeOptions, withCurrentOption } from './extractorOptions'
import ReferenceField from './ReferenceField'
import './ActionChecks.css'

function Result({ result }: { result: CheckResult }) {
  return <span className={`action-check-result ${result.state}`}>{result.detail}</span>
}

/**
 * A step's saved values and checks: added by what they test, edited in
 * labelled fields, each with its result against the last replay's response.
 */
export default function ActionChecks({ action, index, protocol, isBrowserScript, replay, unavailable = '', onAddExtractor, onUpdateExtractor, onRemoveExtractor, onAddAssertion, onUpdateAssertion, onRemoveAssertion }: {
  action: Action
  index: number
  protocol: string
  isBrowserScript: boolean
  /** The step's last replay, when it has one. */
  replay?: ChecksReplay | null
  /** Why the script's last replay has no result for this step, or ''. */
  unavailable?: string
  onAddExtractor: (i: number) => void
  onUpdateExtractor: (ai: number, ei: number, f: string, v: string) => void
  onRemoveExtractor: (ai: number, ei: number) => void
  onAddAssertion: (i: number, assertion?: Assertion) => void
  onUpdateAssertion: (ai: number, ci: number, f: keyof Assertion, v: unknown) => void
  onRemoveAssertion: (ai: number, ci: number) => void
}) {
  const id = useId()
  const kinds = checkKinds(protocol)
  const add = (type: string) => {
    const kind = kinds.find(candidate => candidate.type === type)
    if (kind) onAddAssertion(index, kind.create(replay?.event))
  }
  const noReplay: CheckResult = { state: 'unchecked', detail: unavailable ? 'No result' : 'Not replayed yet' }
  return <div className="action-checks">
    <p className="action-checks-replay">{replay
      ? `Results are from the last replay of this step${replay.stale ? ', before your latest edits' : ''}. Edits are checked again against the response it captured.`
      : unavailable || 'Not replayed yet: debug the script to see each result here.'}</p>
    {!isBrowserScript && <section className="action-checks-list" aria-labelledby={`${id}-values`}>
      <h4 id={`${id}-values`}>Saved values</h4>
      {!(action.extractors || []).length && <p className="action-checks-empty">None. A saved value is read by later steps as <code>{'{{var.name}}'}</code>.</p>}
      {(action.extractors || []).map((extractor, at) => <SavedValue key={at} extractor={extractor} at={at} protocol={protocol} result={replay ? savedValueResult(extractor, replay) : noReplay} onUpdate={(field, value) => onUpdateExtractor(index, at, field, value)} onRemove={() => onRemoveExtractor(index, at)} />)}
      <button type="button" className="btn btn-sm btn-secondary" onClick={() => onAddExtractor(index)}>Save a value for later steps</button>
    </section>}
    <section className="action-checks-list" aria-labelledby={`${id}-checks`} data-action-section="assertions">
      <h4 id={`${id}-checks`}>Checks</h4>
      {!(action.assertions || []).length && <p className="action-checks-empty">None. A check fails the step when the response is not what it expects.</p>}
      {(action.assertions || []).map((assertion, at) => <Check key={at} assertion={assertion} at={at} index={index} protocol={protocol} result={replay ? checkResult(assertion, replay) : noReplay} onUpdate={(field, value) => onUpdateAssertion(index, at, field, value)} onRemove={() => onRemoveAssertion(index, at)} />)}
      <label className="action-checks-add">Add a check
        <select value="" onChange={event => { add(event.target.value); event.currentTarget.value = '' }}>
          <option value="" disabled>Choose what to check…</option>
          {kinds.filter(kind => !kind.more).map(kind => <option key={kind.type} value={kind.type}>{kind.question}</option>)}
          {kinds.some(kind => kind.more) && <optgroup label="More checks">{kinds.filter(kind => kind.more).map(kind => <option key={kind.type} value={kind.type}>{kind.question}</option>)}</optgroup>}
        </select>
      </label>
    </section>
  </div>
}

function SavedValue({ extractor, at, protocol, result, onUpdate, onRemove }: { extractor: Extractor; at: number; protocol: string; result: CheckResult; onUpdate: (field: string, value: string) => void; onRemove: () => void }) {
  const type = displayedExtractorType(extractor.source, extractor.type)
  // Plain left|||right boundaries edit as two fields; the configured form stays as written.
  const boundary = type === 'boundary' && !extractor.expression.startsWith('__') ? parseBoundaryExpression(extractor.expression || '|||') : null
  const name = extractor.name || `value ${at + 1}`
  // Each field is named for its row, as several saved values share the labels.
  const field = (label: string) => `Saved value ${at + 1} ${label.toLowerCase()}`
  const expressionLabel = extractor.source === 'header' && !type ? 'Header' : 'Expression'
  return <div className="action-check-row" role="group" aria-label={`Saved value ${name}`}>
    <div className="action-check-fields">
      <label>Save as<input aria-label={field('Save as')} value={extractor.name} onChange={event => onUpdate('name', event.target.value)} /></label>
      <label>From<select aria-label={field('From')} value={extractor.source} onChange={event => onUpdate('source', event.target.value)}>
        {withCurrentOption(extractorSourceOptions(protocol), extractor.source).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </select></label>
      <label>How<select aria-label={field('How')} value={type} onChange={event => onUpdate('type', event.target.value)}>
        {withCurrentOption(extractorTypeOptions(protocol, extractor.source), type).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </select></label>
      {boundary ? <>
        <label>Left boundary<input aria-label={field('Left boundary')} value={boundary.left} onChange={event => onUpdate('expression', boundaryExpression(event.target.value, boundary.right))} /></label>
        <label>Right boundary<input aria-label={field('Right boundary')} value={boundary.right} onChange={event => onUpdate('expression', boundaryExpression(boundary.left, event.target.value))} /></label>
      </> : <label className="action-check-wide">{expressionLabel}<input aria-label={field(expressionLabel)} value={extractor.expression} placeholder={extractorExpressionPlaceholder(protocol, extractor.source)} onChange={event => onUpdate('expression', event.target.value)} /></label>}
      <button type="button" className="btn-icon danger" aria-label={`Remove saved value ${name}`} onClick={onRemove}>✕</button>
    </div>
    <Result result={result} />
    {extractor.scope && <p className="action-checks-note">{describeExtractorScope(extractor.scope)}</p>}
    {extractor.dfes && extractor.dfes.length > 0 && <p className="action-checks-note">{describeExtractorDFEs(extractor.dfes)}</p>}
  </div>
}

function Check({ assertion, at, index, protocol, result, onUpdate, onRemove }: { assertion: Assertion; at: number; index: number; protocol: string; result: CheckResult; onUpdate: (field: keyof Assertion, value: unknown) => void; onRemove: () => void }) {
  const kind = checkKindOf(protocol, assertion)
  const label = `Check ${at + 1}`
  const field = (spec: CheckField) => {
    // A page title the engine reads from expected when value is empty.
    const titleInExpected = assertion.type === 'page_title' && spec.key === 'value' && !assertion.value && assertion.expected !== undefined
    const current = titleInExpected ? assertion.expected : assertion[spec.key]
    const text = current === undefined || current === null ? '' : String(current)
    const path = ['assertions', at, spec.key]
    // Each field is named for its check, as several checks share the labels.
    const name = `${label} ${spec.label.toLowerCase()}`
    // A number in the script (a status) is not text a parameter can stand in for.
    const makesParameter = typeof current !== 'number'
    switch (spec.input) {
      case 'reference':
        return <label key={spec.key} className="action-check-reference" data-check-field={spec.key}>{spec.label}<ReferenceField value={text} onChange={value => { onUpdate(spec.key, value); if (titleInExpected) onUpdate('expected', undefined) }} field={path} label={name} makesParameter={makesParameter} /></label>
      case 'status':
        // A number, or a reference that resolves to one.
        return <label key={spec.key} className="action-check-reference" data-check-field={spec.key}>{spec.label}<ReferenceField value={text} onChange={value => onUpdate(spec.key, statusExpectation(value))} field={path} label={name} makesParameter={makesParameter} inputProps={{ inputMode: 'numeric' }} /></label>
      case 'number':
        return <label key={spec.key} data-check-field={spec.key}>{spec.label}<input aria-label={name} type="number" min={0} value={text} onChange={event => onUpdate(spec.key, event.target.value === '' ? undefined : Number(event.target.value))} /></label>
      case 'source':
        return <label key={spec.key} data-check-field={spec.key}>{spec.label}<select aria-label={name} value={assertion.source === 'body' ? '' : assertion.source ?? ''} onChange={event => onUpdate('source', event.target.value || undefined)}>
          {TEXT_ASSERTION_SOURCES.map(([value, sourceName]) => <option key={value} value={value}>{sourceName}</option>)}
        </select></label>
      case 'checkbox':
        return <label key={spec.key} className="checkbox-inline" data-check-field={spec.key}><input aria-label={name} type="checkbox" checked={Boolean(current)} onChange={event => onUpdate(spec.key, event.target.checked || undefined)} />{spec.label}</label>
      default:
        return <label key={spec.key} data-check-field={spec.key}>{spec.label}<input aria-label={name} value={text} placeholder={spec.placeholder} onChange={event => onUpdate(spec.key, event.target.value)} /></label>
    }
  }
  return <div className="action-check-row" role="group" aria-label={`${label}: ${kind.question}`} data-check-index={`${index}:${at}`}>
    <div className="action-check-fields">
      <span className="action-check-question">{kind.question}</span>
      {kind.fields.map(field)}
      <button type="button" className="btn-icon danger" aria-label={`Remove check ${at + 1}`} onClick={onRemove}>✕</button>
    </div>
    <Result result={result} />
  </div>
}
