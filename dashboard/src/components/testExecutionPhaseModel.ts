import { TestSummary } from '../types'

interface ExecutionPhaseModel {
  isRunning: boolean
  isPaused: boolean
  isStopping: boolean
  showLiveControls: boolean
  showAbortControl: boolean
  showArchiveSection: boolean
  showArchiveDownload: boolean
}

export function buildExecutionPhaseModel(test: TestSummary, hasArchive: boolean): ExecutionPhaseModel {
  const isRunning = test.status === 'RUNNING' || test.status === 'INITIALIZING'
  const isPaused = test.status === 'PAUSED'
  const isStopping = test.status === 'STOPPING'

  return {
    isRunning,
    isPaused,
    isStopping,
    showLiveControls: isRunning || isPaused,
    // Detailed live controls are unsafe once teardown starts, but operators
    // must retain one escalation path so a wedged graceful stop can retry the
    // backend's idempotent teardown.
    showAbortControl: isRunning || isPaused || isStopping,
    showArchiveSection: !isRunning && !isStopping,
    showArchiveDownload: !isRunning && !isStopping && hasArchive,
  }
}
