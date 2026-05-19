package devweb

import "testing"

func TestGetSupportMatrixCoversExecutableAndReviewOnlyConstructs(t *testing.T) {
	matrix := GetSupportMatrix()
	if len(matrix) == 0 {
		t.Fatal("support matrix is empty")
	}
	byID := map[string]SupportMatrixEntry{}
	for _, entry := range matrix {
		byID[entry.ID] = entry
	}
	for _, id := range []string{"devweb-http-webrequest", "devweb-websocket", "devweb-grpc-unary", "devweb-dynamic-javascript", "devweb-correlation"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("support matrix missing %s", id)
		}
	}
	if byID["devweb-http-webrequest"].ExecutionStatus != "executable" {
		t.Fatalf("HTTP execution status = %q", byID["devweb-http-webrequest"].ExecutionStatus)
	}
	if !byID["devweb-dynamic-javascript"].ReviewRequired {
		t.Fatal("dynamic JavaScript entry should require review")
	}
	if len(GetSupportMatrixNotes()) == 0 || len(GetSupportMatrixExamples()) == 0 {
		t.Fatal("expected notes and tested examples")
	}
}
