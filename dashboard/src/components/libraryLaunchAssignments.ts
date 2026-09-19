import type { StoredScenario, StoredScript } from '../types/storedContent'
export function suggestedApplication(scenario: StoredScenario | undefined, scripts: StoredScript[]): string | undefined {
  if (scenario?.library_assignment?.application) return scenario.library_assignment.application.id
  const ids = new Set(scripts.filter(script => scenario?.scripts.some(group => group.script_id === script.id)).flatMap(script => script.library_assignment?.application ? [script.library_assignment.application.id] : []))
  return ids.size === 1 ? [...ids][0] : undefined
}
export function suggestedReleases(scenario: StoredScenario | undefined, scripts: StoredScript[], applicationId: string): Set<string> {
  const assignments = scenario?.library_assignment?.application ? [scenario.library_assignment] : scripts.filter(script => scenario?.scripts.some(group => group.script_id === script.id)).map(script => script.library_assignment)
  return new Set(assignments.flatMap(assignment => assignment?.application?.id === applicationId ? assignment.releases.filter(release => !release.archived_at).map(release => release.id) : []))
}
