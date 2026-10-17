import { lazy, Suspense, useEffect, useId, useMemo, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { useProject } from '../hooks/useProject'
import { StoredScript, TestSummary } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { dialogBackdropProps } from './dialogBackdropProps'
import { buildScenarioDraftFromTest } from './scenarioDraftUtils'
import DialogLayer from './ux/DialogLayer'
import LoadingState from './ux/LoadingState'
import Notice from './ux/Notice'

const loadCreateTestModal = () => import('./CreateTestModal')
const CreateTestModal = lazy(loadCreateTestModal)

interface Props {
  open: boolean
  test: TestSummary | null
  onClose: () => void
  onSelectTest: (testId: string) => void
}

// The editor opens only once the project's scripts are known: a failed or
// slow load must not read as a project without scripts. A retry keeps the
// failure on screen until its result arrives.
type ScriptsLoad =
  | { status: 'loading' }
  | { status: 'loaded'; scripts: StoredScript[] }
  | { status: 'failed'; failure: string; retrying: boolean }

export default function TestExecutionScenarioEditor({ open, test, onClose, onSelectTest }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const [scriptsLoad, setScriptsLoad] = useState<ScriptsLoad>({ status: 'loading' })
  const [loadAttempt, setLoadAttempt] = useState(0)
  const initialScenario = useMemo(() => (test ? buildScenarioDraftFromTest(test) : undefined), [test])
  const owningProjectId = test?.project_id || activeProject?.id

  useEffect(() => {
    if (!open) return

    let cancelled = false
    setScriptsLoad(current => (current.status === 'loading' || (current.status === 'failed' && current.retrying) ? current : { status: 'loading' }))
    // Fetch the editor's code while its scripts load. A failed fetch surfaces
    // when the editor renders.
    loadCreateTestModal().catch(() => undefined)

    const loadScripts = async () => {
      try {
        const response = await api.listScripts(owningProjectId)
        if (!cancelled) {
          setScriptsLoad({ status: 'loaded', scripts: response.scripts || [] })
        }
      } catch (cause) {
        if (!cancelled) {
          setScriptsLoad({ status: 'failed', failure: getErrorMessage(cause, 'Scripts could not be loaded.'), retrying: false })
        }
      }
    }

    loadScripts()
    return () => {
      cancelled = true
    }
  }, [api, open, owningProjectId, loadAttempt])

  if (!open || !test || !initialScenario) {
    return null
  }

  if (scriptsLoad.status === 'loading') {
    return <LoadingState what="scenario editor" />
  }

  if (scriptsLoad.status === 'failed') {
    return (
      <ScriptsUnavailableDialog
        failure={scriptsLoad.failure}
        retrying={scriptsLoad.retrying}
        onRetry={() => {
          setScriptsLoad({ ...scriptsLoad, retrying: true })
          setLoadAttempt(attempt => attempt + 1)
        }}
        onClose={onClose}
      />
    )
  }

  return (
    <Suspense fallback={<LoadingState what="scenario editor" />}>
      <CreateTestModal
        mode="revise-queued-run"
        projectId={owningProjectId}
        scripts={scriptsLoad.scripts}
        initialScenario={initialScenario}
        profileManaged={test.traffic_profile?.runtime_target === 'tps' && Boolean(test.traffic_profile.mappings?.length)}
        onClose={onClose}
        onCreated={(createdTest) => {
          if (createdTest?.id) {
            onSelectTest(createdTest.id)
          }
          onClose()
        }}
      />
    </Suspense>
  )
}

function ScriptsUnavailableDialog({ failure, retrying, onRetry, onClose }: { failure: string; retrying: boolean; onRetry: () => void; onClose: () => void }) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const retryRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  useModalDialog({ dialogRef, initialFocusRef: retryRef, onClose })
  return (
    <DialogLayer>
      <div className="modal-overlay" {...dialogBackdropProps(onClose)}>
        <div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-labelledby={titleId}>
          <div className="modal-header">
            <h3 id={titleId}>Create revised queued run</h3>
            <button className="btn btn-sm btn-ghost" type="button" onClick={onClose}>Close</button>
          </div>
          <Notice
            tone="error"
            title="Scripts could not be loaded"
            details={failure}
            action={<button ref={retryRef} className="btn btn-secondary" type="button" disabled={retrying} onClick={onRetry}>{retrying ? 'Retrying…' : 'Retry'}</button>}
          >
            The scenario editor needs this project&apos;s scripts. Check your connection or access, then retry.
          </Notice>
        </div>
      </div>
    </DialogLayer>
  )
}
