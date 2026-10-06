package elastic

import (
	"context"
	"errors"
	"path"
	"sort"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// ResolveConcreteIndices resolves only the approved targets. Alias filters stay
// attached to the original PIT target; this set fences normalized record identity
// and remains executor-private for the fixed pagination window.
func (c *Client) ResolveConcreteIndices(ctx context.Context, targets []string) ([]string, error) {
	if err := validateIndices(targets); err != nil {
		return nil, err
	}
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	var response struct {
		Indices []struct {
			Name string `json:"name"`
		} `json:"indices"`
		Aliases []struct {
			Name    string   `json:"name"`
			Indices []string `json:"indices"`
		} `json:"aliases"`
		DataStreams []struct {
			Name           string   `json:"name"`
			BackingIndices []string `json:"backing_indices"`
		} `json:"data_streams"`
	}
	if err := c.request(ctx, "GET", "/_resolve/index/"+indexPath(targets)+"?expand_wildcards=open", nil, &response); err != nil {
		return nil, err
	}
	matches := func(name string) bool {
		for _, target := range targets {
			if ok, _ := path.Match(target, name); ok {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	out := []string{}
	add := func(names ...string) error {
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
			if len(out) > 200 {
				return errors.New("resolved index scope exceeds 200 concrete indices")
			}
		}
		return nil
	}
	for _, index := range response.Indices {
		if matches(index.Name) {
			if err := add(index.Name); err != nil {
				return nil, err
			}
		}
	}
	for _, alias := range response.Aliases {
		if matches(alias.Name) {
			if err := add(alias.Indices...); err != nil {
				return nil, err
			}
		}
	}
	for _, stream := range response.DataStreams {
		if matches(stream.Name) {
			if err := add(stream.BackingIndices...); err != nil {
				return nil, err
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("approved targets did not resolve concrete indices")
	}
	sort.Strings(out)
	if err := analysis.ValidateResolvedIndices(out); err != nil {
		return nil, err
	}
	return out, nil
}
