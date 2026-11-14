package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func sessionIdentityOverride(identities ...dsl.BrowserIdentity) *RuntimeSettingsOverride {
	return &RuntimeSettingsOverride{BrowserConfig: &dsl.BrowserConfig{Session: &dsl.BrowserSessionConfig{
		Strategy: "login_steps", Identities: identities,
	}}}
}

// saveIdentityEdit stores first, masks it for the editor, lets edit build the
// next submission from the masked identities, and saves it the way a scenario
// save does. It returns the stored values of both saves keyed by username.
func saveIdentityEdit(t *testing.T, first *RuntimeSettingsOverride, edit func(masked []dsl.BrowserIdentity) []dsl.BrowserIdentity) (map[string]string, map[string]string, []dsl.BrowserIdentity) {
	t.Helper()
	stored, firstValues, _, err := protectLegacyRuntimeOverrideSecrets("scn-1", 0, nil, first)
	if err != nil {
		t.Fatal(err)
	}
	priorByUser := map[string]string{}
	for _, identity := range stored.BrowserConfig.Session.Identities {
		priorByUser[identity.Username] = firstValues[strings.TrimSuffix(strings.TrimPrefix(identity.Password, "{{secret."), "}}")]
	}
	masked := maskRuntimeSettingsForEditor(stored)
	incoming := sessionIdentityOverride(edit(masked.BrowserConfig.Session.Identities)...)
	reconciled := reconcileGroupRuntimeSecrets(incoming, stored)
	next, nextValues, _, err := protectLegacyRuntimeOverrideSecrets("scn-1", 0, nil, reconciled)
	if err != nil {
		t.Fatal(err)
	}
	// Persist: the stored secret values after the second save are the first
	// save's plus the second save's (which overwrite by name).
	all := map[string]string{}
	for name, value := range firstValues {
		all[name] = value
	}
	for name, value := range nextValues {
		all[name] = value
	}
	nextByUser := map[string]string{}
	for _, identity := range next.BrowserConfig.Session.Identities {
		nextByUser[identity.Username] = all[strings.TrimSuffix(strings.TrimPrefix(identity.Password, "{{secret."), "}}")]
	}
	return priorByUser, nextByUser, next.BrowserConfig.Session.Identities
}

func TestBrowserSessionIdentitySecretsSurviveListEdits(t *testing.T) {
	cases := map[string]struct {
		first []dsl.BrowserIdentity
		edit  func(masked []dsl.BrowserIdentity) []dsl.BrowserIdentity
		want  map[string]string
	}{
		"insert at front": {
			first: []dsl.BrowserIdentity{{Username: "alice", Password: "alice-pw-1"}},
			edit: func(masked []dsl.BrowserIdentity) []dsl.BrowserIdentity {
				return []dsl.BrowserIdentity{{Username: "bob", Password: "bob-pw-2"}, masked[0]}
			},
			want: map[string]string{"alice": "alice-pw-1", "bob": "bob-pw-2"},
		},
		"delete first then add": {
			first: []dsl.BrowserIdentity{{Username: "alice", Password: "alice-pass"}, {Username: "bob", Password: "bob-pass"}},
			edit: func(masked []dsl.BrowserIdentity) []dsl.BrowserIdentity {
				return []dsl.BrowserIdentity{masked[1], {Username: "dave", Password: "dave-pass"}}
			},
			want: map[string]string{"bob": "bob-pass", "dave": "dave-pass"},
		},
		"reorder": {
			first: []dsl.BrowserIdentity{{Username: "alice", Password: "alice-pass"}, {Username: "bob", Password: "bob-pass"}},
			edit: func(masked []dsl.BrowserIdentity) []dsl.BrowserIdentity {
				return []dsl.BrowserIdentity{masked[1], masked[0]}
			},
			want: map[string]string{"alice": "alice-pass", "bob": "bob-pass"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, got, identities := saveIdentityEdit(t, sessionIdentityOverride(tc.first...), tc.edit)
			if identities[0].Password == identities[1].Password {
				t.Fatalf("two identities share one sealed reference %s", identities[0].Password)
			}
			for user, want := range tc.want {
				if got[user] != want {
					t.Errorf("%s credential = %q, want %q", user, got[user], want)
				}
			}
		})
	}
}

func TestBrowserSessionIdentityStorageStateKeyedByUsername(t *testing.T) {
	first := sessionIdentityOverride(dsl.BrowserIdentity{Username: "alice", StorageState: `{"cookies":[{"value":"a"}]}`})
	stored, _, _, err := protectLegacyRuntimeOverrideSecrets("scn-1", 0, nil, first)
	if err != nil {
		t.Fatal(err)
	}
	masked := maskRuntimeSettingsForEditor(stored)
	if masked.BrowserConfig.Session.Identities[0].StorageState != scrub.Redacted {
		t.Fatalf("storage state not masked: %q", masked.BrowserConfig.Session.Identities[0].StorageState)
	}
	incoming := sessionIdentityOverride(dsl.BrowserIdentity{Username: "bob", StorageState: `{"cookies":[{"value":"b"}]}`}, masked.BrowserConfig.Session.Identities[0])
	next, _, _, err := protectLegacyRuntimeOverrideSecrets("scn-1", 0, nil, reconcileGroupRuntimeSecrets(incoming, stored))
	if err != nil {
		t.Fatal(err)
	}
	ids := next.BrowserConfig.Session.Identities
	if ids[0].StorageState == ids[1].StorageState || ids[1].StorageState != stored.BrowserConfig.Session.Identities[0].StorageState {
		t.Errorf("storage state refs = %q / %q, alice's stored ref %q", ids[0].StorageState, ids[1].StorageState, stored.BrowserConfig.Session.Identities[0].StorageState)
	}
}

