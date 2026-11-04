## APEX Load: PASS (not blocking): Checkout load

regression_vs_baseline · Baseline: run-0 (comparison cmp-1)

The run met its acceptance criteria; 1 finding to review.

| Scope | Samples | p95 ms | Δ vs base | Err % | SLA |
|---|---|---|---|---|---|
| overall | 10000 | 260 | — | 0.2 | — |
| Checkout | 4000 | 250 | +31.58% | 0.25 | pass (300) |
| Browse | 6000 | 180 | — | 0.17 | pass (300) |

### Findings

1. **[major] fnd_reg_6dfac97e19fa**: successful_p95 of Checkout regressed to 250 ms from baseline 190 ms (new, high)
   evidence: /tests/run-1/baseline-comparisons/cmp-1 · next: apex_get_finding(comparison_id=cmp-1, finding_id=fnd_reg_6dfac97e19fa, run_id=run-1)

brief_digest a8bd818171960cf57eaf23e171f0161c99f6b10cd4a9836fcba88d9a484230a2 · schema apexload.agent_brief.v1
