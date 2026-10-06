package collectoragent

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/jasonczech/apexload/pkg/analysis"

	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/prometheus"
)

// AdapterTarget is a local operator approval. A signed job cannot add a remote
// identity, executable, driver, command, query, or credential destination.
type AdapterTarget struct {
	DynatraceOrigins []string           `json:"dynatrace_origins,omitempty"`
	Management       *ManagementTarget  `json:"management,omitempty"`
	Prometheus       *prometheus.Config `json:"prometheus,omitempty"`
	Cloud            *CloudQuery        `json:"cloud,omitempty"`
	ApprovedCIDRs    []string           `json:"approved_cidrs,omitempty"`
	CAPEM            string             `json:"ca_pem,omitempty"`
	EventsEndpoint   string             `json:"events_endpoint,omitempty"`
	Applications     []string           `json:"applications,omitempty"`
	Pack             string             `json:"pack"`
	Destination      string             `json:"destination"`
	HostKey          string             `json:"host_key,omitempty"`
	JavaExecutable   string             `json:"java_executable,omitempty"`
	HelperClassPath  string             `json:"helper_class_path,omitempty"`
	QueryPack        string             `json:"query_pack,omitempty"`
	TLSCAFile        string             `json:"tls_ca_file,omitempty"`
	JVMTrustStore    string             `json:"jvm_trust_store,omitempty"`
}

func LoadAdapterTargets(path string) ([]AdapterTarget, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > 64<<10 {
		return nil, errors.New("adapter configuration too large")
	}
	var targets []AdapterTarget
	decoder := json.NewDecoder(io.LimitReader(file, (64<<10)+1))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&targets); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("adapter configuration must contain one JSON value")
	}
	if len(targets) > 16 {
		return nil, errors.New("at most 16 approved monitor destinations")
	}
	return targets, nil
}
func (a *Agent) adapterTarget(job RunJob) (AdapterTarget, bool) {
	for _, target := range a.cfg.AdapterTargets {
		if target.Pack == job.Pack && target.Destination == job.Destination {
			return target, true
		}
	}
	return AdapterTarget{}, false
}
func (a *Agent) collectAdapter(ctx context.Context, job RunJob) ([]analysis.MetricPoint, error) {
	target, ok := a.adapterTarget(job)
	if !ok {
		return nil, errors.New("monitor destination is not locally approved")
	}
	if job.Pack == "otel-metrics" {
		points := a.cfg.OTLP.Snapshot(job)
		fresh := points[:0]
		for _, point := range points {
			if _, accepted := a.otlpAccepted[job.ID][otlpPointIdentity(point)]; !accepted {
				fresh = append(fresh, point)
			}
		}
		return fresh, nil
	}
	if job.Pack == "cloudwatch" || job.Pack == "azure-monitor" {
		points, err := collectCloud(ctx, target, job)
		if err != nil {
			return nil, err
		}
		return a.freshCloudPoints(job, points), nil
	}
	credentials, err := OpenCredentials(job, a.cfg.CredentialPrivateKey, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	switch job.Pack {
	case "ssh-linux":
		return collectSSH(ctx, target, credentials)
	case "jmx", "jdbc":
		return collectJava(ctx, target, credentials)
	}
	return nil, errors.New("unsupported monitor adapter")
}
func (a *Agent) approvedDestinations() []string {
	var destinations []string
	if a.cfg.PrometheusURL != "" {
		destinations = append(destinations, a.cfg.PrometheusURL)
	}
	for _, target := range a.cfg.AdapterTargets {
		if target.Management != nil {
			if !hasConfiguredPack(a.cfg.Packs, target.Pack) || a.management == nil {
				continue
			}
			observer := a.management.observers[target.Pack]
			if observer == nil || observer.validateTarget(target.Management) != nil {
				continue
			}
		}
		destination := target.Destination
		if target.Prometheus != nil || target.Pack == "ssh-linux" || target.Pack == "jmx" || target.Pack == "jdbc" || providerCredentialKey(target.Pack) || target.Management != nil && target.Management.Identity.CredentialRef != "" {
			if key, err := ecdh.X25519().NewPrivateKey(a.cfg.CredentialPrivateKey); err == nil {
				destination += "#" + base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
			}
		}
		destinations = append(destinations, destination)
		if target.Pack == monitorprovider.PackDynatrace {
			for _, origin := range target.DynatraceOrigins {
				if origin == target.Destination {
					continue
				}
				if key, err := ecdh.X25519().NewPrivateKey(a.cfg.CredentialPrivateKey); err == nil {
					destinations = append(destinations, origin+"#"+base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()))
				}
			}
		}
	}
	sort.Strings(destinations)
	return destinations
}

