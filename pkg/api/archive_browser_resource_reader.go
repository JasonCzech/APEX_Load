package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const maxBrowserResourceReadRecordBytes = 8 << 20

// Historical and current transaction writers preserve their source time zone.
// The read projection normalizes the instant to UTC without rewriting source
// bytes or relaxing the strict UTC page/native-evidence wire boundaries.
var browserResourceSourceTimestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)

var errBrowserResourceReadLimit = errors.New("browser resource reader exceeds resource limits")
var errBrowserResourceReadMalformed = errors.New("browser resource source is malformed")
var errBrowserResourceReadChanged = errors.New("browser resource source changed")

// The key always has an archive ordinal tie breaker. Missing remains last in
// either direction; available zero never shares its position with missing.
type browserResourceSortKey struct {
	Available          bool    `json:"available"`
	Value              float64 `json:"value"`
	TransactionOrdinal int64   `json:"transaction_ordinal"`
	ResourceOrdinal    int64   `json:"resource_ordinal"`
}

func browserResourceItemSortKey(item BrowserResourcePageItem, q BrowserResourcePageQuery) browserResourceSortKey {
	k := browserResourceSortKey{TransactionOrdinal: item.Locator.TransactionOrdinal, ResourceOrdinal: item.Locator.ResourceOrdinal}
	switch q.SortBy {
	case "capture_order":
		k.Available = true
	case "duration_ms":
		if item.DurationMs != nil {
			k.Available = true
			k.Value = *item.DurationMs
		}
	case "native_start_ms":
		if e := item.BrowserEvidence; e != nil && e.EpochStartMS != nil {
			k.Available = true
			k.Value = *e.EpochStartMS
		}
	case "response_encoded_body":
		if e := item.BrowserEvidence; e != nil && e.Bytes.ResponseEncodedBody != nil {
			k.Available = true
			k.Value = float64(*e.Bytes.ResponseEncodedBody)
		}
	}
	return k
}
func browserResourceKeyBefore(a, b browserResourceSortKey, q BrowserResourcePageQuery) bool {
	if a.Available != b.Available {
		return a.Available
	}
	if q.SortBy != "capture_order" && a.Available && a.Value != b.Value {
		if q.SortDir == "desc" {
			return a.Value > b.Value
		}
		return a.Value < b.Value
	}
	if a.TransactionOrdinal != b.TransactionOrdinal {
		if q.SortBy == "capture_order" && q.SortDir == "desc" {
			return a.TransactionOrdinal > b.TransactionOrdinal
		}
		return a.TransactionOrdinal < b.TransactionOrdinal
	}
	if q.SortBy == "capture_order" && q.SortDir == "desc" {
		return a.ResourceOrdinal > b.ResourceOrdinal
	}
	return a.ResourceOrdinal < b.ResourceOrdinal
}

type browserResourceScanResult struct {
	Items              []BrowserResourcePageItem
	Last               browserResourceSortKey
	HasNext            bool
	Transactions       int64
	Retained           int64
	Matched            int64
	SourcePresent      bool
	TimestampPrecision string
	SHA256             string
	SizeBytes          int64
}

func (s *browserResourceScanResult) observe(item BrowserResourcePageItem, q BrowserResourcePageQuery, after *browserResourceSortKey) error {
	s.Retained++
	if s.Retained > maxBrowserResourceReadEntries {
		return errBrowserResourceReadLimit
	}
	if s.TimestampPrecision == "" {
		s.TimestampPrecision = item.TimestampPrecision
	} else if s.TimestampPrecision != item.TimestampPrecision {
		s.TimestampPrecision = "mixed"
	}
	if !browserResourceMatches(item, q) {
		return nil
	}
	s.Matched++
	key := browserResourceItemSortKey(item, q)
	if after != nil && !browserResourceKeyBefore(*after, key, q) {
		return nil
	}
	index := sort.Search(len(s.Items), func(i int) bool { return browserResourceKeyBefore(key, browserResourceItemSortKey(s.Items[i], q), q) })
	keep := q.Limit + 1
	if index >= keep {
		return nil
	}
	s.Items = append(s.Items, BrowserResourcePageItem{})
	copy(s.Items[index+1:], s.Items[index:])
	s.Items[index] = item
	if len(s.Items) > keep {
		s.Items = s.Items[:keep]
	}
	return nil
}
func (s *browserResourceScanResult) finish(q BrowserResourcePageQuery) {
	if len(s.Items) > q.Limit {
		s.HasNext = true
		s.Items = s.Items[:q.Limit]
	}
	if len(s.Items) > 0 {
		s.Last = browserResourceItemSortKey(s.Items[len(s.Items)-1], q)
	}
	if s.TimestampPrecision == "" {
		s.TimestampPrecision = "unavailable"
	}
}

