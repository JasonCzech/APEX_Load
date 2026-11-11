import { useMemo } from 'react'
import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptDefinition } from '../types/scripts'
import {
  correlationTableForEvent,
  correlationTableIsEmpty,
  unknownReadsText,
  type CorrelationRow,
} from './inspectorCorrelationModel'
import type { ReplayVariableDiff } from './requestResponseInspectorModel'
import './InspectorCorrelationTable.css'

export interface InspectorCorrelationTableProps {
  event: BrowserReplayEvent
  variableDiff: ReplayVariableDiff
  /**
   * The current script, only while the replay matches it: the table then
   * names each extractor's source and type, lists the request's {{var.…}}
   * reads and counts later uses. Null leaves those out.
   */
  script: ScriptDefinition | null
  /** Runtime evidence was withheld this run: a read is then never claimed Resolved. */
  runtimeVariablesUnavailable?: boolean
  /** Show the empty sentence when there is nothing to list (default true). */
  showEmptyState?: boolean
  /** Prefix for each row's copy key, e.g. `${index}:correlation`. */
  copyKeyPrefix: string
  copiedKey: string | null
  onCopy: (key: string, value: string) => void | Promise<void>
}

function OneLine({ text, className }: { text: string; className?: string }) {
  return <span className={`rr-correlation-one-line${className ? ` ${className}` : ''}`} title={text}>{text}</span>
}

function ValueCell({ row }: { row: CorrelationRow }) {
  const after = row.value === null
    ? <span className="rr-correlation-quiet">{row.missingValue || 'Unknown'}</span>
    : <OneLine text={row.value} />
  if (row.before === undefined) return after
  const before = row.before === null ? <span className="rr-correlation-quiet">unset</span> : <OneLine text={row.before} />
  return (
    <span className="rr-correlation-change">
      {before}
      <span className="rr-correlation-arrow" aria-hidden="true">{'→'}</span>
      <span className="sr-only">changed to</span>
      {after}
    </span>
  )
}

/** Copy, or a check once copied: the accessible name carries which. */
function CopyIcon({ copied }: { copied: boolean }) {
  return (
    <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" focusable="false">
      {copied
        ? <path d="M3.5 8.5l3 3 6-7" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" />
        : <>
          <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <path d="M10.5 3.5v-.5a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
        </>}
    </svg>
  )
}

const laterUseText = (count: number) => count === 0 ? 'Not used by later steps' : `Used by ${count} later ${count === 1 ? 'step' : 'steps'}`

/** What a replayed action extracts, did not apply, reads and changes, in one table. */
export default function InspectorCorrelationTable({
  event,
  variableDiff,
  script,
  runtimeVariablesUnavailable = false,
  showEmptyState = true,
  copyKeyPrefix,
  copiedKey,
  onCopy,
}: InspectorCorrelationTableProps) {
  const model = useMemo(
    () => correlationTableForEvent(event, variableDiff, script, runtimeVariablesUnavailable),
    [event, variableDiff, script, runtimeVariablesUnavailable],
  )

  if (correlationTableIsEmpty(model)) {
    return showEmptyState ? <p className="rr-inspector-empty">No extractions or variable changes were recorded for this action.</p> : null
  }

  return (
    <div className="rr-correlation">
      {model.notes.length > 0 && (
        <ul className="rr-correlation-notes">
          {model.notes.map(note => <li key={note.key} role={note.status ? 'status' : undefined}>{note.text}</li>)}
        </ul>
      )}
      {model.rows.length > 0 && (
        <div className="rr-correlation-frame">
          <table className="rr-correlation-table">
            <caption className="sr-only">Values this action extracts, reads and changes</caption>
            <thead>
              <tr>
                <th scope="col" className="rr-correlation-col-direction">Direction</th>
                <th scope="col" className="rr-correlation-col-variable">Variable</th>
                <th scope="col" className="rr-correlation-col-where">Where</th>
                <th scope="col" className="rr-correlation-col-value">Value (before → after)</th>
                <th scope="col" className="rr-correlation-col-result">Result</th>
                <th scope="col" className="rr-correlation-col-copy"><span className="sr-only">Copy</span></th>
              </tr>
            </thead>
            <tbody>
              {model.rows.map(row => {
                const copyKey = `${copyKeyPrefix}:${row.key}`
                const copied = copiedKey === copyKey
                return (
                  <tr key={row.key} className={row.tone === 'bad' ? 'rr-correlation-row-bad' : undefined}>
                    <td className="rr-correlation-direction">{row.direction}</td>
                    <th scope="row" className="rr-correlation-variable"><OneLine text={row.variable} /></th>
                    <td className="rr-correlation-where">
                      {row.where === null ? <span className="rr-correlation-quiet">Unknown</span> : <OneLine text={row.where} />}
                    </td>
                    <td className="rr-correlation-value"><ValueCell row={row} /></td>
                    <td>
                      <div className="rr-correlation-result">
                        <span className={`rr-correlation-badge rr-correlation-badge-${row.tone}`}>{row.result}</span>
                        {row.laterSteps !== undefined && <span className="rr-correlation-later">{laterUseText(row.laterSteps)}</span>}
                      </div>
                    </td>
                    <td className="rr-correlation-copy">
                      {row.copyValue !== undefined && (
                        <button
                          type="button"
                          className="btn btn-sm btn-ghost rr-correlation-copy-button"
                          aria-label={copied ? `Copied ${row.variable} value` : `Copy ${row.variable} value`}
                          title={copied ? 'Copied' : 'Copy value'}
                          onClick={() => { if (row.copyValue !== undefined) void onCopy(copyKey, row.copyValue) }}
                        >
                          <CopyIcon copied={copied} />
                        </button>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {model.unknownReads.length > 0 && <p className="rr-correlation-note rr-correlation-unknown-reads">{unknownReadsText(model.unknownReads)}</p>}
      {model.streamedChildren.length > 0 && (
        <div className="rr-correlation-children">
          <span className="replay-detail-label">Streamed children</span>
          <ul>
            {model.streamedChildren.map(child => (
              <li key={child.key}>
                {child.label} · {child.passed ? 'Passed' : 'Failed'}
                {child.error && <span className="rr-correlation-child-error">{child.error}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
      {model.streamedChildrenOmitted > 0 && (
        <p className="rr-correlation-note" role="status">
          {model.streamedChildrenOmitted} streamed child {model.streamedChildrenOmitted === 1 ? 'event was' : 'events were'} omitted from retained evidence.
        </p>
      )}
    </div>
  )
}
