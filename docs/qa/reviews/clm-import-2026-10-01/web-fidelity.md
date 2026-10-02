# Web archive import fidelity — 2026-10-01

This pass inspected the three Web HTTP/HTML `.USZ` children of the supplied collection. Source artifacts were treated as data. Private hosts, credentials, dataset values, request bodies, cookies, and full converted scripts are excluded from this report and repository fixtures. Actual converter outputs remain in the private temporary inspection directory. Regression fixtures use synthetic `example.test` sources.

The selected project lifecycle, runtime profiles, parameter profiles, embedded data files, generated DSL, and review findings were compared. Import accounting and synthetic runtime tests provide evidence about conversion and supported Go behavior. They do not certify application replay equivalence: the supplied application hosts are unreachable, and no successful application transaction was observed.

## Selected source profiles and conservation

All three `.usr` projects select `default.cfg` and `default.usp`, with the declared C lifecycle `vuser_init.c`, `Action.c`, and `vuser_end.c`. The USP has a flat, sequential run root containing `Action`. Init/end scaffolding runs once. The error-handler root names `vuser_errorhandler`, but the project carries no corresponding action file; it is reported as inert scaffolding. Unselected backup sources and SDK declarations do not inflate the selected function counts.

| Archive | Parsed selected functions | Native classifications / review only | DSL actions | Source HTTP requests | Requests compacted into sequences | Parameters / datasets |
|---|---:|---:|---:|---:|---:|---:|
| CMChangeQNew1 | 1,141 | 1,141 / 0 | 678 | 218 | 0 | 2 / 2 |
| S01_C360_ViewPPE_Wireless_v0.1 | 578 | 577 / 1 | 485 | 152 | 1 | 4 / 3 |
| KM_SearchArticle | 323 | 322 / 1 | 131 | 77 | 38 | 2 / 2 |

Request compaction retains the requests inside an ordered `http_sequence`; it is not discarded traffic. All three reports contain zero detected dropped request bodies and zero masked-body secret gates. These are structural checks, not a byte-for-byte proof for every request or a claim that all source runtime behaviors execute.

## Runtime settings

Each key in the selected CFG and USP has an explicit disposition. Counts include run-logic tree bookkeeping, stale authoring fields, and inactive child settings. A `mapped` key may select a portable replacement with a partial parity note; the label alone does not imply exact vendor equivalence.

| Archive | Mapped keys | Non-applicable keys | Compatibility-only keys | Total selected keys | Effective iterations | Proxy |
|---|---:|---:|---:|---:|---:|---|
| CMChangeQNew1 | 39 | 84 | 4 | 127 | 1 | Custom proxy |
| S01_C360_ViewPPE_Wireless_v0.1 | 43 | 97 | 2 | 142 | 3 | Generator environment/system proxy, partial portability |
| KM_SearchArticle | 39 | 84 | 4 | 127 | 3 | Generator environment/system proxy, partial portability |

The `default.cfg` iteration values are superseded by the selected USP. C360 and KM preserve three iterations even though their CFG says one. Duration-driven scenarios can supersede a script iteration cap; a standalone or explicitly iteration-driven run uses the imported cap.

