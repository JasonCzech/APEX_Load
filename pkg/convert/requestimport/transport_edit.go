package requestimport

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// Transport edits are an author's explicit HTTP version choices for the
// requests of one origin, made in import review's Transport panel. Each edit
// names the request path, its origin and the policy the review showed, so a
// stale review is refused rather than applied to a different draft.

// TransportEditMetadataKey records the latest transport edit on a request.
const TransportEditMetadataKey = "http_version_transport_edit"

// An explicit version choice is the edit these gates ask for: the cURL
// --http3 race (accepting or replacing its approximate rewrite) and a draft
// HTTP/3 capture (modernizing it or choosing another version).
var (
	transportResolvedExecution = map[string]bool{"curl_http3_race_not_modeled": true}
	transportResolvedReview    = map[string]bool{ReasonHTTP3DraftToken: true}
)

// ErrStaleTransportEdit means the draft no longer matches the reviewed
// transport rows.
var ErrStaleTransportEdit = errors.New("the import draft changed since the transport review; refresh it and preview the edit again")

// TransportEdit is one request's reviewed change. From and To are leaf
// policies; empty means the request inherits the script setting.
type TransportEdit struct {
	Path   string `json:"path"`
	Origin string `json:"origin"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// TransportChange reports an applied edit and the gates it resolved.
type TransportChange struct {
	Path     string   `json:"path"`
	Origin   string   `json:"origin"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Resolved []string `json:"resolved,omitempty"`
}

