# Live tables and VTS compatibility

APEX's `vts_columns` profile is an explicitly authored shared column model. It is **not a verified LoadRunner VTS adapter**. No imported VTS function is automatically converted to an executable `live_table` action. Remote VTS connections, vendor return codes, implicit LoadRunner parameter writes and send flags are not implemented by this profile. `native_queue` is a separate record and lease model; it must not be substituted for VTS column operations.

This review used OpenText's published function reference on 2026-10-12. The `/26.1/` alphabetical page redirects to the maintained 26.1–26.3 reference and lists 28 functions, including `lrvtc_get_column_range`; older indexed versions list 27. Treat that range function's detailed contract as unverified: its detail page was unavailable during this review. [Official alphabetical inventory](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/etc/lrFr_lrvtc_Alphabetical.htm).

## Function-by-function disposition

**Every row below is execution-blocked on import.** “Analogue” describes an independently authored APEX operation, not an approved translation. All candidates still require proof for error handling, parameter publication, empty results, source expressions and control flow. Short descriptions without a separate detail link come from the official inventory above.

| Single connection function | Published behavior | APEX analogue or remaining gap |
| --- | --- | --- |
| `lrvtc_clear_column` | Clear a whole column. | No atomic column-wide clear action. Positional `clear` is narrower. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_clear_column.htm). |
| `lrvtc_clear_message` | Clear one field. | `clear` with position and one column; vendor field edge cases need fixtures. |
| `lrvtc_clear_row` | Present cells become empty; absent cells remain absent. | Positional `clear` models this data distinction; return and parameter contracts remain unverified. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_clear_row.htm). |
| `lrvtc_column_size` | Count populated cells, including empty strings. | No exact size action. `read_column` count is page length, including absent positions, and is not equivalent. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_column_size.htm). |
| `lrvtc_connect` | Establish server connection; replace prior connection. | No remote connection adapter; an explicit per-run table binding has different ownership. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_connect.htm). |
| `lrvtc_connect_ex` | Configure authenticated/TLS connections. | Remote credentials, instance routing and connection options unsupported. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_connect_ex.htm). |
| `lrvtc_create_column` | Add column. | `create_column`; existing-column return behavior needs proof. |
| `lrvtc_disconnect` | End server connection. | No adapter. Never translate to closing the shared table for all users. |
| `lrvtc_drop_index` | Remove column index. | No index-management action; not treated as an ignorable call. |
| `lrvtc_ensure_index` | Build column index; writers queue during construction. | No index-management action or corresponding operational behavior. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_ensure_index.htm). |
| `lrvtc_get_column_range` | Read column interval. | `read_column` is bounded and positional; detailed vendor range/output contract unverified. |
| `lrvtc_increment` | Return new integer; absent/noninteger input initializes to delta. | APEX uses strict signed int64 with overflow rejection; noninteger/absent behavior differs. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_increment.htm). |
| `lrvtc_query_column` | Read one field. | `read` with column and position, not `read_column`; output parameter effects need proof. |
| `lrvtc_query_row` | Read a 1-based row into column-named parameters; missing cells yield NULL. | Positional `read` preserves missing keys; LoadRunner NULL/parameter behavior is not translated. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_query_row.htm). |
| `lrvtc_retrieve_message` | Remove column head. | `pop` on one column; verify all empty-column/error outcomes. |
| `lrvtc_retrieve_messages1` | Remove selected column heads. | `pop` on selected columns; delimiter parsing and published output parameters need proof. |
| `lrvtc_retrieve_row` | Remove all column heads and publish their values. | VTS-profile `pop` shifts each selected column. Native record pop is not a translation. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_retrieve_row.htm). |
| `lrvtc_rotate_message` | Move head according to send flag, optionally discarding duplicates. | APEX `rotate` always moves the cell to the column end; uniqueness flag unsupported. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_rotate_message.htm). |
| `lrvtc_rotate_messages1` | Rotate selected column heads. | No vendor send-flag or delimiter contract. |
| `lrvtc_rotate_row` | Rotate with same-row, stacked or stacked-unique mode. | Basic independent column rotation does not implement these modes. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_rotate_row.htm). |
| `lrvtc_search_row` | Return one randomly selected matching row; report found/not-found. | No equivalent search operation. A bounded read followed by client-side selection would not be atomic. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_search_row.htm). |
| `lrvtc_send_if_unique` | Append only when value is absent from column. | No atomic uniqueness operation. A read followed by push races. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_send_if_unique.htm). |
| `lrvtc_send_message` | Write at available column end; grow if needed. | APEX `push` always appends; available-field selection has not been verified. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_send_message.htm). |
| `lrvtc_send_row1` | Write multiple columns according to send flag. | APEX push independently appends supplied columns; same-row and unique modes unsupported. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_send_row1.htm). |
| `lrvtc_update_all_message_ifequals` | Replace matching values throughout selected columns. | No atomic bulk conditional replacement. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_update_all_message_ifequals.htm). |
| `lrvtc_update_message` | Overwrite positional field. | Positional `update`; bounds/growth/error behavior needs proof. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_update_message.htm). |
| `lrvtc_update_message_ifequals` | Conditional field replacement, returning changed/not-changed. | APEX `update` plus `expected` is atomic, but its typed statuses are not source-language numeric returns. [Details](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/lrFr_lrvtc_update_message_ifequals.htm). |
| `lrvtc_update_row1` | Overwrite row fields. | Multi-field positional `update`; delimiter, missing-column and error behavior unverified. |

