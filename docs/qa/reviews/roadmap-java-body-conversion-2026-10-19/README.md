# Roadmap implementation: Java HTTP body conversion

Date: 2026-10-19. Scope: a bounded byte-preservation prerequisite for [PROTO-A/PROTO-B](../../../ROADMAP_IMPLEMENTATION_SPEC.md#proto-a--finish-truthful-measurements-and-propagation). Base: `0c51cc296`.

## Contract and correction

The embedded Java HTTP adapter decodes an authored base64 body exactly once. The shared HTTP adapter then receives opaque bytes and applies any explicitly requested content compression. Caller-owned actions remain unchanged. Replay evidence retains the transformed-body marker so repair cannot treat decoded bytes as editable offsets in the authored base64 text. A redacted snapshot body stays redacted.

Previously, an explicit action-level `body_encoding: base64` survived the Java conversion and triggered a second conversion in HTTP. Binary serialized bytes failed before traffic; opaque bytes that happened to be valid base64 silently changed. The implicit default also omitted transformed-body provenance. Independent endpoint fixtures reproduced these failures before the fix; [the failing run is retained](evidence/java-body-before.log), including its raw non-UTF-8 fixture bytes.

No JVM object interpretation, new helper, diagnostic schema, timing-field reinterpretation or target probe is introduced. Legacy conversion timings retain their meanings. Versioned byte/conversion measurements, propagation, JVM object-footprint measurement and wider protocol qualification remain open.

## Validation

- [Java HTTP and HTTP package tests](evidence/java-body-tests.log) passed, including request compression and existing binary response mapping.
- [Final Java HTTP and correlation-repair package tests](evidence/java-body-final.log) passed. Six endpoint cases cover implicit/explicit binary, base64-looking opaque bytes, whitespace around the encoding, empty input and gzip. Additional cases preserve preflight rejection of malformed/truncated base64 and redacted HTTP error snapshots.
- [Java HTTP race tests](evidence/java-body-race.log) passed. The independent endpoint counter is atomic and no source body is mutated.
- Final source/test review found no further actionable issues after adding the transformed-body provenance assertion and synchronization in the test counter. This patch is confined to the Java HTTP adapter and its tests; the shared HTTP runtime is unchanged.
- Documentation links and whitespace checks pass. No help catalog or dashboard changes occur in this slice; [the preceding both-profile help verification](../roadmap-monotonic-browser-timing-2026-10-19/README.md) remains applicable.

Validation ran on Go 1.26.9, macOS arm64, with local disposable HTTP endpoints. [Evidence hashes](evidence/SHA256SUMS) preserve both the reproduction and passing runs. Build caches remain available for subsequent slices. External reference/platform qualification remains pending because the required environments are unavailable.
