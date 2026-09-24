package enterprisearchive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Fixture: sanitized runtime-profile twin of acc_wls_reg_1.zip (VuGen 24.3); see
// docs/SCRIPT_DEBUGGER_GUIDE.md. No customer data is included.
func TestAccRuntimeDiagnosticsProfile(t *testing.T) {
	files := map[string]string{}
	entries, err := os.ReadDir("testdata/acc_runtime_diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join("testdata/acc_runtime_diagnostics", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = string(body)
	}
	result, err := ConvertZip(buildSourceZip(t, files), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := result.Script.Config
	detail := cfg.Results.MessageDetail
	if detail == nil || detail.Mode != "sampled" || detail.SampleRate != 1 || detail.IncludeResources == nil || !*detail.IncludeResources || detail.MaxItems != 128 || detail.MaxItemBytes != 1<<20 || detail.MaxTotalBytes != 16<<20 {
		t.Fatalf("capture policy: %+v", detail)
	}
	if cfg.ErrorHandling.SnapshotOnError {
		t.Fatal("replay capture enabled independent error snapshots")
	}
	rows := result.Script.Metadata.Import.Preserved["runtime_setting_dispositions"].(map[string]map[string]map[string]RuntimeSettingDisposition)
	for file, sections := range selectedRuntimeSections(collectRuntimeSections(files), "default.cfg", ParseRuntimeCfg(files["default.cfg"])) {
		for section, keys := range sections {
			for key := range keys {
				r, ok := rows[file][section][key]
				if !ok || r.Note == "" && r.ParityID == "" {
					t.Fatalf("unexplained %s/%s/%s", file, section, key)
				}
				if r.Status == RuntimeSettingDispositionNonApplicable && r.Reason == "" {
					t.Fatalf("no reason: %s", key)
				}
			}
		}
	}
	for _, key := range []string{"HttpErrorsAsWarnings", "FailNonCriticalItem", "ResourcePageTimeoutIsWarning", "EnableChecks", "EnableIPCache", "EnableSnapshotsInReplay", "SaveSnapshotResources"} {
		if rows["default.cfg"]["WEB"][key].Status != RuntimeSettingDispositionMapped {
			t.Fatalf("%s: %+v", key, rows["default.cfg"]["WEB"][key])
		}
	}
	if row := rows["default.cfg"]["WEB"]["DisableAED"]; row.Status != RuntimeSettingDispositionCompatibilityOnly || row.Reason != "unverified_semantics" {
		t.Fatal(row)
	}
	for _, key := range []string{"JsForTransportRuntimeSize", "LogMaxResponseSize", "PrefetchPrerenderCallBackTimerIntervalMS"} {
		if row := rows["default.cfg"]["WEB"][key]; row.Status != RuntimeSettingDispositionNonApplicable || row.Reason != "disabled_feature" {
			t.Fatalf("%s: %+v", key, row)
		}
	}
	parity := result.Script.Metadata.Import.Preserved["runtime_settings_parity"].([]RuntimeSettingParityEntry)
	if runtimeParityEntryByID(t, parity, "runtime_replay_snapshots").Status != RuntimeSettingStatusPartial {
		t.Fatal("snapshot parity must disclose native bounds")
	}
}

func TestSnapshotPolicyPreservesFalseAndConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, cfg string
		want      bool
		mode      string
	}{
		{"explicit off", "EnableSnapshotsInReplay=0\nSaveSnapshotResources=0", true, "errors"},
		{"on", "EnableSnapshotsInReplay=1\nSaveSnapshotResources=0", true, "sampled"},
		{"missing parent", "SaveSnapshotResources=1", false, ""},
		{"invalid", "EnableSnapshotsInReplay=invalid\nSaveSnapshotResources=1", false, ""},
		{"conflict", "EnableSnapshotsInReplay=1\nenablesnapshotsinreplay=0\nSaveSnapshotResources=1", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := ParseRuntimeCfg("[WEB]\n" + tc.cfg)
			cfg := rc.ToScriptConfig()
			dst := &dsl.ScriptConfig{}
			mergeScriptConfig(dst, cfg)
			detail := dst.Results.MessageDetail
			if (detail != nil) != tc.want {
				t.Fatalf("policy: %+v", detail)
			}
			if detail == nil {
				return
			}
			if detail.Mode != tc.mode || detail.IncludeResources == nil || *detail.IncludeResources {
				t.Fatal(detail)
			}
			raw, _ := json.Marshal(dst)
			if !strings.Contains(string(raw), `"include_resources":false`) {
				t.Fatalf("false lost: %s", raw)
			}
			*detail.IncludeResources = true
			if *cfg.Results.MessageDetail.IncludeResources {
				t.Fatal("merge aliases source")
			}
		})
	}
}

