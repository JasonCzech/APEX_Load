# CLM import response performance — 2026-10-01

The first actual collection validation took 130.662 seconds. The controller's
60-second HTTP write deadline expired, so the browser received a Bad Gateway
response. A read-only macOS process sample located repeated work in private
credential disclosure projections and JSON fallback scrubbing. No broker or
application host was contacted during this investigation.

The performance fixes retain the credential boundary:

- The response builder reuses the primary script's private projection when
  its exact normalized source equals a member of the script collection.
- Source-credential discovery reuses that private projection rather than
  projecting the same scripts again. The projection is a separate internal
  return value and is never an HTTP response field.
- Attestation fingerprinting clones its existing detached projection for
  canonicalization instead of repeating credential projection. All digest,
  persistent-review, and capability-removal checks remain in place.
- Both field-aware API review projection and generic script disclosure memoize
  repeated large text within one projection. Each memo is capped at 8 MiB;
  the API memo additionally caps entries at 512, and the generic memo at 256.
  Sensitive-field decisions precede API memo lookup, operation modes have
  separate keys, and known-credential checks still follow generic memo lookup.
- Template protection and restoration scan the text once each instead of
  copying it once per reference. Source-collision checks remain active.
- JSON fallback redaction reuses the original regular-expression capture
  offsets and skips that stage when the required quote/colon syntax is absent.
  Malformed string, scalar, and compound protections remain unchanged.

Source scripts and source-review material remain exact authoring artifacts.
These optimizations reuse private disclosure work; they do not remove source
settings or flatten the evidence needed to inspect fidelity.

## Measurements

| Measurement | Result | Scope |
| --- | --- | --- |
| Private API builder probe on the largest supplied converted result | 2.000 seconds; 5,201,881 response bytes | `newScriptImportResponse`, with the primary script and one-member script array; excludes attestation/session preparation and the other review-envelope fields |
| Actual four-archive `/validate` response after the first optimization pass | HTTP 200; 13.68 seconds; 22,448,161 bytes | Direct local API request; all four child imports succeeded and retained ten parameters in total |
| Ten repeated approximately 70 KiB source strings | 55.98 ms total | Synthetic one-iteration benchmark; scoped generic disclosure memo |
| 8,000 portable template references | 17.98 ms | Synthetic one-iteration protection/restoration benchmark |

These are local observations, not production benchmarks. An earlier browser
retry using its own import options still hit the old 60-second write deadline
while simultaneous Go builds were active. The collection handler now uses a
finite collection-specific response budget and cancellation cleanup. Final
browser verification and the full Go merge gate are recorded in the combined
review once the final controller build is ready.

## Regression coverage

The full `pkg/scrub` and `pkg/scriptdisclosure` suites passed. JSON fallback
tests compare the new capture reuse with the previous implementation across
escaped, malformed, compound, truncated, and seeded generated cases. Scoped
memo tests cover both limits, source collisions, sensitive-field changes, and
known-credential equivalence after lookup.

The first API check passed primary projection reuse, parameterization trust
filtering, neutral located credentials, and header/multipart fidelity checks.
Additional API tests cover review-context equivalence, private source-value
discovery equivalence, and unchanged canonical attestation digests without
mutating the shared projection. The final full Go run covers these latest
changes together with the collection deadline and cancellation tests.

The temporary private API probe was removed. Customer source, payloads,
credential values, and private hostnames are absent from these notes and all
committed regression fixtures.