| Setting category | Observed source policy | Destination and evidence | Limits |
|---|---|---|---|
| Encoding | ANSI, recorder code page 1252 | Strict Windows-1252 source decoding | Request bodies and binary assets retain separate byte semantics; no additional body transcoding is inferred. |
| HTTP transport | HTTP/1.1, keep-alive on, `MaxConnections=0` | Native Go HTTP settings, with zero represented by destination defaults | This is not a benchmark of source and Go connection scheduling. |
| Proxy | CMChange custom; C360/KM browser-default | Custom HTTP proxy or generator `HTTP_PROXY`/`HTTPS_PROXY` policy | Windows browser configuration cannot be inherited by Kubernetes generators. Export/configure its portable equivalent. Credentials stay governed by the existing credential projection. |
| Browser identity | Explicit custom User-Agent; saved IE4 browser identity | Custom User-Agent and imported Accept-Language/Accept-Encoding headers | Saved browser type is dormant. Go HTTP replay does not create a browser DOM. |
| Accepted encodings | gzip/deflate; C360/KM also advertise br | Go HTTP transport supports gzip, deflate, and Brotli response decoding | Application-specific encoded bodies and DFE transforms are a separate concern. |
| Legacy HTML checks | `EnableChecks=0` | Imported legacy `web_find`/`web_image_check` checks disabled | Registered searches and authored assertions remain independent. |
| Embedded resources | `SearchForImages=1` | Bounded HTTP HTML/resource discovery | No browser JavaScript execution, DOM layout, or rendered screenshot equivalence is implied. |
| Iterations/run order | Flat sequential `Action`; 1 or 3 iterations | Verified native init/action/teardown ordering | Nested/custom block scheduling is not claimed. |
| Pacing | C360 explicit ASAP; CMChange/KM USP omit pace type | C360 maps exactly; CMChange/KM use no pacing and retain a partial finding | The importer does not silently splice stale CFG pacing into the selected USP. Missing source selectors require review. |
| Think time | `Options=NOTHINK`; caps inactive | Recorded delays ignored | Stored factor, limit, and C360 random percentages are dormant under this source mode. |
| Automatic transactions | Enabled in CMChange/KM; disabled in C360 | Native per-action wrapping where enabled | `automatic_nested_transactions=1` in CMChange/KM remains compatibility-only; exact source nested automatic sub-transactions are not generated. C360 child toggles are dormant. |
| Logging | Extended log; error-only auto log off; per-class toggles vary | Native supported parameter/data/trace controls | Vendor file routing, environment banners, timestamps, and logger presentation are non-applicable; retention/redaction uses APEX bounds and policy. |
| Error policy | C360 explicit continue-on-error off, fail-on-error-message off, snapshot-on-error off | Native error handling preserves explicit false settings | No successful application failures were replayed against the original hosts. |
| Snapshot resources | Saved preference on, no replay snapshot selector | Preserved partial diagnostic finding | It does not enable capture or reproduce LoadRunner snapshot/rendered resources. |
| Data Format Extensions | Enabled in CMChange/KM; disabled in C360 | Enabled source toggle retained as compatibility-only; disabled toggle is dormant | No source DFE chain definition is present. APEX-native transforms cannot safely substitute an unknown source chain. |
| Other saved/runtime controls | `AnalogMode=0` | Preserved compatibility-only | Its vendor semantics remain unverified; zero is not treated as proof of inactivity. |
| Recorded screen/context | Screen dimensions, recording timestamp, recorder version, ResetContext | Explicitly non-applicable to native HTTP | These fields no longer inflate the report as executed browser viewport/device behavior. |

## Parameter and data policies

Every selected `.prm` parameter is represented. All referenced `.dat` files are embedded in the converted data sources; this report records only row counts. File delimiters, header interpretation, selected column names, starting rows, update modes, selection modes, and exhaustion policies are retained.

| Archive | Parameter | Policy | Dataset rows |
|---|---|---|---:|
| CMChangeQNew1 | p_CaseNum | Unique rows, each iteration, start row 1, automatic allocation, error on exhaustion | 17 |
| CMChangeQNew1 | p_User | Unique rows, once per VUser, start row 5, automatic allocation, retain last on exhaustion | 226 |
| C360 | p_BAN | Random rows, each iteration, start row 1, retain last on exhaustion | 199 |
| C360 | p_Letter | Random rows, each iteration, start row 1, retain last on exhaustion | 26 |
| C360 | p_UserName | Unique rows, once per VUser, start row 1, automatic allocation, retain last on exhaustion | 220 |
| C360 | p_DateTime | Time, each occurrence; source pattern `%Y-%m-%dT%H:%M:%S.000Z`; zero offset and working-days filtering disabled | — |
| KM | p_Article | Sequential rows, each iteration, start row 1, retain last on exhaustion | 2 |
| KM | p_UserName | Unique rows, each iteration, start row 1, automatic allocation, retain last on exhaustion | 501 |

The file-row allocator uses runtime VUser identity and the compiled allocation plan; the source labels do not themselves prove uniqueness across an arbitrary external run topology. Retain-last explicitly permits reused values after exhaustion. Test volume must respect the available rows when strict uniqueness is required. Nonzero business-day offsets/calendar behavior were not exercised; those source options are disabled in this collection.

C360's date pattern now uses `format_dialect=loadrunner_strftime`. Source `.000` is literal text rather than Go's fractional-second token. A literal trailing `Z` is preserved as text and no longer silently chooses UTC. The source contains no timezone setting, so the native runtime uses its local clock unless the operator explicitly sets a timezone. Container/workstation timezone differences require review. The supported formatter rejects unknown and locale-dependent compound directives instead of guessing.

## Concrete fixes from this pass

