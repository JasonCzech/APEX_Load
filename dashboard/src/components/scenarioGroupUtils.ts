export function scenarioGroupNameAfterScriptSelection(
  currentName: string,
  groupIndex: number,
  nextScriptName: string,
  previousScriptName = '',
) {
  const trimmedName = currentName.trim()
  const generatedName = `Group ${groupIndex + 1}`
  const followsSelectedScript = previousScriptName.trim() !== '' && trimmedName === previousScriptName.trim()
  return trimmedName === '' || trimmedName === generatedName || followsSelectedScript
    ? nextScriptName
    : currentName
}
