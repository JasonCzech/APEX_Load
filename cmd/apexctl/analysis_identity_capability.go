package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/analysis"
)

var errAnalysisCLIIdentityUnavailable = errors.New("controller does not advertise the required imported identity version; request was not sent")

func analysisCLIIdentityCommand(action string) bool {
	switch action {
	case "map", "commit", "query", "compare", "save-mapping", "save-comparison":
		return true
	}
	return false
}

func analysisCLIScopeAdmission(id, project string) error {
	for _, value := range []string{id, project} {
		if len(value) > 128 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || value != "" && strings.TrimFunc(value, unicode.IsSpace) == "" {
			return errors.New("invalid analytical command scope")
		}
	}
	return nil
}

func analysisCLIContextLive(ctx context.Context) error {
	if ctx == nil {
		return errors.New("missing analytical request context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

// Admission reads a closed request projection; forwarding still uses the exact
// original bytes. JSON aliases/duplicate decoded names cannot hide a requested
// selector from this fence while an older Go decoder applies it differently.
func analysisCLIObject(data []byte, limit int, fields []string, population ...int) (map[string]json.RawMessage, error) {
	if len(data) == 0 || len(data) > limit || !utf8.Valid(data) || !analysisCLIUnicodeEscapes(data) {
		return nil, errors.New("invalid or excessive analytical request JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, errors.New("analytical request requires an object")
	}
	maximum := 64
	if len(population) != 0 {
		maximum = population[0]
	}
	out := make(map[string]json.RawMessage)
	for d.More() {
		if len(out) == maximum || fields != nil && len(out) == len(fields) {
			return nil, errors.New("analytical request exceeds field bound")
		}
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || len(key) > 256 || strings.ContainsRune(key, 0) {
			return nil, errors.New("invalid analytical request field")
		}
		if _, exists := out[key]; exists {
			return nil, errors.New("duplicate analytical request field")
		}
		if fields != nil {
			allowed := false
			for _, field := range fields {
				allowed = allowed || key == field
			}
			if !allowed {
				return nil, errors.New("unsupported analytical request field")
			}
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return nil, err
		}
		out[key] = raw
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("invalid analytical request object")
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("analytical request has trailing input")
	}
	return out, nil
}

func analysisCLIText(raw json.RawMessage, maximum int, required bool) (string, error) {
	var value string
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || len(raw) > 6*maximum+2 || json.Unmarshal(raw, &value) != nil || !utf8.ValidString(value) || len(value) > maximum || strings.ContainsRune(value, 0) || required && strings.TrimFunc(value, unicode.IsSpace) == "" {
		return "", errors.New("invalid analytical identity text")
	}
	return value, nil
}

func analysisCLIMappingVersion(raw []byte, authored bool) (uint32, error) {
	if authored {
		mapping, err := analysis.DecodeParquetMappingDefinition(raw)
		if err != nil {
			return 0, err
		}
		if mapping.Version >= 2 || mapping.Identity != nil {
			return uint32(max(2, mapping.Version)), nil
		}
		return 0, nil
	}
	return analysisCLIHistoricalMappingVersion(raw, true)
}

func analysisCLIHistoricalMappingVersion(raw []byte, evidenceProjection bool) (uint32, error) {
	// Historical reads keep their existing grammar; only their exact version
	// and explicit identity declaration grant compatibility requirements.
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, nil
	}
	object, err := analysisCLIObject(raw, 128<<10, nil)
	if err != nil {
		return 0, err
	}
	if evidenceProjection {
		if err := analysisCLIHistoricalProjectionDisclosure(object); err != nil {
			return 0, err
		}
	}
	for key := range object {
		if key != "version" && strings.EqualFold(key, "version") || key != "identity" && strings.EqualFold(key, "identity") {
			return 0, errors.New("ambiguous historical identity metadata")
		}
	}
	var version uint32
	if raw, ok := object["version"]; ok && (bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &version) != nil || version < 1) {
		return 0, errors.New("invalid historical mapping version")
	}
	if _, identity := object["identity"]; identity {
		return max(2, version), nil
	}
	if version >= 2 {
		return version, nil
	}
	return 0, nil
}

func analysisCLIRequestScalars(object map[string]json.RawMessage) error {
	for _, key := range []string{"project_id", "dataset_id", "run_id", "metric", "source_id", "host", "service", "transaction", "protocol", "group", "from", "to", "aggregation", "mode", "name", "mapping_id"} {
		if raw, exists := object[key]; exists {
			if _, err := analysisCLIText(raw, 256, false); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"revision", "mapping_revision", "identity_schema_version", "bucket_seconds", "max_lag_seconds"} {
		if raw, exists := object[key]; exists {
			var value uint64
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
				return errors.New("analytical field requires an unsigned integer")
			}
		}
	}
	if raw, exists := object["allow_rejected"]; exists {
		var value bool
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
			return errors.New("analytical rejection option requires a boolean")
		}
	}
	return nil
}

var analysisCLISeriesFields = []string{"project_id", "dataset_id", "metric", "identity_schema_version", "source_id", "host", "service", "dimensions", "from", "to", "bucket_seconds", "aggregation"}
var analysisCLIComparisonFields = []string{"left", "right", "from", "to", "bucket_seconds", "max_lag_seconds", "mode"}
var analysisCLISelectionFields = []string{"dataset_id", "run_id", "source_id", "transaction", "host", "service", "dimensions", "protocol", "group", "metric", "aggregation"}

func analysisCLISelectorsVersion(object map[string]json.RawMessage) (uint32, error) {
	var required uint32
	for _, key := range []string{"source_id", "host", "service"} {
		if raw, exists := object[key]; exists {
			if _, err := analysisCLIText(raw, 256, false); err != nil {
				return 0, err
			}
			required = 2
		}
	}
	if raw, exists := object["dimensions"]; exists {
		labels, err := analysisCLIObject(raw, 16<<10, nil, 16)
		if err != nil || len(labels) > 16 {
			return 0, errors.New("invalid imported dimension population")
		}
		for key, value := range labels {
			if len(key) == 0 || len(key) > 128 || strings.ContainsRune(key, 0) {
				return 0, errors.New("invalid imported dimension name")
			}
			if _, err := analysisCLIText(value, 128, false); err != nil {
				return 0, err
			}
		}
		required = 2 // Exact arbitrary label keys are data, never struct aliases.
	}
	if raw, exists := object["identity_schema_version"]; exists {
		var version uint32
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &version) != nil {
			return 0, errors.New("invalid imported identity version")
		}
		if version >= 2 {
			required = max(required, version)
		}
	}
	return required, nil
}

