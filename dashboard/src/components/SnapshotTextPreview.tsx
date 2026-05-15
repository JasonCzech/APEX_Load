import { useEffect, useMemo, useState } from 'react'

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

function shouldCollapseText(value: string): boolean {
  return value.length > MAX_PREVIEW_CHARS || value.split('\n').length > MAX_PREVIEW_LINES
}

export default function SnapshotTextPreview({
  text,
  emptyMessage = 'n/a',
  preClassName,
  expanded,
  onExpandedChange,
}: Props) {
  const normalizedText = useMemo(() => text || emptyMessage, [text, emptyMessage])
  const [internalExpanded, setInternalExpanded] = useState(false)
  const [copied, setCopied] = useState(false)
  const isControlled = typeof expanded === 'boolean'
  const isExpanded = isControlled ? expanded : internalExpanded

  useEffect(() => {
    if (!isControlled) {
      setInternalExpanded(false)
    }
    setCopied(false)
  }, [normalizedText, isControlled])

  const canExpand = text ? shouldCollapseText(normalizedText) : false
  const canCopy = Boolean(text)
  const copySupported = typeof navigator !== 'undefined' && typeof navigator.clipboard?.writeText === 'function'
  const visibleText = isExpanded ? normalizedText : buildPreviewText(normalizedText)

  const handleCopy = async () => {
    if (!text || !copySupported) return
    await navigator.clipboard.writeText(normalizedText)
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
      <pre className={preClassName}>{visibleText}</pre>
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
              {isExpanded ? 'Show less' : 'Show full text'}
            </button>
          )}
        </div>
      )}
    </>
  )
}