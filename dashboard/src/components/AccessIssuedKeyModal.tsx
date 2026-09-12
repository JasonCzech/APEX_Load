import { API_ORIGIN } from '../hooks/apiClient'
import DialogLayer from './ux/DialogLayer'
import { useEffect, useRef } from 'react'
import type { IssuedKeyState } from './accessTypes'

type AccessIssuedKeyModalProps = {
  issuedKey: IssuedKeyState
  copyMessage: string
  onCopy: () => Promise<void>
  onClose: () => void
}

export function AccessIssuedKeyModal({
  issuedKey,
  copyMessage,
  onCopy,
  onClose,
}: AccessIssuedKeyModalProps) {
  const endpoint = API_ORIGIN
  const curlExample = `curl -H "Authorization: Bearer ${issuedKey.key}" ${endpoint}/api/v1/projects`
  const mcpExample = `APEXLOAD_URL=${endpoint}/api/v1\nAPEXLOAD_API_KEY=${issuedKey.key}\npython3 mcp/server.py`
  const dialogRef = useRef<HTMLDivElement>(null)
  const closeButtonRef = useRef<HTMLButtonElement>(null)

  // The plaintext key appears once, so keep focus contained until the user closes it.
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null
    closeButtonRef.current?.focus()
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onClose()
        return
      }
      if (event.key !== 'Tab') return
      const focusables = dialogRef.current?.querySelectorAll<HTMLElement>(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      )
      if (!focusables || focusables.length === 0) return
      const first = focusables[0]
      const last = focusables[focusables.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      previouslyFocused?.focus?.()
    }
  }, [onClose])

  return (
    <DialogLayer><div className="access-modal-overlay" role="presentation">
      <div className="access-issued-modal" role="dialog" aria-modal="true" aria-label="Issued API key" ref={dialogRef}>
        <div className="access-modal-header">
          <div>
            <h3>{issuedKey.mode === 'created' ? 'API Key Created' : 'API Key Rotated'}</h3>
            <p>Copy this key now. APEX Load stores only a hash after this dialog closes.</p>
          </div>
          <button ref={closeButtonRef} className="btn btn-ghost btn-sm" type="button" onClick={onClose}>Close</button>
        </div>
        <div className="access-issued-key-box">
          <span>{issuedKey.accountName}</span>
          <code>{issuedKey.key}</code>
          <button className="btn btn-secondary btn-sm" type="button" onClick={onCopy}>{copyMessage || 'Copy Key'}</button>
          <span className="access-copy-status" role="status" aria-live="polite">{copyMessage}</span>
        </div>
        <div className="access-issued-examples">
          <div>
            <h4>cURL</h4>
            <pre><code>{curlExample}</code></pre>
          </div>
          <div>
            <h4>MCP Server</h4>
            <pre><code>{mcpExample}</code></pre>
          </div>
        </div>
      </div>
    </div></DialogLayer>
  )
}
