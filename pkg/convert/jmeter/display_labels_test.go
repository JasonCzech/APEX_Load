package jmeter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertJMXPreservesCredentialsInTestPlanAndThreadGroupLabels(t *testing.T) {
	const password = "jmeter-label-password-canary-5f42"
	tests := []struct {
		name string
		jmx  string
	}{
		{
			name: "test plan without thread group",
			jmx: fmt.Sprintf(`<jmeterTestPlan version="1.2"><hashTree>
  <TestPlan testname="Plan %s"/><hashTree>
    <AuthManager testname="Auth"><collectionProp name="AuthManager.auth_list">
      <elementProp name="" elementType="Authorization">
        <stringProp name="Authorization.username">operator</stringProp>
        <stringProp name="Authorization.password">%s</stringProp>
        <stringProp name="Authorization.mechanism">BASIC</stringProp>
      </elementProp>
    </collectionProp></AuthManager><hashTree/>
    <HTTPSamplerProxy testname="Request %s">
      <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
      <stringProp name="HTTPSampler.protocol">https</stringProp>
      <stringProp name="HTTPSampler.path">/home</stringProp>
      <stringProp name="HTTPSampler.method">GET</stringProp>
    </HTTPSamplerProxy><hashTree/>
  </hashTree>
</hashTree></jmeterTestPlan>`, password, password, password),
		},
		{
			name: "thread group",
			jmx: fmt.Sprintf(`<jmeterTestPlan version="1.2"><hashTree>
  <TestPlan testname="Plan"/><hashTree>
    <ThreadGroup testname="Users %s">
      <stringProp name="ThreadGroup.num_threads">3</stringProp>
      <stringProp name="ThreadGroup.ramp_time">1</stringProp>
    </ThreadGroup><hashTree>
      <AuthManager testname="Auth"><collectionProp name="AuthManager.auth_list">
        <elementProp name="" elementType="Authorization">
          <stringProp name="Authorization.username">operator</stringProp>
          <stringProp name="Authorization.password">%s</stringProp>
          <stringProp name="Authorization.mechanism">BASIC</stringProp>
        </elementProp>
      </collectionProp></AuthManager><hashTree/>
      <HTTPSamplerProxy testname="Request %s">
        <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
        <stringProp name="HTTPSampler.protocol">https</stringProp>
        <stringProp name="HTTPSampler.path">/home</stringProp>
        <stringProp name="HTTPSampler.method">GET</stringProp>
      </HTTPSamplerProxy><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`, password, password, password),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normalizedPassword := strings.ReplaceAll(password, "-", "_")
			result, err := ConvertJMX([]byte(test.jmx), ConvertOptions{
				FileName:      "labels.jmx",
				ScenarioDraft: true,
			})
			if err != nil {
				t.Fatalf("ConvertJMX() error = %v", err)
			}
			if len(result.Scripts) != 1 || result.ScenarioDraft == nil ||
				len(result.ScenarioDraft.Groups) != 1 {
				t.Fatalf("unexpected conversion result: %#v", result)
			}
			script := result.Scripts[0]
			if !strings.Contains(script.Name, normalizedPassword) {
				t.Fatalf("script display name lost imported password: %q", script.Name)
			}
			if !strings.Contains(script.Metadata.Import.SourceScriptName, password) {
				t.Fatalf("source script display name lost imported password: %q", script.Metadata.Import.SourceScriptName)
			}
			if !strings.Contains(script.Actions[0].Name, normalizedPassword) {
				t.Fatalf("action display name lost imported password: %q", script.Actions[0].Name)
			}
			group := result.ScenarioDraft.Groups[0]
			if !strings.Contains(group.Name, password) ||
				!strings.Contains(group.ScriptName, normalizedPassword) {
				t.Fatalf("scenario group lost imported password: %#v", group)
			}
			if group.ScriptName != script.Name {
				t.Fatalf("group script_name = %q, want sanitized script name %q", group.ScriptName, script.Name)
			}
			if script.Config.HTTP.Auth.Password != password {
				t.Fatalf("executable auth password = %#v, want exact imported value", script.Config.HTTP.Auth)
			}
		})
	}
}

