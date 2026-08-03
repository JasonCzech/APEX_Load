package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	cacheBackendMemory = "memory"
	cacheBackendRedis  = "redis"
	cacheBackendValkey = "valkey"

	defaultRedisKeyPrefix       = "apexload"
	defaultCacheProbeTimeout    = 2 * time.Second
	defaultCacheWriteTimeout    = 500 * time.Millisecond
	defaultRunSnapshotTTL       = 10 * time.Minute
	defaultGeneratorSnapshotTTL = 30 * time.Second
	defaultFanoutChannel        = "telemetry:fanout"
)

type CacheBackendInfo struct {
	Backend       string        `json:"backend"`
	KeyPrefix     string        `json:"key_prefix,omitempty"`
	RedisURLSet   bool          `json:"redis_url_set,omitempty"`
	TLSEnabled    bool          `json:"tls_enabled,omitempty"`
	Timeout       time.Duration `json:"timeout,omitempty"`
	ConfigError   string        `json:"config_error,omitempty"`
	FanoutChannel string        `json:"fanout_channel,omitempty"`
	// Endpoint is the credential-free scheme://host:port of a remote (Redis/Valkey)
	// backend, suitable for logging and operator-facing readiness details.
	Endpoint string `json:"endpoint,omitempty"`
}

// cacheBackendIsRemote reports whether a backend is a remote, Redis-wire-compatible
// store (Redis or Valkey) — the backends that support cross-controller fanout and
// must fail readiness hard when unreachable.
func cacheBackendIsRemote(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case cacheBackendRedis, cacheBackendValkey:
		return true
	default:
		return false
	}
}

type RunLiveStateSnapshot struct {
	TestID        string       `json:"test_id"`
	TestName      string       `json:"test_name"`
	ProjectID     string       `json:"project_id,omitempty"`
	Status        string       `json:"status"`
	UpdatedAt     time.Time    `json:"updated_at"`
	StartedAt     *time.Time   `json:"started_at,omitempty"`
	EndedAt       *time.Time   `json:"ended_at,omitempty"`
	LiveMetrics   *LiveMetrics `json:"live_metrics,omitempty"`
	HistoryPoints int          `json:"history_points"`
}

type LiveStateStore interface {
	SetRunSnapshot(ctx context.Context, snapshot RunLiveStateSnapshot, ttl time.Duration) error
	GetRunSnapshot(ctx context.Context, testID string) (*RunLiveStateSnapshot, error)
	SetGeneratorInventory(ctx context.Context, generators []GeneratorInventoryEntry, ttl time.Duration) error
	GetGeneratorInventory(ctx context.Context) ([]GeneratorInventoryEntry, error)
}

