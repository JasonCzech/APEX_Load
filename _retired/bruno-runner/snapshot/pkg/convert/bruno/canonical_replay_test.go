package bruno

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importartifact"
	_ "github.com/jasonczech/apexload/pkg/protocol/bruno"
	"gopkg.in/yaml.v3"
)

// This crosses the importer, encrypted artifact resolver, Go replay engine,
// real Node worker and pinned Bruno runtime. Source files are never rewritten:
// an added test environment supplies loopback URLs and synthetic credentials.
func TestAttachedCanonicalBrunoReplayFidelity(t *testing.T) {
	runnerURL := startCanonicalFixtureRunner(t)
	t.Setenv("APEXLOAD_BRUNO_RUNNER_URL", runnerURL)
	t.Setenv("APEXLOAD_BRUNO_ALLOW_DIRECT_EGRESS", "true")
	t.Setenv("APEXLOAD_BRUNO_SAFE_EXECUTION_ENABLED", "true")
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x42}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := importartifact.NewEncryptedStore(importartifact.StoreOptions{RootDir: t.TempDir(), Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(importartifact.SetResolver(&importartifact.ContentAddressedResolver{Objects: objects, Kind: "sanitized"}))

	for _, filename := range []string{"ActivateSubscriber_v302.yml", "AddSalesTransactionDetails_v302.yml", "ExecuteUnifiedCreditPolicyCheck_v246.yml"} {
		t.Run(filename, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("testdata", "attached", filename))
			if err != nil {
				t.Fatal(err)
			}
			original, err := Inspect(Input{Filename: filename, Content: source, ProjectID: "fixture-matrix"})
			if err != nil {
				t.Fatal(err)
			}
			var document struct {
				HTTP struct {
					Method  string
					URL     string
					Body    struct{ Data string }
					Headers []struct {
						Name  string
						Value string
					}
				}
			}
			if err := yaml.Unmarshal(source, &document); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var receivedBody, receivedPath, receivedUser, receivedPassword string
			var receivedHeaders http.Header
			var corrupt atomic.Bool
			var hits atomic.Int64
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				mu.Lock()
				receivedBody, receivedPath = string(body), r.URL.Path
				receivedHeaders = r.Header.Clone()
				receivedUser, receivedPassword, _ = r.BasicAuth()
				mu.Unlock()
				hits.Add(1)
				if r.Method != document.HTTP.Method {
					t.Errorf("method = %s, want %s", r.Method, document.HTTP.Method)
				}
				w.Header().Set("Content-Type", "text/xml")
				w.WriteHeader(500)
				code := "500600000013"
				if corrupt.Load() {
					code = "WRONG-FAULT"
				}
				_, _ = io.WriteString(w, `<Fault><code>500</code><description>Please contact AT&amp;T (800) 331-0500</description><faultCode>`+code+`</faultCode><faultDescription>Please contact AT&amp;T (800) 331-0500</faultDescription></Fault>`)
			}))
			defer target.Close()
			values := map[string]string{}
			for _, name := range original.MissingVariables {
				values[name] = "fixture-" + name
			}
			values["env"] = target.URL + "/"
			for _, name := range []string{"deviceadapter", "accountadapter", "riskassessmentadapter"} {
				values[name] = ""
			}
			variables := []map[string]any{}
			for name, value := range values {
				variables = append(variables, map[string]any{"name": name, "value": value, "enabled": true})
			}
			environment, _ := json.Marshal(map[string]any{"name": "fixture", "variables": variables})
			result, err := Inspect(Input{Filename: filename, ProjectID: "fixture-matrix", Files: map[string][]byte{filename: source, "environments/fixture.json": environment}, Options: InspectOptions{DefaultEnvironment: "fixture"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Fidelity.DroppedItems != 0 || result.Script == nil {
				t.Fatal("canonical import dropped source behavior")
			}
			if !bytes.Equal(tarFile(t, result.RawArchive, filename), source) {
				t.Fatal("source changed during import")
			}
			stored, err := objects.PutBytes(context.Background(), "fixture-matrix", "sanitized", result.SanitizedArchive, 8<<20)
			if err != nil {
				t.Fatal(err)
			}
			script := result.Script
			script.Config.Bruno.BundleRef = "artifact://fixture-matrix"
			script.Config.Bruno.BundleSHA256 = stored.SHA256
			script.Config.Bruno.SecretBindings = map[string]string{}
			for _, requirement := range result.SecretRequirements {
				value := requirement.Value
				// Replace the attached literal session identifier with a dummy.
				if strings.Contains(requirement.SuggestedName, "SESSION_ID") {
					value = "fixture-session"
				}
				script.Config.Bruno.SecretBindings[requirement.SuggestedName] = value
			}
			for _, scenario := range []struct {
				name    string
				invalid bool
			}{{"source_assertions_pass", false}, {"altered_fault_fails", true}} {
				t.Run(scenario.name, func(t *testing.T) {
					corrupt.Store(scenario.invalid)
					before := hits.Load()
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					report, err := engine.ReplayScript(ctx, script, engine.ReplayOptions{IgnoreThinkTime: true})
					if err != nil {
						t.Fatal(err)
					}
					if report.Success == scenario.invalid {
						t.Fatalf("success = %v, expected %v; error=%s", report.Success, !scenario.invalid, report.Error)
					}
					if hits.Load()-before != 1 {
						t.Fatalf("requests = %d, want exactly one", hits.Load()-before)
					}
					replace := func(text string) string {
						for name, value := range values {
							text = strings.ReplaceAll(text, "{{"+name+"}}", value)
						}
						return text
					}
					mu.Lock()
					defer mu.Unlock()
					if receivedBody != replace(document.HTTP.Body.Data) {
						t.Fatal("wire SOAP body differs from interpolated source")
					}
					if target.URL+receivedPath != replace(document.HTTP.URL) {
						t.Fatal("wire target path differs from source")
					}
					if receivedUser != values["username"] || receivedPassword != values["password"] {
						t.Fatal("source basic auth did not survive replay")
					}
					for _, header := range document.HTTP.Headers {
						want := replace(header.Value)
						if strings.EqualFold(header.Name, "idpctx-session-id") {
							want = "fixture-session"
						}
						if receivedHeaders.Get(header.Name) != want {
							t.Errorf("header %s differs from source", header.Name)
						}
					}
				})
			}
		})
	}
}

func startCanonicalFixtureRunner(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("canonical replay requires Node and runners/bruno dependencies")
	}
	runner, err := filepath.Abs("../../../runners/bruno")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(runner, "node_modules", "@usebruno", "cli", "package.json")); err != nil {
		t.Skip("install pinned runners/bruno dependencies to run canonical replay")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, node, "-e", `const {createRunnerServer}=require(process.argv[1]); const s=createRunnerServer({allowDirectEgress:true}); s.listen(0,'127.0.0.1',()=>console.log('http://127.0.0.1:'+s.address().port)); process.on('SIGINT',async()=>{await s.closeRunner();process.exit(0)});`, filepath.Join(runner, "src", "server.js"))
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cancel()
			<-done
		}
		cancel()
	})
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(out)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- ""
		}
	}()
	select {
	case url := <-ready:
		if !strings.HasPrefix(url, "http://127.0.0.1:") {
			t.Fatal("canonical runner did not start")
		}
		return url
	case <-time.After(10 * time.Second):
		t.Fatal("canonical runner startup timed out")
	}
	return ""
}
