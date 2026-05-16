import { Fragment, ReactNode, useEffect, useMemo, useState } from 'react'

import { detectCapturedBodyFormat, formatCapturedBody, type CapturedBodyFormat } from '../utils/snapshotFormatUtils'
import './SnapshotBodyPreview.css'

const MAX_PREVIEW_LINES = 14
const MAX_PREVIEW_CHARS = 1400

interface Props {
  body?: string
  headers?: Record<string, string>
  emptyMessage?: string
  preClassName?: string
  expanded?: boolean
  onExpandedChange?: (expanded: boolean) => void
}

function buildPreviewBody(value: string): string {
  const lines = value.split('\n')
  if (lines.length > MAX_PREVIEW_LINES) {
    return `${lines.slice(0, MAX_PREVIEW_LINES).join('\n')}\n…`
  }
  if (value.length > MAX_PREVIEW_CHARS) {
    return `${value.slice(0, MAX_PREVIEW_CHARS).trimEnd()}\n…`
  }
  return value
}

function shouldCollapseBody(value: string): boolean {
  return value.length > MAX_PREVIEW_CHARS || value.split('\n').length > MAX_PREVIEW_LINES
}

function renderJSONLine(line: string, lineKey: string): ReactNode[] {
  const tokenPattern = /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|[{}[\],:]/g
  const nodes: ReactNode[] = []
  let cursor = 0

  for (const match of line.matchAll(tokenPattern)) {
    const token = match[0]
    const index = match.index ?? 0
    if (index > cursor) {
      nodes.push(line.slice(cursor, index))
    }

    if (match[1]) {
      nodes.push(
        <span key={`${lineKey}-string-${index}`} className={match[2] ? 'snapshot-code-token-key' : 'snapshot-code-token-string'}>
          {match[1]}
        </span>,
      )
      if (match[2]) {
        const spacing = match[2].slice(0, -1)
        if (spacing) nodes.push(spacing)
        nodes.push(
          <span key={`${lineKey}-colon-${index}`} className="snapshot-code-token-punctuation">:</span>,
        )
      }
    } else if (match[3]) {
      nodes.push(
        <span key={`${lineKey}-literal-${index}`} className={token === 'null' ? 'snapshot-code-token-null' : 'snapshot-code-token-boolean'}>
          {token}
        </span>,
      )
    } else if (/^-?\d/.test(token)) {
      nodes.push(
        <span key={`${lineKey}-number-${index}`} className="snapshot-code-token-number">{token}</span>,
      )
    } else {
      nodes.push(
        <span key={`${lineKey}-punct-${index}`} className="snapshot-code-token-punctuation">{token}</span>,
      )
    }

    cursor = index + token.length
  }

  if (cursor < line.length) {
    nodes.push(line.slice(cursor))
  }

  return nodes
}

function renderXMLTag(tag: string, lineKey: string, tagIndex: number): ReactNode[] {
  if (/^<(!|\?)/.test(tag)) {
    return [<span key={`${lineKey}-meta-${tagIndex}`} className="snapshot-code-token-tag">{tag}</span>]
  }

  const nodes: ReactNode[] = []
  let inner = tag.slice(1, -1)
  const closing = inner.startsWith('/')
  if (closing) inner = inner.slice(1)
  const selfClosing = inner.endsWith('/')
  if (selfClosing) inner = inner.slice(0, -1)

  const tagNameMatch = inner.match(/^([^\s/>]+)/)
  if (!tagNameMatch) {
    return [tag]
  }

  const tagName = tagNameMatch[1]
  const attributes = inner.slice(tagName.length)
  nodes.push(
    <span key={`${lineKey}-open-${tagIndex}`} className="snapshot-code-token-punctuation">{closing ? '</' : '<'}</span>,
  )
  nodes.push(
    <span key={`${lineKey}-name-${tagIndex}`} className="snapshot-code-token-tag">{tagName}</span>,
  )

  const attrPattern = /(\s+)([^\s=]+)(\s*=\s*)("[^"]*"|'[^']*')/g
  let cursor = 0
  for (const match of attributes.matchAll(attrPattern)) {
    const index = match.index ?? 0
    if (index > cursor) {
      nodes.push(attributes.slice(cursor, index))
    }
    nodes.push(match[1])
    nodes.push(
      <span key={`${lineKey}-attr-name-${tagIndex}-${index}`} className="snapshot-code-token-attr-name">{match[2]}</span>,
    )
    nodes.push(
      <span key={`${lineKey}-attr-punct-${tagIndex}-${index}`} className="snapshot-code-token-punctuation">{match[3]}</span>,
    )
    nodes.push(
      <span key={`${lineKey}-attr-value-${tagIndex}-${index}`} className="snapshot-code-token-attr-value">{match[4]}</span>,
    )
    cursor = index + match[0].length
  }
  if (cursor < attributes.length) {
    nodes.push(attributes.slice(cursor))
  }
  if (selfClosing) {
    nodes.push(<span key={`${lineKey}-self-close-${tagIndex}`} className="snapshot-code-token-punctuation">/</span>)
  }
  nodes.push(
    <span key={`${lineKey}-close-${tagIndex}`} className="snapshot-code-token-punctuation">&gt;</span>,
  )
  return nodes
}

