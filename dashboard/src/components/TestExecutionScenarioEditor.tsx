import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { StoredScript, TestSummary } from '../types'
import { buildScenarioDraftFromTest } from './scenarioDraftUtils'
import LoadingState from './ux/LoadingState'

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
  const owningProjectId = test?.project_id || activeProject?.id

  useEffect(() => {
    if (!open) return

    let cancelled = false

    const loadScripts = async () => {
      try {
        const response = await api.listScripts(owningProjectId)
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
  }, [api, open, owningProjectId])

  if (!open || !test || !initialScenario) {
    return null
  }

  return (
    <Suspense fallback={<LoadingState what="scenario editor" />}>
      <CreateTestModal
        mode="revise-queued-run"
        projectId={owningProjectId}
        scripts={availableScripts}
        initialScenario={initialScenario}
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
