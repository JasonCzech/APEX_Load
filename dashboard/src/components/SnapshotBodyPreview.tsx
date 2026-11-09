import { useEffect, useMemo, useState } from 'react'

import { canCopyTextToClipboard, copyTextToClipboard } from '../utils/clipboardUtils'
import { detectCapturedBodyFormat, formatCapturedBody, type CapturedBodyFormat } from '../utils/snapshotFormatUtils'
import SnapshotFullDialog from './SnapshotFullDialog'
import { DebugLogHighlightedText, SnapshotHighlightedCode } from './SnapshotTextPreview'
import JsonViewer from './JsonViewer'
import './SnapshotBodyPreview.css'

export { DebugLogHighlightedText, SnapshotHighlightedCode }

const MAX_PREVIEW_LINES = 14
const MAX_PREVIEW_CHARS = 1400
const MAX_HIGHLIGHTED_BODY_CHARS = 128 * 1024
const MAX_HIGHLIGHTED_BODY_LINES = 4_000

interface Props {
  body?: string
  headers?: Record<string, string>
  emptyMessage?: string
  preClassName?: string
  expanded?: boolean
  autoExpandPaths?: string[]
  copyFieldName?: string
  onExpandedChange?: (expanded: boolean) => void
}

function findNamedJsonValue(value: unknown, fieldName: string): unknown {
  if (!value || typeof value !== 'object') return undefined

  if (!Array.isArray(value) && Object.prototype.hasOwnProperty.call(value, fieldName)) {
    return (value as Record<string, unknown>)[fieldName]
  }

  const children = Array.isArray(value) ? value : Object.values(value as Record<string, unknown>)
  for (const child of children) {
    const match = findNamedJsonValue(child, fieldName)
    if (match !== undefined) return match
  }
  return undefined
}

function formatCopiedJsonValue(value: unknown): string | undefined {
  if (value === undefined || value === null) return undefined
  if (typeof value === 'object') return JSON.stringify(value, null, 2)
  return String(value)
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

export default function SnapshotBodyPreview({
  body,
  headers,
  emptyMessage = 'No body captured',
  preClassName,
  expanded,
  autoExpandPaths,
  copyFieldName,
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
  const [copied, setCopied] = useState(false)
  const [copyFailed, setCopyFailed] = useState(false)
  const [copiedField, setCopiedField] = useState(false)
  const [copyFieldFailed, setCopyFieldFailed] = useState(false)
  const [fullDialogOpen, setFullDialogOpen] = useState(false)
  const isExplicitlyExpanded = expanded === true
  const isControlledCollapsed = expanded === false || (expanded === undefined && onExpandedChange !== undefined)
  const showFullInline = !isControlledCollapsed
  const largeBody = formattedBody.length > MAX_HIGHLIGHTED_BODY_CHARS
    || formattedBody.split('\n', MAX_HIGHLIGHTED_BODY_LINES + 1).length > MAX_HIGHLIGHTED_BODY_LINES

  const parsedJson = useMemo(() => {
    if (bodyFormat === 'json' && body) {
      try {
        return JSON.parse(body) as unknown
      } catch {
        return undefined
      }
    }
    return undefined
  }, [body, bodyFormat])
  const copyFieldValue = useMemo(
    () => copyFieldName ? formatCopiedJsonValue(findNamedJsonValue(parsedJson, copyFieldName)) : undefined,
    [copyFieldName, parsedJson],
  )

  useEffect(() => {
    setCopied(false)
    setCopyFailed(false)
    setCopiedField(false)
    setCopyFieldFailed(false)
  }, [formattedBody])

  const canOpenFull = Boolean(body)
  const canCopy = Boolean(body)
  const copySupported = canCopyTextToClipboard()
  const visibleBody = showFullInline ? formattedBody : buildPreviewBody(formattedBody)

  const handleCopy = async () => {
    if (!body || !copySupported) return false
    const copiedText = await copyTextToClipboard(formattedBody)
    setCopied(copiedText)
    setCopyFailed(!copiedText)
    if (!copiedText) {
      setFullDialogOpen(true)
    }
    return copiedText
  }

  const handleOpenFull = () => {
    setCopied(false)
    setCopyFailed(false)
    setFullDialogOpen(true)
  }

  const handleCopyField = async () => {
    if (copyFieldValue === undefined || !copySupported) return
    const copiedText = await copyTextToClipboard(copyFieldValue)
    setCopiedField(copiedText)
    setCopyFieldFailed(!copiedText)
  }

  const copyFieldButton = copyFieldName && copyFieldValue !== undefined ? (
    <button
      type="button"
      className="btn btn-sm btn-secondary"
      onClick={() => void handleCopyField()}
      disabled={!copySupported}
    >
      {copyFieldFailed ? `${copyFieldName} copy failed` : copiedField ? `${copyFieldName} copied` : `Copy ${copyFieldName}`}
    </button>
  ) : null

  return (
    <>
      {isExplicitlyExpanded && parsedJson !== undefined && !largeBody ? (
        <div className={preClassName ? `${preClassName} snapshot-code` : 'snapshot-code'} style={{ margin: 0, padding: 0 }}>
          <JsonViewer data={parsedJson} autoExpandPaths={autoExpandPaths} />
        </div>
      ) : (
        <pre className={preClassName ? `${preClassName} snapshot-code` : 'snapshot-code'}>
          <SnapshotHighlightedCode text={visibleBody} format={showFullInline && largeBody ? 'plain' : bodyFormat} highlightPlain={Boolean(body) && !(showFullInline && largeBody)} />
        </pre>
      )}
      {showFullInline && largeBody && <p className="snapshot-body-render-note">Syntax highlighting is disabled for this large body. Copy or open it to inspect the complete plain text.</p>}
      {(canOpenFull || canCopy) && (
        <div className="snapshot-body-actions">
          {copyFieldButton}
          {canCopy && (
            <button
              type="button"
              className="btn btn-sm btn-ghost snapshot-body-toggle"
              onClick={() => void handleCopy()}
              disabled={!copySupported}
            >
              {copyFailed ? 'Copy failed' : copied ? 'Copied' : 'Copy'}
            </button>
          )}
          {canOpenFull && (
            <button
              type="button"
              className="btn btn-sm btn-ghost snapshot-body-toggle"
              onClick={handleOpenFull}
            >
              Open full body
            </button>
          )}
        </div>
      )}
      <SnapshotFullDialog
        title="Full body"
        open={fullDialogOpen}
        copied={copied}
        copyFailed={copyFailed}
        copyDisabled={!copySupported || !canCopy}
        manualCopyText={formattedBody}
        additionalActions={copyFieldButton}
        onCopy={() => void handleCopy()}
        onClose={() => setFullDialogOpen(false)}
      >
        {parsedJson !== undefined && !largeBody ? (
          <div className={preClassName ? `${preClassName} snapshot-code` : 'snapshot-code'} style={{ flex: 1, margin: 0, padding: 0 }}>
            <JsonViewer data={parsedJson} defaultExpandedDepth={4} autoExpandPaths={autoExpandPaths} />
          </div>
        ) : (
          <>
            {largeBody && <p className="snapshot-body-render-note">Syntax highlighting is disabled for this large body.</p>}
            <pre className={preClassName ? `${preClassName} snapshot-code` : 'snapshot-code'}>
              <SnapshotHighlightedCode text={formattedBody} format={largeBody ? 'plain' : bodyFormat} highlightPlain={Boolean(body) && !largeBody} />
            </pre>
          </>
        )}
      </SnapshotFullDialog>
    </>
  )
}
