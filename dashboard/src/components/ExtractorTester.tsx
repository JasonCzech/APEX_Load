import { useMemo, useState } from 'react'
import type { ScriptExtractorDefinition, ScriptReplayActionEvent } from '../types/scripts'
import { evaluateExtractor, type ExtractorMatchResult } from './extractorMatch'
import { headersAndBodySurface, setCookieSurface } from './extractorSurface'

interface ExtractorTesterProps {
  event: ScriptReplayActionEvent
  /** Index into script.actions, or null when the event is not an editable action-phase step. */
  actionIndex: number | null
  onAddExtractor: (actionIndex: number, extractor: ScriptExtractorDefinition) => void
  /**
   * Starting values (for example the extractor that failed on this step). They
   * are read when the tester mounts: the caller keys it per event, so another
   * event starts afresh while edits on the same event are kept.
   */
  initialDraft?: Partial<Pick<ScriptExtractorDefinition, 'source' | 'type' | 'expression' | 'name'>>
  /**
   * Replaces the step's extractor named initialDraft.name with the tester's
   * source, type and expression (its name and other settings are kept):
   * true when it did, else why not (a fragment that follows "Could not
   * update “NAME”: "). When set, the tester offers Update in place of Add:
   * adding beside the failing extractor would leave it failing.
   */
  onUpdateExtractor?: (extractor: Pick<ScriptExtractorDefinition, 'source' | 'type' | 'expression'>) => true | string
  /** Called whenever the user changes a field, so a caller can tell an untouched tester from an edited one. */
  onDraftChange?: () => void
}

type SourceKey = 'body' | 'header' | 'all' | 'cookies'

const SOURCE_KEYS: readonly string[] = ['body', 'header', 'all', 'cookies']

/** The tester's source for a definition's: "" means body (callers do not seed a surface it cannot show, such as url or status). */
function initialSource(source: string | undefined): SourceKey {
  return source && SOURCE_KEYS.includes(source) ? source as SourceKey : 'body'
}

/** The definition's type, or the engine's default for its source: a header source without one is a direct header lookup. */
function initialType(source: SourceKey, type: string | undefined): string {
  return type?.trim() || (source === 'header' ? 'header' : 'regex')
}

// A header source's direct lookup by name (the engine's default for it).
const HEADER_LOOKUP_OPTION: [string, string] = ['header', 'Header value']

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

/** The engine's direct header lookup: the non-empty values of the headers with this name, in any case. */
function headerLookup(headers: Record<string, string> | undefined, name: string): ExtractorMatchResult {
  const wanted = name.trim().toLowerCase()
  if (!wanted) return { matches: [], previewable: true, error: 'Enter a header name.' }
  const matches = Object.entries(headers || {})
    .filter(([key, value]) => key.toLowerCase() === wanted && String(value ?? '') !== '')
    .map(([, value]) => String(value))
  return { matches, previewable: true }
}

function truncate(value: string, max = 140): string {
  return value.length > max ? `${value.slice(0, max)}…` : value
}

