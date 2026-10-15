package javasidecar

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

// minimalClassBytes is a structurally valid class file with no members, so the
// static native scan parses it and finds nothing.
var minimalClassBytes = []byte{0xca, 0xfe, 0xba, 0xbe, 0, 0, 0, 52, 0, 1, 0, 0x21, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

func writeTestJar(t *testing.T, path string, entries map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

type sharedJVMFixture struct {
	dir, script, library, native string
	server                       *Server
	client                       *Client
}

func newSharedJVMFixture(t *testing.T, major int) *sharedJVMFixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "jdk")
	for _, d := range []string{"bin", "lib"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{filepath.Join(home, "bin", "java"): "launcher", filepath.Join(home, "release"): "release", filepath.Join(home, "lib", "modules"): "modules", filepath.Join(dir, "harness.jar"): "harness"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	f := &sharedJVMFixture{dir: dir}
	stubSharedJVMContainerMemory(t, 0, false)
	api := writeTestJar(t, filepath.Join(dir, config.DefaultJavaVuserSlotAPIJarName), map[string][]byte{"apexload/host/PooledMain.class": minimalClassBytes})
	shim := writeTestJar(t, filepath.Join(dir, config.DefaultJavaVuserSlotShimJarName), map[string][]byte{"lrapi/lr.class": minimalClassBytes})
	f.script = writeTestJar(t, filepath.Join(dir, "script.jar"), map[string][]byte{"example/Actions.class": minimalClassBytes})
	f.library = writeTestJar(t, filepath.Join(dir, "library.jar"), map[string][]byte{"lib/Client.class": minimalClassBytes})
	f.native = writeTestJar(t, filepath.Join(dir, "native.jar"), map[string][]byte{"lib/libnative.so": []byte("elf")})
	operator := config.JavaVuserOperatorConfig{Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 4, HarnessJar: filepath.Join(dir, "harness.jar"), WorkDir: dir,
		SharedJVM: config.JavaVuserSharedJVMConfig{Enabled: true, VUsersPerJVM: 100, HeapMB: 256, DedicatedJVMMemoryMB: 128, SlotAPIJar: api, SlotShimJar: shim}}
	policy, admission, err := NewSharedJVMPolicy(operator)
	if err != nil {
		t.Fatal(err)
	}
	f.server = NewServer(ServerOptions{
		Operator: operator, HostAdmission: admission, SharedJVM: policy,
		CredentialCheck:                  func() error { return nil },
		supportsStrongProcessContainment: func() bool { return true },
		resolveJDK: func(context.Context, javarunner.ResolveOptions) (javarunner.ResolvedJDK, error) {
			return javarunner.ResolvedJDK{Home: home, JavaBin: filepath.Join(home, "bin", "java"), Version: "21-fixture", Major: major, VMFamily: javarunner.JavaVMHotSpot}, nil
		},
	})
	httpServer := httptest.NewServer(f.server)
	t.Cleanup(httpServer.Close)
	f.client, err = NewClientWithToken(httpServer.URL, testSidecarAuthToken, httpServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.ClaimOwner(context.Background(), "owner"); err != nil {
		t.Fatal(err)
	}
	return f
}

func sharedClaim(classpath ...string) SharedJVMClaim {
	return SharedJVMClaim{Runtime: RuntimeSettings{Classpath: classpath, AdditionalVMParameters: []string{"-Xmx512m", "-Dmode=test"}}, TargetClass: "example.Actions", ScriptSHA256: strings.Repeat("a", 64)}
}

func TestNewSharedJVMPolicyValidatesOperatorConfig(t *testing.T) {
	f := newSharedJVMFixture(t, 21)
	base := f.server.operator
	budget := f.server.hostAdmission.Budget()
	profile, _ := javarunner.SharedJVMResourceProfile(100, 256<<20)
	host, _ := javarunner.SharedJVMHostMemoryBytes(profile)
	if budget.MaxHosts != 4 || budget.MaxVUsers != 400 || budget.MaxMemoryBytes != 4*host {
		t.Fatalf("derived budget %+v", budget)
	}
	explicit := base
	explicit.SharedJVM.MemoryBudgetMB = 8192
	if _, admission, err := NewSharedJVMPolicy(explicit); err != nil || admission.Budget().MaxMemoryBytes != 8192<<20 {
		t.Fatalf("explicit budget: %v", err)
	}
	for name, mutate := range map[string]func(*config.JavaVuserOperatorConfig){
		"policy off":         func(o *config.JavaVuserOperatorConfig) { o.SharedJVM.Enabled = false },
		"runner off":         func(o *config.JavaVuserOperatorConfig) { o.Enabled = false },
		"no auth":            func(o *config.JavaVuserOperatorConfig) { o.AuthToken = "" },
		"relative api jar":   func(o *config.JavaVuserOperatorConfig) { o.SharedJVM.SlotAPIJar = "api.jar" },
		"missing shim jar":   func(o *config.JavaVuserOperatorConfig) { o.SharedJVM.SlotShimJar = filepath.Join(f.dir, "absent.jar") },
		"one slot":           func(o *config.JavaVuserOperatorConfig) { o.SharedJVM.VUsersPerJVM = 1 },
		"budget below a jvm": func(o *config.JavaVuserOperatorConfig) { o.SharedJVM.MemoryBudgetMB = 64 },
	} {
		o := base
		mutate(&o)
		if _, _, err := NewSharedJVMPolicy(o); !errors.Is(err, ErrSharedJVMConfig) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
}

func stubSharedJVMContainerMemory(t *testing.T, limit int64, ok bool) {
	t.Helper()
	previous := sharedJVMContainerMemory
	sharedJVMContainerMemory = func() (int64, bool) { return limit, ok }
	t.Cleanup(func() { sharedJVMContainerMemory = previous })
}

// A derived budget must fit the runner's container: four 100-VUser JVMs do not
// fit in 1 GiB, so admission stops at what the container can hold.
func TestSharedJVMDerivedBudgetFitsTheContainer(t *testing.T) {
	f := newSharedJVMFixture(t, 21)
	stubSharedJVMContainerMemory(t, 1<<30, true)
	_, admission, err := NewSharedJVMPolicy(f.server.operator)
	if err != nil || admission.Budget().MaxMemoryBytes != 1<<30-sharedJVMRunnerReserveBytes {
		t.Fatalf("container-limited budget %+v %v", admission, err)
	}
	explicit := f.server.operator
	explicit.SharedJVM.MemoryBudgetMB = 8192
	if _, admission, err := NewSharedJVMPolicy(explicit); err != nil || admission.Budget().MaxMemoryBytes != 8192<<20 {
		t.Fatalf("explicit budget was capped: %v", err)
	}
	// A container too small for one shared JVM refuses the policy at startup.
	stubSharedJVMContainerMemory(t, 512<<20, true)
	if _, _, err := NewSharedJVMPolicy(f.server.operator); !errors.Is(err, ErrSharedJVMConfig) {
		t.Fatalf("undersized container accepted: %v", err)
	}
}

func TestSharedJVMClaimQualifiesAndFreezesDecision(t *testing.T) {
	f := newSharedJVMFixture(t, 21)
	claim := sharedClaim(f.library, f.script)
	claim.VUsersPerJVM = 40
	run, warnings, err := f.client.OpenSharedClientRun(context.Background(), SharedClientOptions{ExecutionID: "execution", GroupID: "group", Claim: claim})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = run.Close(ctx)
	}()
	d := run.Decision()
	if d.Mode != javarunner.SharedJVMMode || d.ProfileID != javarunner.SharedJVMProfileID || d.SlotsPerHost != 40 || !d.InvocationEnabled || d.ScriptSHA256 != claim.ScriptSHA256 || d.TargetClass != "example.Actions" {
		t.Fatalf("decision %+v", d)
	}
	if len(warnings) != 1 || warnings[0] != WarningSharedJVMHeapIgnored {
		t.Fatalf("warnings %v", warnings)
	}
	runs := f.server.selectedPooledRuns(nil)
	if len(runs) != 1 {
		t.Fatalf("runs %d", len(runs))
	}
	// The launch keeps the target jar private, shares the library and replaces
	// the script heap with the operator's while keeping other VM tokens.
	spec := sharedLaunchSpec(t, f, claim)
	if len(spec.ScriptClasspath) != 1 || spec.ScriptClasspath[0] != f.script || len(spec.SharedClasspath) != 1 || spec.SharedClasspath[0] != f.library {
		t.Fatalf("classpath split private=%v shared=%v", spec.ScriptClasspath, spec.SharedClasspath)
	}
	if strings.Join(spec.VMParameters, " ") != "-Dmode=test -Xmx256m" {
		t.Fatalf("vm parameters %v", spec.VMParameters)
	}
	if _, _, err := f.client.OpenSharedClientRun(context.Background(), SharedClientOptions{ExecutionID: "execution", GroupID: "group", Claim: claim}); err == nil {
		t.Fatal("same execution/group reopened in one epoch")
	}
}

// sharedLaunchSpec re-runs the server's selection to expose its frozen launch.
func sharedLaunchSpec(t *testing.T, f *sharedJVMFixture, claim SharedJVMClaim) javarunner.PooledLaunchSpec {
	t.Helper()
	selection, _, reason, err := f.server.sharedJVMSelection(context.Background(), claim)
	if err != nil || reason != "" {
		t.Fatalf("selection %q %v", reason, err)
	}
	provider, err := selection.LaunchProvider()
	if err != nil {
		t.Fatal(err)
	}
	d := selection.Decision()
	spec, err := provider(context.Background(), javarunner.HostAllocation{ID: "host", SlotIDs: []string{"a"}, MemoryBytes: d.HostMemoryBytes, Binding: javarunner.HostBinding{ConfigurationSHA256: d.ConfigurationSHA256}})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestSharedJVMClaimRefusalsKeepWorkloadDedicated(t *testing.T) {
	for name, tc := range map[string]struct {
		major int
		claim func(*sharedJVMFixture) SharedJVMClaim
		want  string
	}{
		"java 11":         {11, func(f *sharedJVMFixture) SharedJVMClaim { return sharedClaim(f.script) }, javarunner.SharedJVMReasonRuntime},
		"native library":  {21, func(f *sharedJVMFixture) SharedJVMClaim { return sharedClaim(f.script, f.native) }, javarunner.SharedJVMReasonNative},
		"class directory": {21, func(f *sharedJVMFixture) SharedJVMClaim { return sharedClaim(f.dir) }, javarunner.SharedJVMReasonClasspath},
		"unresolvable": {21, func(f *sharedJVMFixture) SharedJVMClaim {
			return sharedClaim(filepath.Join(f.dir, "absent.jar"))
		}, "shared_jvm_runtime_rejected"},
		"target absent": {21, func(f *sharedJVMFixture) SharedJVMClaim { return sharedClaim(f.library) }, javarunner.SharedJVMReasonClasspath},
		"vm agent": {21, func(f *sharedJVMFixture) SharedJVMClaim {
			c := sharedClaim(f.script)
			c.Runtime.AdditionalVMParameters = []string{"-javaagent:x.jar"}
			return c
		}, "shared_jvm_vm_parameter_invalid"},
		"jdk home bogus": {21, func(f *sharedJVMFixture) SharedJVMClaim {
			c := sharedClaim(f.script)
			c.Runtime.JDKSelection = "specified"
			return c
		}, "jdk_home_required"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newSharedJVMFixture(t, tc.major)
			_, _, err := f.client.OpenSharedClientRun(context.Background(), SharedClientOptions{ExecutionID: "execution", GroupID: "group", Claim: tc.claim(f)})
			var remote *RemoteError
			if !errors.As(err, &remote) || remote.Code != tc.want || !SharedJVMClaimErrorCode(remote.Code) {
				t.Fatalf("err=%v, want %s", err, tc.want)
			}
			if len(f.server.selectedPooledRuns(nil)) != 0 {
				t.Fatal("refused claim opened a run")
			}
		})
	}
}

func TestSharedJVMClaimDisabledAndValidation(t *testing.T) {
	_, c, _ := controlServerFixture(t)
	_, _, err := c.OpenSharedClientRun(context.Background(), SharedClientOptions{ExecutionID: "execution", GroupID: "group", Claim: sharedClaim("/x.jar")})
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != SharedJVMDisabled {
		t.Fatalf("disabled runner: %v", err)
	}
	f := newSharedJVMFixture(t, 21)
	for name, claim := range map[string]SharedJVMClaim{
		"bad target": {TargetClass: "a/b", ScriptSHA256: strings.Repeat("a", 64)},
		"bad digest": {TargetClass: "a.B", ScriptSHA256: "abc"},
		"one slot":   {TargetClass: "a.B", ScriptSHA256: strings.Repeat("a", 64), VUsersPerJVM: 1},
		"too many":   {TargetClass: "a.B", ScriptSHA256: strings.Repeat("a", 64), VUsersPerJVM: javarunner.MaxPooledSlotsPerHost + 1},
		"label ctrl": {TargetClass: "a.B", ScriptSHA256: strings.Repeat("a", 64), GroupName: "a\nb"},
		"label long": {TargetClass: "a.B", ScriptSHA256: strings.Repeat("a", 64), ScenarioID: strings.Repeat("s", 257)},
	} {
		if _, _, err := f.client.OpenSharedClientRun(context.Background(), SharedClientOptions{ExecutionID: "execution", GroupID: "group", Claim: claim}); err == nil || errors.As(err, &remote) {
			t.Fatalf("%s: invalid claim reached the runner: %v", name, err)
		}
	}
	bad := PooledControlRequest{Operation: "claim", RequestID: "claim-x", ProfileID: "pilot", ExecutionID: "e", GroupID: "g", Shared: &SharedJVMClaim{TargetClass: "a.B", ScriptSHA256: strings.Repeat("a", 64)}}
	if validPooledControlRequest(bad) {
		t.Fatal("profile claim carried a shared claim")
	}
}

func TestSharedJVMCapabilitiesReflectBudgetAndDescriptors(t *testing.T) {
	f := newSharedJVMFixture(t, 21)
	caps := f.server.sharedJVMCapabilities()
	if caps == nil || caps.VUsersPerJVM != 100 || caps.MaxJVMs != 4 || caps.MaxVUsers != 400 || caps.AvailableVUsers != 400 || caps.JVMMemoryBytes <= 256<<20 {
		t.Fatalf("capabilities %+v", caps)
	}
	f.server.captureMu.Lock()
	f.server.captureDescriptorLimit, f.server.captureDescriptors = 150, 100
	f.server.captureMu.Unlock()
	if caps = f.server.sharedJVMCapabilities(); caps.MaxVUsers != 150 || caps.AvailableVUsers != 50 {
		t.Fatalf("descriptor-limited capabilities %+v", caps)
	}
	release, err := f.server.hostAdmission.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	f.server.captureMu.Lock()
	f.server.captureDescriptorLimit = 0
	f.server.captureMu.Unlock()
	// A dedicated JVM holds one of four process slots and part of the memory.
	if caps = f.server.sharedJVMCapabilities(); caps.AvailableVUsers != 300 {
		t.Fatalf("capabilities with a dedicated JVM %+v", caps)
	}
	if (&Server{}).sharedJVMCapabilities() != nil {
		t.Fatal("capabilities without a policy")
	}
}

func TestSharedJVMClaimFencedOwnerSkipsQualification(t *testing.T) {
	f := newSharedJVMFixture(t, 21)
	resolve := f.server.resolveJDK
	var calls int
	f.server.resolveJDK = func(ctx context.Context, o javarunner.ResolveOptions) (javarunner.ResolvedJDK, error) {
		calls++
		return resolve(ctx, o)
	}
	stale := &Client{baseURL: f.client.baseURL, httpClient: f.client.httpClient, authToken: f.client.authToken}
	stale.ownerID, stale.ownerEpoch = f.client.ownerID, f.client.ownerEpoch
	if _, err := f.client.ClaimOwner(context.Background(), "replacement"); err != nil {
		t.Fatal(err)
	}
	_, _, err := stale.OpenSharedClientRun(context.Background(), SharedClientOptions{ExecutionID: "execution", GroupID: "group", Claim: sharedClaim(f.script)})
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != javarunner.ErrHostFence.Error() {
		t.Fatalf("stale owner: %v", err)
	}
	if calls != 0 || len(f.server.selectedPooledRuns(nil)) != 0 {
		t.Fatalf("fenced claim qualified (%d JDK resolutions) or opened a run", calls)
	}
}
