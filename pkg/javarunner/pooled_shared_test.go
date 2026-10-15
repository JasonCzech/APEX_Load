package javarunner

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSharedJVMResourceProfileBounds(t *testing.T) {
	p, ok := SharedJVMResourceProfile(250, 1<<30)
	if !ok || p.SlotsPerHost != 250 || p.HeapBytes != 1<<30 || p.CaptureBytes < 576<<10 {
		t.Fatalf("profile: %+v %v", p, ok)
	}
	total, ok := SharedJVMHostMemoryBytes(p)
	if !ok || total <= p.HeapBytes {
		t.Fatalf("host memory %d does not exceed heap", total)
	}
	for _, tc := range []struct {
		slots int
		heap  int64
	}{{1, 1 << 30}, {MaxPooledSlotsPerHost + 1, 1 << 30}, {250, 1 << 20}, {250, 2 << 40}} {
		if _, ok := SharedJVMResourceProfile(tc.slots, tc.heap); ok {
			t.Fatalf("invalid profile accepted: %+v", tc)
		}
	}
	if p, ok := SharedJVMResourceProfile(MaxPooledSlotsPerHost, 4<<30); !ok || p.SlotsPerHost != MaxPooledSlotsPerHost {
		t.Fatal("maximum slots rejected")
	}
}

func TestSharedJVMArgumentsReplaceHeapSizing(t *testing.T) {
	args, dropped := SharedJVMArguments([]string{"-Xms256m", "-Dlabel=x", "-Xmx512m", "-XX:MaxRAMPercentage=50", "-XX:+UseG1GC", "-Xss1m", "-XX:InitialHeapSize=1g", "-Xsoftmx1g"}, 1536<<20)
	want := []string{"-Dlabel=x", "-XX:+UseG1GC", "-Xss1m", "-Xmx1536m"}
	if !dropped || !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%v dropped=%v", args, dropped)
	}
	if heap, ok := pooledMaxHeap(args); !ok || heap != 1536<<20 {
		t.Fatalf("launcher heap parse %d %v", heap, ok)
	}
	args, dropped = SharedJVMArguments(nil, 64<<20+512<<10)
	if dropped || !reflect.DeepEqual(args, []string{"-Xmx66048k"}) {
		t.Fatalf("args=%v dropped=%v", args, dropped)
	}
	if args, _ := SharedJVMArguments(nil, 64<<20+1); args[0] != "-Xmx67108865" {
		t.Fatalf("byte heap %v", args)
	}
}

func TestSplitSharedClasspathKeepsTargetPrivate(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, entries ...qualificationJarEntry) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, qualificationJarBytes(t, entries), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	script := write("script.jar", qualificationJarEntry{"example/Actions.class", "class"}, qualificationJarEntry{"example/Helper.class", "class"})
	library := write("library.jar", qualificationJarEntry{"lib/Client.class", "class"})
	private, shared, reason := SplitSharedClasspath([]string{library, script}, "example.Actions", false)
	if reason != "" || !reflect.DeepEqual(private, []string{script}) || !reflect.DeepEqual(shared, []string{library}) {
		t.Fatalf("split private=%v shared=%v reason=%q", private, shared, reason)
	}
	private, shared, reason = SplitSharedClasspath([]string{library, script}, "example.Actions", true)
	if reason != "" || !reflect.DeepEqual(private, []string{library, script}) || len(shared) != 0 {
		t.Fatalf("dedicated loader split private=%v shared=%v", private, shared)
	}
	nested := write("nested.jar", qualificationJarEntry{"example/Outer$Inner.class", "class"})
	if private, _, reason := SplitSharedClasspath([]string{nested}, "example.Outer$Inner", false); reason != "" || len(private) != 1 {
		t.Fatalf("nested target: %v %q", private, reason)
	}
	notJar := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(notJar, []byte("text"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		entries []string
		target  string
	}{
		"directory":      {[]string{dir}, "example.Actions"},
		"not a jar":      {[]string{notJar}, "example.Actions"},
		"target missing": {[]string{library}, "example.Actions"},
		"empty":          {nil, "example.Actions"},
		"bad target":     {[]string{script}, "example/Actions"},
		"missing file":   {[]string{filepath.Join(dir, "absent.jar")}, "example.Actions"},
	} {
		if _, _, reason := SplitSharedClasspath(tc.entries, tc.target, false); reason != SharedJVMReasonClasspath {
			t.Fatalf("%s: reason %q", name, reason)
		}
	}
}

