// Scenario names as people read them: the library, its name check and the
// plan editor's picker agree on when two scenarios share a name.

/** A scenario name compared without case or surrounding space. */
export function scenarioNameKey(name: string): string {
  return name.trim().toLowerCase()
}

/** How many of the scenarios carry each name, keyed by scenarioNameKey. */
export function scenarioNameCounts(scenarios: readonly { name: string }[]): Map<string, number> {
  const counts = new Map<string, number>()
  for (const scenario of scenarios) {
    const key = scenarioNameKey(scenario.name)
    counts.set(key, (counts.get(key) || 0) + 1)
  }
  return counts
}
