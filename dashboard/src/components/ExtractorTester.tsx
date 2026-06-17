import { useMemo, useState } from 'react'
import type { ScriptExtractorDefinition, ScriptReplayActionEvent } from '../types/scripts'
import { evaluateExtractor } from './extractorMatch'

interface ExtractorTesterProps {
  event: ScriptReplayActionEvent
  /** Index into script.actions, or null when the event is not an editable action-phase step. */
  actionIndex: number | null
  onAddExtractor: (actionIndex: number, extractor: ScriptExtractorDefinition) => void
}

type SourceKey = 'body' | 'header'

const TYPE_OPTIONS: Array<[string, string]> = [
  ['regex', 'Regex'],
  ['jsonpath', 'JSONPath'],
  ['boundary', 'Boundary'],
  ['xpath', 'XPath'],
  ['jmespath', 'JMESPath'],
  ['css', 'CSS'],
]

function formatHeaders(headers?: Record<string, string>): string {
  if (!headers) return ''
  return Object.entries(headers).map(([key, value]) => `${key}: ${value}`).join('\n')
}

function truncate(value: string, max = 140): string {
  return value.length > max ? `${value.slice(0, max)}…` : value
}

// A quick correlation aid: try an extractor expression against the captured
// response for the selected step, see matches live, and add it to the action.
export function ExtractorTester({ event, actionIndex, onAddExtractor }: ExtractorTesterProps) {
  const [source, setSource] = useState<SourceKey>('body')
  const [type, setType] = useState('regex')
  const [expression, setExpression] = useState('')
  const [name, setName] = useState('')
  const [added, setAdded] = useState<string | null>(null)

  const responseBody = event.snapshot?.response_body ?? ''
  const responseHeaders = formatHeaders(event.snapshot?.response_headers ?? event.headers)
  const sourceText = source === 'body' ? responseBody : responseHeaders

  const result = useMemo(() => evaluateExtractor(type, expression, sourceText), [type, expression, sourceText])

  const canAdd = expression.trim().length > 0 && actionIndex !== null
  const handleAdd = () => {
    if (actionIndex === null) return
    const extractor: ScriptExtractorDefinition = {
      name: name.trim() || 'extracted_value',
      source,
      type,
      expression: expression.trim(),
    }
    onAddExtractor(actionIndex, extractor)
    setAdded(extractor.name)
  }

  return (
    <div className="replay-detail-block extractor-tester" aria-label="Extractor tester">
      <span className="replay-detail-label">Extractor tester</span>
      <p className="extractor-tester-hint">Try an expression against this step’s captured response, then add it to the action.</p>
      <div className="extractor-tester-controls">
        <label>
          <span>Source</span>
          <select value={source} onChange={event => { setSource(event.target.value as SourceKey); setAdded(null) }}>
            <option value="body">Response body</option>
            <option value="header">Response headers</option>
          </select>
        </label>
        <label>
          <span>Type</span>
          <select value={type} onChange={event => { setType(event.target.value); setAdded(null) }}>
            {TYPE_OPTIONS.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
        </label>
        <label className="extractor-tester-name">
          <span>Variable</span>
          <input value={name} onChange={event => { setName(event.target.value); setAdded(null) }} placeholder="extracted_value" />
        </label>
      </div>
      <input
        className="extractor-tester-expression"
        value={expression}
        onChange={event => { setExpression(event.target.value); setAdded(null) }}
        placeholder={type === 'jsonpath' ? '$.data.token' : type === 'regex' ? 'name="csrf" value="([^"]+)"' : 'expression'}
        aria-label="Extractor expression"
      />
      <div className="extractor-tester-result" role="status">
        {!result.previewable ? (
          <span className="extractor-tester-note">No live preview for {type} — it’s evaluated at replay.</span>
        ) : result.error ? (
          <span className="extractor-tester-error">{result.error}</span>
        ) : !expression.trim() ? null : result.matches.length === 0 ? (
          <span className="extractor-tester-note">No matches in this response.</span>
        ) : (
          <>
            <span className="extractor-tester-count">{result.matches.length} match{result.matches.length === 1 ? '' : 'es'}</span>
            <ul className="extractor-tester-matches">
              {result.matches.slice(0, 5).map((match, index) => (
                <li key={index}><code>{truncate(match)}</code></li>
              ))}
              {result.matches.length > 5 && <li className="extractor-tester-note">+{result.matches.length - 5} more</li>}
            </ul>
          </>
        )}
      </div>
      <div className="extractor-tester-actions">
        <button type="button" className="btn btn-sm btn-secondary" onClick={handleAdd} disabled={!canAdd}>
          Add as extractor
        </button>
        {actionIndex === null && <span className="extractor-tester-note">Select an action step to add an extractor.</span>}
        {added && <span className="extractor-tester-added">Added “{added}” to this action.</span>}
      </div>
    </div>
  )
}
