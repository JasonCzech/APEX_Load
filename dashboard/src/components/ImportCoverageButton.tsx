import { useId, useRef, useState, type ReactNode } from 'react'
import DialogLayer from './ux/DialogLayer'
import { useModalDialog } from '../hooks/useModalDialog'
import './ImportCoverageButton.css'

/**
 * Shared trigger + modal for the "Import Coverage" panel that every import
 * wizard (ProtocolAssetImport, JMeterImport, EnterpriseArchiveImport) shows.
 * Keeps the coverage detail out of the wizard's default view until asked for.
 */
export default function ImportCoverageButton({
  label = 'Import Coverage',
  title = 'Import Coverage',
  subtitle,
  triggerAriaLabel,
  triggerClassName = 'btn btn-secondary btn-sm',
  children,
}: {
  label?: string
  title?: string
  subtitle?: string
  triggerAriaLabel?: string
  triggerClassName?: string
  children: ReactNode
}) {
  const [open, setOpen] = useState(false)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  useModalDialog({ dialogRef, enabled: open, initialFocusRef: closeButtonRef, onClose: () => setOpen(false) })

  return (
    <div className="import-coverage-trigger" aria-label={triggerAriaLabel}>
      <button type="button" className={triggerClassName} onClick={() => setOpen(true)}>
        {label}
      </button>
      {open && (
        <DialogLayer>
          <div
            className="import-coverage-modal-backdrop"
            role="presentation"
            onPointerDown={event => {
              if (event.target === event.currentTarget) setOpen(false)
            }}
          >
            <div
              ref={dialogRef}
              className="import-coverage-modal"
              role="dialog"
              aria-modal="true"
              aria-labelledby={titleId}
            >
              <div className="import-coverage-modal-header">
                <div>
                  <h3 id={titleId}>{title}</h3>
                  {subtitle && <p>{subtitle}</p>}
                </div>
                <button ref={closeButtonRef} type="button" className="btn btn-ghost" onClick={() => setOpen(false)}>
                  Close
                </button>
              </div>
              <div className="import-coverage-modal-body">
                {children}
              </div>
            </div>
          </div>
        </DialogLayer>
      )}
    </div>
  )
}
