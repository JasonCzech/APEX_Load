package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/debugserver"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/kubeops"
	_ "github.com/jasonczech/apexload/pkg/protocol/builtin"
	"github.com/jasonczech/apexload/pkg/runtimetuning"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/jasonczech/apexload/pkg/telemetry"
)

func main() {
	loadLocalDotEnv()
	logLevel, logLevelErr := config.ResolveLogLevel(os.Getenv)
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))
	if logLevelErr != nil {
		slog.Warn("invalid log-level configuration; using info", "err", logLevelErr)
	}
	if err := runController(); err != nil {
		slog.Error("controller stopped with an error", "err", err)
		os.Exit(1)
	}
}

func runController() (runErr error) {
	loadLocalDotEnv()
	if err := validatePaaSControllerConfig(os.Getenv); err != nil {
		return err
	}
	if applied, value, err := applyTLSCompatibilityDefaults(os.Getenv, os.Setenv); err != nil {
		slog.Warn("failed to apply TLS compatibility defaults", "err", err)
	} else if applied {
		slog.Warn("applied TLS compatibility defaults", "godebug", value)
	}
	logRuntimeTuning(runtimetuning.ApplyFromCgroup(os.Getenv))
	build := buildinfo.Get()
	tracingConfig, err := telemetry.LoadConfig(os.Getenv, "apexload-controller", build.Version)
	if err != nil {
		return fmt.Errorf("configure OpenTelemetry tracing: %w", err)
	}
	tracingRuntime, err := telemetry.Configure(context.Background(), tracingConfig)
	if err != nil {
		return fmt.Errorf("initialize OpenTelemetry tracing: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tracingRuntime.Shutdown(shutdownCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("flush OpenTelemetry tracing: %w", err))
		}
	}()
	if status := tracingRuntime.Status(); status.Ready {
		slog.Info("OpenTelemetry tracing enabled", "service", status.ServiceName, "protocol", status.Protocol, "sampler", status.Sampler)
	}

	// Optional pprof debug server (disabled unless APEXLOAD_PPROF_ADDR is set).
	if pprofSrv, err := debugserver.StartPprofFromEnv(os.Getenv); err != nil {
		slog.Error("failed to start pprof debug server", "err", err)
	} else if pprofSrv != nil {
		defer pprofSrv.Close()
	}

	// Controller HTTP bind address (for API + embedded dashboard).
	addr, bindScope := config.ResolveControllerBindAddr(os.Getenv)
	slog.Info("controller bind address resolved", "addr", addr, "bind_scope", bindScope)

	generatorTargets := parseGeneratorTargets(os.Getenv("APEXLOAD_GENERATORS"))
	generatorTimeout := parseGeneratorTimeout(os.Getenv("APEXLOAD_GENERATOR_TIMEOUT"))
	runSupervisionTTL, runSupervisionRenew, runSupervisionErr := parseGeneratorRunSupervision(os.Getenv)
	if runSupervisionErr != nil {
		return runSupervisionErr
	}
	if len(generatorTargets) > 0 {
		security := generatorrpc.TransportSecurityFromEnv()
		if err := validateGeneratorTransportStartup(generatorTargets, os.Getenv("APEXLOAD_ENV"), security); err != nil {
			return fmt.Errorf("refusing to start with unsafe generator gRPC transport: %w", err)
		}
		// Validate certificate/key files and token-only production requirements at
		// startup rather than discovering a broken trust boundary when the first
		// distributed run is already staging.
		if _, err := generatorrpc.DialOptionsFromEnv(); err != nil {
			return fmt.Errorf("invalid generator gRPC transport configuration: %w", err)
		}
		slog.Info("distributed execution enabled", "generators", generatorTargets, "timeout", generatorTimeout, "grpc_transport", security.String())
	}

	storeConfig := storage.StoreConfigFromEnv()
	if err := validateHAStoreStartup(envIsTruthy(os.Getenv("APEXLOAD_CONTROLLER_HA")), storeConfig); err != nil {
		return err
	}
	if warning := ignoredDatabaseURLWarning(storeConfig); warning != "" {
		slog.Warn(warning, "store", storeConfig.Backend)
	}
	store, err := storage.NewConfiguredStore(storeConfig)
	if err != nil {
		return fmt.Errorf("initialize storage: %w", err)
	}
	defer store.Close()

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
	haLeaderElection := envIsTruthy(os.Getenv("APEXLOAD_CONTROLLER_HA"))
	if haLeaderElection {
		slog.Info("HA execution leader election enabled; only the lease holder will run tests")
	}

	activeEndpointPublisher, err := activeEndpointPublisherFromEnv(haLeaderElection, addr)
	if err != nil {
		return fmt.Errorf("configure active controller endpoint routing: %w", err)
	}
	importAssetDir, err := importAssetDirFromEnv()
	if err != nil {
		return err
	}

	server := api.NewServer(api.ServerConfig{
		Addr:                         addr,
		BindScope:                    bindScope,
		MetricsAddr:                  strings.TrimSpace(os.Getenv("APEXLOAD_METRICS_ADDR")),
		MetricsToken:                 strings.TrimSpace(os.Getenv("APEXLOAD_METRICS_TOKEN")),
		ProxyAddr:                    recorderProxyAddr,
		MaxConcurrent:                10,
		Store:                        store,
		GeneratorTargets:             generatorTargets,
		EnableDynamicGenerators:      dynamicGeneratorsEnabled(os.Getenv),
		PaaSComputeEnabled:           envIsTruthy(os.Getenv("APEXLOAD_PAAS_COMPUTE_ENABLED")),
		GeneratorTimeout:             generatorTimeout,
		GeneratorRunSupervisionTTL:   runSupervisionTTL,
		GeneratorRunSupervisionRenew: runSupervisionRenew,
		ImportAssetDir:               importAssetDir,
		BrunoArtifactDir:             brunoArtifactDirFromEnv(),
		K8sRestart:                   k8sRestartSettings,
		KubeRestarter:                restarter,
		HAExecutionLeadership:        haLeaderElection,
		ActiveEndpointPublisher:      activeEndpointPublisher,
		TracingStatus:                tracingRuntime.Status(),
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := server.StartScheduler(ctx); err != nil {
		return fmt.Errorf("start scenario scheduler: %w", err)
	}
	var background sync.WaitGroup
	background.Add(2)
	go func() {
		defer background.Done()
		if waitForControllerListener(ctx, server, 25*time.Millisecond) {
			server.RunExecutionLeadership(ctx)
		}
	}()
	go func() {
		defer background.Done()
		server.RunGeneratorPodTerminationWatcher(ctx, 5*time.Second)
	}()

	runErr = serveController(ctx, server, 30*time.Second)
	// A Serve failure is also a shutdown signal for the leadership, scheduler,
	// and pod-watcher loops. Wait for them before the deferred storage close so
	// none can continue using the database after cleanup.
	stop()
	workersDone := make(chan struct{})
	go func() {
		background.Wait()
		close(workersDone)
	}()
	select {
	case <-workersDone:
	case <-time.After(30 * time.Second):
		runErr = errors.Join(runErr, fmt.Errorf("controller background workers did not stop within 30s"))
	}
	return runErr
}

func dynamicGeneratorsEnabled(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	if envIsTruthy(getenv("APEXLOAD_DYNAMIC_GENERATORS")) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(getenv("APEXLOAD_ENVIRONMENT"))) {
	case "azure_container_apps", "aws_ecs_fargate":
		return true
	default:
		return false
	}
}

func validatePaaSControllerConfig(getenv func(string) string) error {
	if getenv == nil {
		getenv = os.Getenv
	}
	deployment := strings.ToLower(strings.TrimSpace(getenv("APEXLOAD_ENVIRONMENT")))
	if deployment != "azure_container_apps" && deployment != "aws_ecs_fargate" {
		return nil
	}
	if envIsTruthy(getenv("APEXLOAD_CONTROLLER_HA")) {
		return fmt.Errorf("APEXLOAD_CONTROLLER_HA is not supported for %s; the initial PaaS release requires one orchestrator/gateway replica", deployment)
	}
	role := strings.ToLower(strings.TrimSpace(getenv("APEXLOAD_CONTROLLER_ROLE")))
	if role != "" && role != "all" {
		return fmt.Errorf("APEXLOAD_CONTROLLER_ROLE=%s is not available for %s yet; use all with a single replica", role, deployment)
	}
	storeBackend := strings.ToLower(strings.TrimSpace(getenv("APEXLOAD_STORE")))
	if storeBackend != storage.StoreBackendPostgres && storeBackend != "postgresql" {
		return fmt.Errorf("%s requires APEXLOAD_STORE=postgres", deployment)
	}
	if strings.TrimSpace(getenv("APEXLOAD_DATABASE_URL")) == "" {
		return fmt.Errorf("%s requires APEXLOAD_DATABASE_URL", deployment)
	}
	if strings.TrimSpace(getenv("APEXLOAD_REDIS_URL")) == "" {
		return fmt.Errorf("%s requires APEXLOAD_REDIS_URL", deployment)
	}
	cacheBackend := strings.ToLower(strings.TrimSpace(getenv("APEXLOAD_CACHE_BACKEND")))
	if cacheBackend != "redis" && cacheBackend != "valkey" {
		return fmt.Errorf("%s requires APEXLOAD_CACHE_BACKEND=redis or valkey", deployment)
	}
	archiveBackend := strings.ToLower(strings.TrimSpace(getenv("APEXLOAD_ARCHIVE_BACKEND")))
	if archiveBackend != "s3" && archiveBackend != "azure_blob" && archiveBackend != "gcs" {
		return fmt.Errorf("%s requires APEXLOAD_ARCHIVE_BACKEND=s3, azure_blob, or gcs so archives and import assets survive controller replacement", deployment)
	}
	return nil
}

type controllerLifecycle interface {
	Start() error
	Shutdown(context.Context) error
}

type controllerListenerState interface {
	BoundAddrReady() bool
}

// waitForControllerListener keeps HA leadership and its active EndpointSlice
// unpublished until the API listener has successfully bound. If startup fails,
// serveController cancels ctx and the leadership worker exits without ever
// advertising an unreachable pod.
func waitForControllerListener(ctx context.Context, server controllerListenerState, pollInterval time.Duration) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if server == nil {
		return false
	}
	if pollInterval <= 0 {
		pollInterval = 25 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if server.BoundAddrReady() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// serveController runs the blocking HTTP server until either a process signal
// cancels ctx or Serve fails. Both paths converge on the same coordinated
// shutdown, so fatal listener errors cannot bypass deferred storage/debug-server
// cleanup via os.Exit from a child goroutine.
func serveController(ctx context.Context, server controllerLifecycle, shutdownTimeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if shutdownTimeout <= 0 {
		shutdownTimeout = 30 * time.Second
	}
	startDone := make(chan error, 1)
	go func() { startDone <- server.Start() }()

	var startErr error
	startObserved := false
	select {
	case <-ctx.Done():
		slog.Info("shutting down")
	case startErr = <-startDone:
		startObserved = true
		if startErr != nil {
			startErr = fmt.Errorf("server: %w", startErr)
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("server shutdown: %w", shutdownErr)
	}
	// When signal cancellation won, wait for Start to observe Shutdown. The
	// buffered channel also lets a late Serve return exit without leaking a
	// sender when the shutdown deadline expires.
	if !startObserved {
		select {
		case err := <-startDone:
			if err != nil {
				startErr = fmt.Errorf("server: %w", err)
			}
		case <-shutdownCtx.Done():
			startErr = shutdownCtx.Err()
		}
	}
	return errors.Join(startErr, shutdownErr)
}

func validateGeneratorTransportStartup(targets []string, appEnv string, security generatorrpc.TransportSecurity) error {
	if len(targets) == 0 {
		return nil
	}
	if security.AuthenticatedTunnel && !(security.TLS && (security.MutualTLS || security.Token)) {
		for _, target := range targets {
			if _, reverse := generatorrpc.ReverseGeneratorID(target); !reverse {
				return fmt.Errorf("authenticated WSS tunnel security applies only to reverse generator targets; direct target %q requires gRPC mTLS or verified TLS plus %s", target, "APEXLOAD_GRPC_AUTH_TOKEN")
			}
		}
	}
	if !security.MustRefuseInsecure(appEnv) {
		return nil
	}
	return fmt.Errorf("production distributed execution requires mTLS or verified TLS plus %s; configured %s", "APEXLOAD_GRPC_AUTH_TOKEN", security.String())
}
func logRuntimeTuning(settings runtimetuning.Settings) {
	if settings.GOMAXPROCS == 0 && settings.GOMEMLIMIT == 0 {
		return
	}
	args := []any{}
	if settings.GOMAXPROCS > 0 {
		args = append(args, "gomaxprocs", settings.GOMAXPROCS, "gomaxprocs_source", settings.GOMAXPROCSSource)
	}
	if settings.GOMEMLIMIT > 0 {
		args = append(args, "gomemlimit", settings.GOMEMLIMIT, "gomemlimit_source", settings.GOMEMLIMITSource)
	}
	slog.Info("go runtime tuning applied", args...)
}

// validateHAStoreStartup refuses HA on a per-replica store. The execution
// lease lives in the store, so with SQLite every replica leases against its own
// file, each believes it is the leader, and they run tests concurrently.
func validateHAStoreStartup(haEnabled bool, config storage.StoreConfig) error {
	if !haEnabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(config.Backend)) {
	case storage.StoreBackendPostgres, "postgresql":
		return nil
	}
	return fmt.Errorf("APEXLOAD_CONTROLLER_HA=true requires APEXLOAD_STORE=postgres; store %q is per-replica, so every replica would hold its own execution lease and run tests concurrently", strings.TrimSpace(config.Backend))
}

// ignoredDatabaseURLWarning reports a database URL that the selected store
// will never use: the backend is chosen by APEXLOAD_STORE, not by the URL.
func ignoredDatabaseURLWarning(config storage.StoreConfig) string {
	if strings.TrimSpace(config.DatabaseURL) == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(config.Backend)) {
	case storage.StoreBackendPostgres, "postgresql":
		return ""
	}
	return "APEXLOAD_DATABASE_URL is set but ignored because APEXLOAD_STORE is not postgres; set APEXLOAD_STORE=postgres to use it"
}

func envIsTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

func applyTLSCompatibilityDefaults(getenv func(string) string, setenv func(string, string) error) (bool, string, error) {
	if getenv == nil || setenv == nil {
		return false, "", nil
	}
	if envIsTruthy(getenv("APEXLOAD_DISABLE_TLS_COMPAT")) || envIsTruthy(getenv("APEXLOAD_DISABLE_DEV_TLS_COMPAT")) {
		return false, "", nil
	}
	appEnv := strings.ToLower(strings.TrimSpace(getenv("APEXLOAD_ENV")))
	if (appEnv == "production" || appEnv == "prod") && !envIsTruthy(getenv("APEXLOAD_ENABLE_TLS_COMPAT")) {
		return false, "", nil
	}
	required := map[string]string{
		"x509negativeserial": "1",
		"tlsmlkem":           "0",
		"tlssecpmlkem":       "0",
	}
	existingRaw := strings.TrimSpace(getenv("GODEBUG"))
	entries := []string{}
	seen := map[string]struct{}{}
	if existingRaw != "" {
		for _, token := range strings.Split(existingRaw, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			entries = append(entries, token)
			key := strings.ToLower(strings.TrimSpace(token))
			if idx := strings.Index(token, "="); idx >= 0 {
				key = strings.ToLower(strings.TrimSpace(token[:idx]))
			}
			seen[key] = struct{}{}
		}
	}
	changed := false
	for key, value := range required {
		if _, ok := seen[key]; ok {
			continue
		}
		entries = append(entries, key+"="+value)
		changed = true
	}
	if !changed {
		return false, strings.Join(entries, ","), nil
	}
	next := strings.Join(entries, ",")
	if err := setenv("GODEBUG", next); err != nil {
		return false, "", err
	}
	return true, next, nil
}

func loadLocalDotEnv() {
	appEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_ENV")))
	if appEnv == "production" || appEnv == "prod" {
		return
	}
	if err := config.LoadDotEnv(".env"); err != nil {
		if os.IsNotExist(err) {
			return
		}
		slog.Warn("failed to load local .env", "err", err)
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

func parseGeneratorRunSupervision(getenv func(string) string) (time.Duration, time.Duration, error) {
	ttl := 60 * time.Second
	renew := 10 * time.Second
	if raw := strings.TrimSpace(getenv("APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL: %w", err)
		}
		ttl = parsed
	}
	if raw := strings.TrimSpace(getenv("APEXLOAD_GENERATOR_RUN_SUPERVISION_RENEW_INTERVAL")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid APEXLOAD_GENERATOR_RUN_SUPERVISION_RENEW_INTERVAL: %w", err)
		}
		renew = parsed
	}
	if ttl < 30*time.Second || ttl > 60*time.Second {
		return 0, 0, fmt.Errorf("APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL must be between 30s and 60s")
	}
	if renew < time.Second || renew > ttl/3 {
		return 0, 0, fmt.Errorf("APEXLOAD_GENERATOR_RUN_SUPERVISION_RENEW_INTERVAL must be at least 1s and no greater than one-third of the TTL")
	}
	return ttl, renew, nil
}

func recorderProxyAddrFromEnv() string {
	return strings.TrimSpace(os.Getenv("APEXLOAD_RECORDER_PROXY_ADDR"))
}

func brunoArtifactDirFromEnv() string {
	return strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_ARTIFACT_ROOT"))
}

func importAssetDirFromEnv() (string, error) {
	dir := strings.TrimSpace(os.Getenv("APEXLOAD_IMPORT_ASSET_DIR"))
	if dir != "" {
		return dir, nil
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APEXLOAD_ENV")), "production") {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_BACKEND"))) {
		case "s3", "azure_blob", "gcs":
			// Import assets share the configured object backend and retain their
			// metadata/CAS state in PostgreSQL; no RWX filesystem is required.
			return "", nil
		}
		return "", fmt.Errorf("APEXLOAD_IMPORT_ASSET_DIR is required in production; import assets must not use the temporary default")
	}
	return "", nil
}

