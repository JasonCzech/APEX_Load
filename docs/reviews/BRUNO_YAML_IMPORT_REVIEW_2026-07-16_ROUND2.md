# YAML / Bruno import — re-review round 2 (2026-07-16)

Follow-up to [BRUNO_YAML_IMPORT_REVIEW_2026-07-16.md](BRUNO_YAML_IMPORT_REVIEW_2026-07-16.md). Another agent
applied a large fix pass (~4,100 changed lines across the feature). This round re-verified all 36 original
findings against the current code and scanned the fix hunks for regressions. Method: 7 area agents (fix-status +
regression scan), then a confirmation pass on the non-obvious new regressions. Several fixes were re-verified
**empirically** against the real store/importer.

## Bottom line
- **All 3 P0s are fixed** (CASCADE-1, SECRETLIST-1, AUTHZ-1) — each re-verified empirically.
- **~30 of 36 findings fully fixed**, 5 partial, 1 left as cleanup-only.
- **The fix pass introduced 6 confirmed new bugs** (1 security, 1 functional, plus data-loss/availability), and
  **partially reopened the secret-scrubbing P0** for one file class. These are the actionable items now.

---

## New regressions introduced by the fix pass (ranked — action needed)

### R1 — Parseable "asset" files with nested credentials leak into the sanitized bundle (partial P0 reopen)
`pkg/convert/bruno/inspect.go:395` · **CONFIRMED (empirically demonstrated)** · security
The SANITIZE-ABORT-1 fix added a role gate so structured sanitization runs only on non-asset `.json`/`.yml`
files. Consequence: a *parseable* asset file with a nested credential is never scrubbed. Demonstrated:
`data/creds.json` = `{"auth":{"password":"HUNTER2LEAK"}}` and `config.yaml` = `oauth: {client_secret: …}` both
appear **verbatim** in the viewer-downloadable `result.SanitizedArchive` with `SecretRequirements==0`; the old
code redacted them. This re-opens the SECRETLIST P0 class for the asset subset. **Better fix:** run structured
sanitization for asset role too, but treat a parse error as pass-through (`continue`) rather than aborting — that
fixes SANITIZE-ABORT-1 without dropping sanitization coverage.

### R2 — Request-body fields named `username`/`token`/`secret`/`pass` are over-redacted, breaking replay
`pkg/convert/bruno/inspect.go:656` · **CONFIRMED (empirically demonstrated)** · correctness
The SECRETLIST-1 fix treats a structured request-body context as a credential context, and
`case "username": return credentialContext` now redacts a legitimate body username. Demonstrated: body
`{json:{username:jason, comment:hello}}` → `username` becomes `{{secret.COL_LOGIN_BODY_USERNAME}}` (the value
`jason` is gone), `comment` preserved. The replayed request then sends an unbound/empty secret instead of the
real username — breaking login-style requests. Same over-redaction hits any body field literally named
`token`/`secret`/`pass`/etc. **Fix:** don't treat non-sensitive identifier fields (username) as secrets inside a
request body, or only redact body fields whose *values* look secret.

### R3 — Global single-slot inspect semaphore serializes all tenants (availability)
`pkg/api/handlers_bruno_import.go:129` · **CONFIRMED** · availability
Apparently added to mitigate the UPLOAD-1 memory-DoS: `var brunoInspectConcurrency = make(chan struct{}, 1)` is a
**process-global, cap-1** semaphore acquired at handler entry and held across the full body read/decode (up to
~272 MiB via `io.ReadAll`), inspect, and all storage Puts — and `http.MaxBytesReader` is set *after* the slot is
taken. One slow or large (or slowloris) upload blocks **every other tenant's** inspect until their request
context deadline → 408. This trades a memory-DoS for a cross-tenant serialization-DoS. **Fix:** bound concurrency
per-project with a small N>1, set `MaxBytesReader` before acquiring, and/or stream to a temp spool instead of
buffering.

