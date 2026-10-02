package api

import (
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	minImportProjectionMemoTextBytes = 1024
	maxImportProjectionMemoEntries   = 512
	maxImportProjectionMemoBytes     = 8 << 20
)

type importProjectionTextMemoKey struct {
	value string
	kind  uint8
}

// Memoize only pure text operations within one disclosure projection. Field,
// semantic-name, URL, and inherited-sensitivity decisions still happen before
// consulting this memo, and the three operations have separate namespaces.
type importProjectionTextMemo struct {
	values map[importProjectionTextMemoKey]string
	bytes  int
}

func (memo *importProjectionTextMemo) project(value string, kind uint8, apply func(string) string) string {
	if memo == nil || len(value) < minImportProjectionMemoTextBytes {
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

func (memo *importProjectionTextMemo) scrubScriptText(value string) string {
	return memo.project(value, 0, func(value string) string {
		return scrubScriptText(value, false)
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
			redacted = scrub.Value(redacted, scrub.Policy{})
		}
		return redacted
	})
}
