import { useState } from 'react'

import { TestSummary } from '../types'
import TestExecutionScenarioEditor from './TestExecutionScenarioEditor'

interface Props {
  test: TestSummary
  onStart: () => void
  onSelectTest: (testId: string) => void
}

export default function TestExecutionQueuedRunActions({ test, onStart, onSelectTest }: Props) {
  const [showScenarioEditor, setShowScenarioEditor] = useState(false)
  const canStart = test.status === 'QUEUED' || test.status === 'RESERVED'

  if (!canStart) {
    return null
  }

  return (
    <>
      {test.status === 'QUEUED' && (
        <button className="btn btn-secondary" onClick={() => setShowScenarioEditor(true)}>
          Revise Queued Run
        </button>
      )}
      <button className="btn btn-success" onClick={onStart}>
        Start Test
      </button>

      <TestExecutionScenarioEditor
        open={showScenarioEditor}
        test={test}
        onClose={() => setShowScenarioEditor(false)}
        onSelectTest={onSelectTest}
      />
    </>
  )
}