### R4 — `cleanupUnreferencedBrunoArtifacts` omits the live-session reference check (cross-session data loss)
`pkg/api/handlers_bruno_import.go:1842` · **CONFIRMED** · data-loss (residual of DEDUP-DEL-1)
The DEDUP-DEL-1 fix added `IsBrunoArtifactBackendKeyReferenced` (committed rows) but omits
`IsSessionRelatedObjectReferenced` (other live sessions) — which both sibling cleanup paths
(`cleanupBrunoImportSessionArtifacts:1812`, `pruneExpiredBrunoImportSessions:1574`) do call. Because backend keys
are content-addressed and dedup across sessions, inspect B of the same raw bytes as an uncommitted session A
dedups to A's blob; if B's sanitized Put fails, cleanup deletes the blob A still needs. **Broader than the
original:** a second caller, `handlers_scripts.go:459` (script-delete cleanup), shares the same omission, so a
committed-script delete can also delete a blob a live session references. **Fix:** add the
`IsSessionRelatedObjectReferenced` check to `cleanupUnreferencedBrunoArtifacts`.

### R5 — Orphaned `.claim.json` (and its secret payload) never garbage-collected
`pkg/brunoartifact/encrypted_store.go:1037` · **CONFIRMED** · leak
The CLAIM-GLOB-1 fix makes `DeleteExpiredSessions` skip `*.claim.json` unconditionally, with no age/staleness
fallback. A controller crash during `ConsumeSession` (after the descriptor is removed at :752, before the claim
is removed at :776) orphans `<id>.claim.json` plus its encrypted session payload (which holds source secret
values) and related blobs. The periodic janitor only acts on `.json` descriptors, so the orphan is never swept —
only a whole-project delete or an explicit session delete reclaims it. **Fix:** in the enumerator, treat a claim
older than the session TTL as reclaimable (delete the claim + its related objects).

### R6 — A capacity-release error after a successful consume loses the import and leaks the slot
`pkg/brunoartifact/encrypted_store.go:799` · **CONFIRMED** · data-loss
In `ConsumeSession`, the descriptor/claim/payload are all removed before `releaseSessionCapacity` runs; if that
returns an error, the code clears and discards the already-read payload and returns `nil, err`. The caller sees a
non-`ErrNoRows` error and the commit handler returns 409 `bruno_import_session_claimed` — but the session is
already destroyed, so the import is unrecoverable, and the capacity slot (whose removal just failed) leaks until
its TTL. **Fix:** return the successfully-read payload even if the capacity release fails (log the release error);
release capacity before the destructive removals, or make it best-effort.

### Low-severity new regressions
- **R7** `pkg/protocol/bruno/bruno.go:398` — the TIMEOUT-1 fix removed the only client-side per-action deadline;
  a runner that accepts `/execute` then stalls mid-stream now blocks the VUser until the whole test-duration
  deadline instead of a per-action timeout, and a per-request `stream_timeout_ms` override is ignored
  client-side. Not an infinite hang (bounded by test duration) and mitigated if the runner honors
  `default_stream_timeout_ms`. **CONFIRMED, latent.**
- **R8** `pkg/brunoartifact/resolver.go:45` — `digestVerifyingReadCloser` verifies the SHA only on `err == io.EOF`
  (direct `==`), and `Close()` doesn't verify, so a future exact-size/partial-read consumer would bypass the
  check. No current consumer triggers it. **Latent.**
- **R9** `runners/bruno/src/worker.js:1074` — `cancelledRequestIDs` can retain dead ids (a disconnect firing
  after the execute `finally` re-adds an id nothing later removes); race-gated, memory-growth only. **CONFIRMED,
  low.**
- **R10** `dashboard/src/components/BrunoImport.tsx:93` — the fix hunks introduced literal-tab indentation into a
  2-space file; compiles fine, fails only if CI runs `prettier --check` / eslint indent. **Cleanup.**

---

## Original findings — fix verification

