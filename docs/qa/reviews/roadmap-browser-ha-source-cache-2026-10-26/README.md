# Roadmap BE-B4 — HA Source-Cache HAR and Interaction Retention (2026-10-26)

This record covers the `BE-B4` (`be-b4-ha-source-cache`) slice retaining optional browser HARs and raw interaction sidecars in the `_collection` source cache across HA archive collection retries.

## Scope Delivered

1. **Best-Effort Optional Evidence Caching (`optional_receipt.json`)**:
   - [`fetchRemoteArchiveBundles`](../../../../pkg/api/distributed_archive.go) fetches required source files plus optional HAR and interaction files and invokes [`collectionSourceCache.save`](../../../../pkg/api/archive_collection_cache.go), which writes required files and `receipt.json` first and then runs a best-effort [`collectionSourceCache.stageOptional`](../../../../pkg/api/archive_collection_cache.go) pass inside the `.pending-<uuid>` directory before atomic `parent.Rename(stage, anchor.ReceiptSHA256)`.
   - `stageOptional` writes optional digests into `optional_receipt.json` bound to `anchor.ReceiptSHA256` without mutating `receipt.json` or SQL `SaveRunArchiveCollectionSource`, preventing cross-pod `ReceiptSHA256` drift when HA pods have different free disk space and keeping the renamed `anchor.ReceiptSHA256` directory immutable.
   - Exceeding `APEXLOAD_ARCHIVE_COLLECTION_MAX_BYTES`, `APEXLOAD_ARCHIVE_COLLECTION_CACHE_MAX_BYTES`, or the `64 MiB` free-space floor skips only the optional files that do not fit and never fails `save`.
2. **Cache-First Optional Retrieval, Live Fallback, and `source_unavailable` Classification**:
   - [`optionalEvidenceSource`](../../../../pkg/api/archive_browser_har.go) serves cached optional files from [`cachedArchiveSource`](../../../../pkg/api/archive_collection_cache.go) when present, falls back to the live generator client when an optional file was not cached and the generator is still reachable, and returns `errOptionalEvidenceSourceUnavailable` when the generator is lost and the optional file was not cached so [`fetchRemoteHARArtifacts`](../../../../pkg/api/archive_browser_har.go) counts `source_unavailable` (not `transfer_failed`) and [`fetchRemoteInteractions`](../../../../pkg/api/archive_browser_interactions.go) reports `source_unavailable`.
3. **Directory Entry Bounds & Cleanup Verification**:
   - Updated [`collectionSourceCache.decode`](../../../../pkg/api/archive_collection_cache.go) and [`verifyCollectionCacheForCleanup`](../../../../pkg/api/archive_collection_cache_cleanup.go) (`ReadDir(maxCollectionCacheDirectoryEntries + 1)`) to validate `optional_receipt.json` and verify size and SHA-256 of all cached optional files before `_collection` directory retirement.
   - Updated [BROWSER_HAR_WORKING_OWNERSHIP.md](../../../guides/BROWSER_HAR_WORKING_OWNERSHIP.md) to document `_collection` optional HAR and interaction retention.

## Verification

- `go test -race -count=2 -shuffle=on -run "TestDistributedArchiveTransfersHARsWithReceipts|TestDistributedArchiveMergesInteractionEvidenceWithReceipts|TestCachedArchiveSource|TestCollectionCache" ./pkg/api`
- `python3 scripts/check-docs.py`
