package api

import (
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	maxImportProjectionMemoEntries = 1 << 16
	maxImportProjectionMemoBytes   = 8 << 20
)

type importProjectionTextMemoKey struct {
	value string
	kind  uint8
}

// Memoize only pure text operations. One memo can serve several disclosure
// projections (an import response shares one across its drafts'
// attestations), so a header value, label or snippet that repeats across
// thousands of actions is scrubbed once, whatever its length.
// Field, semantic-name, URL, and inherited-sensitivity decisions still happen
// before consulting this memo, and the three operations have separate
// namespaces. scrubMemo shares the scrub package's own text functions with the
// script disclosure projector.
type importProjectionTextMemo struct {
	values    map[importProjectionTextMemoKey]string
	bytes     int
	scrubMemo *scrub.Memo
}

func (memo *importProjectionTextMemo) project(value string, kind uint8, apply func(string) string) string {
	if memo == nil || value == "" {
		return apply(value)
	}
	key := importProjectionTextMemoKey{value: value, kind: kind}
	if cached, ok := memo.values[key]; ok {
		return cached
	}
	projected := apply(value)
	bytes := len(value) + len(projected)
	if len(memo.values) < maxImportProjectionMemoEntries && bytes <= maxImportProjectionMemoBytes-memo.bytes {
		if memo.values == nil {
			memo.values = make(map[importProjectionTextMemoKey]string)
		}
		memo.values[key] = projected
		memo.bytes += bytes
	}
	return projected
}

// scrubber returns the memo's scrub.Memo; a nil memo has none, and a nil
// *scrub.Memo scrubs without caching.
func (memo *importProjectionTextMemo) scrubber() *scrub.Memo {
	if memo == nil {
		return nil
	}
	if memo.scrubMemo == nil {
		memo.scrubMemo = &scrub.Memo{}
	}
	return memo.scrubMemo
}

func (memo *importProjectionTextMemo) scrubScriptText(value string) string {
	return memo.project(value, 0, func(value string) string {
		return scrubScriptTextWithProjector(value, false, memo.scrubber().Value)
	})
}

func (memo *importProjectionTextMemo) scrubReviewText(value string, freeText bool) string {
	kind := uint8(1)
	if freeText {
		kind = 2
	}
	return memo.project(value, kind, func(value string) string {
		redacted, _ := sourceview.Redact(value)
		if freeText {
			redacted = memo.scrubber().Value(redacted)
		}
		return redacted
	})
}
