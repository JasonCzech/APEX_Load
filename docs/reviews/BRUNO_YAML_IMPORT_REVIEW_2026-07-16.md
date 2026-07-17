# YAML / Bruno import — code review (2026-07-16)

Scope: the uncommitted Bruno/YAML import feature — `pkg/convert/bruno`, `pkg/protocol/bruno`,
`pkg/brunoartifact`, `pkg/api/handlers_bruno_import.go`, `pkg/storage/*bruno*`, `pkg/dsl/catalog_bruno.go`,
`cmd/apexctl/bruno_import.go`, `runners/bruno` (Node sidecar), the dashboard `BrunoImport.tsx` /
`YAMLImportChooser.tsx` components, and the wiring in `pkg/api/server.go`,
`pkg/distributed/generatorrpc/service.go`, `pkg/storage/sqlite_schema.go`, helm/k8s manifests.

Method: xhigh multi-agent review — 10 finder angles → dedup → single-vote adversarial verification of
every candidate → gap sweep → verification of sweep candidates. 36 findings survived verification;
6 candidates were refuted and 1 (`sla_evaluated` JSON) was ruled out of scope (parallel CI change-set).

Severity legend: **P0** ship-blocker · **P1** high · **P2** medium · **P3** low/cleanup.

---

## P0 — ship blockers

### 1. Bruno scripts are undeletable via the API (hard FK failure → HTTP 500)
`pkg/storage/sqlite_scripts.go:296` · **CONFIRMED (empirically, against the real store)**
The artifact/revision cleanup cascade was added only to `DeleteScript`, but the production delete path is
`DeleteScriptDurableInProject` → `DeleteScriptIfUnchanged`, which runs only
`DELETE FROM scripts WHERE id=? AND updated_at=?`. `bruno_source_revisions.script_id REFERENCES scripts(id)`
has no `ON DELETE` action, and both dialects enforce FKs (SQLite `_pragma=foreign_keys(ON)`; Postgres
`ensurePostgresForeignKeys`). A scratch probe against `SQLiteStore` returned
`deleted=false, err=FOREIGN KEY constraint failed (787)`, so `handleDeleteScript` returns 500 and the script
cannot be deleted at all. `cleanupUnreferencedBrunoArtifacts` runs only after a successful row delete, so it
never fires. Fix: route the cascade into `DeleteScriptIfUnchanged` (or add `ON DELETE CASCADE` to the bruno FKs).

### 2. Live credentials survive into the "sanitized" bundle that project *viewers* can download
`pkg/convert/bruno/inspect.go:588` (+ `:24`, `:602`) · **CONFIRMED (empirically)**
Three different credential-name lists disagree: `pkg/scrub/credentials.go`, `isSecretKey`, and
`bruSecretLinePattern`. `pwd`/`pass`/`secret_key`/`access_key`/`passphrase` are variously missing. A `.bru`
environment with `access_token`/`passphrase`/`pwd` values passes through into `result.SanitizedArchive`
verbatim with `SecretRequirements=0`. Worse, the standard Bruno YAML env shape
`variables: [{name: password, value: ...}]` leaks everything, because the sanitizer inspects map *keys*
(`name`/`value`) rather than the value. The sanitized bundle is downloadable by project **viewers**
(`route_catalog.go:366` `viewerProjectAccess`); only `?raw=true` is `project_admin`-gated. This reopens the
secret-scrubbing P0 class from prior reviews. Fix: sanitize against the shared `pkg/scrub` list and handle the
name/value list shape.

### 3. Cross-project privilege escalation on the inspect route
`pkg/api/route_catalog.go:350` → `handlers_bruno_import.go` · **CONFIRMED**
`/scripts/import/bruno/inspect` authorizes on `resolveProjectIDFromQuery` (query only) but the handler operates
on the body's `project_id` (`firstNonEmpty(body, query)`, body wins) and never calls
`requireAuthorizedProjectMatch`. A user with a role only on project A can send `?project_id=A` with body
`project_id=B`: encrypted artifacts and the persisted import session are written under B, and
`previewBrunoSynchronization` leaks B's script `UpdatedAt` / active revision / conflict summary. Every other
importer uses `reconcileImportProjectID`, which 400s on query≠body. (Commit and migrate routes are *not*
vulnerable — they authorize the body project; source-secret *values* are separately `project_admin`-gated.)
Fix: reconcile query vs body like the other importers, or authorize the body project.

