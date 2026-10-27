package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

type stubCloseWindowPage struct {
	playwright.Page
}

func TestBrowserFillClearValidationAndCloseWindowUnit(t *testing.T) {
	b := &Browser{}
	err := b.fillAction(context.Background(), &protocol.Action{
		Type:     "browser_fill",
		Selector: "#input",
		Value:    "text",
		Params: map[string]any{
			"input_mode": "type",
			"clear":      "not-a-bool",
		},
	}, "#input", "text")
	if err == nil || !strings.Contains(err.Error(), "browser_fill clear must be a boolean") {
		t.Fatalf("fillAction invalid clear err = %v, want boolean error", err)
	}

	if err, retry := b.tryCloseWindow(context.Background(), &protocol.Action{Type: "browser_close_window"}); err == nil || retry || !strings.Contains(err.Error(), "no open pages") {
		t.Fatalf("tryCloseWindow empty pages = (%v, %v), want no open pages", err, retry)
	}

	b.pages = []playwright.Page{&stubCloseWindowPage{}}
	if err, retry := b.tryCloseWindow(context.Background(), &protocol.Action{Type: "browser_close_window"}); err == nil || retry || !strings.Contains(err.Error(), "cannot close the last remaining page") {
		t.Fatalf("tryCloseWindow single page = (%v, %v), want last remaining page error", err, retry)
	}
	if _, err, retry := b.selectWindowToClose(&protocol.Action{
		Type:   "browser_close_window",
		Params: map[string]any{"index": -1},
	}, b.pages); err == nil || retry {
		t.Fatalf("selectWindowToClose negative index = (%v, %v), want non-retry error", err, retry)
	}
}

