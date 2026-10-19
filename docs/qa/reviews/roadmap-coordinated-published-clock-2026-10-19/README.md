# Roadmap implementation: coordinated starts across published-clock drift

Date: 2026-10-19. Scope: a bounded [TEL-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-a--durable-windows-and-measured-end-to-end-age) clock-domain correction. Base: `dd397bbd7` with upstream `609bf1b1a` integrated.

## Finding and contract

The integrated full Go run encountered a roughly 39-minute wall/monotonic discontinuity. Two real-generator adoption tests failed because a heartbeat-derived offset was added to the controller's deadline and then compared directly with generator wall time. That offset describes the generator's monotonic published clock, which intentionally remains anchored while work is assigned. The elapsed-barrier refusal was correct; the conversion into its clock domain was wrong. The failed full run is retained, not marked passed or dismissed as a flaky test.

At RPC receipt, the generator now maps the controller barrier into its published clock, obtains the remaining interval there, then expresses that interval as a local wall-clock deadline. Transit cannot extend the common deadline. Retries keep the once-translated deadline. The active telemetry anchor, elapsed/far-future refusals, epoch/supervision fences, wire format and legacy relative-time path remain unchanged.

Deterministic tests cover positive/negative 39-minute, five-second and subsecond differences, elapsed deadlines, asymmetric transit and input/anchor non-mutation. A real gRPC heartbeat probe and real HTTP generator execution verify the resulting deadline within the measured probe uncertainty. This does not qualify all possible later clock steps, end-to-end telemetry latency, real multi-host recovery or reference environments.

## Validation and review

Draft overlay regressions fail on the original code and pass with the correction. Real execution, existing barrier tests and focused race checks passed against the draft. The applied-source generator package passed (47.892 s), focused race checks passed (6.114 s), and both original adoption regressions passed three consecutive runs (19.541 s). Four native analytics API compatibility workflows passed (24.548 s). The corrected full Go suite passed all 168 tested packages, with 10 skipped/no-test packages and no failures; see [package results](evidence/corrected-full-go-results.json). The generated telemetry inventory, documentation/help checks and whitespace checks passed. Final source, safety-fence, test-cleanup and scoped-diff review found no further actionable issues. The unavailable reference and multi-host lanes remain pending. The initial full-suite failure is recorded in [package results](evidence/initial-full-go-results.json).
