package browser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Native acceptance is intentionally loopback-only with invented content.
// The private user HAR is never input, replayed or written by these fixtures.
func TestHAROwnershipNativeContextFlushAndJoin(t *testing.T) {
	if testing.Short() {
		t.Skip("native HAR ownership fixture requires an installed browser")
	}
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/html")
				w.Header().Set("Set-Cookie", "ownership=synthetic-private-cookie")
				_, _ = w.Write([]byte("<!doctype html><title>HAR ownership fixture</title><p>synthetic loopback</p>"))
			}))
			defer server.Close()
			dir := t.TempDir()
			sibling := &Browser{}
			if err := sibling.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true, "browser_har_capture": "off"}); err != nil {
				requireBrowserRuntime(t, engine, err)
			}
			defer sibling.Teardown(context.Background())
			b := &Browser{}
			if err := b.Init(context.Background(), map[string]any{"base_url": server.URL, "browser_engine": engine, "browser_headless": true, "browser_har_capture": "always", "browser_har_dir": dir}); err != nil {
				requireBrowserRuntime(t, engine, err)
			}
			defer b.Teardown(context.Background())
			if requests.Load() != 0 {
				t.Fatal("context admission executed target traffic")
			}
			o := b.harOwner
			if o == nil {
				t.Fatal("native context has no private capture owner")
			}
			before, err := os.Stat(o.path)
			if err != nil || before.Mode().Perm() != 0o600 {
				t.Fatal("native writer admitted without a private raw file", err)
			}
			navigate := func(browser *Browser) {
				t.Helper()
				result, err := browser.Execute(context.Background(), &protocol.Action{Name: "synthetic navigation", Type: "browser_navigate", Target: "/"})
				if err != nil || result == nil || result.ErrorMessage != "" {
					t.Fatal("loopback navigation failed", err)
				}
			}
			navigate(b)
			// An injected gate blocks the actual exact context.Close callback.
			// Its deadline must not release the file lease or claim retirement.
			gate, entered := make(chan struct{}), make(chan struct{})
			var gateOnce sync.Once
			releaseGate := func() { gateOnce.Do(func() { close(gate) }) }
			nativeContext := b.pwContext
			defer func() {
				releaseGate()
				_ = b.Teardown(context.Background())
				_, _ = o.waitFinish(context.Background())
			}()
			o.bindWriter(func() error { close(entered); <-gate; return closeOwnedHARBrowserContext(nativeContext) })
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			err = b.Teardown(ctx)
			cancel()
			<-entered
			if !errors.Is(err, errHAROwnershipUnproven) || o.retiredSuccessfully() {
				t.Fatal("blocked native callback was reported as a completed join", err)
			}
			if current, err := os.Stat(o.path); err != nil || !os.SameFile(before, current) || current.Mode().Perm() != 0o600 {
				t.Fatal("blocked native flush lost its exact private file", err)
			}
			if len(harArtifacts(t, dir)) != 0 || len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
				t.Fatal("blocked native writer exposed final output or cleanup proof")
			}
			releaseGate()
			artifact, err := o.waitFinish(context.Background())
			if err != nil || artifact == "" || !o.retiredSuccessfully() {
				t.Fatal("actual native flush did not join and publish", err)
			}
			if err := b.Teardown(context.Background()); err != nil {
				t.Fatal("late joined teardown failed", err)
			}
			data, err := os.ReadFile(artifact)
			if err != nil || len(data) > 1<<20 || scrub.HARSensitivity(data) != scrub.SensitivityScrubbed {
				t.Fatal("native scrubbed artifact is absent or invalid", err)
			}
			var document struct {
				Log struct {
					Entries []json.RawMessage `json:"entries"`
				} `json:"log"`
			}
			if json.Unmarshal(data, &document) != nil || len(document.Log.Entries) == 0 {
				t.Fatal("native flush published no valid loopback observations")
			}
			artifactInfo, err := os.Stat(artifact)
			if err != nil || artifactInfo.Mode().Perm() != 0o600 || filepath.Dir(artifact) != filepath.Dir(filepath.Dir(filepath.Dir(o.path))) {
				t.Fatal("completed artifact lost protected flat discoverability", err)
			}
			navigate(sibling)
			time.Sleep(40 * time.Millisecond)
			if _, err := os.Stat(o.path); !os.IsNotExist(err) {
				t.Fatal("raw file resurrected after the native writer joined", err)
			}
			if !sibling.pwContext.Browser().IsConnected() {
				t.Fatal("retiring one owner killed the shared native browser")
			}
			entries, err := os.ReadDir(filepath.Join(dir, runtimeHARWorkingDirectory))
			if err != nil || len(entries) != 2 || entries[0].Name() != "namespace.lock" || entries[1].Name() != harRetirementLedgerName {
				t.Fatal("joined native generation was not retired", err)
			}
		})
	}
}

func TestHAROwnershipNativeCloseErrorRemainsExplicitAndPrivate(t *testing.T) {
	if testing.Short() {
		t.Skip("native HAR ownership fixture requires an installed browser")
	}
	for _, engine := range []string{"chromium", "webkit"} {
		t.Run(engine, func(t *testing.T) {
			dir := t.TempDir()
			b := &Browser{}
			if err := b.Init(context.Background(), map[string]any{"browser_engine": engine, "browser_headless": true, "browser_har_capture": "always", "browser_har_dir": dir}); err != nil {
				requireBrowserRuntime(t, engine, err)
			}
			o, nativeContext := b.harOwner, b.pwContext
			closeFailure := errors.New("synthetic SDK close receipt failure")
			var calls atomic.Int32
			o.bindWriter(func() error {
				calls.Add(1)
				return errors.Join(closeOwnedHARBrowserContext(nativeContext), closeFailure)
			})
			// Only this fixture knows it deliberately closed the actual context.
			// It may release test descriptors; production cannot infer this from
			// an error, PID, age, dropped lease or a short request deadline.
			defer func() { _ = nativeContext.Close(); _ = o.closeDescriptors(); o.releaseReservation() }()
			if err := b.Teardown(context.Background()); !errors.Is(err, closeFailure) || !errors.Is(err, errHAROwnershipUnproven) {
				t.Fatal("native SDK close failure was hidden", err)
			}
			if o.retiredSuccessfully() || calls.Load() != 1 {
				t.Fatal("failed receipt retired or repeated the actual native owner")
			}
			if info, err := os.Stat(o.path); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("failed native ownership lost protected raw evidence", err)
			}
			if len(harArtifacts(t, dir)) != 0 || len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
				t.Fatal("failed native receipt became final output or cleanup proof")
			}
			if err := b.Teardown(context.Background()); !errors.Is(err, closeFailure) || calls.Load() != 1 {
				t.Fatal("failed exact native closer was retried or hidden", err)
			}
		})
	}
}
