import { analysisProtectedIdentityColumn } from '../utils/analysisCredentialColumn'
import type { AnalysisColumn, AnalysisIdentityMapping as IdentityMapping, AnalysisMapping } from '../types/analysis'
import { analysisIdentityMappingError, analysisIdentityMappingShapeError, analysisIdentityTextError } from '../utils/analysisIdentity'

export default function AnalysisIdentityMapping({ mapping, columns, onChange }: { mapping: AnalysisMapping; columns: AnalysisColumn[]; onChange: (mapping: AnalysisMapping) => void }) {
  const shapeError = analysisIdentityMappingShapeError(mapping.identity)
  if (shapeError) return <fieldset aria-label="Imported measurement identities"><legend>Source and labels</legend><p role="alert">{shapeError}</p><button type="button" className="btn btn-ghost btn-sm" onClick={() => onChange({ ...mapping, version: 2, identity: {} })}>Reset identity mapping</button></fieldset>
  // Schema previews share the worker's bounded 1024-column contract. Refuse
  // malformed options before serializing paths or creating controls.
  if (!Array.isArray(columns) || columns.length > 1024 || columns.some(column => !column || typeof column !== 'object' || !!analysisIdentityTextError(column.path, 8207, true) || analysisIdentityMappingShapeError({ source: column.path_components }) || !column.path_components?.length)) return <fieldset aria-label="Imported measurement identities"><legend>Source and labels</legend><p role="alert">Column preview is invalid or exceeds the supported limit. Reload the schema preview.</p></fieldset>
  const identity = mapping.identity ?? {}
  const dimensions = identity.dimensions ?? []
  const scalarColumns = columns.filter(column => !column.repeated)
  const update = (next: IdentityMapping) => onChange({ ...mapping, version: 2, identity: next })
  const options = scalarColumns.map(column => <option key={column.path} disabled={analysisProtectedIdentityColumn(column.path_components)} value={JSON.stringify(column.path_components)}>{column.path}{analysisProtectedIdentityColumn(column.path_components) ? " (protected)" : ""}</option>)
  const error = analysisIdentityMappingError(identity)
  return <fieldset aria-label="Imported measurement identities">
    <legend>Source and labels</legend>
    <p>Map identities to select separate metric series. Project and dataset ownership comes from this import. Protected credential columns cannot become identity labels.</p>
    {error && <p role="alert">{error}</p>}
    {(['source', 'host', 'service'] as const).map(field => <label key={field}>{field === 'source' ? 'Source column' : field === 'host' ? 'Host column' : 'Service column'}<select value={JSON.stringify(identity[field] ?? [])} onChange={event => update({ ...identity, [field]: JSON.parse(event.target.value) })}>
      <option value="[]">{field === 'source' ? 'Use dataset identity' : 'No column'}</option>{options}
    </select></label>)}
    {dimensions.map((dimension, index) => <div key={index}>
      <label>Dimension {index + 1} name<input value={dimension.name} maxLength={128} onChange={event => update({ ...identity, dimensions: dimensions.map((row, at) => at === index ? { ...row, name: event.target.value } : row) })} /></label>
      <label>Dimension {index + 1} column<select value={JSON.stringify(dimension.value)} onChange={event => update({ ...identity, dimensions: dimensions.map((row, at) => at === index ? { ...row, value: JSON.parse(event.target.value) } : row) })}><option value="[]">Choose a scalar column</option>{options}</select></label>
      <button type="button" className="btn btn-ghost btn-sm" onClick={() => update({ ...identity, dimensions: dimensions.filter((_, at) => at !== index) })}>Remove dimension {index + 1}</button>
    </div>)}
    <button type="button" className="btn btn-secondary btn-sm" disabled={dimensions.length >= 16} onClick={() => update({ ...identity, dimensions: [...dimensions, { name: '', value: [] }] })}>Add dimension</button>
  </fieldset>
}
