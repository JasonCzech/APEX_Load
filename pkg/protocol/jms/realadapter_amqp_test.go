//go:build jms_solace_real

package jms

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCorrelationMatches(t *testing.T) {
	tests := []struct {
		name      string
		requestID string
		replyID   string
		want      bool
	}{
		{name: "exact match", requestID: "corr-1", replyID: "corr-1", want: true},
		{name: "mismatch", requestID: "corr-1", replyID: "corr-2", want: false},
		{name: "empty request is permissive", requestID: "", replyID: "corr-2", want: true},
		{name: "empty reply is permissive", requestID: "corr-1", replyID: "", want: true},
		{name: "both empty is permissive", requestID: "", replyID: "", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := correlationMatches(tc.requestID, tc.replyID); got != tc.want {
				t.Fatalf("correlationMatches(%q, %q) = %v, want %v", tc.requestID, tc.replyID, got, tc.want)
			}
		})
	}
}

func TestToAMQPMessageSetsReplyToProperty(t *testing.T) {
	tests := []struct {
		name        string
		msg         message
		wantReplyTo string // empty => expect nil ReplyTo
	}{
		{
			name: "reply destination from headers",
			msg: message{
				Body:    []byte("hello"),
				Headers: map[string]string{"reply_to": "orders.reply"},
			},
			wantReplyTo: "orders.reply",
		},
		{
			name: "reply destination from application properties",
			msg: message{
				Body:       []byte("hello"),
				Properties: map[string]string{"reply_to": "orders.reply.props"},
			},
			wantReplyTo: "orders.reply.props",
		},
		{
			name: "header reply_to wins over property",
			msg: message{
				Body:       []byte("hello"),
				Headers:    map[string]string{"reply_to": "header.reply"},
				Properties: map[string]string{"reply_to": "prop.reply"},
			},
			wantReplyTo: "header.reply",
		},
		{
			name:        "no reply destination leaves ReplyTo nil",
			msg:         message{Body: []byte("hello")},
			wantReplyTo: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := toAMQPMessage("orders.request", tc.msg)
			if out.Properties == nil {
				t.Fatal("Properties = nil, want non-nil message properties")
			}
			if tc.wantReplyTo == "" {
				if out.Properties.ReplyTo != nil {
					t.Fatalf("Properties.ReplyTo = %q, want nil", *out.Properties.ReplyTo)
				}
				return
			}
			if out.Properties.ReplyTo == nil {
				t.Fatalf("Properties.ReplyTo = nil, want %q", tc.wantReplyTo)
			}
			if *out.Properties.ReplyTo != tc.wantReplyTo {
				t.Fatalf("Properties.ReplyTo = %q, want %q", *out.Properties.ReplyTo, tc.wantReplyTo)
			}
			// The reply destination must remain in the application properties too.
			if got, _ := out.ApplicationProperties["reply_to"].(string); got != tc.wantReplyTo {
				t.Fatalf("ApplicationProperties[reply_to] = %q, want %q", got, tc.wantReplyTo)
			}
		})
	}
}

