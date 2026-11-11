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

func TestScriptCommandsAllowLiteralCredentialsAndScrubCLIDisclosure(t *testing.T) {
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
	path := writeTempScript(t, rawScript)

	valRes := validateScriptFile(path)
	if !valRes.Valid {
		t.Fatalf("validateScriptFile marked literal-credential script invalid: %+v", valRes)
	}
	valJSON, _ := json.Marshal(valRes)
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
		if strings.Contains(string(expJSON), secret) {
			t.Fatalf("explainScriptFile output leaked %q: %s", secret, expJSON)
		}
	}

	loaded, cliRedaction, err := loadScriptForCLIExecution(path, runArgs{})
	if err != nil {
		t.Fatalf("loadScriptForCLIExecution blocked script with literal credentials / Java -D VM property: %v", err)
	}
	if loaded == nil || cliRedaction == nil {
		t.Fatalf("expected loaded script and CLI redaction config")
	}
	for _, secret := range []string{urlPwd, javaVMPwd, bearerTok, soapPwd} {
		scrubbed := scrubCLIText("failure detail: "+secret, cliRedaction)
		if strings.Contains(scrubbed, secret) {
			t.Fatalf("scrubCLIText failed to redact literal DSL secret %q: %q", secret, scrubbed)
		}
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
	invalidPath := writeTempScript(t, invalidScript)
	invalidVal := validateScriptFile(invalidPath)
	if invalidVal.Valid {
		t.Fatalf("expected invalid script to fail validation")
	}
	invalidJSON, _ := json.Marshal(invalidVal)
	if strings.Contains(string(invalidJSON), bearerTok) {
		t.Fatalf("validateScriptFile leaked literal header secret on validation error: %s", invalidJSON)
	}
	if _, expErr := explainScriptFile(invalidPath); expErr == nil || strings.Contains(expErr.Error(), bearerTok) {
		t.Fatalf("explainScriptFile validation error unexpected or leaked secret: %v", expErr)
	}

	// Missing body_file (ResolveBodyRefs failure, non-ValidationError) still returns CLI redaction.
	missingBodyPath := writeTempScript(t, `{
  "name": "cli-missing-body-file",
  "protocol": "http",
  "config": {"base_url": "https://operator:`+urlPwd+`@api.example.test"},
  "actions": [{"name": "post", "type": "request", "method": "POST", "target": "/", "body_file": "missing-`+urlPwd+`.json"}]
}`)
	if _, missingRC, bodyErr := parseScriptFileForCLI(missingBodyPath); bodyErr == nil || missingRC == nil || strings.Contains(scrubCLIText(bodyErr.Error(), missingRC), urlPwd) {
		t.Fatalf("expected non-nil redaction config and redacted error on missing body_file, got rc=%v err=%v", missingRC, bodyErr)
	}

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
	ciValidPath := writeTempScript(t, `{
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
}`)
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
	if ciInvalidErr == nil || ciErrRes.Outcome != ciOutcomeConfigurationError || strings.Contains(ciInvalidErr.Error(), bearerTok) {
		t.Fatalf("executeLocalCI failed to redact literal credential on invalid script: res=%+v err=%v", ciErrRes, ciInvalidErr)
	}

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
	// neutral-named DSL fields.
	const cliEnvSecret = "short-pin-99"
	neutralEnvPath := writeTempScript(t, `{
  "name": "cli-env-neutral-target",
  "protocol": "http",
  "config": {"base_url": "https://api.example.test"},
  "actions": [
    {"name": "fetch-{{env.api_password}}", "type": "request", "method": "GET", "target": "/v1/{{env.api_password}}/orders"}
  ]
}`)
	_, envRC, envLoadErr := loadScriptForCLIExecution(neutralEnvPath, runArgs{
		env: map[string]string{"api_password": cliEnvSecret, "region": "us-east-1"},
	})
	if envLoadErr != nil || envRC == nil {
		t.Fatalf("loadScriptForCLIExecution with -env failed: rc=%v err=%v", envRC, envLoadErr)
	}
	scrubbedEnvMsg := scrubCLIText("action fetch-"+cliEnvSecret+" in us-east-1 failed", envRC)
	if strings.Contains(scrubbedEnvMsg, cliEnvSecret) || !strings.Contains(scrubbedEnvMsg, "us-east-1") {
		t.Fatalf("CLI -env redaction unexpected: %q", scrubbedEnvMsg)
	}

	// Round-5 Finding 3 regression: engine.New receives RedactionConfig: envRC so
	// eng.Run redacts sensitive -env values substituted into neutral DSL surfaces.
	envEchoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, "failed path=%s", r.URL.Path)
	}))
	defer envEchoSrv.Close()
	neutralExecPath := writeTempScript(t, `{
  "name": "cli-env-engine-run",
  "protocol": "http",
  "config": {"base_url": "`+envEchoSrv.URL+`", "run_logic": {"iterations": 1}},
  "actions": [
    {"name": "fetch-orders", "type": "request", "method": "GET", "target": "/v1/{{env.api_password}}/orders"}
  ]
}`)
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
	if strings.Contains(string(snapJSON), cliEnvSecret) {
		t.Fatalf("eng.Run FinalSnapshot leaked CLI -env secret %q: %s", cliEnvSecret, snapJSON)
	}

	// Round-8 Finding 6 regression: validateScriptFile and explainScriptFile
	// scrub credential-bearing Name, GeneratedBy, GeneratedFrom URLs, and Summary.
	const (
		genURLSecret = "synth-gen-url-tok-77"
		namePin      = "synth-name-pin-78"
	)
	metaScriptPath := writeTempScript(t, `{
  "name": "import-`+namePin+`",
  "protocol": "http",
  "generated_by": "importer token=`+namePin+`",
  "generated_from": "https://svc:`+genURLSecret+`@specs.example.test/openapi.json?api_key=`+genURLSecret+`",
  "config": {"base_url": "https://api.example.test"},
  "actions": [
    {"name": "ping", "type": "request", "method": "GET", "target": "/", "headers": {"X-Api-Key": "`+namePin+`"}}
  ]
}`)
	metaVal := validateScriptFile(metaScriptPath)
	metaValJSON, _ := json.Marshal(metaVal)
	if !metaVal.Valid || strings.Contains(string(metaValJSON), namePin) {
		t.Fatalf("validateScriptFile leaked script.Name credential: %s", metaValJSON)
	}
	metaExplain, metaExpErr := explainScriptFile(metaScriptPath)
	if metaExpErr != nil {
		t.Fatalf("explainScriptFile(metaScriptPath): %v", metaExpErr)
	}
	metaExpJSON, _ := json.Marshal(metaExplain)
	for _, secret := range []string{genURLSecret, namePin} {
		if strings.Contains(string(metaExpJSON), secret) {
			t.Fatalf("explainScriptFile leaked %q in metadata/summary: %s", secret, metaExpJSON)
		}
	}

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
	if strings.Contains(string(junitBytes), namePin) {
		t.Fatalf("JUnit report leaked script.Name credential %q: %s", namePin, junitBytes)
	}
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
	if strings.Contains(string(fallbackBytes), namePin) {
		t.Fatalf("JUnit report leaked fallback script filename credential %q: %s", namePin, fallbackBytes)
	}
}
