# Lease-bound replay artifact publisher — 2026-10-21

This BE-B slice implements the controller-side publisher behind the optional replay publication port. It remains passive until controller admission, recovered HTTP authorization and exact cleanup wiring are completed. It does not enable restart downloads or certify live cloud storage.

## Contracts

The publisher binds session, project, lease owner and backend authority independently of the supplied terminal snapshot. It rejects mismatched session/report identity, missing terminal report/completion, nonterminal state, invalid HAR length and reports exceeding 16 MiB before writing artifacts. Native HAR input is streamed for hashing with a 256 MiB limit, rewound, then independently checked by immutable staging. Every attempt uses fresh cryptographic generation entropy.

Receipt admission precedes physical staging. Only successful verified staging supplies writer-completion proof. That proof is recorded with an independent bounded context even when caller cancellation prevents publication, permitting exact-generation cleanup later. Final publication still requires the original unexpired retained owner and project through the storage CAS.

Unknown reservation, staging, join or publication outcomes never trigger speculative deletion or namespace release. Failed staging/unknown join keeps the reservation counted and blocks a successor session ID. A verified writer whose lease is lost becomes cleanable without becoming downloadable. Publication errors preserve the execution result through the bridge port's separate durability result.

## Review

Review tightened the port documentation to distinguish joined input readers from ambiguous remote writers, whose durable ownership must survive an error return. Regression coverage was expanded for report identity/size boundaries and for join acknowledgement loss and cancellation after verified staging. Follow-up review of input authority, byte bounds, quotas, generation ownership, cancellation and cleanup found no remaining findings, including low severity.

Filesystem fixtures use real SQLite receipts and the real immutable backend; injected boundary failures wrap the actual store implementation. These tests do not substitute for Salesforce workflow, real TruClient, native platform or external HA qualification.

## Validation

- Final targeted publisher/backend checks: 51 passing test/subtest events.
- Three race-detector repetitions of publisher/backend checks: 147 passing events. The two report identity/size cases added after that build began pass in the final normal run; production publisher code is unchanged between these runs.
- Documentation checks pass for 320 active Markdown files; whitespace checks pass.
- Logs and reviewed source hashes are retained locally under `/private/tmp/apex-browser-durable-backend/`: `publisher-final-targeted.jsonl`, `publisher-race.jsonl`, `publisher-reviewed-source-hashes.json`, and `publisher-docs.log`.

The earlier broad Go run timed out in the API package under concurrent compilation, with no assertion failures before timeout. It predates this publisher's integration and is not a full-suite pass for this slice. Serial final-source validation remains required before merge. The private HAR import baseline and unchanged embedded assets were checked in the preceding publication-port slice; this passive publisher adds no import or dashboard behavior.
