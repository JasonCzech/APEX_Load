package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

// importDraft is the converter-agnostic view of a freshly converted import.
//
// Every ingest handler builds one and calls augment instead of hand-attaching
// the cross-cutting concerns an import response is expected to carry. Before
// this existed each handler attached them itself, and the set it remembered
// drifted per format: source review reached 13 of 17 importers, converter
// diagnostics 9, and the MuleSoft/Agentforce detectors 6 — with two of those six
// discarding the sections they produced. Routing every format through one pass
// makes the coverage a property of the code rather than of what each handler
// remembered.
type importDraft = importreview.ImportDraft

// importAugmentation is what the shared pass produces for the response envelope.
type importAugmentation = importreview.ImportAugmentation

// augment applies every cross-cutting concern an import response carries and
// returns what the envelope needs. It mutates the draft's scripts in place:
// project scope, import provenance, preserved review evidence, and preflight all
// live on the script so a later save keeps them.

// augmentWithAgentforceTransform is augment for callers that resolved the
// Agentforce transform opt-in earlier — Postman decides it before conversion,
// because the same flag also drives its native collection handling.

// allScripts is the batch this draft produced, primary first and deduplicated.
// A single-script format supplies only Script; a batch format supplies Scripts
// and usually repeats its primary there.

// primary is the draft whose preflight the response reports.

// ensureImportProvenance records where a draft came from. attachSourceReview
// only writes provenance alongside a review, so a converter that produces no
// source review would otherwise leave the draft unattributable at save time.
var ensureImportProvenance = importreview.EnsureImportProvenance
