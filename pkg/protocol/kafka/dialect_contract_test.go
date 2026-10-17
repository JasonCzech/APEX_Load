package kafka

import (
	"github.com/jasonczech/apexload/pkg/internal/extractiontest"
	"testing"
)

func TestJSONPathDialectContract(t *testing.T) {
	extractiontest.Run(t, "messaging", extractiontest.Scalar(extractJSONPath))
}
