package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type GroupConfig struct {
	Name     string
	Script   *dsl.Script
	VUsers   int
	RampUp   time.Duration
}

type MultiGroupConfig struct {
	Groups   []GroupConfig
	Duration time.Duration
}

type MultiGroupEngine struct {
	groups   []GroupConfig
	duration time.Duration
	metrics  *MetricsCollector
	cancel   context.CancelFunc
}

type GroupResult struct {
	Name              string
	TotalTransactions int64
	TotalErrors       int64
}

type MultiGroupRunResult struct {
	Duration          time.Duration
	TotalTransactions int64
	TotalErrors       int64
	FinalSnapshot     MetricsSnapshot
	Groups            []GroupResult
}

func NewMultiGroup(cfg MultiGroupConfig) (*MultiGroupEngine, error) {
	if len(cfg.Groups) == 0 {
		return nil, fmt.Errorf("at least one group is required")
	}
	if cfg.Duration <= 0 {
		return nil, fmt.Errorf("duration must be positive")
	}
	for i, g := range cfg.Groups {
		if g.Script == nil {
			return nil, fmt.Errorf("group[%d] %q: script is required", i, g.Name)
		}
		if g.VUsers <= 0 {
			return nil, fmt.Errorf("group[%d] %q: vusers must be positive", i, g.Name)
		}
		if g.Name == "" {
			cfg.Groups[i].Name = g.Script.Name
		}
	}
	return &MultiGroupEngine{
		groups:   cfg.Groups,
		duration: cfg.Duration,
		metrics:  NewMetricsCollector(10000),
	}, nil
}

