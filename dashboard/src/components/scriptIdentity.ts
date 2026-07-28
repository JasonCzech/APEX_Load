export function isAddressableScriptId(id: string | null | undefined): id is string {
  return typeof id === 'string' && id.length > 0 && id.trim() === id
}
