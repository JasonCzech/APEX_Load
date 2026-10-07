package elastic

import (
	"errors"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/credential"
)

const dimensionIdentity = "apex.dimension_id"

func validDimensionField(name string) bool {
	return fieldName.MatchString(name) && name != "query_id" && name != "statistic" && name != dimensionIdentity && !credential.SensitiveName(name)
}

// Typed identity keeps missing values and scrubbed labels from collapsing
// distinct series. Only the bounded opaque identity and scrubbed labels leave
// collection; original scalar values are never persisted as dimension labels.
func (c *Client) mappedDimensions(queryID, statistic string, values map[string]analysis.ScalarValue) (map[string]string, error) {
	dimensions := map[string]string{"query_id": queryID, "statistic": statistic}
	for name, value := range values {
		text := scalarText(value)
		if len(text) > 128 {
			return nil, errors.New("metric dimension exceeds bounds")
		}
		dimensions[name] = stringValue(c.redact(text), 128)
	}
	if len(values) != 0 {
		dimensions[dimensionIdentity] = queryIDHash(queryID, values)
	}
	return dimensions, nil
}

func queryIDHash(identity string, values map[string]analysis.ScalarValue) string {
	return queryID([]any{identity, values})
}