- Separate `ProxyUseProxy` (proxy enable) from `ProxyUseProxyServer` (custom-mode selection). The valid C360/KM combination `ProxyUseProxy=1`, `ProxyUseBrowser=1`, `ProxyUseProxyServer=0` now retains system mode without a false synonym conflict. Malformed and contradictory selectors still fail closed.
- Publish executable producer/read metadata for positional parameter assignments. C360 `lr_save_timestamp` and KM `lr_save_string` no longer create false missing-producer blockers. An absent input array still creates a real dependency finding.
- Make API variable readiness recognize native source-parameter outputs and their exact/lowercase aliases. Analyze the scalar values actually evaluated by the handler instead of preserved source-call evidence. Missing inputs, late assignments, optional branch outputs, and freed variables remain blocked.
- Execute the supplied `lr_save_string(lr_paramarr_random(...), ...)` against indexed array values and their count. It no longer substitutes an undefined base variable. Empty/malformed arrays produce no selected value; vendor NULL/error behavior has not been certified as identical.
- Correct `lr_save_timestamp` default to epoch milliseconds and honor supported `DIGITS` resolutions. Synthetic tests cover the default, seconds, microseconds, lower-width truncation, malformed options, and documented fallback.
- Preserve LoadRunner date/time directive/literal boundaries and explicit timezone policy in the native parameter engine.
- Scope browser-device parity to the browser engine; native HTTP screen recording fields are non-applicable. Custom User-Agent selection now makes saved browser identity and disabled recorded-agent inheritance explicitly dormant in per-key review.

