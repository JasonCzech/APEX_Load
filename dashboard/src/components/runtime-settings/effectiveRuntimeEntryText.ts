import { RUNTIME_VALUE_PROVENANCE_LABELS } from './canonicalRuntimeTaxonomy'
import {
  buildEffectiveRuntimeSummary,
  type EffectiveRuntimeEntry,
  type EffectiveRuntimeGroupSource,
  type StoredRuntimeRow,
} from './effectiveRuntimeSummaryModel'

// One path of the shared effective runtime summary, for a compact card that sits
// beside EffectiveRuntimeSettingsView and must give the same answer.

/** The summary entry for one path, resolved exactly as EffectiveRuntimeSettingsView resolves it. */
export function effectiveRuntimeEntry(group: EffectiveRuntimeGroupSource, scenarioRow: StoredRuntimeRow, path: string): EffectiveRuntimeEntry | undefined {
  for (const category of buildEffectiveRuntimeSummary(group, scenarioRow).categories) {
    const entry = category.entries.find(candidate => candidate.path === path)
    if (entry) return entry
  }
  return undefined
}

/**
 * The entry's text with its layer in parentheses ("5 (scenario default)"). A script value, or a
 * value whose layer is unknown (legacy rows), reads as the bare text; the schedule's note on a
 * removed script cap is part of the text already.
 */
export function formatEffectiveRuntimeEntryInline(entry: EffectiveRuntimeEntry): string {
  if (!entry.provenance || entry.provenance === 'script') return entry.text
  return `${entry.text} (${RUNTIME_VALUE_PROVENANCE_LABELS[entry.provenance].toLowerCase()})`
}
