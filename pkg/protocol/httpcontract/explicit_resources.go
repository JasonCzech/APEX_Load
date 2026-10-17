package httpcontract

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type ExplicitResource struct {
	URL     string  `json:"url"`
	Referer *string `json:"referer,omitempty"`
}

var resourceTemplate = regexp.MustCompile(`\{\{[^{}]+\}\}`)

// ValidateExplicitResourceTemplates permits authoring-time templates; Execute
// still validates the fully substituted URLs before making any request.
func ValidateExplicitResourceTemplates(params map[string]any) error {
	raw, ok := params["resources"]
	if !ok {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("invalid explicit resources")
	}
	var value any
	if err := json.Unmarshal(resourceTemplate.ReplaceAll(data, []byte("template-value")), &value); err != nil {
		return err
	}
	_, err = ParseExplicitResources(map[string]any{"resources": value})
	return err
}

func ParseExplicitResources(params map[string]any) ([]ExplicitResource, error) {
	raw, ok := params["resources"]
	if !ok {
		return nil, nil
	}
	data, err := json.Marshal(raw)
	if err != nil || len(data) > 256*1024 {
		return nil, fmt.Errorf("invalid explicit resources")
	}
	var refs []ExplicitResource
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&refs); err != nil {
		return nil, fmt.Errorf("explicit resources must be an array of {url, referer?}")
	}
	if len(refs) > MaxPageResources {
		return nil, fmt.Errorf("explicit resources exceed %d", MaxPageResources)
	}
	for _, r := range refs {
		if strings.TrimSpace(r.URL) == "" || strings.ContainsAny(r.URL, "\r\n") {
			return nil, fmt.Errorf("explicit resource URL is required")
		}
		u, e := url.Parse(r.URL)
		if e != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return nil, fmt.Errorf("invalid explicit resource URL")
		}
		if r.Referer != nil && strings.ContainsAny(*r.Referer, "\r\n") {
			return nil, fmt.Errorf("invalid resource referer")
		}
	}
	return refs, nil
}
