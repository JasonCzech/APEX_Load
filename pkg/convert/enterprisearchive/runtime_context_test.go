package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestKeepAliveTimeoutImport(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		wantMS         int
		wantStatus     RuntimeSettingStatus
	}{
		{"seconds", "KeepAliveTimeout=60", 60000, RuntimeSettingStatusExecuted},
		{"upper boundary", "KeepAliveTimeout=3600", 3600000, RuntimeSettingStatusExecuted},
		{"over boundary", "KeepAliveTimeout=3601", 0, RuntimeSettingStatusPartial},
		{"archive value", "KeepAliveTimeout=6000", 0, RuntimeSettingStatusPartial},
		{"zero is not destination default", "KeepAliveTimeout=0", 0, RuntimeSettingStatusPartial},
		{"negative", "KeepAliveTimeout=-1", 0, RuntimeSettingStatusPartial},
		{"fractional", "KeepAliveTimeout=0.5", 0, RuntimeSettingStatusPartial},
		{"infinite", "KeepAliveTimeout=Inf", 0, RuntimeSettingStatusPartial},
		{"empty", "KeepAliveTimeout=", 0, RuntimeSettingStatusPartial},
		{"overflow", "KeepAliveTimeout=99999999999999999999999", 0, RuntimeSettingStatusPartial},
		{"conflict", "KeepAliveTimeout=60\nkeepalivetimeout=90", 0, RuntimeSettingStatusPartial},
		{"alias conflict", "KeepAliveTimeout=60\n[HTTP]\nKeepAliveTimeout=90", 0, RuntimeSettingStatusPartial},
		{"disabled", "KeepAlive=0\nKeepAliveTimeout=6000", 0, RuntimeSettingStatusNonApplicable},
		{"disabled conflict", "KeepAlive=0\nKeepAliveTimeout=60\nkeepalivetimeout=90", 0, RuntimeSettingStatusNonApplicable},
		{"ambiguous parent", "KeepAlive=1\nKeepAlive=0\nKeepAliveTimeout=60", 0, RuntimeSettingStatusPartial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := convertRuntimeContextFixture(t, "[WEB]\n"+tc.settings, "", `Action(){ web_url("page", "URL=https://example.test/", LAST); return 0; }`)
			if got := r.Script.Config.HTTP.IdleTimeoutMs; got != tc.wantMS {
				t.Fatalf("idle=%d, want %d", got, tc.wantMS)
			}
			parity := r.Script.Metadata.Import.Preserved["runtime_settings_parity"].([]RuntimeSettingParityEntry)
			entry := runtimeParityEntryByID(t, parity, "http_idle_timeout")
			if entry.Status != tc.wantStatus {
				t.Fatalf("parity=%#v, want %s", entry, tc.wantStatus)
			}
			if issues := dsl.ValidateScriptIssues(r.Script); len(issues) != 0 {
				t.Fatalf("validation=%v", issues)
			}
		})
	}
}

func TestRuntimeParentFeatureContext(t *testing.T) {
	for _, parent := range []string{"0", "false", "1", "", "invalid"} {
		t.Run(parent, func(t *testing.T) {
			section := map[string]string{"SPNCNameLookup": "1", "HeavyKDCLoad": "1", "UseNativeNTLM": "1", "SupportCharset": "None"}
			if parent != "" {
				section["IntegratedAuthentication"] = parent
			}
			rows := classifyRuntimeSelectedSections(map[string]map[string]map[string]string{"default.cfg": {"WEB": section}}, "http")["default.cfg"]["WEB"]
			want := RuntimeSettingDispositionCompatibilityOnly
			if parent == "0" || parent == "false" {
				want = RuntimeSettingDispositionNonApplicable
			}
			for _, key := range []string{"SPNCNameLookup", "HeavyKDCLoad", "UseNativeNTLM"} {
				if rows[key].Status != want {
					t.Fatalf("%s=%#v, want %s", key, rows[key], want)
				}
			}
			if rows["SupportCharset"].Status != RuntimeSettingDispositionNonApplicable {
				t.Fatal(rows["SupportCharset"])
			}
		})
	}
	for _, charset := range []string{"utf-8", "None", "UTF8", "windows-1252"} {
		got := activeWhenNotUTF8Charset(nil, charset)
		if got != (charset == "windows-1252") {
			t.Fatalf("charset %q active=%v", charset, got)
		}
	}
	sections := map[string]map[string]string{"WEB": {"IntegratedAuthentication": "0"}, "HTTP": {"IntegratedAuthentication": "1"}}
	if runtimeHTTPFeatureExplicitlyDisabled(sections, "IntegratedAuthentication") {
		t.Fatal("conflicting parents must remain reviewable")
	}
}