type browserResourceCountingReader struct {
	r   io.Reader
	ctx context.Context
	n   int64
}

func (r *browserResourceCountingReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.n > maxArchiveQueryBytes {
		return 0, errBrowserResourceReadLimit
	}
	remain := maxArchiveQueryBytes + 1 - r.n
	if int64(len(p)) > remain {
		p = p[:remain]
	}
	n, err := r.r.Read(p)
	r.n += int64(n)
	if r.n > maxArchiveQueryBytes {
		return n, errBrowserResourceReadLimit
	}
	return n, err
}

func scanBrowserResourceJSONL(ctx context.Context, source io.Reader, testID string, q BrowserResourcePageQuery, after *browserResourceSortKey) (browserResourceScanResult, error) {
	var result browserResourceScanResult
	if ctx == nil || source == nil || q.Limit < 1 || q.Limit > MaxBrowserResourcePageItems {
		return result, errBrowserResourceReadMalformed
	}
	hash := sha256.New()
	counter := &browserResourceCountingReader{r: source, ctx: ctx}
	scanner := bufio.NewScanner(io.TeeReader(counter, hash))
	scanner.Buffer(make([]byte, 0, 64<<10), maxBrowserResourceReadRecordBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return browserResourceScanResult{}, err
		}
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		result.Transactions++
		if result.Transactions > maxArchiveQueryRecords {
			return browserResourceScanResult{}, errBrowserResourceReadLimit
		}
		items, present, err := browserResourceTransactionRows(scanner.Bytes(), result.Transactions, testID)
		if err != nil {
			return browserResourceScanResult{}, err
		}
		result.SourcePresent = result.SourcePresent || present
		for _, item := range items {
			if err := ctx.Err(); err != nil {
				return browserResourceScanResult{}, err
			}
			if err := result.observe(item, q, after); err != nil {
				return browserResourceScanResult{}, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return browserResourceScanResult{}, err
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, errBrowserResourceReadLimit) || errors.Is(err, bufio.ErrTooLong) {
			return browserResourceScanResult{}, errBrowserResourceReadLimit
		}
		return browserResourceScanResult{}, errBrowserResourceReadMalformed
	}
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	result.SizeBytes = counter.n
	result.finish(q)
	return result, nil
}

// Close on cancellation and join the callback before releasing the owner.
// Stopping a callback that has already started must not leave an owned closer
// or proxy goroutine running after the request returns.
func browserResourceCloseOnCancel(ctx context.Context, closer io.Closer) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = closer.Close(); close(done) })
	return func() {
		if !stop() {
			<-done
		}
	}
}

