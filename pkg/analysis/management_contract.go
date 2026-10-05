package analysis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ManagementScopeVersion     uint32 = 1
	MessagingManagementVersion uint32 = 1
	MonitorEvidenceVersion     uint32 = 2
	MaxManagementEntities             = 256
	MaxManagementSnapshotBytes        = 512 << 10
	MaxMonitorEvidenceBytes           = 1 << 20
	MaxMonitorEvidencePoints          = 512
	ManagementKindKafka               = "kafka_offsets"
	ManagementKindServiceBus          = "servicebus_runtime"
	ManagementKindSolace              = "solace_semp"
	ManagementStateObserved           = "observed"
	ManagementStateMissing            = "missing"
	ManagementStateUnsupported        = "unsupported"
	ManagementStatePermission         = "permission"
	ManagementStateBudget             = "budget"
	ManagementStateInvalid            = "invalid"
	ManagementStateStale              = "stale"
	ManagementStateFenced             = "fenced"
	ManagementStateCancelled          = "cancelled"
	ManagementStateThrottled          = "throttled"
)

// ManagementInteger keeps exact nonnegative int64 values out of float64 metric
// points. Absence is explicit; an observed "0" is a measurement.
type ManagementInteger struct {
	State  string  `json:"state"`
	Value  *string `json:"value,omitempty"`
	Reason string  `json:"reason,omitempty"`
}

func ParseManagementInteger(value string) (int64, error) {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return 0, errors.New("management integer is not canonical")
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, errors.New("management integer is not canonical")
		}
	}
	v, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, errors.New("management integer exceeds int64")
	}
	return v, nil
}
func ObservedManagementInteger(value int64) (ManagementInteger, error) {
	if value < 0 {
		return ManagementInteger{}, errors.New("management integer is negative")
	}
	s := strconv.FormatInt(value, 10)
	return ManagementInteger{State: ManagementStateObserved, Value: &s}, nil
}
func (v ManagementInteger) Validate() error {
	if !validManagementState(v.State) {
		return errors.New("unsupported management observation state")
	}
	if v.State == ManagementStateObserved {
		if v.Value == nil || v.Reason != "" {
			return errors.New("observed management integer requires only an exact value")
		}
		_, err := ParseManagementInteger(*v.Value)
		return err
	}
	if v.Value != nil || !validManagementReason(v.Reason) || v.Reason == "" {
		return errors.New("unavailable management integer requires a bounded reason and no value")
	}
	return nil
}
func (v ManagementInteger) Clone() ManagementInteger {
	if v.Value != nil {
		x := *v.Value
		v.Value = &x
	}
	return v
}
func (v *ManagementInteger) UnmarshalJSON(data []byte) error {
	type plain ManagementInteger
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := requireManagementFields(data, "state"); err != nil {
		return err
	}
	if err := ManagementInteger(x).Validate(); err != nil {
		return err
	}
	*v = ManagementInteger(x)
	return nil
}
func validManagementState(s string) bool {
	switch s {
	case ManagementStateObserved, ManagementStateMissing, ManagementStateUnsupported, ManagementStatePermission, ManagementStateBudget, ManagementStateInvalid, ManagementStateStale, ManagementStateFenced, ManagementStateCancelled, ManagementStateThrottled:
		return true
	}
	return false
}
func validManagementReason(s string) bool {
	switch s {
	case "", "not_observed", "permission_denied", "budget_exhausted", "timeout", "throttled", "cancelled", "fenced", "unsupported", "invalid", "stale", "unqualified", "identity_mismatch", "inconsistent_position", "exact_only":
		return true
	}
	return false
}

// ManagementIdentity describes public approved principal/policy identity, never
// an authentication token or password. It participates in source coalescing.
type ManagementIdentity struct {
	Mode              string `json:"mode"`
	Principal         string `json:"principal"`
	PolicyRevision    string `json:"policy_revision"`
	CredentialRef     string `json:"credential_ref,omitempty"`
	CredentialVersion string `json:"credential_version,omitempty"`
}

func (i ManagementIdentity) Validate() error {
	switch i.Mode {
	case "sasl_plain", "sasl_scram", "basic", "shared_key", "workload_identity", "managed_identity", "mtls", "anonymous":
	default:
		return errors.New("unsupported management identity mode")
	}
	for _, x := range []string{i.Principal, i.PolicyRevision} {
		if !validManagementText(x, 256, false) {
			return errors.New("management principal and policy revision are required")
		}
	}
	for _, x := range []string{i.CredentialRef, i.CredentialVersion} {
		if !validManagementText(x, 256, true) {
			return errors.New("invalid management credential reference")
		}
	}
	if i.CredentialVersion != "" && i.CredentialRef == "" {
		return errors.New("credential version requires a reference")
	}
	return nil
}
func (i ManagementIdentity) Fingerprint() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(i)
	if err != nil {
		return "", err
	}
	return managementHash(b), nil
}
func (i *ManagementIdentity) UnmarshalJSON(data []byte) error {
	type plain ManagementIdentity
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := ManagementIdentity(x).Validate(); err != nil {
		return err
	}
	*i = ManagementIdentity(x)
	return nil
}

