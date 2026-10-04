package collectoragent

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

const kafkaManagementAuthLimit = 16 << 10
const kafkaManagementSCRAMIterations = 65536

func kafkaManagementMechanism(credentials ManagementSASLCredentials) (sasl.Mechanism, error) {
	switch credentials.Mechanism {
	case "plain":
		return plain.Auth{User: credentials.Username, Pass: credentials.Password}.AsMechanism(), nil
	case "scram_sha_256":
		return scram.Auth{User: credentials.Username, Pass: credentials.Password}.AsSha256Mechanism(), nil
	case "scram_sha_512":
		return scram.Auth{User: credentials.Username, Pass: credentials.Password}.AsSha512Mechanism(), nil
	default:
		return nil, &managementObservationError{reason: "unsupported"}
	}
}

func authenticateKafkaManagement(ctx context.Context, poll *managementObservation, connection *kafkaManagementConnection) error {
	if !poll.credentials.MatchesIdentity(poll.target.Identity) || poll.credentials.SASL == nil {
		return &managementObservationError{reason: "permission_denied"}
	}
	mechanism, err := kafkaManagementMechanism(*poll.credentials.SASL)
	if err != nil {
		return err
	}
	response, err := connection.exchange(ctx, poll, kafkaManagementRequest{Operation: kafkaManagementSASLHandshake, Version: 1, Mechanism: mechanism.Name()})
	if err != nil {
		return err
	}
	if response.ErrorCode != 0 {
		return kafkaManagementProviderError(response.ErrorCode)
	}
	found := false
	for _, offered := range response.SASLMechanisms {
		found = found || offered == mechanism.Name()
	}
	if !found {
		return &managementObservationError{reason: "unsupported"}
	}
	session, initial, err := mechanism.Authenticate(ctx, connection.route.endpoint)
	if err != nil {
		return &managementObservationError{reason: "permission_denied"}
	}
	defer clear(initial)
	initialNonce := kafkaManagementClientNonce(initial)
	defer clear(initialNonce)
	next := initial
	for step := 0; step < 3; step++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(next) == 0 || len(next) > kafkaManagementAuthLimit {
			return &managementObservationError{reason: "budget_exhausted"}
		}
		exchangeStarted := time.Now()
		response, err = connection.exchange(ctx, poll, kafkaManagementRequest{Operation: kafkaManagementSASLAuthenticate, Version: 1, AuthBytes: next})
		clear(next)
		if err != nil {
			clear(response.AuthBytes)
			return err
		}
		if response.ErrorCode != 0 {
			clear(response.AuthBytes)
			return kafkaManagementProviderError(response.ErrorCode)
		}
		if len(response.AuthBytes) > kafkaManagementAuthLimit {
			clear(response.AuthBytes)
			return &managementObservationError{reason: "budget_exhausted"}
		}
		if mechanism.Name() != "PLAIN" {
			if step == 0 {
				err = validateKafkaManagementSCRAMFirst(response.AuthBytes, initialNonce)
			} else {
				err = validateKafkaManagementSCRAMFinal(response.AuthBytes, mechanism.Name())
			}
			if err != nil {
				clear(response.AuthBytes)
				return err
			}
		} else if len(response.AuthBytes) != 0 {
			clear(response.AuthBytes)
			return &managementObservationError{reason: "invalid"}
		}
		done, following, challengeErr := session.Challenge(response.AuthBytes)
		clear(response.AuthBytes)
		if challengeErr != nil {
			clear(following)
			return &managementObservationError{reason: "permission_denied"}
		}
		if err := ctx.Err(); err != nil {
			clear(following)
			return err
		}
		if done {
			if len(following) != 0 {
				clear(following)
				return &managementObservationError{reason: "invalid"}
			}
			if response.SessionLifetimeMS > 0 {
				// Conversion is saturated before multiplication; large broker lifetimes
				// cannot overflow into an already expired or negative duration.
				milliseconds := min(response.SessionLifetimeMS, int64(math.MaxInt64)/int64(time.Millisecond))
				// Anchor before transmission: response latency cannot extend the
				// broker's lease or authorize a frame after its expiry.
				connection.authExpires = exchangeStarted.Add(time.Duration(milliseconds) * time.Millisecond)
				if !time.Now().Before(connection.authExpires) {
					return &managementObservationError{reason: "permission_denied"}
				}
			}
			return nil
		}
		next = following
	}
	clear(next)
	return &managementObservationError{reason: "budget_exhausted"}
}