---

## P1 — high

### 4. Sync-mode commit silently discards every run-option / environment / selection edit
`pkg/api/handlers_bruno_import.go:1776` · **CONFIRMED**
`mergeBrunoSynchronization` rebuilds the committed script from the *existing* script:
`merged.Config = existing.Config`, restores `DefaultEnvironment`/`Sandbox`/`Limits`/`PersistSession`/`RunnerProfile`
from the previous script, and overwrites `Actions[0].Params` with the existing params. Only the source binding
(BundleRef/SHA) is updated. The dashboard shows those fields as editable in sync mode and sends the edits at
inspect time, so a user who changes selection/environment/execution/sandbox and commits has all of it silently
reverted — the committed revision runs with the old profile while the UI showed the new one as accepted. Fix:
carry the freshly-inspected `Config.Bruno`/`Params` through the merge; only the immutable source binding should
come from the existing script.

### 5. `collection.bru`-rooted (or marker-less) bundles import successfully, then fail every VUser at run time
`runners/bruno/src/server.js:219` · **CONFIRMED (empirically, both sides)**
The Go importer accepts `collection.bru` as a collection root (`inspect.go:720`), but the runner's
`detectCollectionRoot` recognizes only `opencollection.yml` / `bruno.json`, and its multi-file fallback requires
exactly one `.bru`/`.yml` file. So a multi-file collection rooted only by `collection.bru` (or with no marker at
all — `detectRoot` falls back to `commonDirectory`) imports and commits, then throws
"Bruno bundle has no collection root…" at `POST /v1/bundles/open`, breaking every VUser `Init`. Fix: accept
`collection.bru` as a root marker in the runner, or synthesize `bruno.json` at commit time.

### 6. gRPC target-policy check runs before scheme normalization → blocks every allow-listed scheme-less target
`runners/bruno/src/worker.js:596` · **CONFIRMED (node-verified)**
`enforceTargetPolicy(request.url)` runs on the raw URL *before* the scheme-normalization on line 597. For a
conventional gRPC target like `grpc.example.com:50051`, `new URL()` parses `grpc.example.com` as the scheme and
yields `hostname === ''`, so with any `allowed_target_hosts` set the host is always blocked; IP `host:port`
throws "cannot validate". (Moving the check after 597 still won't fix `localhost:50051` — needs host:port-aware
normalization before the check.) Fix: normalize scheme before enforcing the policy.

### 7. Whole `bruno_run` is capped at one stream timeout (default 60s)
`pkg/protocol/bruno/bruno.go:330` · **CONFIRMED**
`ExecuteStream` wraps the entire `/execute` stream — all data iterations, requests, and `delay_ms` sleeps — in a
`streamTimeout` deadline (default 60s; the operator cap `APEXLOAD_BRUNO_MAX_STREAM_TIMEOUT_MS` also defaults to
60s), and a `min()` clamp means a per-action `stream_timeout_ms` can only *shrink* it. The worker treats the same
value as a per-gRPC/WS-stream deadline. Any collection whose total wall time exceeds 60s (many CSV rows, or
`delay_ms` pacing) fails every iteration with "context deadline exceeded". Fix: separate the whole-run deadline
from the per-stream deadline; drive the run deadline from run duration, not the stream timeout.

### 8. Zip-bomb quota bypass via `uint64→int64` overflow → controller OOM
`pkg/convert/bruno/bundle.go:104` · **CONFIRMED**
`size := int64(entry.UncompressedSize64)` wraps negative for declared sizes ≥ 2^63, so the per-file check, the
compression-ratio check (`expanded<=0` returns false), and the running-total check (`total` goes negative) all
pass. `readBounded` caps only actual per-file bytes (256 MiB), never actual-vs-declared or a running actual
total, and `validateAndNormalizeFiles` re-checks only after the whole expanded slice is already in RAM. With
`MaxFiles=100_000`, a ~256 MiB upload can decompress to hundreds of GiB and OOM the controller. Fix: bound
cumulative *actual* decompressed bytes during extraction; reject declared sizes that overflow.

