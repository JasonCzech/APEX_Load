import { type ImportedSaveResult } from './useScriptsAuthoringState'

interface Props {
  result: ImportedSaveResult | null
  onImportMore: () => void
  onOpen: (result: ImportedSaveResult) => void
  disabled?: boolean
  /** Creates one scenario from every saved script and opens it. */
  onCreateScenario?: () => void
  scenarioBusy?: boolean
  scenarioError?: string
}

// Beyond this many saved scripts, the rest open from a disclosure, so a large
// collection does not bury what to do next under one button per script.
const LISTED_SCRIPTS = 3

export default function ImportSaveConfirmation({ result, onImportMore, onOpen, disabled, onCreateScenario, scenarioBusy = false, scenarioError = '' }: Props) {
  if (!result?.scripts.length) return null
  const [first] = result.scripts
  const listed = result.scripts.length > LISTED_SCRIPTS ? [] : result.scripts
  const more = result.scripts.length > LISTED_SCRIPTS ? result.scripts : []
  const openButton = (script: typeof first) => (
    <button type="button" className="btn btn-secondary" disabled={disabled} key={script.id} onClick={() => onOpen({ scripts: [script] })}>
      Open {script.name} in Visual Editor
    </button>
  )
  return (
    <div className="settings-notice">
      <p role="status">Saved {result.scripts.length} script{result.scripts.length === 1 ? '' : 's'} to the script library.</p>
      <div className="script-import-review-actions">
        {onCreateScenario && (
          <button type="button" className="btn btn-primary" disabled={disabled || scenarioBusy} onClick={onCreateScenario}>
            {scenarioBusy ? 'Creating scenario…' : `Create a scenario from ${result.scripts.length === 1 ? 'this script' : `these ${result.scripts.length} scripts`}`}
          </button>
        )}
        <button type="button" className="btn btn-secondary" disabled={disabled} onClick={() => onOpen({ scripts: [first], editorWorkspace: 'debug' })}>
          Debug {first.name}
        </button>
        <button type="button" className={onCreateScenario ? 'btn btn-secondary' : 'btn btn-primary'} disabled={disabled} onClick={onImportMore}>Import More Files</button>
        {listed.map(openButton)}
      </div>
      {more.length > 0 && (
        <details className="import-save-more-scripts">
          <summary>Open a saved script in Visual Editor ({more.length})</summary>
          <div className="script-import-review-actions">{more.map(openButton)}</div>
        </details>
      )}
      {scenarioError && <p role="alert" className="form-error">{scenarioError}</p>}
    </div>
  )
}
