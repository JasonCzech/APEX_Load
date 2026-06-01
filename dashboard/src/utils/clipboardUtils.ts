export function canCopyTextToClipboard(): boolean {
  return Boolean(
    (typeof navigator !== 'undefined' && typeof navigator.clipboard?.writeText === 'function') ||
    (typeof document !== 'undefined' && typeof document.execCommand === 'function'),
  )
}

export async function copyTextToClipboard(value: string): Promise<boolean> {
  if (!value) return false

  if (copyTextWithTextarea(value)) {
    return true
  }

  if (typeof navigator !== 'undefined' && typeof navigator.clipboard?.writeText === 'function') {
    try {
      await navigator.clipboard.writeText(value)
      return true
    } catch {
      return false
    }
  }

  return false
}

function copyTextWithTextarea(value: string): boolean {
  if (typeof document === 'undefined' || typeof document.execCommand !== 'function') {
    return false
  }

  const textarea = document.createElement('textarea')
  textarea.value = value
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.left = '0'
  textarea.style.top = '0'
  textarea.style.width = '2px'
  textarea.style.height = '2px'
  textarea.style.color = 'transparent'
  textarea.style.background = 'transparent'
  textarea.style.border = '0'
  textarea.style.padding = '0'
  textarea.style.pointerEvents = 'none'
  document.body.appendChild(textarea)

  const selection = document.getSelection()
  const selectedRange = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null
  const activeElement = document.activeElement instanceof HTMLElement ? document.activeElement : null
  let handledCopyEvent = false

  const handleCopy = (event: ClipboardEvent) => {
    if (!event.clipboardData) return
    event.clipboardData.setData('text/plain', value)
    event.preventDefault()
    handledCopyEvent = true
  }

  textarea.focus()
  textarea.select()
  textarea.setSelectionRange(0, value.length)

  try {
    document.addEventListener('copy', handleCopy)
    return document.execCommand('copy') || handledCopyEvent
  } catch {
    return false
  } finally {
    document.removeEventListener('copy', handleCopy)
    document.body.removeChild(textarea)
    activeElement?.focus()
    if (selection && selectedRange) {
      selection.removeAllRanges()
      selection.addRange(selectedRange)
    }
  }
}
