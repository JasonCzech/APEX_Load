package duckdb

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/analysis"
)

const (
	workerProtocolVersion    = 3
	workerMaxFrameBytes      = 8 << 20
	workerMaxInputs          = 1024
	workerMaxOutputs         = 2048
	workerMaxScalarTextBytes = 16*256*2 + 15 // escaped display path, maximum v1 nesting
)

var errWorkerProtocol = errors.New("native worker protocol invalid or excessive")

type workerIdentity struct {
	Version        uint32 `json:"version"`
	BuildID        string `json:"build_id"`
	AttemptID      string `json:"attempt_id"`
	Nonce          string `json:"nonce"`
	ManifestDigest string `json:"manifest_digest"`
}
type workerLimits struct {
	Threads     int   `json:"threads"`
	MemoryBytes int64 `json:"memory_bytes"`
	SpillBytes  int64 `json:"spill_bytes"`
	InputBytes  int64 `json:"input_bytes"`
	OutputBytes int64 `json:"output_bytes"`
	RuntimeMS   int64 `json:"runtime_ms"`
}

// RelativeName is bootstrap-only. The parent generates private flat names and
// validates immutable descriptors under its inherited private attempt root.
type workerHandle struct {
	Handle       string `json:"handle"`
	Role         string `json:"role"` // input, normalized, rejection
	RelativeName string `json:"relative_name"`
	Bytes        int64  `json:"bytes"`            // exact input size; output quota
	SHA256       string `json:"sha256,omitempty"` // input only
	Rows         *int64 `json:"rows,omitempty"`   // exact input rows when parent knows them
	InputHandle  string `json:"input_handle,omitempty"`
	Measure      int    `json:"measure,omitempty"`
}
type workerBootstrap struct {
	Identity workerIdentity `json:"identity"`
	Limits   workerLimits   `json:"limits"`
	Handles  []workerHandle `json:"handles"`
}
type workerPreviewOperation struct {
	InputHandle string `json:"input_handle"`
}
type workerNormalizeOperation struct {
	ProjectID     string   `json:"project_id"`
	DatasetID     string   `json:"dataset_id"`
	InputHandles  []string `json:"input_handles"`
	OutputHandles []string `json:"output_handles"`
	Mapping       Mapping  `json:"mapping"`
	AllowRejected bool     `json:"allow_rejected"`
}
type workerSeriesOperation struct {
	InputHandles []string    `json:"input_handles"`
	Query        SeriesQuery `json:"query"`
}
type workerRequest struct {
	Preview   *workerPreviewOperation   `json:"preview,omitempty"`
	Normalize *workerNormalizeOperation `json:"normalize,omitempty"`
	Series    *workerSeriesOperation    `json:"series,omitempty"`
}
type workerCell struct {
	Kind      string     `json:"kind"` // null, bool, number, redacted, non_finite, timestamp
	Boolean   *bool      `json:"boolean,omitempty"`
	Number    *string    `json:"number,omitempty"`
	Timestamp *time.Time `json:"timestamp,omitempty"`
}
type workerPreviewResult struct {
	Schema  analysis.ParquetSchemaPreview `json:"schema"`
	Samples [][]workerCell                `json:"samples"`
}
type workerOutput struct {
	Partition *analysis.NormalizedPartitionStats `json:"partition,omitempty"`
	Handle    string                             `json:"handle"`
	SHA256    string                             `json:"sha256"`
	Bytes     int64                              `json:"bytes"`
	Rows      int64                              `json:"rows"`
}
type workerNormalizeResult struct {
	Outputs    []workerOutput     `json:"outputs"`
	Rejections []RejectionSummary `json:"rejections"`
	Accepted   int64              `json:"accepted"`
	Rejected   int64              `json:"rejected"`
	InputRows  int64              `json:"input_rows"`
}
type workerSeriesBounds struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}
type workerResult struct {
	SeriesBounds   *workerSeriesBounds     `json:"series_bounds,omitempty"`
	Status         string                  `json:"status"`         // ok, cancelled, error
	Code           string                  `json:"code,omitempty"` // fixed reason, never native error text
	CancelSequence uint64                  `json:"cancel_sequence,omitempty"`
	Preview        *workerPreviewResult    `json:"preview,omitempty"`
	Normalize      *workerNormalizeResult  `json:"normalize,omitempty"`
	Points         *[]analysis.MetricPoint `json:"points,omitempty"`
}
type workerCancel struct {
	Sequence uint64 `json:"sequence"`
	Reason   string `json:"reason"` // cancelled, deadline, resource_limit
}
type workerCancelAck struct {
	Sequence uint64 `json:"sequence"`
}
type workerFrame struct {
	Identity  workerIdentity   `json:"identity"`
	Kind      string           `json:"kind"` // hello, request, cancel, cancel_ack, result
	Request   *workerRequest   `json:"request,omitempty"`
	Cancel    *workerCancel    `json:"cancel,omitempty"`
	CancelAck *workerCancelAck `json:"cancel_ack,omitempty"`
	Result    *workerResult    `json:"result,omitempty"`
}

