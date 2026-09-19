# acc_wls_reg_1 missing-producer investigation

Re-imported the supplied private archive with the current converter and traced all
12 missing producer names plus the conditional producer back to source on
2026-09-19. Source contents and parameter values are not copied into this report.
No target traffic was sent.

## Reported examples

| Finding | Imported consumer | Source evidence |
| --- | --- | --- |
| `c_rewardcard` | `actions[581]`, request starting at `Action.c:7215` | The `c_RewardCard` registration at `Action.c:4222` is commented out. |
| `c_transaction1` | `actions[909]` at `Action.c:14587`; `actions[1042]` at `Action.c:16175` | The registration at `Action.c:14510` is commented out. |
| `productspecificationtype08` | `actions[1062]`, request starting at `Action.c:16436` | Referenced by body templates at `lrw_custom_body.h:200` and `:254`; no producer or parameter definition was found. |
| `tstamp` | First reported consumer `actions[678]`, request starting at `Action.c:11212` | `lr_save_timestamp` calls at `Action.c:8347` and `:11930` are commented out. This unresolved name affects 64 consumer actions. |

The action source locations identify the start of each request call, while the
reference may occur later in a multiline body or in an included body template.
Lowercase diagnostic names reflect converted template names; these cases are
not failures to recognize mixed-case active producers.

## Remaining producer findings

- Commented registrations: `c_idptrace1` (`Action.c:14511`),
  `c_manualpromotions` (`:3270`), `c_productreferenaceid3` (`:3798`),
  `c_productspecificationtype5` (`:4219`), and `c_promotioncatalogid1` (`:3274`).
- `c_productreferenaceid4`: the comment at `Action.c:3800` ends with a backslash.
  C line splicing continues the comment onto the apparent registration at
  `:3801`. The converter correctly excludes that registration.
- `c_offerid` and `c_productspecification01`: references exist, but no matching
  producer or parameter definition was found. Similar names do not establish
  interchangeable meanings.
- `c_offerid1`: an earlier registration at `Action.c:2492` is commented out;
  an active registration at `:3223` occurs after an earlier consumer. This is
  retained as an ordering review finding rather than a definite missing producer.

There are 79 missing-producer occurrences across 12 names, plus one conditional
producer finding. The `.prm` file supplies six parameters, none of which supplies
these names. Repetition reflects affected consumers, not 79 distinct variables.

## Outcome and validation

No parsing correction was warranted for these findings, and no warning was
suppressed. The existing guided repair can add or bind parameters and configure
extractors on preceding responses. Restoring a timestamp producer requires an
explicit source or repair decision about its intended format and lifecycle;
the converter does not infer that behavior from the name `Tstamp`.

Both `go test -p 2 ./pkg/convert/enterprisearchive ./pkg/convert/importreview`
packages passed. Existing regression coverage includes comment continuations,
producer ordering, parameter namespaces, and source-parameter lowercase aliases.
The archive retains 492 primary requests, 58 extractors, 25 transaction pairs,
445 subtransaction pairs, and zero false form blockers.

No implementation files were changed for this investigation. Existing working-tree
changes for the separate NEXT31 import work were left intact.
