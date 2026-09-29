package javarunner

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func wireFixture() PooledFrame {
	return PooledFrame{Generation: "generation", Slot: "slot", Request: "1", Operation: "invoke", Sequence: 1, Fields: map[string]string{"method": "action", "p.unicode": "é🔑"}}
}
func TestPooledWireRoundTripAndForgery(t *testing.T) {
	secret := bytes.Repeat([]byte{17}, 32)
	f := wireFixture()
	wire, err := EncodePooledFrame(f, secret, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadPooledFrame(bytes.NewReader(wire), secret, false)
	if err != nil || got.Fields["p.unicode"] != f.Fields["p.unicode"] {
		t.Fatalf("round trip: %#v %v", got, err)
	}
	for _, name := range []string{"direction", "signature", "identity", "truncated", "oversize", "contract", "trailing", "duplicate"} {
		t.Run(name, func(t *testing.T) {
			damaged := append([]byte(nil), wire...)
			response := false
			switch name {
			case "direction":
				response = true
			case "signature":
				damaged[len(damaged)-1] ^= 1
			case "identity":
				damaged[45] ^= 1
			case "truncated":
				damaged = damaged[:len(damaged)-1]
			case "oversize":
				binary.BigEndian.PutUint32(damaged, pooledMaxFrame+1)
			case "contract":
				damaged[8] ^= 1
				damaged = resignWire(damaged, secret)
			case "trailing":
				damaged = append(damaged[:len(damaged)-32], append([]byte{0}, make([]byte, 32)...)...)
				binary.BigEndian.PutUint32(damaged, uint32(len(damaged)-4))
				damaged = resignWire(damaged, secret)
			case "duplicate":
				var payload bytes.Buffer
				for _, s := range []string{PooledIPCContract, "generation", "slot", "1", "invoke"} {
					pooledString(&payload, s)
				}
				_ = binary.Write(&payload, binary.BigEndian, uint64(1))
				_ = binary.Write(&payload, binary.BigEndian, uint32(2))
				for i := 0; i < 2; i++ {
					pooledString(&payload, "method")
					pooledString(&payload, "action")
				}
				damaged = append(make([]byte, 4), payload.Bytes()...)
				damaged = append(damaged, make([]byte, 32)...)
				binary.BigEndian.PutUint32(damaged, uint32(len(damaged)-4))
				damaged = resignWire(damaged, secret)
			}
			if _, err := ReadPooledFrame(bytes.NewReader(damaged), secret, response); err == nil {
				t.Fatal("accepted invalid frame")
			}
		})
	}
}
func resignWire(raw, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte{0})
	mac.Write(raw[4 : len(raw)-32])
	copy(raw[len(raw)-32:], mac.Sum(nil))
	return raw
}
func TestPooledWireBounds(t *testing.T) {
	for _, change := range []func(*PooledFrame){func(f *PooledFrame) { f.Request = "reused" }, func(f *PooledFrame) { f.Sequence = 1 << 63 }, func(f *PooledFrame) { f.Generation = "" }, func(f *PooledFrame) { f.Fields["key"] = strings.Repeat("x", 65537) }, func(f *PooledFrame) { f.Fields["key"] = string([]byte{255}) }, func(f *PooledFrame) {
		for i := 0; i < 20; i++ {
			f.Fields[strings.Repeat("x", i+1)] = strings.Repeat("x", 65536)
		}
	}} {
		f := wireFixture()
		change(&f)
		if _, err := EncodePooledFrame(f, make([]byte, 32), false); err == nil {
			t.Fatal("accepted invalid frame")
		}
	}
	if _, err := ReadPooledFrame(bytes.NewReader(nil), make([]byte, 32), false); err != io.EOF {
		t.Fatal(err)
	}
}
func FuzzPooledFrame(f *testing.F) {
	seed, _ := EncodePooledFrame(wireFixture(), make([]byte, 32), false)
	f.Add(seed)
	f.Fuzz(func(t *testing.T, raw []byte) { _, _ = ReadPooledFrame(bytes.NewReader(raw), make([]byte, 32), false) })
}
