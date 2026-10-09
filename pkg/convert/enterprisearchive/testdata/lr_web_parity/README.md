# LoadRunner web parity corpus

Each directory is one case. `TestLRWebParityCorpus` zips the case's source files, imports the
archive with the production converter, replays it once through the engine against a recording
fixture server, and compares what the server received with `expected.json`.

- Source files are a minimal VuGen Web HTTP/HTML archive: a `.usr`, `default.cfg`, the action
  files, and any data or body files the script references.
- Use `http://parity.test` for every URL. The test points it at the fixture server.
- `expected.json` fields are documented on `paritytest.Expectation`. `outcome` is `replay` when
  APEX must reproduce VuGen's wire traffic, `review` when replay runs but promotion is gated, or
  `gated` when the construct is not supported yet and the import must carry the listed
  execution-gate reason codes instead of converting silently. A `replay` case can list
  `expect_failures` (action name → error fragment) for checks that must fail on a broken page;
  replay then continues past each failure.
- `source` names the VuGen documentation the expectation follows. When a LoadRunner replay of the
  case against the same fixture responses is available, save it as `replay.har`; it then replaces
  the hand-written `requests` as ground truth.

The proxy recorder (`pkg/recorder/testdata/native_parity`) and the HAR pipeline used by the browser
recorder (`pkg/convert/har/testdata/native_parity`) have matching corpora with the same format,
using `session.json` and `recording.har` as their sources.
