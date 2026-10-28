package generatorrpc

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// archiveManager owns generator-local archive writers, the retained archive
// catalog, artifact leases, transfer admission, and finalizer shutdown. Its
// mutex is independent of Service.mu; filesystem work is performed from
// immutable namespace/identity snapshots after releasing this mutex.
type archiveManager struct {
	mu sync.RWMutex

	namespace    *generatorArchiveNamespace
	namespaceErr error

	writers         map[string]*archiveRecorder
	browserJournals map[string]*protocol.BrowserSourceJournal
	creating        map[string]struct{}
	maxWriters      int

	records    map[string]*storage.RunArchiveRecord
	identities map[string]os.FileInfo
	leases     map[string]int
	deleting   map[string]struct{}
	maxRecords int
	ttl        time.Duration

	maxTransfers      int
	maxUnaryTransfers int
	activeTransfers   int
	activeUnary       int

	finalizerSlots chan struct{}
	finalizerMu    sync.Mutex
	finalizerWG    sync.WaitGroup
	closing        bool
}

type archiveCatalogLease struct {
	namespace        *generatorArchiveNamespace
	namespaceErr     error
	expectedIdentity os.FileInfo
}

func newArchiveManager(generatorID string, maxRecords int, ttl time.Duration, maxWriters, maxTransfers, maxUnaryTransfers int) *archiveManager {
	namespace, namespaceErr := prepareGeneratorArchiveNamespace(generatorID, true)
	if maxRecords <= 0 {
		maxRecords = defaultGeneratorArchiveMax
	}
	if maxWriters <= 0 {
		maxWriters = 1024
	}
	if maxTransfers <= 0 {
		maxTransfers = defaultMaxArchiveTransfers
	}
	if maxUnaryTransfers <= 0 {
		maxUnaryTransfers = defaultMaxArchiveUnaryTransfers
	}
	return &archiveManager{
		namespace:         namespace,
		namespaceErr:      namespaceErr,
		writers:           make(map[string]*archiveRecorder),
		browserJournals:   make(map[string]*protocol.BrowserSourceJournal),
		creating:          make(map[string]struct{}),
		maxWriters:        maxWriters,
		records:           make(map[string]*storage.RunArchiveRecord),
		identities:        make(map[string]os.FileInfo),
		leases:            make(map[string]int),
		deleting:          make(map[string]struct{}),
		maxRecords:        maxRecords,
		ttl:               ttl,
		maxTransfers:      maxTransfers,
		maxUnaryTransfers: maxUnaryTransfers,
		finalizerSlots:    make(chan struct{}, defaultMaxArchiveFinalizers),
	}
}

func (manager *archiveManager) configureRetention(maxRecords int, ttl time.Duration) {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	if maxRecords > 0 {
		manager.maxRecords = maxRecords
	}
	if ttl >= 0 {
		manager.ttl = ttl
	}
	manager.mu.Unlock()
}

func (manager *archiveManager) retention() (int, time.Duration) {
	manager.mu.RLock()
	maxRecords, ttl := manager.maxRecords, manager.ttl
	manager.mu.RUnlock()
	return maxRecords, ttl
}

func (manager *archiveManager) configureWriterLimit(maxWriters int) {
	if manager == nil || maxWriters <= 0 {
		return
	}
	manager.mu.Lock()
	manager.maxWriters = maxWriters
	manager.mu.Unlock()
}

func (manager *archiveManager) configureTransferLimits(maxTransfers, maxUnaryTransfers int) {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	if maxTransfers > 0 {
		manager.maxTransfers = maxTransfers
	}
	if maxUnaryTransfers > 0 {
		manager.maxUnaryTransfers = maxUnaryTransfers
	}
	manager.mu.Unlock()
}

func (manager *archiveManager) namespaceSnapshot() (*generatorArchiveNamespace, error) {
	if manager == nil {
		return nil, errors.New("generator archive manager is unavailable")
	}
	manager.mu.RLock()
	namespace, err := manager.namespace, manager.namespaceErr
	manager.mu.RUnlock()
	return namespace, err
}

func (manager *archiveManager) readinessError() error {
	namespace, err := manager.namespaceSnapshot()
	if err != nil {
		return err
	}
	if namespace == nil || namespace.root == nil {
		return errors.New("generator archive namespace is unavailable")
	}
	return nil
}

func (manager *archiveManager) root() string {
	if manager == nil {
		return ""
	}
	manager.mu.RLock()
	path := ""
	if manager.namespace != nil {
		path = strings.TrimSpace(manager.namespace.path)
	}
	manager.mu.RUnlock()
	return path
}