type IdempotencyStore interface {
	ReserveIdempotencyKey(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

// AtomicValueStore provides a cross-controller load-or-store operation. The
// first caller stores value for ttl; every concurrent or later caller receives
// the exact stored value until it expires. Implementations must perform the
// read and conditional write atomically.
type AtomicValueStore interface {
	LoadOrStoreValue(ctx context.Context, key string, value []byte, ttl time.Duration) (actual []byte, loaded bool, err error)
}

type RateLimitStore interface {
	AllowRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error)
}

type FanoutSubscription interface {
	Messages() <-chan []byte
	Close() error
}

type FanoutBus interface {
	Publish(ctx context.Context, channel string, payload []byte) error
	Subscribe(ctx context.Context, channel string) (FanoutSubscription, error)
}

type CacheBackend interface {
	LiveStateStore
	IdempotencyStore
	AtomicValueStore
	RateLimitStore
	FanoutBus
	Ping(ctx context.Context) error
	Info() CacheBackendInfo
	Close() error
}

type cacheRuntimeConfig struct {
	Backend       string
	RedisURL      string
	KeyPrefix     string
	TLSEnabled    bool
	Timeout       time.Duration
	DialTimeout   time.Duration
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	FanoutChannel string
}

func NewCacheBackendFromEnv() (CacheBackend, error) {
	return newCacheBackend(cacheRuntimeConfigFromEnv())
}

func configuredCacheBackend() CacheBackend {
	backend, err := NewCacheBackendFromEnv()
	if err != nil {
		cfg := cacheRuntimeConfigFromEnv()
		return &unavailableCacheBackend{
			info: CacheBackendInfo{
				Backend:       cfg.Backend,
				KeyPrefix:     cfg.KeyPrefix,
				RedisURLSet:   strings.TrimSpace(cfg.RedisURL) != "",
				TLSEnabled:    cfg.TLSEnabled,
				Timeout:       cfg.Timeout,
				ConfigError:   err.Error(),
				FanoutChannel: cfg.FanoutChannel,
			},
			err: err,
		}
	}
	return backend
}

func newCacheBackend(cfg cacheRuntimeConfig) (CacheBackend, error) {
	switch cfg.Backend {
	case "", cacheBackendMemory:
		if cfg.KeyPrefix == "" {
			cfg.KeyPrefix = defaultRedisKeyPrefix
		}
		return newMemoryCacheBackend(cfg), nil
	case cacheBackendRedis, cacheBackendValkey:
		if strings.TrimSpace(cfg.RedisURL) == "" {
			return nil, fmt.Errorf("cache backend %s requires APEXLOAD_REDIS_URL", cfg.Backend)
		}
		return newRedisCacheBackend(cfg)
	default:
		return nil, fmt.Errorf("unsupported cache backend %q", cfg.Backend)
	}
}

func cacheRuntimeConfigFromEnv() cacheRuntimeConfig {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_CACHE_BACKEND")))
	if backend == "" {
		backend = cacheBackendMemory
	}
	timeout := firstConfiguredDuration(defaultCacheProbeTimeout,
		"APEXLOAD_REDIS_TIMEOUT",
		"APEXLOAD_REDIS_TIMEOUT_SECONDS",
	)
	cfg := cacheRuntimeConfig{
		Backend:       backend,
		RedisURL:      strings.TrimSpace(os.Getenv("APEXLOAD_REDIS_URL")),
		KeyPrefix:     cleanCacheKeyPart(firstNonEmpty(os.Getenv("APEXLOAD_REDIS_KEY_PREFIX"), defaultRedisKeyPrefix)),
		TLSEnabled:    envBool("APEXLOAD_REDIS_TLS"),
		Timeout:       timeout,
		DialTimeout:   firstConfiguredDuration(timeout, "APEXLOAD_REDIS_DIAL_TIMEOUT", "APEXLOAD_REDIS_DIAL_TIMEOUT_SECONDS"),
		ReadTimeout:   firstConfiguredDuration(timeout, "APEXLOAD_REDIS_READ_TIMEOUT", "APEXLOAD_REDIS_READ_TIMEOUT_SECONDS"),
		WriteTimeout:  firstConfiguredDuration(timeout, "APEXLOAD_REDIS_WRITE_TIMEOUT", "APEXLOAD_REDIS_WRITE_TIMEOUT_SECONDS"),
		FanoutChannel: cleanCacheKeyPart(firstNonEmpty(os.Getenv("APEXLOAD_REDIS_FANOUT_CHANNEL"), defaultFanoutChannel)),
	}
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = defaultRedisKeyPrefix
	}
	if cfg.FanoutChannel == "" {
		cfg.FanoutChannel = defaultFanoutChannel
	}
	return cfg
}