func workerHex(value string, size int) bool {
	if len(value) != size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size
}
func workerText(value string, max int) bool {
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
func (i workerIdentity) validate() error {
	if i.Version != workerProtocolVersion || !workerHex(i.BuildID, 32) || !workerHex(i.AttemptID, 16) || !workerHex(i.Nonce, 32) || !workerHex(i.ManifestDigest, 32) {
		return errWorkerProtocol
	}
	return nil
}
func (l workerLimits) validate() error {
	if l.Threads < 1 || l.Threads > 2 || l.MemoryBytes < 1 || l.MemoryBytes > 2<<30 || l.SpillBytes < 1 || l.SpillBytes > 8<<30 || l.InputBytes < 12 || l.InputBytes > 20<<30 || l.OutputBytes < 12 || l.OutputBytes > 20<<30 || l.RuntimeMS < 1 || l.RuntimeMS > int64((15*time.Minute)/time.Millisecond) {
		return errWorkerProtocol
	}
	return nil
}
func workerAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, errWorkerProtocol
	}
	return a + b, nil
}
func (b workerBootstrap) manifestDigest() (string, error) {
	if preflightWorkerValue(reflect.ValueOf(b.Limits), "", 0, new(int)) != nil || preflightWorkerValue(reflect.ValueOf(b.Handles), "handles", 0, new(int)) != nil {
		return "", errWorkerProtocol
	}
	body, err := marshalWorkerJSON(struct {
		Limits  workerLimits   `json:"limits"`
		Handles []workerHandle `json:"handles"`
	}{b.Limits, b.Handles})
	if err != nil || len(body) > workerMaxFrameBytes {
		return "", errWorkerProtocol
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
func (b workerBootstrap) validate() error {
	if b.Identity.validate() != nil || b.Limits.validate() != nil || len(b.Handles) < 1 || len(b.Handles) > workerMaxInputs+workerMaxOutputs {
		return errWorkerProtocol
	}
	seen, names := map[string]bool{}, map[string]bool{}
	inputs := map[string]workerHandle{}
	inputBytes, outputBytes := int64(0), int64(0)
	outputs := 0
	for _, h := range b.Handles {
		if !workerHex(h.Handle, 16) || seen[h.Handle] || !workerText(h.RelativeName, 64) || strings.ContainsAny(h.RelativeName, "/\\:") || h.RelativeName == "." || h.RelativeName == ".." || names[h.RelativeName] || !workerPortableName(h.RelativeName) || h.Bytes < 12 {
			return errWorkerProtocol
		}
		for _, c := range h.RelativeName {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
				return errWorkerProtocol
			}
		}
		seen[h.Handle], names[h.RelativeName] = true, true
		if h.Role == "input" {
			if !workerHex(h.SHA256, 32) || h.InputHandle != "" || h.Measure != 0 || h.Rows != nil && *h.Rows < 0 {
				return errWorkerProtocol
			}
			inputs[h.Handle] = h
			var err error
			inputBytes, err = workerAdd(inputBytes, h.Bytes)
			if err != nil {
				return err
			}
		} else if h.Role == "normalized" || h.Role == "rejection" {
			if h.SHA256 != "" || h.Rows != nil || !workerHex(h.InputHandle, 16) || h.Measure < 0 || h.Measure >= 64 {
				return errWorkerProtocol
			}
			outputs++
			var err error
			outputBytes, err = workerAdd(outputBytes, h.Bytes)
			if err != nil {
				return err
			}
		} else {
			return errWorkerProtocol
		}
	}
	if len(inputs) < 1 || len(inputs) > workerMaxInputs || outputs > workerMaxOutputs || inputBytes > b.Limits.InputBytes || outputBytes > b.Limits.OutputBytes {
		return errWorkerProtocol
	}
	slots := map[string]bool{}
	for _, h := range b.Handles {
		if h.Role == "input" {
			continue
		}
		if _, ok := inputs[h.InputHandle]; !ok {
			return errWorkerProtocol
		}
		slot := h.InputHandle + "/" + strconv.Itoa(h.Measure) + "/" + h.Role
		if slots[slot] {
			return errWorkerProtocol
		}
		slots[slot] = true
	}
	digest, err := b.manifestDigest()
	if err != nil || digest != b.Identity.ManifestDigest {
		return errWorkerProtocol
	}
	return nil
}
func (b workerBootstrap) handle(id, role string) (workerHandle, bool) {
	for _, h := range b.Handles {
		if h.Handle == id && h.Role == role {
			return h, true
		}
	}
	return workerHandle{}, false
}
func (r workerRequest) validate(b workerBootstrap) error {
	if b.validate() != nil {
		return errWorkerProtocol
	}
	n := 0
	for _, present := range []bool{r.Preview != nil, r.Normalize != nil, r.Series != nil} {
		if present {
			n++
		}
	}
	if n != 1 {
		return errWorkerProtocol
	}
	checkInputs := func(ids []string) error {
		if len(ids) < 1 || len(ids) > workerMaxInputs {
			return errWorkerProtocol
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if _, ok := b.handle(id, "input"); !ok || seen[id] {
				return errWorkerProtocol
			}
			seen[id] = true
		}
		return nil
	}
	if r.Preview != nil {
		return checkInputs([]string{r.Preview.InputHandle})
	}
	if r.Series != nil {
		if checkInputs(r.Series.InputHandles) != nil || !workerText(r.Series.Query.ProjectID, 256) || !workerText(r.Series.Query.DatasetID, 256) || r.Series.Query.Validate() != nil {
			return errWorkerProtocol
		}
		return nil
	}
	op := r.Normalize
	if checkInputs(op.InputHandles) != nil || !workerText(op.ProjectID, 256) || !workerText(op.DatasetID, 256) || validateWorkerMapping(op.Mapping) != nil || len(op.InputHandles)*len(op.Mapping.Measures) > 1024 || len(op.OutputHandles) < 1 || len(op.OutputHandles) > workerMaxOutputs {
		return errWorkerProtocol
	}
	selected := map[string]bool{}
	for _, id := range op.InputHandles {
		selected[id] = true
	}
	seen, slots := map[string]bool{}, map[string]bool{}
	for _, id := range op.OutputHandles {
		var h workerHandle
		found := false
		for _, candidate := range b.Handles {
			if candidate.Handle == id && candidate.Role != "input" {
				h, found = candidate, true
				break
			}
		}
		if !found || seen[id] || !selected[h.InputHandle] || h.Measure >= len(op.Mapping.Measures) || h.Role == "rejection" && !op.AllowRejected {
			return errWorkerProtocol
		}
		seen[id] = true
		slots[h.InputHandle+"/"+strconv.Itoa(h.Measure)+"/"+h.Role] = true
	}
	for _, id := range op.InputHandles {
		for j := range op.Mapping.Measures {
			if !slots[id+"/"+strconv.Itoa(j)+"/normalized"] || op.AllowRejected && !slots[id+"/"+strconv.Itoa(j)+"/rejection"] {
				return errWorkerProtocol
			}
		}
	}
	return nil
}

// This checks only finite shape/semantics. Actual source schema compatibility
// is revalidated by child inspection; a synthetic schema is not qualification.
func validateWorkerMapping(m Mapping) error {
	if len(m.Measures) < 1 || len(m.Measures) > 64 {
		return errWorkerProtocol
	}
	columns := []analysis.ParquetColumn{}
	appendPath := func(path analysis.ParquetColumnPath) error {
		if len(path) < 1 || len(path) > 16 {
			return errWorkerProtocol
		}
		for _, part := range path {
			if !workerColumnName(part, 256) {
				return errWorkerProtocol
			}
		}
		columns = append(columns, analysis.ParquetColumn{PathComponents: append([]string(nil), path...)})
		return nil
	}
	if appendPath(m.Timestamp) != nil {
		return errWorkerProtocol
	}
	for _, measure := range m.Measures {
		if appendPath(measure.Value) != nil {
			return errWorkerProtocol
		}
		for _, path := range []Column{measure.NameColumn, measure.SampleCountColumn, measure.SampleSumColumn} {
			if len(path) > 0 && appendPath(path) != nil {
				return errWorkerProtocol
			}
		}
	}
	if m.Identity != nil {
		if len(m.Identity.Dimensions) > 16 {
			return errWorkerProtocol
		}
		for _, path := range []Column{m.Identity.Source, m.Identity.Host, m.Identity.Service} {
			if len(path) > 0 && appendPath(path) != nil {
				return errWorkerProtocol
			}
		}
		for _, d := range m.Identity.Dimensions {
			if appendPath(d.Value) != nil {
				return errWorkerProtocol
			}
		}
	}
	if !workerText(m.Timezone, 256) || m.Validate(analysis.ParquetSchemaPreview{Columns: columns}) != nil {
		return errWorkerProtocol
	}
	return nil
}
func (f workerFrame) validate() error {
	if f.Identity.validate() != nil {
		return errWorkerProtocol
	}
	n := 0
	for _, p := range []bool{f.Request != nil, f.Cancel != nil, f.CancelAck != nil, f.Result != nil} {
		if p {
			n++
		}
	}
	switch f.Kind {
	case "hello":
		if n != 0 {
			return errWorkerProtocol
		}
	case "request":
		if n != 1 || f.Request == nil {
			return errWorkerProtocol
		}
	case "cancel":
		if n != 1 || f.Cancel == nil || f.Cancel.Sequence == 0 || (f.Cancel.Reason != "cancelled" && f.Cancel.Reason != "deadline" && f.Cancel.Reason != "resource_limit") {
			return errWorkerProtocol
		}
	case "cancel_ack":
		if n != 1 || f.CancelAck == nil || f.CancelAck.Sequence == 0 {
			return errWorkerProtocol
		}
	case "result":
		if n != 1 || f.Result == nil {
			return errWorkerProtocol
		}
	default:
		return errWorkerProtocol
	}
	return nil
}
func (f workerFrame) clone() (workerFrame, error) {
	if f.validate() != nil || preflightWorkerValue(reflect.ValueOf(f), "", 0, new(int)) != nil {
		return workerFrame{}, errWorkerProtocol
	}
	body, err := marshalWorkerJSON(f)
	if err != nil || len(body) > workerMaxFrameBytes {
		return workerFrame{}, errWorkerProtocol
	}
	var cloned workerFrame
	if err := decodeWorkerJSON(body, &cloned); err != nil {
		return workerFrame{}, err
	}
	return cloned, nil
}

// One immutable handle names one physical file on all supported platforms.
func workerPortableName(name string) bool {
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	base := strings.Split(name, ".")[0]
	switch base {
	case "con", "prn", "aux", "nul":
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

func workerColumnName(value string, max int) bool {
	return value != "" && len(value) <= max && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}
