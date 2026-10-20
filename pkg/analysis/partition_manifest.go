package analysis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	PartitionManifestVersion  = 1
	PartitionProducerVersion  = "apexload.normalized_partition.v1"
	MaxPartitionManifestBytes = 1 << 20
	MaxNormalizedPartitions   = 1024
	// MaxPartitionObjectKeyBytes bounds a manifest partition's object key.
	MaxPartitionObjectKeyBytes = 274
	// maxPartitionBytes bounds one normalized object.
	maxPartitionBytes = int64(20 << 30)
)

var ErrPartitionManifest = errors.New("normalized partition manifest invalid or excessive")

// A nil Metric or Identity is unknown/mixed, never a truncated membership set.
type NormalizedPartitionIdentity struct {
	Source     string            `json:"source"`
	Host       string            `json:"host"`
	Service    string            `json:"service"`
	Dimensions map[string]string `json:"dimensions"`
}
type NormalizedPartitionStats struct {
	Version         int                          `json:"version"`
	SchemaVersion   string                       `json:"schema_version"`
	IdentityVersion int                          `json:"identity_version"`
	Kind            string                       `json:"kind"`
	Unit            string                       `json:"unit"`
	Rows            int64                        `json:"rows"` // Accepted measurements, including explicit missing.
	Valid           int64                        `json:"valid"`
	Missing         int64                        `json:"missing"`
	MinTime         *time.Time                   `json:"min_time,omitempty"` // Inclusive, actual normalized UTC microseconds.
	MaxTime         *time.Time                   `json:"max_time,omitempty"`
	Metric          *string                      `json:"metric,omitempty"`
	Identity        *NormalizedPartitionIdentity `json:"identity,omitempty"`
	SourceRows      int64                        `json:"source_rows,omitempty"` // Histogram only: accepted source rows grouped into its windows.
	// MaxWindowEnd is, for a histogram partition, the latest end of any of
	// its windows: a partition whose windows all end by a query's start
	// cannot hold the window that crosses into it.
	MaxWindowEnd *time.Time `json:"max_window_end,omitempty"`
}
type NormalizedPartition struct {
	ObjectKey string                   `json:"object_key"`
	SHA256    string                   `json:"sha256"`
	Bytes     int64                    `json:"bytes"`
	Stats     NormalizedPartitionStats `json:"stats"`
}
type NormalizedPartitionManifest struct {
	Version        int                   `json:"version"`
	Producer       string                `json:"producer"`
	ProjectID      string                `json:"project_id"`
	DatasetID      string                `json:"dataset_id"`
	MappingSHA256  string                `json:"mapping_sha256"`
	MappingVersion int                   `json:"mapping_version"`
	Partitions     []NormalizedPartition `json:"partitions"`
	Digest         string                `json:"digest"`
}
type PartitionSelection struct {
	Metric                string
	IdentityVersion       int
	Source, Host, Service string
	Dimensions            map[string]string
	From, To              time.Time
}

