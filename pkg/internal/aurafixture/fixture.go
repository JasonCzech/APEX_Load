// Package aurafixture supplies a bounded, loopback-only synthetic React/Aura
// test target. It is not a Salesforce implementation or device qualification.
package aurafixture

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

//go:embed testdata/react-aura.html
var reactPage []byte

const (
	maxAssetBytes    = 2 << 20
	maxRequestBytes  = 64 << 10
	maxActions       = 16
	maxSessions      = 128
	maxRetainedIDs   = 8192
	maxResponseDelay = 2 * time.Second
)

type Scenario string

const (
	Success    Scenario = "SUCCESS"
	Error      Scenario = "ERROR"
	Incomplete Scenario = "INCOMPLETE"
	Missing    Scenario = "missing"
	Duplicate  Scenario = "duplicate"
)

type Options struct {
	ReactJS       []byte
	ReactDOMJS    []byte
	Scenario      Scenario
	ResponseDelay time.Duration
}

// Stats intentionally exports counts only. Accepted batches passed session and
// request validation; their selected application result may still be a failure.
type Stats struct {
	BootstrapRequests  int `json:"bootstrap_requests"`
	ColdBootstraps     int `json:"cold_bootstraps"`
	WarmBootstraps     int `json:"warm_bootstraps"`
	AcceptedBatches    int `json:"accepted_batches"`
	RejectedBatches    int `json:"rejected_batches"`
	ScopeRejections    int `json:"scope_rejections"`
	ReusedIDRejections int `json:"reused_id_rejections"`
	LimitRejections    int `json:"limit_rejections"`
	RequestActions     int `json:"request_actions"`
	ResponseActions    int `json:"response_actions"`
	SuccessActions     int `json:"success_actions"`
	ErrorActions       int `json:"error_actions"`
	IncompleteActions  int `json:"incomplete_actions"`
	MissingActions     int `json:"missing_actions"`
	DuplicateActions   int `json:"duplicate_actions"`
	ColdBatches        int `json:"cold_batches"`
	WarmBatches        int `json:"warm_batches"`
	DistinctSessions   int `json:"distinct_sessions"`
	DistinctTokens     int `json:"distinct_tokens"`
	DistinctActionIDs  int `json:"distinct_action_ids"`
	TransportFailures  int `json:"transport_failures"`
}

type session struct {
	token      string
	nonce      string
	fwuid      string
	nextAction int
	batches    int
	seenIDs    map[string]struct{}
}

type Fixture struct {
	mu         sync.Mutex
	reactJS    []byte
	reactDOMJS []byte
	scenario   Scenario
	delay      time.Duration
	sessions   map[string]*session
	stats      Stats
}

type Context struct {
	FWUID string `json:"fwuid"`
	Mode  string `json:"mode"`
	App   string `json:"app"`
}

// Bootstrap values are supplied directly to the test client. They never enter
// Stats or retained evidence and remain confined to the synthetic target.
type Bootstrap struct {
	Token          string   `json:"token"`
	Context        Context  `json:"context"`
	Nonce          string   `json:"nonce"`
	ActionIDs      []string `json:"action_ids"`
	ActionSequence int      `json:"action_sequence"`
	ClientState    string   `json:"client_state"`
}

type requestAction struct {
	ID         string `json:"id"`
	Descriptor string `json:"descriptor"`
}

type responseAction struct {
	ID          string         `json:"id"`
	State       string         `json:"state"`
	ReturnValue map[string]any `json:"returnValue,omitempty"`
}

func New(opts Options) (*Fixture, error) {
	if len(opts.ReactJS) == 0 || len(opts.ReactDOMJS) == 0 || len(opts.ReactJS) > maxAssetBytes || len(opts.ReactDOMJS) > maxAssetBytes {
		return nil, errors.New("synthetic React assets are missing or exceed fixture bounds")
	}
	if opts.ResponseDelay < 0 || opts.ResponseDelay > maxResponseDelay {
		return nil, errors.New("synthetic response delay exceeds fixture bounds")
	}
	if opts.Scenario == "" {
		opts.Scenario = Success
	}
	if !validScenario(opts.Scenario) {
		return nil, errors.New("unsupported synthetic Aura scenario")
	}
	return &Fixture{reactJS: append([]byte(nil), opts.ReactJS...), reactDOMJS: append([]byte(nil), opts.ReactDOMJS...), scenario: opts.Scenario, delay: opts.ResponseDelay, sessions: make(map[string]*session)}, nil
}