The timestamp and array contracts were checked against [OpenText lr_save_timestamp](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/c_vuser/lrFr_lr_save_timestamp.htm), [OpenText lr_paramarr_random](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/c_vuser/lrFr_lr_paramarr_random.htm), and the [parameter properties reference](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/UI/116100_ui_parameters_property_db.htm). Proxy portability follows the [runtime settings reference](https://admhelp.microfocus.com/vugen/en/26.1/help/WebHelp/Content/VuGen/c_rts_general_page.htm).

## Remaining source execution gates and validation

CMChange has no structural execution gate after import. C360 and KM each retain the `legacy_tls_replacement` gate at `init[1]` for source `SSL_VERSION=2&3`. Those legacy SSL selectors must be replaced by an explicitly chosen supported TLS policy; the importer does not mislabel them TLS 1.2 or remove the gate automatically. Original hosts, proxy availability, external identities, source credentials, and data appropriateness still require environment preparation even for CMChange.

The actual collection API review exposed an additional disagreement between converter dependency findings and shared API preflight. The converted producers were already present; the API readiness analysis only registered extractors, browser evaluation, and OAuth outputs. It also scanned the diagnostic base-array argument retained for KM's random selection. The shared check now follows the native parameter handler's reads/writes and evaluates them in the existing control-flow analysis.

| Actual imported web script | Original API missing-value count | Corrected missing-value count | Remaining source execution gate |
|---|---:|---:|---|
| CMChangeQNew1 | 1 | 0 | None |
| KM_SearchArticle | 3 | 0 | Legacy SSL selector at `init[1]` |
| S01_C360_ViewPPE_Wireless_v0.1 | 5 | 0 | Legacy SSL selector at `init[1]` |

These results were measured against the decoded scripts in the private actual API response, without replay seeds or invented parameter values. CMChange's correlation extractor at `actions[56]` precedes its URL conversion at `actions[58]`, which publishes the previously reported missing value. KM's bootstrap sequence produces its correlation before `init[7]` encodes it; the save-all article extractor at `actions[23]` supplies the indexed array/count read by random selection at `actions[25]`. The bare save-all name is not a live read for that selection and is still correctly reported missing if a normal request directly references it. C360's bootstrap extractors precede the three encoded correlation assignments; `actions[0]` publishes the timestamp and `actions[44]` encodes its declared date/time parameter.

The nine reported missing names were false preflight blocks, not lost source producers or values the operator must manually supply. The corrected readiness result does not prove that remote responses will match the imported extractor expressions or contain a nonempty array. Those remain application-dependent runtime conditions. The Kafka child also retained zero missing variables in this shared check; its governed Java/runtime constraints are documented separately.

The focused DSL, converter, and Go engine tests passed, including source parameter dependencies, missing-array rejection evidence, random indexed selection, timestamp resolution, source date/time literals/local clock/explicit UTC, proxy selectors, selected profile behavior, and runtime parity. `go generate ./pkg/dsl` completed. An initial broad converter/engine run encountered sandbox-denied loopback listeners; it also exposed one stale golden parity count, which was corrected and passed in the focused rerun. The root task owns the final broad Go suite and browser workflow validation; this document does not claim those gates passed before their result is available.

## Actual credential-preparation save correction

After variable readiness was corrected, the real CMChange review reached credential preparation and failed with `failed to project resolved script`. The failure came from a retained JSON credential locator at `actions[258]`: a default whole-body secret binding changed a structured JSON document into a template while leaving its original JSON field locators. It was not a request-body decoder failure or an exceeded script disclosure byte/work limit. The review now also shows credential preparation errors on the main review screen, so a save failure is visible when the Credentials pane is closed.

The four CMChange default binding choices did not represent four missing source payloads:

| CMChange body surface | Actual source evidence | Correct import treatment |
|---|---|---|
| `actions[41].body` | One original credential field is empty | Keep the original empty value and exact body; optional migration has no source value |
| `actions[258].body` | All 23 retained JSON credential fields are available | Keep the original structured body; its aggregate scalar encoding refusal does not mean its credential leaves are unavailable |
| `actions[453].body` | Its retained form credential value is available | Keep the original encoded form and its structured component |
| `actions[590].body` | Its retained form credential value is available | Keep the original encoded form and its structured component |

The scalar source-value classifier has a bounded encoded-candidate guard. Applying that guard to an entire serialized JSON document or a percent-encoded structured form component produced false `unsafe_source_encoding` availability choices here. The corrected destination-preservation check requires the import's preservation marker, bounded JSON/form decoding, available or source-empty leaves, unchanged source bytes, and shared byte/value/depth budgets. Duplicate JSON members are inspected in order, so a later duplicate cannot hide an unavailable earlier leaf. This proof does not make the entire opaque document available for optional secret migration. Genuine disclosure placeholders, ambiguous alignment, malformed bodies, unsupported opaque bodies, and individual credential leaves that exceed the existing guard retain their existing restrictions.

Explicit full-body secret binding now retains the governed authoring token and changes only the affected body's runtime locators to an opaque body locator. Other header/auth/URL locators, named `body` headers, foreign request keys, sibling actions, and HTTP-sequence children retain their original attributes. Malformed ordinary JSON still fails the existing locator validation.

| Actual private API candidate | Original default unpreserved occurrences | Corrected default unpreserved occurrences | Corrected source-preserving preparation and commit sanitizer |
|---|---:|---:|---|
| CMChangeQNew1 | 4 | 0 | Both succeeded; zero action body changes |
| KM_SearchArticle | 1 | 0 | Both succeeded; zero action body changes |
| S01_C360_ViewPPE_Wireless_v0.1 | 1 | 0 | Both succeeded; zero action body changes |

These are actual private API candidate checks through the credential-preserving resolver and the import commit sanitizer, with no typed credentials or fabricated full-body bindings. KM and C360's original empty bootstrap fields can remain empty during import. Whether the remote application later requires another value is an environment/runtime question. The focused synthetic guard, ownership, empty-source, explicit whole-body reference, and byte-fidelity regressions passed, alongside the actual private API check. The temporary private diagnostic test was removed. Durable receipt/reopen verification and the final broad suites belong to the root task; this section does not claim remote replay or persistence merely from successful preparation.

## Independent code-review corrections

The requested independent review found and corrected three additional boundaries. A selected array entry is now assigned as an opaque runtime value, so response bytes containing `{{var.other}}` are not expanded a second time; exact/lowercase aliases and configured run-evidence redaction remain intact. Random-array names require a complete C string-literal proof, including safe adjacent literals. Conditional expressions and trailing calls become source review actions with execution gates, while an authored string containing function-looking text retains its literal bytes in both the runtime argument and dependency evidence.

Proxy conflict activation now distinguishes the parent enable flag from custom, browser, and PAC selectors. A disabled custom choice leaves its saved static host/port/auth fields dormant, including when the parent is active. Parent/browser/PAC selector conflicts and the Windows-system-proxy migration requirement remain visible. Synthetic tests cover disabled/unset parent combinations, active portable modes, malformed and contradictory selectors, and conflicting dormant hosts.

The independent reviewer rechecked the production corrections. Focused native assignment/evidence tests and the full existing proxy test family passed; the exact literal-expression regressions also passed. The final full Go gate passes all 117 tested packages. Actual native save/reopen checks and the collection browser workflow in both auth profiles pass; the parent import review retains screenshots and evidence limits.

The final actual import saves all three native web drafts to the isolated local Script Library. CMChange, KM, and C360 each reopen with Saved: up to date. Two, two, and four parameter definitions and two, two, and three inline data assets remain present respectively. CMChange has no authoring errors; the KM/C360 source TLS migration gates remain. Protected body reconciliation preserves exact original executable bytes rather than substituting values or accepting a masked body as proof. No original target was replayed. Screenshots and the full gate result are tracked in the parent import review.
