// Package broker contains the durable control-plane primitives for dedicated
// Java execution pods. It does not enable Kubernetes execution by itself.
package broker

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	_ "modernc.org/sqlite"
)

var (
	ErrState      = errors.New("java_broker_state_invalid")
	ErrFence      = errors.New("java_broker_epoch_fenced")
	ErrCapacity   = errors.New("java_broker_capacity_unavailable")
	ErrCleanup    = errors.New("java_broker_cleanup_unproven")
	name          = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	namespaceName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
)

const maxRecords = 10000
const maxDocument = 16 << 20

// Config is immutable for a store. Keep the database on broker-owned persistent
// storage outside execution pods. SQLite requires a filesystem with reliable
// locking and fsync; this is a single-volume authority, not a multi-region store.
type Config struct {
	Namespace string
	Budget    javarunner.HostBudget
}

// Record represents one dedicated JVM and its full memory reservation. PodName
// is allocated before provisioning; creation MUST NOT precede BeginPrepare.
// An empty PodUID after BeginPrepare means an unknown outcome, never no process.
type Record struct {
	PodPolicySHA256              string `json:",omitempty"`
	SessionID                    string `json:",omitempty"`
	ID, BrokerEpoch              string
	Binding                      javarunner.HostBinding
	State                        javarunner.HostState
	MemoryBytes                  int64
	VUserOccupied                bool
	PodName, PodUID, ContainerID string
	LeaseUntil, PrepareUntil     time.Time
	Failure                      string
}

type document struct {
	Version int
	Config  Config
	Epoch   string
	Records map[string]Record
}

type Store struct {
	db     *sql.DB
	config Config
	now    func() time.Time
}

