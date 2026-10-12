import type { TestSummary } from '../types'
import { formatDurationDisplay, isValidDurationInput } from '../utils/timeUtils'

// Confirmation copy for the run lifecycle controls and live load changes.
// Every one of these changes what a live load test is doing to a real target
// environment, so each is confirmed before it is sent.
export type RunActionConfirmationKind = 'start' | 'pause' | 'resume' | 'stop' | 'abort' | 'apply-target'

export interface RunActionConfirmationCopy {
  title: string
  description: string
  confirmLabel: string
  tone: 'primary' | 'secondary' | 'danger'
}

const RUN_ACTION_CONFIRMATIONS: Record<RunActionConfirmationKind, RunActionConfirmationCopy> = {
  start: {
    title: 'Start this test run?',
    description: 'Load generation begins immediately against the target environment.',
    confirmLabel: 'Start Test',
    tone: 'primary',
  },
  pause: {
    title: 'Pause this test run?',
    description: 'VUsers hold at their next action boundary. The run stays active and you can resume it at any time.',
    confirmLabel: 'Pause Test',
    tone: 'secondary',
  },
  resume: {
    title: 'Resume this test run?',
    description: 'VUsers release from the pause gate and load generation continues against the target environment.',
    confirmLabel: 'Resume Test',
    tone: 'primary',
  },
  stop: {
    title: 'Stop this test run?',
    description: 'VUsers finish the action already in flight, then the run ends and is finalized. A stopped run cannot be resumed.',
    confirmLabel: 'Stop Test',
    tone: 'danger',
  },
  abort: {
    title: 'Abort this test run?',
    description: 'Generator work is cancelled immediately. Transactions in flight are lost and the run cannot be resumed.',
    confirmLabel: 'Abort Test',
    tone: 'danger',
  },
  'apply-target': {
    title: 'Apply this load change?',
    description: 'The running test starts changing as soon as it is applied, at once or over its ramp.',
    confirmLabel: 'Apply change',
    tone: 'primary',
  },
}

export function runActionConfirmationCopy(kind: RunActionConfirmationKind): RunActionConfirmationCopy {
  return RUN_ACTION_CONFIRMATIONS[kind]
}

/**
 * What pausing does to a run's clock: a run to a duration keeps counting
 * while paused, so the paused time is not run again after resuming.
 */
export function pauseDurationNote(run?: Pick<TestSummary, 'duration' | 'termination_mode'> | null): string {
  if (!run || run.termination_mode === 'iterations' || !isValidDurationInput(run.duration)) return ''
  return `Paused time counts against the ${formatDurationDisplay(run.duration)} duration; resuming does not add it back.`
}