// Remapping replaces mutable metadata without reading its projected evidence.
// Admission still checks ownership and the current advertised mapping version,
// while query/commit/comparison also refuse unsafe historical projections.
func (c *ApiClient) analysisCLIDataset(ctx context.Context, id, project string, evidenceProjection bool) (string, uint32, error) {
	if err := analysisCLIContextLive(ctx); err != nil {
		return "", 0, err
	}
	if len(id) == 0 || len(id) > 128 || id == "." || id == ".." || strings.TrimFunc(id, unicode.IsSpace) == "" || !utf8.ValidString(id) || strings.ContainsRune(id, 0) {
		return "", 0, errors.New("invalid imported dataset identity")
	}
	data, err := c.workflowRequest(ctx, http.MethodGet, "/api/v1/analysis/imports/"+url.PathEscape(id), nil, "")
	if err != nil {
		return "", 0, err
	}
	if err := analysisCLIContextLive(ctx); err != nil {
		return "", 0, err
	}
	object, err := analysisCLIObject(data, 2<<20, nil)
	if err != nil {
		return "", 0, err
	}
	for key := range object {
		for _, field := range []string{"id", "project_id", "mapping"} {
			if key != field && strings.EqualFold(key, field) {
				return "", 0, errors.New("ambiguous imported dataset metadata")
			}
		}
	}
	actualID, err := analysisCLIText(object["id"], 128, true)
	if err != nil || actualID != id {
		return "", 0, errors.New("controller returned a different imported dataset")
	}
	actualProject, err := analysisCLIText(object["project_id"], 128, true)
	if err != nil || project != "" && actualProject != project {
		return "", 0, errors.New("imported dataset belongs to a different project")
	}
	version, err := analysisCLIHistoricalMappingVersion(object["mapping"], evidenceProjection)
	return actualProject, version, err
}

