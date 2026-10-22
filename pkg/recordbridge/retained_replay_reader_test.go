package recordbridge

import (
	"bytes"
	"io"
	"testing"
)

func TestVerifiedReplayHAROwnership(t *testing.T) {
	raw := []byte(`{"log":{"version":"1.2","entries":[]}}`)
	closed := 0
	har, err := NewVerifiedReplayHAR(bytes.NewReader(raw), int64(len(raw)), func() { closed++ })
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(har.Reader())
	if err != nil || !bytes.Equal(got, raw) || har.Length() != int64(len(raw)) || har.Fidelity != "native" || har.Summary != nil {
		t.Fatal("native byte contract changed", err)
	}
	copied := har
	har.Close()
	copied.Close()
	if closed != 1 {
		t.Fatalf("reader closed %d times", closed)
	}
	for _, size := range []int64{-1, 0, 256<<20 + 1} {
		rejected, err := NewVerifiedReplayHAR(bytes.NewReader(raw), size, func() { closed++ })
		rejected.Close()
		if err == nil || closed != 1 {
			t.Fatal("invalid input transferred ownership")
		}
	}
	if _, err := NewVerifiedReplayHAR(nil, 1, func() { closed++ }); err == nil {
		t.Fatal("nil reader accepted")
	}
	if _, err := NewVerifiedReplayHAR(bytes.NewReader(raw), 1, nil); err == nil {
		t.Fatal("unowned reader accepted")
	}
}
