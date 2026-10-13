import { viewToLocationHash } from '../appRouting'

/** A scenario's builder at its Data step, where a binding is changed or removed. */
export function scenarioDataStepHref(scenarioId: string, projectId: string): string {
  return viewToLocationHash({ page: 'tests', scenarioView: 'builder', scenarioId, scenarioStep: 'data', projectId })
}

/** The Scenario library, where an archived scenario is restored. */
export function scenarioLibraryHref(projectId: string): string {
  return viewToLocationHash({ page: 'tests', scenarioView: 'library', projectId })
}

/** A script's editor. */
export function scriptEditorHref(scriptId: string, projectId: string): string {
  return viewToLocationHash({ page: 'scripts', mode: 'edit', scriptId, projectId })
}
