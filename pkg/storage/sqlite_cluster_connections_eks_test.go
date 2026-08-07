package storage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func eksConnection(id, name string) *ClusterConnection {
	return &ClusterConnection{
		ID:                   id,
		Name:                 name,
		Kind:                 ClusterKindEKS,
		APIServerURL:         "https://ABCDEF0123456789.gr7.us-east-1.eks.amazonaws.com",
		Namespace:            "apex",
		GeneratorStatefulSet: "apexload-generator",
		TLSMode:              ClusterTLSSystemRoots,
		AuthMode:             ClusterAuthAWSIAM,
		AWSRegion:            "us-east-1",
		AWSClusterName:       "prod-perf",
		AWSCredentialMode:    AWSCredentialModeStaticKeys,
		AWSAccessKeyID:       "AKIAIOSFODNN7EXAMPLE",
		AWSSTSEndpoint:       "https://sts.us-east-1.amazonaws.com",
		// Every envelope field gets a DISTINCT value so a transposition between
		// the SELECT projection and the Scan target list fails loudly instead of
		// silently swapping one credential for another.
		AWSSecretKeyCiphertext:  "aws-secret-cipher",
		AWSSecretKeyWrappedKey:  `{"wrapped":"aws-secret-key"}`,
		AWSExternalIDCiphertext: "aws-external-cipher",
		AWSExternalIDWrappedKey: `{"wrapped":"aws-external-key"}`,
		Labels:                  map[string]string{"env": "perf"},
	}
}

// TestEKSClusterConnectionRoundTrip gives every new column a distinct value, so
// a mismatch between clusterConnectionSelect and scanClusterConnection cannot
// pass. It also proves Get and List share one projection.
func TestEKSClusterConnectionRoundTrip(t *testing.T) {
	store := newClusterTestStore(t)
	conn := eksConnection("cluster-eks", "us-east-1-perf")
	if err := store.CreateClusterConnection(conn); err != nil {
		t.Fatalf("CreateClusterConnection: %v", err)
	}

	loaded, err := store.GetClusterConnection("cluster-eks")
	if err != nil {
		t.Fatalf("GetClusterConnection: %v", err)
	}
	for _, field := range []struct{ name, got, want string }{
		{"kind", loaded.Kind, ClusterKindEKS},
		{"auth_mode", loaded.AuthMode, ClusterAuthAWSIAM},
		{"aws_region", loaded.AWSRegion, "us-east-1"},
		{"aws_cluster_name", loaded.AWSClusterName, "prod-perf"},
		{"aws_credential_mode", loaded.AWSCredentialMode, AWSCredentialModeStaticKeys},
		{"aws_access_key_id", loaded.AWSAccessKeyID, "AKIAIOSFODNN7EXAMPLE"},
		{"aws_sts_endpoint", loaded.AWSSTSEndpoint, "https://sts.us-east-1.amazonaws.com"},
		{"aws_secret_key_ciphertext", loaded.AWSSecretKeyCiphertext, "aws-secret-cipher"},
		{"aws_secret_key_wrapped_key", loaded.AWSSecretKeyWrappedKey, `{"wrapped":"aws-secret-key"}`},
		{"aws_external_id_ciphertext", loaded.AWSExternalIDCiphertext, "aws-external-cipher"},
		{"aws_external_id_wrapped_key", loaded.AWSExternalIDWrappedKey, `{"wrapped":"aws-external-key"}`},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}

	listed, err := store.ListClusterConnections()
	if err != nil {
		t.Fatalf("ListClusterConnections: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d connections, want 1", len(listed))
	}
	if !reflect.DeepEqual(listed[0], loaded) {
		t.Errorf("List and Get projections drifted:\n list = %+v\n  get = %+v", listed[0], loaded)
	}
}

