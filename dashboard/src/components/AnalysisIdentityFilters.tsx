import type { AnalysisIdentityFilters as IdentityFilters } from '../types/analysis'
import { analysisIdentityFilterError, analysisIdentityFilterShapeError, emptyAnalysisIdentityFilters } from '../utils/analysisIdentity'

export default function AnalysisIdentityFilters({ value, onChange, label = 'Identity filters' }: { value: IdentityFilters; onChange: (filters: IdentityFilters) => void; label?: string }) {
  const shapeError = analysisIdentityFilterShapeError(value)
  if (shapeError) return <fieldset className="analysis-identity-filters" aria-label={label}><legend>{label}</legend><p role="alert">{shapeError}</p><button type="button" onClick={() => onChange(emptyAnalysisIdentityFilters())}>Reset identity filters</button></fieldset>
  const error = analysisIdentityFilterError(value)
  return <fieldset className="analysis-identity-filters" aria-label={label}>
    <legend>{label}</legend>
    <p>Select one source and label combination when a metric has several series. An empty dimension value matches an explicit empty label.</p>
    {error && <p role="alert">{error}</p>}
    {(['source_id', 'host', 'service'] as const).map(field => <label key={field}>{field === 'source_id' ? 'Source ID' : field === 'host' ? 'Host' : 'Service'}<input value={value[field]} maxLength={256} onChange={event => onChange({ ...value, [field]: event.target.value })} /></label>)}
    {value.dimensions.map((dimension, index) => <div key={index}>
      <label>Filter {index + 1} name<input value={dimension.name} maxLength={128} onChange={event => onChange({ ...value, dimensions: value.dimensions.map((row, at) => at === index ? { ...row, name: event.target.value } : row) })} /></label>
      <label>Filter {index + 1} value<input value={dimension.value} maxLength={128} onChange={event => onChange({ ...value, dimensions: value.dimensions.map((row, at) => at === index ? { ...row, value: event.target.value } : row) })} /></label>
      <button type="button" onClick={() => onChange({ ...value, dimensions: value.dimensions.filter((_, at) => at !== index) })}>Remove filter {index + 1}</button>
    </div>)}
    <button type="button" disabled={value.dimensions.length >= 16} onClick={() => onChange({ ...value, dimensions: [...value.dimensions, { name: '', value: '' }] })}>Add dimension filter</button>
  </fieldset>
}
