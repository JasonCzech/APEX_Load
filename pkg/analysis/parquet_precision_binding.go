package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// ParquetPrecisionInput pairs a schema read from an immutable file with that
// file's verified content digest. Callers must verify the file, not accept a
// client-supplied schema or digest as proof. Content duplicates are refused.
type ParquetPrecisionInput struct {
	SHA256 string
	Schema ParquetSchemaPreview
}
type ParquetPrecisionRisk struct {
	FileSHA256 string `json:"file_sha256"`
	ParquetDecimalRisk
}

// ParquetPrecisionReview describes schema-level potential loss only. It has no
// affected-row count. SHA256 binds the mapping, complete source set and review.
type ParquetPrecisionReview struct {
	Version         int                    `json:"version"`
	Conversion      string                 `json:"conversion"`
	MappingSHA256   string                 `json:"mapping_sha256"`
	SourceSetSHA256 string                 `json:"source_set_sha256"`
	SourceCount     int                    `json:"source_count"`
	Risks           []ParquetPrecisionRisk `json:"risks"`
	SHA256          string                 `json:"sha256"`
}

const MaxParquetPrecisionReviewBytes = 64 << 10

func precisionSHA(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func precisionDigestValid(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ParquetPrecisionReviewer validates one immutable source at a time. It retains
// only bounded risks and source digests, never the full source schemas. It is
// single-owner and must be constructed with NewParquetPrecisionReviewer.
type ParquetPrecisionReviewer struct {
	mapping     ParquetMapping
	mappingSHA  string
	sourceCount int
	hashes      []string
	seen        map[string]bool
	risks       []ParquetPrecisionRisk
	failure     error
}

func NewParquetPrecisionReviewer(mapping ParquetMapping, sourceCount int) (*ParquetPrecisionReviewer, error) {
	if sourceCount < 1 || sourceCount > 1024 || len(mapping.Measures) < 1 || len(mapping.Measures) > 64 || sourceCount*len(mapping.Measures) > 1024 {
		return nil, errors.New("precision review exceeds source or mapping bounds")
	}
	if err := mapping.ValidateDefinition(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(mapping)
	if err != nil {
		return nil, err
	}
	if len(raw) > 128<<10 {
		return nil, errors.New("precision review mapping exceeds byte bound")
	}
	// Own all nested mapping paths so later caller edits cannot change validation
	// while retaining the original mapping digest.
	var owned ParquetMapping
	if err := json.Unmarshal(raw, &owned); err != nil {
		return nil, err
	}
	return &ParquetPrecisionReviewer{mapping: owned, mappingSHA: precisionSHA(raw), sourceCount: sourceCount, hashes: make([]string, 0, sourceCount), seen: map[string]bool{}, risks: []ParquetPrecisionRisk{}}, nil
}

// AddSource refuses duplicate content and permanently invalidates the review on
// any source error. A caller cannot ignore an error and publish a valid prefix.
func (r *ParquetPrecisionReviewer) AddSource(input ParquetPrecisionInput) (err error) {
	if r == nil || r.seen == nil {
		return errors.New("precision reviewer is not initialized")
	}
	if r.failure != nil {
		return r.failure
	}
	fileIndex := len(r.hashes)
	defer func() {
		if err != nil {
			err = fmt.Errorf("file %d: %w", fileIndex, err)
			r.failure = err
		}
	}()
	if fileIndex >= r.sourceCount {
		return errors.New("precision review exceeds declared source count")
	}
	if !precisionDigestValid(input.SHA256) || r.seen[input.SHA256] {
		return errors.New("invalid or duplicate precision review source")
	}
	if err := r.mapping.Validate(input.Schema); err != nil {
		return err
	}
	risks, err := ParquetDecimalRisks(r.mapping, input.Schema)
	if err != nil {
		return err
	}
	if len(risks) > maxParquetDecimalRisks-len(r.risks) {
		return errors.New("precision review exceeds affected column bound")
	}
	for _, risk := range risks {
		r.risks = append(r.risks, ParquetPrecisionRisk{FileSHA256: input.SHA256, ParquetDecimalRisk: risk})
	}
	r.seen[input.SHA256] = true
	r.hashes = append(r.hashes, input.SHA256)
	return nil
}

// Review returns a complete owned disclosure, deterministic regardless of source
// order. Schema order is normalized while authored mapping order is retained.
func (r *ParquetPrecisionReviewer) Review() (*ParquetPrecisionReview, error) {
	if r == nil || r.seen == nil {
		return nil, errors.New("precision reviewer is not initialized")
	}
	if r.failure != nil {
		return nil, r.failure
	}
	if len(r.hashes) != r.sourceCount {
		return nil, errors.New("precision review requires every declared source")
	}
	review := &ParquetPrecisionReview{Version: 1, Conversion: "float64", MappingSHA256: r.mappingSHA, SourceCount: r.sourceCount, Risks: make([]ParquetPrecisionRisk, len(r.risks))}
	for i, risk := range r.risks {
		review.Risks[i] = risk
		review.Risks[i].Column = append(ParquetColumnPath(nil), risk.Column...)
	}
	hashes := append([]string(nil), r.hashes...)
	sort.Strings(hashes)
	raw, _ := json.Marshal(hashes)
	review.SourceSetSHA256 = precisionSHA(raw)
	sort.Slice(review.Risks, func(i, j int) bool {
		a, b := review.Risks[i], review.Risks[j]
		if a.FileSHA256 != b.FileSHA256 {
			return a.FileSHA256 < b.FileSHA256
		}
		x, _ := json.Marshal(a.Column)
		y, _ := json.Marshal(b.Column)
		return string(x) < string(y)
	})
	raw, err := json.Marshal(review)
	if err != nil {
		return nil, err
	}
	// Reserve exactly the SHA-256 value's bytes for the final wire projection.
	if len(raw)+64 > MaxParquetPrecisionReviewBytes {
		return nil, errors.New("precision review exceeds encoded byte bound")
	}
	review.SHA256 = precisionSHA(raw)
	return review, nil
}

// ReviewParquetPrecision is the batch convenience API. Streaming callers should
// use NewParquetPrecisionReviewer to avoid retaining all schemas simultaneously.
func ReviewParquetPrecision(mapping ParquetMapping, inputs []ParquetPrecisionInput) (*ParquetPrecisionReview, error) {
	r, err := NewParquetPrecisionReviewer(mapping, len(inputs))
	if err != nil {
		return nil, err
	}
	for _, input := range inputs {
		if err := r.AddSource(input); err != nil {
			return nil, err
		}
	}
	return r.Review()
}

// AdmitParquetPrecision re-derives the review from immutable verified inputs.
// The acknowledgement is not an independently trusted object: it must match
// this exact mapping/source review. Legacy normalized artifacts remain readable;
// a new lossy normalization must explicitly opt into the versioned contract.
func AdmitParquetPrecision(mapping ParquetMapping, inputs []ParquetPrecisionInput, acknowledgement string) (*ParquetPrecisionReview, error) {
	r, err := NewParquetPrecisionReviewer(mapping, len(inputs))
	if err != nil {
		return nil, err
	}
	for _, input := range inputs {
		if err := r.AddSource(input); err != nil {
			return nil, err
		}
	}
	return r.Admit(acknowledgement)
}

// Admit checks consent against the complete review derived by this reviewer;
// callers cannot substitute a fabricated review for verified source schemas.
func (r *ParquetPrecisionReviewer) Admit(acknowledgement string) (*ParquetPrecisionReview, error) {
	review, err := r.Review()
	if err != nil {
		return nil, err
	}
	if acknowledgement != "" && (!precisionDigestValid(acknowledgement) || acknowledgement != review.SHA256) {
		return nil, errors.New("precision acknowledgement does not match current inputs and mapping")
	}
	if len(review.Risks) == 0 {
		return review, nil
	}
	if r.mapping.Version != 3 {
		return nil, errors.New("potential DECIMAL rounding requires mapping version 3 precision consent")
	}
	if r.mapping.PrecisionPolicy != PrecisionAllowFloat64Rounding {
		return nil, errors.New("precision policy refuses potential DECIMAL rounding")
	}
	if acknowledgement == "" {
		return nil, errors.New("potential DECIMAL rounding requires acknowledgement of the current precision review")
	}
	return review, nil
}

// ParquetPrecisionAcknowledgement binds a review to one editable import revision.
// It is a deterministic integrity binding, not an authorization token. The worker
// independently verifies the current immutable source set before normalization.
func ParquetPrecisionAcknowledgement(projectID, importID string, revision int64, mapping ParquetMapping, reviewSHA string) (string, error) {
	for _, id := range []string{projectID, importID} {
		if len(id) > 128 || strings.TrimSpace(id) == "" || !utf8.ValidString(id) || strings.ContainsRune(id, 0) {
			return "", errors.New("invalid precision review identity")
		}
	}
	if revision < 1 || revision >= 1<<62 || !precisionDigestValid(reviewSHA) {
		return "", errors.New("invalid precision review identity")
	}
	if err := mapping.ValidateDefinition(); err != nil {
		return "", err
	}
	body, err := MarshalBoundedAnalysisJSON(struct {
		Version             int
		ProjectID, ImportID string
		Revision            int64
		Mapping             ParquetMapping
		ReviewSHA256        string
	}{1, projectID, importID, revision, mapping, reviewSHA}, 128<<10)
	if err != nil {
		return "", err
	}
	return precisionSHA(body), nil
}
