package java_vuser

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javasidecar"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type nativeAdapterInvocation struct {
	done chan struct{}
	once sync.Once
	call func(context.Context, uint64, protocol.NativeDataRequest) (protocol.NativeDataResult, error)
}

func (n *nativeAdapterInvocation) Call(ctx context.Context, sequence uint64, q protocol.NativeDataRequest) (protocol.NativeDataResult, error) {
	return n.call(ctx, sequence, q)
}
func (n *nativeAdapterInvocation) Done() <-chan struct{} { return n.done }
func (n *nativeAdapterInvocation) Err() error {
	select {
	case <-n.done:
		return errors.New("continuity_lost")
	default:
		return nil
	}
}
func (n *nativeAdapterInvocation) Close() { n.once.Do(func() { close(n.done) }) }

type nativeAdapterService struct{ invocation *nativeAdapterInvocation }

func (s nativeAdapterService) BeginInvocation(context.Context, string) (protocol.NativeInvocation, error) {
	return s.invocation, nil
}

func TestNativeAdapterBindingLiteralValuesAndLostAuthority(t *testing.T) {
	for _, kind := range []string{"pooled", "broker"} {
		t.Run(kind, func(t *testing.T) {
			invocation := &nativeAdapterInvocation{done: make(chan struct{})}
			invocation.call = func(_ context.Context, sequence uint64, q protocol.NativeDataRequest) (protocol.NativeDataResult, error) {
				if sequence != 1 || q.Values["id"] != "{{secret.literal}}雪" {
					t.Error("callback value or sequence changed")
				}
				return protocol.NativeDataResult{Status: "ok", Revision: "18446744073709551615", Count: "1"}, nil
			}
			call := func(ctx context.Context) {
				cb := javarunner.NativeDataCallbackFrom(ctx)
				if cb == nil {
					t.Fatal("callback not bound to admitted invocation")
				}
				payload, err := cb(ctx, 1, []byte(`{"alias":"work","operation":"push","values":{"id":"{{secret.literal}}雪"}}`))
				var reply nativeReply
				if err != nil || json.Unmarshal(payload, &reply) != nil || reply.Result == nil || reply.Result.Revision != "18446744073709551615" {
					t.Fatal("callback reply changed", err)
				}
				invocation.Close()
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
					t.Fatal("lost authority did not cancel Java invocation")
				}
			}
			var adapter protocol.Protocol
			if kind == "pooled" {
				adapter = adapterFixture(t, &adapterSlot{invoke: func(ctx context.Context, _ string, _ map[string]string, _ time.Duration) (*javasidecar.PooledInvocationResult, error) {
					call(ctx)
					return nil, ctx.Err()
				}})
			} else {
				adapter = brokerAdapterFixture(t, &brokerAdapterSession{invoke: func(ctx context.Context, _ string, _ map[string]string) (javasidecar.ExecutionResponse, error) {
					call(ctx)
					return javasidecar.ExecutionResponse{}, ctx.Err()
				}})
			}
			binding := adapter.(protocol.RuntimeNativeServicesSetter)
			if err := binding.SetRuntimeNativeServices(nativeAdapterService{invocation}); err != nil {
				t.Fatal(err)
			}
			if binding.SetRuntimeNativeServices(nativeAdapterService{invocation}) == nil {
				t.Fatal("native authority was replaced")
			}
			if result, err := adapter.Execute(t.Context(), pooledAction("init")); err != nil || result.Success {
				t.Fatal("lost authority continued successfully", result, err)
			}
		})
	}
}

func TestNativeAdapterMalformedCallbackFailsBeforeCore(t *testing.T) {
	invocation := &nativeAdapterInvocation{done: make(chan struct{}), call: func(context.Context, uint64, protocol.NativeDataRequest) (protocol.NativeDataResult, error) {
		t.Fatal("malformed callback reached core")
		return protocol.NativeDataResult{}, nil
	}}
	for _, payload := range []string{`{"alias":"work","operation":"push","token":"guest"}`, `{"alias":"work","values":null}`, `{`} {
		if _, err := invokeNativeData(t.Context(), invocation, 1, []byte(payload)); err == nil {
			t.Fatal("malformed callback did not close the exchange")
		}
	}
}

func TestNativeJavaCapabilityRequiresSpecificModeProof(t *testing.T) {
	for _, proof := range []bool{false, true} {
		resetSidecarRuntimeCaches()
		features := []string{javasidecar.FeatureOwnerFencingV1, javasidecar.FeatureExternalRuntimeV1, javasidecar.FeatureRunnerOwnerLeaseV1, javasidecar.FeatureRuntimeLeaseV1, protocol.FeatureJavaVuserPersistentLifecycleV1, protocol.FeatureJavaVuserSessionFrameTokenV1, protocol.FeatureNativeDataServicesV1, protocol.FeatureNativeDataJavaV1}
		if proof {
			features = append(features, protocol.FeatureNativeDataSidecarV1)
		}
		runner := JavaVuser{cfg: configuration{Enabled: true, RunnerMode: config.JavaVuserRunnerModeSidecar, RunnerURL: "http://127.0.0.1:19988", RunnerCommand: defaultRunnerCommand}, sidecar: &fakeJavaVuserSidecarClient{capabilitiesResponse: javasidecar.CapabilitiesResponse{Healthy: true, JavaMajor: 21, MaxProcesses: 1, Features: features}}}
		caps := runner.RuntimeCapabilities()
		if !caps.Available || protocol.RuntimeFeaturesSatisfy(caps.Features, protocol.FeatureNativeDataJavaV1) != proof {
			t.Fatalf("specific proof=%v capabilities=%+v", proof, caps)
		}
	}
	t.Cleanup(resetSidecarRuntimeCaches)
}
