package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectLiveTableMutationJSONRejectsLossyRepresentations(t *testing.T) {
	for name, body := range map[string]string{
		"null object":        `null`,
		"array":              `[]`,
		"extra document":     `{} {}`,
		"unknown identity":   `{"owner_id":"forged"}`,
		"folded key":         `{"Values":{"value":"data"}}`,
		"Unicode folded key": `{"value\u017f":{"value":"data"}}`,
		"duplicate field":    `{"values":{"first":"data"},"values":{"value":"data"}}`,
		"escaped duplicate":  `{"operation":"append","oper\u0061tion":"append"}`,
		"duplicate cell":     `{"values":{"value":"first","value":"second"}}`,
		"escaped cell":       `{"values":{"value":"first","val\u0075e":"second"}}`,
		"null map":           `{"values":null}`,
		"null cell":          `{"values":{"value":null}}`,
		"number cell":        `{"values":{"value":1}}`,
		"boolean cell":       `{"values":{"value":true}}`,
		"nested cell":        `{"values":{"value":{"nested":"data"}}}`,
		"null scalar":        `{"position":null}`,
		"wrong scalar":       `{"position":"1"}`,
		"null columns":       `{"columns":null}`,
		"null column":        `{"columns":[null]}`,
		"number column":      `{"columns":[1]}`,
		"high surrogate":     `{"values":{"value":"\ud800"}}`,
		"low surrogate":      `{"values":{"value":"\udc00"}}`,
		"separated pair":     `{"values":{"value":"\ud800x\udc00"}}`,
		"malformed key":      `{"values":{"\ud800":"data"}}`,
		"invalid UTF-8":      "{\"values\":{\"value\":\"\xff\"}}",
		"oversized body":     `{"values":{"value":"` + strings.Repeat("x", 1<<20) + `"}}`,
		"too many columns":   `{"columns":[` + strings.Repeat(`"value",`, 128) + `"value"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			input, err := decodeProjectTableMutationJSON([]byte(body))
			if err != errProjectTableMutationJSON || input.Values != nil || input.Operation != "" {
				t.Fatalf("invalid representation admitted: %+v %v", input, err)
			}
		})
	}
	for _, tc := range []struct{ wire, want string }{
		{`""`, ""},
		{`"\ud83d\ude80"`, "🚀"},
		{`"\\ud800"`, `\ud800`},
		{`"{{secret.literal}}"`, "{{secret.literal}}"},
		{`"Grüße 🚀"`, "Grüße 🚀"},
		{`"\ufffd"`, "�"},
	} {
		input, err := decodeProjectTableMutationJSON([]byte(`{"values":{"value":` + tc.wire + `},"columns":["value"]}`))
		value, present := input.Values["value"]
		if err != nil || !present || value != tc.want || len(input.Columns) != 1 || input.Columns[0] != "value" {
			t.Fatalf("valid literal changed: %q => %+v %v", tc.wire, input, err)
		}
	}
}

func TestProjectLiveTableOperatorMutationRouteRejectsInvalidCellsBeforeAdmission(t *testing.T) {
	server, _, definition := newProjectOperatorServer(t)
	enableTrustedHeaderAuth(t, server)
	path := "/api/v1/projects/" + definition.ProjectID + "/live-tables/" + definition.ID + "/mutations"
	input := projectOperatorInput(definition.CurrentInstanceID)
	input.Values = nil
	envelope, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	withField := func(field string) string { return string(envelope[:len(envelope)-1]) + "," + field + "}" }
	do := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Auth-Request-User", "strict-operator")
		request.Header.Set("X-APEXLoad-Project-Roles", definition.ProjectID+"="+RoleEditor)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response
	}
	for _, field := range []string{
		`"values":{"value":null}`,
		`"values":{"value":42}`,
		`"Values":{"value":"private-invalid-data"}`,
		`"values":{"value":"first"},"values":{"value":"private-invalid-data"}`,
		`"values":{"value":"first","value":"private-invalid-data"}`,
		`"values":{"value":"\ud800"}`,
		`"values":{"value":"` + strings.Repeat("x", 1<<20) + `"}`,
	} {
		response := do(withField(field))
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "private-invalid-data") {
			t.Fatalf("invalid mutation response %d %s", response.Code, response.Body.String())
		}
	}
	stats, err := server.runner.projectLiveTableService().Stats(context.Background(), definition.ProjectID, definition.ID, definition.CurrentInstanceID)
	if err != nil || stats.Revision != 0 || stats.Counts.Available != 0 {
		t.Fatalf("invalid request changed table: %+v %v", stats, err)
	}
	// The same command identity is still available. Empty string is a real,
	// present cell, and an exact replay must not append a second row.
	valid := withField(`"values":{"value":""}`)
	var rowID string
	for attempt := 0; attempt < 2; attempt++ {
		response := do(valid)
		var result ProjectLiveTableMutationResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Result.Row == nil {
			t.Fatalf("valid mutation response %d %s", response.Code, response.Body.String())
		}
		value, present := result.Result.Row.Values["value"]
		if !present || value != "" || result.Result.Revision != "1" || result.Replayed != (attempt == 1) {
			t.Fatalf("empty-cell or exact-replay semantics changed: %+v", result)
		}
		if attempt == 0 {
			rowID = result.Result.Row.ID
		} else if result.Result.Row.ID != rowID {
			t.Fatal("exact retry appended a new row")
		}
	}
}
