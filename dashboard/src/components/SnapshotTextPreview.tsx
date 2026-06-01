import { useEffect, useMemo, useState } from 'react'

import { canCopyTextToClipboard, copyTextToClipboard } from '../utils/clipboardUtils'
import SnapshotFullDialog from './SnapshotFullDialog'

const MAX_PREVIEW_LINES = 10
const MAX_PREVIEW_CHARS = 800

interface Props {
  text?: string
  emptyMessage?: string
  preClassName?: string
  expanded?: boolean
  onExpandedChange?: (expanded: boolean) => void
}

function buildPreviewText(value: string): string {
  const lines = value.split('\n')
  if (lines.length > MAX_PREVIEW_LINES) {
    return `${lines.slice(0, MAX_PREVIEW_LINES).join('\n')}\n…`
  }
  if (value.length > MAX_PREVIEW_CHARS) {
    return `${value.slice(0, MAX_PREVIEW_CHARS).trimEnd()}\n…`
  }
  return value
}

export default function SnapshotTextPreview({
  text,
  emptyMessage = 'n/a',
  preClassName,
  expanded,
}: Props) {
  const normalizedText = useMemo(() => text || emptyMessage, [text, emptyMessage])
  const [copied, setCopied] = useState(false)
  const [copyFailed, setCopyFailed] = useState(false)
  const [fullDialogOpen, setFullDialogOpen] = useState(false)
  const isExpanded = expanded === true

  useEffect(() => {
    setCopied(false)
    setCopyFailed(false)
    setFullDialogOpen(false)
  }, [normalizedText])

  const canOpenFull = Boolean(text)
  const canCopy = Boolean(text)
  const copySupported = canCopyTextToClipboard()
  const visibleText = isExpanded ? normalizedText : buildPreviewText(normalizedText)

  const handleCopy = async () => {
    if (!text || !copySupported) return false
    const copiedText = await copyTextToClipboard(normalizedText)
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

  return (
    <>
      <pre className={preClassName}>{visibleText}</pre>
      {(canOpenFull || canCopy) && (
        <div className="snapshot-body-actions">
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
              Open full text
            </button>
          )}
        </div>
      )}
      <SnapshotFullDialog
        title="Full text"
        open={fullDialogOpen}
        copied={copied}
        copyFailed={copyFailed}
        copyDisabled={!copySupported || !canCopy}
        manualCopyText={normalizedText}
        onCopy={() => void handleCopy()}
        onClose={() => setFullDialogOpen(false)}
      >
        <pre className={preClassName}>{normalizedText}</pre>
      </SnapshotFullDialog>
    </>
  )
}
