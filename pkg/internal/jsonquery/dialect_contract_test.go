package jsonquery

import (
	"github.com/jasonczech/apexload/pkg/internal/extractiontest"
	"testing"
)

func TestJSONPathDialectContract(t *testing.T) {
	extractiontest.Run(t, "loadrunner", func(expression string, body []byte) ([]string, error) {
		document, err := Decode(body)
		if err != nil {
			return nil, err
		}
		path, err := Parse(expression)
		if err != nil {
			return nil, err
		}
		values, err := path.Select(document, MaxMatches)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(values))
		for _, value := range values {
			text, err := ValueString(value)
			if err != nil {
				return nil, err
			}
			out = append(out, text)
		}
		return out, nil
	})
}