func partitionHash(v string) bool {
	if len(v) != 64 || v != strings.ToLower(v) {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}
func partitionText(v string, max int, required bool) bool {
	return len(v) <= max && utf8.ValidString(v) && !strings.ContainsRune(v, 0) && (!required || strings.TrimSpace(v) != "")
}
func canonicalPartitionTime(t *time.Time) bool {
	return t != nil && t.Location() == time.UTC && t.Year() >= 1 && t.Year() <= 9999 && t.Nanosecond()%1000 == 0
}
func (s NormalizedPartitionStats) Validate() error {
	if s.Version != PartitionManifestVersion || s.IdentityVersion < 1 || s.IdentityVersion > 2 || s.Rows < 0 || s.Valid < 0 || s.Missing < 0 || s.Valid > math.MaxInt64-s.Missing || s.Valid+s.Missing != s.Rows {
		return ErrPartitionManifest
	}
	// Histogram partitions hold distribution rows; every other kind holds
	// metric points.
	switch s.Kind {
	case KindGauge, KindCounter, KindObservation, KindSummary:
		if s.SchemaVersion != SchemaVersion || s.SourceRows != 0 {
			return ErrPartitionManifest
		}
	case KindHistogram:
		if s.SchemaVersion != DistributionSchemaVersion || s.SourceRows < s.Rows {
			return ErrPartitionManifest
		}
	default:
		return ErrPartitionManifest
	}
	unit, scale, err := NormalizeUnit(s.Unit)
	if err != nil || unit != s.Unit || scale != 1 {
		return ErrPartitionManifest
	}
	if s.Rows == 0 {
		if s.MinTime != nil || s.MaxTime != nil || s.Metric != nil || s.Identity != nil || s.MaxWindowEnd != nil {
			return ErrPartitionManifest
		}
	} else if !canonicalPartitionTime(s.MinTime) || !canonicalPartitionTime(s.MaxTime) || s.MaxTime.Before(*s.MinTime) {
		return ErrPartitionManifest
	}
	if s.MaxWindowEnd != nil && (s.Kind != KindHistogram || !canonicalPartitionTime(s.MaxWindowEnd) || !s.MaxWindowEnd.After(*s.MaxTime)) {
		return ErrPartitionManifest
	}
	if s.Metric != nil && !partitionText(*s.Metric, 256, true) {
		return ErrPartitionManifest
	}
	if i := s.Identity; i != nil {
		if !partitionText(i.Source, 256, true) || !partitionText(i.Host, 256, false) || !partitionText(i.Service, 256, false) || i.Dimensions == nil || len(i.Dimensions) > 16 {
			return ErrPartitionManifest
		}
		for k, v := range i.Dimensions {
			if !ValidMetricDimension(k, v) {
				return ErrPartitionManifest
			}
		}
		if s.IdentityVersion == 1 && (i.Host != "" || i.Service != "" || len(i.Dimensions) != 0) {
			return ErrPartitionManifest
		}
	}
	return nil
}

// AcceptedRows is the source rows a partition consumed, the unit of dataset
// accepted and rejected counts.
func (s NormalizedPartitionStats) AcceptedRows() int64 {
	if s.Kind == KindHistogram {
		return s.SourceRows
	}
	return s.Rows
}

func (s NormalizedPartitionStats) Clone() NormalizedPartitionStats {
	if s.MinTime != nil {
		v := *s.MinTime
		s.MinTime = &v
	}
	if s.MaxTime != nil {
		v := *s.MaxTime
		s.MaxTime = &v
	}
	if s.MaxWindowEnd != nil {
		v := *s.MaxWindowEnd
		s.MaxWindowEnd = &v
	}
	if s.Metric != nil {
		v := *s.Metric
		s.Metric = &v
	}
	if s.Identity != nil {
		v := *s.Identity
		v.Dimensions = maps.Clone(v.Dimensions)
		s.Identity = &v
	}
	return s
}
func (m *NormalizedPartitionManifest) Clone() *NormalizedPartitionManifest {
	if m == nil || m.Validate() != nil {
		return nil
	}
	v := *m
	v.Partitions = make([]NormalizedPartition, len(m.Partitions))
	for i, p := range m.Partitions {
		v.Partitions[i] = p
		v.Partitions[i].Stats = p.Stats.Clone()
	}
	return &v
}
func PartitionMappingDigest(mapping ParquetMapping) (string, error) {
	if err := mapping.ValidateDefinition(); err != nil {
		return "", err
	}
	body, err := MarshalBoundedAnalysisJSON(mapping, 128<<10)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:]), nil
}
func (m NormalizedPartitionManifest) canonicalDigest() (string, error) {
	m.Digest = ""
	// The digest field is empty while hashing, but its final 64 characters
	// remain part of the manifest total budget.
	body, err := MarshalBoundedAnalysisJSON(m, MaxPartitionManifestBytes-64)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:]), nil
}

// PartitionStatsFitManifest reports whether a manifest holding these
// partition stats fits its byte limit however long the object keys, hashes
// and object sizes its writer chooses are (as long as Validate allows). The
// native worker coarsens partition metadata until this holds, so the
// controller's manifest, keyed by full object keys rather than the worker's
// short handles, cannot exceed the limit the worker checked.
func PartitionStatsFitManifest(project, dataset string, mappingVersion int, stats []NormalizedPartitionStats) bool {
	key, hash := strings.Repeat("k", MaxPartitionObjectKeyBytes), strings.Repeat("0", 64)
	m := NormalizedPartitionManifest{Version: PartitionManifestVersion, Producer: PartitionProducerVersion, ProjectID: project, DatasetID: dataset, MappingSHA256: hash, MappingVersion: mappingVersion, Partitions: make([]NormalizedPartition, len(stats))}
	for i := range stats {
		m.Partitions[i] = NormalizedPartition{ObjectKey: key, SHA256: hash, Bytes: maxPartitionBytes, Stats: stats[i]}
	}
	_, err := m.canonicalDigest()
	return err == nil
}

