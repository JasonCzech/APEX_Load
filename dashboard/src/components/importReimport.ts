import { useCallback, useMemo, useState } from 'react'

import type { ImportExistingScript, ImportPreviousInput, ImportUpdateTarget } from '../types/scriptImports'

// Re-import as a new version, for every importer but Bruno (which has its
// own candidate matching): the import response lists, per script, the earlier
// imported script it would update (existing_scripts), and the save sends the
// choice as positional update targets.

export type ImportSaveChoice = 'update' | 'copy'

export type ImportMatches = readonly (ImportExistingScript | null | undefined)[] | undefined

/**
 * A match from a source that declares its identity (a collection, a
 * specification, a test plan) updates by default. A match on a file name alone
 * may be an unrelated upload, so it defaults to a copy.
 */
export function defaultImportSaveChoice(match: ImportExistingScript | null | undefined): ImportSaveChoice {
  return match?.same_source ? 'update' : 'copy'
}

/**
 * The positional update targets for a save of scripts, or undefined when
 * nothing updates (so the request stays as it always was).
 */
export function importUpdateTargets(
  matches: ImportMatches,
  choiceFor: (index: number) => ImportSaveChoice,
  count: number,
): (ImportUpdateTarget | null)[] | undefined {
  const targets = Array.from({ length: count }, (_unused, index) => importUpdateTarget(matches?.[index], choiceFor(index)))
  return targets.some(Boolean) ? targets : undefined
}

/** The update target of one matched script under choice, or null to create it. */
export function importUpdateTarget(match: ImportExistingScript | null | undefined, choice: ImportSaveChoice): ImportUpdateTarget | null {
  if (!match || choice !== 'update') return null
  return {
    script_id: match.script_id,
    expected_updated_at: match.updated_at,
    ...(match.content_digest ? { expected_content_digest: match.content_digest } : {}),
  }
}

/**
 * How the scripts being updated supplied each input their import recorded, by
 * input name. The first updated script that supplies an input decides it.
 */
export function previousImportInputs(
  matches: ImportMatches,
  choiceFor: (index: number) => ImportSaveChoice,
): Record<string, ImportPreviousInput> | undefined {
  const inputs: Record<string, ImportPreviousInput> = {}
  matches?.forEach((match, index) => {
    if (!match || choiceFor(index) !== 'update') return
    for (const [name, input] of Object.entries(match.inputs ?? {})) {
      if (!(name in inputs)) inputs[name] = input
    }
  })
  return Object.keys(inputs).length > 0 ? inputs : undefined
}

/**
 * The save choices of one import's matched scripts, read from choices (by
 * script position; a position not chosen takes its default) and changed
 * through setChoice.
 */
export function buildImportSaveChoices(
  matches: ImportMatches,
  choices: Readonly<Record<number, ImportSaveChoice>>,
  setChoice: (index: number, choice: ImportSaveChoice) => void,
) {
  const choiceFor = (index: number): ImportSaveChoice => choices[index] ?? defaultImportSaveChoice(matches?.[index])
  return {
    matches,
    hasMatches: (matches ?? []).some(Boolean),
    choiceFor,
    setChoice,
    previousInputs: previousImportInputs(matches, choiceFor),
    updateTargets: (count: number) => importUpdateTargets(matches, choiceFor, count),
    /** The update target of the script at index alone, or null to create it. */
    updateTargetFor: (index: number): ImportUpdateTarget | null => importUpdateTarget(matches?.[index], choiceFor(index)),
    /** Whether the operator chose for the script at index (rather than it taking its default). */
    chosen: (index: number) => choices[index] !== undefined,
  }
}

/**
 * The replace fields of a one-script save that updates an earlier imported
 * script: a full replace of it, refused if it changed after review. The
 * script keeps its name.
 */
export function reimportReplaceFields(target: ImportUpdateTarget) {
  return {
    target_script_id: target.script_id,
    replace_mode: 'full' as const,
    expected_updated_at: target.expected_updated_at,
  }
}

/** Names one import's matches and their versions, to key its choices by. */
export function importMatchesIdentity(matches: ImportMatches): string {
  return (matches ?? []).map(match => match ? `${match.script_id}@${match.updated_at}` : '').join('|')
}

/**
 * The operator's save choice for each matched script of one import, reset
 * when the matches change (a new import or a new version of a match).
 */
export function useImportSaveChoices(matches: ImportMatches) {
  const identity = importMatchesIdentity(matches)
  const [state, setState] = useState<{ identity: string; choices: Record<number, ImportSaveChoice> }>({ identity, choices: {} })
  const choices = state.identity === identity ? state.choices : EMPTY_CHOICES
  const setChoice = useCallback((index: number, choice: ImportSaveChoice) => {
    setState(previous => ({
      identity,
      choices: { ...(previous.identity === identity ? previous.choices : {}), [index]: choice },
    }))
  }, [identity])
  return useMemo(() => buildImportSaveChoices(matches, choices, setChoice), [matches, choices, setChoice])
}

const EMPTY_CHOICES: Record<number, ImportSaveChoice> = {}

export type ImportSaveChoices = ReturnType<typeof useImportSaveChoices>
