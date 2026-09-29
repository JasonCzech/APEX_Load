package perfevidence

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/engine"
	"io"
	"math"
	"sort"
	"time"
)

const JavaEvidenceSchema = "apexload.java-vuser-evidence/v1"

// JavaEvidence is deliberately separate from the existing component report.
// Identity contains public content hashes/labels, never VM arguments or secrets.
type JavaEvidence struct {
	Soak                *JavaSoakEvidence    `json:"soak,omitempty"`
	ConfigurationSHA256 string               `json:"configuration_sha256"`
	Schema              string               `json:"schema"`
	GeneratedAt         time.Time            `json:"generated_at"`
	Workload            string               `json:"workload"`
	Mode                string               `json:"mode"`
	Profile             string               `json:"profile"`
	Identity            JavaEvidenceIdentity `json:"identity"`
	Samples             []JavaEvidenceSample `json:"samples"`
	SoakSeconds         float64              `json:"soak_seconds"`
	SoakPassed          bool                 `json:"soak_passed"`
	RealWorkload        bool                 `json:"real_workload"`
	Limitations         []string             `json:"limitations"`
}

type JavaEvidenceIdentity struct {
	WorkloadSHA256     string  `json:"workload_sha256"`
	RuntimeSHA256      string  `json:"runtime_sha256"`
	Environment        string  `json:"environment"`
	Backend            string  `json:"backend"`
	ResourceProfile    string  `json:"resource_profile"`
	Target             string  `json:"target"`
	VUsers             int     `json:"vusers"`
	OfferedTPS         float64 `json:"offered_tps"`
	WarmupSeconds      float64 `json:"warmup_seconds"`
	MeasurementSeconds float64 `json:"measurement_seconds"`
	MemoryMethod       string  `json:"memory_method"`
	AccountingScope    string  `json:"accounting_scope"`
}

type JavaEvidenceSample struct {
	IncompleteReasons      []string                  `json:"incomplete_reasons,omitempty"`
	HostDiagnostics        []engine.JavaHostEvidence `json:"host_diagnostics,omitempty"`
	MemoryBytes            []float64                 `json:"memory_bytes"`
	StartupPeakBytes       float64                   `json:"startup_peak_bytes"`
	CgroupPeakBytes        float64                   `json:"cgroup_peak_bytes"`
	CPUSeconds             float64                   `json:"cpu_seconds"`
	Transactions           int64                     `json:"transactions"`
	LatencySamples         int64                     `json:"latency_samples"`
	TPS                    float64                   `json:"tps"`
	P95MS                  float64                   `json:"p95_ms"`
	P99MS                  float64                   `json:"p99_ms"`
	Errors                 int64                     `json:"errors"`
	HarnessErrors          int64                     `json:"harness_errors"`
	DroppedResults         int64                     `json:"dropped_results"`
	ResourceFailures       int64                     `json:"resource_failures"`
	FunctionalChecksPassed bool                      `json:"functional_checks_passed"`
	Complete               bool                      `json:"complete"`
}

type JavaGateResult struct {
	Status                string   `json:"status"` // pass | fail | inconclusive
	Reasons               []string `json:"reasons"`
	MemorySavingPercent   float64  `json:"memory_saving_percent"`
	ThroughputLossPercent float64  `json:"throughput_loss_percent"`
	P95IncreasePercent    float64  `json:"p95_increase_percent"`
	P99IncreasePercent    float64  `json:"p99_increase_percent"`
	ProductionQualified   bool     `json:"production_qualified"` // never inferred from a pair
}

// ReadJavaEvidence rejects unknown/trailing fields and bounds untrusted reports.
func ReadJavaEvidence(r io.Reader) (JavaEvidence, error) {
	var report JavaEvidence
	data, err := io.ReadAll(io.LimitReader(r, (16<<20)+1))
	if err != nil || len(data) > 16<<20 {
		return report, errors.New("Java evidence document exceeds read limit")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&report); err != nil {
		return report, errors.New("invalid Java evidence document")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return report, errors.New("trailing Java evidence data")
	}
	if err := report.Validate(); err != nil {
		return report, err
	}
	return report, nil
}

func finiteNonnegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