function renderXMLLine(line: string, lineKey: string): ReactNode[] {
  const segmentPattern = /<[^>]+>/g
  const nodes: ReactNode[] = []
  let cursor = 0

  for (const match of line.matchAll(segmentPattern)) {
    const token = match[0]
    const index = match.index ?? 0
    if (index > cursor) {
      nodes.push(
        <span key={`${lineKey}-text-${index}`} className="snapshot-code-token-text">{line.slice(cursor, index)}</span>,
      )
    }
    nodes.push(...renderXMLTag(token, lineKey, index))
    cursor = index + token.length
  }

  if (cursor < line.length) {
    nodes.push(
      <span key={`${lineKey}-tail`} className="snapshot-code-token-text">{line.slice(cursor)}</span>,
    )
  }

  return nodes
}

function renderHighlightedCode(text: string, format: CapturedBodyFormat): ReactNode {
  if (format === 'plain') {
    return text
  }

  const lines = text.split('\n')
  return lines.map((line, index) => (
    <Fragment key={`line-${index}`}>
      {format === 'json' ? renderJSONLine(line, `json-${index}`) : renderXMLLine(line, `xml-${index}`)}
      {index < lines.length - 1 ? '\n' : null}
    </Fragment>
  ))
}

export default function SnapshotBodyPreview({
  body,
  headers,
  emptyMessage = 'No body captured',
  preClassName,
  expanded,
  onExpandedChange,
}: Props) {
  const formattedBody = useMemo(() => {
    if (!body) return emptyMessage
    return formatCapturedBody(body, headers)
  }, [body, headers, emptyMessage])
  const bodyFormat = useMemo<CapturedBodyFormat>(() => {
    if (!body) return 'plain'
    return detectCapturedBodyFormat(body, headers)
  }, [body, headers])
  const [internalExpanded, setInternalExpanded] = useState(false)
  const [copied, setCopied] = useState(false)
  const isControlled = typeof expanded === 'boolean'
  const isExpanded = isControlled ? expanded : internalExpanded

  useEffect(() => {
    if (!isControlled) {
      setInternalExpanded(false)
    }
    setCopied(false)
  }, [formattedBody, isControlled])

  const canExpand = body ? shouldCollapseBody(formattedBody) : false
  const canCopy = Boolean(body)
  const copySupported = typeof navigator !== 'undefined' && typeof navigator.clipboard?.writeText === 'function'
  const visibleBody = isExpanded ? formattedBody : buildPreviewBody(formattedBody)

  const handleCopy = async () => {
    if (!body || !copySupported) return
    await navigator.clipboard.writeText(formattedBody)
    setCopied(true)
  }

  const handleToggle = () => {
    const next = !isExpanded
    if (!isControlled) {
      setInternalExpanded(next)
    }
    onExpandedChange?.(next)
  }

  return (
    <>
      <pre className={preClassName ? `${preClassName} snapshot-code` : 'snapshot-code'}>{renderHighlightedCode(visibleBody, bodyFormat)}</pre>
      {(canExpand || canCopy) && (
        <div className="snapshot-body-actions">
          {canCopy && (
            <button
              type="button"
              className="btn btn-sm btn-ghost snapshot-body-toggle"
              onClick={() => void handleCopy()}
              disabled={!copySupported}
            >
              {copied ? 'Copied' : 'Copy'}
            </button>
          )}
          {canExpand && (
            <button
              type="button"
              className="btn btn-sm btn-ghost snapshot-body-toggle"
              onClick={handleToggle}
            >
              {isExpanded ? 'Show less' : 'Show full body'}
            </button>
          )}
        </div>
      )}
    </>
  )
}