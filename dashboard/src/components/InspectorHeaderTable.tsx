import { useId, useMemo, useState } from 'react'
import {
  inspectorFormFieldRows,
  inspectorHeaderRows,
  inspectorKeyHeaderRows,
  maskedFieldNote,
  withRequestHeaderSources,
  type InspectorHeaderDirection,
  type InspectorSourceContext,
  type InspectorValueSource,
} from './inspectorHeadersModel'
import './InspectorHeaderTable.css'

interface CopyProps {
  /** Prefix for each row's copy key, e.g. `${index}:request-headers`. */
  copyKeyPrefix: string
  copiedKey: string | null
  onCopy: (key: string, value: string) => void | Promise<void>
}

export interface InspectorHeaderTableProps extends CopyProps {
  direction: InspectorHeaderDirection
  headers: Readonly<Record<string, string>> | null | undefined
  /** Request headers only: adds the "Comes from" column (when the authored step is known). */
  sources?: InspectorSourceContext
  /** Names the table for assistive technology, e.g. "Request headers sent on the wire". */
  label: string
}


function SourceCell({ source }: { source: InspectorValueSource }) {
  return <td className={`inspector-table-source is-${source.kind}`} title={source.detail}>{source.label}</td>
}

/** A masked value: bullets for the eye, "Masked" for assistive technology. */
function ValueCell({ value, masked }: { value: string, masked: boolean }) {
  if (!masked) return <td className="inspector-table-value" title={value}>{value}</td>
  return (
    <td className="inspector-table-value is-masked" title="Masked">
      <span aria-hidden="true">{value}</span>
      <span className="sr-only">Masked</span>
    </td>
  )
}

/** Copy for a value shown here; a masked value offers none, only the word. */
function CopyCell({ copyKey, value, name, masked, copiedKey, onCopy }: { copyKey: string, value: string, name: string, masked: boolean } & Omit<CopyProps, 'copyKeyPrefix'>) {
  if (masked) {
    return <td className="inspector-table-action"><span className="inspector-table-masked-note" aria-hidden="true">Masked</span></td>
  }
  const copied = copiedKey === copyKey
  return (
    <td className="inspector-table-action">
      <button
        type="button"
        className="btn btn-sm btn-ghost inspector-table-copy"
        aria-label={copied ? `Copied ${name}` : `Copy ${name}`}
        onClick={() => { void onCopy(copyKey, value) }}
      >
        {copied ? 'Copied' : 'Copy'}
      </button>
    </td>
  )
}

/**
 * Header | Value (| Comes from) table. Key headers come first; when there are
 * others, the Show all toggle lists every header. Long values stay on one line with
 * the whole value in the tooltip and behind Copy.
 */
export function InspectorHeaderTable({ direction, headers, sources, label, copyKeyPrefix, copiedKey, onCopy }: InspectorHeaderTableProps) {
  const tableId = useId()
  const [showAll, setShowAll] = useState(false)
  const rows = useMemo(() => {
    const base = inspectorHeaderRows(headers, direction)
    return direction === 'request' && sources ? withRequestHeaderSources(base, sources) : base
  }, [headers, direction, sources])
  const keyRows = inspectorKeyHeaderRows(rows)
  const visible = keyRows && !showAll ? keyRows : rows
  const withSource = direction === 'request' && Boolean(sources?.authored)
  if (rows.length === 0) {
    return <p className="inspector-table-empty">No {direction} headers captured.</p>
  }
  return (
    <div className="inspector-table-block">
      <div className="inspector-table-head">
        <span className="inspector-table-title">Headers <span className="inspector-table-count">{rows.length}</span></span>
        {keyRows && (
          <button
            type="button"
            className="link-button inspector-table-toggle"
            aria-expanded={showAll}
            aria-controls={tableId}
            onClick={() => setShowAll(value => !value)}
          >
            {showAll ? 'Show key headers' : `Show all ${rows.length}`}
          </button>
        )}
      </div>
      <table id={tableId} className={`inspector-table${withSource ? ' has-source' : ''}`} aria-label={label}>
        <thead>
          <tr>
            <th scope="col">Header</th>
            <th scope="col">Value</th>
            {withSource && <th scope="col">Comes from</th>}
            <th scope="col"><span className="sr-only">Copy</span></th>
          </tr>
        </thead>
        <tbody>
          {visible.map(row => {
            const position = rows.indexOf(row)
            const copyKey = `${copyKeyPrefix}:${position}`
            return (
              <tr key={copyKey} className={row.masked ? 'is-masked' : undefined}>
                <th scope="row" className="inspector-table-name" title={row.name}>{row.name}</th>
                <ValueCell value={row.value} masked={row.masked} />
                {withSource && row.source && <SourceCell source={row.source} />}
                <CopyCell copyKey={copyKey} value={row.value} name={`${row.name} header`} masked={row.masked} copiedKey={copiedKey} onCopy={onCopy} />
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

export interface InspectorFormFieldTableProps extends CopyProps {
  body: string | null | undefined
  /** Whether the body is a form (requestBodyIsForm); the table renders only for one. */
  form: boolean
  sources: InspectorSourceContext
  label: string
  /**
   * The copies on screen with this table that keep masked fields (they exist
   * to reproduce the exact request), e.g. ['Copy full request', 'Copy as cURL'].
   * The note names them rather than implying the fields are protected.
   */
  unmaskedCopies?: readonly string[]
}


/**
 * Field | Value | Comes from table for a form body; a form body with no
 * readable field says so instead of showing the raw text. Password-like
 * fields are masked and offer no Copy.
 */
export function InspectorFormFieldTable({ body, form, sources, label, copyKeyPrefix, copiedKey, onCopy, unmaskedCopies = [] }: InspectorFormFieldTableProps) {
  const rows = useMemo(() => inspectorFormFieldRows(body, form, sources), [body, form, sources])
  if (!form || !body) return null
  if (!rows) return <p className="inspector-table-empty">No form fields could be read from this body.</p>
  const maskedCount = rows.filter(row => row.masked).length
  const withSource = Boolean(sources.authored)
  return (
    <div className="inspector-table-block">
      <div className="inspector-table-head">
        <span className="inspector-table-title">Form fields <span className="inspector-table-count">{rows.length}</span></span>
        {maskedCount > 0 && <span className="inspector-table-note">{maskedFieldNote(maskedCount, unmaskedCopies)}</span>}
      </div>
      <table className={`inspector-table${withSource ? ' has-source' : ''}`} aria-label={label}>
        <thead>
          <tr>
            <th scope="col">Field</th>
            <th scope="col">Value</th>
            {withSource && <th scope="col">Comes from</th>}
            <th scope="col"><span className="sr-only">Copy</span></th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row, position) => {
            const copyKey = `${copyKeyPrefix}:${position}`
            return (
              <tr key={copyKey} className={row.masked ? 'is-masked' : undefined}>
                <th scope="row" className="inspector-table-name" title={row.name}>{row.name}</th>
                <ValueCell value={row.value} masked={row.masked} />
                {withSource && <SourceCell source={row.source} />}
                <CopyCell copyKey={copyKey} value={row.value} name={`field ${row.name}`} masked={row.masked} copiedKey={copiedKey} onCopy={onCopy} />
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

export default InspectorHeaderTable
