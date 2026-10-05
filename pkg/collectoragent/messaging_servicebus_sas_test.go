package collectoragent

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestServiceBusSASLiteralKeyGoldenAndExactAudience(t *testing.T) {
	// Independent Python HMAC-SHA256 fixture; key is deliberately valid Base64
	// text, whose decoded bytes would generate a different signature.
	now := time.Unix(1700000000, 0)
	credentials := ManagementSharedKeyCredentials{KeyName: "reader", Key: "c2VjcmV0"}
	header, err := serviceBusSASAuthorization(credentials, "https://namespace.example.test/orders", now, now.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	const golden = "SharedAccessSignature sr=https%3a%2f%2fnamespace.example.test%2forders&sig=Fixra632M16KJ6TPDoaKNTFbmtfPh%2BzCZHEO6de7%2BdA%3D&se=1700000020&skn=reader"
	if header != golden {
		t.Fatalf("literal-key SAS fixture mismatch: %q", header)
	}
	fields, err := url.ParseQuery(strings.TrimPrefix(header, "SharedAccessSignature "))
	if err != nil {
		t.Fatal(err)
	}
	if fields.Get("sr") != "https://namespace.example.test/orders" || fields.Get("se") != "1700000020" || fields.Get("skn") != "reader" {
		t.Fatal("SAS widened audience or lifetime")
	}
}
func TestServiceBusSASFloorsAndCapsSignedAuthority(t *testing.T) {
	now := time.Unix(1700000000, 250000000)
	credentials := ManagementSharedKeyCredentials{KeyName: "reader", Key: "controlled-secret"}
	for _, test := range []struct {
		expires time.Time
		want    string
	}{{now.Add(20 * time.Second), "1700000020"}, {now.Add(2 * time.Hour), "1700000030"}} {
		header, err := serviceBusSASAuthorization(credentials, "https://namespace.example.test/events/Subscriptions/workers", now, test.expires)
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := url.ParseQuery(strings.TrimPrefix(header, "SharedAccessSignature "))
		if fields.Get("se") != test.want {
			t.Fatal("expiry rounded beyond approval")
		}
		if fields.Get("sr") != "https://namespace.example.test/events/subscriptions/workers" {
			t.Fatal("pinned lowercase audience grammar changed")
		}
	}
	for _, expiry := range []time.Time{time.Time{}, now, now.Add(time.Second), now.Add(-time.Second)} {
		if _, err := serviceBusSASAuthorization(credentials, "https://namespace.example.test/orders", now, expiry); err == nil {
			t.Fatal("expired/insufficient authority emitted token")
		}
	}
}
func TestServiceBusSASRejectsUnsafeResourceOrPolicy(t *testing.T) {
	now := time.Unix(1700000000, 0)
	credentials := ManagementSharedKeyCredentials{KeyName: "reader", Key: "controlled-secret"}
	for _, resource := range []string{"http://namespace.example.test/orders", "https://namespace.example.test", "https://user@namespace.example.test/orders", "https://namespace.example.test/orders?api-version=2021-05", "https://namespace.example.test/orders#secret", "https://namespace.example.test/orders%2fother", "https://namespace.example.test:443/orders", "https://namespace.example.test/orders/../messages", "https://namespace.example.test/orders//west", "https://namespace.example.test/messages", "https://namespace.example.test/events/subscriptions/workers", "https://unapproved.invalid/orders\r\nAuthorization: hidden"} {
		if _, err := serviceBusSASAuthorization(credentials, resource, now, now.Add(10*time.Second)); err == nil {
			t.Fatal("unsafe SAS resource accepted")
		}
	}
	for _, name := range []string{"", "reader\r\nsecret", "reader&se=9999999999", strings.Repeat("a", 51)} {
		credentials.KeyName = name
		if _, err := serviceBusSASAuthorization(credentials, "https://namespace.example.test/orders", now, now.Add(10*time.Second)); err == nil {
			t.Fatal("unsafe policy accepted")
		}
	}
}
