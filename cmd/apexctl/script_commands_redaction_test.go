package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
)

// withRunEvidenceRedaction stamps config.log.redact_run_evidence onto a raw
// script fixture; every fixture here has a config object with other keys.
func withRunEvidenceRedaction(t *testing.T, raw string, redact bool) string {
	t.Helper()
	const marker = `"config": {`
	if strings.Count(raw, marker) != 1 {
		t.Fatalf("fixture must contain exactly one %s: %s", marker, raw)
	}
	return strings.Replace(raw, marker, fmt.Sprintf(`%s"log": {"redact_run_evidence": %t}, `, marker, redact), 1)
}

// assertCLILiteral states the contract once: a script-sourced literal is
// scrubbed from CLI output exactly when the script turns redaction on.
func assertCLILiteral(t *testing.T, label, output, secret string, redact bool) {
	t.Helper()
	if leaked := strings.Contains(output, secret); leaked == redact {
		t.Fatalf("%s: redact_run_evidence=%t but literal %q leaked=%t: %s", label, redact, secret, leaked, output)
	}
}

// assertCLINeverLeaks covers outputs that either never carry the value or pass
// it through the generic shape/name scrubbers (URL userinfo, quoted validation
// values, token= pairs), which apply in both modes.
func assertCLINeverLeaks(t *testing.T, label, output, secret string) {
	t.Helper()
	if strings.Contains(output, secret) {
		t.Fatalf("%s: leaked %q: %s", label, secret, output)
	}
}

func TestScriptCommandsAllowLiteralCredentialsAndScrubCLIDisclosure(t *testing.T) {
	for _, mode := range []struct {
		name   string
		redact bool
	}{{name: "redaction_on", redact: true}, {name: "redaction_off", redact: false}} {
		t.Run(mode.name, func(t *testing.T) {
			assertCLIDisclosureFollowsRunEvidenceSetting(t, mode.redact)
		})
	}
}

