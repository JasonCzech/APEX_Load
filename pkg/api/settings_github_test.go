package api

import "testing"

func TestDefaultSystemSettingsIncludesGitHubDefaults(t *testing.T) {
	gh := defaultSystemSettings(10).GitHub
	if gh.Enabled {
		t.Fatal("github integration should default to disabled")
	}
	if gh.APIBaseURL != defaultGitHubAPIBaseURL || gh.WebBaseURL != defaultGitHubWebBaseURL {
		t.Fatalf("unexpected default base URLs: %+v", gh)
	}
	if gh.DefaultBranch != defaultGitHubBranch || gh.PathTemplate != defaultGitHubPathTmpl {
		t.Fatalf("unexpected default branch/path: %+v", gh)
	}
	if len(gh.Scopes) != 1 || gh.Scopes[0] != "repo" {
		t.Fatalf("unexpected default scopes: %v", gh.Scopes)
	}
}

func TestNormalizeGitHubSettings(t *testing.T) {
	in := GitHubSettings{
		Enabled:    true,
		ClientID:   "  abc  ",
		APIBaseURL: "https://ghe.example.com/api/v3/",
		WebBaseURL: "https://ghe.example.com/",
		Scopes:     []string{"Repo", "repo", " ", "Workflow"},
	}
	out := normalizeGitHubSettings(in)
	if out.ClientID != "abc" {
		t.Fatalf("client id = %q, want trimmed", out.ClientID)
	}
	if out.APIBaseURL != "https://ghe.example.com/api/v3" || out.WebBaseURL != "https://ghe.example.com" {
		t.Fatalf("base URLs not trimmed of trailing slash: %+v", out)
	}
	if out.DefaultBranch != defaultGitHubBranch || out.PathTemplate != defaultGitHubPathTmpl {
		t.Fatalf("defaults not applied: %+v", out)
	}
	if len(out.Scopes) != 2 || out.Scopes[0] != "repo" || out.Scopes[1] != "workflow" {
		t.Fatalf("scopes not normalized/deduped: %v", out.Scopes)
	}
	if hosts := out.allowlistHosts(); len(hosts) != 2 || hosts[0] != "ghe.example.com" {
		t.Fatalf("allowlist hosts = %v, want ghe.example.com twice", hosts)
	}
}

func TestValidateGitHubSettings(t *testing.T) {
	if err := validateGitHubSettings(GitHubSettings{Enabled: false}); err != nil {
		t.Fatalf("disabled github should validate, got: %v", err)
	}
	if err := validateGitHubSettings(GitHubSettings{Enabled: true}); err == nil {
		t.Fatal("enabled github without client_id should fail")
	}
	if err := validateGitHubSettings(GitHubSettings{Enabled: true, ClientID: "x"}); err == nil {
		t.Fatal("enabled github without client_secret_env_var should fail")
	}
	bad := GitHubSettings{Enabled: true, ClientID: "x", ClientSecretEnvVar: "S", APIBaseURL: "ftp://x", WebBaseURL: "https://ghe"}
	if err := validateGitHubSettings(bad); err == nil {
		t.Fatal("invalid api base url should fail")
	}
	insecure := GitHubSettings{Enabled: true, PATEnvVar: "GH_PAT", APIBaseURL: "http://ghe/api/v3", WebBaseURL: "http://ghe"}
	if err := validateGitHubSettings(insecure); err == nil {
		t.Fatal("cleartext github credential transport should fail")
	}
	userinfo := GitHubSettings{Enabled: true, PATEnvVar: "GH_PAT", APIBaseURL: "https://token@ghe/api/v3", WebBaseURL: "https://ghe"}
	if err := validateGitHubSettings(userinfo); err == nil {
		t.Fatal("github URL userinfo should fail")
	}
	ok := GitHubSettings{Enabled: true, ClientID: "x", ClientSecretEnvVar: "S", APIBaseURL: "https://ghe/api/v3", WebBaseURL: "https://ghe"}
	if err := validateGitHubSettings(ok); err != nil {
		t.Fatalf("valid enabled github should pass, got: %v", err)
	}
	app := GitHubSettings{Enabled: true, AppID: 123, AppPrivateKeyEnvVar: "APP_KEY", APIBaseURL: "https://ghe/api/v3", WebBaseURL: "https://ghe"}
	if err := validateGitHubSettings(app); err != nil {
		t.Fatalf("valid app github should pass, got: %v", err)
	}
	pat := GitHubSettings{Enabled: true, PATEnvVar: "GH_PAT", APIBaseURL: "https://ghe/api/v3", WebBaseURL: "https://ghe"}
	if err := validateGitHubSettings(pat); err != nil {
		t.Fatalf("valid PAT fallback github should pass, got: %v", err)
	}
	badMode := GitHubSettings{Enabled: true, PATEnvVar: "GH_PAT", AutomationMode: "magic", APIBaseURL: "https://ghe/api/v3", WebBaseURL: "https://ghe"}
	if err := validateGitHubSettings(badMode); err == nil {
		t.Fatal("invalid automation mode should fail")
	}
}

func TestGitHubClientSecretAndOAuthConfigured(t *testing.T) {
	t.Setenv("APEXLOAD_TEST_GH_SECRET", "super-secret")
	gh := GitHubSettings{Enabled: true, ClientID: "cid", ClientSecretEnvVar: "APEXLOAD_TEST_GH_SECRET"}
	if gh.clientSecret() != "super-secret" {
		t.Fatalf("clientSecret() = %q", gh.clientSecret())
	}
	if !gh.oauthConfigured() {
		t.Fatal("oauthConfigured() should be true when enabled + id + secret present")
	}
	gh.Enabled = false
	if gh.oauthConfigured() {
		t.Fatal("oauthConfigured() should be false when disabled")
	}
}
