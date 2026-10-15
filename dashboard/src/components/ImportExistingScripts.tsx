import type { ImportSaveChoice, ImportSaveChoices } from './importReimport'
import './ImportExistingScripts.css'

// Lists the scripts of an import that an earlier import already saved, each
// with what the save does to it: update it as a new version (it keeps its
// name and folder) or create a copy. Every importer's review shows it the
// same way. (Bruno, which imports the requests the operator selects, also
// offers Skip in its own review; these importers save every script.)
export default function ImportExistingScripts({ choices, labels, disabled = false }: {
  choices: ImportSaveChoices
  /** The name each script of the import is reviewed under, by position. */
  labels: readonly string[]
  disabled?: boolean
}) {
  const entries = (choices.matches ?? [])
    .map((match, index) => ({ match, index }))
    .filter(entry => Boolean(entry.match))
  if (entries.length === 0) return null
  const total = labels.length
  const updates = entries.filter(entry => choices.choiceFor(entry.index) === 'update').length
  const summary = total <= 1
    ? 'This source was imported before.'
    : `${entries.length} of ${total} scripts were imported before.`
  return (
    <section className="import-existing-scripts" aria-label="Imported before">
      <p>
        {summary}{' '}
        {updates > 0
          ? updates === 1
            ? `${entries.length === 1 ? 'It' : '1'} is updated as a new version, keeping its name and folder.`
            : `${updates} are updated as new versions, keeping their names and folders.`
          : 'Nothing is updated; the save creates new scripts.'}
      </p>
      <ul>
        {entries.map(({ match, index }) => {
          const label = labels[index] || `Script ${index + 1}`
          const choice = choices.choiceFor(index)
          return (
            <li key={`${index}:${match!.script_id}`}>
              <label>
                On save for {label}
                <select
                  value={choice}
                  disabled={disabled}
                  onChange={event => choices.setChoice(index, event.target.value as ImportSaveChoice)}
                >
                  <option value="update">Update existing (new version)</option>
                  <option value="copy">Create a copy</option>
                </select>
              </label>
              <small>
                Imported before as {match!.name}{match!.ref_key ? ` (${match!.ref_key})` : ''}
                {match!.matches > 1 ? ` · one of ${match!.matches} earlier scripts that match` : ''}
              </small>
              {!match!.same_source && <small>Matched by a file or generated name only, so it may be an unrelated upload.</small>}
              {match!.edited_since_import && choice === 'update' && (
                <small className="import-existing-scripts-warning">
                  Changed in APEX after it was imported. Updating replaces its steps and settings; its name and folder stay.
                </small>
              )}
            </li>
          )
        })}
      </ul>
    </section>
  )
}
