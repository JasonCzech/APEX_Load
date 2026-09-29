package broker

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

func TestProvisionHostTransportImmutableAndNeverAdopts(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	material := hostMaterial(t, r)
	calls := 0
	k := kubeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.Method != "POST" || req.URL.Path != "/api/v1/namespaces/java-test/secrets" || req.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("unexpected secret request")
		}
		if record(t, s, epoch, r.ID).State != javarunner.HostReserved {
			t.Error("unexpected host state")
		}
		if calls > 1 {
			w.WriteHeader(409)
			return
		}
		var secret map[string]any
		if json.NewDecoder(req.Body).Decode(&secret) != nil {
			t.Error("bad payload")
			return
		}
		if secret["immutable"] != true || secret["type"] != "kubernetes.io/tls" || len(secret["data"].(map[string]any)) != 3 {
			t.Error("unbounded/mutable credentials")
		}
		secret["metadata"].(map[string]any)["uid"] = "secret-uid"
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(secret)
	}, &testAttestor{})
	if e := k.ProvisionHostTransport(t.Context(), s, epoch, r.ID, material); e != nil {
		t.Fatal(e)
	}
	if e := k.ProvisionHostTransport(t.Context(), s, epoch, r.ID, material); e == nil || calls != 2 {
		t.Fatal("adopted/retried conflict", calls, e)
	}
	if e := s.BeginPrepare(t.Context(), epoch, r.ID); e != nil {
		t.Fatal(e)
	}
	if e := k.ProvisionHostTransport(t.Context(), s, epoch, r.ID, material); e == nil || calls != 2 {
		t.Fatal("changed live credentials", calls, e)
	}
}

func TestProvisionHostTransportRejectsMutationAndLateAuthority(t *testing.T) {
	for _, kind := range []string{"wrong_host", "wrong_namespace", "missing_uid", "mutable", "changed_key", "extra_data", "wrong_type", "server_error", "expired_after_create"} {
		t.Run(kind, func(t *testing.T) {
			s, epoch, _ := newStore(t)
			r := reserve(t, s, epoch)
			material := hostMaterial(t, r)
			calls := 0
			k := kubeFixture(t, func(w http.ResponseWriter, req *http.Request) {
				calls++
				var secret map[string]any
				if json.NewDecoder(req.Body).Decode(&secret) != nil {
					t.Error("bad payload")
					return
				}
				meta := secret["metadata"].(map[string]any)
				meta["uid"] = "secret-uid"
				switch kind {
				case "wrong_host":
					meta["annotations"].(map[string]any)[hostAnnotation] = "other"
				case "wrong_namespace":
					meta["namespace"] = "other"
				case "missing_uid":
					delete(meta, "uid")
				case "mutable":
					secret["immutable"] = false
				case "changed_key":
					secret["data"].(map[string]any)["tls.key"] = "YmFk"
				case "extra_data":
					secret["data"].(map[string]any)["token"] = "dG9rZW4="
				case "wrong_type":
					secret["type"] = "kubernetes.io/service-account-token"
				case "server_error":
					w.WriteHeader(500)
					_, _ = w.Write([]byte("private detail"))
					return
				case "expired_after_create":
					if e := s.Abort(t.Context(), epoch, r.ID, "cancelled"); e != nil {
						t.Error(e)
					}
				}
				w.WriteHeader(201)
				_ = json.NewEncoder(w).Encode(secret)
			}, &testAttestor{})
			if e := k.ProvisionHostTransport(t.Context(), s, epoch, r.ID, material); e == nil || strings.Contains(e.Error(), "private") || calls != 1 {
				t.Fatal("invalid create succeeded/retried", e, calls)
			}
		})
	}
}

func TestTransportCleanupRequiresReapedHostAndExactSecretUID(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	material := hostMaterial(t, r)
	body, e := material.SecretJSON(testConfig.Namespace, r)
	if e != nil {
		t.Fatal(e)
	}
	var secret map[string]any
	_ = json.Unmarshal(body, &secret)
	secret["metadata"].(map[string]any)["uid"] = "original-secret"
	gets, deletes := 0, 0
	missing := true
	k := kubeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/namespaces/java-test/secrets/"+material.SecretName() {
			t.Error("wrong object")
		}
		switch req.Method {
		case "GET":
			gets++
			if missing {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(secret)
		case "DELETE":
			deletes++
			var options struct{ Preconditions map[string]string }
			if json.NewDecoder(req.Body).Decode(&options) != nil || options.Preconditions["uid"] != "original-secret" {
				t.Error("missing exact UID fence")
			}
			w.WriteHeader(202)
		default:
			t.Error("unexpected mutation")
		}
	}, &testAttestor{})
	if e := k.ReconcileHostTransport(t.Context(), s, epoch, r.ID); e == nil || gets != 0 {
		t.Fatal("deleted live credentials")
	}
	if e := s.Abort(t.Context(), epoch, r.ID, "cancelled"); e != nil {
		t.Fatal(e)
	}
	if e := k.ReconcileHostTransport(t.Context(), s, epoch, r.ID); e != nil {
		t.Fatal(e)
	}
	// A late Secret create cannot hide forever behind a cached NotFound.
	missing = false
	if e := k.ReconcileHostTransport(t.Context(), s, epoch, r.ID); e != nil || deletes != 1 {
		t.Fatal("late secret not removed", e)
	}
	secret["metadata"].(map[string]any)["annotations"].(map[string]any)[epochAnnotation] = "foreign"
	if e := k.ReconcileHostTransport(t.Context(), s, epoch, r.ID); e == nil || deletes != 1 {
		t.Fatal("deleted unrelated transport", e)
	}
}

func TestTransportCleanupRetainsQuarantineAndWorksAcrossEpochs(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	k := kubeFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }, &testAttestor{})
	if e := s.BeginPrepare(t.Context(), epoch, r.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.Abort(t.Context(), epoch, r.ID, "cancelled"); e != nil {
		t.Fatal(e)
	}
	if e := k.ReconcileHostTransport(t.Context(), s, epoch, r.ID); e == nil {
		t.Fatal("quarantine transport released")
	}
	if e := s.BindPod(t.Context(), epoch, r.ID, "pod-uid", "containerd://runtime"); e != nil {
		t.Fatal(e)
	}
	if e := s.ConfirmTerminated(t.Context(), epoch, r.ID, "pod-uid", "containerd://runtime"); e != nil {
		t.Fatal(e)
	}
	newEpoch, e := s.StartEpoch(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if e := k.ReconcileHostTransport(t.Context(), s, epoch, r.ID); e == nil {
		t.Fatal("old broker retained authority")
	}
	if e := k.ReconcileHostTransport(t.Context(), s, newEpoch, r.ID); e != nil {
		t.Fatal(e)
	}
}

func TestTransportProvisionRejectsForeignOrExpiredMaterialBeforeNetwork(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	k := kubeFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid material reached API") }, &testAttestor{})
	for _, kind := range []string{"nil", "host", "expiry"} {
		material := hostMaterial(t, r)
		switch kind {
		case "nil":
			material = nil
		case "host":
			material.host = "foreign"
		case "expiry":
			material.expires = time.Now()
		}
		if e := k.ProvisionHostTransport(t.Context(), s, epoch, r.ID, material); e == nil {
			t.Fatal("invalid material admitted", kind)
		}
	}
}
