import { useEffect, useRef } from 'react'

import { viewToLocationHash } from '../appRouting'
import { addRecent } from '../desktop/desktopRecents'
import { savedScenarioBuilderHash } from './importScenario'
import { importScenarioName } from './useImportScenario'
import { type ImportedSaveResult } from './useScriptsAuthoringState'

interface Props {
  result: ImportedSaveResult | null
  onImportMore: () => void
  onOpen: (result: ImportedSaveResult) => void
  disabled?: boolean
  /**
   * Creates one scenario from every saved script and opens it. Not offered
   * when the save already made a scenario (result.scenario): that one opens.
   */
  onCreateScenario?: () => void
  scenarioBusy?: boolean
  scenarioError?: string
  /** What the desktop app's Recent list calls this save: the source's name, as its scenario would be named. */
  recentName?: string
}

// Beyond this many saved scripts, the rest open from a disclosure, so a large
// collection does not bury what to do next under one button per script.
const LISTED_SCRIPTS = 3

export default function ImportSaveConfirmation({ result, onImportMore, onOpen, disabled, onCreateScenario, scenarioBusy = false, scenarioError = '', recentName }: Props) {
  // The desktop app lists each saved import under Recent and File > Open
  // Recent (a no-op in the browser): one script reopens in the editor, a batch
  // in the script library, and a scenario the save made opens in the builder.
  // Recorded once per saved batch.
  const resultRef = useRef(result)
  resultRef.current = result
  const recentKey = [result?.scenario?.id || '', ...(result?.scripts || []).map(script => script.id)].join('\n')
  useEffect(() => {
    const saved = resultRef.current
    const scripts = saved?.scripts
    if (!saved || !scripts?.length) return
    const [firstSaved] = scripts
    const scope = firstSaved.project_id ? { projectId: firstSaved.project_id } : {}
    void (async () => {
      await addRecent({
        kind: 'import',
        id: firstSaved.id,
        name: recentName || importScenarioName(undefined, scripts),
        route: viewToLocationHash(scripts.length === 1
          ? { page: 'scripts', mode: 'edit', scriptId: firstSaved.id, ...scope }
          : { page: 'scripts', mode: 'list', ...scope }),
      })
      const scenario = saved.scenario
      if (scenario?.id) {
        await addRecent({ kind: 'scenario', id: scenario.id, name: scenario.name || scenario.id, route: savedScenarioBuilderHash(scenario.id, scenario.project_id) })
      }
    })()
  }, [recentKey, recentName])
  if (!result?.scripts.length) return null
  const [first] = result.scripts
  const savedScenario = result.scenario?.id ? result.scenario : null
  const createScenario = savedScenario ? undefined : onCreateScenario
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
        {savedScenario && (
          <button type="button" className="btn btn-primary" disabled={disabled} onClick={() => { window.location.hash = savedScenarioBuilderHash(savedScenario.id, savedScenario.project_id) }}>
            Open scenario {savedScenario.name}
          </button>
        )}
        {createScenario && (
          <button type="button" className="btn btn-primary" disabled={disabled || scenarioBusy} onClick={createScenario}>
            {scenarioBusy ? 'Creating scenario…' : `Create a scenario from ${result.scripts.length === 1 ? 'this script' : `these ${result.scripts.length} scripts`}`}
          </button>
        )}
        <button type="button" className="btn btn-secondary" disabled={disabled} onClick={() => onOpen({ scripts: [first], editorWorkspace: 'debug' })}>
          Debug {first.name}
        </button>
        <button type="button" className={savedScenario || createScenario ? 'btn btn-secondary' : 'btn btn-primary'} disabled={disabled} onClick={onImportMore}>Import More Files</button>
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
