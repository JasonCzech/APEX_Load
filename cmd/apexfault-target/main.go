// Command apexfault-target serves independent traffic counters for disposable
// fault trials. It is not a production load benchmark or an assignment authority.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type targetConfig struct {
	RunID         string `json:"run_id"`
	LoadToken     string `json:"load_token"`
	ObserverToken string `json:"observer_token"`
}

var runPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)
var testIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,252}$`)

const (
	maxTestCounters       = 2000
	maxGenerationsPerTest = 64
)

func readConfig(path string) (targetConfig, error) {
	var c targetConfig
	if !filepath.IsAbs(path) {
		return c, errors.New("config must be an absolute file path")
	}
	f, err := os.Open(path)
	if err != nil {
		return c, errors.New("cannot open target config")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16384 {
		return c, errors.New("config must be a regular file no larger than 16 KiB")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 16385))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return targetConfig{}, errors.New("invalid target config")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return targetConfig{}, errors.New("invalid target config")
	}
	if !runPattern.MatchString(c.RunID) || !tokenPattern.MatchString(c.LoadToken) || !tokenPattern.MatchString(c.ObserverToken) || c.LoadToken == c.ObserverToken {
		return targetConfig{}, errors.New("config requires a fault run ID and distinct 32–128 character URL-safe tokens")
	}
	return c, nil
}

type target struct {
	config           targetConfig
	instance         string
	started          float64
	mu               sync.Mutex
	counts           map[string]uint64
	perGeneration    map[string]map[uint64]uint64
	highestGen       map[string]uint64
	duplicateOverlap map[string]uint64
}

func unixNow() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func newTarget(c targetConfig) (*target, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, errors.New("cannot generate target instance identity")
	}
	return &target{
		config:           c,
		instance:         hex.EncodeToString(id[:]),
		started:          unixNow(),
		counts:           make(map[string]uint64),
		perGeneration:    make(map[string]map[uint64]uint64),
		highestGen:       make(map[string]uint64),
		duplicateOverlap: make(map[string]uint64),
	}, nil
}

func requestedTestID(r *http.Request) (string, bool) {
	values := r.Header.Values("X-Apexload-Fault-Test")
	if len(values) != 1 || !testIDPattern.MatchString(values[0]) {
		return "", false
	}
	return values[0], true
}

func requestedGeneration(r *http.Request) (uint64, bool, bool) {
	values := r.Header.Values("X-Apexload-Fault-Generation")
	if len(values) == 0 {
		return 0, false, true
	}
	if len(values) != 1 {
		return 0, false, false
	}
	gen, err := strconv.ParseUint(values[0], 10, 64)
	if err != nil || gen == 0 {
		return 0, false, false
	}
	return gen, true, true
}

func authorized(r *http.Request, token string) bool {
	values := r.Header.Values("Authorization")
	return len(values) == 1 && subtle.ConstantTimeCompare([]byte(values[0]), []byte("Bearer "+token)) == 1
}

func (t *target) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		http.Error(w, "query parameters are not supported", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/healthz":
		w.WriteHeader(http.StatusNoContent)
	case "/load":
		if !authorized(r, t.config.LoadToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ids := r.Header.Values("X-Apexload-Fault-Run")
		if len(ids) != 1 || ids[0] != t.config.RunID {
			http.Error(w, "run mismatch", http.StatusConflict)
			return
		}
		testID, ok := requestedTestID(r)
		if !ok {
			http.Error(w, "invalid test identity", http.StatusBadRequest)
			return
		}
		gen, hasGen, ok := requestedGeneration(r)
		if !ok {
			http.Error(w, "invalid generation identity", http.StatusBadRequest)
			return
		}
		t.mu.Lock()
		count, exists := t.counts[testID]
		if (!exists && len(t.counts) >= maxTestCounters) || count == ^uint64(0) {
			t.mu.Unlock()
			http.Error(w, "counter unavailable", http.StatusServiceUnavailable)
			return
		}
		if hasGen {
			genMap := t.perGeneration[testID]
			if genMap == nil {
				genMap = make(map[uint64]uint64)
				t.perGeneration[testID] = genMap
			}
			genCount, genExists := genMap[gen]
			if (!genExists && len(genMap) >= maxGenerationsPerTest) || genCount == ^uint64(0) {
				t.mu.Unlock()
				http.Error(w, "counter unavailable", http.StatusServiceUnavailable)
				return
			}
			highest := t.highestGen[testID]
			if gen < highest && t.duplicateOverlap[testID] == ^uint64(0) {
				t.mu.Unlock()
				http.Error(w, "counter unavailable", http.StatusServiceUnavailable)
				return
			}
			genMap[gen] = genCount + 1
			if gen > highest {
				t.highestGen[testID] = gen
			} else if gen < highest {
				t.duplicateOverlap[testID]++
			}
		}
		t.counts[testID] = count + 1
		t.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case "/snapshot":
		if !authorized(r, t.config.ObserverToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		testID, ok := requestedTestID(r)
		if !ok {
			http.Error(w, "invalid test identity", http.StatusBadRequest)
			return
		}
		t.mu.Lock()
		var perGen map[string]uint64
		if genMap := t.perGeneration[testID]; len(genMap) > 0 {
			perGen = make(map[string]uint64, len(genMap))
			for g, n := range genMap {
				perGen[strconv.FormatUint(g, 10)] = n
			}
		}
		snapshot := struct {
			SchemaVersion              int               `json:"schema_version"`
			RunID                      string            `json:"run_id"`
			TestID                     string            `json:"test_id"`
			TargetInstanceID           string            `json:"target_instance_id"`
			StartedAt                  float64           `json:"started_at_unix"`
			ObservedAt                 float64           `json:"observed_at_unix"`
			TargetRequests             uint64            `json:"target_requests"`
			PerGenerationRequests      map[string]uint64 `json:"per_generation_requests,omitempty"`
			HighestGeneration          uint64            `json:"highest_generation,omitempty"`
			DuplicateGenerationOverlap uint64            `json:"duplicate_generation_overlap,omitempty"`
		}{2, t.config.RunID, testID, t.instance, t.started, unixNow(), t.counts[testID], perGen, t.highestGen[testID], t.duplicateOverlap[testID]}
		t.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot)
	default:
		http.NotFound(w, r)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "observe" {
		return observeCommand(ctx, args[1:], os.Stdout)
	}
	flags := flag.NewFlagSet("apexfault-target", flag.ContinueOnError)
	configPath := flags.String("config", "", "absolute private target.json path")
	listen := flags.String("listen", "127.0.0.1:8090", "listen address; use an explicit interface for a disposable cluster")
	cert := flags.String("tls-cert", "", "optional TLS certificate file (requires tls-key)")
	key := flags.String("tls-key", "", "optional TLS key file (requires tls-cert)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*cert == "") != (*key == "") {
		return errors.New("unexpected arguments or incomplete TLS configuration")
	}
	c, err := readConfig(*configPath)
	if err != nil {
		return err
	}
	handler, err := newTarget(c)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return errors.New("target listener failed")
	}
	return serveTarget(ctx, server, listener, *cert, *key)
}

func serveTarget(ctx context.Context, server *http.Server, listener net.Listener, cert, key string) error {
	defer listener.Close()
	result := make(chan error, 1)
	go func() {
		if cert != "" {
			result <- server.ServeTLS(listener, cert, key)
		} else {
			result <- server.Serve(listener)
		}
	}()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("target listener failed")
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
		<-result
		return nil
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "apexfault-target:", err)
		os.Exit(1)
	}
}