func kafkaManagementClientNonce(initial []byte) []byte {
	marker := []byte(",r=")
	at := bytes.LastIndex(initial, marker)
	if at < 0 {
		return nil
	}
	nonce := initial[at+len(marker):]
	if len(nonce) == 0 || len(nonce) > 128 || bytes.IndexByte(nonce, ',') >= 0 {
		return nil
	}
	return append([]byte(nil), nonce...)
}

// Bound every server-controlled SCRAM input before the library enters PBKDF2.
// This first profile does not accept optional extensions or mechanism fallback.
func validateKafkaManagementSCRAMFirst(first, clientNonce []byte) error {
	fields := bytes.Split(first, []byte(","))
	if len(first) > kafkaManagementAuthLimit || len(fields) != 3 || len(clientNonce) == 0 ||
		!bytes.HasPrefix(fields[0], []byte("r=")) || !bytes.HasPrefix(fields[1], []byte("s=")) || !bytes.HasPrefix(fields[2], []byte("i=")) {
		return &managementObservationError{reason: "invalid"}
	}
	nonce := fields[0][2:]
	if len(nonce) <= len(clientNonce) || len(nonce) > 256 || !bytes.HasPrefix(nonce, clientNonce) {
		return &managementObservationError{reason: "identity_mismatch"}
	}
	for _, value := range nonce {
		if value < 0x21 || value > 0x7e || value == ',' {
			return &managementObservationError{reason: "invalid"}
		}
	}
	encodedSalt := fields[1][2:]
	if len(encodedSalt) == 0 || len(encodedSalt) > 344 {
		return &managementObservationError{reason: "budget_exhausted"}
	}
	salt, err := base64.StdEncoding.Strict().DecodeString(string(encodedSalt))
	defer clear(salt)
	if err != nil || len(salt) < 1 || len(salt) > 256 || base64.StdEncoding.EncodeToString(salt) != string(encodedSalt) {
		return &managementObservationError{reason: "invalid"}
	}
	iterationsText := fields[2][2:]
	if len(iterationsText) == 0 || len(iterationsText) > 6 || iterationsText[0] == '0' {
		return &managementObservationError{reason: "invalid"}
	}
	for _, value := range iterationsText {
		if value < '0' || value > '9' {
			return &managementObservationError{reason: "invalid"}
		}
	}
	iterations, err := strconv.ParseUint(string(iterationsText), 10, 32)
	if err != nil || iterations < 4096 {
		return &managementObservationError{reason: "invalid"}
	}
	if iterations > kafkaManagementSCRAMIterations {
		return &managementObservationError{reason: "budget_exhausted"}
	}
	return nil
}
func validateKafkaManagementSCRAMFinal(final []byte, mechanism string) error {
	if len(final) > 128 || !bytes.HasPrefix(final, []byte("v=")) || bytes.IndexByte(final, ',') >= 0 {
		return &managementObservationError{reason: "permission_denied"}
	}
	expected := 32
	if mechanism == "SCRAM-SHA-512" {
		expected = 64
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(string(final[2:]))
	defer clear(signature)
	if err != nil || len(signature) != expected || base64.StdEncoding.EncodeToString(signature) != string(final[2:]) {
		return &managementObservationError{reason: "invalid"}
	}
	return nil // The pinned SASL session compares the expected server signature.
}

func kafkaManagementProviderError(code int16) error {
	reason := "invalid"
	switch code {
	case 0:
		return nil
	case 29, 30, 31, 53, 58:
		reason = "permission_denied"
	case 33, 35, 43:
		reason = "unsupported"
	case 3:
		reason = "not_observed"
	case 5, 6, 15, 16, 74, 75, 78:
		reason = "fenced"
	case 7:
		reason = "timeout"
	case 14:
		reason = "not_observed"
	case 88:
		reason = "unqualified"
	}
	return &managementObservationError{reason: reason}
}
func kafkaManagementErrorReason(err error) string {
	if err == nil {
		return ""
	}
	var typed *managementObservationError
	if errors.As(err, &typed) {
		return typed.reason
	}
	var budget *managementBudgetError
	if errors.As(err, &budget) {
		return "budget_exhausted"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return "invalid"
}
