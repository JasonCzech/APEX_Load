package dsl

import (
	"strings"
	"testing"
)

func TestImportSourceIdentityRoundTrips(t *testing.T) {
	script := &Script{}
	SetImportSourceIdentity(script, "  Orders API  ", true, "Buyers")
	document, item, declared, ok := ImportSourceIdentityOf(script.Metadata.Import)
	if !ok || document != "Orders API" || item != "Buyers" || !declared {
		t.Fatalf("identity = %q %q %v %v", document, item, declared, ok)
	}
	SetImportSourceIdentity(script, "checkout.har", false, "")
	document, item, declared, ok = ImportSourceIdentityOf(script.Metadata.Import)
	if !ok || document != "checkout.har" || item != "" || declared {
		t.Fatalf("re-stamped identity = %q %q %v %v", document, item, declared, ok)
	}
	SetImportSourceIdentity(&Script{}, " ", true, "x")
	if _, _, _, ok := ImportSourceIdentityOf(nil); ok {
		t.Fatal("nil metadata has an identity")
	}
}

func TestImportSourceIdentityCutsAtARuneBoundary(t *testing.T) {
	script := &Script{}
	long := strings.Repeat("a", MaxImportSourceIdentityBytes-1) + "é" + "tail"
	SetImportSourceIdentity(script, long, true, "")
	document, _, _, _ := ImportSourceIdentityOf(script.Metadata.Import)
	if len(document) > MaxImportSourceIdentityBytes || strings.ContainsRune(document, '�') || !strings.HasPrefix(long, document) {
		t.Fatalf("bounded document = %d bytes %q…", len(document), document[len(document)-4:])
	}
}

// Names tools give every new document declare no identity; a real name does.
func TestGenericImportSourceName(t *testing.T) {
	for _, name := range []string{"", "OpenAPI definition", "FastAPI", "My  Title", "New Collection", "Test Plan", " api ", "script"} {
		if !GenericImportSourceName(name) {
			t.Errorf("%q is not taken as a default name", name)
		}
	}
	for _, name := range []string{"Orders API", "Checkout plan", "Payments"} {
		if GenericImportSourceName(name) {
			t.Errorf("%q is taken as a default name", name)
		}
	}
}
