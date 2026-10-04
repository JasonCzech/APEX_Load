import type { AuraPayloadCoverage } from '../types/scriptImports'

export default function AuraParameterizationCoverage({ coverage }: { coverage: AuraPayloadCoverage }) {
  return <section className="paramize-aura-coverage" aria-label="Aura payload inspection">
    <h5>Aura payload inspection</h5>
    <p>{coverage.inspected_bodies} of {coverage.recognized_bodies} recognized request bodies inspected. {coverage.complete ? 'Inspection complete.' : 'Inspection incomplete.'}</p>
    <p>{coverage.string_fields} string fields checked; {coverage.candidate_fields} candidate fields, {coverage.retained_candidates} retained candidates.</p>
    {!coverage.complete && <p className="paramize-coverage-warning">Some payloads or fields could not be fully inspected. Review the coverage limits before replay.</p>}
    {(coverage.rejected_bodies > 0 || coverage.omitted_bodies > 0) && <p className="paramize-coverage-warning">{coverage.rejected_bodies} request bodies rejected; {coverage.omitted_bodies} request bodies not fully inspected.</p>}
    {(coverage.unsupported_number_fields > 0 || coverage.unsupported_other_fields > 0) && <p className="paramize-coverage-warning">Only string fields support decoded parameterization. {coverage.unsupported_number_fields} numeric fields and {coverage.unsupported_other_fields} boolean, null, or other fields remain unchanged.</p>}
    {coverage.unsafe_path_fields > 0 && <p className="paramize-coverage-warning">{coverage.unsafe_path_fields} fields excluded because a safe structural path could not be published.</p>}
    {coverage.omitted_candidates > 0 && <p className="paramize-coverage-warning">{coverage.omitted_candidates} candidate fields omitted by review limits.</p>}
  </section>
}