func firstConfiguredDuration(fallback time.Duration, names ...string) time.Duration {
	for _, name := range names {
		raw := strings.TrimSpace(os.Getenv(name))
		if raw == "" {
			continue
		}
		if strings.HasSuffix(name, "_SECONDS") {
			seconds, err := strconv.ParseFloat(raw, 64)
			if err == nil && seconds > 0 {
				return time.Duration(seconds * float64(time.Second))
			}
			continue
		}
		parsed, err := time.ParseDuration(raw)
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func cleanCacheKeyPart(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, ":/")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func cacheReadinessDetails(info CacheBackendInfo) map[string]any {
	details := map[string]any{
		"cache_backend": info.Backend,
	}
	if info.KeyPrefix != "" {
		details["key_prefix"] = info.KeyPrefix
	}
	if info.FanoutChannel != "" {
		details["fanout_channel"] = info.FanoutChannel
	}
	if cacheBackendIsRemote(info.Backend) {
		details["redis_url_configured"] = info.RedisURLSet
		details["tls_enabled"] = info.TLSEnabled
		if info.Endpoint != "" {
			details["endpoint"] = info.Endpoint
		}
		if info.Timeout > 0 {
			details["timeout"] = info.Timeout.String()
		}
	}
	if info.ConfigError != "" {
		details["config_error"] = info.ConfigError
	}
	return details
}

type memoryCacheEntry struct {
	payload   []byte
	expiresAt time.Time
}

type memoryRateBucket struct {
	count   int
	resetAt time.Time
}

type memoryFanoutSubscription struct {
	backend *memoryCacheBackend
	channel string
	ch      chan []byte
	once    sync.Once
}

type memoryCacheBackend struct {
	mu          sync.Mutex
	entries     map[string]memoryCacheEntry
	rateLimits  map[string]memoryRateBucket
	subscribers map[string]map[chan []byte]struct{}
	info        CacheBackendInfo
	writes      uint64
}

func newMemoryCacheBackend(cfg cacheRuntimeConfig) *memoryCacheBackend {
	return &memoryCacheBackend{
		entries:     make(map[string]memoryCacheEntry),
		rateLimits:  make(map[string]memoryRateBucket),
		subscribers: make(map[string]map[chan []byte]struct{}),
		info: CacheBackendInfo{
			Backend:       cacheBackendMemory,
			KeyPrefix:     cfg.KeyPrefix,
			FanoutChannel: cfg.FanoutChannel,
		},
	}
}

func (m *memoryCacheBackend) Ping(context.Context) error {
	return nil
}

func (m *memoryCacheBackend) Info() CacheBackendInfo {
	return m.info
}

func (m *memoryCacheBackend) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for channel, subscribers := range m.subscribers {
		for ch := range subscribers {
			close(ch)
			delete(subscribers, ch)
		}
		delete(m.subscribers, channel)
	}
	return nil
}

func (m *memoryCacheBackend) SetRunSnapshot(ctx context.Context, snapshot RunLiveStateSnapshot, ttl time.Duration) error {
	if strings.TrimSpace(snapshot.TestID) == "" {
		return fmt.Errorf("run snapshot requires test id")
	}
	return m.setJSON(ctx, m.key("runs", snapshot.TestID, "snapshot"), snapshot, ttl)
}

func (m *memoryCacheBackend) GetRunSnapshot(ctx context.Context, testID string) (*RunLiveStateSnapshot, error) {
	var snapshot RunLiveStateSnapshot
	if err := m.getJSON(ctx, m.key("runs", testID, "snapshot"), &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (m *memoryCacheBackend) SetGeneratorInventory(ctx context.Context, generators []GeneratorInventoryEntry, ttl time.Duration) error {
	return m.setJSON(ctx, m.key("generators", "inventory"), generators, ttl)
}

func (m *memoryCacheBackend) GetGeneratorInventory(ctx context.Context) ([]GeneratorInventoryEntry, error) {
	var generators []GeneratorInventoryEntry
	if err := m.getJSON(ctx, m.key("generators", "inventory"), &generators); err != nil {
		return nil, err
	}
	return generators, nil
}

func (m *memoryCacheBackend) ReserveIdempotencyKey(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	key = m.key("idempotency", key)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepExpiredOnWriteLocked(now)
	if entry, ok := m.entries[key]; ok && (entry.expiresAt.IsZero() || entry.expiresAt.After(now)) {
		return false, nil
	}
	m.entries[key] = memoryCacheEntry{payload: []byte("reserved"), expiresAt: expiryForTTL(now, ttl)}
	return true, nil
}

func (m *memoryCacheBackend) LoadOrStoreValue(ctx context.Context, key string, value []byte, ttl time.Duration) ([]byte, bool, error) {
	if err := validateLoadOrStoreValueInput(ctx, key, ttl); err != nil {
		return nil, false, err
	}
	cacheKey := m.key("atomic", key)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepExpiredOnWriteLocked(now)
	if entry, ok := m.entries[cacheKey]; ok && (entry.expiresAt.IsZero() || entry.expiresAt.After(now)) {
		return append([]byte(nil), entry.payload...), true, nil
	}
	stored := append([]byte(nil), value...)
	m.entries[cacheKey] = memoryCacheEntry{payload: stored, expiresAt: expiryForTTL(now, ttl)}
	return append([]byte(nil), stored...), false, nil
}

func (m *memoryCacheBackend) AllowRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error) {
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}
	if limit <= 0 {
		return true, 0, nil
	}
	if window <= 0 {
		window = time.Minute
	}
	cacheKey := m.key("rate-limit", key)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepExpiredOnWriteLocked(now)
	bucket := m.rateLimits[cacheKey]
	if bucket.resetAt.IsZero() || !bucket.resetAt.After(now) {
		bucket = memoryRateBucket{resetAt: now.Add(window)}
	}
	bucket.count++
	m.rateLimits[cacheKey] = bucket
	remaining := limit - bucket.count
	if remaining < 0 {
		remaining = 0
	}
	return bucket.count <= limit, remaining, nil
}

func (m *memoryCacheBackend) Publish(ctx context.Context, channel string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	channel = m.key("fanout", cleanCacheKeyPart(channel))
	m.mu.Lock()
	subscribers := make([]chan []byte, 0, len(m.subscribers[channel]))
	for ch := range m.subscribers[channel] {
		subscribers = append(subscribers, ch)
	}
	m.mu.Unlock()
	for _, ch := range subscribers {
		msg := append([]byte(nil), payload...)
		select {
		case ch <- msg:
		default:
			slog.Warn("dropping memory fanout message for slow subscriber", "channel", channel)
		}
	}
	return nil
}

func (m *memoryCacheBackend) Subscribe(ctx context.Context, channel string) (FanoutSubscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	channel = m.key("fanout", cleanCacheKeyPart(channel))
	ch := make(chan []byte, 64)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.subscribers[channel] == nil {
		m.subscribers[channel] = make(map[chan []byte]struct{})
	}
	m.subscribers[channel][ch] = struct{}{}
	return &memoryFanoutSubscription{backend: m, channel: channel, ch: ch}, nil
}

func (m *memoryFanoutSubscription) Messages() <-chan []byte {
	return m.ch
}

func (m *memoryFanoutSubscription) Close() error {
	m.once.Do(func() {
		m.backend.mu.Lock()
		defer m.backend.mu.Unlock()
		if subscribers := m.backend.subscribers[m.channel]; subscribers != nil {
			if _, ok := subscribers[m.ch]; ok {
				delete(subscribers, m.ch)
				close(m.ch)
			}
			if len(subscribers) == 0 {
				delete(m.backend.subscribers, m.channel)
			}
		}
	})
	return nil
}

func (m *memoryCacheBackend) setJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepExpiredOnWriteLocked(now)
	m.entries[key] = memoryCacheEntry{payload: payload, expiresAt: expiryForTTL(now, ttl)}
	return nil
}

