package javarunner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEffectiveMinimumJavaMajor(t *testing.T) {
	if got := EffectiveMinimumJavaMajor(false); got != MinimumJavaMajor {
		t.Fatalf("EffectiveMinimumJavaMajor(false) = %d, want %d", got, MinimumJavaMajor)
	}
	if got := EffectiveMinimumJavaMajor(true); got != MinimumLegacyJavaMajor {
		t.Fatalf("EffectiveMinimumJavaMajor(true) = %d, want %d", got, MinimumLegacyJavaMajor)
	}
}

// The legacy opt-in exists for middleware that only runs on Java 8. It lowers
// the floor; it never removes it, so a JVM older than 8 stays rejected.
func TestProbeResolvedJDKAppliesLegacyJavaFloor(t *testing.T) {
	tests := []struct {
		name            string
		major           int
		version         string
		allowLegacyJava bool
		wantAccepted    bool
		wantMinimum     string
	}{
		{name: "java 8 rejected by default", major: 8, version: "1.8.0_351", allowLegacyJava: false, wantMinimum: "17"},
		{name: "java 8 accepted under opt-in", major: 8, version: "1.8.0_351", allowLegacyJava: true, wantAccepted: true},
		{name: "java 7 rejected even under opt-in", major: 7, version: "1.7.0_80", allowLegacyJava: true, wantMinimum: "8"},
		{name: "java 21 always accepted", major: 21, version: "21.0.1", allowLegacyJava: false, wantAccepted: true},
	}

	home := filepath.Join(string(filepath.Separator), "opt", "jdk")
	javaBin := filepath.Join(home, "bin", "java")

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jdk, err := probeResolvedJDKWithProbes(
				context.Background(),
				javaBin,
				home,
				JDKSourceSpecifiedHome,
				time.Second,
				tc.allowLegacyJava,
				func(context.Context, string, time.Duration) (string, int, error) {
					return tc.version, tc.major, nil
				},
				func(context.Context, string, time.Duration) (string, error) {
					return home, nil
				},
			)
			if tc.wantAccepted {
				if err != nil {
					t.Fatalf("probeResolvedJDKWithProbes() error = %v, want accepted", err)
				}
				if jdk.Major != tc.major {
					t.Fatalf("resolved major = %d, want %d", jdk.Major, tc.major)
				}
				return
			}
			if err == nil {
				t.Fatal("probeResolvedJDKWithProbes() succeeded, want an unsupported-version error")
			}
			// The message must name the floor that actually applied, so the
			// operator can tell which policy rejected the runtime.
			if !strings.Contains(err.Error(), "version "+tc.wantMinimum+" or newer") {
				t.Fatalf("error = %q, want it to name minimum %s", err.Error(), tc.wantMinimum)
			}
		})
	}
}

func TestResolveInternalJDKPassesLegacyOptInToProbe(t *testing.T) {
	requireShellFixture(t)
	legacyHome := makeFakeJDK(t, "legacy", "1.8.0_351", "java")
	t.Setenv("JAVA_HOME", "")
	t.Setenv("JRE_HOME", "")
	t.Setenv("PATH", "")

	var observed []bool
	_, err := resolveInternalJDKWithProbe(
		context.Background(),
		ResolveOptions{
			DefaultHome:     legacyHome,
			AllowLegacyJava: true,
		},
		time.Second,
		func(_ context.Context, _ string, _ string, _ JDKSource, _ time.Duration, allowLegacyJava bool) (ResolvedJDK, error) {
			observed = append(observed, allowLegacyJava)
			return ResolvedJDK{}, errors.New("probe refused")
		},
	)
	if err == nil {
		t.Fatal("resolveInternalJDKWithProbe() succeeded, want the probe failure")
	}
	if len(observed) == 0 {
		t.Fatal("probe was never called")
	}
	for index, allowLegacyJava := range observed {
		if !allowLegacyJava {
			t.Fatalf("probe call %d received allowLegacyJava=false, want the opt-in propagated", index)
		}
	}
	// The aggregate failure must quote the effective floor, not the constant.
	if !strings.Contains(err.Error(), "Java 8+") {
		t.Fatalf("error = %q, want it to name the legacy floor", err.Error())
	}
}