// RequestOrigin returns scheme://host[:port] for a request target resolved
// against the base URL, lowercased with the scheme's default port removed, or
// "" when the target's origin depends on a template or is not HTTP(S).
func RequestOrigin(baseURL, target string) string {
	trimmed := strings.ToLower(strings.TrimSpace(target))
	if strings.HasPrefix(trimmed, "{{") {
		// The whole URL is a runtime value.
		return ""
	}
	if scheme, _, found := strings.Cut(trimmed, "://"); found && !strings.ContainsAny(scheme, "/?#") && scheme != "http" && scheme != "https" {
		return ""
	}
	resolved := httpprotocol.ResolveURL(baseURL, target)
	parsed, err := url.Parse(resolved)
	if err != nil || parsed.Host == "" || strings.Contains(parsed.Host, "{{") {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.ToLower(parsed.Host)
	if port := parsed.Port(); (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		host = strings.ToLower(parsed.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return scheme + "://" + host
}

// SurfaceHTTPVersionPolicy is a request surface's leaf policy, or "" when it
// inherits.
func SurfaceHTTPVersionPolicy(ref dslwalk.SurfaceRef) string {
	if ref.Action != nil {
		if ref.Action.HTTPVersion.IsPresent() {
			return ref.Action.HTTPVersion.Value()
		}
		return ""
	}
	value, _ := ref.Request["http_version"].(string)
	return strings.TrimSpace(value)
}

func surfaceTarget(ref dslwalk.SurfaceRef) string {
	if ref.Action != nil {
		return ref.Action.Target
	}
	target, _ := ref.Request["target"].(string)
	return target
}

// ApplyTransportEdits applies reviewed edits to the script's HTTP request
// surfaces in place. It refuses the whole batch if any edit no longer matches
// the draft, names an unknown policy, or asks for exact HTTP/3 on a cleartext
// origin.
func ApplyTransportEdits(script *dsl.Script, edits []TransportEdit) ([]TransportChange, error) {
	if script == nil {
		return nil, errors.New("script is required")
	}
	byPath := make(map[string]TransportEdit, len(edits))
	for _, edit := range edits {
		if _, duplicate := byPath[edit.Path]; duplicate || strings.TrimSpace(edit.Path) == "" {
			return nil, fmt.Errorf("transport edits must name each request path once")
		}
		if edit.To != "" {
			if _, err := dsl.NewHTTPVersionOverride(edit.To); err != nil {
				return nil, fmt.Errorf("transport edit for %s: %w", edit.Path, err)
			}
			if edit.To == httpprotocol.VersionHTTP3 && !strings.HasPrefix(edit.Origin, "https://") {
				return nil, fmt.Errorf("transport edit for %s: HTTP/3 serves only https origins", edit.Path)
			}
		}
		byPath[edit.Path] = edit
	}
	refs := map[string]dslwalk.SurfaceRef{}
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if _, wanted := byPath[ref.Path]; wanted && runtimeSurfaceIsHTTPRequest(ref) {
			refs[ref.Path] = ref
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Verify the whole batch before changing anything.
	for path, edit := range byPath {
		ref, ok := refs[path]
		if !ok || RequestOrigin(script.Config.BaseURL, surfaceTarget(ref)) != edit.Origin || SurfaceHTTPVersionPolicy(ref) != edit.From {
			return nil, ErrStaleTransportEdit
		}
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	changes := make([]TransportChange, 0, len(paths))
	for _, path := range paths {
		edit, ref := byPath[path], refs[path]
		changes = append(changes, applyTransportEdit(ref, edit))
	}
	return changes, nil
}

func applyTransportEdit(ref dslwalk.SurfaceRef, edit TransportEdit) TransportChange {
	change := TransportChange{Path: edit.Path, Origin: edit.Origin, From: edit.From, To: edit.To}
	if ref.Action != nil {
		if edit.To == "" {
			ref.Action.HTTPVersion = dsl.OptionalHTTPVersion{}
		} else {
			ref.Action.HTTPVersion, _ = dsl.NewHTTPVersionOverride(edit.To)
		}
	} else if edit.To == "" {
		delete(ref.Request, "http_version")
	} else {
		ref.Request["http_version"] = edit.To
	}
	metadata := ref.Metadata()
	if metadata == nil {
		metadata = map[string]any{}
	}
	provenance := map[string]any{"version": 1, "origin": edit.Origin, "policy": policyLabel(edit.To), "previous_policy": policyLabel(edit.From)}
	// The declared source version follows the author's choice, so the
	// runtime-fidelity check compares like with like; the captured
	// observation stays in http_version_capture.
	if declared, ok := metadata["http_version"].(string); ok && declared != "" {
		provenance["previous_declared"] = declared
	}
	if edit.To == "" {
		delete(metadata, "http_version")
	} else {
		metadata["http_version"] = edit.To
		change.Resolved = clearTransportResolvedGates(metadata)
		if len(change.Resolved) > 0 {
			provenance["resolved"] = change.Resolved
		}
	}
	metadata[TransportEditMetadataKey] = provenance
	ref.SetMetadata(metadata)
	return change
}

func policyLabel(policy string) string {
	if policy == "" {
		return "inherit"
	}
	return policy
}

// clearTransportResolvedGates removes the gate codes an explicit version
// choice resolves, leaving every other gate intact.
func clearTransportResolvedGates(metadata map[string]any) []string {
	var resolved []string
	execution := filterCodes(dslwalk.MetadataStrings(metadata, "execution_blockers"), transportResolvedExecution, &resolved)
	if blocker, ok := metadata["execution_blocker"].(string); ok && strings.TrimSpace(blocker) != "" {
		if transportResolvedExecution[blocker] {
			resolved = appendUnique(resolved, blocker)
		} else if !containsString(execution, blocker) {
			execution = append(execution, blocker)
		}
	}
	if len(resolved) > 0 {
		if len(execution) == 0 {
			delete(metadata, "execution_blocker")
			delete(metadata, "execution_blockers")
		} else {
			sort.Strings(execution)
			metadata["execution_blocker"] = execution[0]
			metadata["execution_blockers"] = execution
		}
		if reasons := filterCodes(dslwalk.MetadataStrings(metadata, curlReviewReasonsKey), transportResolvedExecution, nil); len(reasons) == 0 {
			delete(metadata, curlReviewReasonsKey)
		} else {
			metadata[curlReviewReasonsKey] = reasons
		}
		if review, _ := metadata["import_review"].(map[string]any); review != nil {
			if reasons := filterCodes(dslwalk.MetadataStrings(review, "reason_codes"), transportResolvedExecution, nil); len(reasons) == 0 {
				delete(metadata, "import_review")
			} else {
				review["reason_codes"] = reasons
			}
		}
		// The proposal was accepted or replaced by the choice.
		delete(metadata, "http_version_rewrite_proposal")
	}
	var reviewResolved []string
	reviews := filterCodes(dslwalk.MetadataStrings(metadata, "review_reasons"), transportResolvedReview, &reviewResolved)
	gate, _ := metadata["review_gate"].(string)
	if transportResolvedReview[gate] {
		reviewResolved = appendUnique(reviewResolved, gate)
	}
	if len(reviewResolved) > 0 {
		if len(reviews) == 0 {
			delete(metadata, "review_reasons")
		} else {
			metadata["review_reasons"] = reviews
		}
		if transportResolvedReview[gate] {
			if len(reviews) > 0 {
				metadata["review_gate"] = reviews[0]
			} else {
				delete(metadata, "review_gate")
			}
		}
		if len(reviews) == 0 {
			delete(metadata, "review_required")
			delete(metadata, "promotion_blocker")
		}
		for _, code := range reviewResolved {
			resolved = appendUnique(resolved, code)
		}
	}
	sort.Strings(resolved)
	return resolved
}

// curlReviewReasonsKey mirrors the cURL importer's source review list.
const curlReviewReasonsKey = "curl_review_reasons"

func filterCodes(codes []string, drop map[string]bool, dropped *[]string) []string {
	kept := make([]string, 0, len(codes))
	for _, code := range codes {
		if drop[code] {
			if dropped != nil {
				*dropped = appendUnique(*dropped, code)
			}
			continue
		}
		kept = append(kept, code)
	}
	return kept
}

func appendUnique(values []string, value string) []string {
	if containsString(values, value) {
		return values
	}
	return append(values, value)
}
