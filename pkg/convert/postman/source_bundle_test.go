package postman

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

func TestBuildProtectedSourceBundleRetainsCollectionAndEnvironmentExactly(t *testing.T) {
	collection := []byte("{\n  \"info\": {\"name\": \"Orders\"}\n}\n")
	environment := []byte("{\r\n  \"name\": \"Local\", \"values\": []\r\n}\r\n")
	encoded := BuildProtectedSourceBundle(
		"folder/orders.postman_collection.json",
		collection,
		"../local.postman_environment.json",
		environment,
	)
	bundle, err := ParseProtectedSourceBundle(encoded)
	if err != nil {
		t.Fatalf("ParseProtectedSourceBundle returned error: %v", err)
	}
	if bundle.Version != ProtectedSourceBundleVersion || len(bundle.Files) != 2 {
		t.Fatalf("bundle = %#v", bundle)
	}
	if bundle.Files[0].Role != "collection" || bundle.Files[0].Path != "orders.postman_collection.json" ||
		!bytes.Equal(bundle.Files[0].Content, collection) {
		t.Fatalf("collection entry = %#v", bundle.Files[0])
	}
	if bundle.Files[1].Role != "environment" || bundle.Files[1].Path != "local.postman_environment.json" ||
		!bytes.Equal(bundle.Files[1].Content, environment) {
		t.Fatalf("environment entry = %#v", bundle.Files[1])
	}
}

func TestBuildProtectedSourceBundleAllocatesDistinctBoundedPaths(t *testing.T) {
	encoded := BuildProtectedSourceBundle(
		"SAME.json",
		[]byte(`{"info":{"name":"Orders"}}`),
		"same.JSON",
		[]byte(`{"name":"Local","values":[]}`),
	)
	bundle, err := ParseProtectedSourceBundle(encoded)
	if err != nil {
		t.Fatalf("ParseProtectedSourceBundle returned error: %v", err)
	}
	if strings.EqualFold(bundle.Files[0].Path, bundle.Files[1].Path) {
		t.Fatalf("bundle paths remain ambiguous: %#v", bundle.Files)
	}
	if bundle.Files[0].Path != "SAME.json" || bundle.Files[1].Path != "same_2.JSON" {
		t.Fatalf("bundle paths = %q / %q, want deterministic collision suffix", bundle.Files[0].Path, bundle.Files[1].Path)
	}

	longName := strings.Repeat("x", maxProtectedSourcePathBytes+1) + ".json"
	encoded = BuildProtectedSourceBundle(longName, []byte(`{}`), "environment.json", []byte(`{}`))
	bundle, err = ParseProtectedSourceBundle(encoded)
	if err != nil {
		t.Fatalf("oversized source filename produced an invalid bundle: %v", err)
	}
	if bundle.Files[0].Path != "postman_collection.json" {
		t.Fatalf("oversized path = %q, want bounded fallback", bundle.Files[0].Path)
	}
}

func TestParseProtectedSourceBundleRejectsInvalidOrUnboundedShape(t *testing.T) {
	valid := ProtectedSourceBundle{
		Version: ProtectedSourceBundleVersion,
		Files: []ProtectedSourceBundleFile{
			{Role: "collection", Path: "collection.json", MediaType: "application/json", Content: []byte("{}")},
			{Role: "environment", Path: "environment.json", MediaType: "application/json", Content: []byte("{}")},
		},
	}
	tests := []struct {
		name   string
		mutate func(*ProtectedSourceBundle)
	}{
		{name: "unknown version", mutate: func(bundle *ProtectedSourceBundle) { bundle.Version = "v2" }},
		{name: "missing environment", mutate: func(bundle *ProtectedSourceBundle) { bundle.Files = bundle.Files[:1] }},
		{name: "duplicate role", mutate: func(bundle *ProtectedSourceBundle) { bundle.Files[1].Role = "collection" }},
		{name: "duplicate folded path", mutate: func(bundle *ProtectedSourceBundle) { bundle.Files[1].Path = "COLLECTION.JSON" }},
		{name: "traversal path", mutate: func(bundle *ProtectedSourceBundle) { bundle.Files[1].Path = "../environment.json" }},
		{name: "empty content", mutate: func(bundle *ProtectedSourceBundle) { bundle.Files[1].Content = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Files = append([]ProtectedSourceBundleFile(nil), valid.Files...)
			test.mutate(&candidate)
			payload, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseProtectedSourceBundle(payload); err == nil {
				t.Fatalf("ParseProtectedSourceBundle accepted %#v", candidate)
			}
		})
	}

	t.Run("oversized envelope", func(t *testing.T) {
		if _, err := ParseProtectedSourceBundle(make([]byte, maxProtectedSourceBundleBytes+1)); err == nil {
			t.Fatal("ParseProtectedSourceBundle accepted an oversized envelope")
		}
	})
}

