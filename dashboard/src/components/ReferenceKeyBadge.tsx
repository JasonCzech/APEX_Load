import { useEffect, useState, type MouseEvent } from 'react'

import { canCopyTextToClipboard, copyTextToClipboard } from '../utils/clipboardUtils'

interface Props {
  value?: string
  className?: string
}

export default function ReferenceKeyBadge({ value, className = '' }: Props) {
  const refKey = value?.trim()
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) return undefined
    const timer = window.setTimeout(() => setCopied(false), 1200)
    return () => window.clearTimeout(timer)
  }, [copied])

  if (!refKey) {
    return null
  }

  const copySupported = canCopyTextToClipboard()
  const handleCopy = async (event: MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation()
    if (!copySupported) return
    if (await copyTextToClipboard(refKey)) {
      setCopied(true)
    }
  }

  return (
    <span className={`reference-key-badge ${className}`.trim()} title={refKey}>
      <span className="reference-key-value cell-mono">{refKey}</span>
      <button
        type="button"
        className="reference-key-copy"
        aria-label={`Copy ${refKey}`}
        disabled={!copySupported}
        onClick={handleCopy}
      >
        {copied ? 'Copied' : 'Copy'}
      </button>
    </span>
  )
}
