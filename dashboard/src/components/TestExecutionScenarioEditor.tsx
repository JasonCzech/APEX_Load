import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { StoredScript, TestSummary } from '../types'
import { buildScenarioDraftFromTest } from './scenarioDraftUtils'

const CreateTestModal = lazy(() => import('./CreateTestModal'))

interface Props {
  open: boolean
  test: TestSummary | null
  onClose: () => void
  onSelectTest: (testId: string) => void
}

export default function TestExecutionScenarioEditor({ open, test, onClose, onSelectTest }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const [availableScripts, setAvailableScripts] = useState<StoredScript[]>([])
  const initialScenario = useMemo(() => (test ? buildScenarioDraftFromTest(test) : undefined), [test])

  useEffect(() => {
    if (!open) return

    let cancelled = false

    const loadScripts = async () => {
      try {
        const response = await api.listScripts(activeProject?.id)
        if (!cancelled) {
          setAvailableScripts(response.scripts || [])
        }
      } catch {
        if (!cancelled) {
          setAvailableScripts([])
        }
      }
    }

    loadScripts()
    return () => {
      cancelled = true
    }
  }, [activeProject?.id, api, open])

  if (!open || !test || !initialScenario) {
    return null
  }

  return (
    <Suspense fallback={<div className="page-loading" role="status">Loading scenario editor…</div>}>
      <CreateTestModal
        mode="revise-queued-run"
        projectId={activeProject?.id}
        scripts={availableScripts}
        initialScenario={initialScenario}
        onClose={onClose}
        onCreated={(createdTest) => {
          onClose()
          if (createdTest?.id) {
            onSelectTest(createdTest.id)
          }
        }}
      />
    </Suspense>
  )
}