func (mge *MultiGroupEngine) Run(ctx context.Context) (*MultiGroupRunResult, error) {
	ctx, cancel := context.WithTimeout(ctx, mge.duration)
	mge.cancel = cancel
	defer cancel()

	mge.metrics.Start(time.Second)
	defer mge.metrics.Stop()

	start := time.Now()
	var wg sync.WaitGroup
	groupResults := make([]GroupResult, len(mge.groups))

	vuserOffset := 0
	for gi, group := range mge.groups {
		gi, group := gi, group
		groupResults[gi] = GroupResult{Name: group.Name}

		protoConfig := map[string]any{
			"base_url": group.Script.Config.BaseURL,
		}

		// Pass full HTTP config if present
		httpCfg := group.Script.Config.HTTP
		if httpCfg.ConnectTimeoutMs > 0 {
			protoConfig["connect_timeout_ms"] = float64(httpCfg.ConnectTimeoutMs)
		}
		if httpCfg.RequestTimeoutMs > 0 {
			protoConfig["request_timeout_ms"] = float64(httpCfg.RequestTimeoutMs)
		}
		if httpCfg.TLSTimeoutMs > 0 {
			protoConfig["tls_timeout_ms"] = float64(httpCfg.TLSTimeoutMs)
		}
		if httpCfg.IdleTimeoutMs > 0 {
			protoConfig["idle_timeout_ms"] = float64(httpCfg.IdleTimeoutMs)
		}
		if httpCfg.MaxConnsPerHost > 0 {
			protoConfig["max_conns_per_host"] = float64(httpCfg.MaxConnsPerHost)
		}
		if httpCfg.MaxIdleConns > 0 {
			protoConfig["max_idle_conns"] = float64(httpCfg.MaxIdleConns)
		}
		if httpCfg.KeepAlive != nil {
			protoConfig["keep_alive"] = *httpCfg.KeepAlive
		}
		if httpCfg.Version != "" {
			protoConfig["http_version"] = httpCfg.Version
		}
		if httpCfg.MaxRedirects != nil {
			protoConfig["max_redirects"] = float64(*httpCfg.MaxRedirects)
		}
		if httpCfg.CookiesEnabled != nil {
			protoConfig["cookies_enabled"] = *httpCfg.CookiesEnabled
		}
		if httpCfg.UserAgent != "" {
			protoConfig["user_agent"] = httpCfg.UserAgent
		}
		if httpCfg.AcceptEncoding != "" {
			protoConfig["accept_encoding"] = httpCfg.AcceptEncoding
		}
		if httpCfg.AcceptLanguage != "" {
			protoConfig["accept_language"] = httpCfg.AcceptLanguage
		}
		if len(httpCfg.DefaultHeaders) > 0 {
			protoConfig["default_headers"] = httpCfg.DefaultHeaders
		}

		// TLS config
		tls := httpCfg.TLS
		if tls.MinVersion != "" {
			protoConfig["tls_min_version"] = tls.MinVersion
		}
		if tls.MaxVersion != "" {
			protoConfig["tls_max_version"] = tls.MaxVersion
		}
		if tls.InsecureSkipVerify {
			protoConfig["tls_insecure_skip_verify"] = true
		}
		if tls.ClientCertFile != "" {
			protoConfig["tls_client_cert_file"] = tls.ClientCertFile
		}
		if tls.ClientKeyFile != "" {
			protoConfig["tls_client_key_file"] = tls.ClientKeyFile
		}
		if tls.CACertFile != "" {
			protoConfig["tls_ca_cert_file"] = tls.CACertFile
		}

		// Proxy config
		proxy := httpCfg.Proxy
		if proxy.URL != "" {
			protoConfig["proxy_url"] = proxy.URL
		}
		if proxy.Username != "" {
			protoConfig["proxy_username"] = proxy.Username
		}
		if proxy.Password != "" {
			protoConfig["proxy_password"] = proxy.Password
		}
		if len(proxy.NoProxy) > 0 {
			protoConfig["proxy_no_proxy"] = proxy.NoProxy
		}

		// Auth config
		auth := httpCfg.Auth
		if auth.Type != "" {
			protoConfig["auth_type"] = auth.Type
		}
		if auth.Username != "" {
			protoConfig["auth_username"] = auth.Username
		}
		if auth.Password != "" {
			protoConfig["auth_password"] = auth.Password
		}
		if auth.Token != "" {
			protoConfig["auth_token"] = auth.Token
		}
		if auth.Domain != "" {
			protoConfig["auth_domain"] = auth.Domain
		}

		rampDelay := time.Duration(0)
		if group.RampUp > 0 && group.VUsers > 1 {
			rampDelay = group.RampUp / time.Duration(group.VUsers)
		}

		for i := 0; i < group.VUsers; i++ {
			if ctx.Err() != nil {
				break
			}

			if i > 0 && rampDelay > 0 {
				time.Sleep(rampDelay)
			}

			wg.Add(1)
			vuserID := vuserOffset + i

			vuProto, err := protocol.Get(group.Script.Protocol)
			if err != nil {
				slog.Error("failed to create protocol", "group", group.Name, "vuser", vuserID, "err", err)
				wg.Done()
				continue
			}
			if err := vuProto.Init(ctx, protoConfig); err != nil {
				slog.Error("failed to init protocol", "group", group.Name, "vuser", vuserID, "err", err)
				wg.Done()
				continue
			}

			mge.metrics.SetActiveVUsers(int64(vuserOffset + i + 1))

			go func(gIdx int, grp GroupConfig) {
				defer wg.Done()
				defer vuProto.Teardown(context.Background())

				vu := &VUser{
					ID:        vuserID,
					Group:     grp.Name,
					Proto:     vuProto,
					Script:    grp.Script,
					Metrics:   mge.metrics,
				}
				vu.Run(ctx)
			}(gi, group)
		}

		vuserOffset += group.VUsers
	}

	wg.Wait()
	elapsed := time.Since(start)

	result := &MultiGroupRunResult{
		Duration:          elapsed,
		TotalTransactions: mge.metrics.TotalTransactions(),
		TotalErrors:       mge.metrics.TotalErrors(),
		Groups:            groupResults,
	}
	if snap, ok := mge.metrics.LatestSnapshot(); ok {
		result.FinalSnapshot = snap
	}
	return result, nil
}

func (mge *MultiGroupEngine) Stop() {
	if mge.cancel != nil {
		mge.cancel()
	}
}

func (mge *MultiGroupEngine) LatestSnapshot() (MetricsSnapshot, bool) {
	return mge.metrics.LatestSnapshot()
}

func (mge *MultiGroupEngine) GroupNames() []string {
	names := make([]string, len(mge.groups))
	for i, g := range mge.groups {
		names[i] = g.Name
	}
	return names
}
