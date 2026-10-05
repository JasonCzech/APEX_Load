package collectoragent

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const FeatureMessagingManagementEvidenceV1 = "messaging_management_evidence_v1"
const ManagementEvidenceVersion uint32 = 2
const ManagementPollVersion uint32 = 1
const MaxManagementCohort = 32

// ManagementJob is immutable signed source approval, not a collection admission.
// Only a separately signed poll grant permits a management request.
type ManagementJob struct {
	Version      uint32 `json:"version"`
	ScopeHash    string `json:"scope_hash"`
	IdentityHash string `json:"identity_hash"`
}

type ManagementPollMember struct {
	JobID        string `json:"job_id"`
	RunID        string `json:"run_id"`
	NextSequence int64  `json:"next_sequence,string"`
}

func (m *ManagementPollMember) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := strictManagementJSON(data, &fields); err != nil {
		return err
	}
	raw, ok := fields["next_sequence"]
	if !ok {
		return errors.New("management cohort sequence is required")
	}
	var sequence string
	if err := json.Unmarshal(raw, &sequence); err != nil {
		return errors.New("management cohort sequence must be an exact string")
	}
	n, err := managementDecimal(sequence, false)
	if err != nil {
		return err
	}
	type alias ManagementPollMember
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	decoded.NextSequence = n
	*m = ManagementPollMember(decoded)
	return nil
}

type ManagementPollGrant struct {
	Version                uint32                 `json:"version"`
	PollID                 string                 `json:"poll_id"`
	ProjectID              string                 `json:"project_id"`
	SourceID               string                 `json:"source_id"`
	SourceRevision         int64                  `json:"source_revision"`
	CollectorID            string                 `json:"collector_id"`
	CollectorInstanceID    string                 `json:"collector_instance_id"`
	LeaderJobID            string                 `json:"leader_job_id"`
	Nonce                  string                 `json:"nonce"`
	Pack                   string                 `json:"pack"`
	Destination            string                 `json:"destination"`
	ScopeHash              string                 `json:"scope_hash"`
	IdentityHash           string                 `json:"identity_hash"`
	Generation             string                 `json:"generation"`
	PollSequence           string                 `json:"poll_sequence"`
	CohortHash             string                 `json:"cohort_hash"`
	Cohort                 []ManagementPollMember `json:"cohort"`
	IssuedAt               time.Time              `json:"issued_at"`
	Deadline               time.Time              `json:"deadline"`
	AuthorizationExpiresAt time.Time              `json:"authorization_expires_at"`
	AcceptedCoreDigest     string                 `json:"accepted_core_digest,omitempty"`
	UnavailableReason      string                 `json:"unavailable_reason,omitempty"`
	Credentials            *SealedCredentials     `json:"credentials,omitempty"`
}

type SignedManagementPoll struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type ManagementPollControl struct {
	Operation           string `json:"operation"`
	InstanceID          string `json:"instance_id"`
	Nonce               string `json:"nonce,omitempty"`
	PollID              string `json:"poll_id,omitempty"`
	Generation          string `json:"generation,omitempty"`
	CompletionConfirmed bool   `json:"completion_confirmed,omitempty"`
}

type ManagementPollResponse struct {
	State string                `json:"state"`
	Grant *SignedManagementPoll `json:"grant,omitempty"`
}

type ManagementReadinessScope struct {
	ScopeHash    string `json:"scope_hash"`
	IdentityHash string `json:"identity_hash"`
}