func (manager *archiveManager) deletingTest(testID string) bool {
	manager.mu.RLock()
	_, deleting := manager.deleting[testID]
	manager.mu.RUnlock()
	return deleting
}

func (manager *archiveManager) retainedTest(testID string) bool {
	manager.mu.RLock()
	retained := manager.records[testID] != nil
	manager.mu.RUnlock()
	return retained
}

func (manager *archiveManager) record(testID string) *storage.RunArchiveRecord {
	manager.mu.RLock()
	record := cloneRunArchiveRecord(manager.records[testID])
	manager.mu.RUnlock()
	return record
}

func (manager *archiveManager) recordCount() int {
	manager.mu.RLock()
	count := len(manager.records)
	manager.mu.RUnlock()
	return count
}

func (manager *archiveManager) acquireCatalogLease(testID string) (*archiveCatalogLease, bool) {
	if manager == nil {
		return nil, false
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.records[testID] == nil {
		return nil, false
	}
	manager.leases[testID]++
	return &archiveCatalogLease{
		namespace:        manager.namespace,
		namespaceErr:     manager.namespaceErr,
		expectedIdentity: manager.identities[testID],
	}, true
}

func (manager *archiveManager) releaseCatalogLease(testID string, now time.Time) []generatorArchiveRemoval {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	if manager.leases[testID] <= 1 {
		delete(manager.leases, testID)
	} else {
		manager.leases[testID]--
	}
	removed := manager.pruneArchivedRecordsLocked(now)
	manager.mu.Unlock()
	return removed
}

func (manager *archiveManager) registerRecord(record *storage.RunArchiveRecord, identity os.FileInfo, now time.Time) []generatorArchiveRemoval {
	if manager == nil || record == nil {
		return nil
	}
	manager.mu.Lock()
	manager.records[record.TestID] = cloneRunArchiveRecord(record)
	manager.identities[record.TestID] = identity
	removed := manager.pruneArchivedRecordsLocked(now)
	manager.mu.Unlock()
	return removed
}

func (manager *archiveManager) sweep(now time.Time) []generatorArchiveRemoval {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	removed := manager.pruneArchivedRecordsLocked(now)
	manager.mu.Unlock()
	return removed
}

func (manager *archiveManager) restoreRecord(record *storage.RunArchiveRecord, identity os.FileInfo) {
	if manager == nil || record == nil {
		return
	}
	manager.mu.Lock()
	manager.records[record.TestID] = cloneRunArchiveRecord(record)
	manager.identities[record.TestID] = identity
	manager.mu.Unlock()
}

// completeRemoval clears the transient test-ID reservation after filesystem
// cleanup. Failed cleanup restores the exact catalog snapshot so evidence is
// never silently forgotten.
func (manager *archiveManager) completeRemoval(removal generatorArchiveRemoval, retain bool) {
	if manager == nil || removal.record == nil {
		return
	}
	manager.mu.Lock()
	if retain {
		if _, exists := manager.records[removal.record.TestID]; !exists {
			manager.records[removal.record.TestID] = cloneRunArchiveRecord(removal.record)
			if removal.identity != nil {
				manager.identities[removal.record.TestID] = removal.identity
			}
		}
	} else {
		delete(manager.browserJournals, removal.record.TestID)
	}
	delete(manager.deleting, removal.record.TestID)
	manager.mu.Unlock()
}

// generatorArchiveRemoval is an immutable cleanup event emitted after the
// catalog has reserved the test ID for deletion. Filesystem cleanup happens
// after the manager lock is released and completes through completeRemoval.
type generatorArchiveRemoval struct {
	record   *storage.RunArchiveRecord
	identity os.FileInfo
}

// pruneArchivedRecordsLocked applies TTL first and then an oldest-first hard
// cap. Leased records are temporarily skipped; their release immediately
// triggers another pass. archiveManager.mu must be held.
func (manager *archiveManager) pruneArchivedRecordsLocked(now time.Time) []generatorArchiveRemoval {
	removed := make([]generatorArchiveRemoval, 0)
	if manager.ttl > 0 {
		for testID, record := range manager.records {
			if manager.leases[testID] > 0 || !archiveRecordExpired(record, now, manager.ttl) {
				continue
			}
			removed = append(removed, generatorArchiveRemoval{record: cloneRunArchiveRecord(record), identity: manager.identities[testID]})
			manager.deleting[testID] = struct{}{}
			delete(manager.records, testID)
			delete(manager.identities, testID)
		}
	}
	if len(manager.records) <= manager.maxRecords {
		return removed
	}
	type candidate struct {
		testID string
		record *storage.RunArchiveRecord
		at     time.Time
	}
	candidates := make([]candidate, 0, len(manager.records))
	for testID, record := range manager.records {
		candidates = append(candidates, candidate{testID: testID, record: record, at: archiveRecordTimestamp(record)})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].at.Equal(candidates[j].at) {
			return candidates[i].testID > candidates[j].testID
		}
		return candidates[i].at.After(candidates[j].at)
	})
	// Keep the newest configured window plus every in-flight lease. This permits
	// a bounded temporary overage for active readers without sacrificing a newer
	// archive merely because the oldest archive is still streaming.
	retained := make(map[string]struct{}, manager.maxRecords+len(manager.leases))
	for index, candidate := range candidates {
		if index < manager.maxRecords || manager.leases[candidate.testID] > 0 {
			retained[candidate.testID] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		if _, keep := retained[candidate.testID]; keep {
			continue
		}
		removed = append(removed, generatorArchiveRemoval{record: cloneRunArchiveRecord(candidate.record), identity: manager.identities[candidate.testID]})
		manager.deleting[candidate.testID] = struct{}{}
		delete(manager.records, candidate.testID)
		delete(manager.identities, candidate.testID)
	}
	return removed
}

