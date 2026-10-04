package collectoragent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// ManagementTarget is local operator approval. It is not executable code and
// cannot make an unimplemented provider pack available.
type ManagementTarget struct {
	Scope              analysis.ManagementScope      `json:"scope"`
	Identity           analysis.ManagementIdentity   `json:"identity"`
	Endpoints          []string                      `json:"endpoints"`
	TLSCAFile          string                        `json:"tls_ca_file,omitempty"`
	MinIntervalSeconds int                           `json:"min_interval_seconds,omitempty"`
	Kafka              *KafkaManagementSettings      `json:"kafka,omitempty"`
	ServiceBus         *ServiceBusManagementSettings `json:"service_bus,omitempty"`
	kafkaApproval      *kafkaManagementApproval
	serviceBusApproval *serviceBusManagementApproval
}

func (t ManagementTarget) clone() ManagementTarget {
	t.Scope = t.Scope.Clone()
	t.Endpoints = append([]string(nil), t.Endpoints...)
	t.Kafka = t.Kafka.clone()
	t.kafkaApproval = t.kafkaApproval.clone()
	t.ServiceBus = t.ServiceBus.clone()
	t.serviceBusApproval = t.serviceBusApproval.clone()
	return t
}
func managementKind(pack string) string {
	switch pack {
	case "kafka-offsets":
		return "kafka_offsets"
	case "servicebus-runtime":
		return "servicebus_runtime"
	case "solace-semp":
		return "solace_semp"
	}
	return ""
}
func validateManagementTarget(pack string, t *ManagementTarget) error {
	if t == nil || t.Scope.Kind != managementKind(pack) {
		return errors.New("management target kind mismatch")
	}
	if err := t.Scope.Validate(); err != nil {
		return err
	}
	if err := t.Identity.Validate(); err != nil {
		return err
	}
	if len(t.Endpoints) < 1 || len(t.Endpoints) > 16 {
		return errors.New("management target requires finite approved endpoints")
	}
	seen := map[string]bool{}
	for _, endpoint := range t.Endpoints {
		u, err := url.Parse(endpoint)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" || len(endpoint) > 2048 || !managementLabel(endpoint, 2048) || u.Scheme != "https" && u.Scheme != "tls" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
			return errors.New("invalid management endpoint")
		}
		if u.Scheme == "tls" && (u.Path != "" || u.Port() == "") {
			return errors.New("Kafka management endpoint requires an exact TLS broker port")
		}
		if seen[endpoint] {
			return errors.New("duplicate management endpoint")
		}
		seen[endpoint] = true
	}
	if t.MinIntervalSeconds < 0 || t.MinIntervalSeconds > 3600 || t.MinIntervalSeconds > 0 && t.MinIntervalSeconds < 10 {
		return errors.New("management cadence exceeds bounds")
	}
	if t.TLSCAFile != "" {
		info, err := os.Stat(t.TLSCAFile)
		if !filepath.IsAbs(t.TLSCAFile) || err != nil || !info.Mode().IsRegular() {
			return errors.New("management trust file unavailable")
		}
	}
	return nil
}
func managementMinimumInterval(pack string, t ManagementTarget) int {
	minimum := 10
	if pack == "solace-semp" || pack == "servicebus-runtime" {
		minimum = 30
	}
	return max(minimum, t.MinIntervalSeconds)
}
func newCollectorInstanceID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

// Observer implementations are compiled, fixed management readers. Startup
// preparation may read approved local files; validation is pure and mandatory.
// Controlled observers exist only in package-local tests.
type managementObserver interface {
	prepareTarget(*ManagementTarget) error
	validateTarget(*ManagementTarget) error
	observe(context.Context, *managementObservation) (*analysis.MessagingManagementSnapshot, error)
}

func registeredManagementObservers() map[string]managementObserver {
	return map[string]managementObserver{"kafka-offsets": kafkaManagementObserver{}, "servicebus-runtime": serviceBusManagementObserver{}}
}
func ManagementPacks() []string {
	packs := []string{}
	for pack, observer := range registeredManagementObservers() {
		if observer != nil {
			packs = append(packs, pack)
		}
	}
	sort.Strings(packs)
	return packs
}

type managementObservation struct {
	target          ManagementTarget
	grant           ManagementPollGrant
	credentials     ManagementCredentials
	mu              sync.Mutex
	requests, pages int
	closed          bool
	calls           sync.WaitGroup
	requestSlots    chan struct{}
	origins         *managementOriginGate
}

func newManagementObservation(target ManagementTarget, grant ManagementPollGrant, credentials ManagementCredentials) *managementObservation {
	return &managementObservation{target: target.clone(), grant: grant, credentials: credentials, requestSlots: make(chan struct{}, 2)}
}

