package enterprisearchive

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A LoadRunner project's .usr name identifies it, so a re-import of the same
// project can update its script.
func TestConvertZipStampsTheProjectName(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"Action.c", "GoldenWeb.usr", "default.cfg"} {
		content, err := os.ReadFile(filepath.Join("testdata", "golden_web_http_html", name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = string(content)
	}
	result, err := ConvertZip(zipFilesForTest(t, files), ConvertOptions{ScriptName: "uploaded-archive"})
	if err != nil {
		t.Fatal(err)
	}
	document, _, declared, ok := dsl.ImportSourceIdentityOf(result.Script.Metadata.Import)
	if !ok || document != "GoldenWeb" || !declared {
		t.Fatalf("identity = %q declared=%v ok=%v", document, declared, ok)
	}
}

// A name VuGen generated (its protocol and a number) is shared by unrelated
// projects, so it is not a declared identity: the import's name stands in.
func TestConvertZipDoesNotDeclareAGeneratedVuGenName(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"Action.c", "GoldenWeb.usr", "default.cfg"} {
		content, err := os.ReadFile(filepath.Join("testdata", "golden_web_http_html", name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "GoldenWeb.usr" {
			name = "WebHttpHtml1.usr"
		}
		files[name] = string(content)
	}
	result, err := ConvertZip(zipFilesForTest(t, files), ConvertOptions{ScriptName: "checkout-archive"})
	if err != nil {
		t.Fatal(err)
	}
	document, _, declared, ok := dsl.ImportSourceIdentityOf(result.Script.Metadata.Import)
	if !ok || document != "checkout-archive" || declared {
		t.Fatalf("identity = %q declared=%v ok=%v, want the import name, undeclared", document, declared, ok)
	}
	for _, name := range []string{"WebHttpHtml1", "TruClient2", "WebServices3", "JavaVuser1"} {
		if !vugenDefaultScriptName.MatchString(name) {
			t.Errorf("%s is not taken as a generated VuGen name", name)
		}
	}
	for _, name := range []string{"COR_Login", "Checkout", "GoldenWeb", "Login2_Checkout", "WebCheckout1", "JavaOrders2"} {
		if vugenDefaultScriptName.MatchString(name) {
			t.Errorf("%s is taken as a generated VuGen name", name)
		}
	}
}
