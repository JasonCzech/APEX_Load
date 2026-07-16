package protocol

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestTargetURLAllowed(t *testing.T) {
	allowed := []string{"api.example.test", "*.assets.example.test"}
	for _, raw := range []string{
		"https://api.example.test/path",
		"wss://cdn.assets.example.test/socket",
		"about:blank",
		"data:text/plain,ok",
		"blob:https://api.example.test/id",
		"BLOB:https://api.example.test/id",
		"blob:BLOB:https://api.example.test/id",
	} {
		if !TargetURLAllowed(raw, allowed) {
			t.Errorf("TargetURLAllowed(%q) = false", raw)
		}
	}
	for _, raw := range []string{
		"https://attacker.example/path",
		"blob:https://attacker.example/id",
		"BLOB:https://attacker.example/id",
		"blob:",
		"file:///etc/passwd",
		"not-a-url",
	} {
		if TargetURLAllowed(raw, allowed) {
			t.Errorf("TargetURLAllowed(%q) = true", raw)
		}
	}
}

func TestTargetAddressAndDSNHostExtraction(t *testing.T) {
	addresses := map[string]string{
		"dns:///grpc.example.test:443":                      "grpc.example.test",
		"dns://resolver.example.test/grpc.example.test:443": "grpc.example.test",
		"amqps://broker.example.test:5671":                  "broker.example.test",
		"[2001:db8::1]:9092":                                "2001:db8::1",
		"tcp:sql.example.test,1433":                         "sql.example.test",
	}
	if _, err := TargetAddressHost("xds://control.example.test/service-name"); err == nil {
		t.Fatal("xds resolver target exposed its authority as the outbound peer")
	}
	for raw, want := range addresses {
		got, err := TargetAddressHost(raw)
		if err != nil || got != want {
			t.Errorf("TargetAddressHost(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}

	tests := []struct {
		driver string
		dsn    string
		want   []string
	}{
		{driver: "postgres", dsn: "postgres://user:password@db.example.test:5432/app", want: []string{"db.example.test"}},
		{driver: "postgres", dsn: "postgres://user:password@db.example.test:5432/app?host=override.example.test", want: []string{"db.example.test", "override.example.test"}},
		{driver: "postgres", dsn: "host=db-a.example.test,db-b.example.test port=5432 dbname=app", want: []string{"db-a.example.test", "db-b.example.test"}},
		{driver: "mysql", dsn: "user:password@tcp(mysql.example.test:3306)/app", want: []string{"mysql.example.test"}},
		{driver: "odbc", dsn: "Server=tcp:sql.example.test,1433;Database=app", want: []string{"sql.example.test"}},
		{driver: "oracle", dsn: "user/password@oracle.example.test:1521/service", want: []string{"oracle.example.test"}},
		{driver: "oracle", dsn: "(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=rac.example.test)(PORT=1521)))", want: []string{"rac.example.test"}},
	}
	for _, test := range tests {
		got, err := TargetDSNHosts(test.driver, test.dsn)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Errorf("TargetDSNHosts(%q, ...) = %#v, %v; want %#v", test.driver, got, err, test.want)
		}
	}

	hosts, err := TargetReferenceHosts("Endpoint=sb://orders.servicebus.windows.net/;SharedAccessKeyName=name;SharedAccessKey=secret")
	if err != nil || !reflect.DeepEqual(hosts, []string{"orders.servicebus.windows.net"}) {
		t.Fatalf("Service Bus target hosts = %#v, %v", hosts, err)
	}
}

func TestTargetPolicyProxyValidatesSelectedProxy(t *testing.T) {
	blocked, _ := url.Parse("http://proxy.attacker.example:8080")
	proxy := NewTargetPolicyProxy(func(*http.Request) (*url.URL, error) { return blocked, nil }, []string{"proxy.example.test"})
	selected, err := proxy(httptest.NewRequest(http.MethodGet, "https://api.example.test", nil))
	if err == nil || selected != nil || !strings.Contains(err.Error(), "proxy.attacker.example") {
		t.Fatalf("proxy selection = %v, %v; want sanitized policy rejection", selected, err)
	}
}

func TestTargetPolicyRoundTripperBlocksRedirectLeg(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("blocked redirect target was reached")
	}))
	defer blocked.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", blocked.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()

	client := &http.Client{Transport: NewTargetPolicyRoundTripper(http.DefaultTransport, []string{"127.0.0.1"})}
	// Both test servers share 127.0.0.1, so use an explicitly disallowed host in
	// the redirect while retaining the server's port.
	client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		req.URL.Host = strings.Replace(req.URL.Host, "127.0.0.1", "localhost", 1)
		return nil
	}
	if _, err := client.Get(redirect.URL); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("redirect policy error = %v", err)
	}
}
