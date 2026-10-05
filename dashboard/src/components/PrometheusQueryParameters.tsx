import type { PrometheusParameter } from '../types/prometheus'

export default function PrometheusQueryParameters({ queryIndex, parameters = {}, onChange }: { queryIndex: number; parameters?: Record<string, PrometheusParameter>; onChange: (parameters: Record<string, PrometheusParameter>) => void }) {
  const entries = Object.entries(parameters)
  const update = (index: number, name: string, parameter: PrometheusParameter) => {
    if (entries.some(([existing], i) => i !== index && existing === name)) return
    onChange(Object.fromEntries(entries.map((entry, i) => i === index ? [name, parameter] : entry)))
  }
  const help = 'Saved bindings are frozen with the query definition. Use an entire token such as job={{param.job}}, [{{param.lookback}}], timestamp(up) > {{param.since}}, or up @ {{param.at}}. Label values are quoted by APEX; do not add surrounding quotes. Bindings cannot insert PromQL operators or metric names. Request-relative @ start()/end() anchors are unsupported; exact terminal windows reject fixed anchors too.'
  return <fieldset aria-label={`Query ${queryIndex} typed parameters`}><legend>Typed query bindings</legend><p>{help}</p>
    {entries.map(([name, parameter], index) => <fieldset key={index}><legend>Parameter {index + 1}</legend>
      <label>Query {queryIndex} parameter {index + 1} name<input value={name} maxLength={64} onChange={event => update(index, event.target.value, parameter)} /></label>
      <label>Query {queryIndex} parameter {index + 1} type<select value={parameter.type} onChange={event => update(index, name, { ...parameter, type: event.target.value as PrometheusParameter['type'] })}><option value="label_value">Label value</option><option value="duration">Duration</option><option value="timestamp">UTC timestamp</option></select></label>
      <label>Query {queryIndex} parameter {index + 1} value<input value={parameter.value} maxLength={256} placeholder={parameter.type === 'duration' ? '2m' : parameter.type === 'timestamp' ? '2026-10-03T12:00:00Z' : 'checkout'} onChange={event => update(index, name, { ...parameter, value: event.target.value })} /></label>
      <button type="button" onClick={() => onChange(Object.fromEntries(entries.filter((_, i) => i !== index)))}>Remove query {queryIndex} parameter {index + 1}</button>
    </fieldset>)}
    <button type="button" disabled={entries.length >= 16} onClick={() => { let next = 1; while (Object.prototype.hasOwnProperty.call(parameters, `param_${next}`)) next++; onChange({ ...parameters, [`param_${next}`]: { type: 'label_value', value: '' } }) }}>Add query {queryIndex} parameter</button>
    <p>Duration and timestamp values are parsed by the provider adapter; the resolved expression is bounded and validated as PromQL. These are administrator-selected static bindings, not credentials.</p>
  </fieldset>
}
