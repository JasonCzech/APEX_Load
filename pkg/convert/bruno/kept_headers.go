package bruno

import (
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// KeptHeader is a header that looks sensitive, such as a cookie or a session
// id, but carries recorded state rather than a credential. It stays in the
// scripts as imported; review lists it so nothing is silently dropped.
type KeptHeader struct {
	Name     string `json:"name"`
	Requests int    `json:"requests"`
}

// nativeKeptHeaders lists, by name, the headers the broad redaction policy
// treats as sensitive that credential review leaves as imported, with the
// number of requests that send each.
func nativeKeptHeaders(candidates []NativeScriptCandidate) []KeptHeader {
	counts := make(map[string]*KeptHeader)
	for _, candidate := range candidates {
		if candidate.Script == nil {
			continue
		}
		seen := make(map[string]struct{})
		for _, action := range candidate.Script.Actions {
			for _, header := range action.HeaderList {
				name := strings.TrimSpace(header.Name)
				key := strings.ToLower(name)
				if name == "" || !(scrub.Policy{}).SensitiveHeader(name) || credential.ImportCredentialHeader(name) {
					continue
				}
				if _, counted := seen[key]; counted {
					continue
				}
				seen[key] = struct{}{}
				if counts[key] == nil {
					counts[key] = &KeptHeader{Name: name}
				}
				counts[key].Requests++
			}
		}
	}
	kept := make([]KeptHeader, 0, len(counts))
	for _, header := range counts {
		kept = append(kept, *header)
	}
	sort.Slice(kept, func(i, j int) bool { return strings.ToLower(kept[i].Name) < strings.ToLower(kept[j].Name) })
	return kept
}