func TestConvertJMXPreservesCredentialsInActionReviewMetadata(t *testing.T) {
	const password = "jmeter-metadata-password-canary-9c42"
	jmx := fmt.Sprintf(`<jmeterTestPlan version="1.2"><hashTree>
  <TestPlan testname="Plan"/><hashTree>
    <ThreadGroup testname="Users"/><hashTree>
      <AuthManager testname="%s"><collectionProp name="AuthManager.auth_list">
        <elementProp name="" elementType="Authorization">
          <stringProp name="Authorization.username">operator</stringProp>
          <stringProp name="Authorization.password">%s</stringProp>
          <stringProp name="Authorization.mechanism">KERBEROS</stringProp>
        </elementProp>
      </collectionProp></AuthManager><hashTree/>
      <TransactionController testname="%s"/>
      <hashTree>
        <HTTPSamplerProxy testname="Request">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/home</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy><hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`, password, password, password)

	result, err := ConvertJMX([]byte(jmx), ConvertOptions{
		FileName: "metadata-labels.jmx",
		BaseURL:  "https://api.example.com",
	})
	if err != nil {
		t.Fatalf("ConvertJMX: %v", err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 {
		t.Fatalf("conversion result = %#v, want one script/action", result)
	}
	action := result.Scripts[0].Actions[0]
	if action.Auth == nil || action.Auth.Password != password {
		t.Fatalf("executable auth = %#v, want exact imported password", action.Auth)
	}
	metadata, err := json.Marshal(action.Metadata)
	if err != nil {
		t.Fatalf("json.Marshal metadata: %v", err)
	}
	if !strings.Contains(string(metadata), password) {
		t.Fatalf("descriptive action metadata lost imported password: %s", metadata)
	}
	if action.Metadata["execution_blocker"] != "import_review_required" {
		t.Fatalf("credential sanitization changed execution gate: %#v", action.Metadata)
	}
}

func TestSanitizeJMeterResultReviewTextUsesExecutableCredentialSource(t *testing.T) {
	const password = "jmeter-review-label-canary-a124"
	result := &ConvertResult{
		Scripts: []*dsl.Script{{
			Name:     "Runnable",
			Protocol: "http",
			Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type:     "basic",
				Username: "operator",
				Password: password,
			}}},
		}},
		Warnings:    []string{"Thread group " + password + " requires review"},
		Assumptions: []string{"Imported label " + password},
		Unsupported: diag.Diagnostics{{
			Element:  "JMeter controller",
			Detail:   "Controller " + password + " was review-gated",
			Location: password,
		}},
		Findings: []importreview.Finding{{
			Code:      "review_required",
			Detail:    "Review " + password,
			SourceRef: "labels.jmx#" + password,
		}},
	}

	sanitizeJMeterResultReviewText(result)
	payload, err := json.Marshal(map[string]any{
		"warnings":    result.Warnings,
		"assumptions": result.Assumptions,
		"unsupported": result.Unsupported,
		"findings":    result.Findings,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(payload), password) {
		t.Fatalf("projected review response retained credential: %s", payload)
	}
	if result.Scripts[0].Config.HTTP.Auth.Password != password {
		t.Fatalf("review projection changed executable password to %q", result.Scripts[0].Config.HTTP.Auth.Password)
	}
}

func TestSanitizeDisplayLabelsPreservesCredentialEqualToRedactionSentinel(t *testing.T) {
	script := &dsl.Script{
		Name:     credential.Redacted,
		Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
			Type:     "basic",
			Username: "operator",
			Password: credential.Redacted,
		}}},
		Actions: []dsl.Action{{
			Name:   credential.Redacted,
			Type:   "GET",
			Method: "GET",
			Target: "/home",
		}},
	}
	draft := &ScenarioDraft{
		Name: credential.Redacted,
		Groups: []ScenarioDraftGroup{{
			Name:       credential.Redacted,
			ScriptName: credential.Redacted,
		}},
	}

	SanitizeDisplayLabels([]*dsl.Script{script}, draft)
	if script.Config.HTTP.Auth.Password != credential.Redacted {
		t.Fatalf("executable password = %q, want exact sentinel-valued source credential", script.Config.HTTP.Auth.Password)
	}
	for _, label := range []string{
		script.Name,
		script.Actions[0].Name,
		draft.Name,
		draft.Groups[0].Name,
		draft.Groups[0].ScriptName,
	} {
		if label == credential.Redacted {
			t.Fatalf("credential-valued display label was not replaced: %q", label)
		}
	}
	if draft.Groups[0].ScriptName != script.Name {
		t.Fatalf("scenario script_name = %q, want %q", draft.Groups[0].ScriptName, script.Name)
	}
}

func TestSanitizeDisplayLabelsAllocatesRepeatedNamesAndRewritesEveryScenarioLane(t *testing.T) {
	scripts := []*dsl.Script{
		{Name: "Users", Protocol: "http"},
		{Name: "users", Protocol: "http"},
		{Name: "Users", Protocol: "http"},
	}
	draft := &ScenarioDraft{Groups: []ScenarioDraftGroup{
		{ScriptName: "Users"},
		{ScriptName: "users"},
		{ScriptName: "Users"},
	}}

	SanitizeDisplayLabels(scripts, draft)

	wantNames := []string{"Users", "users_2", "Users_3"}
	for index, want := range wantNames {
		if scripts[index].Name != want {
			t.Fatalf("script %d name = %q, want %q", index, scripts[index].Name, want)
		}
		if draft.Groups[index].ScriptName != want {
			t.Fatalf("scenario group %d script_name = %q, want %q", index, draft.Groups[index].ScriptName, want)
		}
	}
}