// TestClusterConnectionEditPreservesAWSEnvelopes pins that an unrelated edit
// does not blank the AWS credential columns. UpdateClusterConnectionIfUnchanged
// writes exactly what it is given, so omitting the new columns from its UPDATE
// list would silently destroy the stored credential on every rename.
func TestClusterConnectionEditPreservesAWSEnvelopes(t *testing.T) {
	store := newClusterTestStore(t)
	conn := eksConnection("cluster-eks", "us-east-1-perf")
	if err := store.CreateClusterConnection(conn); err != nil {
		t.Fatalf("CreateClusterConnection: %v", err)
	}
	stored, err := store.GetClusterConnection("cluster-eks")
	if err != nil {
		t.Fatalf("GetClusterConnection: %v", err)
	}

	stored.Name = "renamed"
	ok, err := store.UpdateClusterConnectionIfUnchanged(stored, stored.UpdatedAt)
	if err != nil || !ok {
		t.Fatalf("UpdateClusterConnectionIfUnchanged = (%v, %v), want (true, nil)", ok, err)
	}

	reloaded, err := store.GetClusterConnection("cluster-eks")
	if err != nil {
		t.Fatalf("GetClusterConnection: %v", err)
	}
	if reloaded.Name != "renamed" {
		t.Fatalf("Name = %q, want the edit to apply", reloaded.Name)
	}
	if reloaded.AWSSecretKeyCiphertext != "aws-secret-cipher" || reloaded.AWSSecretKeyWrappedKey != `{"wrapped":"aws-secret-key"}` {
		t.Errorf("a rename cleared the AWS secret-key envelope: %+v", reloaded)
	}
	if reloaded.AWSExternalIDCiphertext != "aws-external-cipher" || reloaded.AWSExternalIDWrappedKey != `{"wrapped":"aws-external-key"}` {
		t.Errorf("a rename cleared the AWS external-ID envelope: %+v", reloaded)
	}
	if reloaded.AWSRegion != "us-east-1" || reloaded.AWSClusterName != "prod-perf" {
		t.Errorf("a rename cleared the AWS identifiers: %+v", reloaded)
	}
}

// TestEKSClusterConnectionNeverSerializesCredentials extends the existing
// non-serialization guarantee to the two new envelope slots.
func TestEKSClusterConnectionNeverSerializesCredentials(t *testing.T) {
	conn := eksConnection("cluster-eks", "us-east-1-perf")
	raw, err := json.Marshal(conn)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	encoded := string(raw)
	for _, secret := range []string{
		"aws-secret-cipher", "aws-secret-key", "aws-external-cipher", "aws-external-key",
	} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("ClusterConnection JSON leaked %q: %s", secret, encoded)
		}
	}
	// The non-secret AWS identifiers are expected on the wire.
	for _, shown := range []string{"us-east-1", "prod-perf", "AKIAIOSFODNN7EXAMPLE"} {
		if !strings.Contains(encoded, shown) {
			t.Errorf("ClusterConnection JSON omitted %q, which the admin UI needs: %s", shown, encoded)
		}
	}
}

func TestClusterConnectionCredentialSourceForWorkloadIdentity(t *testing.T) {
	conn := eksConnection("cluster-eks", "us-east-1-perf")
	conn.AWSCredentialMode = AWSCredentialModeAmbient
	conn.AWSAccessKeyID = ""
	conn.AWSSecretKeyCiphertext = ""
	conn.AWSSecretKeyWrappedKey = ""
	if got := conn.CredentialSource(); got != "workload_identity" {
		t.Fatalf("CredentialSource() = %q, want workload_identity", got)
	}
	if conn.HasStoredCredential() {
		t.Error("HasStoredCredential() = true for an ambient connection with no stored key")
	}

	// An external ID alone must not read as "a secret is set" -- that would
	// misdescribe how the connection authenticates.
	conn.AWSExternalIDCiphertext = "aws-external-cipher"
	if conn.HasStoredCredential() {
		t.Error("HasStoredCredential() = true when only an external ID is stored")
	}
}