func (r JavaEvidence) Validate() error {
	i := r.Identity
	for _, digest := range []string{r.ConfigurationSHA256, i.WorkloadSHA256, i.RuntimeSHA256} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != 32 {
			return errors.New("Java evidence requires SHA-256 identities")
		}
	}
	if r.Schema != JavaEvidenceSchema || r.Workload == "" || r.Profile == "" ||
		(r.Mode != "dedicated" && r.Mode != "pooled") || i.WorkloadSHA256 == "" ||
		i.RuntimeSHA256 == "" || i.Environment == "" || i.Backend == "" || i.ResourceProfile == "" ||
		i.Target == "" || i.AccountingScope == "" || i.MemoryMethod != "aggregate_process_pss_bytes" ||
		i.VUsers <= 0 || i.MeasurementSeconds <= 0 || i.WarmupSeconds <= 0 || i.OfferedTPS <= 0 ||
		!finiteNonnegative(i.MeasurementSeconds) || !finiteNonnegative(i.WarmupSeconds) ||
		!finiteNonnegative(i.OfferedTPS) || !finiteNonnegative(r.SoakSeconds) || len(r.Samples) > 100 {
		return errors.New("invalid Java evidence identity or configuration")
	}
	if r.Soak != nil {
		s := r.Soak
		if s.Schedule != javaSoakSchedule || !finiteNonnegative(s.RequestedSeconds) || s.RequestedSeconds < 3600 || s.RequestedSeconds > 7200 || !finiteNonnegative(s.WarmupSeconds) || s.WarmupSeconds != i.WarmupSeconds || !finiteNonnegative(s.ActiveSeconds) || s.ActiveSeconds != r.SoakSeconds || len(s.Users) != i.VUsers || i.VUsers > 10000 || len(s.MemoryBytes) > 100000 || s.Iterations < 0 || s.Transactions < 0 || s.Errors < 0 || s.DroppedResults < 0 || s.ResourceFailures < 0 || !finiteNonnegative(s.CgroupPeakBytes) {
			return errors.New("invalid Java soak evidence")
		}
		expected := javaSoakLifetimes(i.VUsers, time.Duration(s.WarmupSeconds*float64(time.Second)), time.Duration(s.RequestedSeconds*float64(time.Second)))
		for n, u := range s.Users {
			if u.StartSeconds != expected[n].StartAfter.Seconds() || u.StopSeconds != expected[n].StopAfter.Seconds() || !finiteNonnegative(u.InitializedSeconds+1) || !finiteNonnegative(u.ClosedSeconds+1) {
				return errors.New("invalid Java soak lifecycle evidence")
			}
		}
		for _, m := range s.MemoryBytes {
			if !finiteNonnegative(m) || m <= 0 {
				return errors.New("invalid Java soak memory")
			}
		}
		if err := validateJavaDiagnostics(s.HostDiagnostics, i.VUsers); err != nil {
			return err
		}
		if r.SoakPassed && (!s.passed(i.VUsers) || r.Mode == "pooled" && !completeJavaDiagnostics(s.HostDiagnostics, i.VUsers)) {
			return errors.New("inconsistent Java soak verdict")
		}
	}
	for _, s := range r.Samples {
		if err := validateJavaDiagnostics(s.HostDiagnostics, i.VUsers); err != nil {
			return err
		}
		for _, v := range []float64{s.StartupPeakBytes, s.CgroupPeakBytes, s.CPUSeconds, s.TPS, s.P95MS, s.P99MS} {
			if !finiteNonnegative(v) {
				return errors.New("invalid Java evidence measurement")
			}
		}
		if !validJavaIncompleteReasons(s.IncompleteReasons, s.Complete) || len(s.MemoryBytes) > 100000 || s.Transactions < 0 || s.LatencySamples < 0 || s.Errors < 0 || s.HarnessErrors < 0 ||
			s.DroppedResults < 0 || s.ResourceFailures < 0 || s.P99MS < s.P95MS {
			return errors.New("invalid Java evidence sample")
		}
		for _, v := range s.MemoryBytes {
			if !finiteNonnegative(v) || v == 0 {
				return errors.New("invalid Java memory sample")
			}
		}
		if s.Complete && (s.Transactions == 0 || s.LatencySamples == 0 || len(s.MemoryBytes) < 2 || s.TPS <= 0 || s.P95MS <= 0 ||
			math.Abs(s.TPS-float64(s.Transactions)/i.MeasurementSeconds) > 0.001) {
			return errors.New("complete Java evidence sample has missing or inconsistent measurements")
		}
	}
	return nil
}