### 9. Empty data source → zero iterations reported as a passing run
`runners/bruno/src/worker.js:923` · **CONFIRMED**
`items.length===0` is guarded but `dataRows.length===0` is not. A headers-only CSV or `[]` JSON yields
`dataRows=[]`, the iteration loop runs zero times, `failed` stays false, and the aggregate returns
`success=true` / `executed_iterations:0` with no requests sent. `bruno.go:646` copies `Success` verbatim and
`executed_iterations` is consumed nowhere on the Go side — a silent no-op reported as passing
(request-conservation violation). Fix: treat an empty data source as an error (or at least surface it as a
zero-iteration warning that fails the sample).

### 10. Environment-variable bleed across concurrent iterations in `parallel` mode
`runners/bruno/src/worker.js:946` · **CONFIRMED**
In `execution='parallel'`, each concurrent iteration shares the *same* `envVariables`/`globalEnvVariables`/
`secretVariables` objects (only `runtimeVariables` is copied per iteration), and `bru.setEnvVar` mutates them in
place in both sandboxes. Concurrent iterations bleed env writes into each other — e.g. a per-iteration auth token
set by one iteration is interpolated by another. Fix: deep-copy env/secret maps per iteration (as the CLI does
per-run). Sequential mode is unaffected.

### 11. `secret_bindings_required` 409 is unreachable; unbound commit succeeds then fails at run start
`pkg/api/handlers_bruno_import.go:1650` · **CONFIRMED**
`bindOrMigrateBrunoImportSecrets` declares, checks, and returns `missing`, but nothing ever appends to it — every
requirement falls back to `SuggestedName` and emits `{{secret.NAME}}`. The caller's 409
`secret_bindings_required` block (`:481-488`) is therefore dead. With no matching project secret or env binding,
commit returns 201 and the script later fails at `Protocol.Init` with "unresolved Bruno secret binding(s)". The
sibling `bindBrunoImportSecrets` (`:1786`) — which *did* populate `missing` — is dead code with zero callers.
Fix: populate `missing` (or delete the dead 409 path and the dead helper, and document the run-start failure as
intended).

### 12. Commit consumes and destroys the import session *before* validation → retry impossible
`pkg/api/handlers_bruno_import.go:343` · **CONFIRMED**
`handleCommitBrunoImport` consumes the one-shot session (and a `defer` deletes its encrypted artifacts) before
any commit validation runs. Every 4xx outcome — including the 409 `bruno_secret_migration_conflict` whose body
literally tells the user to rename the binding and retry — leaves the session gone; retrying the same
`import_session_id` returns 404. The dashboard keeps the session id and re-enables the button, so the second
click 404s, forcing a full re-upload (up to 256 MiB) and re-review. (CLI re-inspects fresh, softening it there.)
Fix: validate before consuming; consume only on the success path.

### 13. Inspect-failure cleanup deletes a shared, already-committed raw artifact
`pkg/api/handlers_bruno_import.go:234` · **CONFIRMED**
`EncryptedStore.Put` is content-addressed, so re-inspecting a collection whose raw bytes match an
already-committed import dedups onto the committed script's backend key. If the subsequent sanitized `Put` fails
(client disconnect, disk-full, key-wrap error), the error path calls `objects.Delete` with **no** reference check
(unlike `cleanupBrunoImportSessionArtifacts`), deleting the shared object. The committed script's raw-source
download then 500s until someone re-uploads byte-identical content. Fix: reference-check before delete on the
inspect-failure path (reuse the existing helper).

---

## P2 — medium