// Open opens an existing authority or explicitly initializes a new one. A
// missing database during recovery is an error: never silently replace lost
// quarantine state. Creation must only be used for a new, empty execution scope.
func Open(ctx context.Context, path string, cfg Config, create bool) (*Store, error) {
	if !filepath.IsAbs(path) || !namespaceName.MatchString(cfg.Namespace) || cfg.Budget.MaxHosts < 1 || cfg.Budget.MaxHosts > 10000 || cfg.Budget.MaxVUsers < 1 || cfg.Budget.MaxVUsers > 10000 || cfg.Budget.MaxMemoryBytes <= 0 {
		return nil, ErrState
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	if !create {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, ErrState
		}
	}
	file, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || !info.Mode().IsRegular() {
		return nil, ErrState
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := u.Query()
	q.Set("mode", "rw")
	q.Set("_txlock", "immediate")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, config: cfg, now: time.Now}
	fail := func(err error) (*Store, error) { _ = db.Close(); return nil, err }
	if create {
		err := func() error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err = tx.ExecContext(ctx, "CREATE TABLE java_broker_state (singleton INTEGER PRIMARY KEY CHECK(singleton=1), document BLOB NOT NULL)"); err != nil {
				return err
			}
			data, _ := json.Marshal(document{Version: 1, Config: cfg, Records: map[string]Record{}})
			if _, err = tx.ExecContext(ctx, "INSERT INTO java_broker_state VALUES (1, ?)", data); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err != nil {
			return fail(err)
		}
	}
	if err := s.update(ctx, "", false, func(*document) error { return nil }); err != nil {
		return fail(err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// update serializes across connections/processes with BEGIN IMMEDIATE. Nothing
// returned by a mutation is permission to act until its durable commit succeeds.
func (s *Store) update(ctx context.Context, epoch string, fence bool, fn func(*document) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var data []byte
	if err = tx.QueryRowContext(ctx, "SELECT CASE WHEN length(document) <= ? THEN document ELSE NULL END FROM java_broker_state WHERE singleton=1", maxDocument).Scan(&data); err != nil {
		return err
	}
	if len(data) > maxDocument {
		return ErrState
	}
	var d document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&d) != nil || decoder.Decode(new(any)) != io.EOF || validateDocument(d, s.config) != nil {
		return ErrState
	}
	if fence && (epoch == "" || epoch != d.Epoch) {
		return ErrFence
	}
	original := data
	// Persist expiry even when the requested transition is refused. It must
	// never be possible to renew an already-expired reservation back to life.
	expire(&d, s.now())
	operationErr := fn(&d)
	if validateDocument(d, s.config) != nil {
		return ErrState
	}
	data, err = json.Marshal(d)
	if err != nil || len(data) > maxDocument {
		return ErrState
	}
	if !bytes.Equal(original, data) {
		if _, err = tx.ExecContext(ctx, "UPDATE java_broker_state SET document=? WHERE singleton=1", data); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return operationErr
}

// StartEpoch atomically fences prior brokers and seals every old host for
// cleanup. Java state is never adopted. Only never-provisioned reservations can
// be released without external termination evidence.
func (s *Store) StartEpoch(ctx context.Context) (string, error) {
	epoch, err := randomID()
	if err != nil {
		return "", err
	}
	err = s.update(ctx, "", false, func(d *document) error {
		d.Epoch = epoch
		for id, r := range d.Records {
			retire(&r, "broker_epoch_changed")
			d.Records[id] = r
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return epoch, nil
}

func (s *Store) Reserve(ctx context.Context, epoch string, binding javarunner.HostBinding, memoryBytes int64, lease, prepare time.Duration) (Record, error) {
	if !validBinding(binding) || memoryBytes <= 0 || lease < time.Millisecond || lease > time.Minute || prepare < time.Millisecond || prepare > time.Hour {
		return Record{}, ErrState
	}
	id, err := randomID()
	if err != nil {
		return Record{}, err
	}
	var result Record
	err = s.update(ctx, epoch, true, func(d *document) error {
		u := usage(d)
		if len(d.Records) >= maxRecords || u.Hosts >= d.Config.Budget.MaxHosts || u.VUsers >= d.Config.Budget.MaxVUsers || memoryBytes > d.Config.Budget.MaxMemoryBytes-u.MemoryBytes {
			return ErrCapacity
		}
		for _, r := range d.Records {
			if r.State != javarunner.HostReaped && r.Binding.OwnerID == binding.OwnerID && (r.BrokerEpoch != epoch || r.Binding.OwnerEpoch != binding.OwnerEpoch || r.State == javarunner.HostQuarantined) {
				return ErrCleanup
			}
		}
		if _, exists := d.Records[id]; exists {
			return ErrState
		}
		now := s.now()
		result = Record{ID: id, BrokerEpoch: epoch, Binding: binding, State: javarunner.HostReserved, MemoryBytes: memoryBytes, VUserOccupied: true, PodName: "java-" + id, LeaseUntil: now.Add(lease), PrepareUntil: now.Add(prepare)}
		d.Records[id] = result
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return result, nil
}

func (s *Store) change(ctx context.Context, epoch, id string, fn func(*Record) error) error {
	return s.update(ctx, epoch, true, func(d *document) error {
		r, ok := d.Records[id]
		if !ok {
			return ErrState
		}
		if err := fn(&r); err != nil {
			return err
		}
		d.Records[id] = r
		return nil
	})
}

func (s *Store) BeginPrepare(ctx context.Context, epoch, id string) error {
	return s.beginPrepare(ctx, epoch, id, "")
}

// BeginPreparePod binds the exact approved pod request before any create side effect.
func (s *Store) BeginPreparePod(ctx context.Context, epoch, id, policySHA256 string) error {
	if !validPolicyDigest(policySHA256) {
		return ErrState
	}
	return s.beginPrepare(ctx, epoch, id, policySHA256)
}
func validPolicyDigest(raw string) bool {
	digest, e := hex.DecodeString(raw)
	return e == nil && len(digest) == 32 && strings.ToLower(raw) == raw
}
func (s *Store) beginPrepare(ctx context.Context, epoch, id, policySHA256 string) error {
	return s.change(ctx, epoch, id, func(r *Record) error {
		if r.BrokerEpoch != epoch || r.State != javarunner.HostReserved {
			return ErrState
		}
		r.State = javarunner.HostPreparing
		r.PodPolicySHA256 = policySHA256
		return nil
	})
}

// BindPod accepts late provisioning receipts for quarantined hosts. It records
// the exact boundary but cannot make the host live. Only the current broker may
// submit such evidence; old brokers are fenced, including on delayed callbacks.
func (s *Store) BindPod(ctx context.Context, epoch, id, podUID, containerID string) error {
	if !name.MatchString(podUID) || (containerID != "" && !validContainer(containerID)) {
		return ErrState
	}
	return s.change(ctx, epoch, id, func(r *Record) error {
		if r.State != javarunner.HostPreparing && r.State != javarunner.HostQuarantined {
			return ErrState
		}
		if r.PodUID != "" && r.PodUID != podUID || r.ContainerID != "" && r.ContainerID != containerID {
			return ErrState
		}
		r.PodUID, r.ContainerID = podUID, containerID
		return nil
	})
}

func (s *Store) Ready(ctx context.Context, epoch, id string) error {
	return s.change(ctx, epoch, id, func(r *Record) error {
		if r.BrokerEpoch != epoch || r.State != javarunner.HostPreparing || r.PodUID == "" || r.ContainerID == "" {
			return ErrState
		}
		r.State = javarunner.HostReady
		return nil
	})
}

// Commit is an all-host barrier for a single execution/group. Prepare and
// harness readiness must not initialize user classes. A failed commit never
// partially activates the supplied hosts.
func (s *Store) Commit(ctx context.Context, epoch string, ids []string) error {
	if len(ids) == 0 || len(ids) > 10000 {
		return ErrState
	}
	return s.update(ctx, epoch, true, func(d *document) error {
		seen := map[string]bool{}
		var binding javarunner.HostBinding
		for i, id := range ids {
			r, ok := d.Records[id]
			if !ok || seen[id] || r.BrokerEpoch != epoch || r.State != javarunner.HostReady {
				return ErrState
			}
			seen[id] = true
			if i == 0 {
				binding = r.Binding
			} else if binding != r.Binding {
				return ErrState
			}
		}
		// Every pending host for this immutable execution/group must cross
		// the barrier together. Already committed hosts permit a later,
		// separately prepared increase; quarantined peers block activation.
		for id, r := range d.Records {
			if r.Binding == binding && r.State != javarunner.HostReaped && r.State != javarunner.HostCommitted && r.State != javarunner.HostDraining && !seen[id] {
				return ErrState
			}
		}
		for _, id := range ids {
			r := d.Records[id]
			r.State = javarunner.HostCommitted
			d.Records[id] = r
		}
		return nil
	})
}

func (s *Store) Renew(ctx context.Context, epoch, id string, lease time.Duration) error {
	if lease < time.Millisecond || lease > time.Minute {
		return ErrState
	}
	return s.change(ctx, epoch, id, func(r *Record) error {
		if r.BrokerEpoch != epoch || r.State == javarunner.HostQuarantined || r.State == javarunner.HostReaped || r.State == javarunner.HostDraining {
			return ErrFence
		}
		r.LeaseUntil = s.now().Add(lease)
		return nil
	})
}

func (s *Store) CanInvoke(ctx context.Context, epoch, id string) error {
	return s.change(ctx, epoch, id, func(r *Record) error {
		if r.BrokerEpoch != epoch || r.State != javarunner.HostCommitted {
			return ErrFence
		}
		return nil
	})
}

// CloseVUser releases occupancy independently of pod/memory capacity. It must
// only follow a trusted, cooperative lifecycle completion receipt.
func (s *Store) CloseVUser(ctx context.Context, epoch, id string) error {
	return s.change(ctx, epoch, id, func(r *Record) error {
		if r.BrokerEpoch != epoch || (r.State != javarunner.HostCommitted && r.State != javarunner.HostDraining) {
			return ErrState
		}
		r.VUserOccupied = false
		r.State = javarunner.HostDraining
		return nil
	})
}

func (s *Store) Abort(ctx context.Context, epoch, id, reason string) error {
	if !name.MatchString(reason) {
		return ErrState
	}
	return s.change(ctx, epoch, id, func(r *Record) error { retire(r, reason); return nil })
}

// ConfirmTerminated is an internal trusted attestor boundary, never a wire
// request. The caller must positively verify entire-container termination for
// this exact pod UID and container ID on the qualified runtime. Delete accepted,
// NotFound, a stopped JVM, and lease expiry are NOT evidence. Unbound creation
// outcomes cannot be released through this method.
func (s *Store) ConfirmTerminated(ctx context.Context, epoch, id, podUID, containerID string) error {
	return s.change(ctx, epoch, id, func(r *Record) error {
		if r.PodUID == "" || r.ContainerID == "" || r.PodUID != podUID || r.ContainerID != containerID {
			return ErrCleanup
		}
		if r.State != javarunner.HostQuarantined && r.State != javarunner.HostDraining && r.State != javarunner.HostReaped {
			return ErrState
		}
		r.State, r.VUserOccupied = javarunner.HostReaped, false
		return nil
	})
}

// ConfirmNeverStarted accepts only a separately attested creation fence and a
// complete negative runtime history for this immutable pod request. The
// attestor must prove that a delayed create cannot start it in the future.
// Kubernetes NotFound, a rejected create response and deletion are insufficient.
func (s *Store) ConfirmNeverStarted(ctx context.Context, epoch, id, podUID, podPolicySHA256 string) error {
	if podUID != "" && !name.MatchString(podUID) || !validPolicyDigest(podPolicySHA256) {
		return ErrCleanup
	}
	return s.change(ctx, epoch, id, func(r *Record) error {
		if r.State != javarunner.HostQuarantined || r.PodPolicySHA256 != podPolicySHA256 || r.PodUID != podUID || r.ContainerID != "" {
			return ErrCleanup
		}
		r.State, r.VUserOccupied = javarunner.HostReaped, false
		return nil
	})
}

// Snapshot also durably applies timeouts. The future broker monitor must call
// it independently of Java invocation and execute all draining/quarantine work.
func (s *Store) Snapshot(ctx context.Context, epoch string) ([]Record, javarunner.HostUsage, error) {
	var records []Record
	var used javarunner.HostUsage
	err := s.update(ctx, epoch, true, func(d *document) error {
		for _, r := range d.Records {
			records = append(records, r)
		}
		used = usage(d)
		return nil
	})
	if err != nil {
		return nil, javarunner.HostUsage{}, err
	}
	return records, used, nil
}

func expire(d *document, now time.Time) {
	for id, r := range d.Records {
		if !now.Before(r.LeaseUntil) {
			retire(&r, "lease_expired")
		} else if (r.State == javarunner.HostReserved || r.State == javarunner.HostPreparing || r.State == javarunner.HostReady) && !now.Before(r.PrepareUntil) {
			retire(&r, "preparation_expired")
		}
		d.Records[id] = r
	}
}
func retire(r *Record, reason string) {
	if r.State == javarunner.HostReaped {
		return
	}
	if r.Failure == "" {
		r.Failure = reason
	}
	if r.State == javarunner.HostReserved {
		r.State = javarunner.HostReaped
		r.VUserOccupied = false
	} else {
		r.State = javarunner.HostQuarantined
	}
}
func usage(d *document) (u javarunner.HostUsage) {
	for _, r := range d.Records {
		if r.State != javarunner.HostReaped {
			u.Hosts++
			u.MemoryBytes += r.MemoryBytes
			if r.VUserOccupied {
				u.VUsers++
			}
		}
	}
	return
}
func randomID() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func validBinding(b javarunner.HostBinding) bool {
	for _, v := range []string{b.OwnerID, b.OwnerEpoch, b.ExecutionID, b.GroupID} {
		if !name.MatchString(v) {
			return false
		}
	}
	h, err := hex.DecodeString(b.ConfigurationSHA256)
	return err == nil && len(h) == 32 && strings.ToLower(b.ConfigurationSHA256) == b.ConfigurationSHA256
}
func validContainer(s string) bool {
	parts := strings.Split(s, "://")
	return len(parts) == 2 && name.MatchString(parts[0]) && name.MatchString(parts[1])
}
func validateDocument(d document, cfg Config) error {
	if d.Version != 1 || d.Config != cfg || d.Records == nil || len(d.Records) > maxRecords || (d.Epoch != "" && !name.MatchString(d.Epoch)) {
		return ErrState
	}
	var used javarunner.HostUsage
	sessions := map[sessionKey]bool{}
	for id, r := range d.Records {
		decodedID, idErr := hex.DecodeString(id)
		if idErr != nil || len(decodedID) != 16 || strings.ToLower(id) != id || d.Epoch == "" || (r.BrokerEpoch != d.Epoch && r.State != javarunner.HostReaped && r.State != javarunner.HostQuarantined) {
			return ErrState
		}
		if id != r.ID || len(id) != 32 || !name.MatchString(id) || !name.MatchString(r.BrokerEpoch) || !validBinding(r.Binding) || r.MemoryBytes <= 0 || r.MemoryBytes > cfg.Budget.MaxMemoryBytes || r.PodName != "java-"+id || r.LeaseUntil.IsZero() || r.PrepareUntil.IsZero() || (r.Failure != "" && !name.MatchString(r.Failure)) {
			return ErrState
		}
		if r.SessionID != "" {
			key := dedicatedSessionKey(r.Binding, r.SessionID)
			if !name.MatchString(r.SessionID) || sessions[key] {
				return ErrState
			}
			sessions[key] = true
		}
		if r.PodPolicySHA256 != "" && (!validPolicyDigest(r.PodPolicySHA256) || r.State == javarunner.HostReserved) {
			return ErrState
		}
		if r.PodUID == "" && r.ContainerID != "" || r.PodUID != "" && !name.MatchString(r.PodUID) || r.ContainerID != "" && !validContainer(r.ContainerID) {
			return ErrState
		}
		switch r.State {
		case javarunner.HostReserved:
			if r.PodUID != "" || !r.VUserOccupied {
				return ErrState
			}
		case javarunner.HostPreparing:
			if !r.VUserOccupied {
				return ErrState
			}
		case javarunner.HostReady, javarunner.HostCommitted:
			if r.PodUID == "" || r.ContainerID == "" || !r.VUserOccupied {
				return ErrState
			}
		case javarunner.HostDraining:
			if r.PodUID == "" || r.ContainerID == "" || r.VUserOccupied {
				return ErrState
			}
		case javarunner.HostQuarantined:
		case javarunner.HostReaped:
			if r.VUserOccupied {
				return ErrState
			}
		default:
			return ErrState
		}
		if r.State != javarunner.HostReaped {
			if used.Hosts >= cfg.Budget.MaxHosts || r.MemoryBytes > cfg.Budget.MaxMemoryBytes-used.MemoryBytes {
				return ErrState
			}
			used.Hosts++
			used.MemoryBytes += r.MemoryBytes
			if r.VUserOccupied {
				used.VUsers++
			}
		}
	}
	if used.VUsers > cfg.Budget.MaxVUsers {
		return ErrState
	}
	return nil
}

// AbortWave atomically retires attached hosts and any uncommitted reservations
// omitted from their barrier. Previously committed hosts from an earlier ramp
// wave remain live. No partially provisioned capacity is released without proof.
func (s *Store) AbortWave(ctx context.Context, epoch string, binding javarunner.HostBinding, ids []string, reason string) error {
	if !validBinding(binding) || !name.MatchString(reason) || len(ids) > maxRecords {
		return ErrState
	}
	return s.update(ctx, epoch, true, func(d *document) error {
		selected := map[string]bool{}
		for _, id := range ids {
			r, ok := d.Records[id]
			if !ok || r.Binding != binding || selected[id] {
				return ErrState
			}
			selected[id] = true
		}
		for id, r := range d.Records {
			pending := r.State == javarunner.HostReserved || r.State == javarunner.HostPreparing || r.State == javarunner.HostReady
			if r.Binding == binding && (selected[id] || pending) {
				retire(&r, reason)
				d.Records[id] = r
			}
		}
		return nil
	})
}