func browserResourceTransactionRows(raw []byte, ordinal int64, testID string) ([]BrowserResourcePageItem, bool, error) {
	fields, err := browserResourceReadObject(raw, []string{"test_id", "timestamp", "action_name", "group", "region", "page_url", "client_metrics", "network_entries", "snapshot"})
	if err != nil {
		return nil, false, err
	}
	var base BrowserResourcePageItem
	base.TimestampPrecision = "unavailable"
	if raw, ok := fields["test_id"]; ok {
		var id string
		if json.Unmarshal(raw, &id) != nil || id != "" && id != testID {
			return nil, false, errBrowserResourceReadMalformed
		}
	}
	for _, field := range []struct {
		name   string
		target *string
		limit  int
		url    bool
	}{{"action_name", &base.ActionName, 256, false}, {"group", &base.Group, 256, false}, {"region", &base.Region, 256, false}, {"page_url", &base.PageURL, 2048, true}} {
		if raw, ok := fields[field.name]; ok {
			if !browserResourceReadOptionalString(raw, field.target) {
				return nil, false, errBrowserResourceReadMalformed
			}
			*field.target = browserResourceSafeText(*field.target, field.limit, field.url)
		}
	}
	if raw, ok := fields["timestamp"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var text string
		if json.Unmarshal(raw, &text) != nil || !browserResourceSourceTimestamp.MatchString(text) {
			return nil, false, errBrowserResourceReadMalformed
		}
		at, err := time.Parse(time.RFC3339Nano, text)
		if err != nil || at.IsZero() {
			return nil, false, errBrowserResourceReadMalformed
		}
		at = at.UTC()
		if at.Year() < 0 || at.Year() > 9999 || at.IsZero() {
			return nil, false, errBrowserResourceReadMalformed
		}
		base.Timestamp = &at
		base.TimestampPrecision = "rfc3339_nanosecond"
	}
	if raw, ok := fields["client_metrics"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		client, err := browserResourceReadObject(raw, []string{"device_profile", "device_platform", "browser_engine"})
		if err != nil {
			return nil, false, err
		}
		for _, field := range []struct {
			name   string
			target *string
			limit  int
		}{{"device_profile", &base.DeviceProfile, 128}, {"device_platform", &base.DevicePlatform, 32}, {"browser_engine", &base.BrowserEngine, 32}} {
			if raw, ok := client[field.name]; ok {
				if !browserResourceReadOptionalString(raw, field.target) {
					return nil, false, errBrowserResourceReadMalformed
				}
				*field.target = browserResourceSafeText(*field.target, field.limit, false)
			}
		}
	}
	branch := "network_entries"
	entries, topPresent := fields["network_entries"]
	present := topPresent && !bytes.Equal(bytes.TrimSpace(entries), []byte("null"))
	parsed, err := browserResourceReadEntries(entries)
	if err != nil {
		return nil, false, err
	}
	if len(parsed) == 0 {
		if snapshot, ok := fields["snapshot"]; ok && !bytes.Equal(bytes.TrimSpace(snapshot), []byte("null")) {
			snap, err := browserResourceReadObject(snapshot, []string{"network_entries", "page_url"})
			if err != nil {
				return nil, false, err
			}
			if base.PageURL == "" {
				var value string
				if raw, ok := snap["page_url"]; ok {
					if !browserResourceReadOptionalString(raw, &value) {
						return nil, false, errBrowserResourceReadMalformed
					}
					base.PageURL = browserResourceSafeText(value, 2048, true)
				}
			}
			if fallback, ok := snap["network_entries"]; ok {
				present = present || !bytes.Equal(bytes.TrimSpace(fallback), []byte("null"))
				parsed, err = browserResourceReadEntries(fallback)
				if err != nil {
					return nil, false, err
				}
				branch = "snapshot_network_entries"
			}
		}
	}
	rows := make([]BrowserResourcePageItem, 0, len(parsed))
	for i, fields := range parsed {
		item := base
		item.Locator = BrowserResourcePageLocator{TransactionOrdinal: ordinal, ResourceOrdinal: int64(i + 1), Branch: branch}
		item.EvidenceState = "not_captured"
		for _, field := range []struct {
			name   string
			target *string
			limit  int
			url    bool
		}{{"url", &item.URL, 2048, true}, {"method", &item.Method, 32, false}, {"resource_type", &item.ResourceType, 64, false}, {"failure_kind", &item.FailureKind, 32, false}} {
			if raw, ok := fields[field.name]; ok {
				if !browserResourceReadOptionalString(raw, field.target) {
					return nil, false, errBrowserResourceReadMalformed
				}
				*field.target = browserResourceSafeText(*field.target, field.limit, field.url)
			}
		}
		item.FailureKind = sanitizeArchivedNetworkFailureKind(item.FailureKind)
		if raw, ok := fields["status_code"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var value int
			if json.Unmarshal(raw, &value) != nil {
				return nil, false, errBrowserResourceReadMalformed
			}
			item.StatusCode = &value
		}
		if raw, ok := fields["duration_ms"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var value float64
			if json.Unmarshal(raw, &value) != nil {
				return nil, false, errBrowserResourceReadMalformed
			}
			item.DurationMs = &value
		}
		if raw, ok := fields["bytes_received"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var value int64
			if json.Unmarshal(raw, &value) != nil {
				return nil, false, errBrowserResourceReadMalformed
			}
			item.BytesReceived = &value
		}
		if raw, ok := fields["browser_evidence"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			item.EvidenceState = "malformed"
			var version struct {
				Version int `json:"version"`
			}
			if json.Unmarshal(raw, &version) == nil && version.Version > 0 && !protocol.SupportedBrowserResourceEvidenceVersion(version.Version) {
				item.EvidenceState = "unsupported_version"
			} else if evidence, err := protocol.DecodeBrowserResourceEvidence(raw); err == nil {
				item.BrowserEvidence = evidence
				item.EvidenceState = "available"
			}
		}
		if !browserResourcePageItemValid(item) {
			return nil, false, errBrowserResourceReadMalformed
		}
		rows = append(rows, item)
	}
	return rows, present, nil
}

