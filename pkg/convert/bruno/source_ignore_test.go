package bruno

import (
	"fmt"
	"reflect"
	"testing"
)

// Bruno never loads what its own ignore list names. A .gitignore only keeps
// files out of version control: Bruno still loads a gitignored .env or
// request, so the import keeps those and skips only other gitignored files.
func TestInspectNativeSkipsJunkAndIgnoredFilesAndCountsHooks(t *testing.T) {
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"Collection 6/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Orders}
extensions:
  bruno:
    ignore: [node_modules, .git, archive]
`),
			"Collection 6/.gitignore":            []byte("drafts/\n*.log\n.env\n"),
			"Collection 6/.env":                  []byte("ORDER_REGION=west\n"),
			"Collection 6/.DS_Store":             []byte{0, 0, 0, 1, 'B', 'u', 'd', '1'},
			"Collection 6/api/.DS_Store":         []byte{0, 0, 0, 1},
			"Collection 6/node_modules/lib.js":   []byte("module.exports = {}\n"),
			"Collection 6/archive/old.yml":       []byte("info: {name: Old, type: http}\nhttp: {method: GET, url: https://example.test/old}\n"),
			"Collection 6/drafts/unfinished.yml": []byte("info: {name: Draft, type: http}\nhttp: {method: GET, url: https://example.test/draft}\n"),
			"Collection 6/server.log":            []byte("noise\n"),
			"Collection 6/api/orders.yml": []byte(`info: {name: Orders, type: http}
http:
  method: GET
  url: https://example.test/orders
  headers:
    - {name: Cookie, value: "pref=compact"}
    - {name: idpctx-session-id, value: recorded-session}
runtime:
  scripts:
    - type: after-response
      code: |
        test("status", function () {
          expect(res.getStatus()).to.equal(200);
        });
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSkipped := []SkippedFile{
		{Path: "Collection 6/.DS_Store", Reason: "operating_system_file"},
		{Path: "Collection 6/.gitignore", Reason: "version_control_file"},
		{Path: "Collection 6/api/.DS_Store", Reason: "operating_system_file"},
		{Path: "Collection 6/archive/old.yml", Reason: "ignore_list"},
		{Path: "Collection 6/node_modules/lib.js", Reason: "ignore_list"},
		{Path: "Collection 6/server.log", Reason: "ignore_list"},
	}
	if !reflect.DeepEqual(result.SkippedFiles, wantSkipped) || result.SkippedFileCount != len(wantSkipped) {
		t.Fatalf("skipped files = %#v (count %d)", result.SkippedFiles, result.SkippedFileCount)
	}
	// opencollection.yml, .env, api/orders.yml and drafts/unfinished.yml.
	if len(result.Scripts) != 2 || len(result.Manifest.Files) != 4 {
		t.Fatalf("scripts = %d, manifest = %#v", len(result.Scripts), result.Manifest.Files)
	}
	if len(result.Environments) != 1 {
		t.Fatalf("environments = %#v, want the gitignored .env", result.Environments)
	}
	capabilities := result.Capabilities
	if capabilities.HasFileBodies || capabilities.HasJavaScript || capabilities.HasLocalModules ||
		capabilities.ConvertedHooks != 1 || capabilities.UnconvertedHooks != 0 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	if !reflect.DeepEqual(result.KeptHeaders, []KeptHeader{{Name: "Cookie", Requests: 1}, {Name: "idpctx-session-id", Requests: 1}}) {
		t.Fatalf("kept headers = %#v", result.KeptHeaders)
	}
	if len(result.SecretRequirements) != 0 {
		t.Fatalf("recorded headers reached credential review: %#v", result.SecretRequirements)
	}
}

func TestInspectNativeReportsUnconvertedHooksAsJavaScript(t *testing.T) {
	result, err := Inspect(Input{
		Filename: "request.yml",
		Content: []byte(`info: {name: Signed, type: http}
http: {method: GET, url: https://example.test/signed}
runtime:
  scripts:
    - type: before-request
      code: |
        req.setHeader("X-Signature", "computed");
`),
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Capabilities.HasJavaScript || result.Capabilities.UnconvertedHooks != 1 || result.Capabilities.ConvertedHooks != 0 {
		t.Fatalf("capabilities = %#v", result.Capabilities)
	}
}

func TestInspectNativeBoundsTheSkippedFileList(t *testing.T) {
	files := map[string][]byte{
		"Orders/opencollection.yml": []byte("opencollection: 1.0.0\ninfo: {name: Orders}\nextensions: {bruno: {ignore: [node_modules]}}\n"),
		"Orders/orders.yml":         []byte("info: {name: Orders, type: http}\nhttp: {method: GET, url: https://example.test/orders}\n"),
	}
	for index := range maxReportedSkippedFiles + 20 {
		files[fmt.Sprintf("Orders/node_modules/pkg/file%03d.js", index)] = []byte("module.exports = {}\n")
	}
	result, err := Inspect(Input{Files: files, Options: InspectOptions{OutputMode: OutputModeNativeRequests}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.SkippedFiles) != maxReportedSkippedFiles || result.SkippedFileCount != maxReportedSkippedFiles+20 {
		t.Fatalf("skipped files = %d listed of %d", len(result.SkippedFiles), result.SkippedFileCount)
	}
	if result.SkippedFiles[0].Path != "Orders/node_modules/pkg/file000.js" {
		t.Fatalf("first skipped file = %q, want path order", result.SkippedFiles[0].Path)
	}
}

func TestJavaScriptWithoutNativeScriptFindsUncoveredRequests(t *testing.T) {
	items := []CollectionItem{
		{UID: "folder", Type: "folder", HasScripts: true, Children: []CollectionItem{
			{UID: "orders", Path: "api/orders.yml", Type: "http", HasTests: true},
			{UID: "stream", Path: "api/stream.yml", Type: "ws", HasScripts: true},
		}},
		{UID: "app", Path: "apps/board.yml", Type: "app", HasScripts: true},
	}
	covered := []NativeScriptCandidate{{SourceUID: "orders", SourcePath: "api/orders.yml"}}
	if !javaScriptWithoutNativeScript(items, covered) {
		t.Fatal("a request with JavaScript and no native script was not reported")
	}
	covered = append(covered, NativeScriptCandidate{SourcePath: "api/stream.yml"})
	if javaScriptWithoutNativeScript(items, covered) {
		t.Fatal("folders and preserved apps were reported as untranslated JavaScript")
	}
}
