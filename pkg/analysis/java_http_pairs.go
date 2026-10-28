package analysis

import (
	"errors"
	"math"
)

// JavaHTTPPairedSums is produced only from same-action, same-version paired
// observations. Eligible includes missing measurements: incomplete populations
// are unscored. Numeric validation cannot establish source pairing by itself.
type JavaHTTPPairedSums struct {
	Version                int
	Eligible, Samples      int64
	Numerator, Denominator float64
	Complete               bool
}

func EvaluateJavaHTTPPairedSums(metric string, p JavaHTTPPairedSums) (DerivedValue, error) {
	timeWork := metric == "java_http_base64_work_share"
	switch metric {
	case "java_http_decoded_per_encoded", "java_http_prepared_per_decoded", "serialized_representation_vs_http_body", "java_http_base64_per_retained", "java_http_base64_work_share":
	default:
		return DerivedValue{}, errors.New("unsupported Java paired-sum metric")
	}
	if p.Version != 1 || !p.Complete || p.Eligible != p.Samples || p.Samples <= 0 || p.Samples > 100000 {
		return unscored("ratio", "incomplete_representation_evidence"), nil
	}
	limit := float64(1<<53 - 1)
	if timeWork {
		limit = float64(p.Samples) * 24 * 60 * 60 * 1000
	}
	for _, v := range []float64{p.Numerator, p.Denominator} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > limit || !timeWork && math.Trunc(v) != v {
			return unscored("ratio", "invalid_paired_sums"), nil
		}
	}
	if timeWork && p.Numerator > p.Denominator*(1+1e-12) {
		return unscored("ratio", "invalid_paired_sums"), nil
	}
	if p.Denominator == 0 {
		return unscored("ratio", "zero_denominator"), nil
	}
	value := p.Numerator / p.Denominator
	if timeWork {
		value = min(1, value)
	}
	return measured("ratio", value), nil
}
