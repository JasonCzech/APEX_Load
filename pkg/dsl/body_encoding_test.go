package dsl

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func TestDecodeBodyEncoding(t *testing.T) {
	wire := []byte{0x00, 0xff, '{', '}'}
	encoded := base64.StdEncoding.EncodeToString(wire)
	decoded, transformed, err := DecodeBodyEncoding(encoded, " BASE64 ", len(wire))
	if err != nil {
		t.Fatal(err)
	}
	if !transformed || !bytes.Equal(decoded, wire) {
		t.Fatalf("DecodeBodyEncoding() = %v, %t; want %v, true", decoded, transformed, wire)
	}
	if decoded, transformed, err := DecodeBodyEncoding(encoded, "text", 1); err != nil || transformed || decoded != nil {
		t.Fatalf("text DecodeBodyEncoding() = %v, %t, %v", decoded, transformed, err)
	}
}

func TestDecodeBodyEncodingEnforcesBoundBeforeDecode(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("sixsix"))
	if _, transformed, err := DecodeBodyEncoding(encoded, "base64", 5); !transformed || !errors.Is(err, ErrBodyEncodingLimit) {
		t.Fatalf("DecodeBodyEncoding() transformed=%t error=%v, want bounded failure", transformed, err)
	}
}
