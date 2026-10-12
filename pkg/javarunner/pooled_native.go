package javarunner

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
)

func pooledNativeFields(sequence uint64, payload []byte) map[string]string {
	encoded := base64.StdEncoding.EncodeToString(payload)
	fields := map[string]string{"callback_sequence": strconv.FormatUint(sequence, 10)}
	for i := 0; len(encoded) > 0; i++ {
		n := min(len(encoded), 65536)
		fields["payload"+strconv.Itoa(i)] = encoded[:n]
		encoded = encoded[n:]
	}
	return fields
}
func pooledNativePayload(fields map[string]string) (uint64, []byte, error) {
	sequence, err := strconv.ParseUint(fields["callback_sequence"], 10, 64)
	if err != nil || sequence == 0 || sequence > 1024 || len(fields) < 2 || len(fields) > 44 {
		return 0, nil, ErrPooledFrame
	}
	var encoded strings.Builder
	for i := 0; i < len(fields)-1; i++ {
		v, ok := fields["payload"+strconv.Itoa(i)]
		if !ok || v == "" || len(v) > 65536 {
			return 0, nil, ErrPooledFrame
		}
		encoded.WriteString(v)
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(encoded.String())
	if err != nil || len(payload) > NativeDataPayloadBytes {
		return 0, nil, ErrPooledFrame
	}
	return sequence, payload, nil
}

// Four callback workers per host bound retained requests/results to roughly
// 16MiB. They never block the reader/control writer. Saturation reaps the host
// rather than leaving an accepted callback silently queued behind bulk data.
func (t *PooledTransport) nativeCall(f PooledFrame) bool {
	seq, payload, err := pooledNativePayload(f.Fields)
	if err != nil {
		return false
	}
	t.mu.Lock()
	p, ok := t.pending[f.Sequence]
	if !ok || p.native == nil || p.nativeBusy || p.frame.Operation != "invoke" || f.Generation != p.frame.Generation || f.Slot != p.frame.Slot || f.Request != p.frame.Request || seq != p.nativeSequence+1 {
		t.mu.Unlock()
		return false
	}
	select {
	case t.nativeSlots <- struct{}{}:
	default:
		t.mu.Unlock()
		return false
	}
	p.nativeBusy = true
	p.nativeSequence = seq
	t.pending[f.Sequence] = p
	t.mu.Unlock()
	go func() {
		defer func() { <-t.nativeSlots }()
		ctx, cancel := context.WithCancel(p.nativeCtx)
		defer cancel()
		go func() {
			select {
			case <-t.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		result, e := p.native(ctx, seq, payload)
		if e != nil || len(result) > NativeDataPayloadBytes {
			t.fail(ErrNativeExchange)
			return
		}
		f.Operation = "native_reply"
		f.Fields = pooledNativeFields(seq, result)
		data, e := EncodePooledFrame(f, t.secret, false)
		if e != nil {
			t.fail(e)
			return
		}
		t.mu.Lock()
		current, ok := t.pending[f.Sequence]
		if t.stopped || !ok || !current.nativeBusy || current.nativeSequence != seq {
			t.mu.Unlock()
			return
		}
		current.nativeBusy = false
		t.pending[f.Sequence] = current
		select {
		case t.nativeWrites <- data:
			t.mu.Unlock()
		case <-t.done:
			t.mu.Unlock()
		default:
			t.mu.Unlock()
			t.fail(ErrNativeExchange)
		}
	}()
	return true
}
