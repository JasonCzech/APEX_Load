# Recording Profiles

A recording profile is a named set of recording options saved on a project. It
plays the role of VuGen's Recording Options. The proxy recorder, the browser
recorder's HTTP script and HAR imports apply the same profile, so one project
records the same way whichever recorder captured the traffic.

Profiles are stored in the project settings (`settings.recording_profiles`) and
edited from Recording Studio, under **Recording profile → Manage profiles**.
The editor groups them the way VuGen does.

| Route | Access | Purpose |
| --- | --- | --- |
| `GET /api/v1/projects/{id}/recording-profiles` | Viewer | The project's profiles, the automatic correlation rule catalog and `updated_at` |
| `PUT /api/v1/projects/{id}/recording-profiles` | Editor | Replace the profiles; `expected_updated_at` rejects a stale write with 409 |

A project update that leaves out `settings.recording_profiles` keeps the saved
profiles.

## Selecting a profile

- **Proxy recorder.** `POST /api/v1/recorder/start` takes
  `recording_profile_id`. The profile's `include_hosts` and `exclude_hosts`
  are added to the request's (profile hosts first, duplicates dropped without
  regard to case), so typing one more host never drops the profile's list.
  The profile fills the body capture limit and cookie capture when the
  request leaves them unset. Recording Studio shows the hosts a selected
  profile adds under each host field. The session remembers the
  profile, and converting or previewing the session applies it, unless the
  convert request names a different `recording_profile_id`. Cookie capture
  is decided when recording starts (an explicit `capture_cookies` wins over
  the profile); only a different profile at conversion changes it.
- **Browser recorder.** The convert and preview requests take
  `recording_profile_id`. It applies to the HTTP script made from the side-car
  HAR (`also_create_http_script`). The profile's `http_script_level` is the
  default level, and Recording Studio preselects it.
- **HAR import.** `POST /api/v1/scripts/import/har` takes
  `recording_profile_id`, either as a multipart field or in the JSON body.

The proxy recorder and HAR import reject an unknown profile ID with 400. The
browser recorder still saves its browser script, skips the HTTP script, and
says why in a warning.

## One conversion pipeline

The proxy recorder, the browser recorder's HTTP script and HAR import all
convert through the same pipeline (`pkg/convert/sample`). A proxy session
is written as an in-memory HAR, and conversion then applies one set of rules
to all three: redirects, bodies, headers, cookies, correlation, the async
scan, WebSocket placement and the replay baseline.

The proxy recorder adds a few rules of its own:

- **Headers.** It drops browser-supplied headers such as `User-Agent`,
  `Accept` and `Sec-Fetch-*`. It drops `Cookie` unless cookie capture is on.
  A profile's header lists override both.
- **Recorded values.** The step names, transaction names and think-time edits
  a tester made in Recording Studio carry through, as does the recorder's own
  static-resource classification.
- **Correlation.** Only high-confidence correlations are rewritten
  automatically; the rest stay review candidates.

A recording keeps the credentials its requests carried, as every import does.
Correlation still replaces the values that a response supplied.

An exported recording HAR carries the same `_apexload_*` entry fields. If it
is imported again, its steps, transactions, think times and resource
classification are kept.

## Settings

| Group | Field | Effect |
| --- | --- | --- |
| General | `http_script_level` | `url` (default) or `html` for the browser recorder's HTTP script |
| General | `resource_mode` | `runtime` (default: resources are not recorded and are discovered at replay), `in_step` (attached to their page), or `separate` (their own steps) |
| General | `think_time_threshold_ms` | The smallest recorded pause kept as think time (default 500 ms) |
| Code generation | `page_title_checks` | A `<title>` check on each recorded HTML page (default on) |
| Code generation | `async_scan` | Turns repeated background requests into polling, long-polling or push conversations (default on) |
| HTTP properties | `record_headers`, `drop_headers` | Headers always kept, overriding the recorder's default drop list, and headers never recorded |
| Recording filters | `include_hosts`, `exclude_hosts` | Hosts recorded (`*.domain` matches subdomains); excluded hosts win |
| Recording filters | `exclude_content_types` | Response media types (`image/*` wildcards allowed) not recorded as steps |
| Recording filters | `body_capture_limit_bytes`, `capture_cookies` | Proxy capture limit, and whether recorded `Cookie` headers are kept instead of being left to the cookie jar |
| Correlations: configuration | `correlation.disabled` | Turns the automatic correlation scan off |
| Correlations: configuration | `correlation.disabled_rules` | Rule IDs from the catalog, or whole families (`json`, `html`, `xml`, `header`, `cookie`, `form`, `messaging`), that the scan skips |
| Correlations: configuration | `correlation.excluded_values`, `correlation.excluded_content_types` | Values never correlated, and response types the scan ignores |
| Correlations: configuration | `correlation.min_value_length`, `correlation.max_value_length` | Value length bounds (defaults 4 and 400, as in VuGen) |
| Correlations: configuration | `correlation.case_insensitive`, `correlation.prefer_regex` | Boundaries match without regard to case, and boundary rules generate regex extractors |
| Correlations: project rules | `correlation.user_rules` | Boundary (`left`/`right`) or regex rules, optionally scoped by host and content type. A value a rule finds replaces an automatic correlation of the same value |

