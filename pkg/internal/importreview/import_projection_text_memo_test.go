package importreview

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestImportProjectionTextMemoRetainsReviewContext(t *testing.T) {
	Text := strings.Repeat("ordinary source line\n", 100) + `{"password":"memo-credential-canary"}`
	evidence := map[string]any{
		"source_review": map[string]any{
			"source_files": []any{map[string]any{"content": Text}, map[string]any{"content": Text}},
			"ordinary":     Text,
			"password":     Text,
			"properties":   []any{map[string]any{"name": "password", "value": Text}},
		},
	}
	memo := &ImportProjectionTextMemo{}
	got := SanitizeImportReviewNodeWithMemo(evidence, "", false, false, memo)
	want := SanitizeImportReviewNodeWithMemo(evidence, "", false, false, nil)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("memo changed a sensitivity or free-text context")
	}
	entries := 0
	for key := range memo.values {
		if key.value == Text {
			entries++
		}
	}
	if entries != 2 {
		t.Fatalf("memo entries for the shared text = %d, want separate review-only and review-free-text entries", entries)
	}
	for _, key := range []string{"content", "ordinary", "password"} {
		for _, sensitive := range []bool{false, true} {
			if got, want := SanitizeImportReviewNodeWithMemo(Text, key, sensitive, true, memo),
				SanitizeImportReviewNodeWithMemo(Text, key, sensitive, true, nil); !reflect.DeepEqual(got, want) {
				t.Fatalf("memo changed field %q with inherited sensitivity %t", key, sensitive)
			}
		}
	}
	if got := SanitizeImportReviewNodeWithMemo(Text, "password", false, true, memo); got != scrub.Redacted {
		t.Fatal("memo bypassed sensitive field classification")
	}
	if got := SanitizeImportReviewNodeWithMemo(Text, "ordinary", false, false, memo); got != Text {
		t.Fatal("memo rewrote authoring material outside review evidence")
	}
	ctx := ScriptProjectionContext{opaqueMap: true, key: "content", TextMemo: memo}
	if got := ProjectScriptString(Text, ctx); got != ScrubScriptText(Text, false) {
		t.Fatal("memo changed ordinary script text projection")
	}
	ctx.key = "password"
	if got := ProjectScriptString(Text, ctx); got != scrub.Redacted {
		t.Fatal("memo bypassed script sensitive key projection")
	}
}

func TestImportProjectionTextMemoBoundsAndReuse(t *testing.T) {
	memo := &ImportProjectionTextMemo{}
	calls := 0
	apply := func(value string) string { calls++; return value }
	Text := strings.Repeat("ordinary ", 150)
	for i := 0; i < 5; i++ {
		if got := memo.Project(Text, 0, apply); got != Text {
			t.Fatal("memo changed the operation result")
		}
	}
	if calls != 1 {
		t.Fatal("identical text operation was repeated")
	}
	memo.Project(Text, 1, apply)
	if calls != 2 {
		t.Fatal("memo reused an operation from another namespace")
	}
	for i := 0; i < MaxImportProjectionMemoEntries+10; i++ {
		memo.Project("v"+strconv.Itoa(i), 0, apply)
	}
	if len(memo.values) != MaxImportProjectionMemoEntries || memo.bytes > MaxImportProjectionMemoBytes {
		t.Fatal("memo exceeded its entry or byte bound")
	}
	memo = &ImportProjectionTextMemo{bytes: MaxImportProjectionMemoBytes - 1}
	memo.Project(Text, 0, apply)
	memo.Project("short", 0, apply)
	if len(memo.values) != 0 {
		t.Fatal("memo retained text beyond its byte budget")
	}
	// Short values repeat across actions (header values, labels), so they are
	// cached too, and a nil memo applies the operation directly.
	calls = 0
	memo = &ImportProjectionTextMemo{}
	memo.Project("short", 0, apply)
	memo.Project("short", 0, apply)
	var none *ImportProjectionTextMemo
	none.Project("short", 0, apply)
	if calls != 2 || len(memo.values) != 1 {
		t.Fatal("short text was not cached once")
	}
}
