package api

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestImportProjectionTextMemoRetainsReviewContext(t *testing.T) {
	text := strings.Repeat("ordinary source line\n", 100) + `{"password":"memo-credential-canary"}`
	evidence := map[string]any{
		"source_review": map[string]any{
			"source_files": []any{map[string]any{"content": text}, map[string]any{"content": text}},
			"ordinary":     text,
			"password":     text,
			"properties":   []any{map[string]any{"name": "password", "value": text}},
		},
	}
	memo := &importProjectionTextMemo{}
	got := sanitizeImportReviewNodeWithMemo(evidence, "", false, false, memo)
	want := sanitizeImportReviewNodeWithMemo(evidence, "", false, false, nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("memo changed a sensitivity or free-text context")
	}
	entries := 0
	for key := range memo.values {
		if key.value == text {
			entries++
		}
	}
	if entries != 2 {
		t.Fatalf("memo entries for the shared text = %d, want separate review-only and review-free-text entries", entries)
	}
	for _, key := range []string{"content", "ordinary", "password"} {
		for _, sensitive := range []bool{false, true} {
			if got, want := sanitizeImportReviewNodeWithMemo(text, key, sensitive, true, memo),
				sanitizeImportReviewNodeWithMemo(text, key, sensitive, true, nil); !reflect.DeepEqual(got, want) {
				t.Fatalf("memo changed field %q with inherited sensitivity %t", key, sensitive)
			}
		}
	}
	if got := sanitizeImportReviewNodeWithMemo(text, "password", false, true, memo); got != scrub.Redacted {
		t.Fatal("memo bypassed sensitive field classification")
	}
	if got := sanitizeImportReviewNodeWithMemo(text, "ordinary", false, false, memo); got != text {
		t.Fatal("memo rewrote authoring material outside review evidence")
	}
	ctx := scriptProjectionContext{opaqueMap: true, key: "content", textMemo: memo}
	if got := projectScriptString(text, ctx); got != scrubScriptText(text, false) {
		t.Fatal("memo changed ordinary script text projection")
	}
	ctx.key = "password"
	if got := projectScriptString(text, ctx); got != scrub.Redacted {
		t.Fatal("memo bypassed script sensitive key projection")
	}
}

func TestImportProjectionTextMemoBoundsAndReuse(t *testing.T) {
	memo := &importProjectionTextMemo{}
	calls := 0
	apply := func(value string) string { calls++; return value }
	text := strings.Repeat("ordinary ", 150)
	for i := 0; i < 5; i++ {
		if got := memo.project(text, 0, apply); got != text {
			t.Fatal("memo changed the operation result")
		}
	}
	if calls != 1 {
		t.Fatal("identical text operation was repeated")
	}
	memo.project(text, 1, apply)
	if calls != 2 {
		t.Fatal("memo reused an operation from another namespace")
	}
	for i := 0; i < maxImportProjectionMemoEntries+10; i++ {
		memo.project("v"+strconv.Itoa(i), 0, apply)
	}
	if len(memo.values) != maxImportProjectionMemoEntries || memo.bytes > maxImportProjectionMemoBytes {
		t.Fatal("memo exceeded its entry or byte bound")
	}
	memo = &importProjectionTextMemo{bytes: maxImportProjectionMemoBytes - 1}
	memo.project(text, 0, apply)
	memo.project("short", 0, apply)
	if len(memo.values) != 0 {
		t.Fatal("memo retained text beyond its byte budget")
	}
	// Short values repeat across actions (header values, labels), so they are
	// cached too, and a nil memo applies the operation directly.
	calls = 0
	memo = &importProjectionTextMemo{}
	memo.project("short", 0, apply)
	memo.project("short", 0, apply)
	var none *importProjectionTextMemo
	none.project("short", 0, apply)
	if calls != 2 || len(memo.values) != 1 {
		t.Fatal("short text was not cached once")
	}
}
