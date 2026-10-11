import { type ImportedSaveResult } from './useScriptsAuthoringState'

interface Props {
  result: ImportedSaveResult | null
  onImportMore: () => void
  onOpen: (result: ImportedSaveResult) => void
  disabled?: boolean
}

export default function ImportSaveConfirmation({ result, onImportMore, onOpen, disabled }: Props) {
  if (!result?.scripts.length) return null
  return (
    <div className="settings-notice">
      <p role="status">Saved {result.scripts.length} script{result.scripts.length === 1 ? '' : 's'} to the script library.</p>
      <div className="script-import-review-actions">
        <button type="button" className="btn btn-primary" disabled={disabled} onClick={onImportMore}>Import More Files</button>
        {result.scripts.map(script => (
          <button type="button" className="btn btn-secondary" disabled={disabled} key={script.id} onClick={() => onOpen({ scripts: [script] })}>
            Open {script.name} in Visual Editor
          </button>
        ))}
      </div>
    </div>
  )
}
