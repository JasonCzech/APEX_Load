package wsdl

import "testing"

// A WSDL folder picked on a Mac or from a working copy carries .DS_Store and
// .git files; they are left out of the service's files.
func TestNormalizeSourceFilesLeavesOutOperatingSystemAndVersionControlFiles(t *testing.T) {
	files, err := normalizeSourceFiles(map[string]string{
		"service/orders.wsdl":   "<definitions/>",
		"service/types.xsd":     "<schema/>",
		"service/.DS_Store":     "Bud1",
		".git/config":           "[core]",
		"service/._orders.wsdl": "appledouble",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files["service/orders.wsdl"] == "" || files["service/types.xsd"] == "" {
		t.Fatalf("files = %v, want only the WSDL and the schema", files)
	}
}