func (m *memoryCacheBackend) sweepExpiredOnWriteLocked(now time.Time) {
	m.writes++
	if m.writes%256 != 0 {
		return
	}
	for key, entry := range m.entries {
		if !entry.expiresAt.IsZero() && !entry.expiresAt.After(now) {
			delete(m.entries, key)
		}
	}
	for key, bucket := range m.rateLimits {
		if !bucket.resetAt.IsZero() && !bucket.resetAt.After(now) {
			delete(m.rateLimits, key)
		}
	}
}

func (m *memoryCacheBackend) getJSON(ctx context.Context, key string, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	m.mu.Lock()
	entry, ok := m.entries[key]
	if !ok || (!entry.expiresAt.IsZero() && !entry.expiresAt.After(now)) {
		if ok {
			delete(m.entries, key)
		}
		m.mu.Unlock()
		return fs.ErrNotExist
	}
	payload := append([]byte(nil), entry.payload...)
	m.mu.Unlock()
	return json.Unmarshal(payload, out)
}

func (m *memoryCacheBackend) key(parts ...string) string {
	return joinCacheKey(m.info.KeyPrefix, parts...)
}

type redisFanoutSubscription struct {
	pubsub *redis.PubSub
	ch     chan []byte
	once   sync.Once
}

type redisCacheBackend struct {
	client *redis.Client
	info   CacheBackendInfo
}

func newRedisCacheBackend(cfg cacheRuntimeConfig) (*redisCacheBackend, error) {
	options, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse APEXLOAD_REDIS_URL: %w", err)
	}
	if cfg.TLSEnabled && options.TLSConfig == nil {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if cfg.DialTimeout > 0 {
		options.DialTimeout = cfg.DialTimeout
	}
	if cfg.ReadTimeout > 0 {
		options.ReadTimeout = cfg.ReadTimeout
	}
	if cfg.WriteTimeout > 0 {
		options.WriteTimeout = cfg.WriteTimeout
	}
	tlsEnabled := cfg.TLSEnabled || options.TLSConfig != nil
	label := cacheBackendRedis
	if strings.EqualFold(strings.TrimSpace(cfg.Backend), cacheBackendValkey) {
		label = cacheBackendValkey
	}
	return &redisCacheBackend{
		client: redis.NewClient(options),
		info: CacheBackendInfo{
			Backend:       label,
			KeyPrefix:     cfg.KeyPrefix,
			RedisURLSet:   true,
			TLSEnabled:    tlsEnabled,
			Timeout:       cfg.Timeout,
			FanoutChannel: cfg.FanoutChannel,
			Endpoint:      redactRedisEndpoint(options.Addr, tlsEnabled),
		},
	}, nil
}