func cloneRunArchiveRecord(record *storage.RunArchiveRecord) *storage.RunArchiveRecord {
	if record == nil {
		return nil
	}
	clone := *record
	return &clone
}

func archiveRecordExpired(record *storage.RunArchiveRecord, now time.Time, ttl time.Duration) bool {
	if record == nil || ttl <= 0 {
		return false
	}
	timestamp := archiveRecordTimestamp(record)
	return !timestamp.IsZero() && now.Sub(timestamp) > ttl
}

func archiveRecordTimestamp(record *storage.RunArchiveRecord) time.Time {
	if record == nil {
		return time.Time{}
	}
	if !record.CompletedAt.IsZero() {
		return record.CompletedAt
	}
	return record.CreatedAt
}

func (manager *archiveManager) writer(testID string) *archiveRecorder {
	manager.mu.RLock()
	writer := manager.writers[testID]
	manager.mu.RUnlock()
	return writer
}

func (manager *archiveManager) writerCount() int {
	manager.mu.RLock()
	count := len(manager.writers)
	manager.mu.RUnlock()
	return count
}

func (manager *archiveManager) writerAdmissionError(testID string) error {
	if manager == nil {
		return errors.New("generator archive manager is unavailable")
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if manager.writers[testID] != nil {
		return nil
	}
	if _, creating := manager.creating[testID]; creating {
		return nil
	}
	if len(manager.writers)+len(manager.creating) >= manager.maxWriters {
		return fmt.Errorf("generator active archive writer limit exceeded (%d)", manager.maxWriters)
	}
	return nil
}

// ensureWriter reserves admission under the manager lock, performs filesystem
// setup without that lock, and then publishes the initialized writer. Callers
// receive an existing writer when one is already installed for the test.
func (manager *archiveManager) ensureWriter(testID, testName string) (*archiveRecorder, bool, error) {
	if manager == nil {
		return nil, false, errors.New("generator archive manager is unavailable")
	}
	manager.mu.Lock()
	if writer := manager.writers[testID]; writer != nil {
		manager.mu.Unlock()
		return writer, false, nil
	}
	if _, creating := manager.creating[testID]; creating {
		manager.mu.Unlock()
		return nil, false, errors.New("generator archive writer creation is already in progress")
	}
	if len(manager.writers)+len(manager.creating) >= manager.maxWriters {
		limit := manager.maxWriters
		manager.mu.Unlock()
		return nil, false, fmt.Errorf("generator active archive writer limit exceeded (%d)", limit)
	}
	manager.creating[testID] = struct{}{}
	namespace, namespaceErr := manager.namespace, manager.namespaceErr
	manager.mu.Unlock()

	var writer *archiveRecorder
	if namespaceErr == nil {
		writer, namespaceErr = newArchiveRecorderInNamespace(namespace, testID, testName)
	}

	manager.mu.Lock()
	delete(manager.creating, testID)
	if namespaceErr != nil {
		manager.mu.Unlock()
		return nil, false, namespaceErr
	}
	if existing := manager.writers[testID]; existing != nil {
		manager.mu.Unlock()
		_ = writer.Discard()
		return existing, false, nil
	}
	writer.onBrowserJournalCreated = func(journal *protocol.BrowserSourceJournal) {
		manager.registerBrowserJournal(testID, journal)
	}
	delete(manager.browserJournals, testID)
	manager.writers[testID] = writer
	manager.mu.Unlock()
	return writer, true, nil
}

func (manager *archiveManager) setWriterForTest(testID string, writer *archiveRecorder) {
	manager.mu.Lock()
	if writer != nil {
		writer.onBrowserJournalCreated = func(journal *protocol.BrowserSourceJournal) {
			manager.registerBrowserJournal(testID, journal)
		}
	}
	manager.writers[testID] = writer
	manager.mu.Unlock()
}

func (manager *archiveManager) registerBrowserJournal(testID string, journal *protocol.BrowserSourceJournal) {
	if manager == nil || testID == "" || journal == nil {
		return
	}
	manager.mu.Lock()
	if manager.browserJournals == nil {
		manager.browserJournals = make(map[string]*protocol.BrowserSourceJournal)
	}
	manager.browserJournals[testID] = journal
	manager.mu.Unlock()
}

func (manager *archiveManager) sampleBrowserClockBracket(testID string) (protocol.BrowserClockBracketSample, bool) {
	if manager == nil || testID == "" {
		return protocol.BrowserClockBracketSample{}, false
	}
	manager.mu.RLock()
	journal := manager.browserJournals[testID]
	manager.mu.RUnlock()
	if journal == nil {
		return protocol.BrowserClockBracketSample{}, false
	}
	return journal.SampleClockBracket()
}

func (manager *archiveManager) deleteBrowserJournal(testID string) {
	if manager == nil || testID == "" {
		return
	}
	manager.mu.Lock()
	if _, retained := manager.records[testID]; !retained {
		delete(manager.browserJournals, testID)
	}
	manager.mu.Unlock()
}

func (manager *archiveManager) takeWriter(testID string) *archiveRecorder {
	if manager == nil {
		return nil
	}
	manager.mu.Lock()
	writer := manager.writers[testID]
	delete(manager.writers, testID)
	manager.mu.Unlock()
	return writer
}

func (manager *archiveManager) takeWriterIf(testID string, expected *archiveRecorder) *archiveRecorder {
	if manager == nil || expected == nil {
		return nil
	}
	manager.mu.Lock()
	writer := manager.writers[testID]
	if writer == expected {
		delete(manager.writers, testID)
	} else {
		writer = nil
	}
	manager.mu.Unlock()
	return writer
}

func (manager *archiveManager) beginTransfer(unary bool) (func(), error) {
	manager.mu.Lock()
	if manager.activeTransfers >= manager.maxTransfers || (unary && manager.activeUnary >= manager.maxUnaryTransfers) {
		manager.mu.Unlock()
		return nil, status.Error(codes.ResourceExhausted, "generator archive transfer concurrency limit exceeded")
	}
	manager.activeTransfers++
	if unary {
		manager.activeUnary++
	}
	manager.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			manager.mu.Lock()
			if manager.activeTransfers > 0 {
				manager.activeTransfers--
			}
			if unary && manager.activeUnary > 0 {
				manager.activeUnary--
			}
			manager.mu.Unlock()
		})
	}, nil
}

