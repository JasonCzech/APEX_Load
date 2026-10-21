# Native browser lifecycle fixtures — 2026-10-20

Two independent native lifecycle fixtures extend the existing document evidence checks. They change no runtime defaults or wire contracts.

- **WebKit process loss:** an isolated child owns desktop and iPhone 15 Pro emulation contexts. The fixture verifies the exact WebKit executable under its own Playwright driver before killing it, observes native disconnect, preserves completed measurements, censors unfinished measurements, and verifies a fresh pool replacement with a new source generation. Later teardown cannot revise the lost populations or damage the replacement.
- **Genuine BFCache restore:** isolated desktop and Pixel 8 emulation contexts use bundled full Chromium with `--disable-back-forward-cache` removed. Trusted persisted `pageshow`, a preserved heap marker/time origin and one initial target request establish actual restoration. The original finalized FCP remains unchanged, restored measurements have a distinct lifecycle, and teardown leaves no provisional or duplicate measurements.

The BFCache fixture deliberately differs from the pinned default launch, which uses headless shell and disables BFCache. Its evidence cannot promote default-runtime BFCache support. macOS emulation is not an actual iPhone/Pixel device, Linux/Windows qualification or reference-host performance evidence.

## Validation and review

- The complete browser package passed **779 tests/subtests**, with three explicit benchmark/platform skips (`TestBrowserBenchmark`, `TestProcProcessSamplesIncludeThisProcess`, `TestBrowserVUserMarginalRSS`). Native runtime tests were required.
- Both new lifecycle fixtures passed **three race-detector repetitions** (six outer tests); BFCache exercises both desktop and Pixel 8 emulation in every repetition.
- Documentation checks passed for **299 active Markdown files**. Embedded assets are byte-identical to the preceding reviewed slice, including the Help modules verified there in both auth profiles. This test-only slice changes no dashboard or runtime behavior.
- Self-review fixed the roadmap prose finding; the final review found no issues. Review rounds (`evidence/review-rounds.json`), validation summary (`evidence/validation-summary.json`), source hashes (`evidence/source-hashes.json`) and checksums (`evidence/SHA256SUMS`) retain the scope and commands.

The exploratory BFCache logs are retained separately: headless-shell attempts did not restore, and the first full-Chromium attempt used an unsuitable `load` wait. The final fixture waits for native history commit and independently verifies restoration. These earlier failures are diagnostic evidence, not passing qualifications. Runtime versions and viewport/platform details appear in the native logs. The successful local cohorts use Chromium 149.0.7827.55 and WebKit 26.5 on macOS/arm64.
