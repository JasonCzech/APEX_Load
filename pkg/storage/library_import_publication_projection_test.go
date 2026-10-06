package storage

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLibraryImportPublicationProjection(t *testing.T) {
	for _, tc := range []struct {
		name, raw        string
		pending, invalid bool
	}{
		{"legacy_flags", `[{"kind":"script"}]`, false, false},
		{"padded_plural", `[{"kind":" Scripts ","prepared":true,"local_item_id":" source ","remote_id":" direct-import:retry "}]`, true, false},
		{"committed", `[{"kind":"script","prepared":true,"committed":true,"local_item_id":"source","remote_id":"direct-import:retry"}]`, false, false},
		{"prefix_case", `[{"kind":"script","prepared":true,"local_item_id":"source","remote_id":"Direct-import:retry"}]`, false, false},
		{"expired_pending", `[{"kind":"script","prepared":true,"local_item_id":"source","remote_id":"direct-import:retry","expires_at":"2000-01-01T00:00:00Z"}]`, true, false},
		{"unicode_key", `[{"Kind":"script"}]`, false, false},
		{"unknown_payload", `[{"kind":"script","before_script":{"kind":null,"prepared":[]}}]`, false, false},
		{"duplicate", `[{"kind":"script","prepared":true,"PREPARED":false}]`, false, true},
		{"duplicate_escaped", `[{"kind":"script","prepar\u0065d":true,"prepared":false}]`, false, true},
		{"duplicate_unicode", `[{"kind":"script","Kind":"result"}]`, false, true},
		{"duplicate_unknown", `[{"kind":"script","Owner":"a","owner":"b"}]`, false, true},
		{"null_flag", `[{"kind":"script","prepared":null}]`, false, true},
		{"null_id", `[{"kind":"script","local_item_id":null}]`, false, true},
		{"wrong_flag", `[{"kind":"script","prepared":"true"}]`, false, true},
		{"wrong_id", `[{"kind":"script","local_item_id":7}]`, false, true},
		{"wrong_remote", `[{"kind":"script","remote_id":[]}]`, false, true},
		{"wrong_kind", `[{"kind":{}}]`, false, true},
		{"unknown_kind", `[{"kind":"invalid"}]`, false, true},
		{"prepared_missing_id", `[{"kind":"script","prepared":true}]`, false, true},
		{"null_entry", `[null]`, false, true},
		{"array_entry", `[[]]`, false, true},
		{"scalar_entry", `[1]`, false, true},
		{"truncated", `[{"kind":"script"}`, false, true},
		{"trailing", `[] {}`, false, true},
		{"utf8", "[{\"kind\":\"\xff\"}]", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts, err := ParseLibraryImportPublication(tc.raw)
			if tc.invalid {
				if !errors.Is(err, ErrLibraryImportPublicationUnavailable) {
					t.Fatalf("accepted malformed projection: %v", err)
				}
				return
			}
			if err != nil || len(facts) != 1 {
				t.Fatalf("facts=%#v err=%v", facts, err)
			}
			if facts[0].PendingDirectImport() != tc.pending {
				t.Fatal("pending semantics changed")
			}
		})
	}
	for _, raw := range []string{"", " \n ", "null", " [] "} {
		facts, err := ParseLibraryImportPublication(raw)
		if err != nil || len(facts) != 0 {
			t.Fatal(raw, err)
		}
	}
	for _, kind := range []string{"script", " scripts ", "SCRIPT", "scenario", " scenarios ", "result", " RESULTS "} {
		if NormalizeLibraryImportKind(kind) == "" || LibraryImportAssetKey(kind, " item ") != NormalizeLibraryImportKind(kind)+"\x00item" {
			t.Fatal("normalization", kind)
		}
	}
}

func TestLibraryImportPublicationProjectionBounds(t *testing.T) {
	entry := `{"kind":"script"}`
	raw := "[" + strings.Repeat(entry+",", MaxLibraryImportClaims-1) + entry + "]"
	facts, err := ParseLibraryImportPublication(raw)
	if err != nil || len(facts) != MaxLibraryImportClaims {
		t.Fatal("exact entry boundary", err)
	}
	if _, err := ParseLibraryImportPublication("[" + strings.Repeat(entry+",", MaxLibraryImportClaims) + entry + "]"); err == nil {
		t.Fatal("overflow accepted")
	}
	prefix := `[{"kind":"script","unknown":"`
	suffix := `"}]`
	raw = prefix + strings.Repeat("a", MaxLibraryImportClaimsRawBytes-len(prefix)-len(suffix)) + suffix
	if _, err := ParseLibraryImportPublication(raw); err != nil {
		t.Fatal("exact byte boundary", err)
	}
	if _, err := ParseLibraryImportPublication(raw + " "); err == nil {
		t.Fatal("byte overflow accepted")
	}
	f := LibraryImportClaimFacts{Prepared: true, Kind: "script", LocalItemID: "s", RemoteID: "direct-import:x"}
	copy := f
	if !f.PendingDirectImport() || !reflect.DeepEqual(f, copy) {
		t.Fatal("predicate mutated facts")
	}
}
