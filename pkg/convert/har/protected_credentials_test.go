package har

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestImportedCredentialRemainsRunnableAndHARReviewIsExact(t *testing.T) {
	const value = "har-protected-canary-7096f94f"
	result, err := Convert([]byte(`{
	  "log":{"entries":[{
	    "request":{
	      "method":"POST",
	      "url":"https://api.example.test/login",
	      "headers":[{"name":"Content-Type","value":"application/json"}],
	      "postData":{"mimeType":"application/json","text":"{\"password\":\"`+value+`\"}"}
	    },
	    "response":{"status":204,"headers":[],"content":{"text":""}}
	  }]}
	}`), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if result.Script == nil || len(result.Script.Actions) != 1 ||
		!strings.Contains(result.Script.Actions[0].Body, value) {
		t.Fatalf("destination script did not retain imported credential: %#v", result.Script)
	}
	reviewJSON, err := json.Marshal(result.SourceReview)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reviewJSON), value) {
		t.Fatal("HAR source review lost an imported credential value")
	}
	requirements := result.ProtectedRequirements.Take()
	if len(requirements) != 0 {
		t.Fatalf("runnable imported credential unexpectedly requires re-entry: %#v", requirements)
	}
	if result.ProtectedRequirements.Take() != nil {
		t.Fatal("protected credential requirements were returned more than once")
	}
}