func (c *ApiClient) preflightAnalysisIdentity(ctx context.Context, action, project, id string, body []byte) error {
	switch action {
	case "map", "commit", "query", "compare", "save-mapping", "save-comparison":
	default:
		return nil
	}
	if err := analysisCLIContextLive(ctx); err != nil {
		return err
	}
	if err := analysisCLIScopeAdmission(id, project); err != nil {
		return err
	}
	if action == "commit" && len(body) != 0 {
		return errors.New("analysis commit takes no request body; configure import options with analysis map")
	}
	var required uint32
	if action == "map" || action == "query" || action == "commit" {
		ownedProject, version, err := c.analysisCLIDataset(ctx, id, project, action != "map")
		if err != nil {
			return err
		}
		project, required = ownedProject, version
	}
	switch action {
	case "map", "save-mapping":
		fields, field := []string{"revision", "mapping", "allow_rejected", "mapping_id", "mapping_revision"}, "mapping"
		if action == "save-mapping" {
			fields, field = []string{"name", "definition"}, "definition"
		}
		object, err := analysisCLIObject(body, 128<<10, fields)
		if err != nil {
			return err
		}
		if err := analysisCLIRequestScalars(object); err != nil {
			return err
		}
		version, err := analysisCLIMappingVersion(object[field], true)
		if err != nil {
			return err
		}
		required = max(required, version)
	case "query":
		object, err := analysisCLIObject(body, 16<<10, analysisCLISeriesFields)
		if err != nil {
			return err
		}
		if err := analysisCLIRequestScalars(object); err != nil {
			return err
		}
		for key, expected := range map[string]string{"project_id": project, "dataset_id": id} {
			if raw, exists := object[key]; exists {
				value, err := analysisCLIText(raw, 128, true)
				if err != nil || value != expected {
					return errors.New("analytical query requests a different dataset or project")
				}
			}
		}
		version, err := analysisCLISelectorsVersion(object)
		if err != nil {
			return err
		}
		required = max(required, version)
	case "compare", "save-comparison":
		definition := body
		if action == "save-comparison" {
			object, err := analysisCLIObject(body, 128<<10, []string{"name", "definition"})
			if err != nil {
				return err
			}
			if err := analysisCLIRequestScalars(object); err != nil {
				return err
			}
			definition = object["definition"]
		}
		object, err := analysisCLIObject(definition, 128<<10, analysisCLIComparisonFields)
		if err != nil {
			return err
		}
		if err := analysisCLIRequestScalars(object); err != nil {
			return err
		}
		for _, side := range []string{"left", "right"} {
			selection, err := analysisCLIObject(object[side], 16<<10, analysisCLISelectionFields)
			if err != nil {
				return err
			}
			if err := analysisCLIRequestScalars(selection); err != nil {
				return err
			}
			_, dataset := selection["dataset_id"]
			_, run := selection["run_id"]
			if dataset == run {
				return errors.New("comparison requires exactly one dataset or managed run per side")
			}
			if run {
				if _, err := analysisCLIText(selection["run_id"], 128, true); err != nil {
					return err
				}
				if _, err := analysisCLISelectorsVersion(selection); err != nil {
					return err
				}
				continue // Managed-run monitor selectors retain their existing contract.
			}
			selectedID, err := analysisCLIText(selection["dataset_id"], 128, true)
			if err != nil {
				return err
			}
			_, version, err := c.analysisCLIDataset(ctx, selectedID, project, true)
			if err != nil {
				return err
			}
			required = max(required, version)
			version, err = analysisCLISelectorsVersion(selection)
			if err != nil {
				return err
			}
			required = max(required, version)
		}
	}
	if required != 0 {
		if err := analysisCLIContextLive(ctx); err != nil {
			return err
		}
		data, err := c.workflowRequest(ctx, http.MethodGet, "/api/v1/analysis/capabilities?project_id="+url.QueryEscape(project), nil, "")
		if err != nil {
			return err
		}
		if err := analysisCLIContextLive(ctx); err != nil {
			return err
		}
		object, err := analysisCLIObject(data, 2<<20, nil)
		if err != nil {
			return err
		}
		for key := range object {
			if key != "imported_identity_version" && strings.EqualFold(key, "imported_identity_version") {
				return errAnalysisCLIIdentityUnavailable
			}
		}
		var advertised uint32
		if raw, exists := object["imported_identity_version"]; !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &advertised) != nil || advertised < required {
			return errAnalysisCLIIdentityUnavailable
		}
	}
	return analysisCLIContextLive(ctx)
}

// JSON decoding replaces isolated surrogate escapes. Refuse them before they
// can change an exact requested label or authority in the compatibility fence.
func analysisCLIUnicodeEscapes(data []byte) bool {
	hex := func(c byte) int {
		switch {
		case c >= '0' && c <= '9':
			return int(c - '0')
		case c >= 'a' && c <= 'f':
			return int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			return int(c-'A') + 10
		}
		return -1
	}
	decode := func(at int) (int, bool) {
		if at+6 > len(data) || data[at] != '\\' || data[at+1] != 'u' {
			return 0, false
		}
		value := 0
		for _, c := range data[at+2 : at+6] {
			digit := hex(c)
			if digit < 0 {
				return 0, false
			}
			value = value*16 + digit
		}
		return value, true
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		if i+1 >= len(data) {
			return false
		}
		if data[i+1] != 'u' {
			i++
			continue
		}
		value, ok := decode(i)
		if !ok || value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			low, ok := decode(i + 6)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
		i += 5
	}
	return true
}