func validScenario(scenario Scenario) bool {
	return scenario == Success || scenario == Error || scenario == Incomplete || scenario == Missing || scenario == Duplicate
}

func (f *Fixture) SetScenario(scenario Scenario) error {
	if !validScenario(scenario) {
		return errors.New("unsupported synthetic Aura scenario")
	}
	f.mu.Lock()
	f.scenario = scenario
	f.mu.Unlock()
	return nil
}

func (f *Fixture) Stats() Stats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stats
}

func (f *Fixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(remote) == nil || !net.ParseIP(remote).IsLoopback() || !loopbackHost(r.Host) {
		http.Error(w, "synthetic fixture requires loopback", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/aura" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		f.aura(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(reactPage)
	case "/react.js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(f.reactJS)
	case "/react-dom.js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(f.reactDOMJS)
	case "/bootstrap":
		f.bootstrap(w, r)
	default:
		http.NotFound(w, r)
	}
}

func loopbackHost(host string) bool {
	if value, _, err := net.SplitHostPort(host); err == nil {
		host = value
	}
	host = strings.Trim(host, "[]")
	return strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

func freshValue() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", errors.New("synthetic fixture random source unavailable")
	}
	return "synthetic-" + base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func (f *Fixture) bootstrap(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats.BootstrapRequests++
	cookie, _ := r.Cookie("synthetic_aura_session")
	key := ""
	if cookie != nil {
		key = cookie.Value
	}
	current := f.sessions[key]
	state := "warm"
	if current == nil {
		if len(f.sessions) >= maxSessions {
			f.stats.LimitRejections++
			writeError(w, http.StatusTooManyRequests, "fixture_capacity")
			return
		}
		values := make([]string, 4)
		for i := range values {
			var err error
			values[i], err = freshValue()
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "fixture_unavailable")
				return
			}
		}
		key = values[0]
		current = &session{token: values[1], nonce: values[2], fwuid: values[3], nextAction: 1, seenIDs: make(map[string]struct{})}
		f.sessions[key] = current
		f.stats.ColdBootstraps++
		f.stats.DistinctSessions++
		f.stats.DistinctTokens++
		state = "cold"
	} else {
		f.stats.WarmBootstraps++
	}
	sequence := current.nextAction
	ids := []string{actionID(current.nonce, sequence), actionID(current.nonce, sequence+1)}
	current.nextAction += 2
	http.SetCookie(w, &http.Cookie{Name: "synthetic_aura_session", Value: key, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Bootstrap{Token: current.token, Context: Context{FWUID: current.fwuid, Mode: "PROD", App: "synthetic:reactAura"}, Nonce: current.nonce, ActionIDs: ids, ActionSequence: sequence, ClientState: state})
}

func actionID(nonce string, sequence int) string {
	return nonce + "-" + strconv.Itoa(sequence) + ";a"
}