func NewNormalizedPartitionManifest(project, dataset string, mapping ParquetMapping, parts []NormalizedPartition) (*NormalizedPartitionManifest, error) {
	if len(parts) < 1 || len(parts) > MaxNormalizedPartitions {
		return nil, ErrPartitionManifest
	}
	mappingHash, err := PartitionMappingDigest(mapping)
	if err != nil {
		return nil, err
	}
	for _, p := range parts {
		if p.Stats.Validate() != nil {
			return nil, ErrPartitionManifest
		}
	}
	if _, err := MarshalBoundedAnalysisJSON(parts, MaxPartitionManifestBytes); err != nil {
		return nil, err
	}
	m := &NormalizedPartitionManifest{Version: PartitionManifestVersion, Producer: PartitionProducerVersion, ProjectID: project, DatasetID: dataset, MappingSHA256: mappingHash, MappingVersion: mapping.Version, Partitions: make([]NormalizedPartition, len(parts))}
	if len(parts) > MaxNormalizedPartitions {
		return nil, ErrPartitionManifest
	}
	for i, p := range parts {
		if p.Stats.Validate() != nil {
			return nil, ErrPartitionManifest
		}
		m.Partitions[i] = p
		m.Partitions[i].Stats = p.Stats.Clone()
	}
	m.Digest, err = m.canonicalDigest()
	if err != nil {
		return nil, err
	}
	if err = m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}
func (m *NormalizedPartitionManifest) Validate() error {
	if m == nil || m.Version != PartitionManifestVersion || m.Producer != PartitionProducerVersion || !partitionText(m.ProjectID, 256, true) || !partitionText(m.DatasetID, 256, true) || !partitionHash(m.MappingSHA256) || !partitionHash(m.Digest) || m.MappingVersion < 1 || m.MappingVersion > 3 || len(m.Partitions) < 1 || len(m.Partitions) > MaxNormalizedPartitions {
		return ErrPartitionManifest
	}
	seen := map[string]bool{}
	var rows, bytes int64
	for _, p := range m.Partitions {
		if !partitionText(p.ObjectKey, MaxPartitionObjectKeyBytes, true) || seen[p.ObjectKey] || !partitionHash(p.SHA256) || p.Bytes < 12 || p.Bytes > maxPartitionBytes || p.Stats.Validate() != nil || p.Stats.IdentityVersion != (ParquetMapping{Version: m.MappingVersion}).IdentityVersion() || p.Stats.AcceptedRows() > math.MaxInt64-rows || p.Bytes > (20<<30)-bytes {
			return ErrPartitionManifest
		}
		if p.Stats.IdentityVersion == 1 && p.Stats.Identity != nil && p.Stats.Identity.Source != m.DatasetID {
			return ErrPartitionManifest
		}
		seen[p.ObjectKey] = true
		rows += p.Stats.AcceptedRows()
		bytes += p.Bytes
	}
	digest, err := m.canonicalDigest()
	if err != nil || digest != m.Digest {
		return ErrPartitionManifest
	}
	return nil
}

// MatchesArtifacts binds a proof to the existing immutable dataset population.
// Mapping digest must be captured before any lossy query metadata projection.
func (m *NormalizedPartitionManifest) MatchesArtifacts(project, dataset, mappingHash string, mappingVersion int, keys []string, hashes map[string]string, accepted int64) error {
	if m.Validate() != nil || m.ProjectID != project || m.DatasetID != dataset || m.MappingSHA256 != mappingHash || m.MappingVersion != mappingVersion || len(keys) != len(m.Partitions) || len(hashes) != len(keys) || accepted < 0 {
		return ErrPartitionManifest
	}
	byKey := map[string]NormalizedPartition{}
	for _, p := range m.Partitions {
		byKey[p.ObjectKey] = p
	}
	seen := map[string]bool{}
	var rows int64
	for _, key := range keys {
		p, ok := byKey[key]
		if !ok || seen[key] || p.SHA256 != hashes[key] || p.Stats.AcceptedRows() > math.MaxInt64-rows {
			return ErrPartitionManifest
		}
		seen[key] = true
		rows += p.Stats.AcceptedRows()
	}
	if rows != accepted {
		return ErrPartitionManifest
	}
	return nil
}

