package enterprisearchive

import (
	"strings"
	"testing"
)

// Auxiliary callback files live beside the .usr. JavaScript elsewhere in the
// archive (recorded assets, data folders) must not gain a C twin, and a
// helper that cannot be translated must not fail the import.
func TestTranslateJavaScriptWebArchiveAuxiliaryCallbackScope(t *testing.T) {
	files := map[string]string{
		"S.usr":             "[General]\nType=Multi\nScriptLanguage=JavaScript\n[Actions]\nAction=Action.js\n",
		"Action.js":         "function Action()\n{\n\tweb.url({ name : 'home', url : 'http://shop.test/home' });\n\treturn 0;\n}\n",
		"AsyncCallbacks.js": "function Poll_0_RequestCB()\n{\n\treturn WEB_ASYNC_CB_RC_OK;\n}\n",
		"Broken.js":         "function Broken() { web.url({ name: 'x', }",
		"data/vendor.js":    "function vendor() { return 1; }\n",
	}
	out, _, reason := translateJavaScriptWebArchive(files, "S.usr", []string{"Action.js"})
	if reason != "" {
		t.Fatalf("translation refused: %s", reason)
	}
	if _, ok := out["Action.c"]; !ok {
		t.Fatal("action file has no C twin")
	}
	callbacks, ok := out["AsyncCallbacks.c"]
	if !ok {
		t.Fatal("root callback file has no C twin")
	}
	if !strings.Contains(callbacks, "Poll_0_RequestCB(") {
		t.Fatalf("callback twin lost its function: %q", callbacks)
	}
	if _, ok := out["data/vendor.c"]; ok {
		t.Fatal("JavaScript outside the script folder must not be translated")
	}
	if _, ok := out["Broken.c"]; ok {
		t.Fatal("an untranslatable helper must not produce a C twin")
	}
	if out["data/vendor.js"] != files["data/vendor.js"] {
		t.Fatal("original archive files must be carried through unchanged")
	}
}

func TestTranslateJavaScriptWebArchiveAbortCallbackRetainsPromotionBlocker(t *testing.T) {
	files := map[string]string{
		"S.usr":             "[General]\nType=Multi\nScriptLanguage=JavaScript\n[Actions]\nAction=Action.js\n",
		"Action.js":         "function Action()\n{\n\tweb.url({ name : 'home', url : 'http://shop.test/home' });\n\treturn 0;\n}\n",
		"AsyncCallbacks.js": "function Poll_0_RequestCB()\n{\n\treturn WEB_ASYNC_CB_RC_ABORT_ASYNC_NOT_REQUEST;\n}\nfunction Poll_1_RequestCB()\n{\n\treturn web.WEB_ASYNC_CB_RC_ABORT_ASYNC_NOT_ERROR;\n}\n",
	}
	out, _, reason := translateJavaScriptWebArchive(files, "S.usr", []string{"Action.js"})
	if reason != "" {
		t.Fatalf("translation refused: %s", reason)
	}
	cb0 := translateSourceCallback(out, "RequestCB", "Poll_0_RequestCB", "request", nil)
	if !strings.Contains(cb0.reason, "it returns WEB_ASYNC_CB_RC_ABORT_ASYNC_NOT_REQUEST") {
		t.Fatalf("Poll_0_RequestCB reason = %q, want abort return blocker", cb0.reason)
	}
	cb1 := translateSourceCallback(out, "RequestCB", "Poll_1_RequestCB", "request", nil)
	if !strings.Contains(cb1.reason, "it returns WEB_ASYNC_CB_RC_ABORT_ASYNC_NOT_ERROR") {
		t.Fatalf("Poll_1_RequestCB reason = %q, want abort return blocker", cb1.reason)
	}
}
