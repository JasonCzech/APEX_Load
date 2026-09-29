package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

var errConfiguration = errors.New("java_broker_configuration_invalid")

type tlsFiles struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
	CA          string `json:"ca"`
}
type profileConfig struct {
	ConfigurationSHA256 string            `json:"configuration_sha256"`
	Image               string            `json:"image"`
	ServiceAccount      string            `json:"service_account"`
	MemoryBytes         int64             `json:"memory_bytes"`
	CPUMilli            int64             `json:"cpu_milli"`
	ScratchBytes        int64             `json:"scratch_bytes"`
	NodeSelector        map[string]string `json:"node_selector"`
	HostLeaseMS         int64             `json:"host_lease_ms"`
	PreparationMS       int64             `json:"preparation_ms"`
	InvocationMS        int64             `json:"invocation_ms"`
	TransportLifetimeMS int64             `json:"transport_lifetime_ms"`
}
type principalConfig struct {
	URI      string   `json:"uri"`
	OwnerID  string   `json:"owner_id"`
	Profiles []string `json:"profiles"`
}
type brokerConfig struct {
	Contract                    string                   `json:"contract"`
	Listen                      string                   `json:"listen"`
	StatePath                   string                   `json:"state_path"`
	Namespace                   string                   `json:"execution_namespace"`
	MaxHosts                    int                      `json:"max_hosts"`
	MaxVUsers                   int                      `json:"max_vusers"`
	MaxMemoryBytes              int64                    `json:"max_memory_bytes"`
	OwnerLeaseMS                int64                    `json:"owner_lease_ms"`
	KubernetesEndpoint          string                   `json:"kubernetes_endpoint"`
	KubernetesCA                string                   `json:"kubernetes_ca"`
	KubernetesToken             string                   `json:"kubernetes_token_file"`
	ServerTLS                   tlsFiles                 `json:"server_tls"`
	ControlTLS                  tlsFiles                 `json:"control_tls"`
	ControlClientURI            string                   `json:"control_client_uri"`
	HostIssuer                  tlsFiles                 `json:"host_issuer"`
	HostTrustDomain             string                   `json:"host_trust_domain"`
	HostDNSDomain               string                   `json:"host_dns_domain"`
	AttestorEndpoint            string                   `json:"attestor_endpoint"`
	AttestorURI                 string                   `json:"attestor_uri"`
	AttestorCA                  string                   `json:"attestor_ca"`
	AttestorQualificationSHA256 string                   `json:"attestor_qualification_sha256"`
	Principals                  []principalConfig        `json:"principals"`
	Profiles                    map[string]profileConfig `json:"profiles"`
}

func readBounded(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errConfiguration
	}
	st, e := os.Stat(path)
	if e != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errConfiguration
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, errConfiguration
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !opened.Mode().IsRegular() || !os.SameFile(st, opened) {
		return nil, errConfiguration
	}
	data, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(data)) > limit {
		return nil, errConfiguration
	}
	return data, nil
}
func loadConfig(path string) (brokerConfig, error) {
	var cfg brokerConfig
	data, e := readBounded(path, 1<<20)
	if e != nil {
		return cfg, e
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&cfg) != nil || dec.Decode(new(any)) != io.EOF || cfg.Contract != broker.BrokerContract || cfg.OwnerLeaseMS < 2000 || cfg.OwnerLeaseMS > 60000 || len(cfg.Profiles) == 0 || len(cfg.Profiles) > 256 || !filepath.IsAbs(cfg.StatePath) {
		return brokerConfig{}, errConfiguration
	}
	for _, p := range cfg.Profiles {
		if p.MemoryBytes <= 0 || p.HostLeaseMS < 1000 || p.HostLeaseMS > cfg.OwnerLeaseMS/2 || p.PreparationMS < 1000 || p.PreparationMS > 3600000 || p.InvocationMS < 1000 || p.InvocationMS > 3600000 || p.TransportLifetimeMS <= p.PreparationMS+2*p.HostLeaseMS || p.TransportLifetimeMS > 86400000 {
			return brokerConfig{}, errConfiguration
		}
		pod := p.managed(cfg.ControlClientURI).Pod
		pod.TLSSecret = "validation"
		if _, e := broker.ExecutionPod(cfg.Namespace, broker.Record{ID: "validation", BrokerEpoch: "validation", PodName: "validation", MemoryBytes: p.MemoryBytes, Binding: javarunner.HostBinding{ConfigurationSHA256: p.ConfigurationSHA256}}, pod); e != nil {
			return brokerConfig{}, errConfiguration
		}
	}
	return cfg, nil
}
func rootsFrom(path string) (*x509.CertPool, error) {
	data, e := readBounded(path, 128<<10)
	if e != nil {
		return nil, e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		return nil, errConfiguration
	}
	return roots, nil
}
func clientTLS(files tlsFiles) (*tls.Config, error) {
	certificate, e := readBounded(files.Certificate, 128<<10)
	if e != nil {
		return nil, e
	}
	key, e := readBounded(files.Key, 32<<10)
	if e != nil {
		return nil, e
	}
	pair, e := tls.X509KeyPair(certificate, key)
	if e != nil {
		return nil, errConfiguration
	}
	roots, e := rootsFrom(files.CA)
	if e != nil {
		return nil, e
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, RootCAs: roots}, nil
}
func (p profileConfig) managed(clientURI string) broker.ManagedPreparationConfig {
	return broker.ManagedPreparationConfig{ConfigurationSHA256: p.ConfigurationSHA256, Pod: broker.ExecutionPodConfig{Image: p.Image, ServiceAccount: p.ServiceAccount, CPUmilli: p.CPUMilli, ScratchBytes: p.ScratchBytes, NodeSelector: p.NodeSelector}, BrokerClientURI: clientURI, LeaseTTL: time.Duration(p.HostLeaseMS) * time.Millisecond, PreparationTimeout: time.Duration(p.PreparationMS) * time.Millisecond, InvocationTimeout: time.Duration(p.InvocationMS) * time.Millisecond, TransportLifetime: time.Duration(p.TransportLifetimeMS) * time.Millisecond}
}
func (c brokerConfig) storeConfig() broker.Config {
	return broker.Config{Namespace: c.Namespace, Budget: javarunner.HostBudget{MaxHosts: c.MaxHosts, MaxVUsers: c.MaxVUsers, MaxMemoryBytes: c.MaxMemoryBytes}}
}

func (p profileConfig) policySHA256() string {
	data, _ := json.Marshal(p)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
