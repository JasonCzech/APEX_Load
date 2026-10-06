package bruno

import (
	"fmt"
	"strings"
	"testing"
)

// nestedNativeCollection builds a native collection whose requests all sit at
// the same nesting depth, so folder metadata is shared by every request.
func nestedNativeCollection(requestNames ...string) map[string][]byte {
	files := map[string][]byte{
		"coll/opencollection.yml": []byte("opencollection: 1.0.0\ninfo: {name: Nested, type: collection}\n"),
		"coll/a/folder.yml":       []byte("info: {name: A, type: folder}\n"),
		"coll/a/b/folder.yml":     []byte("info: {name: B, type: folder}\n"),
		"coll/a/b/c/folder.yml":   []byte("info: {name: C, type: folder}\n"),
	}
	for index, name := range requestNames {
		files["coll/a/b/c/"+name+".yml"] = []byte("info: {name: " + name + ", uid: " + name +
			", type: http, seq: " + string(rune('1'+index)) + "}\n" +
			"http:\n  method: GET\n  url: https://example.test/" + name + "\n")
	}
	return files
}

func TestInspectNativeParsesAncestorMetadataOncePerPass(t *testing.T) {
	requests := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}
	files := nestedNativeCollection(requests...)

	// Two full metadata passes run per inspect: the effective-credential shape
	// validation over the raw upload and the candidate projection over the
	// sanitized bundle. Each pass may parse the collection document, each folder
	// document, and each request document exactly once. Without ancestor
	// memoisation each pass instead parses every ancestor once per request
	// (6 requests x 3 folders = 18 folder parses per pass instead of 3).
	const folders = 3
	const collections = 1
	const passes = 2
	budget := int64(passes * (collections + folders + len(requests)))

	before := nativeLayerParses.Load()
	result, err := Inspect(Input{
		Files:   files,
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	parses := nativeLayerParses.Load() - before

	if len(result.Scripts) != len(requests) {
		t.Fatalf("native candidates = %d, want %d", len(result.Scripts), len(requests))
	}
	if parses > budget {
		t.Fatalf("parsed %d Bruno layer document(s) for %d request(s) at depth %d, want at most %d",
			parses, len(requests), folders, budget)
	}
}

func TestInspectNativeReportsEveryUnimportableRequest(t *testing.T) {
	files := nestedNativeCollection("good")
	for _, name := range []string{"bad1", "bad2", "bad3"} {
		files["coll/a/b/c/"+name+".yml"] = []byte("info: {name: " + name + ", uid: " + name +
			", type: http}\nhttp:\n  method: PROPFIND\n  url: https://example.test/" + name + "\n")
	}

	_, err := Inspect(Input{
		Files:   files,
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil {
		t.Fatal("malformed native requests were accepted")
	}
	for _, want := range []string{"a/b/c/bad1.yml", "a/b/c/bad2.yml", "a/b/c/bad3.yml"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not report %q", err.Error(), want)
		}
	}
	if !strings.Contains(err.Error(), "3 Bruno requests could not be imported") {
		t.Fatalf("error %q does not summarise the failure count", err.Error())
	}
}

// shapeInvalidNativeRequest is rejected by validateNativeYAMLFiles, the
// pre-parse gate that runs before any request layer is built. Malformed request
// shape is the most common native authoring error, so it has to be reported for
// every offending document in one pass rather than one upload at a time.
func shapeInvalidNativeRequest(name string) []byte {
	return []byte("info: {name: " + name + ", uid: " + name + ", type: http}\n" +
		"http:\n  method: GET\n  url: https://example.test/" + name +
		"\n  headers: \"not-a-list\"\n")
}

func TestInspectNativeReportsEveryShapeInvalidRequest(t *testing.T) {
	files := nestedNativeCollection("good")
	for _, name := range []string{"bad1", "bad2", "bad3"} {
		files["coll/a/b/c/"+name+".yml"] = shapeInvalidNativeRequest(name)
	}

	_, err := Inspect(Input{
		Files:   files,
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil {
		t.Fatal("shape-invalid native requests were accepted")
	}
	for _, want := range []string{"a/b/c/bad1.yml", "a/b/c/bad2.yml", "a/b/c/bad3.yml"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not report %q", err.Error(), want)
		}
	}
	if !strings.Contains(err.Error(), "3 Bruno YAML documents could not be imported") {
		t.Fatalf("error %q does not summarise the failure count", err.Error())
	}
}

func TestInspectNativeSingleShapeFailureMessageIsUnaggregated(t *testing.T) {
	files := nestedNativeCollection("good")
	files["coll/a/b/c/bad.yml"] = shapeInvalidNativeRequest("bad")

	_, err := Inspect(Input{
		Files:   files,
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil {
		t.Fatal("shape-invalid native request was accepted")
	}
	want := `Bruno YAML "coll/a/b/c/bad.yml": unsupported Bruno native shape: ` +
		`http.headers must be an array or object`
	if err.Error() != want {
		t.Fatalf("single-failure error = %q, want %q", err.Error(), want)
	}
}

// Legacy Bruno exports mix .json request bodies into an OpenCollection tree.
// The container-format gate rejects them, and must report every one of them —
// up to maxReportedNativeRequestFailures, see
// TestInspectNativeCapsNonYAMLRequestFileReports — so a migration is not a
// file-at-a-time re-upload loop.
func TestInspectNativeReportsEveryNonYAMLRequestFile(t *testing.T) {
	files := nestedNativeCollection("good")
	for _, name := range []string{"legacy1", "legacy2", "legacy3"} {
		files["coll/a/b/c/"+name+".json"] = []byte(`{"info": {"name": "` + name +
			`", "uid": "` + name + `", "type": "http"},` +
			`"http": {"method": "GET", "url": "https://example.test/` + name + `"}}`)
	}

	_, err := Inspect(Input{
		Files:   files,
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil {
		t.Fatal("non-YAML native request files were accepted")
	}
	for _, want := range []string{"a/b/c/legacy1.json", "a/b/c/legacy2.json", "a/b/c/legacy3.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not report %q", err.Error(), want)
		}
	}
	if !strings.Contains(err.Error(), "3 Bruno requests could not be imported") {
		t.Fatalf("error %q does not summarise the failure count", err.Error())
	}
	if strings.Contains(err.Error(), "was not found in the sanitized bundle") {
		t.Fatalf("error %q double-reports a rejected container as a missing source", err.Error())
	}
}

// legacyNativeRequestJSON is a Bruno HTTP request in the legacy JSON container
// format. classifyFileRole types it "request", so it reaches the native
// container-format gate and is rejected there.
func legacyNativeRequestJSON(name string) []byte {
	return []byte(`{"info": {"name": "` + name + `", "uid": "` + name + `", "type": "http"},` +
		`"http": {"method": "GET", "url": "https://example.test/` + name + `"}}`)
}

// A wholly legacy bundle must not be able to grow the aggregated import error
// without bound: the message is returned verbatim to the HTTP client, and the
// default file limit is 100_000. The container-format gate therefore obeys the
// same maxReportedNativeRequestFailures cap as every other accumulator loop.
func TestInspectNativeCapsNonYAMLRequestFileReports(t *testing.T) {
	const offenders = maxReportedNativeRequestFailures * 3
	files := nestedNativeCollection("good")
	names := make([]string, 0, offenders)
	for index := 0; index < offenders; index++ {
		name := fmt.Sprintf("legacy%03d", index)
		names = append(names, name)
		files["coll/a/b/c/"+name+".json"] = legacyNativeRequestJSON(name)
	}

	_, err := Inspect(Input{
		Files:   files,
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil {
		t.Fatal("non-YAML native request files were accepted")
	}
	reported := 0
	for _, name := range names {
		if strings.Contains(err.Error(), "a/b/c/"+name+".json") {
			reported++
		}
	}
	if reported != maxReportedNativeRequestFailures {
		t.Fatalf("error names %d of %d offending containers, want the %d-failure cap: %q",
			reported, offenders, maxReportedNativeRequestFailures, err.Error())
	}
	if want := fmt.Sprintf("%d Bruno requests could not be imported", maxReportedNativeRequestFailures); !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not summarise the capped failure count", err.Error())
	}
	if !strings.Contains(err.Error(), "remaining Bruno requests were not inspected") {
		t.Fatalf("error %q does not say the remaining requests went uninspected", err.Error())
	}
}

// An OpenCollection item typed graphql keeps its http block, so it survives the
// pre-parse shape gate and is only rejected by the item-type guard. That guard
// must not throw away container-format failures already accumulated for the
// same bundle, or the operator learns about them only on the next upload.
func TestInspectNativeReportsRejectedContainersWithUnsupportedItemTypes(t *testing.T) {
	files := nestedNativeCollection("good")
	files["coll/a/b/c/legacy.json"] = legacyNativeRequestJSON("legacy")
	files["coll/a/b/c/gql.yml"] = []byte("info: {name: gql, uid: gql, type: http}\n" +
		"graphql:\n  query: \"{ ok }\"\n" +
		"http:\n  method: POST\n  url: https://example.test/gql\n")

	_, err := Inspect(Input{
		Files:   files,
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil {
		t.Fatal("unsupported native item type was accepted")
	}
	if !strings.Contains(err.Error(), "canonical mode is required for: graphql") {
		t.Fatalf("error %q does not report the unsupported item type", err.Error())
	}
	if !strings.Contains(err.Error(), "a/b/c/legacy.json") {
		t.Fatalf("error %q discards the rejected legacy container", err.Error())
	}
}

func TestInspectNativeSingleFailureMessageIsUnaggregated(t *testing.T) {
	files := nestedNativeCollection("good")
	files["coll/a/b/c/bad.yml"] = []byte("info: {name: bad, uid: bad, type: http}\n" +
		"http:\n  method: PROPFIND\n  url: https://example.test/bad\n")

	_, err := Inspect(Input{
		Files:   files,
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err == nil {
		t.Fatal("malformed native request was accepted")
	}
	if want := `Bruno request "a/b/c/bad.yml" uses unsupported HTTP method "PROPFIND"`; err.Error() != want {
		t.Fatalf("single-failure error = %q, want %q", err.Error(), want)
	}
}
