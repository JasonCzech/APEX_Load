package http

import (
	"github.com/jasonczech/apexload/pkg/internal/extractiontest"
	"testing"
)

func TestJSONPathDialectContract(t *testing.T) {
	extractiontest.Run(t, "http", extractiontest.Scalar(extractJSONPath))
}
