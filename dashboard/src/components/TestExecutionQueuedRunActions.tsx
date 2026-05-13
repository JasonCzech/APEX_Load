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

  if (test.status !== 'QUEUED') {
    return null
  }

  return (
    <>
      <button className="btn btn-secondary" onClick={() => setShowScenarioEditor(true)}>
        Revise Queued Run
      </button>
      <button className="btn btn-success" onClick={onStart}>
        Start
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