function normalizeScenarioGroupName(name: string): string {
  return name.trim().replace(/\s+/g, ' ').toLocaleLowerCase()
}

export function duplicateScenarioGroupNameIndexes(names: string[]): Set<number> {
  const indexesByName = new Map<string, number[]>()
  names.forEach((name, index) => {
    const normalized = normalizeScenarioGroupName(name)
    if (!normalized) return
    indexesByName.set(normalized, [...(indexesByName.get(normalized) || []), index])
  })

  return new Set(
    Array.from(indexesByName.values())
      .filter(indexes => indexes.length > 1)
      .flat(),
  )
}

function uniqueDefaultScenarioGroupName(
  scriptName: string,
  groupIndex: number,
  existingNames: string[],
): string {
  const usedNames = new Set(existingNames
    .filter((_, index) => index !== groupIndex)
    .map(normalizeScenarioGroupName)
    .filter(Boolean))
  if (!usedNames.has(normalizeScenarioGroupName(scriptName))) return scriptName

  let suffix = groupIndex + 1
  let candidate = `${scriptName} (#${suffix})`
  while (usedNames.has(normalizeScenarioGroupName(candidate))) {
    suffix += 1
    candidate = `${scriptName} (#${suffix})`
  }
  return candidate
}

export function scenarioGroupNameAfterScriptSelection(
  currentName: string,
  groupIndex: number,
  nextScriptName: string,
  previousScriptName = '',
  existingNames: string[] = [],
) {
  const trimmedName = currentName.trim()
  const generatedName = `Group ${groupIndex + 1}`
  const followsSelectedScript = previousScriptName.trim() !== '' && trimmedName === previousScriptName.trim()
  return trimmedName === '' || trimmedName === generatedName || followsSelectedScript
    ? uniqueDefaultScenarioGroupName(nextScriptName, groupIndex, existingNames)
    : currentName
}