func TestClusterConnectionValidateAWSIAM(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*ClusterConnection)
		wantErr bool
	}{
		{name: "static keys", mutate: nil},
		{
			name: "ambient needs no stored key",
			mutate: func(c *ClusterConnection) {
				c.AWSCredentialMode = AWSCredentialModeAmbient
				c.AWSAccessKeyID = ""
				c.AWSSecretKeyCiphertext = ""
			},
		},
		{
			name: "assume role chained from ambient",
			mutate: func(c *ClusterConnection) {
				c.AWSCredentialMode = AWSCredentialModeAssumeRole
				c.AWSRoleARN = "arn:aws:iam::123456789012:role/apexload-eks"
				c.AWSAccessKeyID = ""
				c.AWSSecretKeyCiphertext = ""
			},
		},
		{
			name: "a secret supplied by env var counts",
			mutate: func(c *ClusterConnection) {
				c.AWSSecretKeyCiphertext = ""
				c.AWSSecretKeyWrappedKey = ""
				c.AWSExternalIDCiphertext = ""
				c.AWSExternalIDWrappedKey = ""
				c.ClientSecretEnvVar = "APEXLOAD_EKS_SECRET"
			},
		},

		{name: "missing region", mutate: func(c *ClusterConnection) { c.AWSRegion = "" }, wantErr: true},
		{name: "malformed region", mutate: func(c *ClusterConnection) { c.AWSRegion = "US East 1" }, wantErr: true},
		{name: "missing cluster name", mutate: func(c *ClusterConnection) { c.AWSClusterName = "" }, wantErr: true},
		{
			name:    "cluster name with a CRLF header injection",
			mutate:  func(c *ClusterConnection) { c.AWSClusterName = "prod\r\nX-Evil: 1" },
			wantErr: true,
		},
		{
			name:    "aws IAM on a non-eks kind",
			mutate:  func(c *ClusterConnection) { c.Kind = ClusterKindGeneric },
			wantErr: true,
		},
		{
			name:    "unknown credential mode",
			mutate:  func(c *ClusterConnection) { c.AWSCredentialMode = "instance_profile" },
			wantErr: true,
		},
		{
			name: "static keys without a secret",
			mutate: func(c *ClusterConnection) {
				c.AWSSecretKeyCiphertext = ""
				c.AWSSecretKeyWrappedKey = ""
			},
			wantErr: true,
		},
		{
			name: "ambient combined with a static key",
			mutate: func(c *ClusterConnection) {
				c.AWSCredentialMode = AWSCredentialModeAmbient
			},
			wantErr: true,
		},
		{
			name: "assume role with a malformed ARN",
			mutate: func(c *ClusterConnection) {
				c.AWSCredentialMode = AWSCredentialModeAssumeRole
				c.AWSRoleARN = "arn:aws:iam::123:role/short-account"
			},
			wantErr: true,
		},
		{
			name:    "STS endpoint impersonating AWS",
			mutate:  func(c *ClusterConnection) { c.AWSSTSEndpoint = "https://sts.us-east-1.amazonaws.com.evil.example" },
			wantErr: true,
		},
		{
			name:    "STS endpoint in another region",
			mutate:  func(c *ClusterConnection) { c.AWSSTSEndpoint = "https://sts.eu-west-1.amazonaws.com" },
			wantErr: true,
		},
		{
			name:    "plaintext STS endpoint",
			mutate:  func(c *ClusterConnection) { c.AWSSTSEndpoint = "http://sts.us-east-1.amazonaws.com" },
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := eksConnection("cluster-eks", "us-east-1-perf")
			// The env-var alternative is mutually exclusive with a stored
			// credential, so the base fixture clears it before each mutation.
			if tc.mutate != nil {
				tc.mutate(conn)
			}
			err := conn.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// TestClusterConnectionsDeclareNoIntegerColumns pins the deliberate all-TEXT
// choice. looksBooleanColumn (the Postgres rewrite guard) matches names like
// has_* and *_enabled but would NOT catch something like aws_ambient INTEGER,
// which would silently break Postgres. Keeping the table free of INTEGER
// columns sidesteps the whole class.
func TestClusterConnectionsDeclareNoIntegerColumns(t *testing.T) {
	for _, statement := range schemaCreateTableStatements() {
		if !strings.Contains(statement, "CREATE TABLE IF NOT EXISTS cluster_connections") {
			continue
		}
		for _, line := range strings.Split(statement, "\n") {
			if strings.Contains(strings.ToUpper(line), " INTEGER") {
				t.Errorf("cluster_connections declares an INTEGER column, which escapes the Postgres boolean rewrite guard: %s", strings.TrimSpace(line))
			}
		}
		return
	}
	t.Fatal("cluster_connections CREATE TABLE statement not found")
}