// Select returns object ordinals only after full manifest/query admission.
// Submicrosecond bounds disable time pruning; native literal casts remain the
// authority and cannot be approximated with Go nanosecond comparisons.
func (m *NormalizedPartitionManifest) Select(q PartitionSelection) ([]int, error) {
	if m.Validate() != nil || !partitionText(q.Metric, 256, true) || q.IdentityVersion != (ParquetMapping{Version: m.MappingVersion}).IdentityVersion() || q.From.IsZero() || !q.To.After(q.From) || len(q.Dimensions) > 16 {
		return nil, ErrPartitionManifest
	}
	for _, v := range []string{q.Source, q.Host, q.Service} {
		if !partitionText(v, 256, false) {
			return nil, ErrPartitionManifest
		}
	}
	for k, v := range q.Dimensions {
		if !ValidMetricDimension(k, v) {
			return nil, ErrPartitionManifest
		}
	}
	timeKnown := q.From.Nanosecond()%1000 == 0 && q.To.Nanosecond()%1000 == 0
	out := []int{}
	for index, p := range m.Partitions {
		s := p.Stats
		if s.Rows == 0 || s.Metric != nil && *s.Metric != q.Metric {
			continue
		}
		// A histogram window can start before From and end inside the range:
		// its earlier partition stays selected, unless all of its windows end
		// by From, so the query sees the lead-in and marks the bucket partial
		// rather than complete.
		before := s.MaxTime.Before(q.From)
		if s.Kind == KindHistogram {
			before = s.MaxWindowEnd != nil && !s.MaxWindowEnd.After(q.From)
		}
		if timeKnown && (before || !s.MinTime.Before(q.To)) {
			continue
		}
		if i := s.Identity; i != nil {
			if q.Source != "" && i.Source != q.Source || q.Host != "" && i.Host != q.Host || q.Service != "" && i.Service != q.Service {
				continue
			}
			mismatch := false
			for k, v := range q.Dimensions {
				actual, ok := i.Dimensions[k]
				if !ok || actual != v {
					mismatch = true
					break
				}
			}
			if mismatch {
				continue
			}
		}
		out = append(out, index)
	}
	return out, nil
}
func DecodeNormalizedPartitionManifest(data []byte) (*NormalizedPartitionManifest, error) {
	if len(data) < 2 || len(data) > MaxPartitionManifestBytes || !utf8.Valid(data) || !normalizedIdentityUnicodeEscapes(string(data)) {
		return nil, ErrPartitionManifest
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	nodes := 0
	if err := preflightPartitionJSON(d, reflect.TypeOf(NormalizedPartitionManifest{}), 0, &nodes); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrPartitionManifest
	}
	var m NormalizedPartitionManifest
	if err := json.Unmarshal(data, &m); err != nil || m.Validate() != nil {
		return nil, ErrPartitionManifest
	}
	return &m, nil
}
func preflightPartitionJSON(d *json.Decoder, t reflect.Type, depth int, nodes *int) error {
	*nodes++
	if depth > 32 || *nodes > 65536 {
		return ErrPartitionManifest
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	token, err := d.Token()
	if err != nil {
		return ErrPartitionManifest
	}
	if token == nil {
		return ErrPartitionManifest
	}
	if t == reflect.TypeOf(time.Time{}) {
		v, ok := token.(string)
		if !ok || len(v) > 32 || !strings.HasSuffix(v, "Z") {
			return ErrPartitionManifest
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		if token != json.Delim('{') {
			return ErrPartitionManifest
		}
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			key, ok := k.(string)
			if err != nil || !ok || seen[key] {
				return ErrPartitionManifest
			}
			seen[key] = true
			var field reflect.Type
			if t.Kind() == reflect.Map {
				if len(seen) > 16 || len(key) > 128 {
					return ErrPartitionManifest
				}
				field = t.Elem()
			} else {
				for i := 0; i < t.NumField(); i++ {
					f := t.Field(i)
					if strings.Split(f.Tag.Get("json"), ",")[0] == key {
						field = f.Type
						break
					}
				}
				if field == nil {
					return ErrPartitionManifest
				}
			}
			if err := preflightPartitionJSON(d, field, depth+1, nodes); err != nil {
				return err
			}
		}
		if t.Kind() == reflect.Struct {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				tag := strings.Split(f.Tag.Get("json"), ",")
				if len(tag) == 1 && !seen[tag[0]] {
					return ErrPartitionManifest
				}
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('}') {
			return ErrPartitionManifest
		}
	case reflect.Slice:
		if token != json.Delim('[') {
			return ErrPartitionManifest
		}
		count := 0
		for d.More() {
			count++
			if count > MaxNormalizedPartitions {
				return ErrPartitionManifest
			}
			if err := preflightPartitionJSON(d, t.Elem(), depth+1, nodes); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim(']') {
			return ErrPartitionManifest
		}
	case reflect.String:
		v, ok := token.(string)
		if !ok || len(v) > 274 || !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return ErrPartitionManifest
		}
	case reflect.Int, reflect.Int64:
		n, ok := token.(json.Number)
		if !ok {
			return ErrPartitionManifest
		}
		if _, err := strconv.ParseInt(string(n), 10, 64); err != nil {
			return ErrPartitionManifest
		}
	default:
		return ErrPartitionManifest
	}
	return nil
}

// analysisJSONOmitted reports whether encoding/json omits an omitempty field.
func analysisJSONOmitted(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

// All metadata owners use a total escaped-JSON preflight before cloning/encoding.
// The bound is conservative (it charges every written field its worst-case
// escaping) and cannot turn a product of individually bounded labels into an
// unbounded allocation.
func MarshalBoundedAnalysisJSON(v any, limit int) ([]byte, error) {
	if limit < 1 || limit > 8<<20 {
		return nil, ErrPartitionManifest
	}
	remaining := int64(limit)
	if err := analysisJSONCharge(reflect.ValueOf(v), 0, &remaining); err != nil {
		return nil, err
	}
	body, err := json.Marshal(v)
	if err != nil || len(body) > limit {
		return nil, ErrPartitionManifest
	}
	return body, nil
}
func analysisJSONCharge(v reflect.Value, depth int, left *int64) error {
	charge := func(n int64) error {
		if n < 0 || n > *left {
			return ErrPartitionManifest
		}
		*left -= n
		return nil
	}
	if !v.IsValid() {
		return charge(4)
	}
	if depth > 64 {
		return ErrPartitionManifest
	}
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return charge(4)
		}
		v = v.Elem()
		depth++
		if depth > 64 {
			return ErrPartitionManifest
		}
	}
	if v.Type() == reflect.TypeOf(time.Time{}) {
		return charge(40)
	}
	text := func(s string) error {
		if charge(2) != nil {
			return ErrPartitionManifest
		}
		for _, r := range s {
			n := int64(1)
			switch {
			case r < 32 || r == '<' || r == '>' || r == '&' || r == 0x2028 || r == 0x2029:
				n = 6
			case r == '"' || r == '\\':
				n = 2
			case r >= 0x10000:
				n = 4
			case r >= 0x800:
				n = 3
			case r >= 0x80:
				n = 2
			}
			if charge(n) != nil {
				return ErrPartitionManifest
			}
		}
		return nil
	}
	switch v.Kind() {
	case reflect.Struct:
		if charge(2) != nil {
			return ErrPartitionManifest
		}
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.PkgPath != "" {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			name := tag[0]
			if name == "-" {
				continue
			}
			// encoding/json writes nothing for an empty omitempty field, so
			// it costs nothing: an optional field added later does not push
			// a stored manifest that fit before over its limit.
			if slices.Contains(tag[1:], "omitempty") && analysisJSONOmitted(v.Field(i)) {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if text(name) != nil || charge(2) != nil || analysisJSONCharge(v.Field(i), depth+1, left) != nil {
				return ErrPartitionManifest
			}
		}
	case reflect.Map:
		if v.IsNil() {
			return charge(4)
		}
		if v.Type().Key().Kind() != reflect.String || charge(2+int64(v.Len())*2) != nil {
			return ErrPartitionManifest
		}
		it := v.MapRange()
		for it.Next() {
			if text(it.Key().String()) != nil || analysisJSONCharge(it.Value(), depth+1, left) != nil {
				return ErrPartitionManifest
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return charge(4)
		}
		if charge(2+int64(v.Len())) != nil {
			return ErrPartitionManifest
		}
		for i := 0; i < v.Len(); i++ {
			if analysisJSONCharge(v.Index(i), depth+1, left) != nil {
				return ErrPartitionManifest
			}
		}
	case reflect.String:
		return text(v.String())
	case reflect.Bool:
		return charge(5)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return charge(int64(len(strconv.FormatInt(v.Int(), 10))))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return charge(int64(len(strconv.FormatUint(v.Uint(), 10))))
	case reflect.Float32, reflect.Float64:
		return charge(32)
	default:
		return ErrPartitionManifest
	}
	return nil
}