func activeEndpointPublisherFromEnv(haEnabled bool, bindAddr string) (api.ExecutionEndpointPublisher, error) {
	sliceName := strings.TrimSpace(os.Getenv("APEXLOAD_ACTIVE_ENDPOINT_SLICE"))
	if sliceName == "" {
		return nil, nil
	}
	if !haEnabled {
		return nil, fmt.Errorf("APEXLOAD_ACTIVE_ENDPOINT_SLICE requires APEXLOAD_CONTROLLER_HA=true")
	}
	namespace := strings.TrimSpace(os.Getenv("APEXLOAD_POD_NAMESPACE"))
	podName := strings.TrimSpace(os.Getenv("APEXLOAD_POD_NAME"))
	podIP := strings.TrimSpace(os.Getenv("APEXLOAD_POD_IP"))
	serviceName := strings.TrimSpace(os.Getenv("APEXLOAD_ACTIVE_ENDPOINT_SERVICE"))
	if serviceName == "" {
		serviceName = sliceName
	}
	tokenFile := strings.TrimSpace(os.Getenv("APEXLOAD_ACTIVE_ENDPOINT_TOKEN_FILE"))
	caFile := strings.TrimSpace(os.Getenv("APEXLOAD_ACTIVE_ENDPOINT_CA_FILE"))
	client, err := kubeops.NewInClusterClient(kubeops.Config{
		Namespace: namespace,
		TokenFile: tokenFile,
		CAFile:    caFile,
	})
	if err != nil {
		return nil, err
	}
	endpointPort, err := controllerEndpointPort(bindAddr)
	if err != nil {
		return nil, err
	}
	return kubeops.NewActiveEndpointPublisher(client, kubeops.ActiveEndpointConfig{
		Namespace:         namespace,
		EndpointSliceName: sliceName,
		ServiceName:       serviceName,
		PodName:           podName,
		PodIP:             podIP,
		PortName:          "http",
		Port:              endpointPort,
	})
}

func controllerEndpointPort(bindAddr string) (int32, error) {
	_, portText, err := net.SplitHostPort(strings.TrimSpace(bindAddr))
	if err != nil {
		return 0, fmt.Errorf("resolve controller endpoint port from bind address %q: %w", bindAddr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("controller bind address %q has an invalid port", bindAddr)
	}
	return int32(port), nil
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
	return api.KubePodRef{UID: pod.UID, NodeName: pod.NodeName, Name: pod.Name, Namespace: pod.Namespace, IP: pod.IP, Phase: pod.Phase, Ready: pod.Ready, Terminating: pod.Terminating}, err
}

func (a kubeRestarterAdapter) ListPods(ctx context.Context, namespace string, selector string) ([]api.KubePodRef, error) {
	pods, err := a.client.ListPods(ctx, namespace, selector)
	if err != nil {
		return nil, err
	}
	out := make([]api.KubePodRef, 0, len(pods))
	for _, pod := range pods {
		out = append(out, api.KubePodRef{UID: pod.UID, NodeName: pod.NodeName, Name: pod.Name, Namespace: pod.Namespace, IP: pod.IP, Phase: pod.Phase, Ready: pod.Ready, Terminating: pod.Terminating})
	}
	return out, nil
}
