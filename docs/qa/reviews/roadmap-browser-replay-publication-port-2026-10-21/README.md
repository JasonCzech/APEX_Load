# Terminal replay publication port — 2026-10-21

This BE-B slice adds the optional lease-bound publication boundary to the shared replay manager. It does not enable controller persistence: the controller publisher, recovered authorized reads and exact cleanup worker remain separate work. Existing local bridges keep their terminal-fence behavior.

## Contracts

- Publication receives a detached, normalized terminal session/report and the joined native HAR reader. No synthetic HAR substitutes for missing native bytes. Readers remain owned by the worker until the synchronous publisher returns.
- Publication runs outside manager/session locks with a two-minute context deadline. The adapter must honor cancellation and stop using its input readers before returning. Ambiguous remote writer outcomes retain durable cleanup identity and quota; context expiry alone never establishes writer completion.
- Downloads return 409 with retry guidance while publication is pending. Completion callbacks run only after the publication decision. A failed publisher does not change the replay's successful action result.
- Optional capability results distinguish local-only, pending, durable and unavailable. This slice exposes these to adapters, not as a new HTTP/UI contract. A nil publisher remains local-only.
- A late publisher cannot settle an abandoned/replaced session. Execution epoch or retained-lease loss during publication suppresses the report and retires the local capability; the controller adapter must additionally enforce storage ownership at publication CAS.

## Review

The first review found that cancellation reconciliation treated pending publication as an already-settled terminal fence. The fix suppresses pending report payloads after epoch loss and adds a regression. Further review preserved the original terminal-fence behavior for adapters without a publisher and stopped-before-start sessions, narrowing cancellation reconciliation to explicitly pending publication. Tests also exercise HTTP download ordering, detached snapshot mutation, native-only publication, storage failure, lease loss, abandonment and same-ID replacement.

A final review of lifecycle ordering, snapshot isolation, lock boundaries, native ownership, cancellation, nil capability compatibility and regression coverage found no remaining findings, including low severity.  Native Salesforce qualification and restart/HA qualification are not claimed by these deterministic port fixtures.

## Validation

Main `ceb3f9b83` was current before cross-package validation. Final reviewed source passes all bridge tests under three race-detector repetitions (381 passing test/subtest events) and the controller's replay handler, session, artifact, HAR and lifecycle regressions (94 passing events; one conditional skip). The supplied private HAR import separately passes and its stable summary matches the retained baseline, excluding randomized script identity. Its target requests were never replayed.

Documentation checks pass for 319 active Markdown files; whitespace checks pass. All 311 previously tested embedded dashboard assets remain unchanged. Raw local logs live under `/private/tmp/apex-browser-durable-backend/`: `publication-reviewed-race.jsonl`, `publication-reviewed-api.jsonl`, `publication-final-api.jsonl`, and `publication-private-har-summary.json`. `publication-reviewed-source-hashes.json` pins the final reviewed source. No dashboard assets or runtime browser qualification changed.

The broader Go suite was also started in this cycle, before the final local-only/stopped-before-start compatibility fixes. It reached 32,891 passing events and 42 conditional skips, then the API package hit its 20-minute timeout under concurrent compilation, without an assertion failure. The final bridge and controller checks above qualify the compatibility fixes. This is not a passing full-suite gate; serial final-source validation remains required before merge.
