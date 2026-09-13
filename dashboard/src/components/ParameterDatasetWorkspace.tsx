import { useEffect, useState } from 'react'
import type { ParameterDataDialogProps } from './ScriptEditorParameterFeatures'
import { ParameterDataEditor } from './ScriptEditorParameterDataSources'
import { parseInlineData, summarizeInlineData, validateInlineData } from './scriptEditorUtils'

export function ParameterDatasetWorkspace(props: ParameterDataDialogProps & { onShowParameter: (index: number) => void }) {
  const [selectedIndex, setSelectedIndex] = useState(props.openDataEditor ?? 0)
  useEffect(() => { if (props.openDataEditor !== null) setSelectedIndex(props.openDataEditor) }, [props.openDataEditor])
  const sources = props.script.data_sources || []
  const index = sources[selectedIndex] ? selectedIndex : 0
  const source = sources[index]
  const parameters = props.script.parameters?.filter(parameter => parameter.data_source?.trim().toLowerCase() === source?.name.trim().toLowerCase()) || []
  return <section className="dataset-workspace" aria-label="Shared datasets">
    <div className="parameter-workspace-actions">
      <label>Dataset<select aria-label="Dataset" value={index} onChange={event => setSelectedIndex(Number(event.target.value))}>{sources.map((item, position) => <option key={position} value={position}>{item.name}</option>)}</select></label>
      {props.onAddDataset && <button type="button" className="btn btn-sm btn-secondary" onClick={props.onAddDataset}>+ Dataset</button>}
      {props.onMergeSources && <button type="button" className="btn btn-sm btn-secondary" disabled={!props.mergeSummary?.canMerge} onClick={props.onMergeSources}>Merge compatible sources</button>}
    </div>
    {!source ? <p>No shared datasets. Add a file parameter or import a script with parameter data.</p> : <>
      <p>Shared dataset · {parameters.length} parameter mappings. Changes to rows and file structure apply to every mapping.</p>
      <div className="dataset-usage" aria-label="Dataset parameter mappings">{parameters.map(parameter => <button type="button" className="btn btn-sm btn-ghost" key={parameter.name} onClick={() => props.onShowParameter(props.script.parameters!.indexOf(parameter))}><code>{parameter.name}</code> → {parameter.column || 'default column'}</button>)}</div>
      {!source.inline_data?.trim() && (source.path || source.storage_ref || source.staged_asset) ? <p role="status">External source: <code>{source.path || source.storage_ref || source.staged_asset}</code>. Contents are not loaded here. Attach an inline copy to inspect it; generator availability is checked by execution preflight.<label className="btn btn-secondary file-btn">Attach inline copy<input type="file" accept=".csv,.dat,.txt" onChange={event => { props.onImportDataSourceFile(index, event.target.files?.[0] || null); event.currentTarget.value = '' }} /></label></p> : <ParameterDataEditor
        {...props}
        key={`${index}:${source.name}`}
        index={index}
        source={source}
        dataEditorMode={props.dataEditorMode[index] || 'grid'}
        history={props.dataSourceHistory[index]}
        parsed={parseInlineData(source)}
        summary={summarizeInlineData(source)}
        warnings={validateInlineData(source)}
      />}
    </>}
  </section>
}