type KafkaManagementEntity struct {
	Topic     string `json:"topic"`
	Partition int32  `json:"partition"`
	Group     string `json:"group"`
}
type ServiceBusManagementEntity struct {
	Queue        string `json:"queue,omitempty"`
	Topic        string `json:"topic,omitempty"`
	Subscription string `json:"subscription,omitempty"`
}
type SolaceManagementEntity struct {
	VPN           string `json:"vpn"`
	Queue         string `json:"queue,omitempty"`
	TopicEndpoint string `json:"topic_endpoint,omitempty"`
}
type ManagementEntity struct {
	Kafka      *KafkaManagementEntity      `json:"kafka,omitempty"`
	ServiceBus *ServiceBusManagementEntity `json:"service_bus,omitempty"`
	Solace     *SolaceManagementEntity     `json:"solace,omitempty"`
}

func (e ManagementEntity) Kind() string {
	if e.Kafka != nil {
		return ManagementKindKafka
	}
	if e.ServiceBus != nil {
		return ManagementKindServiceBus
	}
	if e.Solace != nil {
		return ManagementKindSolace
	}
	return ""
}
func (e ManagementEntity) Validate() error {
	n := 0
	for _, yes := range []bool{e.Kafka != nil, e.ServiceBus != nil, e.Solace != nil} {
		if yes {
			n++
		}
	}
	if n != 1 {
		return errors.New("management entity requires exactly one provider")
	}
	if k := e.Kafka; k != nil {
		if !validManagementText(k.Topic, 256, false) || !validManagementText(k.Group, 256, false) || k.Partition < 0 {
			return errors.New("invalid Kafka management entity")
		}
	}
	if s := e.ServiceBus; s != nil {
		q := s.Queue != ""
		pair := s.Topic != "" && s.Subscription != ""
		if q == pair || q && (s.Topic != "" || s.Subscription != "") || !q && !pair {
			return errors.New("Service Bus entity requires queue or topic/subscription")
		}
		for _, x := range []string{s.Queue, s.Topic, s.Subscription} {
			if !validManagementText(x, 256, true) {
				return errors.New("invalid Service Bus entity")
			}
		}
	}
	if s := e.Solace; s != nil {
		if !validManagementText(s.VPN, 256, false) || (s.Queue == "") == (s.TopicEndpoint == "") {
			return errors.New("Solace entity requires VPN and queue or topic endpoint")
		}
		for _, x := range []string{s.Queue, s.TopicEndpoint} {
			if !validManagementText(x, 256, true) {
				return errors.New("invalid Solace entity")
			}
		}
	}
	return nil
}
func (e ManagementEntity) ID() (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return managementHash(b), nil
}
func (e ManagementEntity) Clone() ManagementEntity {
	if e.Kafka != nil {
		x := *e.Kafka
		e.Kafka = &x
	}
	if e.ServiceBus != nil {
		x := *e.ServiceBus
		e.ServiceBus = &x
	}
	if e.Solace != nil {
		x := *e.Solace
		e.Solace = &x
	}
	return e
}
func (e *ManagementEntity) UnmarshalJSON(data []byte) error {
	type plain ManagementEntity
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := ManagementEntity(x).Validate(); err != nil {
		return err
	}
	*e = ManagementEntity(x)
	return nil
}
func (e *KafkaManagementEntity) UnmarshalJSON(data []byte) error {
	type plain KafkaManagementEntity
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := requireManagementFields(data, "topic", "partition", "group"); err != nil {
		return err
	}
	*e = KafkaManagementEntity(x)
	return nil
}

// Scope is immutable approved inventory. Hash canonicalizes ordering without
// altering the caller's entities; provider identity is separate from a URL.
type ManagementScope struct {
	Version   uint32             `json:"version"`
	Kind      string             `json:"kind"`
	Provider  string             `json:"provider"`
	Isolation string             `json:"isolation,omitempty"`
	Entities  []ManagementEntity `json:"entities"`
}