func assertCLIDisclosureFollowsRunEvidenceSetting(t *testing.T, redact bool) {
	const (
		urlPwd    = "synth-cli-urlpwd-9901"
		javaVMPwd = "synth-cli-javavm-9902"
		bearerTok = "synth-cli-bearer-9903"
		soapPwd   = "synth-cli-soappwd-9904"
	)

	rawScript := `{
  "name": "cli-literal-credentials",
  "protocol": "http",
  "config": {
    "base_url": "https://operator:` + urlPwd + `@api.example.test",
    "java_vuser": {
      "additional_vm_parameters": ["-Dapp.password=` + javaVMPwd + `"]
    }
  },
  "metadata": {
    "import": {
      "source": "postman",
      "review_findings": [
        {
          "code": "import_credential_preserved",
          "severity": "warning",
          "message": "Preserved literal credentials in imported script"
        }
      ]
    }
  },
  "actions": [
    {
      "name": "login",
      "type": "request",
      "method": "POST",
      "target": "/soap",
      "headers": {
        "Authorization": "Bearer ` + bearerTok + `"
      },
      "body": "<ns:Login xmlns:ns=\"urn:test\"><ns:userPassword>` + soapPwd + `</ns:userPassword></ns:Login>"
    }
  ]
}`
	path := writeTempScript(t, withRunEvidenceRedaction(t, rawScript, redact))

	valRes := validateScriptFile(path)
	if !valRes.Valid {
		t.Fatalf("validateScriptFile marked literal-credential script invalid: %+v", valRes)
	}
	valJSON, _ := json.Marshal(valRes)
	// A valid result carries no script content, so nothing surfaces in either mode.
	for _, secret := range []string{urlPwd, javaVMPwd, bearerTok, soapPwd} {
		if strings.Contains(string(valJSON), secret) {
			t.Fatalf("validateScriptFile output leaked %q: %s", secret, valJSON)
		}
	}

	expRes, err := explainScriptFile(path)
	if err != nil {
		t.Fatalf("explainScriptFile failed on script with literal credentials: %v", err)
	}
	expJSON, _ := json.Marshal(expRes)
	for _, secret := range []string{urlPwd, javaVMPwd, bearerTok, soapPwd} {
		assertCLINeverLeaks(t, "explainScriptFile", string(expJSON), secret)
	}

	loaded, cliRedaction, err := loadScriptForCLIExecution(path, runArgs{})
	if err != nil {
		t.Fatalf("loadScriptForCLIExecution blocked script with literal credentials / Java -D VM property: %v", err)
	}
	if loaded == nil {
		t.Fatalf("expected loaded script")
	}
	if redact && cliRedaction == nil {
		t.Fatalf("expected a CLI redaction config with redact_run_evidence on")
	}
	for _, secret := range []string{urlPwd, javaVMPwd, bearerTok, soapPwd} {
		assertCLILiteral(t, "scrubCLIText", scrubCLIText("failure detail: "+secret, cliRedaction), secret, redact)
	}

	// Finding 6 regression: when a script unmarshals cleanly with literal
	// credentials but fails DSL validation, parseScriptFileForCLI still returns
	// exact-value CLI redaction so validation errors cannot leak script secrets.
	invalidScript := `{
  "name": "cli-invalid-with-secret",
  "protocol": "http",
  "config": {
    "base_url": "https://api.example.test"
  },
  "actions": [
    {
      "name": "bad-action",
      "type": "unsupported_action_" + "` + bearerTok + `",
      "headers": {
        "X-AccPassword": "` + bearerTok + `"
      }
    }
  ]
}`
	invalidScript = strings.ReplaceAll(invalidScript, `"unsupported_action_" + "`+bearerTok+`"`, `"unsupported_action_`+bearerTok+`"`)
	invalidPath := writeTempScript(t, withRunEvidenceRedaction(t, invalidScript, redact))
	invalidVal := validateScriptFile(invalidPath)
	if invalidVal.Valid {
		t.Fatalf("expected invalid script to fail validation")
	}
	invalidJSON, _ := json.Marshal(invalidVal)
	assertCLINeverLeaks(t, "validateScriptFile validation error", string(invalidJSON), bearerTok)
	_, expErr := explainScriptFile(invalidPath)
	if expErr == nil {
		t.Fatalf("explainScriptFile accepted an invalid script")
	}
	assertCLINeverLeaks(t, "explainScriptFile validation error", expErr.Error(), bearerTok)

	// Missing body_file (ResolveBodyRefs failure, non-ValidationError) still returns CLI redaction.
	missingBodyPath := writeTempScript(t, withRunEvidenceRedaction(t, `{
  "name": "cli-missing-body-file",
  "protocol": "http",
  "config": {"base_url": "https://operator:`+urlPwd+`@api.example.test"},
  "actions": [{"name": "post", "type": "request", "method": "POST", "target": "/", "body_file": "missing-`+urlPwd+`.json"}]
}`, redact))
	_, missingRC, bodyErr := parseScriptFileForCLI(missingBodyPath)
	if bodyErr == nil || (redact && missingRC == nil) {
		t.Fatalf("expected an error (and a redaction config when on) on missing body_file, got rc=%v err=%v", missingRC, bodyErr)
	}
	assertCLINeverLeaks(t, "missing body_file error", scrubCLIText(bodyErr.Error(), missingRC), urlPwd)

	// Oversized script (> maxCLIScriptBytes) is rejected before unbounded allocation.
	oversizedPath := writeTempScript(t, `{"name":"big","pad":"`+strings.Repeat("a", maxCLIScriptBytes+64)+`"}`)
	if _, _, bigErr := parseScriptFileForCLI(oversizedPath); bigErr == nil || !strings.Contains(bigErr.Error(), "exceeds") {
		t.Fatalf("expected byte-limit rejection on >32MiB script, got %v", bigErr)
	}

	// Finding 3 regression: executeLocalCI allows preserved-import scripts with
	// literal credentials / Java -D VM properties and scrubs literal secrets from
	// preparation and execution errors, while keeping --variables-file secret-key
	// rejection intact.
	t.Setenv("GITHUB_ACTIONS", "false")
	ciTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ciTarget.Close()
	parsedTarget, err := url.Parse(ciTarget.URL)
	if err != nil {
		t.Fatalf("parse ciTarget URL: %v", err)
	}
	ciValidPath := writeTempScript(t, withRunEvidenceRedaction(t, `{
  "name": "ci-local-preserved-import",
  "protocol": "http",
  "config": {
    "base_url": "http://operator:`+urlPwd+`@`+parsedTarget.Host+`",
    "java_vuser": {
      "additional_vm_parameters": ["-Dapp.password=`+javaVMPwd+`"]
    },
    "run_logic": {"iterations": 1}
  },
  "actions": [
    {"name": "ping", "type": "request", "method": "GET", "target": "/"}
  ]
}`, redact))
	ciRes, ciErr := executeCI(context.Background(), ciArgs{
		executionMode: "local",
		scriptPath:    ciValidPath,
		vusers:        1,
		duration:      20 * time.Millisecond,
	})
	if ciErr != nil || ciRes.Outcome != ciOutcomePassed {
		t.Fatalf("executeLocalCI rejected valid preserved-import script: res=%+v err=%v", ciRes, ciErr)
	}

	ciErrRes, ciInvalidErr := executeCI(context.Background(), ciArgs{
		executionMode: "local",
		scriptPath:    invalidPath,
		vusers:        1,
		duration:      20 * time.Millisecond,
	})
	if ciInvalidErr == nil || ciErrRes.Outcome != ciOutcomeConfigurationError {
		t.Fatalf("executeLocalCI accepted an invalid script: res=%+v err=%v", ciErrRes, ciInvalidErr)
	}
	assertCLINeverLeaks(t, "executeLocalCI invalid script", ciInvalidErr.Error(), bearerTok)

	varsPath := filepath.Join(t.TempDir(), "vars.json")
	writeTempFile := writeTempScript(t, `{"API_SECRET":"not-allowed-in-variables-file"}`)
	_ = varsPath
	_, secretVarsErr := executeCI(context.Background(), ciArgs{
		executionMode: "local",
		scriptPath:    ciValidPath,
		variablesFile: writeTempFile,
		vusers:        1,
		duration:      20 * time.Millisecond,
	})
	if secretVarsErr == nil {
		t.Fatalf("expected --variables-file secret-key guard to remain enforced")
	}

	// Round-4 Finding 4b regression: CLI -env sensitive-named overrides are
	// registered onto the returned CLI RedactionConfig even when substituted into
	// neutral-named DSL fields, but only when redact_run_evidence is on. They are
	// literal operator values, not secret-store material, so they follow the
	// setting like script literals and never count as runtime secret material
	// (which would drop browser screenshots and change payload retention).
	const cliEnvSecret = "short-pin-99"
	neutralEnvPath := writeTempScript(t, withRunEvidenceRedaction(t, `{
  "name": "cli-env-neutral-target",
  "protocol": "http",
  "config": {"base_url": "https://api.example.test"},
  "actions": [
    {"name": "fetch-{{env.api_password}}", "type": "request", "method": "GET", "target": "/v1/{{env.api_password}}/orders"}
  ]
}`, redact))
	_, envRC, envLoadErr := loadScriptForCLIExecution(neutralEnvPath, runArgs{
		env: map[string]string{"api_password": cliEnvSecret, "region": "us-east-1"},
	})
	if envLoadErr != nil || (redact && envRC == nil) {
		t.Fatalf("loadScriptForCLIExecution with -env failed: rc=%v err=%v", envRC, envLoadErr)
	}
	if envRC != nil && envRC.HasRuntimeSecretMaterial() {
		t.Fatalf("CLI -env value registered as platform-managed secret material (redact_run_evidence=%t)", redact)
	}
	scrubbedEnvMsg := scrubCLIText("action fetch-"+cliEnvSecret+" in us-east-1 failed", envRC)
	if !strings.Contains(scrubbedEnvMsg, "us-east-1") {
		t.Fatalf("CLI -env redaction scrubbed a neutral value: %q", scrubbedEnvMsg)
	}
	assertCLILiteral(t, "CLI -env value", scrubbedEnvMsg, cliEnvSecret, redact)
	// A JWT-shaped -env value under a neutral key is equally a literal: it
	// must not mark the run as carrying platform-managed secret material.
	_, jwtRC, jwtErr := loadScriptForCLIExecution(neutralEnvPath, runArgs{
		env: map[string]string{"region": "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJjbGktZW52In0.c3ludGgtY2xpLWVudi1zaWc"},
	})
	if jwtErr != nil {
		t.Fatalf("loadScriptForCLIExecution with JWT -env failed: %v", jwtErr)
	}
	if jwtRC != nil && jwtRC.HasRuntimeSecretMaterial() {
		t.Fatalf("JWT-shaped CLI -env value registered as platform-managed secret material (redact_run_evidence=%t)", redact)
	}

	// Round-5 Finding 3 regression: engine.New receives RedactionConfig: envRC so
	// eng.Run redacts sensitive -env values substituted into neutral DSL surfaces.
	envEchoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, "failed path=%s", r.URL.Path)
	}))
	defer envEchoSrv.Close()
	neutralExecPath := writeTempScript(t, withRunEvidenceRedaction(t, `{
  "name": "cli-env-engine-run",
  "protocol": "http",
  "config": {"base_url": "`+envEchoSrv.URL+`", "run_logic": {"iterations": 1}},
  "actions": [
    {"name": "fetch-orders", "type": "request", "method": "GET", "target": "/v1/{{env.api_password}}/orders"}
  ]
}`, redact))
	execScript, execRC, execLoadErr := loadScriptForCLIExecution(neutralExecPath, runArgs{
		env: map[string]string{"api_password": cliEnvSecret},
	})
	if execLoadErr != nil {
		t.Fatalf("loadScriptForCLIExecution(neutralExecPath): %v", execLoadErr)
	}
	eng, engErr := engine.New(engine.Config{
		Script:          execScript,
		VUsers:          1,
		Duration:        50 * time.Millisecond,
		RedactionConfig: execRC,
	})
	if engErr != nil {
		t.Fatalf("engine.New with RedactionConfig failed: %v", engErr)
	}
	runRes, _ := eng.Run(context.Background())
	snapJSON, _ := json.Marshal(runRes)
	if redact && strings.Contains(string(snapJSON), cliEnvSecret) {
		t.Fatalf("eng.Run FinalSnapshot leaked CLI -env value %q (redact_run_evidence=%t): %s", cliEnvSecret, redact, snapJSON)
	}

	// Round-8 Finding 6 regression: validateScriptFile and explainScriptFile
	// scrub credential-bearing Name, GeneratedBy, GeneratedFrom URLs, and Summary.
	const (
		genURLSecret = "synth-gen-url-tok-77"
		namePin      = "synth-name-pin-78"
	)
	metaScriptPath := writeTempScript(t, withRunEvidenceRedaction(t, `{
  "name": "import-`+namePin+`",
  "protocol": "http",
  "generated_by": "importer token=`+namePin+`",
  "generated_from": "https://svc:`+genURLSecret+`@specs.example.test/openapi.json?api_key=`+genURLSecret+`",
  "config": {"base_url": "https://api.example.test"},
  "actions": [
    {"name": "ping", "type": "request", "method": "GET", "target": "/", "headers": {"X-Api-Key": "`+namePin+`"}}
  ]
}`, redact))
	metaVal := validateScriptFile(metaScriptPath)
	metaValJSON, _ := json.Marshal(metaVal)
	if !metaVal.Valid {
		t.Fatalf("validateScriptFile rejected the metadata fixture: %s", metaValJSON)
	}
	assertCLILiteral(t, "validateScriptFile script.Name", string(metaValJSON), namePin, redact)
	metaExplain, metaExpErr := explainScriptFile(metaScriptPath)
	if metaExpErr != nil {
		t.Fatalf("explainScriptFile(metaScriptPath): %v", metaExpErr)
	}
	metaExpJSON, _ := json.Marshal(metaExplain)
	// generated_from carries the value in URL userinfo and an api_key query
	// pair, both shape-scrubbed; the bare name pin follows the setting.
	assertCLINeverLeaks(t, "explainScriptFile generated_from", string(metaExpJSON), genURLSecret)
	assertCLILiteral(t, "explainScriptFile script.Name", string(metaExpJSON), namePin, redact)

	// Round-9 Finding 2 regression: writeRedactedRunJUnit scrubs both script.Name
	// and fallback args.script filename in JUnit suite attributes.
	metaScript, metaRC, metaLoadErr := loadScriptForCLIExecution(metaScriptPath, runArgs{})
	if metaLoadErr != nil {
		t.Fatalf("loadScriptForCLIExecution(metaScriptPath): %v", metaLoadErr)
	}
	junitOut := filepath.Join(t.TempDir(), "report.xml")
	if err := writeRedactedRunJUnit(runArgs{script: metaScriptPath, junit: junitOut}, metaScript, metaRC, &engine.RunResult{}, nil, nil, nil); err != nil {
		t.Fatalf("writeRedactedRunJUnit(named): %v", err)
	}
	junitBytes, err := os.ReadFile(junitOut)
	if err != nil {
		t.Fatalf("ReadFile(junitOut): %v", err)
	}
	assertCLILiteral(t, "JUnit script.Name", string(junitBytes), namePin, redact)
	blankNameScript := *metaScript
	blankNameScript.Name = ""
	fallbackScriptPath := filepath.Join(t.TempDir(), "scenario-"+namePin+".json")
	if err := writeRedactedRunJUnit(runArgs{script: fallbackScriptPath, junit: junitOut}, &blankNameScript, metaRC, &engine.RunResult{}, nil, nil, nil); err != nil {
		t.Fatalf("writeRedactedRunJUnit(fallback filename): %v", err)
	}
	fallbackBytes, err := os.ReadFile(junitOut)
	if err != nil {
		t.Fatalf("ReadFile(fallback junitOut): %v", err)
	}
	assertCLILiteral(t, "JUnit fallback filename", string(fallbackBytes), namePin, redact)
}
