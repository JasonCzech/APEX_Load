import type { AnalysisMapping, AnalysisPrecisionReview } from '../types/analysis'

export default function AnalysisPrecisionPanel({ mapping, review, acknowledged, reviewDisabled, onMappingChange, onReview, onAcknowledge }: {
  mapping: AnalysisMapping; review?: AnalysisPrecisionReview; acknowledged: boolean; reviewDisabled: boolean
  onMappingChange: (mapping: AnalysisMapping) => void; onReview: () => void; onAcknowledge: (value: boolean) => void
}) {
  const allow = mapping.version === 3 && mapping.precision_policy === 'allow_float64_rounding'
  return <fieldset className="analysis-precision-panel">
    <legend>Numeric precision</legend>
    <p>Analysis converts numeric measurements to floating-point values. Original Parquet files remain unchanged. Review all uploaded files before importing.</p>
    <label>Decimal conversion<select value={allow ? 'allow_float64_rounding' : 'reject_potential_loss'} onChange={event => onMappingChange({ ...mapping, version: 3, precision_policy: event.target.value as AnalysisMapping['precision_policy'] })}>
      <option value="reject_potential_loss">Reject potential rounding</option>
      <option value="allow_float64_rounding">Allow rounding after review</option>
    </select></label>
    <button type="button" className="btn btn-secondary btn-sm" disabled={reviewDisabled} onClick={onReview}>Review precision</button>
    {review && <>
      <p role="status">{review.review.risks.length ? 'The schema indicates potential decimal rounding. This does not count rows whose values actually change.' : 'No potential decimal rounding was found in the mapped numeric columns.'} Reviewed {review.review.source_count} source {review.review.source_count === 1 ? 'file' : 'files'}.</p>
      {!!review.review.risks.length && <>
        <ul>{review.review.risks.map((risk, i) => <li key={i}>Column {risk.column.join('.')} — DECIMAL({risk.precision}, {risk.scale}); source digest <code>{risk.file_sha256.slice(0, 12)}</code>.</li>)}</ul>
        {allow ? <label><input type="checkbox" checked={acknowledged} onChange={event => onAcknowledge(event.target.checked)} />I accept potential rounding for these source files and this mapping</label> : <p>Choose “Allow rounding after review” and review again to import these measurements.</p>}
      </>}
    </>}
  </fieldset>
}