func TestQualifySharedAutomatically(t *testing.T) {
	clean := NativeInspection{Complete: true, Assessment: "no_static_indicators"}
	request := qualificationFixture(t)
	selection, reason, err := QualifySharedAutomatically(context.Background(), request, clean)
	if err != nil || reason != "" {
		t.Fatalf("clean workload refused: %q %v", reason, err)
	}
	d := selection.Decision()
	digest, err := InspectPooledQualification(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	total, _ := request.Resources.memoryBytes()
	if d.Mode != SharedJVMMode || d.QualificationProfile != SharedJVMProfileID || d.ConfigurationSHA256 != digest || d.SlotsPerHost != request.Resources.SlotsPerHost || d.HostMemoryBytes != total || d.TargetClass != request.Launch.TargetClass {
		t.Fatalf("decision %+v", d)
	}
	provider, err := selection.LaunchProvider()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := provider(context.Background(), HostAllocation{ID: "host", SlotIDs: []string{"a"}, MemoryBytes: total, Binding: HostBinding{ConfigurationSHA256: digest}})
	if err != nil || spec.ConfigurationSHA256 != digest {
		t.Fatalf("launch spec %v", err)
	}
	// The selection owns copies: caller mutation cannot redirect a launch.
	request.Launch.ScriptClasspath[0] = "/elsewhere.jar"
	if spec, err = provider(context.Background(), HostAllocation{ID: "host", SlotIDs: []string{"a"}, MemoryBytes: total, Binding: HostBinding{ConfigurationSHA256: digest}}); err != nil || spec.ScriptClasspath[0] == "/elsewhere.jar" {
		t.Fatalf("selection aliased caller request: %v", err)
	}

	for name, tc := range map[string]struct {
		mutate func(*PooledQualificationRequest)
		native NativeInspection
		want   string
	}{
		"java 11":           {func(r *PooledQualificationRequest) { r.Launch.JDK.Major = 11 }, clean, SharedJVMReasonRuntime},
		"unknown vm":        {func(r *PooledQualificationRequest) { r.Launch.JDK.VMFamily = "" }, clean, SharedJVMReasonRuntime},
		"native findings":   {nil, NativeInspection{Complete: true, Assessment: "native_indicators_found", Findings: []NativeFinding{{Kind: "packaged_native_library"}}}, SharedJVMReasonNative},
		"incomplete scan":   {nil, NativeInspection{Assessment: "incomplete"}, SharedJVMReasonIncomplete},
		"one slot":          {func(r *PooledQualificationRequest) { r.Resources.SlotsPerHost = 1 }, clean, SharedJVMReasonCapacity},
		"heap mismatch":     {func(r *PooledQualificationRequest) { r.Launch.VMParameters = []string{"-Xmx64m"} }, clean, SharedJVMReasonInputs},
		"uncontained":       {func(r *PooledQualificationRequest) { r.Launch.ContainmentBackend = "windows_job" }, clean, SharedJVMReasonInputs},
		"unqualified clean": {func(r *PooledQualificationRequest) { r.Launch.QualifiedCleanup = false }, clean, SharedJVMReasonInputs},
		"missing script jar": {func(r *PooledQualificationRequest) {
			r.Launch.ScriptClasspath = []string{r.Launch.WorkDir + "/absent.jar"}
		}, clean, SharedJVMReasonInputs},
	} {
		r := qualificationFixture(t)
		if tc.mutate != nil {
			tc.mutate(&r)
		}
		selection, reason, err := QualifySharedAutomatically(context.Background(), r, tc.native)
		if err != nil || reason != tc.want || selection.Decision().Mode != "dedicated" {
			t.Fatalf("%s: reason=%q err=%v mode=%s", name, reason, err, selection.Decision().Mode)
		}
		if _, err := selection.LaunchProvider(); err == nil {
			t.Fatalf("%s: refused selection can launch", name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := QualifySharedAutomatically(ctx, qualificationFixture(t), clean); err == nil {
		t.Fatal("cancelled qualification succeeded")
	}
}
