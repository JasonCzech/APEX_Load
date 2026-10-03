package analysis

import (
	"errors"
	"slices"
	"strings"
)

const MaxDerivationSteps = 16
const legacyDerivedOperation = "legacy_derived"

// DerivationStep identifies one bounded transformation. Full parent lineage is
// retained: avg(p95(...)) cannot be matched to avg(avg(...)). Algorithm and
// elapsed bucket size are semantic identities, not display hints.
type DerivationStep struct {
	Operation     string `json:"operation"`
	Algorithm     string `json:"algorithm"`
	BucketSeconds int    `json:"bucket_seconds,omitempty"`
}

func validateDerivation(steps []DerivationStep, qualified bool) error {
	if len(steps) > MaxDerivationSteps {
		return errors.New("measurement derivation exceeds 16 steps")
	}
	for _, step := range steps {
		if step.Operation == legacyDerivedOperation {
			if qualified {
				return errors.New("legacy derived measurement has unknown parent statistic")
			}
		} else if _, found := lookupOperation(step.Operation); !found {
			return errors.New("unsupported typed measurement derivation")
		}
		if strings.TrimSpace(step.Algorithm) == "" || len(step.Algorithm) > maxMetricIdentityBytes || strings.ContainsRune(step.Algorithm, 0) || step.BucketSeconds < 0 || step.BucketSeconds > 86400 {
			return errors.New("invalid bounded derivation algorithm or bucket size")
		}
	}
	return nil
}

// ScalarBucketDerivation returns independent lineage for the existing scalar
// aggregation algorithms. Reaggregated legacy buckets retain an unknown-parent
// marker and stay unqualified for comparisons instead of becoming new raw data.
// Raw counter rates have no epoch contract: higher values after a restart are
// indistinguishable from progress, so these display series stay unqualified.
func ScalarBucketDerivation(point MetricPoint, bucketSeconds int, aggregation string) ([]DerivationStep, error) {
	if bucketSeconds < 1 || bucketSeconds > 86400 {
		return nil, errors.New("invalid scalar bucket size")
	}
	if err := ValidateScalarAggregation(point.Kind, aggregation); err != nil {
		return nil, err
	}
	steps := slices.Clone(point.Derivation)
	if point.InputKind != "" && len(steps) == 0 {
		steps = append(steps, DerivationStep{Operation: legacyDerivedOperation, Algorithm: "unknown"})
	}
	algorithm := "scalar_bucket.v1"
	if slices.Contains([]string{"p50", "p95", "p99"}, aggregation) {
		algorithm = "linear_quantile.v1"
	}
	if aggregation == "rate" {
		steps = append(steps, DerivationStep{Operation: legacyDerivedOperation, Algorithm: "unknown_counter_epoch"})
		algorithm = "adjacent_counter_mean_rate.v1"
	}
	steps = append(steps, DerivationStep{Operation: aggregation, Algorithm: algorithm, BucketSeconds: bucketSeconds})
	return steps, validateDerivation(steps, false)
}

func semanticsOf(point MetricPoint) MeasurementSemantics {
	return MeasurementSemantics{Kind: point.Kind, InputKind: point.InputKind, Unit: point.Unit, Population: point.Population, MetricVersion: point.MetricVersion, Derivation: point.Derivation}
}