func ManagementCohortHash(members []ManagementPollMember) (string, error) {
	if len(members) < 1 || len(members) > MaxManagementCohort {
		return "", errors.New("management cohort exceeds bounds")
	}
	sorted := append([]ManagementPollMember(nil), members...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].JobID < sorted[j].JobID })
	for i, m := range sorted {
		if !managementLabel(m.JobID, 1024) || !managementLabel(m.RunID, 256) || m.NextSequence < 0 || i > 0 && m.JobID == sorted[i-1].JobID {
			return "", errors.New("invalid management cohort")
		}
	}
	body, err := json.Marshal(sorted)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func (g ManagementPollGrant) Validate() error {
	if g.AcceptedCoreDigest != "" && (!managementHash(g.AcceptedCoreDigest) || g.Credentials != nil) {
		return errors.New("invalid accepted management core")
	}
	if g.UnavailableReason != "" && g.UnavailableReason != "permission_denied" && g.UnavailableReason != "identity_mismatch" {
		return errors.New("invalid management admission coverage")
	}
	if g.UnavailableReason != "" && g.Credentials != nil {
		return errors.New("unavailable management grant cannot carry credentials")
	}
	if g.Version != ManagementPollVersion || g.SourceRevision < 1 || !managementLabel(g.PollID, 256) || !managementLabel(g.ProjectID, 256) || !managementLabel(g.SourceID, 256) || !managementLabel(g.CollectorID, 256) || !managementLabel(g.CollectorInstanceID, 128) || !managementLabel(g.LeaderJobID, 1024) || !managementLabel(g.Nonce, 128) || !managementLabel(g.Destination, 2048) || !isManagementPack(g.Pack) {
		return errors.New("invalid management poll identity")
	}
	if !managementHash(g.ScopeHash) || !managementHash(g.IdentityHash) || !managementHash(g.CohortHash) {
		return errors.New("invalid management poll scope")
	}
	if _, err := managementDecimal(g.Generation, true); err != nil {
		return err
	}
	if _, err := managementDecimal(g.PollSequence, true); err != nil {
		return err
	}
	hash, err := ManagementCohortHash(g.Cohort)
	if err != nil || hash != g.CohortHash {
		return errors.New("management cohort hash mismatch")
	}
	leader := false
	for _, m := range g.Cohort {
		leader = leader || m.JobID == g.LeaderJobID
	}
	if !leader || g.IssuedAt.IsZero() || !g.Deadline.After(g.IssuedAt) || g.Deadline.Sub(g.IssuedAt) > 30*time.Second || !g.AuthorizationExpiresAt.After(g.IssuedAt) {
		return errors.New("invalid management poll window")
	}
	return nil
}

func SignManagementPoll(grant ManagementPollGrant, key ed25519.PrivateKey) (SignedManagementPoll, error) {
	if len(key) != ed25519.PrivateKeySize {
		return SignedManagementPoll{}, errors.New("monitor signing key unavailable")
	}
	if err := grant.Validate(); err != nil {
		return SignedManagementPoll{}, err
	}
	body, err := json.Marshal(grant)
	if err != nil || len(body) > 32768 {
		return SignedManagementPoll{}, errors.New("management grant exceeds bounds")
	}
	return SignedManagementPoll{Payload: string(body), Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, body))}, nil
}

// allowCompletedExpired permits only signature/identity inspection of durable
// completed evidence. It never authorizes another observer or network call.
func VerifyManagementPoll(signed SignedManagementPoll, key ed25519.PublicKey, collector, instance string, now time.Time, allowCompletedExpired bool) (ManagementPollGrant, error) {
	var grant ManagementPollGrant
	if len(key) != ed25519.PublicKeySize || len(signed.Payload) > 32768 || len(signed.Signature) > 128 {
		return grant, errors.New("invalid management poll grant")
	}
	sig, err := base64.RawStdEncoding.DecodeString(signed.Signature)
	if err != nil || !ed25519.Verify(key, []byte(signed.Payload), sig) {
		return grant, errors.New("management grant signature rejected")
	}
	if err = strictManagementJSON([]byte(signed.Payload), &grant); err != nil {
		return ManagementPollGrant{}, err
	}
	if err = grant.Validate(); err != nil {
		return ManagementPollGrant{}, err
	}
	if grant.CollectorID != collector || grant.CollectorInstanceID != instance || grant.IssuedAt.After(now.Add(30*time.Second)) {
		return ManagementPollGrant{}, errors.New("management grant owner rejected")
	}
	if !allowCompletedExpired && (grant.AcceptedCoreDigest != "" || !grant.AuthorizationExpiresAt.After(now) || !grant.Deadline.After(now)) {
		return ManagementPollGrant{}, errors.New("management poll lease expired")
	}
	return grant, nil
}

func managementDecimal(value string, positive bool) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 || positive && n == 0 || strconv.FormatInt(n, 10) != value {
		return 0, errors.New("management population must be an exact decimal string")
	}
	return n, nil
}
func managementHash(value string) bool {
	_, err := hex.DecodeString(value)
	return len(value) == 64 && value == strings.ToLower(value) && err == nil
}
func managementLabel(value string, max int) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func isManagementPack(pack string) bool {
	return pack == "kafka-offsets" || pack == "servicebus-runtime" || pack == "solace-semp"
}

// Reject duplicate/case-aliased JSON keys before encoding/json can overwrite
// signed scope or exact numeric fields. Total grant/spool sizes are bounded by
// the caller; recursion is additionally bounded here.
func strictManagementJSON(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return errors.New("management JSON nesting exceeds bounds")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				text, ok := key.(string)
				if !ok {
					return errors.New("invalid management JSON key")
				}
				folded := strings.ToLower(text)
				if text != folded {
					return errors.New("noncanonical management JSON key")
				}
				if seen[folded] {
					return errors.New("duplicate management JSON key")
				}
				seen[folded] = true
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("invalid management JSON object")
			}
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("invalid management JSON array")
			}
		default:
			return errors.New("invalid management JSON delimiter")
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("management JSON requires one value")
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	return nil
}
