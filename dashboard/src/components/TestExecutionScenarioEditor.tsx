import { useEffect, useMemo, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { StoredScript, TestSummary } from '../types'
import CreateTestModal from './CreateTestModal'
import { buildScenarioDraftFromTest } from './scenarioDraftUtils'

interface Props {
  open: boolean
  test: TestSummary | null
  onClose: () => void
  onSelectTest: (testId: string) => void
}

export default function TestExecutionScenarioEditor({ open, test, onClose, onSelectTest }: Props) {
  const api = useApi()
  const [availableScripts, setAvailableScripts] = useState<StoredScript[]>([])
  const initialScenario = useMemo(() => (test ? buildScenarioDraftFromTest(test) : undefined), [test])

  useEffect(() => {
    if (!open) return

    let cancelled = false

    const loadScripts = async () => {
      try {
        const response = await api.listScripts()
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
  }, [api, open])

  if (!open || !test || !initialScenario) {
    return null
  }

  return (
    <CreateTestModal
      mode="revise-queued-run"
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
  )
}