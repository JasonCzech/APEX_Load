package javarunner

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func qualificationFixture(t *testing.T) PooledQualificationRequest {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "jdk")
	for _, d := range []string{"bin", "lib", "conf"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(home, "bin", "java"): "launcher", filepath.Join(home, "release"): "runtime-release",
		filepath.Join(home, "lib", "modules"): "jdk implementation", filepath.Join(home, "conf", "security"): "runtime configuration",
		filepath.Join(dir, "host.jar"): "harness", filepath.Join(dir, "shim.jar"): "private shim", filepath.Join(dir, "script.jar"): "script",
		filepath.Join(dir, "dependency.jar"): "shared dependency", filepath.Join(dir, "source.zip"): "original source",
	}
	for path, data := range files {
		payload := []byte(data)
		if strings.HasSuffix(path, ".jar") {
			payload = qualificationJarBytes(t, []qualificationJarEntry{{"payload.txt", data}})
		}
		if err := os.WriteFile(path, payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return PooledQualificationRequest{
		Launch:        PooledLaunchSpec{ContainmentBackend: "delegated_linux", JDK: ResolvedJDK{Home: home, JavaBin: filepath.Join(home, "bin", "java"), Version: "17-fixture", Major: 17, VMFamily: JavaVMHotSpot}, HarnessJar: filepath.Join(dir, "host.jar"), ShimJar: filepath.Join(dir, "shim.jar"), TargetClass: "example.Actions", WorkDir: dir, ScriptClasspath: []string{filepath.Join(dir, "script.jar")}, SharedClasspath: []string{filepath.Join(dir, "dependency.jar")}, VMParameters: []string{"-Xmx128m"}, QualifiedCleanup: true},
		SourceArchive: filepath.Join(dir, "source.zip"), SharingProfile: "synthetic-http-v1",
		Resources: PooledResourceProfile{ID: "test-resource-v1", SlotsPerHost: 4, HeapBytes: 128 << 20, NativeBytes: 32 << 20, StackBytes: 16 << 20, MetaspaceBytes: 32 << 20, CodeCacheBytes: 16 << 20, CaptureBytes: 4 << 20},
	}
}
func approvedFixture(t *testing.T, request PooledQualificationRequest) (*PooledQualificationRegistry, PooledQualificationProfile) {
	t.Helper()
	digest, err := InspectPooledQualification(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	evidence := strings.Repeat("e", 64)
	p := PooledQualificationProfile{Schema: PooledQualificationSchema, ID: "synthetic-http", ConfigurationSHA256: digest, Evidence: PooledQualificationEvidence{ApprovedBy: "operator-1", NativeAssessment: "no_application_native_observed", InspectionSHA256: evidence, RepresentativeRunSHA256: evidence, LoaderChecksSHA256: evidence, CleanupChecksSHA256: evidence}}
	registry, err := NewPooledQualificationRegistry([]PooledQualificationProfile{p})
	if err != nil {
		t.Fatal(err)
	}
	return registry, p
}
func TestPooledQualificationSelectAndFreeze(t *testing.T) {
	request := qualificationFixture(t)
	registry, p := approvedFixture(t, request)
	selection, err := registry.Select(context.Background(), p.ID, 4, request)
	if err != nil {
		t.Fatal(err)
	}
	d := selection.Decision()
	total, _ := request.Resources.memoryBytes()
	if d.TargetClass != request.Launch.TargetClass || d.Mode != "pooled_pilot" || d.FallbackReason != "" || d.SlotsPerHost != 4 || d.HostMemoryBytes != total || d.ConfigurationSHA256 != p.ConfigurationSHA256 || d.SharingProfile != request.SharingProfile || d.ContainmentBackend != "delegated_linux" {
		t.Fatal(d)
	}
	provider, err := selection.LaunchProvider()
	if err != nil {
		t.Fatal(err)
	}
	a := HostAllocation{ID: "host", SlotIDs: []string{"slot-a", "slot-b"}, MemoryBytes: total, Binding: HostBinding{ConfigurationSHA256: p.ConfigurationSHA256}}
	// Neither caller mutations nor returned values can alter the frozen selection.
	request.Launch.VMParameters[0] = "-Xmx256m"
	request.Launch.ScriptClasspath[0] = "missing"
	d.Mode = "dedicated"
	d.TargetClass = "Wrong"
	request.Launch.TargetClass = "Other"
	spec, err := provider(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if spec.TargetClass != "example.Actions" || selection.Decision().TargetClass != "example.Actions" || spec.VMParameters[0] != "-Xmx128m" || selection.Decision().Mode != "pooled_pilot" {
		t.Fatal("selection mutated")
	}
	spec.VMParameters[0] = "-Xmx512m"
	again, err := provider(context.Background(), a)
	if err != nil || again.VMParameters[0] != "-Xmx128m" {
		t.Fatal("returned spec aliased frozen state", err)
	}
	for _, mutate := range []func(*HostAllocation){func(a *HostAllocation) { a.MemoryBytes-- }, func(a *HostAllocation) { a.Binding.ConfigurationSHA256 = strings.Repeat("f", 64) }, func(a *HostAllocation) { a.SlotIDs = make([]string, 5) }, func(a *HostAllocation) { a.SlotIDs = nil }} {
		bad := a
		mutate(&bad)
		if _, err := provider(context.Background(), bad); !errors.Is(err, ErrHostIdentity) {
			t.Fatal("invalid allocation", err)
		}
	}
}
func TestPooledQualificationDedicatedFallbacks(t *testing.T) {
	request := qualificationFixture(t)
	registry, p := approvedFixture(t, request)
	cases := []struct {
		name, profile string
		slots         int
		mutate        func(*PooledQualificationRequest)
		reason        string
	}{
		{name: "default", profile: p.ID, slots: 1, reason: "dedicated_requested"},
		{name: "absent", profile: "missing", slots: 4, reason: "pooled_profile_missing"},
		{name: "invalid slots", profile: p.ID, slots: 0, reason: "pooled_capacity_invalid"},
		{name: "resource slots", profile: p.ID, slots: 2, reason: "pooled_resource_mismatch"},
		{name: "legacy", profile: p.ID, slots: 4, mutate: func(r *PooledQualificationRequest) { r.Launch.JDK.Major = 8 }, reason: "pooled_runtime_unqualified"},
		{name: "different arguments", profile: p.ID, slots: 4, mutate: func(r *PooledQualificationRequest) { r.Launch.VMParameters = append(r.Launch.VMParameters, "-Xms32m") }, reason: "pooled_profile_mismatch"},
		{name: "different target", profile: p.ID, slots: 4, mutate: func(r *PooledQualificationRequest) { r.Launch.TargetClass = "Other" }, reason: "pooled_profile_mismatch"},
		{name: "different sharing", profile: p.ID, slots: 4, mutate: func(r *PooledQualificationRequest) { r.SharingProfile = "another" }, reason: "pooled_profile_mismatch"},
		{name: "unqualified cleanup", profile: p.ID, slots: 4, mutate: func(r *PooledQualificationRequest) { r.Launch.QualifiedCleanup = false }, reason: "pooled_inputs_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := cloneQualificationRequest(request)
			if tc.mutate != nil {
				tc.mutate(&r)
			}
			s, err := registry.Select(context.Background(), tc.profile, tc.slots, r)
			if err != nil {
				t.Fatal(err)
			}
			d := s.Decision()
			if d.Mode != "dedicated" || d.SlotsPerHost != 1 || d.FallbackReason != tc.reason || d.ConfigurationSHA256 != "" {
				t.Fatal(d)
			}
			if _, err := s.LaunchProvider(); !errors.Is(err, ErrPooledQualification) {
				t.Fatal(err)
			}
		})
	}
	var absent *PooledQualificationRegistry
	s, err := absent.Select(context.Background(), p.ID, 4, request)
	if err != nil || s.Decision().FallbackReason != "pooled_profile_missing" {
		t.Fatal(s, err)
	}
}
func TestPooledQualificationRequiresOperatorEvidence(t *testing.T) {
	_, profile := approvedFixture(t, qualificationFixture(t))
	mutations := map[string]func(*PooledQualificationProfile){
		"unknown native": func(p *PooledQualificationProfile) { p.Evidence.NativeAssessment = "" },
		"schema":         func(p *PooledQualificationProfile) { p.Schema = "v0" }, "identity": func(p *PooledQualificationProfile) { p.ID = "" }, "digest": func(p *PooledQualificationProfile) { p.ConfigurationSHA256 = "bad" },
		"approval": func(p *PooledQualificationProfile) { p.Evidence.ApprovedBy = "" }, "inspection": func(p *PooledQualificationProfile) { p.Evidence.InspectionSHA256 = "" }, "execution": func(p *PooledQualificationProfile) { p.Evidence.RepresentativeRunSHA256 = "" }, "loader": func(p *PooledQualificationProfile) { p.Evidence.LoaderChecksSHA256 = "" }, "cleanup": func(p *PooledQualificationProfile) { p.Evidence.CleanupChecksSHA256 = "" }, "native": func(p *PooledQualificationProfile) { p.Evidence.NativeAssessment = "application_native_detected" },
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			p := profile
			change(&p)
			if _, err := NewPooledQualificationRegistry([]PooledQualificationProfile{p}); !errors.Is(err, ErrPooledQualification) {
				t.Fatal(err)
			}
		})
	}
	if _, err := NewPooledQualificationRegistry([]PooledQualificationProfile{profile, profile}); err == nil {
		t.Fatal("duplicate profile accepted")
	}
	if _, err := NewPooledQualificationRegistry(make([]PooledQualificationProfile, 1025)); err == nil {
		t.Fatal("unbounded registry")
	}
}
func TestPooledQualificationDetectsAssetDriftBeforeLaunch(t *testing.T) {
	for _, name := range []string{"runtime modules", "runtime configuration", "launcher", "source", "harness", "shim", "script", "dependency"} {
		t.Run(name, func(t *testing.T) {
			r := qualificationFixture(t)
			registry, p := approvedFixture(t, r)
			s, err := registry.Select(context.Background(), p.ID, 4, r)
			if err != nil {
				t.Fatal(err)
			}
			provider, err := s.LaunchProvider()
			if err != nil {
				t.Fatal(err)
			}
			paths := map[string]string{"runtime modules": filepath.Join(r.Launch.JDK.Home, "lib", "modules"), "runtime configuration": filepath.Join(r.Launch.JDK.Home, "conf", "security"), "launcher": r.Launch.JDK.JavaBin, "source": r.SourceArchive, "harness": r.Launch.HarnessJar, "shim": r.Launch.ShimJar, "script": r.Launch.ScriptClasspath[0], "dependency": r.Launch.SharedClasspath[0]}
			if err := os.WriteFile(paths[name], []byte("changed content"), 0600); err != nil {
				t.Fatal(err)
			}
			total, _ := r.Resources.memoryBytes()
			a := HostAllocation{SlotIDs: []string{"slot"}, MemoryBytes: total, Binding: HostBinding{ConfigurationSHA256: p.ConfigurationSHA256}}
			if _, err := provider(context.Background(), a); !errors.Is(err, ErrPooledQualificationChanged) {
				t.Fatal("drift admitted", err)
			}
			if s.Decision().Mode != "pooled_pilot" {
				t.Fatal("mode changed after selection")
			}
		})
	}
}
func TestPooledQualificationIdentityPreservesOrderAndBoundaries(t *testing.T) {
	r := qualificationFixture(t)
	r.Launch.ScriptClasspath = append(r.Launch.ScriptClasspath, r.Launch.SharedClasspath[0])
	r.Launch.VMParameters = append(r.Launch.VMParameters, "-Xms32m")
	original, err := InspectPooledQualification(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*PooledQualificationRequest){
		"classpath order": func(r *PooledQualificationRequest) {
			r.Launch.ScriptClasspath[0], r.Launch.ScriptClasspath[1] = r.Launch.ScriptClasspath[1], r.Launch.ScriptClasspath[0]
		},
		"argument order": func(r *PooledQualificationRequest) {
			r.Launch.VMParameters[0], r.Launch.VMParameters[1] = r.Launch.VMParameters[1], r.Launch.VMParameters[0]
		},
		"private shared boundary": func(r *PooledQualificationRequest) { r.Launch.ScriptClasspath = r.Launch.ScriptClasspath[:1] },
		"resources":               func(r *PooledQualificationRequest) { r.Resources.NativeBytes++ },
		"work directory":          func(r *PooledQualificationRequest) { r.Launch.WorkDir = r.Launch.JDK.Home },
	} {
		t.Run(name, func(t *testing.T) {
			copy := cloneQualificationRequest(r)
			change(&copy)
			digest, err := InspectPooledQualification(context.Background(), copy)
			if err != nil || digest == original {
				t.Fatal(digest, err)
			}
		})
	}
	r.Launch.ConfigurationSHA256 = "untrusted-caller-hash"
	digest, err := InspectPooledQualification(context.Background(), r)
	if err != nil || digest != original {
		t.Fatal("caller supplied hash affects verified identity", err)
	}
}
func TestPooledQualificationRejectsInvalidInputs(t *testing.T) {
	for name, change := range map[string]func(*PooledQualificationRequest){
		"malformed argument": func(r *PooledQualificationRequest) {
			r.Launch.VMParameters = append(r.Launch.VMParameters, "-Dmode="+string([]byte{255}))
		},
		"malformed runtime": func(r *PooledQualificationRequest) { r.Launch.JDK.Version = string([]byte{255}) },
		"ambiguous harness path": func(r *PooledQualificationRequest) {
			r.Launch.HarnessJar += " "
			if err := os.WriteFile(r.Launch.HarnessJar, []byte("different approved content"), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"unknown containment": func(r *PooledQualificationRequest) { r.Launch.ContainmentBackend = "" },
		"missing release": func(r *PooledQualificationRequest) {
			if err := os.Remove(filepath.Join(r.Launch.JDK.Home, "release")); err != nil {
				t.Fatal(err)
			}
		},
		"directory launcher": func(r *PooledQualificationRequest) {
			if err := os.Remove(r.Launch.JDK.JavaBin); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(r.Launch.JDK.JavaBin, 0700); err != nil {
				t.Fatal(err)
			}
		},
		"no source":      func(r *PooledQualificationRequest) { r.SourceArchive = "" },
		"native budget":  func(r *PooledQualificationRequest) { r.Resources.NativeBytes = 0 },
		"overflow":       func(r *PooledQualificationRequest) { r.Resources.OtherBytes = 1<<63 - 1 },
		"capture budget": func(r *PooledQualificationRequest) { r.Resources.CaptureBytes = 1 },
		"heap mismatch":  func(r *PooledQualificationRequest) { r.Resources.HeapBytes++ },
		"symlink": func(r *PooledQualificationRequest) {
			path := filepath.Join(r.Launch.WorkDir, "link.jar")
			if err := os.Symlink(r.SourceArchive, path); err != nil {
				t.Skip(err)
			}
			r.SourceArchive = path
		},
		"JDK symlink": func(r *PooledQualificationRequest) {
			if err := os.Symlink(r.SourceArchive, filepath.Join(r.Launch.JDK.Home, "linked.jar")); err != nil {
				t.Skip(err)
			}
		},
		"external launcher": func(r *PooledQualificationRequest) {
			path := filepath.Join(r.Launch.WorkDir, "java")
			if err := os.WriteFile(path, []byte("launcher"), 0600); err != nil {
				t.Fatal(err)
			}
			r.Launch.JDK.JavaBin = path
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := qualificationFixture(t)
			change(&r)
			if _, err := InspectPooledQualification(context.Background(), r); !errors.Is(err, ErrPooledQualification) {
				t.Fatal(err)
			}
		})
	}
}
func TestPooledQualificationCancellation(t *testing.T) {
	r := qualificationFixture(t)
	registry, p := approvedFixture(t, r)
	s, err := registry.Select(context.Background(), p.ID, 4, r)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := s.LaunchProvider()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectPooledQualification(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := registry.Select(ctx, p.ID, 4, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	total, _ := r.Resources.memoryBytes()
	if _, err := provider(ctx, HostAllocation{SlotIDs: []string{"slot"}, MemoryBytes: total, Binding: HostBinding{ConfigurationSHA256: p.ConfigurationSHA256}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPooledQualificationScanLimitsAndCancellation(t *testing.T) {
	r := qualificationFixture(t)
	scan := qualificationScanner{ctx: context.Background(), bytes: 4 << 30}
	if _, err := scan.file(r.SourceArchive); !errors.Is(err, ErrPooledQualification) {
		t.Fatal("aggregate limit", err)
	}
	path := filepath.Join(r.Launch.WorkDir, "oversized")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate((2 << 30) + 1)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	scan.bytes = 0
	if _, err := scan.file(path); !errors.Is(err, ErrPooledQualification) {
		t.Fatal("file size limit", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader := qualificationReader{ctx: ctx, reader: strings.NewReader("pending input")}
	cancel()
	if n, err := reader.Read(make([]byte, 20)); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal(n, err)
	}
}

func TestPooledQualificationRegistryCopiesOperatorProfiles(t *testing.T) {
	r := qualificationFixture(t)
	_, p := approvedFixture(t, r)
	profiles := []PooledQualificationProfile{p}
	registry, err := NewPooledQualificationRegistry(profiles)
	if err != nil {
		t.Fatal(err)
	}
	profiles[0].ConfigurationSHA256 = strings.Repeat("f", 64)
	s, err := registry.Select(context.Background(), p.ID, 4, r)
	if err != nil || s.Decision().Mode != "pooled_pilot" {
		t.Fatal(s.Decision(), err)
	}
}

func TestPooledQualificationDriftAbortsReservationBeforeStart(t *testing.T) {
	request := qualificationFixture(t)
	registry, p := approvedFixture(t, request)
	selection, err := registry.Select(context.Background(), p.ID, 4, request)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := selection.LaunchProvider()
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewLocalHostBackend(provider)
	if err != nil {
		t.Fatal(err)
	}
	backend.supports = func() bool { return true }
	backend.containmentBackend = "delegated_linux"
	backend.credentialCheck = func() error { return nil }
	backend.start = func(*exec.Cmd) error { t.Fatal("changed assets reached process start"); return nil }
	d := selection.Decision()
	ledger, err := NewHostLedger(HostBinding{OwnerID: "owner", OwnerEpoch: "epoch", ExecutionID: "run", GroupID: "group", ConfigurationSHA256: d.ConfigurationSHA256}, HostBudget{MaxHosts: 1, MaxVUsers: 4, MaxMemoryBytes: d.HostMemoryBytes}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := ledger.Reserve("epoch", 2, HostProfile{SlotsPerHost: d.SlotsPerHost, MemoryBytesPerHost: d.HostMemoryBytes}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	supervisor, err := NewHostSupervisor(ledger, backend, true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSupervisor(t, supervisor)
	if err := os.WriteFile(request.Launch.SharedClasspath[0], []byte("changed after reservation"), 0600); err != nil {
		t.Fatal(err)
	}
	contexts := map[string]map[string]string{}
	for _, slot := range hosts[0].SlotIDs {
		contexts[slot] = map[string]string{}
	}
	err = supervisor.Prepare(context.Background(), "epoch", hosts[0].ReservationID, contexts)
	if !errors.Is(err, ErrPooledQualificationChanged) || !errors.Is(err, ErrHostNotStarted) {
		t.Fatal(err)
	}
	allocations, usage := ledger.Snapshot()
	if usage != (HostUsage{}) || allocations[0].State != HostReaped {
		t.Fatal("definitely unstarted capacity retained", usage, allocations)
	}
	if selection.Decision().Mode != "pooled_pilot" {
		t.Fatal("silent dedicated fallback after reservation")
	}
}

func TestPooledQualificationConcurrentRevalidation(t *testing.T) {
	request := qualificationFixture(t)
	registry, p := approvedFixture(t, request)
	selection, err := registry.Select(context.Background(), p.ID, 4, request)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := selection.LaunchProvider()
	if err != nil {
		t.Fatal(err)
	}
	total, _ := request.Resources.memoryBytes()
	allocation := HostAllocation{SlotIDs: []string{"slot"}, MemoryBytes: total, Binding: HostBinding{ConfigurationSHA256: p.ConfigurationSHA256}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			spec, err := provider(context.Background(), allocation)
			if err != nil {
				t.Error(err)
				return
			}
			spec.VMParameters[0] = "mutated return"
		}()
	}
	wg.Wait()
}

func TestPooledQualificationBackendBindingEnforcedBeforeStart(t *testing.T) {
	request := qualificationFixture(t)
	registry, p := approvedFixture(t, request)
	selection, err := registry.Select(context.Background(), p.ID, 4, request)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := selection.LaunchProvider()
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewLocalHostBackend(provider)
	if err != nil {
		t.Fatal(err)
	}
	backend.supports = func() bool { return true }
	backend.credentialCheck = func() error { return nil }
	backend.containmentBackend = "windows_job"
	backend.start = func(*exec.Cmd) error { t.Fatal("wrong containment backend started"); return nil }
	d := selection.Decision()
	a := HostAllocation{ID: "host", SlotIDs: []string{"slot"}, MemoryBytes: d.HostMemoryBytes, Binding: HostBinding{ConfigurationSHA256: d.ConfigurationSHA256}}
	h, err := backend.Provision(context.Background(), a)
	if h != nil || !errors.Is(err, ErrStrongProcessContainmentUnavailable) || !errors.Is(err, ErrHostNotStarted) {
		t.Fatal(h, err)
	}
}

type qualificationJarEntry struct{ name, body string }

func qualificationJarBytes(t *testing.T, entries []qualificationJarEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		stream, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func TestPooledQualificationRejectsImplicitJarDependencies(t *testing.T) {
	for name, entries := range map[string][]qualificationJarEntry{
		"manifest reference": {{"META-INF/MANIFEST.MF", "Manifest-Version: 1.0\r\nClass-Path: hidden.jar\r\n\r\n"}},
		"folded reference":   {{"META-INF/MANIFEST.MF", "Manifest-Version: 1.0\nclass-path: \n hidden.jar\n\n"}},
		"jar index":          {{"META-INF/INDEX.LIST", "JarIndex-Version: 1.0\n\nhidden.jar\n"}},
		"duplicate manifest": {{"META-INF/MANIFEST.MF", "Manifest-Version: 1.0\n"}, {"META-INF/MANIFEST.MF", "Manifest-Version: 1.0\n"}},
		"oversized manifest": {{"META-INF/MANIFEST.MF", strings.Repeat("x", (64<<10)+1)}},
	} {
		t.Run(name, func(t *testing.T) {
			r := qualificationFixture(t)
			if err := os.WriteFile(r.Launch.SharedClasspath[0], qualificationJarBytes(t, entries), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := InspectPooledQualification(context.Background(), r); !errors.Is(err, ErrPooledQualification) {
				t.Fatal("implicit/unbounded layout admitted", err)
			}
		})
	}
	r := qualificationFixture(t)
	if err := os.WriteFile(r.Launch.HarnessJar, qualificationJarBytes(t, []qualificationJarEntry{{"META-INF/MANIFEST.MF", "Manifest-Version: 1.0\r\nMulti-Release: true\r\n\r\n"}}), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectPooledQualification(context.Background(), r); err != nil {
		t.Fatal("ordinary bounded manifest rejected", err)
	}
}