Requests a profile filters out are listed in a conversion warning. Nothing is
dropped silently.

## VuGen recording options

The plan's baseline table lists VuGen's recording options. This table gives
each one its APEX setting, or the reason it does not apply to a native
recording.

| VuGen option | APEX native recording | Notes |
| --- | --- | --- |
| Recording level: HTML-based | `http_script_level: html` | Browser recorder only. The proxy recorder and HAR import see requests but not the user actions that HTML-mode steps describe |
| Recording level: URL-based | `http_script_level: url` (default) | Every recorder |
| Advanced HTML: user actions vs explicit URLs | `http_script_level` | The two choices are the `html` and `url` levels |
| Advanced HTML: non-HTML resources in step, in concurrent groups, or not recorded | `resource_mode: in_step, separate, runtime` | Every recorder. The replay baseline follows the resulting steps |
| Advanced URL: concurrent groups; `web_custom_request` only | Not a recording choice | Native scripts are always explicit requests, which is what `web_custom_request` gives VuGen. Resources a page fetches in parallel replay as that step's resources (`in_step`) |
| HTTP properties: `web_reg_find` for page titles | `page_title_checks` | |
| HTTP properties: generate `web_add_cookie` | `capture_cookies` | Off: replay's cookie jar sends cookies, the equivalent of not generating cookie steps. On: the recorded `Cookie` headers are kept |
| HTTP properties: WebSocket steps | Always on | The proxy recorder and HAR import place WebSocket connect, send, receive and close steps on the timeline. Values from received frames that later steps send are correlated |
| HTTP properties: replace passwords with masked parameters | Conversion credential review | Recorded credentials go through the conversion's secret-binding review, not through a profile switch |
| HTTP properties: parameterize server names | Script `base_url` | The recorded origin becomes `config.base_url`, and step targets are relative to it |
| HTTP properties: reset context per action | Not a recording option | This is a runtime behavior: the new-user-each-iteration runtime setting |
| HTTP properties: UTF-8 and EUC-JP charsets | Not applicable | Recorders keep request bodies as exact bytes, base64 when they are not text, so no charset conversion step is needed |
| HTTP properties: headers to record or not | `record_headers`, `drop_headers` | |
| Correlations: rules scan | `correlation.user_rules`, `correlation.disabled_rules` | |
| Correlations: record scan | Automatic scan (`correlation.disabled` turns it off) | |
| Correlations: replay scan | Replay diagnosis | After a debug replay, the script's replay diagnosis (`correlate.DiagnoseReplayReviewChecked`) compares the recorded and replayed responses and proposes missed correlations |
| Correlations: boundary or regex API | `correlation.prefer_regex` | |
| Correlations: exclusions | `correlation.excluded_values`, `correlation.excluded_content_types` | |
| Correlations: 4 to 400 character bounds | `correlation.min_value_length`, `correlation.max_value_length` | |
| Code generation: correlation scan | `correlation.disabled` | |
| Code generation: async scan | `async_scan` | |
| Think-time threshold | `think_time_threshold_ms` | |
| Script language: C or JavaScript | Not applicable | Native recordings produce APEX DSL scripts. There is no source language to choose |
| Registration search scope | Not a recording option | Generated extractors carry their own `source` (body, headers, all) |
| Text checks (`web_reg_find` scope and flags) | Not a recording option | Page title checks are the only recorded checks. Other checks are authored on the step |
| Forms (`web_submit_form`) | `http_script_level: html` | |
| Runtime settings | Not a recording option | Script runtime settings (`config.*`) |
| Run logic | Not a recording option | Script `config.run_logic`; imported VuGen blocks drive execution |

## Limits

- A project keeps at most 32 profiles.
- A list holds at most 128 entries.
- A profile holds at most 64 project correlation rules.
- Rule names must be parameter names.
- A regex rule must capture the value in a group.
- A boundary rule needs both a left and a right boundary.
