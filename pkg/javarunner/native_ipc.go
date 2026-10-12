package javarunner

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf8"
)

const NativeDataContract = "apexload.native_data.v1"
const NativeDataPrefix = "APEXLOAD_NATIVE_DATA_V1 "
const NativeDataPayloadBytes = 2 << 20
const NativeDataFrameBytes = 3 << 20

var ErrNativeDataFrame = errors.New("native_data_frame_invalid")

// NativeDataFrame authenticates every byte and its direction. The scope binds
// the persistent session or generation/slot; Invocation and Sequence bind one
// active guest call. The payload is opaque to this policy-neutral transport.
type NativeDataFrame struct {
	Scope      string
	Invocation string
	Sequence   uint64
	Payload    []byte
}

func EncodeNativeDataFrame(frame NativeDataFrame, secret []byte, reply bool) (string, error) {
	if len(secret) != 32 || frame.Scope == "" || len(frame.Scope) > 256 || frame.Invocation == "" || len(frame.Invocation) > 256 || frame.Sequence == 0 || frame.Sequence > 1<<63-1 || len(frame.Payload) > NativeDataPayloadBytes || !utf8.Valid(frame.Payload) || !utf8.ValidString(frame.Scope) || !utf8.ValidString(frame.Invocation) {
		return "", ErrNativeDataFrame
	}
	var buffer bytes.Buffer
	if reply {
		buffer.WriteByte(1)
	} else {
		buffer.WriteByte(0)
	}
	for _, s := range []string{NativeDataContract, frame.Scope, frame.Invocation} {
		_ = binary.Write(&buffer, binary.BigEndian, uint32(len(s)))
		buffer.WriteString(s)
	}
	_ = binary.Write(&buffer, binary.BigEndian, frame.Sequence)
	_ = binary.Write(&buffer, binary.BigEndian, uint32(len(frame.Payload)))
	buffer.Write(frame.Payload)
	mac := hmac.New(sha256.New, secret)
	mac.Write(buffer.Bytes())
	buffer.Write(mac.Sum(nil))
	return base64.StdEncoding.EncodeToString(buffer.Bytes()), nil
}

func DecodeNativeDataFrame(encoded string, secret []byte, reply bool) (NativeDataFrame, error) {
	var frame NativeDataFrame
	if len(secret) != 32 || len(encoded) > NativeDataFrameBytes {
		return frame, ErrNativeDataFrame
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) < 33 || len(raw) > NativeDataPayloadBytes+2048 {
		return frame, ErrNativeDataFrame
	}
	body, signature := raw[:len(raw)-32], raw[len(raw)-32:]
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), signature) {
		return frame, ErrNativeDataFrame
	}
	reader := bytes.NewReader(body)
	direction, _ := reader.ReadByte()
	if direction != 0 && !reply || direction != 1 && reply {
		return frame, ErrNativeDataFrame
	}
	read := func(limit uint32) (string, error) {
		var n uint32
		if binary.Read(reader, binary.BigEndian, &n) != nil || n > limit || uint64(n) > uint64(reader.Len()) {
			return "", ErrNativeDataFrame
		}
		data := make([]byte, int(n))
		if _, err := io.ReadFull(reader, data); err != nil || !utf8.Valid(data) {
			return "", ErrNativeDataFrame
		}
		return string(data), nil
	}
	contract, err := read(128)
	if err != nil || contract != NativeDataContract {
		return frame, ErrNativeDataFrame
	}
	frame.Scope, err = read(256)
	if err != nil || frame.Scope == "" {
		return frame, ErrNativeDataFrame
	}
	frame.Invocation, err = read(256)
	if err != nil || frame.Invocation == "" {
		return frame, ErrNativeDataFrame
	}
	if binary.Read(reader, binary.BigEndian, &frame.Sequence) != nil || frame.Sequence == 0 || frame.Sequence > 1<<63-1 {
		return frame, ErrNativeDataFrame
	}
	payload, err := read(NativeDataPayloadBytes)
	if err != nil || reader.Len() != 0 {
		return frame, ErrNativeDataFrame
	}
	frame.Payload = []byte(payload)
	return frame, nil
}
