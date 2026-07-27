package javasidecar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

// The controller compares the sidecar's advertised JavaMajor against its own
// effective floor, so a sidecar that accepts a legacy JVM while advertising the
// unconditional constant would make the two sides of the wire disagree.
func TestCapabilitiesAdvertiseTheEffectiveJavaFloor(t *testing.T) {
	tests := []struct {
		name            string
		allowLegacyJava bool
		wantMinimum     int
	}{
		{name: "default", allowLegacyJava: false, wantMinimum: javarunner.MinimumJavaMajor},
		{name: "legacy opt-in", allowLegacyJava: true, wantMinimum: javarunner.MinimumLegacyJavaMajor},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			implementation := NewServer(ServerOptions{
				Operator: config.JavaVuserOperatorConfig{
					Enabled:         true,
					AuthToken:       testSidecarAuthToken,
					MaxProcesses:    1,
					AllowLegacyJava: tc.allowLegacyJava,
				},
				CredentialCheck:                  func() error { return nil },
				supportsStrongProcessContainment: func() bool { return true },
				// The response field is independent of runtime health. Avoid a
				// real JDK probe here: on a host without process-containment
				// support that probe permanently fences the server and makes
				// the second table row observe only a generic error payload.
				resolveJDK: func(context.Context, javarunner.ResolveOptions) (javarunner.ResolvedJDK, error) {
					return javarunner.ResolvedJDK{}, errors.New("fixture runtime unavailable")
				},
			})

			request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
			request.Header.Set("X-Apexload-Java-Runner-Token", testSidecarAuthToken)
			response := httptest.NewRecorder()
			implementation.ServeHTTP(response, request)

			var capabilities CapabilitiesResponse
			if err := json.Unmarshal(response.Body.Bytes(), &capabilities); err != nil {
				t.Fatalf("json.Unmarshal returned error: %v body=%q", err, response.Body.String())
			}
			if capabilities.MinimumJavaMajor != tc.wantMinimum {
				t.Fatalf("advertised minimum_java_major = %d, want %d", capabilities.MinimumJavaMajor, tc.wantMinimum)
			}
		})
	}
}

// The sidecar resolves its own JDK from its own environment, so the opt-in has
// to reach that resolution too. Without it the sidecar's health probe rejects
// the legacy JVM and the caller sees zero capacity instead of a version error.
func TestPrepareRuntimePassesLegacyOptInToJDKResolution(t *testing.T) {
	for _, allowLegacyJava := range []bool{false, true} {
		observed := false
		var observedValue bool
		implementation := NewServer(ServerOptions{
			Operator: config.JavaVuserOperatorConfig{
				Enabled:         true,
				AuthToken:       testSidecarAuthToken,
				MaxProcesses:    1,
				AllowLegacyJava: allowLegacyJava,
			},
			CredentialCheck:                  func() error { return nil },
			supportsStrongProcessContainment: func() bool { return true },
			resolveJDK: func(_ context.Context, options javarunner.ResolveOptions) (javarunner.ResolvedJDK, error) {
				observed = true
				observedValue = options.AllowLegacyJava
				return javarunner.ResolvedJDK{}, context.Canceled
			},
		})

		implementation.prepareRuntime(context.Background(), RuntimeSettings{})
		if !observed {
			t.Fatalf("allowLegacyJava=%v: JDK resolution was never called", allowLegacyJava)
		}
		if observedValue != allowLegacyJava {
			t.Fatalf("resolve options AllowLegacyJava = %v, want %v", observedValue, allowLegacyJava)
		}
	}
}