func (s ManagementScope) Validate() error {
	if s.Version != ManagementScopeVersion || !validManagementKind(s.Kind) {
		return errors.New("invalid management scope version or kind")
	}
	if err := validateManagementProvider(s.Provider); err != nil {
		return err
	}
	if s.Kind == ManagementKindKafka {
		if s.Isolation != "read_committed" && s.Isolation != "read_uncommitted" {
			return errors.New("Kafka management requires explicit isolation")
		}
	} else if s.Isolation != "" {
		return errors.New("isolation applies only to Kafka")
	}
	if len(s.Entities) < 1 || len(s.Entities) > MaxManagementEntities {
		return errors.New("management scope inventory exceeds bounds")
	}
	seen := map[string]bool{}
	for _, e := range s.Entities {
		if err := e.Validate(); err != nil {
			return err
		}
		if e.Kind() != s.Kind {
			return errors.New("management scope provider mismatch")
		}
		id, _ := e.ID()
		if seen[id] {
			return errors.New("duplicate management entity")
		}
		seen[id] = true
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(b) > 16<<10 {
		return errors.New("management scope exceeds source size limit")
	}
	return nil
}
func (s ManagementScope) Clone() ManagementScope {
	s.Entities = append([]ManagementEntity{}, s.Entities...)
	for j := range s.Entities {
		s.Entities[j] = s.Entities[j].Clone()
	}
	return s
}
func (s ManagementScope) Hash() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	x := s.Clone()
	sort.Slice(x.Entities, func(i, j int) bool { a, _ := x.Entities[i].ID(); b, _ := x.Entities[j].ID(); return a < b })
	b, err := json.Marshal(x)
	if err != nil {
		return "", err
	}
	return managementHash(b), nil
}
func (s *ManagementScope) UnmarshalJSON(data []byte) error {
	type plain ManagementScope
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := requireManagementFields(data, "version", "kind", "provider", "entities"); err != nil {
		return err
	}
	if err := ManagementScope(x).Validate(); err != nil {
		return err
	}
	*s = ManagementScope(x)
	return nil
}

// The public provider identity is retained in snapshots and archives. Apply
// the same disclosure boundary even when the approved scope is unavailable.
func validateManagementProvider(provider string) error {
	if !validManagementText(provider, 256, false) {
		return errors.New("invalid management provider identity")
	}
	if u, err := url.Parse(provider); err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("management provider identity cannot contain URL credentials, query or fragment")
	}
	return nil
}

func validManagementKind(k string) bool {
	return k == ManagementKindKafka || k == ManagementKindServiceBus || k == ManagementKindSolace
}
func validManagementText(s string, max int, optional bool) bool {
	if s == "" {
		return optional
	}
	if len(s) > max || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func managementHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validManagementHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func requireManagementFields(data []byte, keys ...string) error {
	var f map[string]json.RawMessage
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	for _, k := range keys {
		v, ok := f[k]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return errors.New("required management field missing")
		}
	}
	return nil
}

// Strict bounded JSON is scoped to the new contract. Legacy metric artifacts
// deliberately retain their existing decoder behavior.
func decodeManagementJSON(data []byte, target any) error {
	if !utf8.Valid(data) {
		return errors.New("invalid UTF-8 management JSON")
	}
	if len(data) > MaxMonitorEvidenceBytes {
		return errors.New("management JSON exceeds byte limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	nodes := 0
	if err := walkManagementJSON(d, 0, &nodes); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("management JSON has trailing input")
	}
	if err := canonicalManagementKeys(data, reflect.TypeOf(target)); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	return nil
}
func walkManagementJSON(d *json.Decoder, depth int, nodes *int) error {
	*nodes++
	if depth > 16 || *nodes > 40000 {
		return errors.New("management JSON exceeds structural limit")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	if t == nil {
		return errors.New("null is not a management observation")
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := k.(string)
			if !ok {
				return errors.New("invalid management JSON key")
			}
			if seen[key] {
				return errors.New("duplicate management JSON key")
			}
			seen[key] = true
			if err := walkManagementJSON(d, depth+1, nodes); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := walkManagementJSON(d, depth+1, nodes); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid management JSON delimiter")
	}
	end, err := d.Token()
	if err != nil {
		return err
	}
	if delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
		return fmt.Errorf("invalid management JSON closing delimiter")
	}
	return nil
}

// encoding/json accepts case-insensitive field aliases. Reject them explicitly
// so one noncanonical key cannot overwrite a validated observed zero.
func canonicalManagementKeys(data []byte, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		allowed := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == "-" {
				continue
			}
			if tag == "" {
				tag = f.Name
			}
			allowed[tag] = f.Type
		}
		for key, value := range fields {
			field, ok := allowed[key]
			if !ok {
				return errors.New("noncanonical or unknown management JSON field")
			}
			if err := canonicalManagementKeys(value, field); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		var values []json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		for _, v := range values {
			if err := canonicalManagementKeys(v, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		var values map[string]json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return err
		}
		for _, v := range values {
			if err := canonicalManagementKeys(v, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
