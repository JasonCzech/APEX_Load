package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/debugserver"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/kubeops"
	_ "github.com/jasonczech/apexload/pkg/protocol/builtin"
	"github.com/jasonczech/apexload/pkg/storage"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Optional pprof debug server (disabled unless APEXLOAD_PPROF_ADDR is set).
	if pprofSrv, err := debugserver.StartPprofFromEnv(os.Getenv); err != nil {
		slog.Error("failed to start pprof debug server", "err", err)
	} else if pprofSrv != nil {
		defer pprofSrv.Close()
	}

	// Controller HTTP bind address (for API + embedded dashboard).
	addr, bindScope := config.ResolveControllerBindAddr(os.Getenv)
	slog.Info("controller bind address resolved", "addr", addr, "bind_scope", bindScope)

	store, err := storage.NewStoreFromEnv()
	if err != nil {
		slog.Error("failed to initialize storage", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	generatorTargets := parseGeneratorTargets(os.Getenv("APEXLOAD_GENERATORS"))
	generatorTimeout := parseGeneratorTimeout(os.Getenv("APEXLOAD_GENERATOR_TIMEOUT"))
	recorderProxyAddr := recorderProxyAddrFromEnv()
	k8sRestartConfig := config.LoadK8sRestartOperatorConfig()
	k8sRestartSettings := api.K8sRestartSettings{
		Enabled:              k8sRestartConfig.Enabled,
		Namespace:            k8sRestartConfig.Namespace,
		ControllerDeployment: k8sRestartConfig.ControllerDeployment,
		GeneratorStatefulSet: k8sRestartConfig.GeneratorStatefulSet,
		GeneratorSelector:    k8sRestartConfig.GeneratorSelector,
		AllowForce:           k8sRestartConfig.AllowForce,
	}
	var restarter api.KubeRestarter
	if k8sRestartConfig.Enabled {
		client, err := kubeops.NewInClusterClient(kubeops.Config{
			Namespace:         k8sRestartConfig.Namespace,
			GeneratorSelector: k8sRestartConfig.GeneratorSelector,
		})
		if err != nil {
			slog.Warn("Kubernetes restart controls requested but unavailable", "err", err)
		} else {
			restarter = kubeRestarterAdapter{client: client}
			slog.Info("Kubernetes restart controls enabled", "namespace", k8sRestartConfig.Namespace, "generator_selector", k8sRestartConfig.GeneratorSelector)
		}
	}
	if len(generatorTargets) > 0 {
		security := generatorrpc.TransportSecurityFromEnv()
		slog.Info("distributed execution enabled", "generators", generatorTargets, "timeout", generatorTimeout, "grpc_transport", security.String())
		if security.Insecure() && os.Getenv("APEXLOAD_ENV") == "production" {
			slog.Warn("generator gRPC is plaintext and unauthenticated in production; set APEXLOAD_GRPC_TLS_* and/or APEXLOAD_GRPC_AUTH_TOKEN")
		}
	}

	haLeaderElection := envIsTruthy(os.Getenv("APEXLOAD_CONTROLLER_HA"))
	if haLeaderElection {
		slog.Info("HA execution leader election enabled; only the lease holder will run tests")
	}

	server := api.NewServer(api.ServerConfig{
		Addr:                  addr,
		BindScope:             bindScope,
		MetricsAddr:           strings.TrimSpace(os.Getenv("APEXLOAD_METRICS_ADDR")),
		MetricsToken:          strings.TrimSpace(os.Getenv("APEXLOAD_METRICS_TOKEN")),
		ProxyAddr:             recorderProxyAddr,
		MaxConcurrent:         10,
		Store:                 store,
		GeneratorTargets:      generatorTargets,
		GeneratorTimeout:      generatorTimeout,
		K8sRestart:            k8sRestartSettings,
		KubeRestarter:         restarter,
		HAExecutionLeadership: haLeaderElection,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go server.RunExecutionLeadership(ctx)
	go server.RunGeneratorPodTerminationWatcher(ctx, 5*time.Second)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := server.RunDueSchedules(); err != nil {
					slog.Error("scheduled test evaluator failed", "err", err)
				}
			}
		}
	}()

	// Run server in background so the main goroutine can handle shutdown signals.
	go func() {
		if err := server.Start(); err != nil {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	server.Shutdown(context.Background())
}

func envIsTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

func parseGeneratorTargets(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	targets := make([]string, 0, len(parts))
	for _, part := range parts {
		target := strings.TrimSpace(part)
		if target == "" {
			continue
		}
		targets = append(targets, target)
	}
	return targets
}

func parseGeneratorTimeout(raw string) time.Duration {
	if raw == "" {
		return 10 * time.Second
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		slog.Warn("invalid APEXLOAD_GENERATOR_TIMEOUT, falling back to default", "value", raw)
		return 10 * time.Second
	}
	return duration
}

func recorderProxyAddrFromEnv() string {
	return strings.TrimSpace(os.Getenv("APEXLOAD_RECORDER_PROXY_ADDR"))
}

type kubeRestarterAdapter struct {
	client *kubeops.Client
}

func (a kubeRestarterAdapter) DeletePod(ctx context.Context, namespace string, name string) error {
	return a.client.DeletePod(ctx, namespace, name)
}

func (a kubeRestarterAdapter) RolloutRestart(ctx context.Context, kind string, namespace string, name string) error {
	return a.client.RolloutRestart(ctx, kind, namespace, name)
}

func (a kubeRestarterAdapter) ScaleStatefulSet(ctx context.Context, namespace string, name string, replicas int) error {
	return a.client.ScaleStatefulSet(ctx, namespace, name, replicas)
}

func (a kubeRestarterAdapter) PodByIP(ctx context.Context, namespace string, selector string, ip string) (api.KubePodRef, error) {
	pod, err := a.client.PodByIP(ctx, namespace, selector, ip)
	return api.KubePodRef{Name: pod.Name, Namespace: pod.Namespace, IP: pod.IP, Phase: pod.Phase, Ready: pod.Ready, Terminating: pod.Terminating}, err
}

func (a kubeRestarterAdapter) ListPods(ctx context.Context, namespace string, selector string) ([]api.KubePodRef, error) {
	pods, err := a.client.ListPods(ctx, namespace, selector)
	if err != nil {
		return nil, err
	}
	out := make([]api.KubePodRef, 0, len(pods))
	for _, pod := range pods {
		out = append(out, api.KubePodRef{Name: pod.Name, Namespace: pod.Namespace, IP: pod.IP, Phase: pod.Phase, Ready: pod.Ready, Terminating: pod.Terminating})
	}
	return out, nil
}