func validateAdapterTargets(cfg Config) error {
	if len(cfg.AdapterTargets) > 16 {
		return errors.New("at most 16 adapter targets are allowed")
	}
	seen := map[string]bool{}
	for _, target := range cfg.AdapterTargets {
		if target.Management != nil || isManagementPack(target.Pack) {
			if len(target.DynatraceOrigins) != 0 || target.Prometheus != nil || target.Cloud != nil || len(target.Applications) != 0 || target.EventsEndpoint != "" {
				return errors.New("management target cannot include provider query configuration")
			}
			endpoint, err := url.Parse(target.Destination)
			if err != nil || endpoint.Scheme != "management" || !managementAlias(endpoint.Hostname()) || endpoint.Host != endpoint.Hostname() || endpoint.Port() != "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || strings.Contains(target.Destination, "#") || endpoint.Fragment != "" || endpoint.Path != "" || endpoint.Opaque != "" || !managementLabel(target.Destination, 2048) {
				return errors.New("management destination must be a public alias")
			}
			if seen[target.Pack+"\x00"+target.Destination] {
				return errors.New("duplicate adapter destination")
			}
			seen[target.Pack+"\x00"+target.Destination] = true
			if err := validateManagementTarget(target.Pack, target.Management); err != nil {
				return err
			}
			if cfg.SpoolDirectory == "" || len(cfg.ControllerPublicKey) == 0 {
				return errors.New("management targets require signed jobs and durable spool")
			}
			if target.Management.Identity.CredentialRef != "" && len(cfg.CredentialPrivateKey) != 32 {
				return errors.New("management secret identity requires collector encryption key")
			}
			continue
		}
		if target.Pack == monitorprovider.PackPrometheusQuery {
			if target.Prometheus == nil || target.Prometheus.Endpoint != target.Destination || target.Prometheus.Validate() != nil || len(cfg.ControllerPublicKey) != ed25519.PublicKeySize {
				return errors.New("Prometheus query target requires valid local configuration and signing key")
			}
			if prometheusNeedsSealing(*target.Prometheus) {
				if len(cfg.CredentialPrivateKey) != 32 {
					return errors.New("secured Prometheus query target requires enrolled encryption key")
				}
			}
			if seen[target.Pack+"\x00"+target.Destination] {
				return errors.New("duplicate adapter destination")
			}
			seen[target.Pack+"\x00"+target.Destination] = true
			continue
		}
		expected := map[string]string{"ssh-linux": "ssh", "jmx": "jmx", "jdbc": "postgresql", "otel-metrics": "otlp", "dynatrace": "https", "cloudwatch": "cloudwatch", "azure-monitor": "azure-monitor"}[target.Pack]
		endpoint, err := url.Parse(target.Destination)
		if expected == "" || err != nil || endpoint.Scheme != expected || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Opaque != "" || len(target.Destination) > 2048 || target.Pack != "jdbc" && endpoint.Path != "" && !((target.Pack == monitorprovider.PackDynatrace) && endpoint.Path == "/") {
			return errors.New("invalid approved monitor destination")
		}
		if seen[target.Pack+"\x00"+target.Destination] {
			return errors.New("duplicate adapter destination")
		}
		seen[target.Pack+"\x00"+target.Destination] = true
		switch target.Pack {
		case monitorprovider.PackDynatrace:
			if len(target.DynatraceOrigins) > 3 || len(target.DynatraceOrigins) == 0 {
				return errors.New("Dynatrace requires locally approved origins")
			}
			for _, origin := range append([]string{target.Destination}, target.DynatraceOrigins...) {
				if origin == "https://sso.dynatrace.com" {
					continue
				}
				suffix := ".live.dynatrace.com"
				if strings.HasSuffix(strings.TrimRight(origin, "/"), ".apps.dynatrace.com") {
					suffix = ".apps.dynatrace.com"
				}
				if err := dynatrace.ValidateOrigin(origin, suffix); err != nil {
					return err
				}
			}
		case "cloudwatch", "azure-monitor":
			if endpoint.Port() != "" {
				return errors.New("cloud destination is an alias, not an endpoint")
			}
			if err := validateCloudQuery(target.Pack, target.Cloud); err != nil {
				return err
			}
		case "ssh-linux":
			_, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(target.HostKey))
			if err != nil || strings.TrimSpace(string(rest)) != "" {
				return errors.New("SSH requires one pinned host key")
			}
		case "jmx", "jdbc":
			if err := validateJavaTarget(target); err != nil {
				return err
			}
		case "otel-metrics":
			if cfg.OTLP == nil || cfg.OTLP.tokens[target.Destination] == "" {
				return errors.New("approved OTLP stream has no authenticated receiver")
			}
		}
	}
	return nil
}
func validateJavaTarget(target AdapterTarget) error {
	if !filepath.IsAbs(target.JavaExecutable) || !filepath.IsAbs(target.HelperClassPath) {
		return errors.New("Java helper requires approved absolute runtime and classpath")
	}
	if info, err := os.Stat(target.JavaExecutable); err != nil || !info.Mode().IsRegular() {
		return errors.New("approved Java runtime unavailable")
	}
	for _, entry := range filepath.SplitList(target.HelperClassPath) {
		if !filepath.IsAbs(entry) || strings.ContainsAny(entry, "*?") {
			return errors.New("helper classpath entries must be approved absolute paths")
		}
		if _, err := os.Stat(entry); err != nil {
			return errors.New("approved helper classpath unavailable")
		}
	}
	for _, path := range []string{target.TLSCAFile, target.JVMTrustStore} {
		if path != "" {
			info, err := os.Stat(path)
			if !filepath.IsAbs(path) || err != nil || !info.Mode().IsRegular() {
				return errors.New("approved TLS trust file unavailable")
			}
		}
	}
	if target.Pack == "jmx" && target.QueryPack != "jvm-basic-v1" || target.Pack == "jdbc" && target.QueryPack != "connection-health-v1" {
		return errors.New("unapproved Java query pack")
	}
	return nil
}

// Missing provider buckets may acquire a value during the next lookback query.
// Only numeric observations are immutable evidence eligible for deduplication.
func (a *Agent) freshCloudPoints(job RunJob, points []analysis.MetricPoint) []analysis.MetricPoint {
	fresh := make([]analysis.MetricPoint, 0, len(points))
	for _, point := range points {
		if point.Value == nil || point.Quality == analysis.QualityMissing {
			continue
		}
		if _, accepted := a.otlpAccepted[job.ID][otlpPointIdentity(point)]; !accepted {
			fresh = append(fresh, point)
		}
	}
	return fresh
}
