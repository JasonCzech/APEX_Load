package enterprisearchive

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

func TestDisclosureSafeEnterpriseSourceReviewRedactsWholeWebSetUserPasswordExpression(t *testing.T) {
	t.Parallel()

	const (
		adjacentTailCanary = "koala"
		nestedCanary       = "otter"
	)
	review := BuildSourceReview(map[string]string{
		"Action.c": `
Action()
{
	web_set_user(
		"auth-user",
		"panda" /* source comments may separate C literals */
		"koala",
		"secure.example.test:443");
	web_set_user("auth-user", lr_unmask("otter"), "secure.example.test:443");
	return 0;
}`,
	}, nil)

	disclosureSafeEnterpriseSourceReview(review)

	payload, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range []string{adjacentTailCanary, nestedCanary} {
		if strings.Contains(string(payload), canary) {
			t.Errorf("source review retained web_set_user password material %q: %s", canary, payload)
		}
	}
}

func TestDisclosureSafeEnterpriseSourceReviewRedactsPositionalRuntimeCredentials(t *testing.T) {
	t.Parallel()

	const (
		jndiPassword = "jndi-neutral-canary-52f1"
		jdbcPassword = "jdbc-neutral-canary-76a4"
		jdbcInline   = "jdbc-inline-canary-41e8"
		sapPassword  = "sap-neutral-canary-98c3"
	)
	review := BuildSourceReview(map[string]string{
		"Actions.java": `
env.put(Context.PROVIDER_URL, "smf://broker.example.test:55555");
env.put(Context.SECURITY_CREDENTIALS, "` + jndiPassword + `");
DriverManager.getConnection("jdbc:postgresql://db.example.test/orders", "perf_user", "` + jdbcPassword + `");
DriverManager.getConnection("jdbc:postgresql://embedded:` + jdbcInline + `@db.example.test/orders");
`,
		"Action.c": `
sapgui_logon("001", "{User}", "` + sapPassword + `", "EN", LAST);
sapgui_set_password("wnd[0]/usr/pwdRSYST-BCODE", "` + sapPassword + `", LAST);
`,
	}, nil)

	disclosureSafeEnterpriseSourceReview(review)

	payload, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range []string{jndiPassword, jdbcPassword, jdbcInline, sapPassword} {
		if strings.Contains(string(payload), canary) {
			t.Fatalf("source review retained positional credential %q: %s", canary, payload)
		}
	}
	for _, safe := range []string{
		"smf://broker.example.test:55555",
		"jdbc:postgresql://db.example.test/orders",
		"001",
	} {
		if !strings.Contains(string(payload), safe) {
			t.Fatalf("source review lost noncredential context %q: %s", safe, payload)
		}
	}
}

func TestRedactEnterpriseCredentialParameterData(t *testing.T) {
	t.Parallel()
	const passwordCanary = "parameter-data-password-canary"
	files := map[string]string{
		"flow.prm": `[parameter:UserName]
ParamName="UserName"
ColumnName="UserName"
Table="users.dat"

[parameter:Password]
ParamName="Password"
ColumnName="Password"
Table="passwords.dat"
`,
		"users.dat":     "UserName\nsynthetic-user\n",
		"passwords.dat": "Password\n" + passwordCanary + "\n",
	}
	review := BuildSourceReview(files, nil)

	requestimport.RedactSourceReviewFilesByBaseName(
		review,
		enterpriseProtectedParameterDataFiles(
			[]string{"flow.prm"},
			files,
		),
	)

	payload, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(payload), passwordCanary) {
		t.Fatalf("source review retained credential parameter data: %s", payload)
	}
	if !strings.Contains(string(payload), "synthetic-user") {
		t.Fatalf("source review removed nonsensitive parameter data: %s", payload)
	}
}

func TestReviewOnlyResultPreservesAllEnterpriseSourceContent(t *testing.T) {
	t.Parallel()
	const (
		setUserPassword = "review-only-web-set-user-password-canary"
		urlUser         = "review-only-url-user-canary"
		urlPassword     = "review-only-url-password-canary"
		queryToken      = "review-only-query-token-canary"
		parameterValue  = "review-only-parameter-row-canary"
		preservedValue  = "review-only-prm-value-canary"
	)
	files := map[string]string{
		"Action.c": `Action() {
  web_set_user("synthetic-user", "` + setUserPassword + `", "safe.example.test:443");
  web_custom_request("review",
    "URL=https://` + urlUser + `:` + urlPassword + `@safe.example.test:8443/services/review?api_key=` + queryToken + `",
    "Body=<!-- namespace http://schemas.example.test/review/v1 --><request/>",
    LAST);
  return 0;
}`,
		"flow.prm": `[parameter:Password]
ParamName=Password
ColumnName=Password
Table=passwords.dat
ValueList=` + preservedValue,
		"passwords.dat": "Password\n" + parameterValue + "\n",
	}
	result := newEnterpriseArchiveReviewResult(
		DetectedProtocol{
			CanonicalSourceProtocol: "web_http",
			ImportMode:              ImportModeReviewOnly,
			DisplayName:             "Web HTTP",
		},
		files,
		nil,
		map[string]any{"reason": "review_only_regression"},
	)
	if result.SourceReview == nil {
		t.Fatal("review-only result omitted source review")
	}
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	for _, canary := range []string{
		setUserPassword,
		urlUser,
		urlPassword,
		queryToken,
		parameterValue,
		preservedValue,
	} {
		if !strings.Contains(string(payload), canary) {
			t.Fatalf("review-only result did not preserve credential %q: %s", canary, payload)
		}
	}
	for _, safe := range []string{
		"safe.example.test:8443",
		"/services/review",
		"http://schemas.example.test/review/v1",
	} {
		if !strings.Contains(string(payload), safe) {
			t.Fatalf("review-only result lost safe source URL %q: %s", safe, payload)
		}
	}
}