// redactRedisEndpoint returns a credential-free scheme://host:port locator for a
// Redis/Valkey server. go-redis' parsed Addr is already just host:port (no
// username/password), so this never leaks secrets.
func redactRedisEndpoint(addr string, tlsEnabled bool) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	scheme := "redis"
	if tlsEnabled {
		scheme = "rediss"
	}
	return scheme + "://" + addr
}

func (r *redisCacheBackend) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

func (r *redisCacheBackend) Info() CacheBackendInfo {
	return r.info
}

func (r *redisCacheBackend) Close() error {
	return r.client.Close()
}

func (r *redisCacheBackend) SetRunSnapshot(ctx context.Context, snapshot RunLiveStateSnapshot, ttl time.Duration) error {
	if strings.TrimSpace(snapshot.TestID) == "" {
		return fmt.Errorf("run snapshot requires test id")
	}
	return r.setJSON(ctx, r.key("runs", snapshot.TestID, "snapshot"), snapshot, ttl)
}

func (r *redisCacheBackend) GetRunSnapshot(ctx context.Context, testID string) (*RunLiveStateSnapshot, error) {
	var snapshot RunLiveStateSnapshot
	if err := r.getJSON(ctx, r.key("runs", testID, "snapshot"), &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r *redisCacheBackend) SetGeneratorInventory(ctx context.Context, generators []GeneratorInventoryEntry, ttl time.Duration) error {
	return r.setJSON(ctx, r.key("generators", "inventory"), generators, ttl)
}

func (r *redisCacheBackend) GetGeneratorInventory(ctx context.Context) ([]GeneratorInventoryEntry, error) {
	var generators []GeneratorInventoryEntry
	if err := r.getJSON(ctx, r.key("generators", "inventory"), &generators); err != nil {
		return nil, err
	}
	return generators, nil
}

func (r *redisCacheBackend) ReserveIdempotencyKey(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return r.client.SetNX(ctx, r.key("idempotency", key), "reserved", ttl).Result()
}

var redisLoadOrStoreValueScript = redis.NewScript(`
local existing = redis.call("GET", KEYS[1])
if existing then
  return {existing, 1}
end
redis.call("PSETEX", KEYS[1], ARGV[2], ARGV[1])
return {ARGV[1], 0}
`)

func (r *redisCacheBackend) LoadOrStoreValue(ctx context.Context, key string, value []byte, ttl time.Duration) ([]byte, bool, error) {
	if err := validateLoadOrStoreValueInput(ctx, key, ttl); err != nil {
		return nil, false, err
	}
	ttlMillis := ttl / time.Millisecond
	if ttl%time.Millisecond != 0 {
		ttlMillis++
	}
	values, err := redisLoadOrStoreValueScript.Run(ctx, r.client, []string{r.key("atomic", key)}, value, int64(ttlMillis)).Slice()
	if err != nil {
		return nil, false, err
	}
	if len(values) != 2 {
		return nil, false, fmt.Errorf("redis atomic load-or-store returned %d values, want 2", len(values))
	}
	actual, err := redisResultBytes(values[0])
	if err != nil {
		return nil, false, err
	}
	loaded, err := redisResultBool(values[1])
	if err != nil {
		return nil, false, err
	}
	return actual, loaded, nil
}

func (r *redisCacheBackend) AllowRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error) {
	if limit <= 0 {
		return true, 0, nil
	}
	if window <= 0 {
		window = time.Minute
	}
	cacheKey := r.key("rate-limit", key)
	count, err := r.client.Incr(ctx, cacheKey).Result()
	if err != nil {
		return false, 0, err
	}
	if count == 1 {
		if err := r.client.Expire(ctx, cacheKey, window).Err(); err != nil {
			return false, 0, err
		}
	}
	remaining := limit - int(count)
	if remaining < 0 {
		remaining = 0
	}
	return count <= int64(limit), remaining, nil
}

func (r *redisCacheBackend) Publish(ctx context.Context, channel string, payload []byte) error {
	return r.client.Publish(ctx, r.key("fanout", cleanCacheKeyPart(channel)), payload).Err()
}