### 14. `folder`/`request` selection with empty `paths` commits, then silently runs the whole collection
`pkg/dsl/catalog_bruno.go:42` · **CONFIRMED**
`validateBrunoRunAction` requires `params.paths` for `folder`/`request`, but the importer emits selection
verbatim, the UI doesn't force paths, and commit goes through a raw INSERT that bypasses DSL validation (201).
The runner then maps `folder` + empty paths to `['.']` = the whole collection (silent scope loss), and any later
promote/edit that runs `validateBrunoRunAction` rejects the stored script as unpromotable. Fix: require paths in
the converter/UI for folder/request, or validate at commit.

### 15. `ConsumeSession` destroys the session even when reading the payload fails
`pkg/brunoartifact/encrypted_store.go:556` · **CONFIRMED**
The `Delete` is gated only on `referenceErr == nil && !referenced`, not on `readErr == nil`, and the descriptor +
claim are already removed. A transient Key Vault unwrap / IO / ctx-cancel error during `ReadAll` therefore
destroys the session *and* orphans its raw/sanitized blobs (the descriptor's `RelatedObjects` — the janitor's
only GC pointer — is gone). Same-controller in-memory state mitigates partially, but a restart or a
persisted-session-after-restart leaks permanently. Fix: don't delete the payload when the read failed; restore
the claim on any error, as the reference-check path already does.

### 16. Janitor can delete a session payload out from under an in-flight consume (claim-file glob race)
`pkg/brunoartifact/encrypted_store.go:748` · **CONFIRMED**
`DeleteExpiredSessions` enumerates `*.json`, which also matches the `<id>.claim.json` O_EXCL claim file. The
claim holds the real descriptor bytes, so it parses as a valid expired descriptor; during the consume window
(`<id>.json` already removed, payload not yet read) `sessionPayloadReferenced` finds nothing referencing the
blob and the janitor deletes it. `ConsumeSession`'s `ReadAll` then fails "file not found" → spurious commit error
→ 404 on retry. The janitor runs without `brunoImportMu` and the store has no internal lock. Narrow TTL-boundary
race, but unguarded. Fix: skip `.claim.json` in the enumerator (or make it claim-aware).

### 17. `size_bytes INTEGER` overflows Postgres int4 for large imports (dialect-divergent)
`pkg/storage/sqlite_schema.go:304` · **CONFIRMED**
`rewriteSQL` has no INTEGER→BIGINT mapping, so `bruno_source_artifacts.size_bytes` is int4 on Postgres
(max 2,147,483,647). It stores `len(SanitizedArchive)` — the *uncompressed* expanded tar — and
`MaxExpandedBytes` defaults to `2<<30` = 2,147,483,648 (one byte over int4 max), env-clampable to 64 GiB. A large
import's commit INSERT fails with "integer out of range" (22003) on Postgres after inspect/review succeed, while
SQLite accepts it. Fix: make the column BIGINT (add an INTEGER→BIGINT rule for it in `rewriteSQL`, or a dedicated
DDL).

### 18. Unbounded in-memory upload buffering → controller OOM under concurrent uploads
`pkg/api/handlers_bruno_import.go:887` · **CONFIRMED**
The raw body and each multipart part are `io.ReadAll`'d into memory (256 MiB each) with no `http.MaxBytesReader`
and no concurrency semaphore, even though downstream consumers accept `io.Reader`. Concurrent max-size uploads
scale controller RSS linearly (upload buffer + rawArchive copy + expanded files slice + sanitized tar). Overlaps
#8. Fix: spool parts to a temp file with an incremental hash; add a concurrency limit.

