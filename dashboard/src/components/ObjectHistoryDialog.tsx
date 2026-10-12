import { useId, useRef } from 'react'
import { createPortal } from 'react-dom'
import { useModalDialog } from '../hooks/useModalDialog'
import type { ActivityResourceType, ObjectActivitySummary } from '../types/objectActivity'
import { dialogBackdropProps } from './dialogBackdropProps'
import ObjectHistory from './ObjectHistory'
import DialogLayer from './ux/DialogLayer'

/** An object's attribution and activity history, opened on demand instead of taking page space. */
export default function ObjectHistoryDialog({ title, projectId, resourceType, resourceId, activity, onClose }: { title: string; projectId?: string; resourceType: ActivityResourceType; resourceId: string; activity?: ObjectActivitySummary; onClose: () => void }) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  useModalDialog({ dialogRef, initialFocusRef: closeRef, onClose })
  return createPortal(<DialogLayer><div className="modal-overlay" {...dialogBackdropProps(onClose)}>
    <div ref={dialogRef} className="modal object-history-dialog" role="dialog" aria-modal="true" aria-labelledby={titleId}>
      <div className="modal-header">
        <h3 id={titleId}>{title}</h3>
        <button ref={closeRef} className="btn btn-sm btn-ghost" type="button" onClick={onClose}>Close</button>
      </div>
      <ObjectHistory projectId={projectId} resourceType={resourceType} resourceId={resourceId} activity={activity} expanded />
    </div>
  </div></DialogLayer>, document.body)
}