// EvaluateJavaGate compares one matched workload. A pass does not qualify a
// deployment or a real corpus; callers must independently require both fixtures.
func EvaluateJavaGate(baseline, candidate JavaEvidence) JavaGateResult {
	r := JavaGateResult{Status: "inconclusive", Reasons: []string{}}
	if baseline.Validate() != nil || candidate.Validate() != nil {
		r.Reasons = append(r.Reasons, "invalid_evidence")
		return r
	}
	if baseline.Mode != "dedicated" || candidate.Mode != "pooled" || baseline.Workload != candidate.Workload ||
		baseline.Identity != candidate.Identity || baseline.RealWorkload != candidate.RealWorkload {
		r.Reasons = append(r.Reasons, "comparison_not_matched")
		return r
	}
	if len(baseline.Samples) != 5 || len(candidate.Samples) != 5 {
		r.Reasons = append(r.Reasons, "five_repetitions_required")
		return r
	}
	bs, cs := javaGateSeries(baseline), javaGateSeries(candidate)
	for _, report := range []JavaEvidence{baseline, candidate} {
		for _, s := range report.Samples {
			if !s.Complete || s.DroppedResults != 0 || report.Mode == "pooled" && !completeJavaDiagnostics(s.HostDiagnostics, report.Identity.VUsers) {
				r.Reasons = append(r.Reasons, "incomplete_evidence")
				return r
			}
			if !s.FunctionalChecksPassed || s.Errors != 0 || s.HarnessErrors != 0 || s.ResourceFailures != 0 {
				r.Status = "fail"
				r.Reasons = append(r.Reasons, "correctness_or_resource_failure")
				return r
			}
			if s.TPS < report.Identity.OfferedTPS*.95 {
				r.Status = "fail"
				r.Reasons = append(r.Reasons, "offered_load_not_achieved")
				return r
			}
		}
	}
	for _, series := range append(bs, cs...) {
		median := javaMedian(series)
		if median <= 0 || (maxSlice(series)-minSlice(series))/median > 0.05+1e-9 {
			r.Reasons = append(r.Reasons, "measurement_noise_exceeds_five_percent")
			return r
		}
	}
	r.MemorySavingPercent = 100 * (1 - javaMedian(cs[0])/javaMedian(bs[0]))
	r.ThroughputLossPercent = 100 * (1 - javaMedian(cs[1])/javaMedian(bs[1]))
	r.P95IncreasePercent = 100 * (javaMedian(cs[2])/javaMedian(bs[2]) - 1)
	r.P99IncreasePercent = 100 * (javaMedian(cs[3])/javaMedian(bs[3]) - 1)
	if r.MemorySavingPercent+1e-9 < 30 {
		r.Reasons = append(r.Reasons, "memory_saving_below_thirty_percent")
	}
	if r.ThroughputLossPercent > 5+1e-9 {
		r.Reasons = append(r.Reasons, "throughput_regression")
	}
	if r.P95IncreasePercent > 5+1e-9 || r.P99IncreasePercent > 5+1e-9 {
		r.Reasons = append(r.Reasons, "latency_regression")
	}
	if len(r.Reasons) > 0 {
		r.Status = "fail"
		return r
	}
	if baseline.Soak == nil || candidate.Soak == nil || baseline.Soak.RequestedSeconds != candidate.Soak.RequestedSeconds {
		r.Reasons = append(r.Reasons, "matched_observed_soak_required")
		return r
	}
	if baseline.SoakSeconds < 3600 || candidate.SoakSeconds < 3600 || !baseline.SoakPassed || !candidate.SoakPassed {
		r.Reasons = append(r.Reasons, "one_hour_soak_required")
		return r
	}
	r.Status = "pass"
	return r
}

func javaGateSeries(r JavaEvidence) [][]float64 {
	s := make([][]float64, 4)
	for _, v := range r.Samples {
		s[0] = append(s[0], javaMedian(v.MemoryBytes))
		s[1] = append(s[1], v.TPS)
		s[2] = append(s[2], v.P95MS)
		s[3] = append(s[3], v.P99MS)
	}
	return s
}
func javaMedian(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	a := append([]float64(nil), v...)
	sort.Float64s(a)
	if len(a)%2 == 0 {
		return (a[len(a)/2-1] + a[len(a)/2]) / 2
	}
	return a[len(a)/2]
}
func minSlice(v []float64) float64 {
	x := math.Inf(1)
	for _, n := range v {
		if n < x {
			x = n
		}
	}
	return x
}
func maxSlice(v []float64) float64 {
	x := float64(0)
	for _, n := range v {
		if n > x {
			x = n
		}
	}
	return x
}
