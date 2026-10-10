package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/keywrap"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Business keys (G16): a project admin approves a mapping from a script
// extraction (an order ID, a billing account number) to a log field of one
// of the project's monitor sources. Archives keep a project-keyed HMAC
// digest of each mapped value, and drop the raw value too for groups whose
// scripts redact run evidence (config.log.redact_run_evidence). A lookup
// joins operator-supplied values, which are never stored, to the archived
// requests by digest and to the source's logs by value.

const (
	businessKeyDigestPrefix = "bk1:"
	businessKeyDigestDomain = "apexload.business-key.v1\x00"
	// maxBusinessKeyMappings bounds the mappings a run's policy carries.
	maxBusinessKeyMappings = 64
)

// businessKeyMapping is a business_key_mapping definition.
type businessKeyMapping struct {
	// Extraction is the script extraction name whose values are the keys.
	Extraction string `json:"extraction"`
	// SourceID is the monitor source whose logs carry the key, and Field
	// the log field that holds it.
	SourceID string `json:"source_id"`
	Field    string `json:"field"`
	Label    string `json:"label,omitempty"`
}

var (
	businessKeyExtractionPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	businessKeyFieldPattern      = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,256}$`)
)

func validateBusinessKeyMappingDefinition(raw json.RawMessage) (businessKeyMapping, error) {
	var m businessKeyMapping
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return m, errors.New("invalid business key mapping")
	}
	switch {
	case !businessKeyExtractionPattern.MatchString(m.Extraction):
		return m, errors.New("extraction must be 1-128 letters, digits or _ . : -")
	case !businessKeyFieldPattern.MatchString(m.Field):
		return m, errors.New("field must be 1-256 letters, digits or _ . @ -")
	case strings.TrimSpace(m.SourceID) == "" || len(m.SourceID) > 128 || !plainText(m.SourceID, 128):
		return m, errors.New("source_id is required")
	case m.Label != "" && !plainText(m.Label, 128):
		return m, errors.New("label must be plain text of at most 128 characters")
	}
	return m, nil
}

// validBusinessKeyMapping checks a mapping to approve, including that its
// source is one of the project's elastic-query sources, or writes the error.
func (s *Server) validBusinessKeyMapping(w http.ResponseWriter, project string, raw json.RawMessage) bool {
	mapping, err := validateBusinessKeyMappingDefinition(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	sources, ok := s.store.(storage.RunMonitorStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "monitor sources unavailable")
		return false
	}
	list, err := sources.ListMonitorSources(project)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "monitor sources unavailable")
		return false
	}
	for _, source := range list {
		if source.ID == mapping.SourceID {
			if source.Pack != monitorprovider.PackElasticQuery {
				writeError(w, http.StatusBadRequest, "business keys are looked up in Elasticsearch or OpenSearch query sources only")
				return false
			}
			return true
		}
	}
	writeError(w, http.StatusBadRequest, "source_id is not a monitor source of this project")
	return false
}

// businessKeyMappingDefinition is an approved mapping with its identity.
type businessKeyMappingDefinition struct {
	ID       string
	Revision int64
	businessKeyMapping
}

// loadBusinessKeyMappings reads a project's approved, undeleted mappings.
func loadBusinessKeyMappings(store any, project string) ([]businessKeyMappingDefinition, error) {
	definitions, ok := store.(storage.AnalysisDefinitionStore)
	if !ok {
		return nil, errors.New("analysis definitions unavailable")
	}
	var out []businessKeyMappingDefinition
	for offset := 0; ; offset += 100 {
		page, err := definitions.ListAnalysisDefinitions(project, storage.AnalysisDefinitionKindBusinessKeyMapping, 100, offset)
		if err != nil {
			return nil, err
		}
		for _, d := range page {
			if d.Deleted {
				continue
			}
			m, err := validateBusinessKeyMappingDefinition(d.Definition)
			if err != nil {
				continue
			}
			out = append(out, businessKeyMappingDefinition{ID: d.ID, Revision: d.Revision, businessKeyMapping: m})
		}
		if len(page) < 100 {
			return out, nil
		}
	}
}

// businessKeyDigester computes a project's business-key digests.
type businessKeyDigester struct {
	key []byte
}

// digest is the joinable form of one extraction's value.
func (d *businessKeyDigester) digest(extraction, value string) string {
	mac := hmac.New(sha256.New, d.key)
	_, _ = io.WriteString(mac, businessKeyDigestDomain+extraction+"\x00"+value)
	return businessKeyDigestPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func businessKeyAAD(project string) []byte {
	return []byte("apexload/business-key/v1\x00" + project)
}

// businessKeyDigester returns the project's digester, creating the
// project's key on first use. It needs the controller's key wrapper.
func (tr *TestRunner) businessKeyDigester(ctx context.Context, project string) (*businessKeyDigester, error) {
	project = normalizeProjectID(project)
	if cached, ok := tr.businessKeys.Load(project); ok {
		return cached.(*businessKeyDigester), nil
	}
	wrapper := tr.continuityKeyWrapper
	store, ok := tr.testStore.(storage.BusinessKeyStore)
	if wrapper == nil || !ok {
		return nil, errors.New("business key digests need the controller's key wrapper")
	}
	aad := businessKeyAAD(project)
	var encoded string
	err := tr.trackDurableWrite(func() error {
		var err error
		encoded, err = store.BusinessKeyProjectKey(project, func() (string, error) {
			key := make([]byte, 32)
			if _, err := io.ReadFull(rand.Reader, key); err != nil {
				return "", err
			}
			wrapped, err := wrapper.WrapKey(ctx, key, aad)
			if err != nil {
				return "", err
			}
			body, err := json.Marshal(wrapped)
			return string(body), err
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("business key for project: %w", err)
	}
	var wrapped keywrap.WrappedKey
	if err := json.Unmarshal([]byte(encoded), &wrapped); err != nil {
		return nil, errors.New("stored business key is unreadable")
	}
	key, err := wrapper.UnwrapKey(ctx, wrapped, aad)
	if err != nil {
		return nil, fmt.Errorf("unwrap business key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("stored business key has the wrong length")
	}
	digester := &businessKeyDigester{key: key}
	actual, _ := tr.businessKeys.LoadOrStore(project, digester)
	return actual.(*businessKeyDigester), nil
}

// RunArchiveBusinessKeys is a run's business-key policy, frozen when its
// archive recording begins.
type RunArchiveBusinessKeys struct {
	// Extractions are the extraction names approved as business keys; their
	// values are archived with a digest.
	Extractions []string `json:"extractions"`
	// DigestOnlyGroups are the groups whose scripts redact run evidence:
	// their business-key values are archived as digests only.
	DigestOnlyGroups []string `json:"digest_only_groups,omitempty"`
}

// runArchiveBusinessKeys builds a run's policy from its project's approved
// mappings and its groups' effective scripts. A group whose script cannot be
// read is treated as redacting, so a value is never kept by mistake.
func (tr *TestRunner) runArchiveBusinessKeys(project string, groups []TestGroup, groupSpecs []engine.GroupConfig) *RunArchiveBusinessKeys {
	if tr == nil || !businessKeyProjectEnabled(tr.projectStore, project) {
		return nil
	}
	mappings, err := loadBusinessKeyMappings(tr.testStore, project)
	if err != nil || len(mappings) == 0 {
		return nil
	}
	names := map[string]bool{}
	for _, m := range mappings {
		names[m.Extraction] = true
	}
	policy := &RunArchiveBusinessKeys{Extractions: slices.Sorted(func(yield func(string) bool) {
		for name := range names {
			if !yield(name) {
				return
			}
		}
	})}
	if len(policy.Extractions) > maxBusinessKeyMappings {
		policy.Extractions = policy.Extractions[:maxBusinessKeyMappings]
	}
	specs := map[string]*engine.GroupConfig{}
	for i := range groupSpecs {
		specs[groupSpecs[i].Name] = &groupSpecs[i]
	}
	for _, group := range groups {
		spec := specs[group.Name]
		if spec == nil || spec.Script == nil || engine.ScriptRedactsRunEvidence(spec.Script) {
			policy.DigestOnlyGroups = append(policy.DigestOnlyGroups, group.Name)
		}
	}
	sort.Strings(policy.DigestOnlyGroups)
	return policy
}

// sanitizeBusinessKeyDigests keeps only well-formed digests of bounded
// extraction names, at most one per policy entry.
func sanitizeBusinessKeyDigests(digests map[string]string) map[string]string {
	if len(digests) == 0 {
		return nil
	}
	out := map[string]string{}
	for name, digest := range digests {
		if len(out) >= maxBusinessKeyMappings {
			break
		}
		if businessKeyExtractionPattern.MatchString(name) && strings.HasPrefix(digest, businessKeyDigestPrefix) && len(digest) <= 64 {
			out[name] = digest
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// businessKeyArchiver applies a run's policy to archive rows.
type businessKeyArchiver struct {
	policy   *RunArchiveBusinessKeys
	digester *businessKeyDigester
	// unavailable records that a value was dropped without a digest.
	unavailable bool
}

// newBusinessKeyArchiver returns the archiver for a run, or nil when the run
// has no policy. Without a digester, digest-only groups drop their values
// and other groups keep them raw.
func (tr *TestRunner) newBusinessKeyArchiver(ctx context.Context, project string, policy *RunArchiveBusinessKeys) *businessKeyArchiver {
	if policy == nil || len(policy.Extractions) == 0 {
		return nil
	}
	digester, _ := tr.businessKeyDigester(ctx, project)
	return &businessKeyArchiver{policy: policy, digester: digester}
}

// apply digests a row's business-key values from their raw extracted form,
// before the row is scrubbed, and drops them for digest-only groups. The
// row's extraction map is replaced, never edited, since it may be shared.
func (a *businessKeyArchiver) apply(event *TransactionEvent) {
	if a == nil || len(event.Extractions) == 0 {
		return
	}
	digestOnly := slices.Contains(a.policy.DigestOnlyGroups, event.Group)
	var extractions map[string]string
	for _, name := range a.policy.Extractions {
		value, ok := event.Extractions[name]
		if !ok || value == "" || value == scrub.Redacted {
			continue
		}
		if a.digester != nil {
			if event.ExtractionDigests == nil {
				event.ExtractionDigests = map[string]string{}
			}
			event.ExtractionDigests[name] = a.digester.digest(name, value)
		}
		if digestOnly {
			if extractions == nil {
				extractions = make(map[string]string, len(event.Extractions))
				for k, v := range event.Extractions {
					extractions[k] = v
				}
			}
			extractions[name] = scrub.Redacted
			a.unavailable = a.unavailable || a.digester == nil
		}
	}
	if extractions != nil {
		event.Extractions = extractions
	}
}
