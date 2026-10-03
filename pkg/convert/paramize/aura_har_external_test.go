package paramize_test

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/paramize"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"net/url"
	"testing"
)

func auraHARBody(params any) string {
	b, _ := json.Marshal(map[string]any{"actions": []any{map[string]any{"id": "synthetic-1;a", "descriptor": "synthetic://save", "params": params}}})
	return string(b)
}
func auraHARSnapshot(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestAuraAdviceFromActualHARConversion(t *testing.T) {
	body := auraHARBody(map[string]any{"input": `{"records":[{"recordId":"synthetic-record"}]}`})
	capture := map[string]any{"log": map[string]any{"version": "1.2", "entries": []any{map[string]any{"startedDateTime": "2026-10-03T00:00:00Z", "time": 1, "request": map[string]any{"method": "POST", "url": "https://synthetic.invalid/aura", "headers": []any{}, "postData": map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": "message=" + url.QueryEscape(body)}}, "response": map[string]any{"status": 200, "headers": []any{}, "content": map[string]any{"mimeType": "application/json", "text": `{"actions":[{"id":"synthetic-1;a","state":"SUCCESS"}]}`}}}}}}
	converted, err := har.Convert([]byte(auraHARSnapshot(t, capture)), har.ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result := paramize.Suggest(converted.Script, paramize.Options{EnableDataParams: true})
	if len(result.Suggestions) != 1 || result.AuraPayload == nil || result.AuraPayload.InspectedBodies != 1 {
		t.Fatalf("HAR advice coverage/count = %+v/%d", result.AuraPayload, len(result.Suggestions))
	}
	selected := result.Suggestions[0]
	applied, _, err := paramize.ApplySuggestions(converted.Script, result.Suggestions, map[string]bool{selected.ID: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	_ = dslwalk.Walk(applied, func(ref dslwalk.SurfaceRef) error {
		body, encoding := ref.Action.Body, ref.Action.BodyEncoding
		if ref.Path == selected.Locations[0].ActionPath {
			inspection, err := aurapayload.Inspect(body, encoding)
			if err != nil {
				t.Fatal(err)
			}
			for _, leaf := range inspection.Leaves {
				if leaf.Key == "recordId" && leaf.Value == selected.Replacement {
					found = true
				}
			}
		}
		return nil
	})
	if !found {
		t.Fatal("canonical HAR action not precisely parameterized")
	}
}
