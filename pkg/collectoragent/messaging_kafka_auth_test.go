package collectoragent

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/sasl/scram"
)

func TestKafkaManagementSCRAMBoundsBeforeChallenge(t *testing.T) {
	nonce := []byte("client-nonce")
	cases := []struct{ name, challenge, reason string }{
		{"valid minimum", "r=client-nonce-server,s=c2FsdA==,i=4096", ""},
		{"valid maximum", "r=client-nonce-server,s=c2FsdA==,i=65536", ""},
		{"minimum", "r=client-nonce-server,s=c2FsdA==,i=4095", "invalid"},
		{"excess CPU", "r=client-nonce-server,s=c2FsdA==,i=65537", "budget_exhausted"},
		{"huge CPU", "r=client-nonce-server,s=c2FsdA==,i=2147483647", "invalid"},
		{"nonce replaced", "r=another-server,s=c2FsdA==,i=4096", "identity_mismatch"},
		{"nonce not extended", "r=client-nonce,s=c2FsdA==,i=4096", "identity_mismatch"},
		{"optional extension", "r=client-nonce-server,s=c2FsdA==,i=4096,x=ignored", "invalid"},
		{"duplicate iteration", "r=client-nonce-server,s=c2FsdA==,i=4096,i=4096", "invalid"},
		{"empty salt", "r=client-nonce-server,s=,i=4096", "budget_exhausted"},
		{"malformed salt", "r=client-nonce-server,s=%%%%,i=4096", "invalid"},
		{"noncanonical salt", "r=client-nonce-server,s=c2FsdA=\n=,i=4096", "invalid"},
		{"leading zero", "r=client-nonce-server,s=c2FsdA==,i=04096", "invalid"},
		{"signed iteration", "r=client-nonce-server,s=c2FsdA==,i=+4096", "invalid"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := validateKafkaManagementSCRAMFirst([]byte(test.challenge), nonce)
			if got := kafkaManagementErrorReason(err); got != test.reason {
				t.Fatalf("reason %q, expected %q", got, test.reason)
			}
		})
	}
	huge := []byte("r=" + strings.Repeat("n", 257) + ",s=c2FsdA==,i=4096")
	if validateKafkaManagementSCRAMFirst(huge, []byte("n")) == nil {
		t.Fatal("unbounded server nonce accepted")
	}
	if validateKafkaManagementSCRAMFirst([]byte("r=client-nonce-server,s="+base64.StdEncoding.EncodeToString(make([]byte, 257))+",i=4096"), nonce) == nil {
		t.Fatal("unbounded salt accepted")
	}
}
func TestKafkaManagementSCRAMServerProofIsRequired(t *testing.T) {
	mechanism := scram.Auth{User: "reader", Pass: "controlled-password", Nonce: []byte("fixed-nonce")}.AsSha256Mechanism()
	session, first, err := mechanism.Authenticate(context.Background(), "tls://broker.test:9093")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(first)
	nonce := kafkaManagementClientNonce(first)
	challenge := append([]byte("r="), nonce...)
	challenge = append(challenge, []byte("-server,s=c2FsdA==,i=4096")...)
	if err := validateKafkaManagementSCRAMFirst(challenge, nonce); err != nil {
		t.Fatal(err)
	}
	done, reply, err := session.Challenge(challenge)
	defer clear(reply)
	if done || err != nil || !bytes.Contains(reply, []byte(",p=")) {
		t.Fatal("bounded first challenge did not produce client proof")
	}
	badProof := []byte("v=" + base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err := validateKafkaManagementSCRAMFinal(badProof, mechanism.Name()); err != nil {
		t.Fatal("structural proof check should allow correctly sized bytes")
	}
	done, _, err = session.Challenge(badProof)
	if done || err == nil {
		t.Fatal("mismatched server signature authenticated")
	}
	for _, final := range [][]byte{[]byte("e=raw-private-error"), []byte("v=abc"), append(badProof, []byte(",extension=1")...), []byte("v=" + base64.StdEncoding.EncodeToString(make([]byte, 64)))} {
		if validateKafkaManagementSCRAMFinal(final, "SCRAM-SHA-256") == nil {
			t.Fatal("invalid final proof accepted")
		}
	}
}
func TestKafkaManagementProviderErrorsDoNotObserveDefaults(t *testing.T) {
	for _, test := range []struct {
		code   int16
		reason string
	}{{29, "permission_denied"}, {30, "permission_denied"}, {58, "permission_denied"}, {35, "unsupported"}, {6, "fenced"}, {88, "unqualified"}, {7, "timeout"}} {
		err := kafkaManagementProviderError(test.code)
		value := kafkaManagementPosition(0, err)
		if value.Value != nil || value.Reason != test.reason {
			t.Fatalf("error %d manufactured observed zero: %#v", test.code, value)
		}
	}
	for _, value := range []int64{0, 1, 9007199254740993, 9223372036854775807} {
		if observed := kafkaManagementPosition(value, nil); observed.Value == nil {
			t.Fatalf("valid exact offset %d unavailable", value)
		}
	}
	if value := kafkaManagementPosition(-1, nil); value.Reason != "not_observed" || value.Value != nil {
		t.Fatal("provider sentinel became a measurement")
	}
	if value := kafkaManagementPosition(-2, nil); value.Reason != "invalid" || value.Value != nil {
		t.Fatal("invalid negative became a measurement")
	}
}
