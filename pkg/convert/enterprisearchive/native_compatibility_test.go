package enterprisearchive

import (
	"encoding/base64"
	"github.com/jasonczech/apexload/pkg/dsl"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeArchiveCompatibilityAccounting(t *testing.T) {
	files := map[string]string{}
	entries, e := os.ReadDir("testdata/native_acc_compat")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		b, e := os.ReadFile(filepath.Join("testdata/native_acc_compat", entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		files[entry.Name()] = string(b)
	}
	result, e := ConvertZip(buildSourceZip(t, files), ConvertOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if result.Script.Config.HTTP.ReceiveBufferBytes != 12288 || result.Script.Config.HTTP.MaxMetaRefresh != 2 || result.Script.Config.HTTP.ContentCheckErrorLimit != 10 || result.Script.Config.Log.ErrorBufferBytes != 102400 {
		t.Fatal("native settings not mapped")
	}
	if result.Script.Config.HTTP.ProxyMode != "none" {
		t.Fatal("conflicting source proxy selection was activated")
	}
	name, rc := resolveRuntimeConfig(files, nil)
	selected := selectedRuntimeSections(collectRuntimeSections(files), name, rc)
	rows := classifyRuntimeSelectedSections(selected, "http")
	refineRuntimeSettingDispositions(rows, rc, files, "http")
	count := 0
	for file, sections := range selected {
		for section, keys := range sections {
			for key := range keys {
				count++
				row, ok := rows[file][section][key]
				if !ok || (row.Note == "" && row.ParityID == "") {
					t.Fatalf("lost source disposition: %s.%s", section, key)
				}
			}
		}
	}
	if count != 31 {
		t.Logf("fixture covers %d selected source keys", count)
	}
	for _, key := range []string{"ResetContext", "StartRecordingGMT", "StartRecordingIsDst", "NavigatorSystemLanguage", "ScreenAvailWidth", "ScreenAvailHeight", "WebSocketCallBackTimerIntervalMS", "ProxyAutoConfigScriptURL"} {
		if row := rows[name]["WEB"][key]; row.Status != RuntimeSettingDispositionNonApplicable {
			t.Fatalf("%s: %+v", key, row)
		}
	}
	for _, key := range []string{"AnalogMode", "SSLVersionKey", "IgnoreContentCachingTypes", "KeepNonTextMimeType"} {
		if row := rows[name]["WEB"][key]; row.Status != RuntimeSettingDispositionCompatibilityOnly {
			t.Fatalf("ambiguous %s was certified: %+v", key, row)
		}
	}
}
func TestNativeArchiveEncodingPreservesBodiesAndAssets(t *testing.T) {
	files := map[string]string{"default.cfg": "[General]\nEncoding=ANSI\n[WEB]\nRecorderWinCodePage=1252", "Action.c": "Action(){/* caf\xe9 */}", "p.prm": "[parameter:Name]\nTable=data.dat\nType=Table", "data.dat": "name\ncaf\xe9\n", "asset.dat": "\x81\xff\x00"}
	encoding, e := decodeArchiveText(files, "default.cfg")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(files["Action.c"], "café") || !strings.Contains(files["data.dat"], "café") || files["asset.dat"] != "\x81\xff\x00" {
		t.Fatal("source decoding changed asset or lost text")
	}
	a := &dsl.Action{}
	if e := encodeImportedBody(a, ParsedFunction{Body: "café {Name}"}, encoding); e != nil {
		t.Fatal(e)
	}
	if a.Body != "café {{var.name}}" || a.BodyEncoding != "windows-1252" {
		t.Fatalf("%+v", a)
	}
	b := &dsl.Action{}
	// Body is decoded once by the parser; RawBody keeps the source spelling so
	// the escapes name Windows-1252 bytes.
	if e := encodeImportedBody(b, ParsedFunction{Body: "\x81\x00", RawBody: `\x81\x00`}, encoding); e != nil {
		t.Fatal(e)
	}
	raw, e := base64.StdEncoding.DecodeString(b.Body)
	if e != nil || string(raw) != "\x81\x00" {
		t.Fatal("binary body changed")
	}
	for _, cfg := range []string{"[General]\nEncoding=ANSI", "[General]\nEncoding=ANSI\n[WEB]\nRecorderWinCodePage=9999"} {
		if _, e := decodeArchiveText(map[string]string{"default.cfg": cfg}, "default.cfg"); e == nil {
			t.Fatal("ambiguous encoding accepted")
		}
	}
	if _, e := decodeArchiveText(map[string]string{"Action.c": "\xff"}, ""); e == nil {
		t.Fatal("undecodable text silently accepted")
	}
}

func TestNativePortableProxySelectorPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, source, mode string
		review             bool
	}{
		{"pac", "ProxyUseProxy=1\nProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=https://proxy.example.invalid/pac", "pac", false},
		{"system", "ProxyUseBrowser=1", "system", true},
		{"conflict", "ProxyUseBrowser=1\nProxyUseProxyServer=1", "none", true},
		{"disabled parent conflict", "ProxyUseProxy=0\nProxyUseAutoConfigScript=1\nProxyAutoConfigScriptURL=https://proxy.example.invalid/pac", "none", true},
		{"case fold conflict", "ProxyUseBrowser=1\nproxyusebrowser=0", "none", true},
		{"disabled PAC", "ProxyUseAutoConfigScript=0\nProxyAutoConfigScriptURL=https://proxy.example.invalid/pac", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc := ParseRuntimeCfg("[WEB]\n" + tc.source)
			if rc.ProxyMode != tc.mode {
				t.Fatalf("mode=%q want=%q", rc.ProxyMode, tc.mode)
			}
			review := false
			for _, issue := range rc.RuntimeIssues {
				if issue.ID == "proxy" {
					review = true
				}
			}
			if review != tc.review {
				t.Fatalf("review=%v want=%v", review, tc.review)
			}
			if tc.mode == "pac" && rc.ToHTTPConfig().Proxy.PACURL == "" {
				t.Fatal("PAC URL lost")
			}
		})
	}
}

func TestNativeWindows1252FormLiteralConversion(t *testing.T) {
	a := &dsl.Action{Body: "name=caf%C3%A9&next={{var.name}}"}
	if err := encodeImportedBody(a, ParsedFunction{Body: a.Body, itemDataURLForm: true}, "windows-1252"); err != nil {
		t.Fatal(err)
	}
	if a.Body != "name=caf%E9&next={{var.name}}" {
		t.Fatalf("form literals changed: %q", a.Body)
	}
}

func TestNativeArchiveDecodesParameterFilenamesBeforeData(t *testing.T) {
	files := map[string]string{
		"default.cfg": "[General]\nEncoding=ANSI\n[WEB]\nRecorderWinCodePage=1252",
		"p.prm":       "[parameter:Name]\nTable=caf\xe9.dat\nType=Table",
		"café.dat":    "name\ncaf\xe9\n",
		"other.prm":   "[parameter:Other]\nTable=caf\xe9.dat\nType=Table",
		"binary.dat":  "\x81\xff",
	}
	if _, err := decodeArchiveText(files, "default.cfg"); err != nil {
		t.Fatal(err)
	}
	if files["café.dat"] != "name\ncafé\n" {
		t.Fatalf("parameter file not decoded exactly once: %q", files["café.dat"])
	}
	if files["binary.dat"] != "\x81\xff" {
		t.Fatal("binary asset changed")
	}
}
