package protocol

import "testing"

func TestNativeDataRequestRejectsForgedIdentityAndNullCells(t *testing.T) {
	for _, data := range []string{`{"alias":"x","operation":"pop","client_id":"forged"}`, `{"alias":"x","operation":"push","values":{"id":null}}`, `{"alias":"x","operation":"pop","wait_ms":null}`, `{"alias":"x","operation":"pop"} {}`, `null`} {
		if _, err := DecodeNativeDataRequest([]byte(data)); err == nil {
			t.Fatalf("invalid request accepted %s", data)
		}
	}
	if _, err := DecodeNativeDataRequest([]byte(`{"alias":"x","operation":"push","values":{"id":"","literal":"{{secret.raw}}"}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestNativeDataRequestRejectsUnicodeReplacement(t *testing.T) {
	for _, input := range []string{
		`{"alias":"x","operation":"push","values":{"id":"\ud800"}}`,
		`{"alias":"x","operation":"push","values":{"id":"\udc00"}}`,
		`{"alias":"x","operation":"push","values":{"id":"\ud800\u0041"}}`,
		"{\"alias\":\"x\",\"operation\":\"push\",\"values\":{\"id\":\"" + string([]byte{0xff}) + "\"}}",
	} {
		if _, err := DecodeNativeDataRequest([]byte(input)); err == nil {
			t.Fatal("malformed Unicode accepted")
		}
	}
	q, err := DecodeNativeDataRequest([]byte(`{"alias":"x","operation":"push","values":{"id":"\ud83d\ude00","literal":"\\ud800"}}`))
	if err != nil || q.Values["id"] != "😀" || q.Values["literal"] != `\ud800` {
		t.Fatalf("valid literals changed: %+v %v", q, err)
	}
}

func TestNativeDataRequestRequiresUniqueCanonicalEnvelopeKeys(t *testing.T) {
	for _, input := range []string{
		`{"alias":"x","operation":"push","Values":{"id":null}}`,
		`{"alias":"x","operation":"push","value\u017f":{"id":null}}`,
		`{"alias":"x","operation":"update","Expected":{"id":null}}`,
		`{"alias":"x","operation":"push","values":{"id":null},"values":{"other":"safe"}}`,
		`{"alias":"x","alias":"y","operation":"pop"}`,
	} {
		if _, err := DecodeNativeDataRequest([]byte(input)); err == nil {
			t.Fatalf("ambiguous envelope accepted: %s", input)
		}
	}
}