## Multiple connections and language bindings

OpenText deprecates the multiple-connection family and documents separate connection handles. The following functions remain individually blocked, with the same data-operation gaps as their single-connection parallels **plus** unimplemented handle ownership/output arguments. [Official multiple-connection mapping](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/multi_connect/etc/lrFr_vtc_Multi_Connection.htm).

| Multiple-connection functions | Additional review requirement |
| --- | --- |
| `vtc_clear_column`, `vtc_clear_message`, `vtc_clear_row` | Preserve transport status and operation out-status separately. |
| `vtc_column_size` | Preserve output count separately from return code. |
| `vtc_connect`, `vtc_disconnect` | Bind handle lifetime and prevent one user's disconnect from closing shared state. |
| `vtc_create_column`, `vtc_drop_index`, `vtc_ensure_index` | Retain connection scope and each status contract. |
| `vtc_increment` | Preserve new-value out-argument and distinct success return. |
| `vtc_query_column`, `vtc_query_row` | Preserve output allocation, absence and status. |
| `vtc_retrieve_message`, `vtc_retrieve_messages1`, `vtc_retrieve_row` | Preserve outputs and destructive column shifts atomically. |
| `vtc_rotate_message`, `vtc_rotate_messages1`, `vtc_rotate_row` | Preserve send flags and allocated results. |
| `vtc_send_if_unique`, `vtc_send_message`, `vtc_send_row1` | Preserve flags, uniqueness and operation status. |
| `vtc_update_message`, `vtc_update_message_ifequals`, `vtc_update_row1` | Preserve out-status and dependent branches. |
| `vtc_free`, `vtc_free_list` | No allocation ABI adapter. Do not silently erase source memory ownership. |
| `vtc_get_last_error` | No vendor error-state adapter. |

Numeric success is particularly unsafe to infer from function names: single-connection conditional update returns 0 for unchanged and 1 for changed, while its multiple-connection twin returns a transport status plus an `outRc`. Multiple-connection increment documents 1 for success and 0 for failure, with the new value in an output argument. A blanket “zero means success” mapping is incorrect. [Conditional update](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/multi_connect/lrFr_vtc_update_message_ifequals.htm), [increment](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/multi_connect/lrFr_vtc_increment.htm).

C names, Java `Lrvtc.*`, C# `vts.*`/`vts_multi.*`, and JavaScript `lrvtc.*`/`vtc.*` are reviewed as separate bindings. C# frequently returns void where C/Java return an integer; some method spellings also differ. Do not derive one binding's contract from another's spelling. [Official language mapping](https://admhelp.microfocus.com/vugen/en/26.1/help/function_reference/Content/FuncRef/VTS/etc/lrFr_lrvtc_Update.htm).

## Import safety and acceptance evidence

`pkg/convert/enterprisearchive/vts_source_gate.go` records a bounded, value-free `source_vts_requires_review` execution gate for referenced source files. It recognizes `lrvtc_`/`vtc_` identifiers, including unknown future functions, macro references and aliases, plus the language namespaces above. Assignments and predicates cannot erase this dependency when translated calls are dropped. Original JavaScript sources are checked after translation. Comments and ordinary quoted values do not count as identifiers. Source references conservatively block the imported file even when reachability is uncertain; arbitrary dynamic/reflection-based foreign API use remains subject to the importer's existing unsupported-source gates and is not a verified VTS adapter.

The gate runs unconditionally in init and applies to every executable output lane. It identifies functions and source locations without retaining connection URLs, passwords or argument values. Repeated finalization does not duplicate the gate. Replacing it requires explicit table binding and review of every dependent branch and output variable; acknowledging a warning alone does not establish compatibility.

Tests in `vts_source_gate_test.go` cover standalone calls, returned values, predicates, function pointers, macros, future function names, bounded evidence, comment/string exclusion and C/JavaScript archive imports. Core tests in `pkg/livetable` establish the APEX contract for ragged columns, absent/empty cells, positional clear/update, atomic expected-value updates, shifting/rotation, overflow and replay. **No vendor-server differential tests have run; none of these tests proves VTS parity.**

Before enabling any automatic mapping, add golden cases against the targeted vendor version/language for successful, empty, missing, malformed and concurrent inputs; assert the full post-state, return value, parameter/out-argument writes and subsequent branch behavior. Include ragged columns, absent versus empty, all supported send flags, delimiter edge cases, reconnect/error behavior and retries after lost responses. Keep each unsupported or unverified call blocked until its observable contract is proven.
