package storage

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// SQL payloads are bounded before typed allocation. A present pruning proof is
// decoded without case folding, duplicate keys, unknown fields or implicit null
// scalars. Omitted legacy proof retains its original full-scan contract.
func (in *AnalysisImport) UnmarshalJSON(body []byte) error {
	raw, err := analysisPartitionMetadataRaw(body, 2<<20, true)
	if err != nil {
		return err
	}
	type plain AnalysisImport
	value := plain(*in) // Preserve separately read json:"-" owner/lease columns.
	value.PartitionManifestVersion, value.PartitionManifest = 0, nil
	// Shadow the typed manifest until its complete bounded canonical preflight.
	envelope := struct {
		*plain
		PartitionManifest json.RawMessage `json:"partition_manifest,omitempty"`
	}{plain: &value}
	if err = json.Unmarshal(body, &envelope); err != nil {
		return err
	}
	if value.PartitionManifestVersion < 0 || value.PartitionManifestVersion > analysis.PartitionManifestVersion {
		return ErrAnalysisAuthority
	}
	if raw != nil {
		if value.PartitionManifestVersion != analysis.PartitionManifestVersion {
			return ErrAnalysisAuthority
		}
		value.PartitionManifest, err = analysis.DecodeNormalizedPartitionManifest(raw)
		if err != nil || value.PartitionManifest.ProjectID != value.ProjectID || value.PartitionManifest.DatasetID != value.ID {
			return ErrAnalysisAuthority
		}
	}
	*in = AnalysisImport(value)
	return nil
}

func (a *AnalysisAttempt) UnmarshalJSON(body []byte) error {
	if _, err := analysisPartitionMetadataRaw(body, 4<<20, false); err != nil {
		return err
	}
	type plain AnalysisAttempt
	var value plain
	if err := json.Unmarshal(body, &value); err != nil {
		return err
	}
	if value.PartitionManifestVersion < 0 || value.PartitionManifestVersion > analysis.PartitionManifestVersion || value.Published != nil && value.Published.PartitionManifestVersion != value.PartitionManifestVersion {
		return ErrAnalysisAuthority
	}
	if err := validateAnalysisAttemptReceipt(AnalysisAttempt(value)); err != nil {
		return err
	}
	*a = AnalysisAttempt(value)
	return nil
}

func analysisPartitionMetadataRaw(body []byte, limit int, manifestAllowed bool) (json.RawMessage, error) {
	if len(body) > limit {
		return nil, ErrAnalysisAuthority
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrAnalysisAuthority
	}
	var manifest json.RawMessage
	seen := map[string]bool{}
	fields := 0
	for decoder.More() {
		fields++
		if fields > 65536 {
			return nil, ErrAnalysisAuthority
		}
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, ErrAnalysisAuthority
		}
		guarded := strings.EqualFold(key, "partition_manifest") || strings.EqualFold(key, "partition_manifest_version")
		if guarded && (seen[key] || key != "partition_manifest" && key != "partition_manifest_version" || key == "partition_manifest" && !manifestAllowed) {
			return nil, ErrAnalysisAuthority
		}
		if guarded {
			seen[key] = true
		}
		var raw json.RawMessage
		if err = decoder.Decode(&raw); err != nil {
			return nil, ErrAnalysisAuthority
		}
		if key == "partition_manifest" {
			manifest = raw
		}
		if key == "partition_manifest_version" && (bytes.Equal(raw, []byte("null")) || len(raw) != 1 || raw[0] < '0' || raw[0] > '1') {
			return nil, ErrAnalysisAuthority
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, ErrAnalysisAuthority
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, ErrAnalysisAuthority
	}
	return manifest, nil
}

// Receipts retain their original result even after later association/cancel or
// deletion revisions. An independently changed inline proof cannot claim the
// previous durable result digest merely by carrying that digest alongside it.
func validateAnalysisAttemptReceipt(a AnalysisAttempt) error {
	if a.PublicationDigest == "" {
		if a.Published != nil {
			return ErrAnalysisAuthority
		}
		return nil
	}
	if !analysisCanonicalHash(a.PublicationDigest) || a.Published == nil || a.Revision < 1 || a.Revision >= 1<<62 || a.Published.State != "ready" || a.Published.Revision != a.Revision+1 || a.Published.Owner != "" || !a.Published.LeaseExpiresAt.IsZero() {
		return ErrAnalysisAuthority
	}
	candidate := *a.Published
	candidate.State, candidate.Owner, candidate.Revision = "normalizing", a.ID, a.Revision
	if ValidateAnalysisPublication(candidate, a) != nil {
		return ErrAnalysisAuthority
	}
	digest, err := AnalysisAttemptResultDigest(*a.Published)
	if err != nil || digest != a.PublicationDigest {
		return ErrAnalysisAuthority
	}
	return nil
}