func (r *redisCacheBackend) Subscribe(ctx context.Context, channel string) (FanoutSubscription, error) {
	pubsub := r.client.Subscribe(ctx, r.key("fanout", cleanCacheKeyPart(channel)))
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return nil, err
	}
	sub := &redisFanoutSubscription{
		pubsub: pubsub,
		ch:     make(chan []byte, 64),
	}
	go sub.run()
	return sub, nil
}

func (r *redisFanoutSubscription) run() {
	defer close(r.ch)
	for message := range r.pubsub.Channel() {
		if message == nil {
			continue
		}
		payload := []byte(message.Payload)
		select {
		case r.ch <- payload:
		default:
			slog.Warn("dropping redis fanout message for slow subscriber", "channel", message.Channel)
		}
	}
}

func (r *redisFanoutSubscription) Messages() <-chan []byte {
	return r.ch
}

func (r *redisFanoutSubscription) Close() error {
	var err error
	r.once.Do(func() {
		err = r.pubsub.Close()
	})
	return err
}

func (r *redisCacheBackend) setJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return r.client.Set(ctx, key, payload, ttl).Err()
}

func (r *redisCacheBackend) getJSON(ctx context.Context, key string, out any) error {
	payload, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return fs.ErrNotExist
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, out)
}

func (r *redisCacheBackend) key(parts ...string) string {
	return joinCacheKey(r.info.KeyPrefix, parts...)
}

type unavailableCacheBackend struct {
	info CacheBackendInfo
	err  error
}

func (u *unavailableCacheBackend) Ping(context.Context) error {
	return u.err
}

func (u *unavailableCacheBackend) Info() CacheBackendInfo {
	return u.info
}

func (u *unavailableCacheBackend) Close() error {
	return nil
}

func (u *unavailableCacheBackend) SetRunSnapshot(context.Context, RunLiveStateSnapshot, time.Duration) error {
	return u.err
}

func (u *unavailableCacheBackend) GetRunSnapshot(context.Context, string) (*RunLiveStateSnapshot, error) {
	return nil, u.err
}

func (u *unavailableCacheBackend) SetGeneratorInventory(context.Context, []GeneratorInventoryEntry, time.Duration) error {
	return u.err
}

func (u *unavailableCacheBackend) GetGeneratorInventory(context.Context) ([]GeneratorInventoryEntry, error) {
	return nil, u.err
}

func (u *unavailableCacheBackend) ReserveIdempotencyKey(context.Context, string, time.Duration) (bool, error) {
	return false, u.err
}

func (u *unavailableCacheBackend) LoadOrStoreValue(context.Context, string, []byte, time.Duration) ([]byte, bool, error) {
	return nil, false, u.err
}

func (u *unavailableCacheBackend) AllowRateLimit(context.Context, string, int, time.Duration) (bool, int, error) {
	return false, 0, u.err
}

func (u *unavailableCacheBackend) Publish(context.Context, string, []byte) error {
	return u.err
}

func (u *unavailableCacheBackend) Subscribe(context.Context, string) (FanoutSubscription, error) {
	return nil, u.err
}

func expiryForTTL(now time.Time, ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return now.Add(ttl)
}

func validateLoadOrStoreValueInput(ctx context.Context, key string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("atomic load-or-store requires a key")
	}
	if ttl <= 0 {
		return fmt.Errorf("atomic load-or-store requires a positive TTL")
	}
	return nil
}

func redisResultBytes(value any) ([]byte, error) {
	switch typed := value.(type) {
	case string:
		return []byte(typed), nil
	case []byte:
		return append([]byte(nil), typed...), nil
	default:
		return nil, fmt.Errorf("redis atomic load-or-store returned an invalid value type %T", value)
	}
}

func redisResultBool(value any) (bool, error) {
	switch typed := value.(type) {
	case int64:
		return typed != 0, nil
	case int:
		return typed != 0, nil
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return false, fmt.Errorf("parse redis atomic load-or-store flag: %w", err)
		}
		return parsed != 0, nil
	default:
		return false, fmt.Errorf("redis atomic load-or-store returned an invalid flag type %T", value)
	}
}

func joinCacheKey(prefix string, parts ...string) string {
	cleaned := []string{}
	if part := cleanCacheKeyPart(prefix); part != "" {
		cleaned = append(cleaned, part)
	}
	for _, part := range parts {
		part = cleanCacheKeyPart(part)
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return strings.Join(cleaned, ":")
}