func TestConflictingParentDeclarationsRemainReviewable(t *testing.T) {
	r := convertRuntimeContextFixture(t, "[WEB]\nIntegratedAuthentication=1\nIntegratedAuthentication=0\nSPNCNameLookup=1", "[RunLogicRunRoot]\nRunLogicNumOfIterations=1\nRunLogicPaceType=Random\nRunLogicPaceType=Const\nRunLogicPaceConstTime=5\nRunLogicRandomPaceMin=2\nRunLogicRandomPaceMax=4", `Action(){ web_url("page", "URL=https://example.test/", LAST); }`)
	rows := r.Script.Metadata.Import.Preserved["runtime_setting_dispositions"].(map[string]map[string]map[string]RuntimeSettingDisposition)
	for _, row := range []RuntimeSettingDisposition{rows["default.cfg"]["WEB"]["SPNCNameLookup"], rows["default.usp"]["RunLogicRunRoot"]["RunLogicRandomPaceMin"]} {
		if row.Status != RuntimeSettingDispositionCompatibilityOnly || !strings.Contains(row.Note, "Conflicting") {
			t.Fatalf("ambiguous child marked inactive: %#v", row)
		}
	}
}

func TestPrefetchCallbackFollowsSelectedParent(t *testing.T) {
	for _, parent := range []string{"0", "1", "invalid"} {
		r := convertRuntimeContextFixture(t, "[WEB]\nSimulatePrefetchPrerender="+parent+"\nPrefetchPrerenderCallBackTimerIntervalMS=500", "", `Action(){ web_url("page", "URL=https://example.test/", LAST); }`)
		row := r.Script.Metadata.Import.Preserved["runtime_setting_dispositions"].(map[string]map[string]map[string]RuntimeSettingDisposition)["default.cfg"]["WEB"]["PrefetchPrerenderCallBackTimerIntervalMS"]
		want := RuntimeSettingDispositionCompatibilityOnly
		if parent == "0" {
			want = RuntimeSettingDispositionNonApplicable
		}
		if row.Status != want {
			t.Fatalf("parent=%s row=%#v", parent, row)
		}
	}
}

func TestSelectedUSPExplainsSupersededAndInactivePacing(t *testing.T) {
	for _, mode := range []string{"Const", "ASAP", "Invalid"} {
		t.Run(mode, func(t *testing.T) {
			r := convertRuntimeContextFixture(t, "[Iterations]\nIterationPace=IterationASAP\nStartEvery=99\nNumOfIterations=77", "[RunLogicRunRoot]\nRunLogicNumOfIterations=2\nRunLogicPaceType="+mode+"\nRunLogicPaceConstTime=5\nRunLogicRandomPaceMin=2\nRunLogicRandomPaceMax=4", `Action(){ web_url("page", "URL=https://example.test/", LAST); }`)
			rows := r.Script.Metadata.Import.Preserved["runtime_setting_dispositions"].(map[string]map[string]map[string]RuntimeSettingDisposition)
			for key, row := range rows["default.cfg"]["Iterations"] {
				if row.Status != RuntimeSettingDispositionNonApplicable || !strings.Contains(row.Note, "Superseded") || !strings.Contains(row.Note, "default.usp") || row.ParityID != "" {
					t.Fatalf("%s=%#v", key, row)
				}
			}
			if mode == "Const" {
				if r.Script.Config.Pacing.IntervalMs != 5000 || r.Script.Config.RunLogic.Iterations != 2 {
					t.Fatal(r.Script.Config)
				}
				usp := rows["default.usp"]["RunLogicRunRoot"]
				if usp["RunLogicPaceConstTime"].Status != RuntimeSettingDispositionMapped || usp["RunLogicRandomPaceMin"].Status != RuntimeSettingDispositionNonApplicable {
					t.Fatal(usp)
				}
			}
		})
	}
	r := convertRuntimeContextFixture(t, "[Iterations]\nIterationPace=IterationASAP", "", `Action(){ web_url("page", "URL=https://example.test/", LAST); }`)
	rows := r.Script.Metadata.Import.Preserved["runtime_setting_dispositions"].(map[string]map[string]map[string]RuntimeSettingDisposition)
	if rows["default.cfg"]["Iterations"]["IterationPace"].Status != RuntimeSettingDispositionMapped {
		t.Fatal("cfg-only pacing incorrectly superseded")
	}
}

