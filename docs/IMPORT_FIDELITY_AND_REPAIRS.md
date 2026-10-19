# Import Fidelity and Repairs (JMeter and LoadRunner)

Status: Current. Behavior reference for imported JMeter plans and LoadRunner archives: fidelity semantics, source-plan execution, and protected guided repair.

Source note: consolidated 2026-09-24 from the former `JMETER_IMPORT_FIDELITY.md`, `JMETER_SOURCE_PLAN_REPAIRS.md` and `LOADRUNNER_IMPORT_REPAIR.md`. Messaging plugin imports are covered separately in [JMeter messaging fidelity](PROTOCOL_SUPPORT.md#jmeter-messaging-fidelity).

Contents: [JMeter import fidelity](#jmeter-import-fidelity) · [JMeter source-plan execution and batch repairs](#jmeter-source-plan-execution-and-reviewed-batch-repairs) · [LoadRunner import fidelity and guided repair](#loadrunner-import-fidelity-and-guided-repair)

---

## JMeter import fidelity

Reimport existing plans to receive these changes. Saved imports are not rewritten, and importing never modifies the JMX source.

The importer retains the first effective endpoint independently of header, timer, assertion, and variable scopes. Requests to other hosts remain absolute. An explicit base-URL override retains precedence.

Finite, unscheduled Thread Groups produce `termination_mode: "iterations"`, no duration, and positive `config.run_logic.iterations`. The supplied split-pay fixture preserves 50 users, a one-second ramp, and 200 iterations per user. Normal completion means 10,000 sampler executions. Each user retires after its own count. Duration mode remains the default for API requests that omit termination mode and requires a positive duration.

Iteration mode uses fixed closed-loop populations. Scaling, duration adjustment, traffic profiles, timeslots, and recurring schedules are unavailable. Pause/resume and cancellation remain supported. Generator admission requires `iteration_termination_v1`; loss of a generator produces incomplete work rather than replaying its work on another generator. Duration-based JMeter schedulers retain their authored duration and finite loop limits; unresolved and incompatible completion semantics stay blocked.

In timestamp review, choose either UTC normalization (one timestamp with milliseconds) or the source's IANA timezone. The source option preserves wall-clock fields and explicitly acknowledges a literal `Z` suffix. Supported Java formats become formatted, per-occurrence parameters. Dynamic formats, unsupported patterns, and unsupported `__timeShift` calls require repair. Choices are recorded in import provenance and applied by server-side regeneration.

Variable review lists declared names and unresolved references. Enter the declared name to bind, or an explicit `{{secret.NAME}}` reference. The before/after preview shows the proposed rewrite; inspecting again creates a newly validated, attested draft. Asterisk masks and disclosure placeholders are unavailable credential material. Correcting credentials does not clear timestamp or unsupported-processor gates.

A Counter config element becomes a native counter parameter: start, maximum (wrapping to the start), increment and zero-padding formats carry over, and it advances once per iteration. A Counter that JMeter shares across threads (`per_user` false), and `__counter(FALSE, ...)`, become unique-number counters: each VUser draws from its own block, so values stay distinct across VUsers but are not issued in JMeter's single global order. Blocks hold 1,000,000 values, reduced when a Counter's range would otherwise not give each of the thread group's threads its own block, so at least the plan's thread count of VUsers stays within the source maximum; the import notes how many blocks the range holds. Unique-number blocks step by 1, so a shared Counter with a larger increment keeps the cross-thread gate once a request uses it. Variable starts, other number formats, reset-per-iteration counters, and same-named counters whose settings differ between controllers stay unconverted, and requests using them remain gated. Throughput Shaping Timers on HTTP, JDBC and network thread groups become group-wide TPS pacing: an iteration sends each paced request once, so the most restrictive timer (its rate divided by the requests it paces) sets the iteration rate, as JMeter's sequential loop does, and the import names the requests whose higher targets this cannot meet. Profiles that change rate, timers above the thread group, and timers with a loop or conditional controller around or inside their scope are reported and left unpaced; timers that pace only requests of another protocol are left out. A plan that starts with a UTF-8 byte-order mark imports normally. A Kafka or Event Hubs sampler inside a thread group converted as HTTP is review-gated like a non-dominant JDBC sampler.

JSON extractors preserve missing versus explicitly empty defaults. `on_miss: "default"` with `default_value` replaces a previous extraction on a miss. The default does not suppress malformed expressions or resource-limit failures. This extractor mode is supported by HTTP-family protocols.

### Native CSV results

A complete match of the supported Groovy trace writer becomes `config.results.csv_exports`. The selected variable must be extracted before the writer, without a later enabled overwrite; other processor orderings retain a review gate. The declaration identifies a source action, a filename, and ordered columns selecting extraction values or `latency_ms`. Arbitrary Groovy execution and source-machine filesystem writes are unsupported.

`traceId-results.csv` is a per-run archive download with `traceId,latencyMs` columns. Rows come from finalized action samples, including failures, and exclude synthetic transaction records. Empty trace values use the writer's fallback. Latency is integer milliseconds to the first response byte; connection failures without a response byte report zero.

Exports use complete local archives or deterministically merged generator archives, not sampled live metrics. Archive authorization, retention, redaction, and integrity controls apply. Missing shards, retention truncation, scan limits, count mismatches, and write failures produce an incomplete export instead of a downloadable partial file. Export definitions are frozen in run provenance. Finalized samples bind to export IDs before action-name redaction, so redaction cannot change which samples belong to a CSV.

Sampler totals and source-behavior classifications are separate. Disabled elements are ignored, informational listeners do not block replay, and enabled unsupported processors retain durable gates after unrelated repairs. Source links identify the contributing assertions, extractors, timers, and postprocessors.

The sanitized original and repaired regression sources are under `pkg/convert/jmeter/testdata`. The repaired fixture uses fake credentials and still requires an explicit timestamp policy; execution tests use loopback services.

### Regression coverage

| Original finding | Regression evidence |
| --- | --- |
| Scoped endpoint loss and explicit override | `pkg/convert/jmeter/fidelity_test.go`: split-pay, nested scopes, multiple hosts, and override cases |
| Invalid timestamps and ambiguous literal Z | Converter format/repair tests and `pkg/engine/parameters_test.go`: frozen midnight and timezone cases |
| Lost 200-iteration limit | `pkg/engine/iteration_termination_test.go` and `pkg/distributed/generatorrpc/iteration_termination_test.go`: exactly 10,000 loopback samples, pause/resume, cancellation, and group completion |
| Omitted Groovy CSV writer | Converter full-pattern rejection tests; `pkg/api/jmeter_fidelity_execution_test.go`: finalized samples, failures, deterministic shard merging, escaping, redaction, and incomplete exports |
| Missing extractor default and stale trace ID | `pkg/protocol/http/extractor_default_test.go`: present/missing/present, empty defaults, invalid responses, bounds, redirects, and connection failure |
| Contradictory accounting and inaccurate source links | Converter source mappings, disabled elements, and durable reason-code regressions; import contract registry tests |
| Unresolved source variable names and masked credentials | Converter binding tests and `dashboard/e2e/workflow-jmeter-fidelity.spec.ts`: explicit review regeneration and retained blockers |
| Persistence and rolling upgrades | SQLite/PostgreSQL termination migrations, scenario admission/persistence tests, and legacy generator capability rejection |

The browser workflow runs in both dev-admin and local-auth profiles. It imports the original fixture, regenerates explicit repairs, saves a small repaired iteration scenario, executes six loopback samples, and downloads the seven-line CSV through the results UI. The source endpoint is never used by these tests.

For messaging plugin imports, see [Kafka / Event Hubs draft fidelity and remaining conformance work](PROTOCOL_SUPPORT.md#jmeter-messaging-fidelity).

### Review evidence

New JMeter imports and validation responses include a `review_summary` with distinct source-behavior counts: `blocking_behaviors`, nonblocking `review_behaviors`, `unsupported_behaviors`, `warnings`, and `informational`. Unsupported behaviors are a subset of the affected behaviors, not another count to add to the review total. Sampler/action totals and legacy classification statistics remain separate.

The `diagnostics` array contains typed severity, stable reason codes, source references, and actionable details; an import without findings returns an empty array. Review sections and compatibility evidence use the same normalized findings. `unsupported_elements` and compatibility `unsupported_fragments` contain only enabled source behaviors lacking a faithful implementation. Translated CSV exports, reporting listeners, ordinary warnings, and repairable timezone or credential configuration problems do not belong in these lists.

The dashboard counts blocking behaviors and explicit review decisions separately from warning and informational findings. Marking an item reviewed or editing scenario settings does not remove a server-authored gate. Reinspect with explicit repair choices to regenerate the evidence. Existing saved imports are unchanged; reimport them to obtain the normalized evidence. No storage migration is required.

---

## JMeter source-plan execution and reviewed batch repairs

Messaging imports retain three native drafts and their concurrent scenario. A
converted or saved draft is not necessarily runnable. Strict messaging replay
remains gated by `jmeter_messaging_v1`, which generators do not advertise until
all protocol and live-conformance requirements pass.

### One generator, one source-engine session

The controller sends an immutable version-1 source-plan manifest with every
assignment. It contains the project, source plan, execution generation, sorted
group membership, source-group identities, script hashes, fixed populations,
and the `source_plan_execution_v1` capability requirement. Commit binds to the
manifest digest. The controller rejects generators missing this capability.

The generator reserves the complete population while staging. It constructs
one `MultiGroupEngine` for all groups at commit; missing or inconsistent groups
cannot start. Failed staging/commit tears down the staged plan. Identical retries
are idempotent, and committed membership cannot grow. Per-group sinks, redaction,
secret resolvers, VUser coordinates and archive attribution remain separate.
Counters, random generators, timer gates and Kafka producer sessions share the
engine lifetime. Producers close after the complete engine drains.

All groups use the commit epoch and retain their authored ramps and schedules.
Pause freezes the shared clock without catch-up; interrupted comparisons remain
outside uninterrupted JMeter parity. Scaling and duration changes are rejected.
Generator loss fails the plan: there is no migration, state reset or message replay.
This grouping capability alone is not a strict-parity certification.

### Protected repair workflow

Batch repairs require the existing operator-managed protected import store.
Imports without that store retain the legacy workflow; configuring protected
storage and reimporting creates a repairable batch. Older previews are not
reconstructed from client-submitted drafts.

The encrypted parent retains original source documents and their digest, exact
scripts, scenario, source identities and reviewed operation history. Parent and
child candidates share an expiry. Public previews, diffs and receipts contain
credential-safe projections. Original JMX bytes never change.

The existing repair preview/apply endpoints accept the batch variant:

- `batch_id`, `revision`, `project_id`, and 1–32 typed `operations`.
- `preview_digest` on apply, matching the reviewed operation set and revision.
- Source-group and source-node targets for group/sampler-specific operations.

Supported operations confirm UTC/no property overrides, normalize supported
legacy OAuth option names, bind Kafka and separate Event Hubs secret references,
replace a selected timestamp with one UTC evaluation, add an Event Hubs
application property (including an explicit UUID choice), and parameterize a
selected JSON payload field. Message indices preserve sampled batch boundaries.
Credential binding takes secret names, never secret values. Normalizing OAuth
option names does not provision secrets; bind the standard client ID and
client-secret references separately. Conflicting or
unsupported JAAS options remain blocked; uploaded Java is never executed.

Preview publishes no new revision. Apply prepares replacement candidates and
publishes one create-only receipt under the same cross-process lock that fences
claim epochs. A stale lease cannot publish. Identical apply retries retrieve the
same revision even if the response or old-claim cleanup was lost. Unpublished
replacement candidates expire through the protected-store janitor.

Save seals the revision with a durable save-intent receipt before entering the
existing transactional scripts-plus-scenario persistence path. Repair and save
cannot both win. After a failed save, retry the identical save request; that
revision cannot be repaired or saved with changed content. Existing asset-save
receipts recover completed transactions after a lost response. Mixed revisions,
missing members and stripped child batch identities are rejected.

Every repair rechecks script validation, variable dependencies, secret references
and scenario consistency. Acknowledging a warning cannot remove an execution
blocker. Missing credentials, Kafka batch-allocation differences, remaining
producer failure-boundary differences and unverified live conformance continue
to prevent strict replay. No repair changes the workload to exactly 600,000
messages, enables the disabled CSV, or invents downstream assertions.

### Verification boundary

Automated checks cover complete grouped execution, manifest validation, admission
and cleanup, group iteration attribution, protected revision fencing, retry
receipts, payload edits and browser preview/apply/save. Existing virtual-clock
and reference-vector tests cover schedules and parameter semantics. Build-tag
messaging tests exercise the real adapter code without claiming live service
conformance. Authorized Event Hubs and Kafka/OAuth evidence is still required
before strict compatibility can be advertised.

---

## LoadRunner import fidelity and guided repair

Re-import affected archives to obtain corrected semantics. Existing saved scripts are never rewritten.

The HTTP importer preserves ordered ITEMDATA fields and treats EXTRARES as its terminator. A multipart/form-data step (`EncType=multipart/form-data`) imports as `body_multipart`, one part per field. Replay builds the body with a new boundary each time. Text fields keep their `{{…}}` parameters, and each uploaded file becomes a file data source (`upload_<field>`) that the part reads through `{{file.NAME}}`, so the file can be swapped without editing the request. HAR imports and both recorders convert recorded multipart uploads the same way. One exception: a body that holds an imported credential keeps its recorded form, so the credential workflow still finds it. Explicit resources live on the originating request in `params.resources`; the bounded resource fetcher executes them for POST and JSON responses as well as HTML pages. A page fetches at most 256 resources, explicit and discovered together (recorded pages routinely list more than 100); a page that offers more reports `resource_cap_reached` in its evidence. Resource work contributes to the parent request's timing. The shared cookie jar, cache and download filters remain in effect.

`source_verification` registers, pauses or resumes a check at its original position. State belongs to the VUser, and a registration in actions does not affect earlier init requests. Its ID, case flag, failure condition and search scope are preserved. Existing explicitly authored script-wide checks continue to apply globally. Unsupported verification forms remain execution-gated.

The active user-agent selection respects `UseCustomAgent`. Navigator language metadata does not supply an HTTP Accept-Language header. Conflicting active declarations still require review. Transaction-order declarations accept `__*delimiter*__` without splitting punctuation inside transaction names.

### Unique allocation

`parameters[].auto_allocate` records LR automatic allocation. A unique file or list parameter without `block_size` (or `value_for_each_vuser`) is allocated automatically even when `auto_allocate` is absent, as VuGen's default "automatically allocate block size" is, so no two VUsers read the same rows; imports record `auto_allocate: true` with a note. Allocation compiles before execution using the planned VUser population and usable rows after StartRow. Each VUser receives a contiguous block identified by a dense ordinal independently of distributed runtime IDs.

- A duration run divides usable rows by planned VUsers using integer division; remainder rows are unused.
- An iteration run uses the effective iteration requirement per VUser.
- Once reserves one value per VUser. An explicit block size is authoritative.
- Exhaustion stays within the VUser's block. ContinueWithLast cannot lend another VUser's final row to an unallocated VUser.
- Insufficient allocation capacity fails before execution. Automatic each-occurrence allocation requires an explicit block size.
- Blocks remain frozen across planned ramps and shards. Groups executing the same script reserve disjoint ordinals from their combined planned population. Conflicting parameter contracts or iteration requirements for that shared script are rejected. Population changes and generator-loss resharding are rejected when they would lose allocation state. Open workloads, stage-driven rescaling, parallel each-occurrence cursors, and shared at-once init are not accepted for these allocations. Fixed-population ramp-up/ramp-down uses the reserved identities.

Generators advertise `lr_fidelity_v1`; assignments carry population and ordinal offset, and their retry fingerprint includes both. Older generators cannot be selected silently for the new semantics.

Allocation rules follow [OpenText's allocation examples](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/UI/116150_ui_parameter_simulation_db.htm). Verification scope follows [OpenText's global verification reference](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/web/lrFr_web_global_verification.htm).

### Protected repair

The existing archive review screen offers source-linked repair and allocation previews. Repair requires encrypted candidate storage. The preview/apply endpoints are `/api/v1/scripts/import/repairs/preview` and `/api/v1/scripts/import/repairs/apply`; see the generated API contract for typed fields.

Requests identify the project, protected candidate, opaque revision, finding and action path. Operations restore a body, replace an encrypted proxy-password secret binding, bind an existing producer, add a parameter, add an extractor on a preceding response, select supported TLS settings, or preview allocation. Preview returns a redacted before/after draft and remaining requirements. Apply rotates the candidate and preserves its original expiry. Encrypted receipts make retries of the same operation stable and fence the replaced revision. Project/actor authorization and existing protected save paths remain authoritative.

Restored request bodies remain encrypted in candidate storage and are fully redacted in repair previews. There is no native LR ciphertext decoder. Ordinary LR masking and stronger encryption have distinct diagnostics. An encrypted proxy password requires a replacement secret, and that secret must be configured before replay. SSL_VERSION=2&3 stays blocked until an operator chooses supported TLS behavior; the choice is recorded as an intentional migration, not exact parity.

Dependency analysis examines executable request fields and nested requests. A `{{var.*}}` reference that nothing produces is replayed as its placeholder text, as LoadRunner sends an undefined `{param}` literally and warns that it "is not a parameter". The import warns with a promotion review gate, replay preflight warns instead of refusing, and each run result that sends a placeholder carries the warning `unresolved_variable_sent` naming the variables. Three cases still block replay: a missing `{{param.*}}`, because replay never sends an unresolved parameter; a missing value in an `lr_eval_json` buffer, because the JSON helper fails its step instead of parsing placeholder text; and a `{{var.*}}` reference in protocol configuration, which is never interpolated. Conditional or later producers require review. Commented source is not reactivated, and TSTAMP receives no invented meaning. A `//` comment ending in a backslash continues onto the next line, as in C, so a registration on that line never runs in LoadRunner either. The import skips it and warns with both line numbers, because the editor does not show the line as a comment. Every repair refreshes the shared findings and fidelity summary. Acknowledgement cannot clear an execution blocker.

C string literals are decoded exactly once, with C escape rules (`\'`, `\?`, octal and two-digit hex included). A literal containing any other escape keeps its text verbatim, so quoted values such as `C:\data\file.dat` are not altered. Boundaries, extractor queries, text checks and bodies therefore keep escaped JSON such as `userAccountNumber\":\"`. In Windows-1252 scripts a body escape such as `\x81` still names the code-page byte.

`lr_eval_json`, `lr_json_get_values` and `lr_json_stringify` execute. Documents keep member order and number spelling. `QueryString` accepts `$`, `.name`, `['name']`, `[n]` (negative from the end), `[*]`, `.*`, `..` recursive descent and `[?( )]` filters over `@` paths (comparisons with string, number, boolean or null literals, existence tests, `!`, `&&`, `||`). `SelectAll=Yes` publishes `NAME_1..NAME_n` and `NAME_count`; a miss fails the step unless `NotFound=Continue`. JSONPath slices, unions and script expressions, `Buffer/File`, unknown arguments and the other JSON helpers import as gated review steps with a specific reason code. `ValueParam` and `OutputParam` count as producers in dependency analysis.

Conditions of the form `strlen(lr_eval_string("{p}")) <op> N` translate to `control_if`. A registration with `NotFound=warning` stores an empty string on a miss, so `!= 0` tests "found". An `int` local declared once inside a function, and written only with integer-literal assignments, imports as `lr_save_int` steps on `clocal_<name>`. Conditions and `while` headers that read it then translate. A loop driven by such a local has no bound in the source, so it imports with `max_iterations: 1000` and an import warning.

Only actions the selected run logic runs are imported. VuGen lists every action file in the `.usr` `[Actions]` block, but an action executes only where the run logic places it. An action in the Run tree (at any block depth) imports as a run action. An action placed in the `vuser_init` or `vuser_end` section imports into that phase, in the section's order. An action placed nowhere is skipped with a warning, as LoadRunner skips it. Without a readable run logic, or when the Run tree names none of the declared actions, every declared action imports as before.

C functions a script defines (business steps such as `Login()` written in an action file and called from `vuser_init` or `Action`) run where they are called. A call without arguments to a defined function is replaced by that function's calls, recursively, under the caller's conditions and loops. Each call site gets its own copy, so a step called from `vuser_init` runs once and a transaction around a step call times its requests. Function bodies no longer import at top level in definition order, and functions nothing calls do not run, as in LoadRunner. A function defined in an action file the run logic skips still resolves, and that file's parse failures gate the phase that runs it. A call whose value is used once also runs in place: `rc = Login();`, `int rc = Login();`, `return Login();` and `if (Login() == 0)`. A call in a loop condition, or inside another call's arguments, stays a `custom_call` for review. If no call in an action file belongs to its declared action function, the file's function bodies import at top level as before, and only functions other files define are inlined.

A function called with arguments, such as a `formatCTN(char* str)` that slices a number apart and rebuilds it, is translated into LoadRunner parameter operations on namespaced variables (`clocal_<function>_<name>`). Each string argument is bound with `lr_save_string`. Character locals are initialized, `strncpy(local, value + N, M)` becomes `lr_save_var` with an offset, `strcpy` becomes `lr_save_string`, `strcat` and `sprintf` become `lr_param_sprintf`, `memset(local, 0, N)` clears, and other LoadRunner calls read locals through `lr_eval_string`. `lr_save_var` saves at most its length, from its offset, in bytes as C does. Saved and formatted values keep their exact text, including spaces at the edges and inside literals, and an empty or blank literal still saves (`lr_save_string("", "p")` clears `p`). Anything else stays a `custom_call` for review, with the reason: control flow, assignments, early returns, non-string parameters, or calls to C functions with no LoadRunner equivalent (which might write into a local). Recursion and nesting deeper than 16 calls also stay for review.

`web_reg_find` reads flagged text arguments. `Text/IC=` imports as a case-insensitive check: a `body_contains` or `body_not_contains` assertion with `case_insensitive: true`, or a `(?i)` count for `SaveCount`. Other text flags (`DIG`, `ALNUM`, `BIN`) change what the text matches, so they import no check and the import warns.

`lr_force_think_time` imports as its own action whose `think_time` has `force: true`. A forced delay waits exactly as recorded whatever the think-time settings say (ignore, multiplier, limit or percentage), including debug replay that ignores think time, as in VuGen.

A script that appends lines to a file on the load generator (`fopen`, `fprintf`, `fclose`, often to record created users) replays each `fprintf` line as an output message tagged with its stream (`params.output_stream`), alongside the run's other output messages. APEX Load does not write files on generators. The file calls themselves are no-ops, and the import warns.

Parameter files keep VuGen's delimiter, including a tab written as a literal tab between quotes (`Delimiter="<TAB>"`, the usual Java Vuser layout). Trimming had read every tab-delimited data file as one comma-separated column, so each parameter held a whole line. `same_line_as` takes the row number its referenced parameter picked, as LoadRunner's "Same line as" does, even when the two read different data files. When that row lies beyond the dependent parameter's own data, its exhaustion policy applies (`wrap`, `last` or `empty`). A same-line parameter also follows its master's row each iteration whatever its own update setting says, because VuGen ignores that setting for same-line parameters.

Java Vuser scripts keep their recorded source, so the runner resolves `lr.eval_string` references the way LoadRunner does. The project's parameter delimiters (`.usr` `ParamLeftBrace`/`ParamRightBrace`, such as `<dbUrl>`) import as `config.java_vuser.param_left_brace`/`param_right_brace`. A reference resolves by exact name first. An identifier-shaped reference also matches the imported parameter's canonical lowercase name, because `.prm` parameters import as `connectionfactoryjndi` while the source asks for `{connectionFactoryJNDI}`. Substitution is a single pass, and text between delimiters that names no parameter stays literal. C conversion recognizes only `{name}`, so a C script with custom delimiters imports with a warning.

The private acceptance archive is not committed. Regression tests use synthetic values and local HTTP servers. Live Salesforce replay and comparison with an LR baseline remain separate validation requiring usable credentials and target access.

### Local acceptance evidence

The supplied private archive was re-imported locally on 2026-09-19. No target traffic was sent.

| Check | Result |
| --- | --- |
| Primary HTTP requests | 492 retained |
| Transaction boundaries | 25 starts and 25 ends |
| Subtransaction boundaries | 445 starts and 445 ends |
| Correlation extractors | 58 retained |
| Largest request body | 113,839 bytes retained |
| Explicit resource parent requests | 4 |
| False ITEMDATA/EXTRARES blockers | 0 |

Three bodies still need restoration. The encrypted proxy credential, legacy SSL selection, 12 missing producer names (including TSTAMP), and one conditional/later producer remain requirements. The parameter-conversion producer's lowercase alias is recognized. Commented correlations remain disabled. These results establish import structure and local replay semantics, not Salesforce/LR baseline parity.

### Post-implementation review (LoadRunner)

The follow-up review corrected five issues and added regression tests:

- Dependency deduplication now distinguishes `var` and `param`, so a conditional variable producer cannot hide a missing parameter blocker with the same name.
- Producer-binding repairs accept whitespace in template expressions and preserve the `var`/`param` namespace. Source evidence remains unchanged.
- Unknown global-verification failure modes remain execution blockers instead of silently using `Fail=Found`.
- Explicit resources take precedence over automatically discovered duplicates, preserving the authored referer and reserving their bounded fetch slots.
- Positional-verification template values participate in dependency analysis; missing inputs block execution.

Validation summary: the full Go suite, the unit/lint/build cycle and all seven LoadRunner browser workflows (dev-admin and local-auth, including guided repair preview/apply) passed at implementation time. The broader browser profiles were not fully green then; the failures were existing workspace-selector assertions unrelated to this work, and later reviews record a passing `npm run test:cycle`. The focused regression tests and the private-archive structural probe passed after the review fixes.

## Aura capture outcomes and HTTP replay

Salesforce HAR and archive review distinguishes captured Aura application results from HTTP status and replay readiness. The Aura outcomes panel reports request/response identity coverage, known action states, incomplete evidence, and retained-detail omissions. Its source button follows a unique existing action-path mapping; HAR entry numbers retain their original capture positions after filtering. The saved review retains this structural evidence without publishing action IDs, descriptors, payloads, tokens or server error messages.

Native imports add the HTTP assertion `aura_actions_success` to structurally valid Aura POST envelopes. It matches every response action to the actual resolved outgoing request, independently of response order, and fails on unsuccessful/unknown states, malformed envelopes, duplicate identities or incomplete coverage. HTTP 200 remains the HTTP status even when an Aura action fails. A framework error response is decoded the way the Aura client decodes it: a body ending in `/*ERROR*/` (JSON between the first `*/` and the last `/*`), or an exception event in a non-200 body. It is reported as `invalid_session`, `client_out_of_sync` or `framework_error`, a known failure of the whole batch, instead of unreadable JSON; the event and its message are never reported. The assertion accepts no value, min, max or legacy HTML-check bypass. When the recording matched the imported request action for action, the actions that already failed there become its `expected` baseline (`{"recorded": [{"ordinal", "state"}]}`), and the import review counts them. A repeat failure is then reported as "also failed in the recording", so it is not chased as a correlation problem. Replay still requires success unless the HTTP setting `aura_recorded_failures` (script or scenario override) is `accept_recorded`. That accepts an action that ends in the same unsuccessful state as the recording, with the run warning `aura_recorded_failure_accepted`. A new failure, a different failure state, or a structural problem still fails. Malformed imported request envelopes retain an execution review gate through save/reopen; REST business fields named `descriptor` do not establish Aura traffic.

Inspection is bounded to 8 MiB per envelope, 128 nesting levels, 256 actions, 131,072 JSON tokens and 4,096 form fields. Import review inspects at most 512 batches and 64 MiB of envelope work, retaining at most 2,048 outcome details and 2,048 findings shared across imported requests and captured responses. Runtime diagnostics retain at most 64 outcomes and 64 findings. Validation and inspected aggregate counts precede detail retention; failure outcomes take priority and omissions are explicit. A duplicate of a member the validator reads (the envelope `actions` and `exceptionEvent`, an action `id`, `descriptor` or `state`, or a framework error event `descriptor`), including an escaped alias, is an incomplete-evidence diagnostic. Duplicates elsewhere, such as in component payloads and return values, cannot change the result and are accepted; every payload is still parsed within the limits above.

Private offline acceptance inspected 409 Aura batches and 504 request actions, retaining 503 structurally matched responses and 25 captured ERROR actions under HTTP 200. One Login response was incomplete because of a duplicate member in a payload the validator does not read; it now validates. These checks and synthetic local HTTP/browser tests establish implementation behavior; live Salesforce session, correlation and physical Chrome/iPad parity require separate qualification. See the [implementation and review record](qa/reviews/aura-record-replay-2026-10-03/implementation.md) and [safe capture counts](qa/reviews/aura-record-replay-2026-10-03/capture-check.json).

Correlation detection rejects values that are not server state before generating any extractor. It skips name- and word-shaped values from the generic rules, whose only evidence is a key, cookie or header name (`navigation`, `Retail_Home_Page`, a cookie `qualified`, `Bearer` in `token_type`), short or repetitive values from the weakest of them (generic cookies, redirect query parameters, dynamic headers, JSON/XML ids need at least 6 characters, 4 of them distinct), and bare URL origins, which `base_url` retargets. For the weak heuristic rules (generic cookies, redirect query parameters, dynamic headers, JSON/XML ids) it also skips values the client already sent in a URL, body or non-credential header at or before the producing response. Credential, session and Salesforce platform rules are exempt from that check, because an earlier occurrence there usually means the capture began after the first producer. When a Salesforce shape extractor and a generic rule find the same value, the earlier producer wins. The analysis cap counts distinct analyzed values, so values repeated across Lightning responses no longer truncate analysis of later responses. JSON responses up to 131,072 tokens are walked structurally (Lightning record UI and preload responses reach about 56,000), with up to 4,096 candidates per response and 16,384 distinct analyzed values per capture, so one large response cannot use the whole budget. Analysis reads at most 16 MiB of evidence per response and 64 MiB per capture. A large browser capture is mostly static assets (a Salesforce Lightning session records over 100 MiB of Aura component JavaScript), so when a capture exceeds those budgets, the bodies of static-asset responses (JavaScript, stylesheets, fonts, images and media, judged by `Content-Type`) are left out, largest first and only as far as needed. Their requests and headers are still analyzed, and the import notes how many bodies were skipped. That note is not a blocker, because application responses (HTML, JSON and the rest) are always analyzed. A capture whose application responses alone exceed the budget still reports the limit. Hidden form fields whose names contain `csrf`, `xsrf`, `token`, `nonce`, `state`, `viewstate`, `authenticity` or `session`, or that are named exactly `sid` or `lm`, are correlated when a later request sends the value. This covers the session hand-off pages a Salesforce login posts automatically (`frontdoor.jsp` to `contentDoor`, `visualforce/session` to `recsession`). When analysis is still incomplete, the HAR importer applies the correlations it completed, since each one finished its reuse and collision checks, and reports the unanalyzed part for review. A value that also occurs inside a longer identifier is held for review instead, because the skipped part may hold that longer value. A complexity work-limit error still applies none. A correlated value is rewritten only where it stands on its own: never inside a longer identifier (a percent, JSON or backslash escape before it is a boundary), and never in a URL's scheme or host or a `Host` header. A high-confidence correlation's extractor fails the step when its value is missing; a less certain one falls back to the recorded value it replaced, when that value is at most 64 KiB.

When Aura requests in the native script still send the recorded `aura.token`, the Salesforce review warns. It says whether the capture has no producer, as with Chrome DevTools HAR exports that drop the `__Host-ERIC_*` Set-Cookie, or has a producer that was not applied.

A HAR file cut off partway through its entries (for example, a device capture that stopped writing) is rejected with HTTP 422. The message says how many complete entries the file holds. With the import option `salvage_truncated=true` (the "Recover the complete entries of a truncated HAR" checkbox), the HAR and Salesforce importers keep the log fields before the entries and every complete entry, and discard the incomplete entry and everything after it. The import then warns how many entries were recovered and how many bytes were dropped, and that the script ends where the capture was cut. Only a file that ends early is salvaged: a file that is malformed in the middle, or has no complete entry, is still rejected.

The HAR and Salesforce importers accept a capture of up to 300 MiB, either as a `.har` file or as a `.zip` that holds exactly one `.har` (folders and operating-system files such as `__MACOSX` are ignored). A zipped capture is recognized by its content, not its name, and the 300 MiB limit applies to the decompressed HAR. An archive with no `.har`, or with more than one, is rejected with HTTP 400 naming what it found. An archive over the decompressed limit, or one that expands more than 1,000 times, is rejected with HTTP 413. A comparison HAR may be zipped the same way.

Suggested secret bindings come from imported credential values. Salesforce Aura's `_au` and `_uid` query parameters are public application and component-definition cache keys, so the credential-shape check leaves them alone, in URLs and recorded query-string metadata alike. A credential-named parameter is still always masked. Cookies and session, anti-forgery, trace and idempotency headers are state the source recorded, not credentials: no importer suggests a binding for them, and script views show them as recorded. One the source itself declares a credential, such as an OpenAPI API key sent in a cookie, is still a credential. When optional run-evidence redaction is enabled, a cookie value is masked only when it is at least 8 characters long, or when its cookie name is credential-like (for example `session`, `sid`, `token` or `auth`) and the value meets the short-value rule below. Short flags and preferences, such as `autocomplete=1`, a language tag or a consent policy, are not masked; masking them marked every matching token, such as a digit in `User-Agent` or `en` in `Accept-Language`.

HAR, Salesforce and browser-recorder imports give client-generated UUIDs fresh values. A UUID in a request target, body or header is client-generated when no recorded response contains it: not in any body (static assets included), header, cookie, redirect or decoded JWT. There is nothing to correlate it from, and replaying the recorded literal would make every virtual user send the same session, request or event id. Only values with evidence of generation are replaced with `uuid` parameters:

- A key with three or more values, none of them also used under another key (log, event and request ids), gets one parameter that is fresh at every reference (`update: each_occurrence`).
- A key whose name marks a session or flow value (`session`, `conversation`, `transaction`, `correlation`, `trace`, `span`), any other generated-id name (`request`, `nonce`, `event` and similar), or a key with more than one value, gets one parameter per recorded value. That parameter is fresh each iteration (`update: each_iteration`). Set its update to `once` to keep one value per virtual user.

Identity and configuration names (tenant, device, install, app, site, org and similar), single values with neutral names, path segments without a key, and values also used in fields the import credential workflow covers, encoded or Aura contract bodies, assertions, extractors or other fields are left as recorded. The import warning counts both groups. Uppercase UUIDs, as iOS sends them, get `format: uppercase`. When too few responses kept their bodies to tell what the server sent, detection is skipped and the import says so. On a real iPad capture, 95 of 97 client-made UUIDs became 4 parameters, and the `clientSessionId` values no longer appear as suggested secret bindings.

A credential value is matched by value elsewhere in a script only when it can be told apart from ordinary text. A value found by its name counts only when it is at least 4 characters long, or when the name is password-like (`password`, `passcode`, `pin`, `otp`, `secret`). This covers credential-like query, form, header, JSON, XML, multipart, GraphQL and cookie names, including import credential locators on query, form and header fields. A `cookie=1` flag no longer counts. Otherwise every `1` in a `DNT` header or an `HTTP/1.1` version became a suggested secret binding. On a real desktop capture, suggested bindings dropped from 601 to 115. Values from authentication settings (basic, bearer and similar) and fields a locator marks inside a JSON or XML body (such as a schema-declared credential) keep their whole-value protection, even when short.

## Native archive compatibility

Re-import an archive into a new review draft to use these mappings. Stored scripts and approved runtime overrides are not rewritten. The original source values and their selected-file provenance remain in import metadata.

For `acc_wls_reg_1.zip`, the selected runtime profiles contain 220 keys. Re-import classifies 79 as mapped/consumed, 137 as non-applicable, and four as compatibility-only. These are source-key counts, not dashboard row counts: the dashboard expands native settings and groups related source keys. The dashboard now has four **Preserved only** rows. Proxy selection and partial receive-buffer equivalence still require review.

| Source setting | Native behavior |
| --- | --- |
| `Encoding=ANSI`, `RecorderWinCodePage=1252` | Strict Windows-1252 decoding of C/header/configuration/parameter text and referenced parameter data. Binary assets remain unchanged. Unsupported/ambiguous encodings and undefined text bytes fail import instead of introducing replacement characters. |
| `AutoLogBufferSize` | KiB converted to `config.log.error_buffer_bytes`. Error-only logging holds redacted history until a failure; successful history is otherwise discarded. |
| `NetBufSize` | `config.http.receive_buffer_bytes`. Application response-copy buffer, **not** OS socket buffers or HTTP/2 flow-control windows; mapping remains partial. |
| `MaxSelfMetaRefreshCount` | `config.http.max_meta_refresh`. Limits refresh visits to each URL; also enforces an overall 50-navigation ceiling. |
| `MaxErrorMatchesAsERRORS` | `config.http.content_check_error_limit`. Boundary-match failure counts above the cap are informational; failure status remains unchanged. |
| Disabled `ProxyAutoConfigScriptURL` | Non-applicable when an unambiguous selector explicitly disables PAC. |
| `ProxyUseBrowser` | Portable environment proxy selection with a review notice. Windows browser inheritance requires an explicit exported configuration. Conflicting selectors leave proxying disabled pending a decision. |
| `ProxyPasswordIsEncrypted` | Consumed credential metadata. Provision the protected proxy password using Credentials. |
| `WebSocketCallBackTimerIntervalMS` | Non-applicable when inspection proves no WebSocket operation or indirect reference in the native HTTP source. Source callbacks remain review-required until converted to the supported native callback contract. |
| System language / available-screen dimensions | Non-applicable to HTTP replay. Explicit browser-protocol emulation is available below. |
| `ResetContext`, recording timestamps and DST | Recording/code-generation provenance; no iteration resets or recorded-session replay semantics are inferred. |
| `IgnoreContentCachingTypes`, `KeepNonTextMimeType`, `AnalogMode`, `SSLVersionKey` | Remain preserved with review explanations. Native MIME controls are explicit operator-selected alternatives. Numeric SSL selectors and `SSL_VERSION="2&3"` are not interpreted as modern TLS versions. |

### Logging and HTTP contracts

- `config.log.error_buffer_bytes`: default 102400 bytes in error-only mode; maximum 1 MiB per VUser. All buffers share a 16 MiB process reservation budget. Capacity includes record metadata. Oldest entries are evicted; entries larger than capacity or beyond the shared budget are dropped. Failures still emit. Flush clears history, and teardown releases retained capacity. Disabled logging emits nothing. Error-only output applies to ordinary requests, sequence children, lifecycle actions, and diagnostic replay.
- `config.http.receive_buffer_bytes`: zero retains the existing copy strategy; 1–1048576 selects a bounded application read buffer. Existing body-capture limits remain separate. Buffer allocations use the shared HTTP capture budget.
- `config.http.max_meta_refresh`: zero preserves existing behavior (no automatic meta-refresh); 1–50 enables it. Relative URLs, quoted targets and delays are supported. Refresh navigation and resource fetches share the outer page deadline and outbound policy. Cross-origin navigation strips explicit and default authentication headers and action credentials. Timing and bytes include the navigation chain. Authored action assertions/extractions apply to the final page; global content checks and registered verification observe the navigation responses.
- Boundary checks use `config.web_runtime.content_checks[].left_boundary` and `.right_boundary`, with optional `.text` to match inside each pair. Both boundaries are required together. `config.http.content_check_error_limit` is 0 (uncapped count) or 1–1000 per response. Counts are exposed as `content_check_matches` protocol evidence; they never turn a failed check into a passing one. Main-document behavior is supported; existing resource-scope limitations remain.
- `body_encoding: "windows-1252"` encodes text after parameter substitution. Unrepresentable characters fail before sending. Fixed binary bodies use base64. Imported URL-encoded form components retain their wire charset when substituting values. Mixed binary/deferred bodies without a lossless representation require repair.

### PAC proxy mode

Set `config.http.proxy_mode: "pac"` and `config.http.proxy.pac_url` to an HTTP(S) URL without userinfo. Existing none/system/custom modes remain available. PAC retrieval is capped at 256 KiB and five seconds. The evaluator runs in a separate credential-minimized process, with at most four concurrent workers, a two-second parent deadline, a 500 ms evaluation interrupt, 4096 AST nodes and nesting depth 64. Linux also applies a 256 MiB data limit. Each literal and output is bounded to 4096 bytes; DNS helpers are limited to 32 calls.

The supported PAC language profile is intentionally restricted: one `FindProxyForURL` function, variables, `if`/`return`, conditional expressions, boolean/comparison operators, and these helpers: `dnsResolve`, `isPlainHostName`, `dnsDomainIs`, `localHostOrDomainIs`, `dnsDomainLevels`, `isResolvable`, `shExpMatch`, `isInNet`. Loops, recursion, objects, dynamic/property calls, string-building operators and unlisted helpers fail closed. This is not arbitrary JavaScript compatibility.

PAC downloads, DNS helpers, destination resolution and returned proxy addresses are policy checked. Without an explicit target allowlist, PAC-related network resolution requires public addresses. Private enterprise PAC/proxy/destination hosts must be explicitly permitted. DNS answers used for physical dialing are pinned. An ordered list may contain `PROXY`, `HTTPS`, `SOCKS5`, and explicit `DIRECT` entries (maximum 16). Only connection-establishment failures advance to the next route; ambiguous write/read failures are not replayed. No download, evaluation, or exhausted-route failure silently becomes direct access.

Evaluation and route transport setup currently happen per request. Measure PAC overhead for the intended workload; use a reviewed static proxy when that overhead is unsuitable. PAC proxy credentials are not inferred from encrypted source values.

### Browser and WebSocket contracts

Browser settings are `config.browser.system_language`, `available_screen_width`, and `available_screen_height` (dimensions 0–8192; zero means unchanged). An initialization script emulates `navigator.systemLanguage` and `screen.availWidth/availHeight`. It does not change viewport dimensions or reproduce host OS / Internet Explorer behavior.

WebSocket callbacks use `config.websocket.callback_interval_ms` (default 500; authored positive range 1–60000). After `ws_connect`, register `ws_on_message` with 1–64 `control.steps`. For each message the engine sets `{{var.ws_message}}` and `{{var.ws_message_type}}`, then executes the steps serially on that VUser. Binary message bodies use the existing base64 representation.

The reader queues at most 64 messages / 1 MiB of message text per connection. Overflow is an execution error. Dispatch occurs when due at safe protocol-action boundaries; `ws_dispatch` waits until the next dispatch interval when the script is otherwise idle. This is not a concurrent callback thread or a precise real-time timer. `ws_receive` cannot compete with registered callbacks. Close, teardown and reconnect invalidate the queue; register again on the new connection. Arbitrary C callbacks are not executed.

New settings are available in the existing script/import editors and scenario/group overrides. No new REST routes are required. Generated DSL/schema and API/type artifacts describe the native fields and callback actions.

### Verification

Regression coverage includes the sanitized `pkg/convert/enterprisearchive/testdata/native_acc_compat` source structure and settings, source-key accounting, strict decoding/binary preservation, logging suppression/history/eviction/redaction, HTTP/1.1 and HTTP/2 reads, refresh limits/cancellation/cross-origin credentials, content failure caps, PAC ordering/failure/resource controls, browser page-visible properties, WebSocket queue ordering/lifecycle, and override cloning/application. The dashboard workflow edits and saves the new HTTP and PAC controls through the real UI.

Local test servers provide behavior evidence. No application endpoint from the supplied archive is contacted by the import assessment or these tests.

#### Validation record (2026-09-23)

Point-in-time record (2026-09-23); the "not green" statements below are historical. the Code review section of `docs/contracts/orchestration-v1/README.md` records a full `npm run test:cycle` passing (typecheck, lint, unit tests, build, dev-admin and local-auth browser workflows) on a different change set, so the browser-suite failures listed here were not reproduced there; this document has not been re-run since. The following records the initial implementation checks. See [Review notes](#review-notes) below for review fixes and the latest test outcomes.

- The final `go test ./...` passed. An earlier concurrent run exposed the existing 80 ms generator execution-lease test's timing sensitivity; five isolated repetitions and the final full suite passed. Focused race checks passed for the engine, HTTP/PAC, WebSocket, and browser changes, including the final PAC alias/shadowing regressions.
- Dashboard type checking, lint, all 4,446 unit tests, and the embedded build passed.
- The new HTTP/PAC settings edit/save workflow passed against real controllers in both dev-admin and local-auth modes. Import-review apply/save/source-evidence workflows also passed in both profiles.
- The complete `npm run test:cycle` is **not green**: dev-admin reported 120 passed, four skipped, and ten failed; one of those failures was the new test's counted-tab selector, which was corrected and passed on rerun. The nine remaining failures are in command-center details, authentication-shell navigation, live run controls, workspace navigation/setup, and source-mapping review. Local-auth reported 48 passed, one skipped, and two failed (the workspace journey's obsolete “Current project” selector and the existing source-mapping review selector). These broader workflow failures remain outstanding; this change does not claim a clean full browser suite.

### Review notes

Historical code review (all listed findings were fixed), folded in 2026-09-24 from the former `docs/reviews/NATIVE_ARCHIVE_COMPATIBILITY_REVIEW.md`. Reviewed the compatibility implementation against the saved pre-implementation working-tree baseline. Existing unrelated edits were retained. Review covered HTTP refresh/read/content-check paths, PAC isolation and network handling, source decoding, logging, callback lifecycle, browser configuration, and configuration/override plumbing.

#### Findings fixed

| Priority | Finding | Fix and regression evidence |
| --- | --- | --- |
| P1 | An HTTP redirect followed by a same-origin meta-refresh on the redirected page could reattach credentials from the original request. | Refresh credential forwarding now compares the destination with the original request origin, rather than the redirected response origin, while treating default ports as equivalent. Local redirect/refresh server test verifies credentials are absent. |
| P1 | Same-origin refresh discarded caller-supplied header redactions, allowing custom secret headers into protocol failure snapshots. | Preserve redacted header maps/lists when forwarding headers; clear both actual and redacted credentials together on origin changes. Failure-snapshot regression checks the custom header. |
| P2 | Refresh GETs inherited entity headers from default header layers, and latency assertions measured only the final response. | Clear entity headers from all header layers; evaluate latency against the complete navigation chain. Tests cover default content type and a delayed refresh that must fail its latency assertion. |
| P2 | A rejected duplicate WebSocket callback registration replaced the active callback body during action resolution. | Commit the body only after successful registration, in load, lifecycle, and diagnostic replay execution. The lifecycle test rejects replacement, then verifies original callbacks still execute. |
| P2 | Peer close discarded messages already queued for callbacks. | Deliver the queued batch before surfacing the terminal read error on the next dispatch. A real server sends a final message and immediately closes. Queue overflow remains fatal before dispatch, with a real 65-message regression preventing reconnect from erasing the error. |
| P2 | Failed explicit reconnect left the old callback reader blocked; close/teardown did not wake an interval wait. | Close the previous connection before dialing its replacement, and wake dispatch waits through callback cancellation. Tests cover failed handshake cleanup and teardown. |
| P2 | Callback queue/reader errors could stop execution without a failed sample. | Record dispatch failures through the normal metrics/evidence path before returning the error. The real WebSocket lifecycle test checks the aggregate error count after peer close. |
| P2 | ANSI parameter filenames were resolved before decoding their metadata, so referenced data with non-ASCII filenames could remain undecoded. | Decode metadata first, resolve referenced data afterward, and decode each file once. Regression includes a Windows-1252 filename, repeated references, and an unchanged binary asset. |
| P2 | PAC rejection before transport handoff leaked the request body; `isResolvable` aborted on NXDOMAIN instead of returning false. | Close bodies on early rejection. Treat DNS name-not-found as the helper's empty result while keeping policy violations and other failures fatal. Tests use a tracked body and a local DNS responder; no external DNS is required. |
| P2 | PAC DNS pinning dialed only the first address, losing IPv4/IPv6 and multi-address fallback. | Try the validated, pinned addresses under the existing deadline before rejecting a route; an unresolvable proxy can advance to the next explicit PAC route. Tests cover fallback and cancellation without external network access. |

#### Review validation

- Focused HTTP/PAC, WebSocket, engine lifecycle, and archive-conversion regressions passed with the race detector. Final callback overflow/lifecycle checks also passed three repeated race runs.
- Two full Go runs passed during review. The final `go test -p 2 ./...` run reported one failure: `TestDistributedExecutionBackendStagesRemoteArchiveOnCompletion` exceeded its eight-second completion deadline. Three subsequent isolated repetitions passed. All other packages passed in that final run; the final full-suite result is therefore not reported as green.
- Dashboard type checking, lint, all 4,446 unit tests, and the embedded build passed. The HTTP/PAC editor save workflow and import-review apply/save workflows passed in both authentication profiles.
- The full `npm run test:cycle` remains red: dev-admin had 121 passed, four skipped, and nine failed; local-auth had 48 passed, one skipped, and two failed. These repeat the broader workflow failures recorded before review: command-center details, authentication/workspace navigation, live run controls, authoring scope, guided setup, and source-mapping review. They remain outstanding outside this compatibility review.
- Re-import retained all 220 source keys: 79 mapped/consumed, 137 non-applicable, and four compatibility-only. No source application endpoints were contacted.
- `git diff --check` passed. The generated QA dashboard was refreshed using its repository generator.

#### Second review after fixes (2026-09-23)

Re-read the final refresh credential/header forwarding and timing paths, callback registration/queue/reconnect/error reporting, PAC policy/body ownership/DNS fallback and evaluator restrictions, source-decoding order, error-log retention, and browser emulation. No new actionable findings were identified in this focused review; no production changes were needed.

Repeated the focused HTTP/PAC, WebSocket, engine, and archive-converter regression tests with `-race -count=1`; all four packages passed. `git diff --check` also passed. The full-suite and browser-cycle limitations above remain unchanged; those suites were not rerun for this documentation-only follow-up.

## Shared HTTP Import Safety Contract

Postman, JMeter HTTP, OpenAPI, WSDL, the constrained k6 HTTP subset, and sample/HAR requests retain their source-specific parsers but share effective-request projection, structured credential handling, runtime capability checks, durable review gates, and final DSL validation.

The shared result vocabulary is `executable_exact` (preserved supported behavior), `executable_bounded` (replay-safe runtime clamp plus diagnostic), `blocked_draft` (valid sanitized draft that cannot reach transport), and `rejected` (no valid disclosure-safe draft). These outcomes do not replace existing endpoint response fields or the broader `supported`/`partial`/`review_only` support labels. Full per-importer dispositions and archive-ingress rejection rules are documented in [Web/API Execution Support Matrix](PROTOCOL_SUPPORT.md#tested-adopter-dispositions).

---

## Enterprise Script Archive Routing

APEX Load's Enterprise Script Archive importer is protocol-aware. It classifies ZIP archives containing C-based virtual-user scripts, runtime settings, parameter files, and protocol metadata before conversion using project metadata as the primary signal.

**Import modes:**

| Mode | Meaning |
| --- | --- |
| `executable` | APEX Load has a native converter/runtime path and returns a generated script. |
| `review_only` | Asset can be classified and preserved, but promotion requires operator review or a different importer. |
| `external_runner_required` | Protocol needs a non-APEX Load runner bridge before execution. |
| `unsupported` | Protocol recognized or suspected; no current conversion path available. |

**Current routing:**

| Source protocol family | APEX Load route |
| --- | --- |
| Web HTTP/HTML | `http` executable import |
| Web Services / SOAP | `soap` executable import (exclusive SOAP evidence); mixed SOAP/HTTP stays HTTP with warning |
| Mobile browser-profile archives | `browser` executable import with browser replay gate |
| Web 2.0 browser-profile archives | `browser` review path with required replay and selector evidence |
| Database / ODBC SQL-style scripts | `database` executable import for SQL statements, binds, transactions, and DSN/runtime hints |
| DNS, FTP, SMTP, POP3, IMAP, LDAP, Winsock | `network` executable import with subprotocol config and endpoint placeholders |
| Oracle-Web / Siebel-Web | `http` executable import plus Oracle E-Business correlation review pack |
| SAP GUI, Oracle NCA, Oracle 2-Tier | External-runner review artifact |
| MAPI, Tuxedo, Template VUsers | Review artifact until runner/converter is approved |

If `.usr` metadata and source content conflict, APEX Load returns a review artifact rather than claiming an executable import. The harmless exception: Web HTTP/HTML metadata with exclusive SOAP evidence routes to `soap`.

**API surface:** `POST /api/v1/scripts/import/enterprise-script-archive` and `POST /api/v1/scripts/import/enterprise-script-archive/validate` return `detected_protocol`, `review_artifact`, `summary.import_mode`, confidence, signals, conflicts, and warnings.

---

## Web 2.0 Browser Profile

First-class APEX Load support for browser-driven Web 2.0 source profiles: import, authoring, replay, repair, and optional protocol-load conversion.

**Scope:**

| Area | Decision |
| --- | --- |
| TruClient 2.0 - Web import | In scope |
| APEX Load Web 2.0 browser authoring profile | In scope |
| Browser replay/debug evidence | In scope |
| Browser-to-HTTP conversion for protocol-load density | In scope |
| Legacy desktop/native application automation | Out of scope |
| High-density real-browser load claims | Evidence-gated non-claim |
| Byte-for-byte VuGen proprietary project compatibility | Out of scope |

**Profile shape:**

```json
{
  "protocol": "browser",
  "metadata": {
    "import": {
      "source": "web2_browser_profile",
      "preserved": {
        "apexload_profile": "web2_browser_profile",
        "source_script_type": "TruClient 2.0 - Web",
        "trueclient2_promotion_review": {
          "status": "replay_evidence_attached",
          "replay_evidence": {
            "session_id": "draft-browser-validation",
            "screenshots": 1,
            "network_entries": 12
          }
        }
      }
    }
  }
}
```

**API surface:** `POST /api/v1/scripts/import/enterprise-script-archive` routes TruClient 2.0 - Web archives to a `browser` review path. `GET /api/v1/runtime-settings/support-matrix` includes Web 2.0 runtime settings rows. `POST /api/v1/scripts/browser-to-http/preview` supports optional protocol-load conversion preview.

**Import detection:** `.usr` script-type detection distinguishes `TruClient 2.0 - Web`, `TruClient - Web`, `TruClient - Mobile Web`, and legacy desktop/native variants.

**Implementation status:** Scope matrix, fixture contract, import detection, runtime settings rows, iframe/window replay evidence, replay preflight evidence gates, saved `trueclient2_promotion_review` artifacts, and browser-to-HTTP conversion preview are complete. Remaining: real TruClient If/Loop archive mapping and deeper selector repair fixture coverage.

---

## JMeter Import

APEX Load converts `.jmx` test plans into APEX Load JSON DSL scripts and optional scenario drafts.

**Status:** Implemented. `POST /api/v1/scripts/import/jmeter`, `POST /api/v1/scripts/import/jmeter/validate`, `POST /api/v1/scripts/import/jmeter/save`, `GET /api/v1/scripts/import/jmeter/support-matrix`.

**Supported constructs:**

| Element | Status |
| --- | --- |
| Test Plan, Thread Group | Supported |
| HTTP Request Defaults, HTTP Header Manager, HTTP Cookie Manager | Supported; ordered HeaderManager duplicates and casing are preserved |
| HTTP Authorization Manager | Basic, bearer, and digest supported; Kerberos/NTLM/negotiate become blocked drafts |
| User Defined Variables, CSV Data Set Config | Supported |
| HTTP Request samplers | Supported |
| Constant Timer, Uniform Random Timer | Supported |
| Response Assertions | Supported |
| Regex / JSONPath / JMESPath / CSS / XPath / XPath2 post-processors | Supported — map to APEX Load action extractors |
| Boundary extractors | Supported for HTTP response body and headers |
| Simple If / fixed Loop / Once Only / Transaction controllers | Supported for the tested deterministic subset |
| Dynamic or complex controller behavior | Valid flattened draft with a durable review gate |
| Basic variable substitution (`${variable}`) | Supported |

**Review/adapter boundaries:** Fully recognized safe single-statement JSR223 patterns can translate, while residual JSR223 / BeanShell / Groovy and third-party plugin behavior becomes disclosure-safe review evidence and cannot execute. JDBC, JMS, FTP/TCP, and SOAP-shaped samplers retain their existing protocol/adapter routes and are not part of the shared JMeter HTTP request pipeline. JMeter listeners remain reporting metadata rather than replay actions.

**Import report contract:** Every import returns `warnings[]`, `unsupported_elements[]`, `assumptions[]`, and metadata (`file name`, `thread group count`, `sampler count`, `variables discovered`, `CSV sources discovered`).

**Thread group mapping:** Thread groups become separate reusable APEX Load scripts with an optional scenario draft (named workload components with VUser counts, ramp-up hints).

**Result import:** k6, Gatling, and JMeter JSON-style summary payloads can be imported and trended via `/api/v1/results/import`.

---

## DevWeb Import

DevWeb is OpenText's JavaScript-based protocol-level HTTP testing engine. APEX Load's HTTP, WebSocket, and gRPC protocols cover DevWeb's core transport scenarios natively.

**API surface:** `POST /api/v1/scripts/import/devweb`, `GET /api/v1/scripts/import/devweb/support-matrix`.

**Coverage:**

| DevWeb capability | APEX Load status |
| --- | --- |
| HTTP/1.1 and HTTP/2 requests | Supported (`pkg/protocol/http`) |
| Request headers, body, cookies | Supported |
| Response extraction (JSON, regex, XPath) | Supported |
| Response assertions | Supported |
| TLS/SSL configuration | Supported |
| Proxy support | Supported |
| Auth (Basic, Bearer) | Supported |
| Data parameterization | Supported |
| Think time / pacing | Supported |
| WebSocket connect/send/receive/ping/close | Supported (`pkg/protocol/websocket`) |
| gRPC unary, streaming, descriptor-set loading | Supported (`pkg/protocol/grpc`) |
| DevWeb `.js` script import (static conversion) | Supported (`pkg/convert/devweb`) |
| Full JavaScript runtime compatibility | Non-goal — dynamic JS execution is review-only |

**Design principles:** One Go goroutine per VUser. WebSocket and gRPC reuse HTTP TLS/proxy/auth config. Protocols compose with the existing VUser loop, metrics, and pause control.

---
