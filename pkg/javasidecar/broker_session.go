package javasidecar

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/javarunner/broker"
)

var ErrBrokerSession = errors.New("java_broker_session_unusable")

// DedicatedBrokerSession is the typed execution boundary of the local facade.
// It owns one already-reserved dedicated host, not a generator owner or wave.
// The caller must stage/commit the wave and drive Renew from this Vuser's actual
// runtime authority; this object never manufactures a background lease.
// It does not itself expose HTTP, select a profile, or start user initialization.
type DedicatedBrokerSession struct {
	wave          *broker.BrokerClientWave
	host, session string
	mu            sync.Mutex
	sequence      uint64
	prefix        string
	terminal      bool
	busy          bool
	ending        bool
}

func NewDedicatedBrokerSession(w *broker.BrokerClientWave, host string) (*DedicatedBrokerSession, error) {
	if w == nil {
		return nil, ErrBrokerSession
	}
	for _, h := range w.Hosts() {
		if h.HostID == host {
			var nonce [16]byte
			if _, e := rand.Read(nonce[:]); e != nil {
				return nil, ErrBrokerSession
			}
			return &DedicatedBrokerSession{wave: w, host: host, session: h.SessionID, prefix: "broker-" + hex.EncodeToString(nonce[:])}, nil
		}
	}
	return nil, ErrBrokerSession
}
func (s *DedicatedBrokerSession) HostID() string               { return s.host }
func (s *DedicatedBrokerSession) SessionID() string            { return s.session }
func (s *DedicatedBrokerSession) LeaseInterval() time.Duration { return s.wave.HostLeaseInterval() }

// Err reports a terminal local session decision; it is not a remote health probe.
func (s *DedicatedBrokerSession) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return ErrBrokerSession
	}
	return nil
}
func (s *DedicatedBrokerSession) Renew(ctx context.Context) error {
	s.mu.Lock()
	terminal := s.terminal
	s.mu.Unlock()
	if terminal {
		return ErrBrokerSession
	}
	if e := s.wave.RenewHost(ctx, s.host); e != nil {
		s.mu.Lock()
		if !s.ending {
			s.terminal = true
		}
		s.mu.Unlock()
		return e
	}
	return nil
}
func (s *DedicatedBrokerSession) fail() { s.mu.Lock(); s.terminal = true; s.mu.Unlock() }
func (s *DedicatedBrokerSession) Close(ctx context.Context) error {
	s.fail()
	return s.wave.CloseHost(ctx, s.host)
}
func (s *DedicatedBrokerSession) Execute(ctx context.Context, method string, params map[string]string) (ExecutionResponse, error) {
	s.mu.Lock()
	// Generate fresh correlation identities without an unbounded replay map.
	// The broker independently enforces at-most-once per-host sequences.
	if s.terminal || s.busy || s.sequence == ^uint64(0) || method != "init" && method != "action" && method != "end" {
		s.mu.Unlock()
		return ExecutionResponse{}, ErrBrokerSession
	}
	s.sequence++
	id := s.prefix + "-" + strconv.FormatUint(s.sequence, 10)
	s.busy = true
	s.ending = method == "end"
	s.mu.Unlock()
	raw, e := s.wave.Invoke(ctx, s.host, method, params)
	var out ExecutionResponse
	if e == nil {
		out, e = translateBrokerResult(raw, id, s.session)
	}
	s.mu.Lock()
	s.busy = false
	if s.terminal && e == nil {
		e = ErrBrokerSession
	}
	if e != nil || method == "end" {
		s.terminal = true
	}
	s.mu.Unlock()
	if e != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.wave.CloseHost(cleanup, s.host)
		cancel()
		return ExecutionResponse{}, e
	}
	return out, nil
}

