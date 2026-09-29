package javasidecar

import (
	"context"
	"encoding/json"
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

func pilotManifestFixture(t *testing.T) PooledPilotManifest {
	t.Helper()
	p := controlProfileFixture(t)
	d := p.Selection.Decision()
	provider, err := p.Selection.LaunchProvider()
	if err != nil {
		t.Fatal(err)
	}
	launch, err := provider(context.Background(), javarunner.HostAllocation{MemoryBytes: d.HostMemoryBytes, SlotIDs: []string{"slot"}, Binding: javarunner.HostBinding{ConfigurationSHA256: d.ConfigurationSHA256}})
	if err != nil {
		t.Fatal(err)
	}
	evidence := strings.Repeat("e", 64)
	return PooledPilotManifest{Schema: PooledPilotManifestSchema, ScriptSHA256: strings.Repeat("d", 64), Budget: p.Budget, DedicatedMemoryBytes: d.HostMemoryBytes, AllowPreparation: true, AllowCommit: true, AllowInvocation: true,
		Qualification: javarunner.PooledQualificationProfile{Schema: javarunner.PooledQualificationSchema, ID: "pilot", ConfigurationSHA256: d.ConfigurationSHA256, Evidence: javarunner.PooledQualificationEvidence{ApprovedBy: "operator", InspectionSHA256: evidence, RepresentativeRunSHA256: evidence, LoaderChecksSHA256: evidence, CleanupChecksSHA256: evidence, NativeAssessment: "no_application_native_observed"}},
		Request:       javarunner.PooledQualificationRequest{Launch: launch, SourceArchive: filepath.Join(launch.WorkDir, "source.zip"), SharingProfile: "sharing", Resources: javarunner.PooledResourceProfile{ID: "resource", SlotsPerHost: 2, HeapBytes: 128 << 20, NativeBytes: 16 << 20, StackBytes: 8 << 20, MetaspaceBytes: 16 << 20, CodeCacheBytes: 16 << 20, CaptureBytes: 1 << 20}},
	}
}
func writePilotManifest(t *testing.T, m PooledPilotManifest) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pilot.json")
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestPooledPilotManifestBindsActualRunnerDecision(t *testing.T) {
	m := pilotManifestFixture(t)
	settings, err := LoadPooledPilotManifest(context.Background(), writePilotManifest(t, m), 2)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ScriptSHA256 != m.ScriptSHA256 {
		t.Fatal("script approval lost")
	}
	s := NewServer(ServerOptions{Operator: config.JavaVuserOperatorConfig{Enabled: true, AuthToken: testSidecarAuthToken, MaxProcesses: 2}, PooledControlProfiles: settings.Profiles, HostAdmission: settings.Admission})
	h := httptest.NewServer(s)
	defer h.Close()
	defer s.Shutdown(context.Background())
	c, err := NewClientWithToken(h.URL, testSidecarAuthToken, h.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ClaimOwner(context.Background(), "owner"); err != nil {
		t.Fatal(err)
	}
	claim := claimControl(t, c)
	if claim.Decision != settings.Decision {
		t.Fatal("manifest and wire decisions differ", claim.Decision, settings.Decision)
	}
	q := controlRequest(claim, "reserve", "reserve")
	q.VUsers = 4
	q.PrepareTimeoutMS = 1000
	allocation, err := c.PooledControl(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, err := settings.Admission.Acquire(ctx); err == nil {
		release()
		t.Fatal("dedicated bypassed shared budget")
	}
	q = controlRequest(claim, "abort", "abort")
	q.ReservationID = allocation.ReservationID
	if _, err = c.PooledControl(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	release, err := settings.Admission.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}
func TestPooledPilotManifestRejectsInvalidApproval(t *testing.T) {
	base := pilotManifestFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*PooledPilotManifest)
	}{
		{"schema", func(m *PooledPilotManifest) { m.Schema = "old" }},
		{"script", func(m *PooledPilotManifest) { m.ScriptSHA256 = strings.Repeat("A", 64) }},
		{"digest", func(m *PooledPilotManifest) { m.Qualification.ConfigurationSHA256 = strings.Repeat("f", 64) }},
		{"native_unknown", func(m *PooledPilotManifest) { m.Qualification.Evidence.NativeAssessment = "unknown" }},
		{"host_ceiling", func(m *PooledPilotManifest) { m.Budget.MaxHosts = 3 }},
		{"vuser_ceiling", func(m *PooledPilotManifest) { m.Budget.MaxVUsers = 1 }},
		{"memory", func(m *PooledPilotManifest) { m.Budget.MaxMemoryBytes = 1 }},
		{"dedicated_charge", func(m *PooledPilotManifest) { m.DedicatedMemoryBytes = 0 }},
		{"preparation_required", func(m *PooledPilotManifest) { m.AllowPreparation = false }},
		{"commit_required", func(m *PooledPilotManifest) { m.AllowCommit = false }},
		{"single_slot", func(m *PooledPilotManifest) { m.Request.Resources.SlotsPerHost = 1 }},
		{"unsupported_runtime", func(m *PooledPilotManifest) { m.Request.Launch.JDK.Major = 8 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.change(&m)
			if _, err := LoadPooledPilotManifest(context.Background(), writePilotManifest(t, m), 2); !errors.Is(err, ErrPooledPilotManifest) {
				t.Fatal(err)
			}
		})
	}
	path := writePilotManifest(t, base)
	if err := os.WriteFile(base.Request.SourceArchive, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPooledPilotManifest(context.Background(), path, 2); err == nil {
		t.Fatal("changed assets accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadPooledPilotManifest(ctx, path, 2); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestPooledPilotManifestBoundedStrictFile(t *testing.T) {
	for _, data := range []string{`{}`, `{"schema":"a","SCHEMA":"b"}`, `{"schema":"a","ſchema":"b"}`, `{"unexpected":true}`, `{} {}`, strings.Repeat(" ", maxPooledPilotManifestBytes+1), `{"request":{"Launch":{"TargetClass":"A","targetclass":"B"}}}`} {
		path := filepath.Join(t.TempDir(), "manifest")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPooledPilotManifest(context.Background(), path, 2); err == nil {
			t.Fatal("invalid document accepted")
		}
	}
	for _, path := range []string{"relative.json", t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		if _, err := LoadPooledPilotManifest(context.Background(), path, 2); err == nil {
			t.Fatal("invalid file accepted")
		}
	}
	for _, tc := range []struct {
		data  string
		valid bool
	}{
		{`{"x":[{}, {"a":1}],"y":null}`, true}, {`{"x":{"a":1,"a":2}}`, false}, {`[1,2]`, true}, {`{}x`, false}, {strings.Repeat("[", 34) + strings.Repeat("]", 34), false},
	} {
		if uniquePilotJSONKeys([]byte(tc.data)) != tc.valid {
			t.Fatal(tc)
		}
	}
}

func TestPooledPilotManifestRejectsUnknownFieldsOnOtherwiseValidApproval(t *testing.T) {
	m := pilotManifestFixture(t)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{ "unknown":true,` + string(data[1:]),
		strings.Replace(string(data), `"budget":{`, `"budget":{"Unknown":0,`, 1),
		string(data) + ` {}`,
		strings.Replace(string(data), `"schema":`, `"SCHEMA":"`+PooledPilotManifestSchema+`","schema":`, 1),
	} {
		path := filepath.Join(t.TempDir(), "manifest.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadPooledPilotManifest(context.Background(), path, 2); err == nil {
			t.Fatal("ambiguous or unknown approval field accepted")
		}
	}
}
