package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These are offline runner-control checks. Stubbed commands prove registration
// and verdict propagation; they never qualify Kafka behavior or a provider.
func TestKafkaOptInQualificationSelectionAndFailure(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is unavailable for the opt-in runner control test")
	}
	runner, err := os.ReadFile("run-optin-suites.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, verdict string
		args          []string
		status, calls int
	}{
		{name: "list does not provision", verdict: "LIST", args: []string{"--list"}},
		{name: "default does not provision", verdict: "SKIP"},
		{name: "explicit fixtures propagate failures", verdict: "FAIL", args: []string{"--all"}, status: 1, calls: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, directory := range []string{"scripts", "bin", "receipts"} {
				if err := os.Mkdir(filepath.Join(root, directory), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			write("scripts/run-optin-suites.sh", string(runner))
			write("bin/go", `#!/usr/bin/env bash
if [[ "$*" == "env CGO_ENABLED" ]]; then printf '0\n'; exit 0; fi
printf '%s\n' '--- PASS: TestOfflineOptInRunnerControl (0.00s)'
`)
			write("scripts/run-recording-e2e.sh", "#!/usr/bin/env bash\nexit 0\n")
			wrapper := `#!/usr/bin/env bash
set -euo pipefail
[[ $# == 2 && "$1" == '--report' && ! -e "$2" ]]
[[ -d "$(dirname "$2")" ]]
printf '%s\t%s\n' "${0##*/}" "$2" >> "$APEX_OPTIN_CONTROL_CALLS"
printf '%s\n' '{"offline_runner_control":true,"status":"failed"}' > "$2"
printf '%s\n' 'required fixture missing (offline runner-control stub)'
exit 37
`
			for _, name := range []string{"test-kafka-owned-seek.sh", "test-kafka-transactions.sh"} {
				write("scripts/"+name, wrapper)
			}
			callsFile := filepath.Join(root, "calls.txt")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bash, append([]string{filepath.Join(root, "scripts/run-optin-suites.sh")}, test.args...)...)
			command.Dir = root
			command.WaitDelay = 2 * time.Second
			// External integration gates must not inherit the operator's targets.
			exclude := map[string]bool{
				"PATH": true, "TMPDIR": true, "APEX_OPTIN_CONTROL_CALLS": true,
				"KAFKA_BROKERS": true, "SOLACE_AMQP_URL": true, "SOLACE_QUEUE": true,
				"SERVICEBUS_CONNECTION_STRING": true, "SERVICEBUS_NAMESPACE": true,
				"EVENTHUBS_CONNECTION_STRING": true, "APEXLOAD_TEST_POSTGRES_URL": true,
				"APEXLOAD_JAVA_CDS_TEST_JDK_HOME": true, "APEXLOAD_JAVA_EVIDENCE_FIXTURE_DIR": true,
				"APEXLOAD_JAVA_POD_DOCKER_IMAGE": true,
			}
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if !exclude[key] {
					command.Env = append(command.Env, entry)
				}
			}
			command.Env = append(command.Env,
				"PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TMPDIR="+filepath.Join(root, "receipts"), "APEX_OPTIN_CONTROL_CALLS="+callsFile)
			output, err := command.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("runner control exceeded its deadline: %v", ctx.Err())
			}
			status := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					status = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if status != test.status {
				t.Fatalf("runner status=%d want=%d output=%s", status, test.status, output)
			}
			for _, name := range []string{
				"Kafka owned-seek qualification (kafka_real,kafka_seek_integration)",
				"Kafka transaction qualification (kafka_real,kafka_transaction_integration)",
			} {
				if !strings.Contains(string(output), test.verdict+"  "+name) || strings.Contains(string(output), "PASS  "+name) {
					t.Fatalf("missing exact %s verdict or false provider PASS for %s: %s", test.verdict, name, output)
				}
			}
			calls, err := os.ReadFile(callsFile)
			if test.calls == 0 {
				if !os.IsNotExist(err) {
					t.Fatalf("unselected qualification created calls: %q (%v)", calls, err)
				}
				entries, err := os.ReadDir(filepath.Join(root, "receipts"))
				if err != nil || len(entries) != 0 {
					t.Fatalf("unselected qualification allocated receipts: %v (%v)", entries, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
			if len(lines) != test.calls {
				t.Fatalf("qualification calls=%q", calls)
			}
			paths := map[string]bool{}
			for index, name := range []string{"test-kafka-owned-seek.sh", "test-kafka-transactions.sh"} {
				called, path, ok := strings.Cut(lines[index], "\t")
				if !ok || called != name || paths[path] || filepath.Base(path) != "report.json" {
					t.Fatalf("qualification routing/receipt isolation=%q", lines)
				}
				paths[path] = true
				if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), `"offline_runner_control":true`) {
					t.Fatalf("failed qualification receipt was not retained: %s (%v)", data, err)
				}
				if !strings.Contains(string(output), filepath.Dir(path)) {
					t.Fatal("retained receipt path is not discoverable")
				}
			}
		})
	}
}