func TestRuntimeOperationAbsenceIsConservative(t *testing.T) {
	for _, tc := range []struct {
		name, protocol string
		files          map[string]string
		stream, sync   bool
	}{
		{"ordinary", "http", map[string]string{"Action.c": `Action(){ web_url("page", "URL=https://example.test/", LAST); }`}, true, true},
		{"soap", "soap", map[string]string{"Action.c": `Action(){ web_custom_request("page", "URL=https://example.test/", LAST); }`}, true, true},
		{"SDK declarations", "http", map[string]string{"Action.c": `Action(){ web_url("page", "URL=https://example.test/", LAST); }`, "sdk.h": `int web_stream_open(char *name, ...); int web_sync(char *name, ...);`}, true, true},
		{"token pasting", "http", map[string]string{"Action.c": "#define CALL(n) web_##n\nAction(){ CALL(stream_open)(\"stream\", LAST); }"}, false, false},
		{"comments", "http", map[string]string{"Action.c": "Action(){ /* web_stream_open(); */ return 0; } // web_sync();"}, true, true},
		{"stream helper", "http", map[string]string{"Action.c": `Action(){ return 0; }`, "helper.h": `void play(){ web_stream_open("stream", LAST); }`}, false, true},
		{"sync", "http", map[string]string{"Action.c": `Action(){ web_sync("step", LAST); }`}, true, false},
		{"macro reference", "http", map[string]string{"Action.c": "#define OPEN web_stream_open\nAction(){ OPEN(\"stream\", LAST); }"}, false, true},
		{"stream pointer", "http", map[string]string{"Action.c": `Action(){ int (*play)(char *, ...) = web_stream_open; play("stream", LAST); }`}, false, true},
		{"sync pointer", "http", map[string]string{"Action.c": `Action(){ int (*sync)(char *, ...) = &web_sync; sync("step", LAST); }`}, true, false},
		{"stream callback", "http", map[string]string{"Action.c": `Action(){ register_callback(web_stream_open); }`}, false, true},
		{"header reference", "http", map[string]string{"Action.c": `Action(){ return 0; }`, "helper.h": `void *callback = &web_sync;`}, true, false},
		{"wrapped call", "http", map[string]string{"Action.c": `Action(){ (web_sync)("step", LAST); }`}, true, false},
		{"literal references", "http", map[string]string{"Action.c": `Action(){ lr_output_message("web_stream_open and web_sync"); }`}, true, true},
		{"multiline SDK declaration", "http", map[string]string{"sdk.h": "int web_stream_open\n(char *name, ...);\nint web_sync\n(char *name, ...);"}, true, true},
		{"parse failure", "http", map[string]string{"Action.c": `Action(){ web_url("broken`}, false, false},
		{"empty corpus", "http", nil, false, false},
		{"external runner", "java_vuser", map[string]string{"Action.c": `Action(){ return 0; }`}, false, false},
		{"mixed language", "http", map[string]string{"Action.c": `Action(){ return 0; }`, "custom.js": "execute();"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream, sync := runtimeSourceOperationsAbsent(tc.files, tc.protocol)
			if stream != tc.stream || sync != tc.sync {
				t.Fatalf("absent=%v/%v want %v/%v", stream, sync, tc.stream, tc.sync)
			}
		})
	}
	for _, operation := range []string{"", `web_stream_open("stream", LAST);`, `int (*play)(char *, ...) = web_stream_open; play("stream", LAST);`} {
		r := convertRuntimeContextFixture(t, "[Streaming]\nStreamTimeout=60\nStreamRetryTime=5\nStreamSeekMethod=normal", "", fmt.Sprintf(`Action(){ %s web_url("page", "URL=https://example.test/", LAST); }`, operation))
		rows := r.Script.Metadata.Import.Preserved["runtime_setting_dispositions"].(map[string]map[string]map[string]RuntimeSettingDisposition)["default.cfg"]["Streaming"]
		for key, row := range rows {
			want := RuntimeSettingDispositionCompatibilityOnly
			if operation == "" {
				want = RuntimeSettingDispositionNonApplicable
			}
			if row.Status != want {
				t.Fatalf("%s %s=%#v want %s", operation, key, row, want)
			}
		}
	}
}

func convertRuntimeContextFixture(t *testing.T, cfg, usp, source string) *ConvertResult {
	t.Helper()
	files := map[string]string{"sample.usr": "[General]\nType=Web\n[Actions]\nAction=Action.c", "default.cfg": cfg, "Action.c": source}
	if usp != "" {
		files["default.usp"] = usp
	}
	r, err := ConvertZip(buildSourceZip(t, files), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
