package prometheus

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSafeDiscoveryEntityFields(t *testing.T) {
	fields, omitted := SafeDiscoveryEntityFields("series", map[string]string{"job": "checkout", "instance": "192.0.2.10:9100", "namespace": "production", "token": "credential", "url": "https://private", "service": "https://private", "template": "{{secret.reference}}", "password_alias": "hidden", "payload": "password=hidden", "invalid-key": "unsafe", "control": "line\nnext", "oversize": strings.Repeat("a", 129), "bad_utf8": string([]byte{0xff})})
	if !omitted || len(fields) != 3 || fields["job"] != "checkout" || fields["instance"] != "192.0.2.10:9100" || fields["namespace"] != "production" {
		t.Fatalf("unsafe or missing discovery fields: %+v, omitted=%v", fields, omitted)
	}
	if oversized, omitted := SafeDiscoveryEntityFields("series", map[string]string{strings.Repeat("a", 129): "safe"}); oversized != nil || !omitted {
		t.Fatal("oversized discovery label name retained")
	}
	metadata, omitted := SafeDiscoveryEntityFields("metric_metadata", map[string]string{"type": "counter", "unit": "seconds", "help": "unrestricted text", "secret": "credential"})
	if !omitted || len(metadata) != 2 || metadata["unit"] != "seconds" || metadata["type"] != "counter" {
		t.Fatalf("metadata projection: %+v", metadata)
	}
	if unknown, omitted := SafeDiscoveryEntityFields("unknown", map[string]string{"job": "checkout"}); unknown != nil || !omitted {
		t.Fatal("unknown entity type retained arbitrary fields")
	}
	many := map[string]string{}
	for i := 0; i < 15; i++ {
		many[fmt.Sprintf("label_%02d", i)] = "safe"
	}
	bounded, omitted := SafeDiscoveryEntityFields("target", many)
	if len(bounded) != 12 || !omitted || bounded["label_00"] != "safe" || bounded["label_11"] != "safe" {
		t.Fatalf("nondeterministic or unbounded fields: %+v", bounded)
	}
}

func TestMetadataDiscoveryUsesMetricName(t *testing.T) {
	called := false
	c := fixture(t, Config{}, func(request *http.Request) (*http.Response, error) {
		called = true
		if request.URL.Path != "/api/v1/metadata" || request.URL.Query().Get("metric") != "requests_total" || request.URL.Query().Has("match[]") {
			t.Fatalf("metadata API received a series selector: %s", request.URL)
		}
		return success(map[string]any{"requests_total": []map[string]string{{"type": "counter", "unit": "requests"}}}), nil
	})
	entities, err := c.Discover(context.Background(), `requests_total{job="checkout"}`, "metadata", "")
	if err != nil || !called || len(entities) != 1 || entities[0].EntityIDs["type"] != "counter" || entities[0].EntityIDs["unit"] != "requests" {
		t.Fatalf("metadata=%+v err=%v", entities, err)
	}
	called = false
	if _, err := c.Discover(context.Background(), `{job="checkout"}`, "metadata", ""); err == nil || called {
		t.Fatal("label-only metadata selector reached a backend without label filtering")
	}
}

func TestAzureLabelValueDiscoveryUsesCaseInsensitiveIdentity(t *testing.T) {
	for _, label := range []string{"Job", "job", "JOB", "__NaMe__"} {
		t.Run(label, func(t *testing.T) {
			client := fixture(t, Config{}, func(request *http.Request) (*http.Response, error) {
				if strings.EqualFold(label, "__name__") {
					if request.URL.Path != "/api/v1/label/__name__/values" {
						t.Fatal("metric-name discovery did not use the supported endpoint", request.URL.Path)
					}
					return success([]string{"up"}), nil
				}
				if request.URL.Path != "/api/v1/series" || request.URL.Query().Get("match[]") != `up{job="SUT"}` {
					t.Fatal("Azure label values must use bounded scoped series", request.URL)
				}
				return success([]map[string]string{{"__name__": "up", "Job": "SUT"}}), nil
			})
			client.cfg.BackendProfile = BackendAzureManaged
			entities, err := client.Discover(context.Background(), `up{job="SUT"}`, "label_values", label)
			want := "sut"
			if strings.EqualFold(label, "__name__") {
				want = "up"
			}
			if err != nil || len(entities) != 1 || entities[0].Name != want {
				t.Fatalf("mixed-case label values: %+v, err=%v", entities, err)
			}
		})
	}
}
