package java_vuser

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"sync"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Each adapter is owned by one VUser or branch. Binding is immutable after
// preparation, including pooled adapters whose transport is shared by slots.
type nativeServiceBinding struct {
	nativeMu sync.Mutex
	native   protocol.NativeServices
}

func (b *nativeServiceBinding) SetRuntimeNativeServices(service protocol.NativeServices) error {
	b.nativeMu.Lock()
	defer b.nativeMu.Unlock()
	if service == nil || b.native != nil {
		return errors.New("native_service_binding_invalid")
	}
	b.native = service
	return nil
}
func (b *nativeServiceBinding) beginNative(ctx context.Context, id string) (protocol.NativeInvocation, error) {
	b.nativeMu.Lock()
	service := b.native
	b.nativeMu.Unlock()
	if service == nil {
		return nil, nil
	}
	return service.BeginInvocation(ctx, id)
}

type nativeReply struct {
	Result *protocol.NativeDataResult `json:"result,omitempty"`
	Error  *protocol.NativeDataError  `json:"error,omitempty"`
}

func invokeNativeData(ctx context.Context, invocation protocol.NativeInvocation, sequence uint64, payload []byte) ([]byte, error) {
	if invocation == nil {
		return nil, errors.New("native_service_not_admitted")
	}
	request, err := protocol.DecodeNativeDataRequest(payload)
	if err != nil {
		// A malformed channel request has no accepted sequence. Close the
		// exchange immediately instead of allowing the guest to skip it.
		return nil, errors.New("native_service_request_invalid")
	}
	result, err := invocation.Call(ctx, sequence, request)
	reply := nativeReply{}
	if err == nil {
		reply.Result = &result
	} else {
		var typed *protocol.NativeDataError
		if !errors.As(err, &typed) {
			typed = &protocol.NativeDataError{Code: "operation_rejected"}
		}
		reply.Error = typed
	}
	if invocation.Err() != nil {
		return nil, errors.New("native_service_continuity_lost")
	}
	data, err := json.Marshal(reply)
	if err != nil || len(data) > protocol.NativeDataResultBytes {
		return nil, errors.New("native_service_reply_invalid")
	}
	return data, nil
}

func nativeInvocationContext(ctx context.Context, invocation protocol.NativeInvocation) (context.Context, func()) {
	if invocation == nil {
		return ctx, func() {}
	}
	call, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-call.Done():
		case <-invocation.Done():
			cancel()
		}
	}()
	return call, func() { cancel(); invocation.Close() }
}

func (b *nativeServiceBinding) hasNative() bool {
	b.nativeMu.Lock()
	defer b.nativeMu.Unlock()
	return b.native != nil
}
func (b *nativeServiceBinding) nativeContext(ctx context.Context, id string) (context.Context, func(), error) {
	invocation, err := b.beginNative(ctx, id)
	if err != nil {
		return ctx, func() {}, err
	}
	call, close := nativeInvocationContext(ctx, invocation)
	if invocation != nil {
		call = javarunner.WithNativeDataCallback(call, func(ctx context.Context, seq uint64, payload []byte) ([]byte, error) {
			return invokeNativeData(ctx, invocation, seq, payload)
		})
	}
	return call, close, nil
}