### Fully fixed (re-verified; ★ = empirically re-verified)
CASCADE-1 ★, SECRETLIST-1 ★, AUTHZ-1 ★, SYNC-MERGE-1, ROOT-1, GRPC-POLICY-1, TIMEOUT-1, ZIP-1 ★, DATAROWS-1,
PARALLEL-ENV-1, CONSUME-EARLY-1, CONSUME-DESTROY-1, MUTEX-KMS-1, ONCE-1, MUTEX-1, INIT-CACHE-1, PG-INT4-1,
DSL-VALIDATE-PATHS-1, HB-PROBE-1, CLAIM-GLOB-1, LIFECYCLE-DUP-1, SPECIALCASE-1, ROUTE-NS-1, WS-MISROUTE-1,
PREFLIGHT-1, BRUENV-1 ★, SANITIZE-ABORT-1, TIMER-1, CANCEL-1, AAD-1, SHA-SKIP-1, MIGRATE-DIVERGE-1,
MIGRATE-BRANCHDUP-1, PARAMIZE-1, CLONE-1, FMTBYTES-1, CHOOSER-TEST-1, FNE-1.

Notable fix details:
- **CASCADE-1** — the cascade + optimistic-concurrency guard + orphan-artifact cleanup now live in
  `DeleteScriptIfUnchanged` (the production path). Empirically: delete returns `deleted=true`, no FK error, rows
  and blobs removed, stale `updated_at` is a safe no-op, shared content-addressed artifacts preserved while still
  referenced.
- **SECRETLIST-1** — the three lists were unified onto `pkg/scrub`, `bruSecretLinePattern` expanded, and the
  `variables:[{name,value}]` shape is now scrubbed. (But see R1/R2 for the over- and under-reach the new logic
  introduced.)
- **AUTHZ-1** — `reconcileImportProjectID(query, body)` now 400s on mismatch on both the JSON and multipart paths.
- **ROUTE-NS-1 / SPECIALCASE-1** — bruno endpoints moved to `/bruno-imports/*` + `/bruno-projection/*`; the RPC
  service now dispatches through `protocol.RuntimeCapabilityProvider` / `RuntimeHealthChecker` interfaces instead
  of by-name special cases.

### Partially fixed
- **MISSING-1** `handlers_bruno_import.go:2056` — `missing` is *still* never appended, so the 409
  `secret_bindings_required` is still unreachable and an unbound suggested-name secret still commits (201). The
  failure is now caught at **run-preflight** (before runner-session creation), not at commit — loud, not silent,
  so no data harm, but the intended fail-closed-at-commit contract still isn't met, and the dead
  `bindBrunoImportSecrets` helper remains.
- **DEDUP-DEL-1** — committed-script trigger closed; the cross-session residual is now tracked as **R4** above.
- **PROTO-PARSE-1** `generatorrpc/service.go:2456` — `scriptProtocol` now parses only a single-field header (the
  heavy full-parse is gone), but it still runs under the `s.mu` write lock in `cacheScriptLocked`, so an O(n)
  tokenization of multi-MB content still blocks Heartbeat/AssignWork. Derive the protocol before acquiring the
  lock to fully close it.
- **KEYWRAP-COUPLE-1** — the env vars were renamed to protocol-neutral `APEXLOAD_WRAPPING_*` and the impl moved to
  `pkg/keywrap` (addresses the ops-naming half), but the Go config field `cfg.BrunoArtifactWrapper` is still
  bruno-named. Altitude residual. (The rename is safe — the whole feature is new/untracked, so there are no
  already-deployed secrets wrapped under the old names to break.)
- **METADUP-1** — `parseBRUMetaFields` was extracted and reused in two sites, but `collectionIdentity` still
  inlines the meta-parse loop.

### Not fixed (cleanup only)
- **WORKER-DUP-1** — `executeGRPC` / `executeWebSocket` remain near-duplicate; no correctness impact.

---

## Suggested next actions (priority order)
1. **R1** — restore sanitization coverage for asset-role files (parse-error → pass-through, not skip). Security.
2. **R2** — stop redacting non-secret request-body identifiers (username). Breaks replay.
3. **R3** — replace the global cap-1 inspect semaphore with per-project N>1 + pre-acquire `MaxBytesReader`.
4. **R4** — add the `IsSessionRelatedObjectReferenced` check to `cleanupUnreferencedBrunoArtifacts` (2 callers).
5. **R5 / R6** — claim staleness reclamation; don't discard a consumed payload on capacity-release error.
6. **MISSING-1** — actually populate `missing` (or delete the dead 409 path + dead helper) so the contract is honest.
7. **R7–R10, PROTO-PARSE-1, METADUP-1, KEYWRAP field rename** — low-severity / cleanup.
