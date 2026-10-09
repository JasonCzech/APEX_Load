import type { AuthoringHealthInput, StoredScript } from '../types'

function healthInputPriority(status?: string) {
  switch (status) {
    case 'blocker':
      return 0
    case 'warning':
    case 'needs_review':
      return 1
    case 'pass':
      return 3
    case 'info':
      return 4
    default:
      return 2
  }
}

/** Health inputs, most severe first (stable within a severity). */
export function prioritizeHealthInputs(inputs: AuthoringHealthInput[]) {
  return inputs
    .map((input, index) => ({ input, index }))
    .sort((left, right) => {
      const priorityDelta = healthInputPriority(left.input.status) - healthInputPriority(right.input.status)
      return priorityDelta !== 0 ? priorityDelta : left.index - right.index
    })
    .map(({ input }) => input)
}

/** Blocker and warning counts of a script's authoring health (review items count as warnings). */
export function authoringHealthCounts(script: StoredScript) {
  const inputs = script.authoring_health?.inputs || []
  return {
    blockers: inputs.filter(input => input.status === 'blocker').length,
    warnings: inputs.filter(input => input.status === 'warning' || input.status === 'needs_review').length,
  }
}
