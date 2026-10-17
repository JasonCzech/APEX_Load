package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	MaxImportProjectionMemoEntries = 1 << 16
	MaxImportProjectionMemoBytes   = 8 << 20
)

type ImportProjectionTextMemoKey struct {
	value string
	kind  uint8
}

type ImportProjectionTextMemo struct {
	values    map[ImportProjectionTextMemoKey]string
	bytes     int
	scrubMemo *scrub.Memo
}

func (memo *ImportProjectionTextMemo) Project(value string, kind uint8, apply func(string) string) string {
	if memo == nil || value == "" {
		return apply(value)
	}
	key := ImportProjectionTextMemoKey{value: value, kind: kind}
	if cached, ok := memo.values[key]; ok {
		return cached
	}
	projected := apply(value)
	bytes := len(value) + len(projected)
	if len(memo.values) < MaxImportProjectionMemoEntries && bytes <= MaxImportProjectionMemoBytes-memo.bytes {
		if memo.values == nil {
			memo.values = make(map[ImportProjectionTextMemoKey]string)
		}
		memo.values[key] = projected
		memo.bytes += bytes
	}
	return projected
}

func (memo *ImportProjectionTextMemo) Scrubber() *scrub.Memo {
	if memo == nil {
		return nil
	}
	if memo.scrubMemo == nil {
		memo.scrubMemo = &scrub.Memo{}
	}
	return memo.scrubMemo
}

func (memo *ImportProjectionTextMemo) ScrubScriptText(value string) string {
	return memo.Project(value, 0, func(value string) string {
		return ScrubScriptTextWithProjector(value, false, memo.Scrubber().Value)
	})
}

func (memo *ImportProjectionTextMemo) ScrubReviewText(value string, freeText bool) string {
	kind := uint8(1)
	if freeText {
		kind = 2
	}
	return memo.Project(value, kind, func(value string) string {
		redacted, _ := sourceview.Redact(value)
		if freeText {
			redacted = memo.Scrubber().Value(redacted)
		}
		return redacted
	})
}
