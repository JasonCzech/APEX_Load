import { orchestrationReturn } from '../utils/orchestrationReturn'

export function OrchestrationReturn({ testId }: { testId: string }) {
  const destination = orchestrationReturn(window.location.search, testId)
  if (!destination) return null
  return <nav aria-label="Orchestration handoff" className="tonal-card">
    <a href={destination.href} rel="noopener noreferrer">Return to APEX Orchestration</a>
    <span> · {destination.host} · Execution evidence stays in APEX Load; analysis and phase decisions stay in Orchestration.</span>
  </nav>
}