func (manager *archiveManager) addFinalizer() bool {
	if manager == nil {
		return false
	}
	manager.finalizerMu.Lock()
	defer manager.finalizerMu.Unlock()
	if manager.closing {
		return false
	}
	manager.finalizerWG.Add(1)
	return true
}

func (manager *archiveManager) finalizerDone() {
	if manager != nil {
		manager.finalizerWG.Done()
	}
}

func (manager *archiveManager) beginShutdown() {
	if manager == nil {
		return
	}
	manager.finalizerMu.Lock()
	manager.closing = true
	manager.finalizerMu.Unlock()
}

func (manager *archiveManager) finalizersDone() <-chan struct{} {
	done := make(chan struct{})
	if manager == nil {
		close(done)
		return done
	}
	go func() {
		manager.finalizerWG.Wait()
		close(done)
	}()
	return done
}

func (manager *archiveManager) acquireFinalizerSlot() func() {
	if manager == nil {
		return func() {}
	}
	manager.finalizerMu.Lock()
	slots := manager.finalizerSlots
	manager.finalizerMu.Unlock()
	if slots == nil {
		return func() {}
	}
	slots <- struct{}{}
	var once sync.Once
	return func() {
		once.Do(func() { <-slots })
	}
}

func (manager *archiveManager) setFinalizerSlotsForTest(slots chan struct{}) {
	manager.finalizerMu.Lock()
	manager.finalizerSlots = slots
	manager.finalizerMu.Unlock()
}