func TestBuildTLSRootCAs(t *testing.T) {
	pemCert := generateSelfSignedPEM(t)
	pemFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(pemFile, []byte(pemCert), 0o600); err != nil {
		t.Fatalf("WriteFile(ca.pem) returned error: %v", err)
	}

	tests := []struct {
		name          string
		caCertSecret  string
		trustStoreRef string
		wantPool      bool
		wantErr       bool
		wantErrSubstr string
	}{
		{
			name:         "neither set returns nil pool",
			caCertSecret: "",
			wantPool:     false,
		},
		{
			name:         "inline PEM content",
			caCertSecret: pemCert,
			wantPool:     true,
		},
		{
			name:         "PEM file path",
			caCertSecret: pemFile,
			wantPool:     true,
		},
		{
			name:          "unparseable inline PEM errors",
			caCertSecret:  "-----BEGIN CERTIFICATE-----\nnot-base64\n-----END CERTIFICATE-----",
			wantErr:       true,
			wantErrSubstr: "no parseable PEM",
		},
		{
			name:          "missing PEM file errors",
			caCertSecret:  filepath.Join(t.TempDir(), "does-not-exist.pem"),
			wantErr:       true,
			wantErrSubstr: "read JMS TLS CA cert file",
		},
		{
			name:          "JKS truststore-only is rejected",
			caCertSecret:  "",
			trustStoreRef: "vault://truststore.jks",
			wantErr:       true,
			wantErrSubstr: "JKS truststores",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool, err := buildTLSRootCAs(tc.caCertSecret, tc.trustStoreRef)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("buildTLSRootCAs returned nil error, want substring %q", tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErrSubstr)
				}
				if pool != nil {
					t.Fatalf("pool = %v, want nil on error", pool)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildTLSRootCAs returned error: %v", err)
			}
			if tc.wantPool && pool == nil {
				t.Fatal("pool = nil, want non-nil CertPool")
			}
			if !tc.wantPool && pool != nil {
				t.Fatal("pool = non-nil, want nil CertPool")
			}
		})
	}
}

// generateSelfSignedPEM returns a PEM-encoded self-signed certificate suitable
// for exercising buildTLSRootCAs without touching the network.
func generateSelfSignedPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "apexload-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate returned error: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestAMQPMessageTypeAndHeaderRoundTrip(t *testing.T) {
	cases := []struct {
		typ       string
		body      string
		wantValue bool
	}{
		{"text", "hello world", true},
		{"map", `{"a":"1"}`, false},
		{"bytes", "rawbytes", false},
	}
	for _, tc := range cases {
		in := message{ID: "m1", Type: tc.typ, Body: []byte(tc.body), DeliveryMode: "persistent"}
		amqpMsg := toAMQPMessage("orders.q", in)
		if amqpMsg.Properties == nil || amqpMsg.Properties.Subject == nil || *amqpMsg.Properties.Subject != tc.typ {
			t.Fatalf("%s: Subject not set to message type", tc.typ)
		}
		if tc.wantValue {
			if s, ok := amqpMsg.Value.(string); !ok || s != tc.body {
				t.Fatalf("%s: text body should ride Value, got %#v", tc.typ, amqpMsg.Value)
			}
		} else if len(amqpMsg.Data) == 0 {
			t.Fatalf("%s: non-text body should ride Data", tc.typ)
		}
		out := fromAMQPMessage("orders.q", amqpMsg)
		if out.Type != tc.typ {
			t.Fatalf("%s: Type round-trip = %q", tc.typ, out.Type)
		}
		if string(out.Body) != tc.body {
			t.Fatalf("%s: Body round-trip = %q, want %q", tc.typ, string(out.Body), tc.body)
		}
	}

	// delivery_mode/priority/TTL map onto the AMQP header.
	pr := uint8(7)
	in := message{Type: "bytes", Body: []byte("x"), DeliveryMode: "non_persistent", Priority: &pr, TimeToLive: 1500 * time.Millisecond}
	amqpMsg := toAMQPMessage("orders.q", in)
	if amqpMsg.Header == nil {
		t.Fatal("expected an AMQP message header")
	}
	if amqpMsg.Header.Durable {
		t.Fatal("non_persistent must set Durable=false")
	}
	if amqpMsg.Header.Priority != 7 {
		t.Fatalf("Priority header = %d, want 7", amqpMsg.Header.Priority)
	}
	if amqpMsg.Header.TTL != 1500*time.Millisecond {
		t.Fatalf("TTL header = %v, want 1.5s", amqpMsg.Header.TTL)
	}
}

func TestSolaceImplementsSelectorAndBrowser(t *testing.T) {
	var a brokerAdapter = &solaceAMQPAdapter{}
	if _, ok := a.(selectiveConsumer); !ok {
		t.Fatal("Solace/AMQP must implement selectiveConsumer (server-side selector filter)")
	}
	if _, ok := a.(messageBrowser); !ok {
		t.Fatal("Solace/AMQP must implement messageBrowser (copy-mode browse)")
	}
}
