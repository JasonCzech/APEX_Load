package analysis

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/credential"
)

var (
	errParquetProjectedPath      = errors.New("invalid analytical projected column path")
	errParquetProjectedProtected = errors.New("protected credential columns cannot become analytical evidence")
)

// ValidateParquetProjectedColumn admits finite source paths before credential
// classification. A selected protected value cannot enter a public timestamp,
// metric name, measurement or identity merely through a cast or aggregation.
// Authored output names and ordinary scalar values remain exact data.
func ValidateParquetProjectedColumn(path ParquetColumnPath) error {
	if len(path) < 1 || len(path) > 16 {
		return errParquetProjectedPath
	}
	for _, part := range path {
		if part == "" || len(part) > 256 || !utf8.ValidString(part) || strings.ContainsRune(part, 0) {
			return errParquetProjectedPath
		}
	}
	for _, part := range path {
		if credential.SensitiveName(part) {
			return errParquetProjectedProtected
		}
	}
	if credential.SensitiveName(strings.Join(path, ".")) {
		return errParquetProjectedProtected
	}
	return nil
}

// ValidateDisclosure checks every declared projected source path, including
// persisted mapping metadata before querying normalized artifacts. Historical
// projections may omit paths; this check never invents their source origin.
// New mapping admission separately requires the complete valid mapping shape.
func (m ParquetMapping) ValidateDisclosure() error {
	if len(m.Measures) > 64 {
		return errors.New("mapping exceeds projected measurement bound")
	}
	if len(m.Timestamp) != 0 {
		if err := ValidateParquetProjectedColumn(m.Timestamp); err != nil {
			return err
		}
	}
	for _, measure := range m.Measures {
		for _, path := range []ParquetColumnPath{measure.Value, measure.NameColumn} {
			if len(path) != 0 {
				if err := ValidateParquetProjectedColumn(path); err != nil {
					return err
				}
			}
		}
	}
	return m.Identity.ValidateDisclosure()
}
