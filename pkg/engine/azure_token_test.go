package engine

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type fakeAzureCredential struct {
	calls  int
	token  string
	expiry time.Time
	err    error
	scopes []string
}

func (f *fakeAzureCredential) GetToken(_ context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	f.calls++
	f.scopes = opts.Scopes
	if f.err != nil {
		return azcore.AccessToken{}, f.err
	}
	return azcore.AccessToken{Token: f.token, ExpiresOn: f.expiry}, nil
}

func TestAzureCredentialTokenSource_CachesUntilNearExpiry(t *testing.T) {
	cred := &fakeAzureCredential{token: "tok-1", expiry: time.Now().Add(time.Hour)}
	source := newAzureCredentialTokenSource(cred, "https://vault.azure.net/.default")

	for i := 0; i < 3; i++ {
		token, err := source.Token(context.Background())
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		if token != "tok-1" {
			t.Fatalf("token = %q, want tok-1", token)
		}
	}
	if cred.calls != 1 {
		t.Fatalf("GetToken calls = %d, want 1 (cached)", cred.calls)
	}
	if len(cred.scopes) != 1 || cred.scopes[0] != "https://vault.azure.net/.default" {
		t.Fatalf("scopes = %v, want vault scope", cred.scopes)
	}
}

func TestAzureCredentialTokenSource_RefreshesNearExpiry(t *testing.T) {
	cred := &fakeAzureCredential{token: "tok-1", expiry: time.Now().Add(time.Hour)}
	source := newAzureCredentialTokenSource(cred, "scope")
	if _, err := source.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}

	// Force the cached token inside the refresh-skew window.
	source.mu.Lock()
	source.expiry = time.Now().Add(30 * time.Second)
	source.mu.Unlock()
	cred.token = "tok-2"

	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if token != "tok-2" {
		t.Fatalf("token = %q, want refreshed tok-2", token)
	}
	if cred.calls != 2 {
		t.Fatalf("GetToken calls = %d, want 2", cred.calls)
	}
}

func TestAzureCredentialTokenSource_PropagatesError(t *testing.T) {
	cred := &fakeAzureCredential{err: fmt.Errorf("no identity available")}
	source := newAzureCredentialTokenSource(cred, "scope")
	if _, err := source.Token(context.Background()); err == nil {
		t.Fatal("expected error from credential, got nil")
	}
}

func TestAzureKeyVaultTokenSourceFromEnv_ModeSelection(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		token      string
		wantStatic bool
		wantNil    bool
	}{
		{name: "auto prefers static token", mode: "", token: "abc", wantStatic: true},
		{name: "explicit token mode", mode: "token", token: "abc", wantStatic: true},
		{name: "token mode without token", mode: "token", token: "", wantNil: true},
		{name: "unknown mode", mode: "bogus", token: "abc", wantNil: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_AZURE_KEY_VAULT_AUTH", tc.mode)
			t.Setenv("APEXLOAD_AZURE_KEY_VAULT_TOKEN", tc.token)
			t.Setenv("AZURE_KEY_VAULT_TOKEN", "")

			source := azureKeyVaultTokenSourceFromEnv()
			if tc.wantNil {
				if source != nil {
					t.Fatalf("expected nil source, got %T", source)
				}
				return
			}
			if tc.wantStatic {
				static, ok := source.(staticAzureTokenSource)
				if !ok {
					t.Fatalf("expected static token source, got %T", source)
				}
				if string(static) != tc.token {
					t.Fatalf("static token = %q, want %q", string(static), tc.token)
				}
			}
		})
	}
}

func TestAzureKeyVaultTokenSourceFromEnv_IdentityModeUsesCredential(t *testing.T) {
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_AUTH", "azure_identity")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_TOKEN", "ignored-static")
	t.Setenv("APEXLOAD_AZURE_KEY_VAULT_SCOPE", "https://vault.usgovcloudapi.net/.default")

	source := azureKeyVaultTokenSourceFromEnv()
	if source == nil {
		t.Skip("DefaultAzureCredential could not be constructed in this environment")
	}
	credSource, ok := source.(*azureCredentialTokenSource)
	if !ok {
		t.Fatalf("expected credential token source, got %T", source)
	}
	if credSource.scope != "https://vault.usgovcloudapi.net/.default" {
		t.Fatalf("scope = %q, want sovereign-cloud override", credSource.scope)
	}
}