// A literal is never sealed under a name another leaf already references,
// whatever path derived it (here an empty-username identity, which falls
// back to its index, colliding with a kept reference at that index name).
func TestProtectNeverSealsUnderAHeldReference(t *testing.T) {
	heldName := runtimeOverrideSecretName("scn-1", 0, "browser.session.identities.1.password")
	incoming := sessionIdentityOverride(
		dsl.BrowserIdentity{Username: "kept", Password: "{{secret." + heldName + "}}"},
		dsl.BrowserIdentity{Username: "", Password: "new-pw"},
	)
	next, values, _, err := protectLegacyRuntimeOverrideSecrets("scn-1", 0, nil, incoming)
	if err != nil {
		t.Fatal(err)
	}
	ids := next.BrowserConfig.Session.Identities
	if ids[0].Password != "{{secret."+heldName+"}}" || ids[1].Password == ids[0].Password {
		t.Fatalf("identities = %+v", ids)
	}
	if _, overwritten := values[heldName]; overwritten {
		t.Errorf("new literal overwrote held secret %s", heldName)
	}
}

// A deferred [REDACTED] sentinel records no value, so it keeps its own path
// name even when another leaf already holds that name: diverting it to a
// path#N name would reference a secret that was never written.
func TestProtectDeferredSentinelKeepsItsOwnName(t *testing.T) {
	ownName := runtimeOverrideSecretName("scn-1", 0, "browser.session.identities.1.password")
	incoming := sessionIdentityOverride(
		dsl.BrowserIdentity{Username: "kept", Password: "{{secret." + ownName + "}}"},
		dsl.BrowserIdentity{Username: "", Password: "[REDACTED]"},
	)
	next, values, _, err := protectStoredRuntimeOverrideSecrets("scn-1", 0, nil, incoming)
	if err != nil {
		t.Fatal(err)
	}
	ids := next.BrowserConfig.Session.Identities
	if ids[1].Password != "{{secret."+ownName+"}}" {
		t.Fatalf("deferred sentinel = %q, want {{secret.%s}}", ids[1].Password, ownName)
	}
	if len(values) != 0 {
		t.Errorf("deferred sentinel recorded values %v", values)
	}
}

func TestKafkaBrokerSentinelsRefusedAfterListEdit(t *testing.T) {
	first := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Brokers: []string{"sasl://u0:p0@b0:9092", "sasl://u1:p1@b1:9092"}}}
	stored, _, _, err := protectLegacyRuntimeOverrideSecrets("scn-2", 0, nil, first)
	if err != nil {
		t.Fatal(err)
	}
	masked := maskRuntimeSettingsForEditor(stored)
	if masked.Kafka.Brokers[0] != scrub.Redacted || masked.Kafka.Brokers[1] != scrub.Redacted {
		t.Fatalf("editor brokers = %v", masked.Kafka.Brokers)
	}
	for name, brokers := range map[string][]string{
		"delete first then add": {masked.Kafka.Brokers[1], "sasl://u2:p2@b2:9092"},
		"replace beside mask":   {"sasl://u2:p2@b2:9092", masked.Kafka.Brokers[1]},
		"delete one":            {masked.Kafka.Brokers[1]},
	} {
		incoming := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Brokers: brokers}}
		_, _, _, err := protectLegacyRuntimeOverrideSecrets("scn-2", 0, nil, reconcileGroupRuntimeSecrets(incoming, stored))
		if err == nil || !strings.Contains(err.Error(), "re-enter the credential brokers") {
			t.Errorf("%s: save accepted an unmatched broker sentinel (err %v)", name, err)
		}
	}
	// An unchanged list (only masks) still restores by position.
	unchanged := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Brokers: append([]string(nil), masked.Kafka.Brokers...)}}
	next, _, _, err := protectLegacyRuntimeOverrideSecrets("scn-2", 0, nil, reconcileGroupRuntimeSecrets(unchanged, stored))
	if err != nil {
		t.Fatal(err)
	}
	if next.Kafka.Brokers[0] != stored.Kafka.Brokers[0] || next.Kafka.Brokers[1] != stored.Kafka.Brokers[1] {
		t.Errorf("unchanged list = %v, want %v", next.Kafka.Brokers, stored.Kafka.Brokers)
	}
	// Re-entering every broker as literals saves.
	reentered := &RuntimeSettingsOverride{Kafka: &dsl.KafkaConfig{Brokers: []string{"sasl://u1:p1@b1:9092", "sasl://u2:p2@b2:9092"}}}
	if _, _, _, err := protectLegacyRuntimeOverrideSecrets("scn-2", 0, nil, reconcileGroupRuntimeSecrets(reentered, stored)); err != nil {
		t.Errorf("re-entered brokers refused: %v", err)
	}
}
