package dynatrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (c *Client) Discover(ctx context.Context, request DiscoveryRequest) (DiscoveryResult, error) {
	result := DiscoveryResult{Items: []Entity{}}
	if len(request.Search) > 256 || len(request.Selector) > 4096 || len(request.Cursor) > 16<<10 {
		return result, errors.New("discovery inputs exceed bounds")
	}
	limit := request.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 200 {
		return result, errors.New("discovery limit must be 1-200")
	}
	ctx, cancel := c.pollContext(ctx)
	defer cancel()
	if request.Kind == "smartscape" || request.TopologyMode == "smartscape" {
		cursor, err := decodeCursor(request.Cursor)
		if err != nil {
			return result, err
		}
		query := "smartscapeNodes \"*\""
		if request.Selector != "" {
			if !safeName.MatchString(request.Selector) {
				return result, errors.New("Smartscape discovery selector must be a node type")
			}
			query = "smartscapeNodes " + strconv.Quote(request.Selector)
		}
		if request.Search != "" {
			query += " | filter contains(name, " + strconv.Quote(request.Search) + ")"
		}
		query += " | fields id, id_classic, name, type | limit " + strconv.Itoa(limit+1)
		from, to := c.now().Add(-time.Hour).UTC(), c.now().UTC()
		if !cursor.From.IsZero() {
			from, to = cursor.From, cursor.To
		}
		response, next, err := c.executeGrail(ctx, query, from, to, cursor, &QueryProgress{})
		if err != nil {
			return result, err
		}
		if next.Token != "" {
			result.NextCursor = encodeCursor(next)
			result.Message = "query still running"
			return result, nil
		}
		for _, record := range response.Result.Records {
			if len(result.Items) >= limit {
				result.Truncated = true
				break
			}
			id := text(record["id"])
			if !providerEntityID.MatchString(id) {
				continue
			}
			ids := map[string]string{"smartscape_id": id}
			if classic := text(record["id_classic"]); entityID.MatchString(classic) {
				ids["classic_id"] = classic
			}
			result.Items = append(result.Items, Entity{ID: id, Name: boundedText(text(record["name"]), 256), Type: boundedText(text(record["type"]), 128), EntityIDs: ids})
		}
		result.Truncated = result.Truncated || grailTruncated(response)
		if result.Truncated {
			result.Message = "discovery result limit reached; refine the type or search"
		}
		return result, nil
	}
	params := url.Values{"pageSize": {strconv.Itoa(limit)}}
	path := ""
	switch request.Kind {
	case "metrics":
		path = "/api/v2/metrics"
		params.Set("fields", "displayName,unit,defaultAggregation,aggregationTypes,dimensionDefinitions")
		if request.Selector != "" {
			params.Set("metricSelector", request.Selector)
		} else if request.Search != "" {
			params.Set("metricSelector", request.Search+"*")
		}
	case "entities":
		path = "/api/v2/entities"
		selector := request.Selector
		if selector == "" {
			selector = `type("SERVICE")`
		}
		params.Set("entitySelector", selector)
		params.Set("fields", classicRelationshipFields)
	case "entity_types":
		path = "/api/v2/entityTypes"
	default:
		return result, errors.New("unsupported discovery kind")
	}
	if request.Cursor != "" {
		params = url.Values{"nextPageKey": {request.Cursor}}
	}
	data, err := c.request(ctx, false, http.MethodGet, path, params, nil)
	if err != nil {
		return result, err
	}
	var page struct {
		Next     string                               `json:"nextPageKey"`
		Metrics  []metricDescriptor                   `json:"metrics"`
		Entities []classicEntity                      `json:"entities"`
		Types    []struct{ Type, DisplayName string } `json:"types"`
	}
	if json.Unmarshal(data, &page) != nil {
		return result, fmt.Errorf("invalid discovery response: %w", ErrInvalidResponse)
	}
	result.NextCursor = page.Next
	for _, m := range page.Metrics {
		if request.Search != "" && !strings.Contains(strings.ToLower(m.ID+" "+m.Name), strings.ToLower(request.Search)) {
			continue
		}
		if len(result.Items) >= limit {
			result.Truncated = true
			break
		}
		result.Items = append(result.Items, Entity{ID: m.ID, Name: boundedText(m.Name, 256), Type: "metric", Path: m.ID, EntityIDs: map[string]string{"unit": NormalizeUnit(m.Unit), "statistic": m.DefaultAggregation.Type}})
	}
	for _, e := range page.Entities {
		if request.Search != "" && !strings.Contains(strings.ToLower(e.Name), strings.ToLower(request.Search)) {
			continue
		}
		if len(result.Items) >= limit {
			result.Truncated = true
			break
		}
		result.Items = append(result.Items, normalizeEntity(e))
	}
	for _, e := range page.Types {
		if len(result.Items) >= limit {
			result.Truncated = true
			break
		}
		result.Items = append(result.Items, Entity{ID: e.Type, Name: boundedText(e.DisplayName, 256), Type: "entity_type"})
	}
	result.Truncated = result.Truncated || page.Next != ""
	if result.Truncated {
		result.Message = "bounded discovery page; request the continuation or refine search"
	}
	return result, nil
}

// Entities API v2 returns only ID and display name unless extra properties are
// requested with a leading plus.
const classicRelationshipFields = "+fromRelationships,+toRelationships"

// classicEntityRef is the Environment API v2 EntityId reference {id,type}.
type classicEntityRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// classicEntityStub is the Environment API v2 EntityStub used by problems and
// events: {entityId:{id,type}, name}.
type classicEntityStub struct {
	EntityID classicEntityRef `json:"entityId"`
	Name     string           `json:"name"`
}

type classicEntity struct {
	ID   string                        `json:"entityId"`
	Name string                        `json:"displayName"`
	Type string                        `json:"type"`
	From map[string][]classicEntityRef `json:"fromRelationships"`
	To   map[string][]classicEntityRef `json:"toRelationships"`
}

func normalizeEntity(e classicEntity) Entity {
	ids := map[string]string{}
	if entityID.MatchString(e.ID) {
		ids["classic_id"] = e.ID
	}
	fromKinds := make([]string, 0, len(e.From))
	for kind := range e.From {
		fromKinds = append(fromKinds, kind)
	}
	sort.Strings(fromKinds)
	for _, kind := range fromKinds {
		for index, ref := range e.From[kind] {
			if len(ids) >= 16 {
				break
			}
			if entityID.MatchString(ref.ID) {
				key := "from." + kind + "." + strconv.Itoa(index)
				if len(key) <= 128 {
					ids[key] = ref.ID
				}
			}
		}
	}
	toKinds := make([]string, 0, len(e.To))
	for kind := range e.To {
		toKinds = append(toKinds, kind)
	}
	sort.Strings(toKinds)
	for _, kind := range toKinds {
		for index, ref := range e.To[kind] {
			if len(ids) >= 16 {
				break
			}
			if entityID.MatchString(ref.ID) {
				key := "to." + kind + "." + strconv.Itoa(index)
				if len(key) <= 128 {
					ids[key] = ref.ID
				}
			}
		}
	}
	return Entity{ID: e.ID, Name: boundedText(e.Name, 256), Type: e.Type, EntityIDs: ids}
}