func browserResourceReadEntries(raw []byte) ([]map[string]json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('[') {
		return nil, errBrowserResourceReadMalformed
	}
	rows := make([]map[string]json.RawMessage, 0)
	for d.More() {
		if len(rows) >= maxBrowserResourceRecordEntries {
			return nil, errBrowserResourceReadLimit
		}
		var entry json.RawMessage
		if d.Decode(&entry) != nil {
			return nil, errBrowserResourceReadMalformed
		}
		fields, err := browserResourceReadObject(entry, []string{"url", "method", "status_code", "resource_type", "failure_kind", "duration_ms", "bytes_received", "browser_evidence"})
		if err != nil {
			return nil, err
		}
		rows = append(rows, fields)
	}
	if end, err := d.Token(); err != nil || end != json.Delim(']') {
		return nil, errBrowserResourceReadMalformed
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errBrowserResourceReadMalformed
	}
	return rows, nil
}
func browserResourceReadOptionalString(raw []byte, target *string) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	return json.Unmarshal(raw, target) == nil
}
func browserResourceSafeText(value string, limit int, isURL bool) string {
	if isURL {
		value = scrub.ValueURL(value, scrub.Policy{})
	} else {
		value = scrub.ValueLabel(value, scrub.Policy{})
	}
	value = strings.ToValidUTF8(value, "")
	value = strings.Map(func(r rune) rune {
		if r == 0 || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, value)
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

// Only selected fields are materialized. All unrelated bodies/headers/debug
// values are traversed with bounded token/depth/key work and then discarded.
func browserResourceReadObject(raw []byte, selected []string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxBrowserResourceReadRecordBytes || !utf8.Valid(raw) {
		return nil, errBrowserResourceReadMalformed
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, errBrowserResourceReadMalformed
	}
	allowed := map[string]bool{}
	for _, key := range selected {
		allowed[key] = true
	}
	seen := map[string]bool{}
	fields := map[string]json.RawMessage{}
	tokens := 1 << 17
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] || len(seen) >= 4096 || len(name) > 256 {
			return nil, errBrowserResourceReadMalformed
		}
		seen[name] = true
		for _, candidate := range selected {
			if name != candidate && strings.EqualFold(name, candidate) {
				return nil, errBrowserResourceReadMalformed
			}
		}
		if allowed[name] {
			var value json.RawMessage
			if d.Decode(&value) != nil {
				return nil, errBrowserResourceReadMalformed
			}
			fields[name] = value
		} else if !browserResourceSkipValue(d, 0, &tokens) {
			return nil, errBrowserResourceReadMalformed
		}
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return nil, errBrowserResourceReadMalformed
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errBrowserResourceReadMalformed
	}
	return fields, nil
}
func browserResourceSkipValue(d *json.Decoder, depth int, tokens *int) bool {
	if depth > 32 || *tokens <= 0 {
		return false
	}
	*tokens -= 1
	token, err := d.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] || len(name) > 256 || len(seen) >= 4096 {
				return false
			}
			seen[name] = true
			if !browserResourceSkipValue(d, depth+1, tokens) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for d.More() {
			if !browserResourceSkipValue(d, depth+1, tokens) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}

func browserResourceSortKeyValid(k browserResourceSortKey, q BrowserResourcePageQuery) bool {
	if k.TransactionOrdinal < 1 || k.TransactionOrdinal > maxArchiveQueryRecords || k.ResourceOrdinal < 1 || k.ResourceOrdinal > maxBrowserResourceRecordEntries || math.IsNaN(k.Value) || math.IsInf(k.Value, 0) || k.Value < 0 {
		return false
	}
	if !k.Available && k.Value != 0 {
		return false
	}
	switch q.SortBy {
	case "capture_order":
		return k.Available && k.Value == 0
	case "duration_ms":
		return k.Value <= 86400000
	case "native_start_ms":
		return k.Value <= 253402300799999
	case "response_encoded_body":
		return k.Value <= 1<<50 && math.Trunc(k.Value) == k.Value
	default:
		return false
	}
}
