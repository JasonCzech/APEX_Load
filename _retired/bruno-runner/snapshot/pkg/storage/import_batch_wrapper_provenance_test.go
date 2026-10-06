package storage

import (
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestGenericImportBatchRejectsWrapperExtractorCredentialProvenance(t *testing.T) {
	tests := []struct {
		name    string
		wrapper dsl.Action
	}{
		{
			name: "control if",
			wrapper: dsl.Action{
				Name: "conditional", Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Left: "yes", Op: "eq", Right: "yes"},
					Steps:     []dsl.Action{{Name: "then", Type: "http_request", Method: "GET", Target: "/then"}},
					Else:      []dsl.Action{{Name: "else", Type: "http_request", Method: "GET", Target: "/else"}},
				},
			},
		},
		{
			name: "control loop",
			wrapper: dsl.Action{
				Name: "loop", Type: "control_loop",
				Control: &dsl.ControlFlow{
					Count: 1,
					Steps: []dsl.Action{{Name: "step", Type: "http_request", Method: "GET", Target: "/step"}},
				},
			},
		},
		{
			name: "http sequence",
			wrapper: dsl.Action{
				Name: "sequence", Type: "http_sequence", Method: "SEQUENCE", Target: "/",
				Params: map[string]any{"requests": []dsl.Action{{
					Name: "child", Type: "http_request", Method: "GET", Target: "/child",
				}}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newBrunoImportTestStore(t)
			commit := genericImportBatchFixture(
				"postman_native",
				"postman-native-commit-v1",
				"wrapper-provenance-"+strings.ReplaceAll(tt.name, " ", "-"),
				"wrapper-provenance-sha",
			)
			wrapper := tt.wrapper
			wrapper.Extractors = []dsl.Extractor{{
				Name: "wrapper_token", Source: "body", Type: "jsonpath", Expression: "$.token",
			}}
			commit.Scripts[0].Actions = []dsl.Action{
				wrapper,
				{
					Name: "use token", Type: "http_request", Method: "GET", Target: "https://example.test/profile",
					HeaderList: dsl.HeaderList{{
						Name: "Authorization", Value: "Bearer {{var.wrapper_token}}",
					}},
				},
			}

			_, err := store.CommitImportBatch(commit)
			if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
				t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
			}
			if strings.Contains(err.Error(), "wrapper_token") {
				t.Fatalf("credential boundary disclosed binding identity: %v", err)
			}
			assertGenericImportBatchTablesEmpty(t, store)
		})
	}
}
