# Proposed pinned owned-HAR close acknowledgment

Scope is a local source replacement of github.com/mxschmitt/playwright-go v0.6100.0, preserving its Go API/module identity and current native driver/browser versions. The current exported Close result is insufficient: its IsClosed guard reports nil for a previous/in-flight client close or a server-closed context without executing or joining the HAR export/body. The project must not turn that result into writer-joined.json.

## Source boundary and integrity

Proposed directory: third_party/playwright-go. Copy the 66 root production Go files (933,072 bytes), internal/safe/map.go (1,519 bytes), all nine upstream root test files (40,557 bytes), LICENSE (MIT, Copyright 2020 Max Schmitt), go.mod/go.sum, and upstream README. Preserve an UPSTREAM_MANIFEST.json with original copied-file hashes, module version and module-zip checksum, plus an APEX_PATCHES.md listing only the altered/new files and optional method contract. Do not copy the 2.9 MB upstream native integration assets or its CI, build patches, scripts, or examples; the manifest explicitly identifies this package-source subset. The current complete cached 364-file upstream module matches its downloaded zip byte-for-byte. The Go module zip h1 is HYNnbGZsTHz8veJyDGe4fU1iPxfvXqzmwKchzuvGCsY= and matches its cache ziphash. No global module cache or installed driver file is changed.

Production delta: browser_context.go and one new owned_har_close.go only. Add new owned_har_close_test.go tests. Keep generated-interfaces.go and driver/version/platform pinning unchanged. This is approximately one MB of committed SDK source, rather than vendoring all application dependencies.

## Owned method and atomic entry

Add an optional exported interface/method CloseHAROwned(...BrowserContextCloseOptions) error on the concrete SDK context, separate from generated BrowserContext. Its one retained state records start, completion channel and exact result. Under that state mutex:

1. A prior owned invocation joins the same completion channel and returns its retained complete result; it never runs another export/close body.
2. A prior server-closed flag or a failed closeWasCalled.CompareAndSwap(false,true) before any owned entry produces an explicit ownership-unproven error. Prior ordinary Close cannot be adopted, even if it eventually succeeds.
3. A fresh successful CAS reserves the client close entry. It calls the existing complete close body BELOW the early-nil guard. Once reserved, it never consults IsClosed to skip the physical work. Unexpected server close before/during protocol acknowledgments consequently returns an error from that body, rather than an invented nil receipt.
4. The retained result is published only after request disposal, any explicit HAR export, server close acknowledgment and actual close notification wait complete. Panic/Goexit record explicit unproven completion (with completion markers) and wake repeat callers; panic behavior remains a panic for the entered caller. No error or partial path yields nil proof.

Ordinary Close retains its current public early-nil behavior. Its entry uses CAS rather than Store so two ordinary calls racing, or an ordinary caller racing the owned method, cannot enter duplicate close/export bodies. Extract the shared complete body without changing its statements or protocol phases. The concrete owned method bypasses the early-nil guard after successful ownership CAS.

The current constructor's unbuffered single send into b.closed can block the SDK dispatcher when an earlier close-body error returned before consuming it. Convert its existing Once close notification to closing the channel (broadcast completion), preserving ordinary Close waiting semantics. There is only one existing Once emitter and one production receive. Never infer native HAR completion solely from that notification: successful owned result still requires the complete body and all protocol acknowledgments.

## APEX integration

Bind the capture owner's closer to the optional CloseHAROwned method, with explicit unavailable error if the concrete SDK cannot provide it. Unit owner callback fixtures retain their deliberately controlled closers. Native gated fixtures call the owned method below their test gate. No public reflection/private-memory access, network health probe, shared browser Stop, PID/age inference, raw-JSON-validity inference, driver process kill, or new native dependency/version is needed.

Candidate modules and packaging that must change after approval: root go.mod adds replace github.com/mxschmitt/playwright-go => ./third_party/playwright-go; run root go mod tidy and preserve its actual go.sum delta. Root go test ./... does not execute nested/dependency module tests, so add an explicit CI and Make test step: go test -race github.com/mxschmitt/playwright-go -run '^TestAPEXOwnedHAR' -count=1. SDK contract tests run under root dependency resolution. Retain copied upstream tests but do not run their native installation/process-kill cases as this slice's acceptance. No new third_party pkg inventory entry is needed.

All six container dependency stages must copy third_party/playwright-go before go mod download: Dockerfile, Dockerfile.controller, runners/java_vuser/Dockerfile, Dockerfile.supervisor, Dockerfile.broker, Dockerfile.facade. Existing .dockerignore permits this directory. Add a focused packaging assertion proving each stage preserves the local replacement before download. Existing desktop checkout contains the local replacement; **/*.go already triggers its build workflow. Native pins/installer flags are unchanged. Standard controller/generator/desktop/recording builds now resolve the same pinned local SDK; composed full gates and existing release lanes remain necessary.

## Required proof before qualification

Preserve the original-v5 RED native reproducer separately: an invented loopback navigation blocks the Go SDK's synchronous OnRequestFinished dispatch callback; a separately owned exact-context Close has entered IsClosed but cannot receive the dispose acknowledgment/HAR export, so it is physically unjoined. Old owner.startClose must incorrectly produce a joined seal while that callback is blocked. All paths release the gate once and join exact navigation/close workers; no shared-driver Stop.

SDK contract unit/race tests: fresh nil body only after completion; concurrent owned callers join one body; retained failures (ordinary error, panic, Goexit) remain explicit; prior ordinary/in-flight entry fails; prior server-closed entry fails; ordinary-vs-owned entry ordering/CAS; close notification after a failed body cannot block dispatcher; no duplicate export/close body. Native required Chromium/WebKit: the same blocked-original-close state fails closed without seal/publication/retirement; already closed contexts fail closed; fresh actual close flushes and joins; close failures/deadlines retain lease and charge; independent sibling browser remains alive. Continue existing HAR lease/retirement/collision tests and API retention gates, then final independent source review.

This does not qualify arbitrary external close entry, crash reclamation, full native HAR archive transfer/export, aggregate active-writer quotas/RSS, or full BE-3. Unknown failed ownership remains protected and charged. Existing secret suppression and capture body policies are unchanged.
