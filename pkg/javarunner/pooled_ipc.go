package javarunner

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"strconv"
	"unicode/utf8"
)

// PooledIPCContract is experimental and negotiated separately from dedicated IPC v2.
const PooledIPCContract = "apexload.java_vuser.ipc.v3"
const pooledMaxFrame = 1 << 20
const pooledMaxFields = 1024

var ErrPooledFrame = errors.New("pooled_frame_invalid")

// PooledFrame binds every response to an operation and its unique host/request/slot.
// Sequence is monotonically increasing across the host; Request is its decimal
// representation. Retries and slot identity reuse are forbidden.
type PooledFrame struct {
	Generation, Slot, Request, Operation string
	Sequence                             uint64
	Fields                               map[string]string
}

func pooledID(s string, empty bool) bool {
	if s == "" {
		return empty
	}
	if len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func (f PooledFrame) valid() bool {
	if !pooledID(f.Generation, false) || !pooledID(f.Slot, true) || f.Sequence == 0 || f.Sequence > 1<<63-1 || f.Request != strconv.FormatUint(f.Sequence, 10) || len(f.Fields) > pooledMaxFields {
		return false
	}
	switch f.Operation {
	case "ready", "open", "commit", "invoke", "interrupt", "close", "health":
	default:
		return false
	}
	for k, v := range f.Fields {
		if k == "" || len(k) > 128 || len(v) > 65536 || !utf8.ValidString(k) || !utf8.ValidString(v) {
			return false
		}
	}
	return true
}
func pooledString(b *bytes.Buffer, s string) {
	_ = binary.Write(b, binary.BigEndian, uint32(len(s)))
	b.WriteString(s)
}

// EncodePooledFrame authenticates the contract and direction as well as all
// payload bytes. The secret is delivered through the saved control pipe, never
// argv/environment/script context. It is not a sandbox against hostile JVM code.
func EncodePooledFrame(f PooledFrame, secret []byte, response bool) ([]byte, error) {
	if len(secret) != 32 || !f.valid() {
		return nil, ErrPooledFrame
	}
	var b bytes.Buffer
	pooledString(&b, PooledIPCContract)
	for _, s := range []string{f.Generation, f.Slot, f.Request, f.Operation} {
		pooledString(&b, s)
	}
	_ = binary.Write(&b, binary.BigEndian, f.Sequence)
	_ = binary.Write(&b, binary.BigEndian, uint32(len(f.Fields)))
	keys := make([]string, 0, len(f.Fields))
	for k := range f.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pooledString(&b, k)
		pooledString(&b, f.Fields[k])
		if b.Len() > pooledMaxFrame-32 {
			return nil, ErrPooledFrame
		}
	}
	mac := hmac.New(sha256.New, secret)
	if response {
		mac.Write([]byte{1})
	} else {
		mac.Write([]byte{0})
	}
	mac.Write(b.Bytes())
	var out bytes.Buffer
	_ = binary.Write(&out, binary.BigEndian, uint32(b.Len()+32))
	out.Write(b.Bytes())
	out.Write(mac.Sum(nil))
	return out.Bytes(), nil
}

func ReadPooledFrame(r io.Reader, secret []byte, response bool) (PooledFrame, error) {
	var f PooledFrame
	if len(secret) != 32 {
		return f, ErrPooledFrame
	}
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return f, err
	}
	if n < 32 || n > pooledMaxFrame {
		return f, ErrPooledFrame
	}
	raw := make([]byte, int(n))
	if _, err := io.ReadFull(r, raw); err != nil {
		return f, err
	}
	payload := raw[:len(raw)-32]
	mac := hmac.New(sha256.New, secret)
	if response {
		mac.Write([]byte{1})
	} else {
		mac.Write([]byte{0})
	}
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), raw[len(raw)-32:]) {
		return f, ErrPooledFrame
	}
	b := bytes.NewReader(payload)
	readString := func(limit uint32) (string, error) {
		var size uint32
		if binary.Read(b, binary.BigEndian, &size) != nil || size > limit || uint64(size) > uint64(b.Len()) {
			return "", ErrPooledFrame
		}
		v := make([]byte, int(size))
		_, _ = io.ReadFull(b, v)
		if !utf8.Valid(v) {
			return "", ErrPooledFrame
		}
		return string(v), nil
	}
	contract, err := readString(128)
	if err != nil || contract != PooledIPCContract {
		return f, ErrPooledFrame
	}
	for _, p := range []*string{&f.Generation, &f.Slot, &f.Request, &f.Operation} {
		*p, err = readString(128)
		if err != nil {
			return f, ErrPooledFrame
		}
	}
	var count uint32
	if binary.Read(b, binary.BigEndian, &f.Sequence) != nil || binary.Read(b, binary.BigEndian, &count) != nil || count > pooledMaxFields {
		return f, ErrPooledFrame
	}
	f.Fields = make(map[string]string, int(count))
	for i := uint32(0); i < count; i++ {
		k, e := readString(128)
		if e != nil {
			return f, ErrPooledFrame
		}
		v, e := readString(65536)
		if e != nil {
			return f, ErrPooledFrame
		}
		if _, ok := f.Fields[k]; ok {
			return f, ErrPooledFrame
		}
		f.Fields[k] = v
	}
	if b.Len() != 0 || !f.valid() {
		return f, ErrPooledFrame
	}
	return f, nil
}
