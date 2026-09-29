package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/javarunner/broker"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

type javaHostGeneratorConfig struct {
	Contract  string                      `json:"contract"`
	Endpoint  string                      `json:"endpoint"`
	TokenFile string                      `json:"token_file"`
	Approvals []javaHostGeneratorApproval `json:"approvals"`
}
type javaHostGeneratorApproval struct {
	ScriptSHA256    string                               `json:"script_sha256"`
	TargetClass     string                               `json:"target_class"`
	Profile         broker.BrokerProfileCapability       `json:"profile"`
	FacadeResources generatorrpc.JavaHostFacadeResources `json:"facade_resources,omitzero"`
}

func readJavaHostConfigFile(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("java_host_config_path_invalid")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("java_host_config_unavailable")
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() < 1 || stat.Size() > limit {
		return nil, errors.New("java_host_config_file_invalid")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("java_host_config_read_failed")
	}
	return data, nil
}

// configureJavaHostRuntime is disabled unless an explicit operator config is
// mounted. It receives only a loopback token, never broker or Kubernetes keys.
func configureJavaHostRuntime(path string) (generatorrpc.ServiceOption, func() error, error) {
	if strings.TrimSpace(path) == "" {
		return nil, func() error { return nil }, nil
	}
	data, err := readJavaHostConfigFile(path, 128<<10)
	if err != nil {
		return nil, nil, err
	}
	var cfg javaHostGeneratorConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cfg) != nil || decoder.Decode(new(any)) != io.EOF || cfg.Contract != generatorrpc.JavaHostSelectionContract || len(cfg.Approvals) < 1 || len(cfg.Approvals) > 256 {
		return nil, nil, errors.New("java_host_config_invalid")
	}
	token, err := readJavaHostConfigFile(cfg.TokenFile, 4096)
	if err != nil {
		return nil, nil, err
	}
	invocation := time.Millisecond
	approvals := make([]generatorrpc.JavaHostApproval, len(cfg.Approvals))
	for i, a := range cfg.Approvals {
		if _, err := a.FacadeResources.Digest(); err != nil {
			return nil, nil, err
		}
		if a.Profile.InvocationMS < 1 || a.Profile.InvocationMS > 3600000 {
			return nil, nil, errors.New("java_host_config_invalid")
		}
		invocation = max(invocation, time.Duration(a.Profile.InvocationMS)*time.Millisecond)
		approvals[i] = generatorrpc.JavaHostApproval{ScriptSHA256: a.ScriptSHA256, TargetClass: a.TargetClass, Profile: a.Profile, FacadeResources: a.FacadeResources}
	}
	client, err := javasidecar.NewBrokerFacadeClient(cfg.Endpoint, strings.TrimSpace(string(token)), invocation)
	if err != nil {
		return nil, nil, errors.New("java_host_facade_config_invalid")
	}
	// Keep the owner alive through the generator's bounded natural drain; the
	// returned closer is called after execution shutdown, before any os.Exit.
	runtime, err := javasidecar.NewBrokerRuntime(context.Background(), client)
	if err != nil {
		client.Close()
		return nil, nil, errors.New("java_host_facade_unavailable")
	}
	option, err := generatorrpc.WithJavaHostRuntime(runtime, approvals)
	if err != nil {
		_ = runtime.Close()
		return nil, nil, err
	}
	return option, runtime.Close, nil
}
