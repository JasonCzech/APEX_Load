package dslwalk

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRequestScalarPresenceAndMalformedInput(t *testing.T) {
	for _, tc := range []struct {
		name           string
		input          map[string]any
		present, valid bool
		value          string
	}{
		{"absent", map[string]any{}, false, true, ""},
		{"empty", map[string]any{"body": ""}, true, true, ""},
		{"null", map[string]any{"body": nil}, true, false, ""},
		{"object", map[string]any{"body": map[string]any{"id": 1234567}}, true, false, ""},
		{"literal", map[string]any{"body": "payload"}, true, true, "payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := json.Marshal(tc.input)
			ref := SurfaceRef{Request: tc.input}
			got := ref.Body()
			if got.Present != tc.present || got.Valid != tc.valid || got.Value != tc.value {
				t.Fatalf("field = %#v", got)
			}
			if !reflect.DeepEqual(got.Raw, tc.input["body"]) {
				t.Fatalf("raw body lost: %#v", got.Raw)
			}
			after, _ := json.Marshal(tc.input)
			if string(before) != string(after) {
				t.Fatalf("reading changed input: %s -> %s", before, after)
			}
			ref.SetBody("")
			if raw, exists := tc.input["body"]; !exists || raw != "" {
				t.Fatalf("empty setter erased presence: %#v", tc.input)
			}
		})
	}
}

func TestRequestViewTypedAndJSONRoundTripParity(t *testing.T) {
	disabled := false
	original := dsl.Action{Type: "request", Target: "/orders", Body: "literal body", BodyEncoding: "base64",
		HeaderList: dsl.HeaderList{{Name: "X-Duplicate", Value: "one"}, {Name: "x-duplicate", Value: "two"}, {Name: "X-Disabled", Value: "kept", Enabled: &disabled}},
		Headers:    map[string]string{"x-duplicate": "two"},
	}
	original.HTTPVersion, _ = dsl.NewHTTPVersionOverride("2")
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	typed, mapped := SurfaceRef{Action: &original}, SurfaceRef{Request: request}
	for name, pair := range map[string][2]StringField{"target": {typed.Target(), mapped.Target()}, "body": {typed.Body(), mapped.Body()}, "encoding": {typed.BodyEncoding(), mapped.BodyEncoding()}, "version": {typed.HTTPVersion(), mapped.HTTPVersion()}} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Fatalf("%s: %#v != %#v", name, pair[0], pair[1])
		}
	}
	for _, ref := range []SurfaceRef{typed, mapped} {
		fields := ref.Headers()
		if !fields.ListPresent || !fields.MapPresent {
			t.Fatal("header presence lost")
		}
		ref.SetTarget("/new")
		ref.SetBody("changed")
		ref.SetBodyEncoding("")
		if err := ref.SetHTTPVersion("1.1"); err != nil {
			t.Fatal(err)
		}
		// Compatibility mirrors are a caller decision; preserve duplicate order and disabled entries.
		ref.SetHeaders(original.HeaderList, original.Headers)
	}
	got, _ := json.Marshal(request)
	want, _ := json.Marshal(original)
	var decoded map[string]any
	if err := json.Unmarshal(want, &decoded); err != nil {
		t.Fatal(err)
	}
	// Typed strings use omitempty. The map retains the explicitly authored empty encoding.
	decoded["body_encoding"] = ""
	want, _ = json.Marshal(decoded)
	var gotObject, wantObject any
	if err := json.Unmarshal(got, &gotObject); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantObject); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotObject, wantObject) {
		t.Fatalf("round-trip edit differs:\n%s\n%s", got, want)
	}
	for _, ref := range []SurfaceRef{typed, mapped} {
		ref.ClearHTTPVersion()
		ref.ClearBodyEncoding()
		ref.ClearHeaders()
		if ref.HTTPVersion().Present || ref.BodyEncoding().Present || ref.Headers().ListPresent || ref.Headers().MapPresent {
			t.Fatal("clear retained presence")
		}
	}
}

func TestRequestHeaderViewPreservesMalformedAndUnknownShapes(t *testing.T) {
	input := map[string]any{"header_list": []any{map[string]any{"name": "X", "value": "1", "enabled": false, "extension": "retained"}, nil, 17, map[string]any{"name": "X", "value": map[string]any{"malformed": true}}}, "headers": map[string]any{"X": "legacy", "Bad": []any{1, 2}}}
	before, _ := json.Marshal(input)
	ref := SurfaceRef{Request: input}
	fields := ref.Headers()
	if !fields.ListPresent || !fields.MapPresent || !reflect.DeepEqual(fields.List, input["header_list"]) || !reflect.DeepEqual(fields.Map, input["headers"]) {
		t.Fatal("raw header view changed authored shape")
	}
	ref.SetTarget("/unrelated")
	delete(input, "target")
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatalf("unrelated edit changed malformed headers: %s", after)
	}
}

func TestRequestVersionDoesNotRepairMalformedOrEmptyValuesOnRead(t *testing.T) {
	for _, raw := range []string{`null`, `""`, `"invalid"`, `"2"`} {
		var version dsl.OptionalHTTPVersion
		if err := json.Unmarshal([]byte(raw), &version); err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		typed := SurfaceRef{Action: &dsl.Action{HTTPVersion: version}}
		mapped := SurfaceRef{Request: map[string]any{"http_version": value}}
		if !reflect.DeepEqual(typed.HTTPVersion(), mapped.HTTPVersion()) {
			t.Fatalf("version %s changed between representations", raw)
		}
		before := mapped.Request["http_version"]
		if err := mapped.SetHTTPVersion("not-a-policy"); err == nil {
			t.Fatal("invalid setter succeeded")
		}
		if !reflect.DeepEqual(before, mapped.Request["http_version"]) {
			t.Fatal("invalid setter mutated source")
		}
	}
}

func TestRequestViewVisitsTypedAndMapNestedSurfaces(t *testing.T) {
	script := &dsl.Script{Init: []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": []dsl.Action{{Type: "request", Target: "/typed"}}}}}, Actions: []dsl.Action{{Type: "http_async_start", Params: map[string]any{"request": map[string]any{"target": "/mapped", "body": ""}}}}}
	paths := []string{}
	if err := Walk(script, func(ref SurfaceRef) error {
		if !ref.IsNestedRequest() {
			return nil
		}
		paths = append(paths, ref.Path)
		ref.SetTarget(ref.Target().Value + "/edited")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("visited %v", paths)
	}
	if script.Init[0].Params["requests"].([]dsl.Action)[0].Target != "/typed/edited" || script.Actions[0].Params["request"].(map[string]any)["target"] != "/mapped/edited" {
		t.Fatal("nested edits not published")
	}
}