// The outer broker client has authenticated the exact host, epoch, profile and
// monotonic invocation. Translate only the structured response/event identities;
// stdout/stderr are user bytes and must never be blindly rewritten. Stderr
// fallback events are copied into the bounded event stream, preserving stderr.
func translateBrokerResult(raw broker.SupervisorResponse, id, session string) (ExecutionResponse, error) {
	var out ExecutionResponse
	if !executionIDPattern.MatchString(id) || !executionIDPattern.MatchString(session) || raw.InvocationID == "" || len(raw.Payload) > broker.MaxSupervisorPayload || !utf8.Valid(raw.Payload) {
		return out, ErrBrokerSession
	}
	dec := json.NewDecoder(bytes.NewReader(raw.Payload))
	dec.DisallowUnknownFields()
	if dec.Decode(&out) != nil || dec.Decode(new(any)) != io.EOF || out.Contract != ContractVersion || out.ExecutionID != raw.InvocationID || out.DurationMS < 0 || out.DurationMS > MaximumExecutionTimeout.Milliseconds()+10000 || len(out.WarningCodes) != 0 || (out.Success && (out.ErrorCode != "" || out.Error != "")) || (!out.Success && out.ErrorCode != "harness_reported_failure") {
		return ExecutionResponse{}, ErrBrokerSession
	}
	for _, text := range []string{out.Stdout, out.Stderr, out.Events, out.Error} {
		if len(text) > MaxCaptureBytes || !utf8.ValidString(text) {
			return ExecutionResponse{}, ErrBrokerSession
		}
	}
	events := map[int64]map[string]json.RawMessage{}
	add := func(line string, strict bool) error {
		if strings.TrimSpace(line) == "" {
			return nil
		}
		var value map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &value) != nil {
			if strict {
				return ErrBrokerSession
			}
			return nil
		}
		var contract, request string
		_ = json.Unmarshal(value["contract"], &contract)
		_ = json.Unmarshal(value["request_id"], &request)
		if contract != harnessEventContract || request != raw.InvocationID {
			// Background work from a previous invocation is never attributed
			// to the current Vuser action, matching dedicated IPC v2 filtering.
			return nil
		}
		var seq int64
		var kind string
		if json.Unmarshal(value["sequence"], &seq) != nil || seq <= 0 || json.Unmarshal(value["type"], &kind) != nil || strings.TrimSpace(kind) == "" {
			return ErrBrokerSession
		}
		// The protocol decoder must not silently drop malformed structured
		// timings or exit records after we have accepted the invocation.
		for _, key := range []string{"name", "message", "transaction_id", "parent_transaction_id"} {
			if field, ok := value[key]; ok {
				var text string
				if bytes.Equal(field, []byte("null")) || json.Unmarshal(field, &text) != nil {
					return ErrBrokerSession
				}
			}
		}
		for _, key := range []string{"duration_ms", "raw_duration_ms", "think_time_ms", "wasted_time_ms", "timestamp_ms"} {
			if field, ok := value[key]; ok {
				var number int64
				if bytes.Equal(field, []byte("null")) || json.Unmarshal(field, &number) != nil || number < 0 {
					return ErrBrokerSession
				}
				if key != "timestamp_ms" && number > MaximumExecutionTimeout.Milliseconds()+2000 {
					return ErrBrokerSession
				}
			}
		}
		if previous := events[seq]; previous != nil {
			a, _ := json.Marshal(previous)
			b, _ := json.Marshal(value)
			if !bytes.Equal(a, b) {
				return ErrBrokerSession
			}
		}
		events[seq] = value
		return nil
	}
	lines := strings.Split(out.Events, "\n")
	for i, line := range lines {
		if i == len(lines)-1 && line != "" && out.EventsTruncated {
			break
		}
		if e := add(line, true); e != nil {
			return ExecutionResponse{}, e
		}
	}
	// Incomplete fallback lines have no complete attributable event. An existing
	// truncation flag remains observable even if the file had complete records.
	for _, line := range strings.Split(out.Stderr, "\n") {
		if e := add(line, false); e != nil {
			return ExecutionResponse{}, e
		}
	}
	seqs := make([]int64, 0, len(events))
	for seq := range events {
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	var capture strings.Builder
	for _, seq := range seqs {
		value := events[seq]
		value["request_id"], _ = json.Marshal(id)
		for _, key := range []string{"transaction_id", "parent_transaction_id"} {
			if v, ok := value[key]; ok {
				var old string
				if json.Unmarshal(v, &old) != nil {
					return ExecutionResponse{}, ErrBrokerSession
				}
				if old != "" {
					value[key], _ = json.Marshal(session + ":" + id + ":" + old)
				}
			}
		}
		line, e := json.Marshal(value)
		if e != nil {
			return ExecutionResponse{}, ErrBrokerSession
		}
		if capture.Len()+len(line)+1 > MaxCaptureBytes {
			out.EventsTruncated = true
			break
		}
		capture.Write(line)
		capture.WriteByte('\n')
	}
	out.ExecutionID = id
	out.Events = capture.String()
	out.EventsTruncated = out.EventsTruncated || out.StderrTruncated
	return out, nil
}