// A context timer can be queued after its actual deadline. Signed authority
// cannot depend on that timer being scheduled before admission or publication.
func managementContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !deadline.After(time.Now()) {
		return context.DeadlineExceeded
	}
	return nil
}

func managementPollAuthorityError(ctx context.Context, grant ManagementPollGrant) error {
	if grant.Deadline.IsZero() || grant.AuthorizationExpiresAt.IsZero() {
		return &managementObservationError{reason: "permission_denied"}
	}
	if err := managementContextError(ctx); err != nil {
		return err
	}
	now := time.Now()
	if !grant.Deadline.After(now) || !grant.AuthorizationExpiresAt.After(now) {
		return context.DeadlineExceeded
	}
	return nil
}

// request bounds every actual provider operation and holds admission until the
// callback really returns, even when a transport ignores its cancellation.
// The observer supplies only a destination already present in local approval.
func (p *managementObservation) request(ctx context.Context, endpoint string, page bool, call func(context.Context) error) error {
	// Scheduling and signed authorization are independent lifetimes. Derive the
	// actual operation deadline before queue/origin admission, so waiting cannot
	// consume authority and then start target work using an expired credential.
	if err := managementPollAuthorityError(ctx, p.grant); err != nil {
		return err
	}
	deadline := minTime(minTime(p.grant.Deadline, p.grant.AuthorizationExpiresAt), time.Now().Add(5*time.Second))
	budget, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := managementPollAuthorityError(budget, p.grant); err != nil {
		return err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("management poll is completed")
	}
	p.calls.Add(1)
	p.mu.Unlock()
	defer p.calls.Done()
	approved := false
	for _, known := range p.target.Endpoints {
		approved = approved || endpoint == known
	}
	if !approved || call == nil {
		return errors.New("management destination is not approved")
	}
	select {
	case p.requestSlots <- struct{}{}:
	case <-budget.Done():
		return managementContextError(budget)
	}
	defer func() { <-p.requestSlots }()
	if err := managementPollAuthorityError(budget, p.grant); err != nil {
		return err
	}
	if p.origins != nil {
		release, err := p.origins.admit(budget, endpoint, p.grant.PollID, p.grant.Pack)
		if err != nil {
			return err
		}
		defer release()
	}
	if err := managementPollAuthorityError(budget, p.grant); err != nil {
		return err
	}
	p.mu.Lock()
	if p.requests >= 512 || page && p.pages >= 16 {
		p.mu.Unlock()
		return &managementBudgetError{}
	}
	p.requests++
	if page {
		p.pages++
	}
	p.mu.Unlock()
	if err := managementPollAuthorityError(budget, p.grant); err != nil {
		return err
	}
	err := call(budget)
	if authorityErr := managementPollAuthorityError(budget, p.grant); authorityErr != nil {
		return authorityErr
	}
	return err
}

// retainResource keeps physical ownership charged after a request callback
// returns. The returned once-only release must run only after actual closure
// proof; cancellation or an unexplained Close error does not discharge it.
func (p *managementObservation) retainResource() (func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("management poll is completed")
	}
	p.calls.Add(1)
	var once sync.Once
	return func() { once.Do(p.calls.Done) }, nil
}

type managementBudgetError struct{}

func (*managementBudgetError) Error() string { return "management request budget exhausted" }

func (p *managementObservation) finish() { p.mu.Lock(); p.closed = true; p.mu.Unlock(); p.calls.Wait() }
func (p *managementObservation) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests, p.pages
}
func (p *managementObservation) base(started, completed time.Time, elapsed time.Duration, interval int, missed int64) analysis.MessagingManagementSnapshot {
	ms := max(int64(0), elapsed.Milliseconds())
	return analysis.MessagingManagementSnapshot{Version: 1, Kind: p.target.Scope.Kind, Provider: p.target.Scope.Provider, ScopeHash: p.grant.ScopeHash, IdentityHash: p.grant.IdentityHash, SourceID: p.grant.SourceID, SourceRevision: p.grant.SourceRevision, CollectorID: p.grant.CollectorID, CollectorInstanceID: p.grant.CollectorInstanceID, Generation: p.grant.Generation, PollID: p.grant.PollID, PollSequence: p.grant.PollSequence, StartedAt: started.UTC(), CompletedAt: completed.UTC(), CapturedAt: completed.UTC(), ElapsedMS: &ms, IntervalSeconds: interval, MissedTicks: decimalNonnegative(missed), ExpectedEntities: len(p.target.Scope.Entities), Rows: []analysis.MessagingManagementRow{}}
}
func decimalNonnegative(n int64) string {
	if n < 0 {
		n = 0
	}
	return strconv.FormatInt(n, 10)
}

func managementAlias(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