func TestTruClientReplayPrimitivesLiveChromiumAndWebKit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Primary Workspace</title></head><body>
			<input id="query" type="text" value="prefix" />
			<button id="net-btn" type="button" onclick="
				fetch('/api/slow').then(r => r.text()).then(txt => {
					document.getElementById('net-status').textContent = txt + ':' + document.getElementById('query').value;
				});
			">Run Network Step</button>
			<div id="net-status">idle</div>
			<a id="open-popup" href="/popup" target="_blank">Open Popup</a>
			<a id="download-btn" href="/exports/report.csv" download="report.csv">Download Report</a>
		</body></html>`)
	})
	mux.HandleFunc("/api/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = fmt.Fprint(w, "settled")
	})
	mux.HandleFunc("/popup", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>Popup Tab</title></head><body><h1 id="popup-heading">Popup Ready</h1></body></html>`)
	})
	mux.HandleFunc("/exports/report.csv", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
		_, _ = fmt.Fprint(w, "id,status\n1,ok\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, engineName := range []string{"chromium", "webkit"} {
		engineName := engineName
		t.Run(engineName, func(t *testing.T) {
			downloadDir := t.TempDir()
			t.Setenv("APEXLOAD_EXPORT_DIR", downloadDir)
			xmlDoc := fmt.Sprintf(`<truScript>
  <step type="block" action="action" actionName="Action">
    <step action="Navigate" stepId="1" endEvent="Document Loaded" arguments='{"Location":{"value":%q}}' />
    <step action="Type" stepId="2" arguments='{"Selector":{"value":"\"#query\""},"Value":{"value":"\"-suffix\""},"Clear":{"value":"\"false\""}}' />
    <step action="Click" stepId="3" endEvent="Step network completed" arguments='{"Selector":{"value":"\"#net-btn\""}}' />
    <step action="Click" stepId="4" arguments='{"Selector":{"value":"\"#open-popup\""}}' />
    <step action="Activate Tab" stepId="5" arguments='{"Tab":{"value":"\"Popup Tab\""}}' />
    <step action="Wait" stepId="6" arguments='{"Selector":{"value":"\"#popup-heading\""}}' />
    <step action="Close Tab" stepId="7" arguments='{"Tab":{"value":"\"Popup Tab\""}}' />
    <step action="Click" stepId="8" arguments='{"Selector":{"value":"\"#download-btn\""}}' />
    <step action="Wait for File Download" stepId="9" arguments='{"File Name":{"value":"\"report.csv\""},"Save Dir":{"value":"\"reports\""}}' />
  </step>
</truScript>`, fmt.Sprintf("%q", srv.URL+"/"))

			converted, err := trueclient.ConvertArchive(map[string]string{"default.xml": xmlDoc}, trueclient.ConvertOptions{
				ScriptName: "TruClientLiveReplay_" + engineName,
			})
			if err != nil {
				t.Fatalf("ConvertArchive: %v", err)
			}
			if issues := dsl.ValidateScriptIssues(converted.Script); len(issues) != 0 {
				t.Fatalf("ValidateScriptIssues: %v", issues)
			}
			if converted.Stats.ReviewOnlySteps != 0 || len(converted.Stats.UnsupportedSteps) != 0 {
				t.Fatalf("unexpected review/unsupported steps: %+v", converted.Stats)
			}

			b := &Browser{}
			if err := b.Init(context.Background(), map[string]any{
				"browser_engine":                       engineName,
				"browser_headless":                     true,
				"browser_capture_document_metrics":     true,
				"browser_capture_interactions":         true,
				"browser_interaction_evidence_version": 4,
				"browser_viewport_width":               1280,
				"browser_viewport_height":              720,
			}); err != nil {
				requireBrowserRuntime(t, engineName, err)
			}
			defer func() { _ = b.Teardown(context.Background()) }()

			for i, dslAction := range converted.Script.Actions {
				protoAction := &protocol.Action{
					Name:      dslAction.Name,
					Type:      dslAction.Type,
					Target:    dslAction.Target,
					Selector:  dslAction.Selector,
					Value:     dslAction.Value,
					Params:    dslAction.Params,
					TimeoutMs: 10000,
				}
				if dslAction.WaitFor != nil {
					protoAction.WaitFor = &protocol.WaitCondition{
						Type:      dslAction.WaitFor.Type,
						Value:     dslAction.WaitFor.Value,
						TimeoutMs: dslAction.WaitFor.TimeoutMs,
					}
				}
				if dslAction.Sync != nil {
					protoAction.Sync = &protocol.BrowserSyncPolicy{
						Type:      dslAction.Sync.Type,
						Value:     dslAction.Sync.Value,
						TimeoutMs: 10000,
						StableMs:  150,
					}
				}
				if dslAction.Type == "browser_click" && dslAction.Selector == "#net-btn" {
					protoAction.Assertions = []protocol.Assertion{{
						Type:  "body_contains",
						Value: "settled:prefix-suffix",
					}}
				}
				res, execErr := b.Execute(context.Background(), protoAction)
				if execErr != nil {
					t.Fatalf("step %d (%s %s) err: %v", i, dslAction.Type, dslAction.Name, execErr)
				}
				if res == nil || !res.Success {
					t.Fatalf("step %d (%s %s) failed: %+v", i, dslAction.Type, dslAction.Name, res)
				}
			}

			if pages := b.contextPages(); len(pages) != 1 {
				t.Fatalf("remaining open pages = %d, want 1 after Close Tab", len(pages))
			}
			savedPath := filepath.Join(downloadDir, "reports", "report.csv")
			data, err := os.ReadFile(savedPath)
			if err != nil || !strings.Contains(string(data), "1,ok") {
				t.Fatalf("downloaded report.csv data=%q err=%v", string(data), err)
			}

			interactionBatches := b.TakeBrowserInteractionEvidence()
			foundDownloadOutcome := false
			for _, batch := range interactionBatches {
				for _, rec := range batch.Interactions {
					if rec.Kind == "download" {
						for _, out := range rec.Outcomes {
							if out.Kind == protocol.InteractionOutcomeKindDownload && out.Observed == protocol.InteractionOutcomeMet {
								foundDownloadOutcome = true
							}
						}
					}
				}
			}
			if !foundDownloadOutcome {
				t.Fatalf("did not find met download outcome in %+v", interactionBatches)
			}

			if err := b.Teardown(context.Background()); err != nil {
				t.Fatalf("Teardown(%s): %v", engineName, err)
			}
			docEvidence := b.BrowserDocumentEvidence()
			if docEvidence == nil || docEvidence.Incomplete {
				t.Fatalf("docEvidence after Close Tab = %+v, want complete", docEvidence)
			}
		})
	}
}