// A quick correlation aid: try an extractor expression against the captured
// response for the selected step, see matches live, and add it to the action
// (or, for the step's failing extractor, update that one in place).
export function ExtractorTester({ event, actionIndex, onAddExtractor, initialDraft, onUpdateExtractor, onDraftChange }: ExtractorTesterProps) {
  const [source, setSource] = useState<SourceKey>(() => initialSource(initialDraft?.source))
  const [type, setType] = useState(() => initialType(initialSource(initialDraft?.source), initialDraft?.type))
  const [expression, setExpression] = useState(() => initialDraft?.expression ?? '')
  const [name, setName] = useState(() => initialDraft?.name ?? '')
  // The seeded extractor is updated in place, under its own name.
  const updatesName = onUpdateExtractor && initialDraft?.name ? initialDraft.name : null
  // A header source offers its direct lookup; a definition's type the list
  // does not offer (json, regex_count…) stays selectable.
  const baseOptions: Array<[string, string]> = source === 'header' ? [HEADER_LOOKUP_OPTION, ...TYPE_OPTIONS] : TYPE_OPTIONS
  const typeOptions: Array<[string, string]> = baseOptions.some(([value]) => value === type) ? baseOptions : [...baseOptions, [type, type]]
  const [added, setAdded] = useState<string | null>(null)
  // 'updated', or why the update was refused.
  const [updated, setUpdated] = useState<{ ok: true } | { ok: false; reason: string } | null>(null)
  const clearOutcome = () => {
    setAdded(null)
    setUpdated(null)
    onDraftChange?.()
  }

  const responseBody = event.snapshot?.response_body ?? ''
  const responseHeaders = formatHeaders(event.snapshot?.response_headers ?? event.headers)
  // "all" mirrors the runtime surface: header lines ending in CRLF, a blank
  // line, then the body (LoadRunner's default search scope).
  const capturedHeaders = event.snapshot?.response_headers ?? event.headers
  const sourceText = source === 'body'
    ? responseBody
    : source === 'all'
      ? `${headersAndBodySurface(capturedHeaders)}${responseBody}`
      : source === 'cookies'
        ? setCookieSurface(capturedHeaders)
        : responseHeaders

  const result = useMemo(() => source === 'header' && type === 'header'
    ? headerLookup(capturedHeaders, expression)
    : evaluateExtractor(type, expression, sourceText), [capturedHeaders, source, type, expression, sourceText])

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
  const handleUpdate = () => {
    if (!onUpdateExtractor || !expression.trim()) return
    const outcome = onUpdateExtractor({ source, type, expression: expression.trim() })
    setUpdated(outcome === true ? { ok: true } : { ok: false, reason: outcome })
  }

  return (
    <div className="replay-detail-block extractor-tester" aria-label="Extractor tester">
      <span className="replay-detail-label">Extractor tester</span>
      <p className="extractor-tester-hint">
        {updatesName !== null
          ? 'Try an expression against this step’s captured response, then update the extractor with it.'
          : 'Try an expression against this step’s captured response, then add it to the action.'}
      </p>
      <div className="extractor-tester-controls">
        <label>
          <span>Source</span>
          <select value={source} onChange={event => { setSource(event.target.value as SourceKey); clearOutcome() }}>
            <option value="body">Response body</option>
            <option value="header">Response headers</option>
            <option value="all">Headers and body</option>
            <option value="cookies">Set-Cookie lines</option>
          </select>
        </label>
        <label>
          <span>Type</span>
          <select value={type} onChange={event => { setType(event.target.value); clearOutcome() }}>
            {typeOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
        </label>
        <label className="extractor-tester-name">
          <span>Variable</span>
          {/* Update keeps the extractor's name: later steps refer to it. */}
          <input value={name} readOnly={updatesName !== null} onChange={event => { setName(event.target.value); clearOutcome() }} placeholder="extracted_value" />
        </label>
      </div>
      <input
        className="extractor-tester-expression"
        value={expression}
        onChange={event => { setExpression(event.target.value); clearOutcome() }}
        placeholder={type === 'jsonpath' ? '$.data.token' : type === 'regex' ? 'name="csrf" value="([^"]+)"' : type === 'header' ? 'X-CSRF-Token' : 'expression'}
        aria-label="Extractor expression"
      />
      <div className="extractor-tester-result" role="status">
        {!result.previewable ? (
          <span className="extractor-tester-note">{result.note ?? `No live preview for ${type} — it’s evaluated at replay.`}</span>
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
        {updatesName !== null ? (
          <>
            <button type="button" className="btn btn-sm btn-primary" onClick={handleUpdate} disabled={!expression.trim()}>
              Update extractor {updatesName}
            </button>
            {updated?.ok === true && <span className="extractor-tester-added">Updated “{updatesName}” on this step. Replay to check it.</span>}
            {updated?.ok === false && <span className="extractor-tester-error">Could not update “{updatesName}”: {updated.reason}</span>}
          </>
        ) : (
          <>
            <button type="button" className="btn btn-sm btn-secondary" onClick={handleAdd} disabled={!canAdd}>
              Add as extractor
            </button>
            {actionIndex === null && <span className="extractor-tester-note">Select an action step to add an extractor.</span>}
            {added && <span className="extractor-tester-added">Added “{added}” to this action.</span>}
          </>
        )}
      </div>
    </div>
  )
}