### 19. Global bundle mutex held across the runner upload wedges ramp-up and stop/drain
`pkg/protocol/bruno/bruno.go:494` · **CONFIRMED**
`acquireBundle` holds the process-global `sharedBundles` mutex across the entire `openBundle` HTTP upload, with a
client that has no timeout. One mutex covers all keys, so different scripts/digests block too, and
`Teardown`/`releaseSharedBundle` take the same lock — a hung runner wedges cross-group `Init` and stop/drain
(the 30s teardown context can't interrupt a `sync.Mutex` wait). Fix: per-key singleflight; give the runner
client a timeout.

### 20. `brunoImportMu` held across the full `ConsumeSession` (incl. KMS unwrap) stalls the import surface
`pkg/api/handlers_bruno_import.go:1071` · **CONFIRMED**
The lock is held across file IO + a Key Vault `UnwrapKey` network call (30s client) + up-to-64 MiB decrypt + a
directory scan, so a slow KMS serializes all commits and stalls inspect-finalize, deletes, the janitor, and every
expiry callback. The O_EXCL claim file already provides consume mutual exclusion, so the lock scope is broader
than needed. Fix: narrow the lock to the map mutation.

### 21. Heartbeat pong blocks on synchronous runner health probes
`pkg/distributed/generatorrpc/service.go:2404` · **CONFIRMED**
`RunnerHealthy` does a fresh 750 ms HTTP probe with no caching, and the second probe in the pong is
unconditional. With Bruno advertised and the runner endpoint *hanging* (firewall DROP), pongs pay up to
2×750 ms; the probes are one-shot placement/inventory/evacuation calls (deadline `APEXLOAD_GENERATOR_TIMEOUT`,
default 10s), so healthy generators are marked degraded only if an operator sets that timeout ≤~2s, but the added
latency hits time-critical sequential evacuation. Fix: cache the probe result briefly; skip when Bruno isn't
advertised.

### 22. Full script-JSON parse under the write lock on every `AssignWork`
`pkg/distributed/generatorrpc/service.go:2463` · **CONFIRMED**
`activeBrunoVUsersLocked` calls `scriptProtocol(cached.content)` — a full `json.Unmarshal` of a script document
(cache limit 64 MiB) to read one field — for every group of every active assignment, under `s.mu` write lock,
which the Heartbeat pong's RLock waits behind. Fix: compute and cache the protocol once in `cacheScriptLocked`
(`cachedScript` has no protocol field today).

### 23. Per-VUser artifact open (incl. per-VUser KMS unwrap) before the cache is consulted
`pkg/protocol/bruno/bruno.go:273` · **CONFIRMED**
`Init` opens the artifact (or copies+rehashes inline bytes) per VUser *before* consulting the digest-keyed cache;
on a hit the reader is discarded unread. With streaming resolvers that means a per-VUser file open + DEK
`UnwrapKey` (a live Azure Key Vault round-trip per VUser when key-vault is configured) + ~1 MiB decrypt on every
ramp step. Fix: check the cache by digest before opening the artifact.

### 24. `sync.Once` caches a wrapper-init failure forever
`pkg/api/handlers_bruno_import.go:808` · **CONFIRMED**
A first-call failure of `NewConfiguredKeyWrapper`/`NewEncryptedStore` is cached in `sync.Once` with no reset, and
the session janitor consumes the Once at startup (always, in desktop mode). A locked macOS keychain at launch
takes down all Bruno endpoints (503) until process restart, even after the keychain unlocks. (Transient Key Vault
outages are *not* cached — real triggers are the keychain and store-dir creation.) Fix: retry on failure instead
of caching it.

### 25. Ambiguous `.json` Bruno files with alias request types misroute to the OpenAPI importer
`dashboard/src/App.tsx:153` · **CONFIRMED**
Client-side `hasBrunoSourceMarkers` hardcodes `(http|graphql|grpc|websocket|app)`, but the server accepts
`ws`/`ws-request`/`graphql-request`/`grpc-request`/`bruno-app` aliases (and any `info.type`). A `.json` Bruno
file with `info.type: 'ws'` fails the client sniff and falls through to `{launcher:'openapi'}`, dropping the user
into the OpenAPI importer with a confusing parse error. (`.yaml`/`.yml` fall to the YAML chooser, so this is
`.json`-specific.) Fix: derive the type list from the server (support-matrix), or delegate ambiguous JSON to a
server classify call.

### 26. No replay preflight for Bruno imports
`pkg/api/handlers_bruno_import.go:96` · **CONFIRMED**
Neither the inspect nor commit response carries a preflight block, and none is attached to script metadata, so
`ReplayPreflightCard` has no data for Bruno scripts — despite Bruno having the hardest runtime prerequisites
(runner sidecar, resolver, egress). The helm combo `importEnabled=true, enabled=false` is explicitly supported;
import succeeds and the user only learns the script can't run when execution fails. The support-matrix serves
`safe_execution_enabled`, but `BrunoImport.tsx` never reads it. Fix: add a Bruno-specific readiness signal
(runner/egress/execution-enabled) and surface it before commit.

### 27. One unparseable asset aborts the entire import
`pkg/convert/bruno/inspect.go:380` · **CONFIRMED**
`sanitizeFiles` dispatches by *extension*, not classified role, and any parse error aborts the whole `Inspect`
→ 422. A `fixtures/data.json` with a trailing comma (or a helm-templated, genuinely-unparseable `.yml`) makes an
otherwise-valid collection unimportable, even though that file's role is an opaque asset. Fix: only sanitize
files whose role warrants it; treat parse failures on asset-role files as pass-through.

### 28. Classic `.bru` environment variables falsely reported as missing
`pkg/convert/bruno/inspect.go:941` · **CONFIRMED (empirically)**
`environmentVariableNames` handles only `.env` and YAML shapes; a classic `.bru` environment (`vars { … }` block,
the most common export) hits `yaml.Unmarshal`, which fails, so `definedVariables` stays empty and every `{{var}}`
is reported in `MissingVariables`. Warning-only (doesn't block commit), but shown to every pure-`.bru` importer.
Fix: parse `.bru` `vars` blocks for environment files.

---

## P2 — altitude (feature bolted onto shared infra as special cases)

### 29. The general project-secret store's crypto is wired through the Bruno feature package
`pkg/api/server.go:271` · **CONFIRMED**
`pkg/secretstore/database.go` imports `pkg/brunoartifact`; the persisted secret schema *is*
`brunoartifact.WrappedKey`; the env keys are Bruno-named (`APEXLOAD_BRUNO_ARTIFACT_MASTER_KEY` /
`_KEY_VAULT_KEY_ID`); and helm hard-fails unless `projectSecrets.wrappingMasterKey == bruno.artifactMasterKey`.
A non-Bruno deployment must set Bruno env vars or the DB secret store silently degrades (slog.Warn) to the env
fallback. A future refactor of `pkg/brunoartifact` changes the schema all stored project secrets were encrypted
under. Fix: extract the keywrap/envelope primitive into a protocol-neutral package.

### 30. Bruno claims the importer-neutral `/imports/{source,revisions,rollback}` namespace
`pkg/api/server.go:740` · **CONFIRMED**
Generic paths are bound to Bruno-only handlers (source 409s on non-Bruno; `X-APEXLoad-Bruno-Revision` header),
and swagger documents them as Bruno operations. A second source-retaining importer would need a breaking change
or a forked namespace. Fix: namespace these as Bruno-specific, or make them protocol-dispatched.

### 31. Bruno is special-cased by name at five sites in the shared generator RPC service
`pkg/distributed/generatorrpc/service.go:1182` · **CONFIRMED**
Capacity gate, protocol filter, feature advertisement, heartbeat `bruno_`-prefix health filter (a hidden naming
contract), and execution precondition — the second copy of the `java_vuser` pattern. The `Protocol` interface has
no capability surface to hang this on. Fix: add availability/feature-advertisement methods to the protocol
registry.

---

## P3 — low / cleanup

- **AAD-1** `pkg/brunoartifact/wrapping.go:191` — Azure Key Vault wrapper discards the AAD, dropping the
  wrap-layer project/kind/digest binding. No swap/disclosure is possible (header pin + chunk-AEAD AAD +
  plaintext-SHA re-check still hold), so defense-in-depth only — but it's silent; add a comment / audit metadata.
- **SHA-SKIP-1** `pkg/brunoartifact/resolver.go:96` — the streaming branch skips the top-level SHA check; both
  production resolvers verify internally, so latent-only. Add a digesting tee or document that streaming
  resolvers own verification.
- **TIMER-1** `runners/bruno/src/worker.js:644` — gRPC/WS `Promise.race` deadline timers are never cleared;
  bounded (~rate×60s resident timers) rather than an unbounded leak. `clearTimeout` in `finally`.
- **HTTP-DOUBLEPREP-1** `runners/bruno/src/worker.js:501` — the request is prepared+interpolated twice per HTTP
  sample (once only to feed the policy check), even when the allow-list is empty. Skip the pre-prep when
  `normalizedAllowedHosts()` is empty.
- **MIGRATE-DIVERGE-1** `handlers_bruno_import.go:1687` — Bruno's secret-migration flow silently adopts an
  equal-valued pre-existing secret where the shared flow 409s, under the same audit action name. Reuse the shared
  `handleMigrateSourceSecrets` path.
- **LIFECYCLE-DUP-1** `handlers_bruno_import.go:1019` — three overlapping session-expiry mechanisms with the same
  4-step cleanup copy-pasted at three sites. Collapse to one `removeSession` helper + the janitor.
- **MIGRATE-BRANCHDUP-1** `handlers_bruno_import.go:1718` — ~20 duplicated lines differing only in the
  `getErr==nil` case.
- **CLONE-1** `handlers_bruno_import.go:1978` — `cloneDSLScript`/`deepCloneAnyMap` duplicate the package's
  `cloneScriptDefinition`; the corruption kicker is refuted (persist propagates marshal errors), so cleanup only.
- **SQLDUP-1** `pkg/storage/sqlite_bruno_imports.go:141` — the 18-column scripts INSERT/UPDATE is hand-copied
  (3rd/4th copy); identical today, future-drift risk. Extract a shared tx helper.
- **FNE-1** `pkg/convert/bruno/inspect.go:1092` — `firstNonEmpty`/`firstNonEmptyStorage` re-add the copy
  `convertutil.FirstNonEmpty` was created to eliminate.
- **METADUP-1** `pkg/convert/bruno/inspect.go:319` — the `.bru` meta-field extraction loop + YAML runtime
  detection are copy-pasted across five sites.
- **WORKER-DUP-1** `runners/bruno/src/worker.js:568` — `executeGRPC`/`executeWebSocket` duplicate ~55-60 lines
  in three mirrored chunks.
- **FMTBYTES-1** `dashboard/src/components/BrunoImport.tsx:68` — third private `formatBytes` copy, missing the GB
  tier (a 2 GiB bundle renders "2048.0 MB"). Use the exported `settingsPageFormatters.formatBytes`.
- **PARAMIZE-1** `pkg/api/handlers_bruno_import.go:320` — the Bruno importer skips the shared
  `buildImportParameterization` pass every other importer calls. Functional impact is near-zero (the converter
  emits one opaque `bruno_run` action, so `paramize.Suggest` would return empty), so this is contract-consistency
  / altitude, not a correctness bug — but it means future hardening of the shared import-response boundary won't
  cover Bruno.
- **CHOOSER-TEST-1** `dashboard/src/components/YAMLImportChooser.tsx:16` — new component ships with zero tests
  (AGENTS.md testing policy). Add render/routing tests.

---

## Refuted / out of scope (recorded so they aren't re-raised)
- **RESET-DRIFT** (`BrunoImport.tsx:139`) — reset-block drift is real but harm is guarded (remount on reopen +
  `setConflictResolutions({})` on every inspect). Latent-cleanliness only.
- **UNIQUIFY** — the converter emits exactly one `bruno_run` action, so `DeduplicateActionNames` is a no-op.
- **DEDUP-DEL-2** — pending-session cleanup *does* check other live sessions; only a much narrower sub-second
  window remains (partly covered by #16).
- **COLLECTIONBRU-EEXIST** — a single-file `collection.bru` bundle can't be imported (Go rejects "no request
  items"), so the runner EEXIST path is unreachable.
- **FLAGS** (`bruno_import.go`) — hand-rolled flag parsing is the apexctl house style; Bruno's parser is stricter.
- **SLA-JSON** (`cmd/apexctl/main.go:133`) — belongs to the parallel CI change-set, not this feature; the new
  output is self-describing (`sla_evaluated:false`).