func TestRuntimeInactiveRequiresValidEvidence(t *testing.T) {
	for _, parent := range []string{"", "invalid", "1", "0"} {
		sections := map[string]map[string]string{"WEB": {"EnableJsForTransport": parent, "JsForTransportRuntimeSize": "51200", "DisableAED": "0", "AedUtf8Values": "0", "ZlibHeadersInCompressedRequestBody": "0"}}
		rows := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{"default.cfg": sections}, "http")["default.cfg"]["WEB"]
		for _, key := range []string{"DisableAED", "AedUtf8Values", "ZlibHeadersInCompressedRequestBody"} {
			if rows[key].Status != RuntimeSettingDispositionCompatibilityOnly {
				t.Fatalf("%s incorrectly inactive", key)
			}
		}
		want := RuntimeSettingDispositionCompatibilityOnly
		if parent == "0" {
			want = RuntimeSettingDispositionNonApplicable
		}
		if rows["JsForTransportRuntimeSize"].Status != want {
			t.Fatalf("parent=%s: %+v", parent, rows["JsForTransportRuntimeSize"])
		}
	}
}

func TestRuntimeModemSpeedMissingSelectorRequiresReview(t *testing.T) {
	for _, key := range []string{"ModemSpeed", "CustomModemSpeed"} {
		sections := map[string]map[string]map[string]string{"default.cfg": {"ModemSpeed": {key: "128000"}}}
		row := classifyRuntimeSelectedSections(sections, "http")["default.cfg"]["ModemSpeed"][key]
		if row.Status != RuntimeSettingDispositionCompatibilityOnly {
			t.Fatal(row)
		}
	}
}

func TestSnapshotConflictsRemainIndependentAndReviewable(t *testing.T) {
	for _, tc := range []struct {
		name, cfg     string
		wantCapture   bool
		captureStatus RuntimeSettingDispositionStatus
	}{
		{"child conflict", "EnableSnapshotsInReplay=1\nSaveSnapshotResources=1\nsavesnapshotresources=0", true, RuntimeSettingDispositionMapped},
		{"parent conflict", "EnableSnapshotsInReplay=1\nenablesnapshotsinreplay=0\nSaveSnapshotResources=1", false, RuntimeSettingDispositionCompatibilityOnly},
		{"invalid parent", "EnableSnapshotsInReplay=unknown\nSaveSnapshotResources=1", false, RuntimeSettingDispositionCompatibilityOnly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := "[WEB]\n" + tc.cfg
			rc := ParseRuntimeCfg(content)
			policy := rc.ToScriptConfig().Results.MessageDetail
			if (policy != nil) != tc.wantCapture {
				t.Fatalf("capture policy lost: %+v", policy)
			}
			if policy != nil && (policy.Mode != "sampled" || policy.SampleRate != 1 || policy.IncludeResources != nil) {
				t.Fatalf("conflict guessed a policy: %+v", policy)
			}
			files := map[string]string{"default.cfg": content}
			rows := classifyRuntimeSelectedSections(collectRuntimeSections(files), "http")
			refineRuntimeSettingDispositions(rows, rc, files, "http")
			if row := rows["default.cfg"]["WEB"]["EnableSnapshotsInReplay"]; row.Status != tc.captureStatus {
				t.Fatalf("parent row: %+v", row)
			}
			if row := rows["default.cfg"]["WEB"]["SaveSnapshotResources"]; row.Status != RuntimeSettingDispositionCompatibilityOnly || row.Note == "" {
				t.Fatalf("unapplied child advertised as mapped: %+v", row)
			}
		})
	}
}
