package javarunner

import (
	"bytes"
	"strings"
	"testing"
)

func TestNativeDataFrameBindsDirectionPayloadAndSession(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	f := NativeDataFrame{Scope: "generation:slot", Invocation: "invoke-1", Sequence: 1, Payload: []byte(`{"alias":"work","operation":"pop"}`)}
	encoded, err := EncodeNativeDataFrame(f, secret, false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeNativeDataFrame(encoded, secret, false)
	if err != nil || out.Scope != f.Scope || out.Invocation != f.Invocation || out.Sequence != f.Sequence || !bytes.Equal(out.Payload, f.Payload) {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := DecodeNativeDataFrame(encoded, secret, true); err == nil {
		t.Fatal("direction reflection accepted")
	}
	if _, err := DecodeNativeDataFrame(encoded, bytes.Repeat([]byte{8}, 32), false); err == nil {
		t.Fatal("other session accepted")
	}
	corrupt := []byte(encoded)
	corrupt[len(corrupt)/2] ^= 1
	if _, err := DecodeNativeDataFrame(string(corrupt), secret, false); err == nil {
		t.Fatal("payload alteration accepted")
	}
	f.Payload = []byte(strings.Repeat("x", NativeDataPayloadBytes+1))
	if _, err := EncodeNativeDataFrame(f, secret, false); err == nil {
		t.Fatal("oversized payload accepted")
	}
}
