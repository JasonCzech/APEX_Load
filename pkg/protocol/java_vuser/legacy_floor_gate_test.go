package java_vuser

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

// The controller-side capability gate compares the sidecar's advertised
// JavaMajor against the effective floor. Without honoring the operator's
// legacy opt-in here, a deliberately enabled Java 8 lane would be zeroed out
// by this check with no error naming it; without the floor at all, any
// ancient JVM would be admitted.
func TestRuntimeCapabilitiesHonorsLegacyJavaFloor(t *testing.T) {
	resetSidecarRuntimeCaches()
	t.Cleanup(resetSidecarRuntimeCaches)

	tests := []struct {
		name            string
		runnerURL       string
		javaMajor       int
		allowLegacyJava bool
		wantAvailable   bool
	}{
		{name: "java 8 rejected by default", runnerURL: "http://127.0.0.1:19201", javaMajor: 8, allowLegacyJava: false, wantAvailable: false},
		{name: "java 8 accepted under opt-in", runnerURL: "http://127.0.0.1:19202", javaMajor: 8, allowLegacyJava: true, wantAvailable: true},
		{name: "java 7 rejected even under opt-in", runnerURL: "http://127.0.0.1:19203", javaMajor: 7, allowLegacyJava: true, wantAvailable: false},
		{name: "java 21 accepted either way", runnerURL: "http://127.0.0.1:19204", javaMajor: 21, allowLegacyJava: false, wantAvailable: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeJavaVuserSidecarClient{
				capabilitiesResponse: javasidecar.CapabilitiesResponse{
					Healthy:      true,
					JavaMajor:    tc.javaMajor,
					MaxProcesses: 7,
					Features: []string{
						javasidecar.FeatureValidationOnly,
						javasidecar.FeatureOwnerFencingV1,
						javasidecar.FeatureExternalRuntimeV1,
						javasidecar.FeatureRunnerOwnerLeaseV1,
						javasidecar.FeatureRuntimeLeaseV1,
					},
				},
			}
			runner := JavaVuser{
				cfg: configuration{
					Enabled:         true,
					RunnerMode:      config.JavaVuserRunnerModeSidecar,
					RunnerURL:       tc.runnerURL,
					RunnerCommand:   defaultRunnerCommand,
					AllowLegacyJava: tc.allowLegacyJava,
				},
				sidecar: fake,
			}
			capabilities := runner.RuntimeCapabilities()
			if capabilities.Available != tc.wantAvailable {
				t.Fatalf("RuntimeCapabilities().Available = %v (MaxVUsers=%d), want %v for JavaMajor=%d allowLegacy=%v",
					capabilities.Available, capabilities.MaxVUsers, tc.wantAvailable, tc.javaMajor, tc.allowLegacyJava)
			}
			if tc.wantAvailable && capabilities.MaxVUsers != 7 {
				t.Fatalf("MaxVUsers = %d, want the sidecar's advertised capacity 7", capabilities.MaxVUsers)
			}
		})
	}
}
