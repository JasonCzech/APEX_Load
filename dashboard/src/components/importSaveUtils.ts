import { type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'

export function withFallbackPersistedScript(
  savedScript: StoredScript,
  fallbackScript?: ScriptDefinition | null,
): StoredScript {
  if (savedScript.script || !fallbackScript) {
    return savedScript
  }

  return {
    ...savedScript,
    script: fallbackScript,
  }
}

export function withFallbackPersistedScripts(
  savedScripts: StoredScript[] | null | undefined,
  fallbackScripts: Array<ScriptDefinition | null | undefined>,
): StoredScript[] {
  return (savedScripts || []).map((savedScript, index) => (
    withFallbackPersistedScript(savedScript, fallbackScripts[index] || null)
  ))
}
