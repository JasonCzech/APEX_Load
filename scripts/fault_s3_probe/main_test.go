package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func fixture(t *testing.T) (string, inputs) {
	t.Helper()
	root := t.TempDir()
	in := inputs{run: "0123456789abcdef", access: strings.Repeat("a", 32), secret: strings.Repeat("b", 32)}
	write := func(name string, value any) {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("ownership.json", map[string]string{"run_id": in.run, "cluster": "apexload-fault-" + in.run, "namespace": "apexload-fault", "namespace_label": "apexload.io/fault-run=" + in.run})
	write("credentials.json", map[string]string{"s3_access_key": in.access, "s3_secret_key": in.secret})
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	in.ca = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(root, "ca.crt"), in.ca, 0600); err != nil {
		t.Fatal(err)
	}
	return root, in
}

func TestPrivateInputsAndDestination(t *testing.T) {
	root, in := fixture(t)
	got, err := loadInputs(root)
	if err != nil || got.run != in.run || got.secret != in.secret {
		t.Fatal("valid input rejected")
	}
	for _, address := range []string{"", ":8333", "localhost:8333", "192.0.2.1:8333", "127.0.0.1:0", "127.0.0.1:65536", "[::1]:8333"} {
		if _, _, err := clientFor(in, address); err == nil {
			t.Fatalf("accepted %q", address)
		}
	}
	_, tr, err := clientFor(in, "127.0.0.1:8333")
	if err != nil {
		t.Fatal(err)
	}
	defer tr.CloseIdleConnections()
	for _, dest := range []string{"example.com:8333", serviceHost + ":443", "evil." + serviceHost + ":8333"} {
		if _, err := tr.DialContext(context.Background(), "tcp", dest); err == nil {
			t.Fatal("destination accepted")
		}
	}
	if tr.Proxy != nil || tr.TLSClientConfig.InsecureSkipVerify || tr.TLSClientConfig.RootCAs == nil {
		t.Fatal("unsafe TLS/proxy")
	}
	for _, contents := range []string{`{"s3_access_key":"a","s3_access_key":"b"}`, `{"s3_access_key":true}`, `{} {}`, strings.Repeat("x", 32769)} {
		if err := os.WriteFile(filepath.Join(root, "credentials.json"), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadInputs(root); err == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
	if _, err := loadInputs("relative"); err == nil {
		t.Fatal("relative inputs accepted")
	}
}

func TestProbeStages(t *testing.T) {
	for _, tc := range []struct{ name, mode, failure, want string }{
		{"seed", "seed", "", "complete"}, {"retention", "check", "", "complete"},
		{"existing", "seed", "existing", "complete"}, {"create failed", "seed", "create", "create_bucket"},
		{"put failed", "seed", "put", "put_probe"}, {"missing", "check", "get", "get_probe"},
		{"wrong contents", "check", "contents", "probe_contents"}, {"oversize", "check", "oversize", "probe_contents"},
		{"scope accepted", "check", "scope", "bucket_scope"}, {"scope unavailable", "check", "scope transport", "bucket_scope"},
		{"bad key accepted", "check", "badkey", "credential_rejection"}, {"bad key unavailable", "check", "badkey transport", "credential_rejection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, in := fixture(t)
			payload := "apexload-retained-" + in.run
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reject := func(code string) {
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(403)
					fmt.Fprintf(w, "<Error><Code>%s</Code></Error>", code)
				}
				if strings.Contains(r.Header.Get("Authorization"), "Credential=bad/") {
					if tc.failure == "badkey" {
						io.WriteString(w, payload)
					} else if tc.failure == "badkey transport" {
						w.WriteHeader(503)
					} else {
						reject("SignatureDoesNotMatch")
					}
					return
				}
				if r.URL.Path == "/apexload-unowned" {
					if tc.failure == "scope" {
						w.WriteHeader(200)
					} else if tc.failure == "scope transport" {
						w.WriteHeader(503)
					} else {
						reject("AccessDenied")
					}
					return
				}
				if r.Method == http.MethodPut {
					writes++
					if r.URL.Path == "/"+bucket {
						if tc.failure == "create" {
							reject("AccessDenied")
						} else if tc.failure == "existing" {
							reject("BucketAlreadyExists")
						}
						return
					}
					if tc.failure == "put" {
						w.WriteHeader(503)
					}
					return
				}
				if tc.failure == "get" {
					w.WriteHeader(404)
					return
				}
				if tc.failure == "contents" {
					io.WriteString(w, "wrong")
					return
				}
				io.WriteString(w, payload)
				if tc.failure == "oversize" {
					io.WriteString(w, "extra")
				}
			}))
			defer server.Close()
			client := func(key string) *s3.Client {
				return s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(key, "secret", ""), HTTPClient: server.Client(), RetryMaxAttempts: 1}, func(o *s3.Options) { o.BaseEndpoint = &server.URL; o.UsePathStyle = true })
			}
			if got := check(context.Background(), in, tc.mode, client("valid"), client("bad")); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
			if tc.mode == "check" && writes != 0 {
				t.Fatal("retention check rewrote probe")
			}
		})
	}
}

func TestEvidencePrivateExclusiveAndFailedTransport(t *testing.T) {
	root, _ := fixture(t)
	output := filepath.Join(t.TempDir(), "evidence")
	// A closed loopback listener gives a deterministic failed transport.
	s := httptest.NewServer(http.NotFoundHandler())
	address := strings.TrimPrefix(s.URL, "http://")
	s.Close()
	args := []string{"--inputs", root, "--address", address, "--output", output}
	if run(args) != 1 {
		t.Fatal("transport must fail")
	}
	b, err := os.ReadFile(filepath.Join(output, "s3-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report evidence
	if json.Unmarshal(b, &report) != nil || report.Passed || report.Stage != "get_probe" {
		t.Fatal("failed evidence invalid")
	}
	for _, p := range []struct {
		path string
		mode os.FileMode
	}{{output, 0700}, {filepath.Join(output, "s3-evidence.json"), 0600}} {
		info, err := os.Stat(p.path)
		if err != nil || info.Mode().Perm() != p.mode {
			t.Fatal("bad permissions")
		}
	}
	if run(args) != 2 {
		t.Fatal("overwrote existing evidence")
	}
	after, _ := os.ReadFile(filepath.Join(output, "s3-evidence.json"))
	if string(after) != string(b) {
		t.Fatal("changed prior evidence")
	}
	if strings.Contains(string(b), strings.Repeat("b", 32)) {
		t.Fatal("secret leaked")
	}
}

func TestTLSRejectsWrongServiceIdentity(t *testing.T) {
	_, in := fixture(t)
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	in.ca = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, tr, err := clientFor(in, strings.TrimPrefix(server.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.CloseIdleConnections()
	_, err = client.GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("probe")})
	if err == nil {
		t.Fatal("accepted invalid DNS certificate")
	}
}
