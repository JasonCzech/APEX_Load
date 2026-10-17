package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

const maxImportProjectionMemoEntries = importreview.MaxImportProjectionMemoEntries
const maxImportProjectionMemoBytes = importreview.MaxImportProjectionMemoBytes

type importProjectionTextMemoKey = importreview.ImportProjectionTextMemoKey

// Memoize only pure text operations. One memo can serve several disclosure
// projections (an import response shares one across its drafts'
// attestations), so a header value, label or snippet that repeats across
// thousands of actions is scrubbed once, whatever its length.
// Field, semantic-name, URL, and inherited-sensitivity decisions still happen
// before consulting this memo, and the three operations have separate
// namespaces. scrubMemo shares the scrub package's own text functions with the
// script disclosure projector.
type importProjectionTextMemo = importreview.ImportProjectionTextMemo

// scrubber returns the memo's scrub.Memo; a nil memo has none, and a nil
// *scrub.Memo scrubs without caching.
