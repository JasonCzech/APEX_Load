package importcontract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	k6import "github.com/jasonczech/apexload/pkg/convert/k6"
	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/convert/wsdl"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// converted is the deliberately small common surface exercised by the
// cross-importer contracts. It is test-only: production converters can move
// toward a shared IR without making this package part of their dependency
// graph.
type converted struct {
	importer     string
	script       *dsl.Script
	publicResult any
	accounting   conversionAccounting
	sourceReview *sourceview.Review
}

type importerCase struct {
	name           string
	protocol       string
	target         string
	fixture        string
	convertFixture func(t *testing.T, content []byte) converted
}

type conversionAccounting struct {
	Detected   int `json:"detected"`
	Converted  int `json:"converted"`
	Skipped    int `json:"skipped"`
	ReviewOnly int `json:"review_only"`
}

type stableConversion struct {
	Importer         string               `json:"importer"`
	PublicResult     json.RawMessage      `json:"public_result"`
	Accounting       conversionAccounting `json:"accounting"`
	ValidationIssues []string             `json:"validation_issues"`
}

func baselineImporters() []importerCase {
	return []importerCase{
		{
			name:     "postman",
			protocol: "http",
			target:   "/orders",
			fixture:  "postman.json",
			convertFixture: func(t *testing.T, content []byte) converted {
				t.Helper()
				result, err := postman.Convert(postman.ImportRequest{
					Name:    "Import Contract",
					Content: string(content),
				})
				if err != nil {
					t.Fatalf("postman.Convert: %v", err)
				}
				reviewOnly := reviewOnlyActionMappingCount(result.SourceReview)
				return converted{
					importer:     "postman",
					script:       result.Script,
					publicResult: result,
					accounting: conversionAccounting{
						Detected:   result.Stats.TotalRequests,
						Converted:  result.Stats.ConvertedActions - reviewOnly,
						Skipped:    len(result.Stats.SkippedItems),
						ReviewOnly: reviewOnly,
					},
					sourceReview: result.SourceReview,
				}
			},
		},
		{
			name:     "raw_http",
			protocol: "http",
			target:   "/orders",
			fixture:  "raw-http.txt",
			convertFixture: func(t *testing.T, content []byte) converted {
				t.Helper()
				result, err := sample.Convert(sample.ImportRequest{
					Name:    "Import Contract",
					Format:  "raw_http",
					Content: string(content),
				})
				if err != nil {
					t.Fatalf("sample.Convert(raw_http): %v", err)
				}
				reviewOnly := reviewOnlyActionMappingCount(result.SourceReview)
				return converted{
					importer:     "raw_http",
					script:       result.Script,
					publicResult: result,
					accounting: conversionAccounting{
						Detected:   result.Stats.TotalRequests,
						Converted:  result.Stats.ConvertedActions - reviewOnly,
						Skipped:    result.Stats.FilteredAssets + result.Stats.SkippedRequests,
						ReviewOnly: reviewOnly,
					},
					sourceReview: result.SourceReview,
				}
			},
		},
		{
			name:     "har",
			protocol: "http",
			target:   "/orders",
			fixture:  "har.json",
			convertFixture: func(t *testing.T, content []byte) converted {
				t.Helper()
				result, err := har.Convert(content, har.ConvertOptions{ScriptName: "Import Contract"})
				if err != nil {
					t.Fatalf("har.Convert: %v", err)
				}
				reviewOnly := reviewOnlyActionMappingCount(result.SourceReview)
				return converted{
					importer:     "har",
					script:       result.Script,
					publicResult: result,
					accounting: conversionAccounting{
						Detected:   result.Stats.TotalRequests,
						Converted:  result.Stats.ConvertedActions - reviewOnly,
						Skipped:    result.Stats.FilteredAssets + result.Stats.SkippedRequests,
						ReviewOnly: reviewOnly,
					},
					sourceReview: result.SourceReview,
				}
			},
		},
		{
			name:     "jmeter",
			protocol: "http",
			target:   "/orders",
			fixture:  "jmeter.jmx",
			convertFixture: func(t *testing.T, content []byte) converted {
				t.Helper()
				result, err := jmeter.ConvertJMX(content, jmeter.ConvertOptions{
					ScriptName: "Import Contract",
					FileName:   "contract.jmx",
					BaseURL:    "https://api.contract.test",
				})
				if err != nil {
					t.Fatalf("jmeter.ConvertJMX: %v", err)
				}
				script := result.Script
				if script == nil && len(result.Scripts) > 0 {
					script = result.Scripts[0]
				}
				reviewOnly := reviewOnlyActionMappingCount(result.SourceReview)
				return converted{
					importer:     "jmeter",
					script:       script,
					publicResult: result,
					accounting: conversionAccounting{
						Detected:   result.Stats.TotalSamplers,
						Converted:  result.Stats.ConvertedActions - reviewOnly,
						Skipped:    result.Stats.SkippedSamplers,
						ReviewOnly: reviewOnly,
					},
					sourceReview: result.SourceReview,
				}
			},
		},
		{
			name:     "openapi",
			protocol: "http",
			target:   "/orders",
			fixture:  "openapi.yaml",
			convertFixture: func(t *testing.T, content []byte) converted {
				t.Helper()
				result, err := openapi.Convert(openapi.ImportRequest{
					Name:    "Import Contract",
					Content: string(content),
				})
				if err != nil {
					t.Fatalf("openapi.Convert: %v", err)
				}
				reviewOnly := reviewOnlyActionMappingCount(result.SourceReview)
				return converted{
					importer:     "openapi",
					script:       result.Script,
					publicResult: result,
					accounting: conversionAccounting{
						Detected:   result.Stats.TotalOperations,
						Converted:  result.Stats.ConvertedActions - reviewOnly,
						Skipped:    len(result.Stats.SkippedOperations),
						ReviewOnly: reviewOnly,
					},
					sourceReview: result.SourceReview,
				}
			},
		},
		{
			name:     "wsdl",
			protocol: "soap",
			target:   "https://api.contract.test/soap",
			fixture:  "wsdl.xml",
			convertFixture: func(t *testing.T, content []byte) converted {
				t.Helper()
				result, err := wsdl.Convert(wsdl.ImportRequest{
					Name:    "Import Contract",
					Content: string(content),
				})
				if err != nil {
					t.Fatalf("wsdl.Convert: %v", err)
				}
				reviewOnly := reviewOnlyActionMappingCount(result.SourceReview)
				return converted{
					importer:     "wsdl",
					script:       result.Script,
					publicResult: result,
					accounting: conversionAccounting{
						Detected:   result.Stats.OperationsDetected,
						Converted:  result.Stats.ConvertedActions - reviewOnly,
						Skipped:    result.Stats.OperationsDetected - result.Stats.ConvertedActions,
						ReviewOnly: reviewOnly,
					},
					sourceReview: result.SourceReview,
				}
			},
		},
		{
			name:     "k6",
			protocol: "http",
			target:   "/orders",
			fixture:  "k6.js",
			convertFixture: func(t *testing.T, content []byte) converted {
				t.Helper()
				result, err := k6import.Convert(k6import.ImportRequest{
					Name:    "Import Contract",
					Content: string(content),
				})
				if err != nil {
					t.Fatalf("k6.Convert: %v", err)
				}
				reviewOnly := reviewOnlyActionMappingCount(result.SourceReview)
				return converted{
					importer:     "k6",
					script:       result.Script,
					publicResult: result,
					accounting: conversionAccounting{
						Detected:   result.Stats.HTTPRequests,
						Converted:  result.Stats.ConvertedActions - reviewOnly,
						Skipped:    result.Stats.HTTPRequests - result.Stats.ConvertedActions,
						ReviewOnly: reviewOnly,
					},
					sourceReview: result.SourceReview,
				}
			},
		},
	}
}

func loadBaselineFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "baseline", name))
	if err != nil {
		t.Fatalf("read baseline fixture %q: %v", name, err)
	}
	return content
}

func normalizeConversion(t *testing.T, result converted) stableConversion {
	t.Helper()
	if result.script == nil {
		t.Fatalf("%s converter returned a nil script", result.importer)
	}
	if result.publicResult == nil {
		t.Fatalf("%s converter returned a nil public result", result.importer)
	}

	publicJSON, err := json.Marshal(result.publicResult)
	if err != nil {
		t.Fatalf("marshal %s public result: %v", result.importer, err)
	}
	var publicValue any
	decoder := json.NewDecoder(bytes.NewReader(publicJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&publicValue); err != nil {
		t.Fatalf("decode %s public result for normalization: %v", result.importer, err)
	}
	normalizeGeneratedPublicFields(publicValue)
	publicJSON, err = json.Marshal(publicValue)
	if err != nil {
		t.Fatalf("marshal normalized %s public result: %v", result.importer, err)
	}

	issues := append([]string(nil), dsl.ValidateScriptIssues(result.script)...)
	sort.Strings(issues)

	return stableConversion{
		Importer:         result.importer,
		PublicResult:     publicJSON,
		Accounting:       result.accounting,
		ValidationIssues: issues,
	}
}

// normalizeGeneratedPublicFields retains the complete public result surface,
// including import metadata and stable finding/mapping/requirement IDs. Only
// converter clock values and persistence-assigned script identity are replaced.
func normalizeGeneratedPublicFields(value any) {
	switch typed := value.(type) {
	case []any:
		for _, child := range typed {
			normalizeGeneratedPublicFields(child)
		}
	case map[string]any:
		if isScriptJSON(typed) {
			if _, ok := typed["id"]; ok {
				typed["id"] = "<generated-script-id>"
			}
			if _, ok := typed["ref_key"]; ok {
				typed["ref_key"] = "<generated-script-ref-key>"
			}
			if _, ok := typed["created_at"]; ok {
				typed["created_at"] = "<generated-timestamp>"
			}
			if _, ok := typed["updated_at"]; ok {
				typed["updated_at"] = "<generated-timestamp>"
			}
			normalizeScriptImportTimestamp(typed)
		}
		for _, child := range typed {
			normalizeGeneratedPublicFields(child)
		}
	}
}

func isScriptJSON(value map[string]any) bool {
	_, hasProtocol := value["protocol"]
	_, hasConfig := value["config"]
	_, hasActions := value["actions"]
	_, hasVersion := value["version"]
	return hasProtocol && hasConfig && hasActions && hasVersion
}

func normalizeScriptImportTimestamp(script map[string]any) {
	metadata, _ := script["metadata"].(map[string]any)
	importMetadata, _ := metadata["import"].(map[string]any)
	preserved, _ := importMetadata["preserved"].(map[string]any)
	if _, ok := preserved["imported_at"]; ok {
		preserved["imported_at"] = "<generated-timestamp>"
	}
}

func reviewOnlyActionMappingCount(review *sourceview.Review) int {
	if review == nil {
		return 0
	}
	count := 0
	for _, mapping := range review.Mappings {
		if strings.EqualFold(mapping.TargetKind, "action") &&
			strings.TrimSpace(mapping.TargetPath) != "" &&
			strings.EqualFold(mapping.Status, "review_only") {
			count++
		}
	}
	return count
}
