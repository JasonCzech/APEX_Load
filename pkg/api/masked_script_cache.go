package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// maskScriptSecrets is a pure function of the script's content, and one import
// request masks the same script several times: the draft claim's credential
// fingerprint, binding scopes, reconciliation and the response, then the save
// masks it again. On a multi-thousand-step VuGen import each pass costs seconds.
// Its costly projection is kept by a digest of the unmasked script's JSON, as
// masked JSON, so a repeat costs one decode and a few cheap restores. Only
// masked output is held, and the cache is bounded by entries and bytes.
const maskedScriptCacheBytes = importreview.MaskedScriptCacheBytes
const maskedScriptCacheEntries = importreview.MaskedScriptCacheEntries

var maskedScripts = importreview.MaskedScripts

type maskedScriptCache = importreview.MaskedScriptCache

type maskedScriptEntry = importreview.MaskedScriptEntry

var newMaskedScriptCache = importreview.NewMaskedScriptCache

// get returns the masked JSON for key, or nil. Callers decode their own copy.
