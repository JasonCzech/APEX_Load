import type { ScriptResultsConfig } from '../types/scripts'
type Rule = NonNullable<ScriptResultsConfig['property_analytics']>[number]
export default function PropertyAnalyticsFields({ value = [], onChange }: { value?: Rule[]; onChange: (value: Rule[]) => void }) {
 const patch = (index: number, update: Partial<Rule>) => onChange(value.map((item, i) => i === index ? { ...item, ...update } : item))
 return <fieldset><legend>Response property analytics</legend><p>Promote only non-sensitive categories. Approved values are retained as labels; other values aggregate into “other” and absent extractions into “missing”. Maximum four properties and sixteen categories each.</p>
 {value.map((rule, index) => <div key={index}><label>Property name<input maxLength={64} value={rule.name} onChange={event => patch(index, { name: event.target.value })} /></label><label>Extraction key<input maxLength={64} value={rule.extraction} onChange={event => patch(index, { extraction: event.target.value })} /></label><label>Approved categories (comma separated)<input maxLength={1039} value={rule.values.join(',')} onChange={event => patch(index, { values: event.target.value.split(',').map(item => item.trim()) })} /></label><button type="button" onClick={() => onChange(value.filter((_, i) => i !== index))}>Remove property {index + 1}</button></div>)}
 <button type="button" disabled={value.length >= 4} onClick={() => onChange([...value, { name: '', extraction: '', values: [] }])}>Promote extraction</button>
 </fieldset>
}