func TestConvertPostmanReviewRetainsEnvironmentProvenanceWithoutDisclosingCredential(t *testing.T) {
	const environmentCredential = "environment-provenance-secret"
	sanitize := true
	result, err := Convert(ImportRequest{
		CollectionFileName: "orders.postman_collection.json",
		Content: `{
  "info":{"name":"Orders"},
  "variable":[{"key":"token","value":"collection-shadow"}],
  "item":[{"name":"List","request":{"method":"GET","url":"https://api.example.test/orders","header":[{"key":"Authorization","value":"Bearer {{token}}"}]}}]
}`,
		EnvironmentFileName: "local.postman_environment.json",
		EnvironmentContent:  `{"name":"Local","values":[{"key":"token","value":"` + environmentCredential + `","enabled":true}]}`,
		Options:             ConvertOptions{SanitizeSensitive: &sanitize},
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.Summary.EnvironmentName != "Local" || len(result.Summary.SourceFiles) != 2 {
		t.Fatalf("summary provenance = %#v", result.Summary)
	}
	if result.SourceReview == nil || result.SourceReview.TotalSourceFiles != 2 {
		t.Fatalf("source review = %#v", result.SourceReview)
	}
	for _, file := range result.SourceReview.SourceFiles {
		if strings.Contains(file.Content, environmentCredential) {
			t.Fatalf("source review file %q disclosed environment credential", file.Path)
		}
	}
	requirements := result.ProtectedRequirements.Take()
	if len(requirements) != 1 {
		t.Fatalf("requirements = %#v", requirements)
	}
	if requirements[0].Source.Path != "local.postman_environment.json" {
		t.Fatalf("credential source = %#v, want selected environment file", requirements[0].Source)
	}
}

func TestConvertPostmanEventReviewFilesCannotOverwriteUploadedSources(t *testing.T) {
	result, err := Convert(ImportRequest{
		CollectionFileName: "Request_test_0.js",
		Content: `{
  "info":{"name":"Review paths"},
  "item":[{"name":"Request","event":[{"listen":"test","script":{"exec":["pm.test('ok', () => true);"]}}],"request":{"method":"GET","url":"https://api.example.test/"}}]
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 2 {
		t.Fatalf("source review = %#v, want collection and event files", result.SourceReview)
	}
	paths := make(map[string]struct{}, len(result.SourceReview.SourceFiles))
	for _, file := range result.SourceReview.SourceFiles {
		identity := strings.ToLower(file.Path)
		if _, duplicate := paths[identity]; duplicate {
			t.Fatalf("source review contains duplicate path %q", file.Path)
		}
		paths[identity] = struct{}{}
	}
	if _, ok := paths["request_test_0.js"]; !ok {
		t.Fatalf("uploaded collection source was overwritten: %#v", result.SourceReview.SourceFiles)
	}
	if _, ok := paths["request_test_0_2.js"]; !ok {
		t.Fatalf("event review file was not deterministically renamed: %#v", result.SourceReview.SourceFiles)
	}
}

func TestConvertPostmanRejectsAmbiguousEnvironmentJSON(t *testing.T) {
	_, err := Convert(ImportRequest{
		Content: `{
  "info":{"name":"Ambiguous environment"},
  "item":[{"name":"Request","request":{"method":"GET","url":"https://api.example.test/"}}]
}`,
		EnvironmentContent: `{"name":"Local","values":[{"key":"token","value":"shown","value":"executed"}]}`,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid Postman environment") {
		t.Fatalf("Convert error = %v, want duplicate-key environment rejection", err)
	}
}

func TestPostmanSourceVariableExpressionRefDoesNotMisattributeComposedValues(t *testing.T) {
	fallback := requestimport.SourceRef{
		Format:  "postman",
		Path:    "collection.json",
		Pointer: "/item/0/request/header/0",
	}
	variables := map[string]credentialpolicy.SourceVariable{
		"user": {
			Value: "alice",
			Source: credentialpolicy.SourceRef{
				Format:  "postman",
				Path:    "environment.json",
				Pointer: "/variables/user",
			},
		},
		"password": {
			Value: "secret",
			Source: credentialpolicy.SourceRef{
				Format:  "postman",
				Path:    "environment.json",
				Pointer: "/variables/password",
			},
		},
	}

	got := postmanSourceVariableExpressionRef(
		"Basic {{user}}:{{password}}",
		variables,
		fallback,
	)
	if got != fallback {
		t.Fatalf("composed credential source = %#v, want authored request fallback %#v", got, fallback)
	}

	repeated := postmanSourceVariableExpressionRef(
		"Bearer {{user}}/{{user}}",
		variables,
		fallback,
	)
	if repeated.Path != "environment.json" || repeated.Pointer != "/variables/user" {
		t.Fatalf("repeated single-variable source = %#v", repeated)
	}
	caseVariant := postmanSourceVariableExpressionRef(
		"Bearer {{USER}}",
		variables,
		fallback,
	)
	if caseVariant.Path != "environment.json" || caseVariant.Pointer != "/variables/user" {
		t.Fatalf("case-variant destination binding source = %#v, want exact source-variable provenance", caseVariant)
	}
}

func TestConvertPostmanCaseVariantCredentialDependencyMasksExactSourceValue(t *testing.T) {
	const canary = "postman-case-variant-source-secret-7f29"
	result, err := Convert(ImportRequest{Content: `{
  "info":{"name":"Case variant credential"},
  "variable":[
    {"key":"token","value":"` + canary + `"},
    {"key":"wrapper","value":"Bearer {{token}}"}
  ],
  "item":[{"name":"Request","request":{
    "method":"GET",
    "url":"https://api.example.test/items",
    "header":[{"key":"Authorization","value":"{{WRAPPER}}"}]
  }}]
}`})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Actions[0].Headers["Authorization"]; got != "Bearer "+canary {
		t.Fatalf("executable header = %q, want preserved effective source value", got)
	}
	review, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(review), canary) {
		t.Fatalf("case-variant dependency disclosed source credential: %s", review)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 ||
		!result.SourceReview.SourceFiles[0].Redacted {
		t.Fatalf("source review was not marked redacted: %#v", result.SourceReview)
	}
}

func TestConvertPostmanMasksCredentialVariableValueUsedBeforeLaterScopeMutation(t *testing.T) {
	const canary = "early-plain-value"
	result, err := Convert(ImportRequest{
		EnvironmentFileName: "history.postman_environment.json",
		EnvironmentContent: `{"name":"History","values":[` +
			`{"key":"credentialValue","value":"` + canary + `","enabled":true},` +
			`{"key":"payload","value":"Bearer {{credentialValue}}","enabled":true}]}`,
		Content: `{
  "info":{"name":"Scoped credential history"},
  "item":[
    {"name":"Authenticated","request":{
      "method":"GET",
      "url":"https://api.example.test/first",
      "header":[{"key":"Authorization","value":"{{payload}}"}]
    }},
    {"name":"Mutated later","event":[{"listen":"prerequest","script":{"exec":[
      "pm.environment.set(\"credentialValue\", \"ordinary-later-value\");"
    ]}}],"request":{"method":"GET","url":"https://api.example.test/second?after={{payload}}"}}
  ]
}`,
	})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Script.Actions[0].Headers["Authorization"]; got != "Bearer "+canary {
		t.Fatalf("first executable header = %q, want preserved source-time credential", got)
	}
	review, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(review), canary) {
		t.Fatalf("credential value consumed before a later scope mutation remained public: %s", review)
	}
}

func TestMaskPostmanLiteralSourceValuesFailsClosedAtWorkLimit(t *testing.T) {
	const canary = "postman-redaction-work-canary"
	sources := map[string]string{
		"collection.json": "first\n" + canary + "\nlast",
	}
	if !maskPostmanLiteralSourceValuesWithBudget(
		sources,
		[]string{canary, "another-value"},
		1,
	) {
		t.Fatal("bounded source omission was not reported as a redaction")
	}
	projected := sources["collection.json"]
	if strings.Contains(projected, canary) ||
		!strings.Contains(projected, "WORK LIMIT EXCEEDED") {
		t.Fatalf("bounded source projection = %q", projected)
	}
	if strings.Count(projected, "\n") != 2 {
		t.Fatalf("bounded source projection changed line coordinates: %q", projected)
	}
}