func (f *Fixture) reject(w http.ResponseWriter, status int, code string, scope, reused bool) {
	f.mu.Lock()
	f.stats.RejectedBatches++
	if scope {
		f.stats.ScopeRejections++
	}
	if reused {
		f.stats.ReusedIDRejections++
	}
	f.mu.Unlock()
	writeError(w, status, code)
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (f *Fixture) aura(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		f.reject(w, http.StatusBadRequest, "invalid_form", false, false)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes {
		f.reject(w, http.StatusRequestEntityTooLarge, "request_bound", false, false)
		return
	}
	if !aura.ValidateRequest(body).Success {
		f.reject(w, http.StatusBadRequest, "invalid_envelope", false, false)
		return
	}
	values, err := url.ParseQuery(string(body))
	if err != nil || len(values["message"]) != 1 || len(values["aura.token"]) != 1 || len(values["aura.context"]) != 1 {
		f.reject(w, http.StatusBadRequest, "invalid_form", false, false)
		return
	}
	var envelope struct {
		Actions []requestAction `json:"actions"`
	}
	var context Context
	contextJSON := values.Get("aura.context")
	// Apply the shared strict JSON checks to context as nested synthetic data,
	// so duplicate members cannot make session binding depend on decoder order.
	contextEnvelope := []byte(`{"actions":[{"id":"context","descriptor":"synthetic","params":` + contextJSON + `}]}`)
	if json.Unmarshal([]byte(values.Get("message")), &envelope) != nil || len(envelope.Actions) > maxActions || !aura.ValidateRequest(contextEnvelope).Success || json.Unmarshal([]byte(contextJSON), &context) != nil {
		f.reject(w, http.StatusBadRequest, "invalid_envelope", false, false)
		return
	}
	cookie, _ := r.Cookie("synthetic_aura_session")
	f.mu.Lock()
	current := (*session)(nil)
	if cookie != nil {
		current = f.sessions[cookie.Value]
	}
	if current == nil || current.token != values.Get("aura.token") || current.fwuid != context.FWUID || context.Mode != "PROD" || context.App != "synthetic:reactAura" {
		f.mu.Unlock()
		f.reject(w, http.StatusUnauthorized, "session_scope", true, false)
		return
	}
	for _, action := range envelope.Actions {
		if !strings.HasPrefix(action.ID, current.nonce+"-") {
			f.mu.Unlock()
			f.reject(w, http.StatusUnauthorized, "action_scope", true, false)
			return
		}
		if _, reused := current.seenIDs[action.ID]; reused {
			f.mu.Unlock()
			f.reject(w, http.StatusConflict, "action_reused", false, true)
			return
		}
	}
	if len(envelope.Actions) > maxRetainedIDs-f.stats.DistinctActionIDs {
		f.stats.LimitRejections++
		f.mu.Unlock()
		f.reject(w, http.StatusTooManyRequests, "fixture_capacity", false, false)
		return
	}
	for _, action := range envelope.Actions {
		current.seenIDs[action.ID] = struct{}{}
		if tail, found := strings.CutPrefix(action.ID, current.nonce+"-"); found {
			if sequence, err := strconv.Atoi(strings.TrimSuffix(tail, ";a")); err == nil && sequence >= current.nextAction && sequence < int(^uint(0)>>1)-1 {
				current.nextAction = sequence + 1
			}
		}
	}
	if current.batches == 0 {
		f.stats.ColdBatches++
	} else {
		f.stats.WarmBatches++
	}
	current.batches++
	f.stats.AcceptedBatches++
	f.stats.RequestActions += len(envelope.Actions)
	f.stats.DistinctActionIDs += len(envelope.Actions)
	scenario, delay := f.scenario, f.delay
	f.mu.Unlock()

	responses := make([]responseAction, 0, len(envelope.Actions))
	failureOrdinal := len(envelope.Actions) - 1
	for index := len(envelope.Actions) - 1; index >= 0; index-- {
		if scenario == Missing && index == failureOrdinal {
			continue
		}
		state := "SUCCESS"
		if index == failureOrdinal && (scenario == Error || scenario == Incomplete) {
			state = string(scenario)
		}
		responses = append(responses, responseAction{ID: envelope.Actions[index].ID, State: state, ReturnValue: map[string]any{"synthetic": true}})
	}
	if scenario == Duplicate && len(responses) > 0 {
		responses = append(responses, responses[0])
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			f.mu.Lock()
			f.stats.TransportFailures++
			f.mu.Unlock()
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(map[string]any{"actions": responses})
	written, writeErr := w.Write(data)
	f.mu.Lock()
	defer f.mu.Unlock()
	if writeErr != nil || written != len(data) {
		f.stats.TransportFailures++
		return
	}
	f.stats.ResponseActions += len(responses)
	for _, action := range responses {
		switch action.State {
		case "SUCCESS":
			f.stats.SuccessActions++
		case "ERROR":
			f.stats.ErrorActions++
		case "INCOMPLETE":
			f.stats.IncompleteActions++
		}
	}
	if scenario == Missing {
		f.stats.MissingActions++
	}
	if scenario == Duplicate {
		f.stats.DuplicateActions++
	}
}

// LoadReactDependencies reads only the known installed React18 UMD files. Each
// asset is bounded and no package manager, CDN or network request is involved.
func LoadReactDependencies(dashboardDir string) ([]byte, []byte, error) {
	read := func(relative string) ([]byte, error) {
		file, err := os.Open(filepath.Join(dashboardDir, "node_modules", relative))
		if err != nil {
			return nil, errors.New("installed synthetic React dependency unavailable")
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxAssetBytes {
			return nil, errors.New("installed synthetic React dependency exceeds fixture bounds")
		}
		data, err := io.ReadAll(io.LimitReader(file, maxAssetBytes+1))
		if err != nil || len(data) == 0 || len(data) > maxAssetBytes {
			return nil, errors.New("installed synthetic React dependency exceeds fixture bounds")
		}
		return data, nil
	}
	react, err := read("react/umd/react.production.min.js")
	if err != nil {
		return nil, nil, err
	}
	dom, err := read("react-dom/umd/react-dom.production.min.js")
	return react, dom, err
}
