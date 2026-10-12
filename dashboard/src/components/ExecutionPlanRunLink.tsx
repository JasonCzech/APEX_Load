import type { TestExecutionPlanLink } from '../types'
import { viewToLocationHash } from '../appRouting'
import { executionPlanRunLabel } from './executionPlanActivityModel'

/** "Plan: Nightly regression, entry 2 of 3" on a run an execution plan started. */
export default function ExecutionPlanRunLink({ link, projectId }: { link?: TestExecutionPlanLink; projectId?: string }) {
  if (!link?.plan_id) return null
  const href = viewToLocationHash({ page: 'tests', scenarioView: 'plans', ...(projectId ? { projectId } : {}) })
  return (
    <p className="execution-plan-run-link">
      Plan: <a href={href}>{executionPlanRunLabel(link)}</a>
    </p>
  )
}
