package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/filelock"
	"github.com/jasonczech/apexload/pkg/internal/localfs"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	maxRuntimeHARWorkingGenerations = 128
	maxRuntimeHARPendingOwners      = 64
	maxRuntimeHARWorkingBytes       = int64(2 << 30)
	maxRuntimeHARManifestBytes      = 4096
	runtimeHARCloseWait             = 2 * time.Second
	harOwnershipManifestVersion     = 3
)

// harWorkingDirectoryIsLocal reports whether the HAR working directory lives on
// an allowlisted local filesystem (tests may override it to simulate network or
// FUSE mounts).
var harWorkingDirectoryIsLocal = localfs.IsLocal

var errHAROwnershipUnproven = errors.New("browser HAR physical ownership remains unproven")
var runtimeHAROwners = struct {
	sync.Mutex
	owners map[*harCaptureOwner]bool
}{owners: make(map[*harCaptureOwner]bool)}

type harOwnershipManifest struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
	State      string `json:"state"`
	// Processes (version 2 of a sealed or writer_entered manifest) is the
	// exact identity of the generator or of the native writer.
	Processes []harProcessIdentity `json:"processes,omitempty"`
}

// harFinalizeCheckpoint lets crash tests stop finalization at a stage; it is
// nil outside tests.
var harFinalizeCheckpoint func(stage string)

type harCaptureOwner struct {
	omissionOnce                   sync.Once
	mu                             sync.Mutex
	generation, path, artifactPath string
	parent, work, root             *os.Root
	namespace                      *harNamespace
	parentIdentity, identity       os.FileInfo
	lease                          *os.File
	release                        func() error
	localFS                        bool
	leaseClosed                    bool
	inheritedWriterPIDs            map[int]bool
	writerEntered                  bool
	closer                         func() error
	// bound closes when the writer's closer is bound or the writer was
	// abandoned, so a close requested while the context opens waits for it.
	bound                                      chan struct{}
	boundOnce                                  sync.Once
	closeOnce, finishOnce                      sync.Once
	generationOnce                             sync.Once
	generationErr                              error
	descriptorOnce                             sync.Once
	descriptorErr                              error
	closeDone, finishDone                      chan struct{}
	closeErr, finishErr                        error
	keep                                       bool
	redactor                                   protocol.TextRedactor
	retired                                    bool
	runtimeJoinOnce                            sync.Once
	runtimeDone                                chan struct{}
	runtimeErr                                 error
	documentJoinOnce                           sync.Once
	documentDone                               chan struct{}
	documentErr                                error
	finalizationOnce                           sync.Once
	finalizationDeadline                       time.Time
	finalizationTimer                          *time.Timer
	finalizationReported, finalizationComplete bool
}

func reserveHAROwner() (*harCaptureOwner, error) {
	runtimeHAROwners.Lock()
	defer runtimeHAROwners.Unlock()
	if len(runtimeHAROwners.owners) >= maxRuntimeHARPendingOwners {
		return nil, fmt.Errorf("browser HAR pending-owner limit reached")
	}
	o := &harCaptureOwner{closeDone: make(chan struct{}), finishDone: make(chan struct{}), bound: make(chan struct{})}
	runtimeHAROwners.owners[o] = true
	return o, nil
}

func (o *harCaptureOwner) releaseReservation() {
	runtimeHAROwners.Lock()
	delete(runtimeHAROwners.owners, o)
	runtimeHAROwners.Unlock()
}

func (o *harCaptureOwner) retiredSuccessfully() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.retired
}

func (o *harCaptureOwner) setLeaseInheritable(inheritable bool) error {
	if o == nil {
		return errHAROwnershipUnproven
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.localFS || o.leaseClosed || o.lease == nil || (inheritable && o.writerEntered) {
		return errHAROwnershipUnproven
	}
	return setHARLeaseInheritable(o.lease, inheritable)
}

func (o *harCaptureOwner) markLeaseInheritedByWriter(pid int) {
	if o == nil || pid <= 0 {
		return
	}
	o.mu.Lock()
	if o.inheritedWriterPIDs == nil {
		o.inheritedWriterPIDs = make(map[int]bool)
	}
	o.inheritedWriterPIDs[pid] = true
	o.mu.Unlock()
}

func (o *harCaptureOwner) writerInheritedLease(pid int) bool {
	if o == nil || pid <= 0 {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.localFS && o.inheritedWriterPIDs[pid]
}

func (o *harCaptureOwner) startProcessWithInheritedLease(start func() (int, error)) error {
	if err := o.setLeaseInheritable(true); err != nil {
		return err
	}
	pid, err := start()
	restoreErr := o.setLeaseInheritable(false)
	if err == nil && restoreErr == nil && pid > 0 {
		o.markLeaseInheritedByWriter(pid)
	}
	return errors.Join(err, restoreErr)
}

func pendingHAROwnerNeedsDriverInheritance(driverPID int) bool {
	if !harLeaseInheritableAcrossExec || driverPID <= 0 {
		return false
	}
	runtimeHAROwners.Lock()
	defer runtimeHAROwners.Unlock()
	for o := range runtimeHAROwners.owners {
		o.mu.Lock()
		needs := o.localFS && !o.leaseClosed && o.lease != nil && !o.writerEntered && !o.inheritedWriterPIDs[driverPID]
		o.mu.Unlock()
		if needs {
			return true
		}
	}
	return false
}

func newHARCaptureOwner(ctx context.Context, dir string) (_ *harCaptureOwner, err error) {
	o, err := reserveHAROwner()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			closeErr := o.closeDescriptors()
			err = errors.Join(err, closeErr)
			if closeErr == nil {
				o.releaseReservation()
			}
		}
	}()
	n, err := lockHARNamespace(ctx, dir)
	o.namespace = n
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, n.close()) }()
	if err = checkHARWorkingAdmission(n.work); err != nil {
		return nil, err
	}
	isLocal, localErr := harWorkingDirectoryIsLocal(filepath.Join(n.path, runtimeHARWorkingDirectory))
	isLocalFS := localErr == nil && isLocal && harLeaseInheritableAcrossExec
	o.generation, err = harRandomGeneration()
	if err != nil {
		return nil, err
	}
	if err = n.work.Mkdir(o.generation, 0o700); err != nil {
		return nil, err
	}
	o.root, err = openHARPrivateChild(n.work, o.generation, false)
	if err != nil {
		return nil, err
	}
	o.parent, err = n.root.OpenRoot(".")
	if err == nil {
		o.work, err = n.work.OpenRoot(".")
	}
	if err == nil {
		o.parentIdentity, err = o.parent.Stat(".")
	}
	if err == nil {
		o.identity, err = o.root.Stat(".")
	}
	var lease *os.File
	var release func() error
	if err == nil {
		lease, err = openHARRegular(o.root, "owner.lock", true)
	}
	if err == nil {
		release, err = filelock.LockContextOwned(ctx, lease)
	}
	o.mu.Lock()
	o.lease, o.release = lease, release
	o.mu.Unlock()
	if err == nil {
		err = verifyHARLease(o.root, "owner.lock", lease)
	}
	if err == nil {
		manifest := harOwnershipManifest{Version: 1, Generation: o.generation, State: "sealed"}
		if processes := harGeneratorProcesses(); len(processes) > 0 {
			manifest.Version, manifest.Processes = 2, processes
			if isLocalFS {
				manifest.Version = harOwnershipManifestVersion
			}
		}
		data, _ := json.Marshal(manifest)
		err = writeHARPrivateDurable(o.root, "manifest.json", data)
	}
	if err == nil {
		// The pinned local SDK truncates/rewrites this existing path during
		// flush. Reserve it at 0600 before admission so its ordinary Node
		// writeFile cannot create a default-mode raw file.
		err = writeHARPrivateDurable(o.root, "working.har", nil)
	}
	if err == nil {
		err = errors.Join(syncHARRoot(o.root), syncHARRoot(n.work))
	}
	if err != nil {
		return nil, err
	}
	o.path = filepath.Join(n.path, runtimeHARWorkingDirectory, o.generation, "working.har")
	o.mu.Lock()
	o.localFS = isLocalFS
	o.mu.Unlock()
	return o, nil
}

func checkHARWorkingAdmission(work *os.Root) error {
	if err := checkHARRetirementAdmission(work); err != nil {
		return err
	}
	dir, err := work.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	count, auxiliary, scanned := 0, 0, 0
	var bytes int64
	for {
		entries, readErr := dir.ReadDir(32)
		for _, e := range entries {
			scanned++
			if scanned > 1024 {
				return fmt.Errorf("HAR staging scan limit reached")
			}
			if e.Name() == "namespace.lock" {
				continue
			}
			if isHARLedgerAuxiliary(e.Name()) {
				auxiliary++
				info, err := work.Lstat(e.Name())
				if err != nil || auxiliary > maxHARRetirementRecords || info.Size() > maxHARRetirementLedgerBytes || validateHARAuxiliary(work, e.Name()) != nil {
					return fmt.Errorf("HAR retirement auxiliary state unavailable")
				}
				if info.Size() > maxRuntimeHARWorkingBytes-bytes {
					return fmt.Errorf("HAR staging byte limit reached")
				}
				bytes += info.Size()
				continue
			}
			count++
			if count >= maxRuntimeHARWorkingGenerations {
				return fmt.Errorf("browser HAR staging generation limit reached")
			}
			child, childErr := openHARPrivateChild(work, e.Name(), false)
			if childErr != nil {
				return fmt.Errorf("browser HAR staging contains an unowned entry")
			}
			children, childErr := child.Open(".")
			if childErr != nil {
				_ = child.Close()
				return childErr
			}
			files, childErr := children.ReadDir(harWorkingGenerationEntries + 1)
			_ = children.Close()
			if childErr != nil && childErr != io.EOF || len(files) > harWorkingGenerationEntries {
				_ = child.Close()
				return fmt.Errorf("browser HAR staging contains unexpected children")
			}
			for _, f := range files {
				if !allowedHARWorkingName(f.Name()) {
					_ = child.Close()
					return fmt.Errorf("browser HAR staging contains an unowned file")
				}
				info, statErr := child.Lstat(f.Name())
				if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
					_ = child.Close()
					return fmt.Errorf("browser HAR staging file is not regular")
				}
				if info.Size() > maxRuntimeHARWorkingBytes-bytes {
					_ = child.Close()
					return fmt.Errorf("browser HAR staging byte limit reached")
				}
				bytes += info.Size()
			}
			_ = child.Close()
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

// validateHARAuxiliary proves a ledger auxiliary file private and regular.
func validateHARAuxiliary(work *os.Root, name string) error {
	f, err := openHARRegular(work, name, false)
	if err != nil {
		return err
	}
	return f.Close()
}

// harWorkingGenerationEntries is how many files a generation may hold: the
// names allowedHARWorkingName admits.
const harWorkingGenerationEntries = 6

func allowedHARWorkingName(name string) bool {
	switch name {
	case "owner.lock", "manifest.json", "writer-entered.json", "writer-joined.json", "working.har", "scrubbed.part":
		return true
	}
	return false
}

// syncHARWriterRecord makes the writer record's directory entry durable
// (tests replace it to fail after the record exists).
var syncHARWriterRecord = syncHARRoot

// beginWriter durably records the native writer about to receive the
// working path before it may write there; an identity it cannot record keeps
// the generation until the writer joins.
//
// The writer counts as entered only once the record is durable: a write that
// fails leaves no writer (the caller never opens the context) and removes
// what it wrote, so neither this owner nor a later cleaner waits for a writer
// that never received the path.
func (o *harCaptureOwner) beginWriter() error {
	if o.root == nil {
		// An owner without a generation has nowhere to record its writer.
		o.mu.Lock()
		o.writerEntered = true
		o.mu.Unlock()
		return errHAROwnershipUnproven
	}
	manifest := harOwnershipManifest{Version: 1, Generation: o.generation, State: "writer_entered"}
	if processes := harWriterProcesses(); len(processes) > 0 {
		manifest.Version, manifest.Processes = 2, processes
		if o.writerInheritedLease(processes[0].PID) {
			manifest.Version = harOwnershipManifestVersion
		}
	}
	data, _ := json.Marshal(manifest)
	err := writeHARPrivateDurable(o.root, "writer-entered.json", data)
	if err == nil {
		err = syncHARWriterRecord(o.root)
	}
	if err != nil {
		// Remove only a record this call created (an existing one is not
		// ours to delete).
		if !errors.Is(err, os.ErrExist) {
			if removeErr := o.root.Remove("writer-entered.json"); removeErr == nil {
				_ = syncHARRoot(o.root)
			}
		}
		return err
	}
	o.mu.Lock()
	o.writerEntered = true
	o.mu.Unlock()
	return nil
}
func (o *harCaptureOwner) bindWriter(closer func() error) {
	o.mu.Lock()
	o.closer = closer
	o.mu.Unlock()
	o.boundOnce.Do(func() { close(o.bound) })
}

// abandonWriter records that the writer's context never opened.
func (o *harCaptureOwner) abandonWriter() {
	o.boundOnce.Do(func() { close(o.bound) })
}

// startClose does not start the finalization budget: a resource-limit close can
// happen mid-session, while finalization begins only at teardown (the runtime
// and document joins and startFinish start the one shared deadline).
func (o *harCaptureOwner) startClose() {
	o.closeOnce.Do(func() {
		go func() {
			var err error
			completed := false
			defer func() {
				recovered := recover()
				if !completed || recovered != nil {
					err = errors.Join(err, errHAROwnershipUnproven)
				}
				if err != nil {
					err = errors.Join(err, errHAROwnershipUnproven)
				}
				o.mu.Lock()
				o.closeErr = err
				o.mu.Unlock()
				close(o.closeDone)
			}()
			o.mu.Lock()
			closer, entered := o.closer, o.writerEntered
			o.mu.Unlock()
			if closer == nil && entered {
				// The context is still opening: wait for its writer to be bound
				// (or abandoned) rather than declaring the owner unproven.
				select {
				case <-o.bound:
				case <-time.After(runtimeHARCloseWait):
				}
				o.mu.Lock()
				closer = o.closer
				o.mu.Unlock()
			}
			if closer != nil {
				err = closer()
			} else if entered {
				err = errHAROwnershipUnproven
			}
			if err == nil {
				data, _ := json.Marshal(harOwnershipManifest{Version: 1, Generation: o.generation, State: "writer_joined"})
				err = writeHARPrivateDurable(o.root, "writer-joined.json", data)
				if err == nil {
					err = syncHARRoot(o.root)
				}
			}
			completed = true
		}()
	})
}

func (o *harCaptureOwner) startDocumentJoin(join func()) {
	o.startFinalizationBudget()
	o.documentJoinOnce.Do(func() {
		o.mu.Lock()
		o.documentDone = make(chan struct{})
		done := o.documentDone
		o.mu.Unlock()
		go func() {
			completed := false
			defer func() {
				recovered := recover()
				if !completed || recovered != nil {
					o.recordDocumentError(errHAROwnershipUnproven)
				}
				close(done)
			}()
			join()
			completed = true
		}()
	})
}

func (o *harCaptureOwner) recordDocumentError(err error) {
	if err != nil {
		o.mu.Lock()
		o.documentErr = errors.Join(o.documentErr, err, errHAROwnershipUnproven)
		o.mu.Unlock()
	}
}

func (o *harCaptureOwner) waitDocument(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, runtimeHARCloseWait)
	defer cancel()
	o.mu.Lock()
	done := o.documentDone
	o.mu.Unlock()
	select {
	case <-done:
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.documentErr
	case <-ctx.Done():
		return errors.Join(errHAROwnershipUnproven, ctx.Err())
	}
}

func (o *harCaptureOwner) startRuntimeJoin(join func()) {
	o.startFinalizationBudget()
	o.runtimeJoinOnce.Do(func() {
		o.mu.Lock()
		o.runtimeDone = make(chan struct{})
		done := o.runtimeDone
		o.mu.Unlock()
		go func() {
			completed := false
			defer func() {
				recovered := recover()
				if !completed || recovered != nil {
					o.recordRuntimeError(errHAROwnershipUnproven)
				}
				close(done)
			}()
			join()
			completed = true
		}()
	})
}

func (o *harCaptureOwner) recordRuntimeError(err error) {
	if err != nil {
		o.mu.Lock()
		o.runtimeErr = errors.Join(o.runtimeErr, err, errHAROwnershipUnproven)
		o.mu.Unlock()
	}
}

func (o *harCaptureOwner) upgradeFinish(keep bool, redactor protocol.TextRedactor) {
	o.mu.Lock()
	if keep {
		o.keep = true
	}
	if redactor != nil && o.redactor == nil {
		o.redactor = redactor
	}
	o.mu.Unlock()
}

func (o *harCaptureOwner) startFinish(keep bool, redactor protocol.TextRedactor) {
	o.upgradeFinish(keep, redactor)
	o.startFinalizationBudget()
	o.finishOnce.Do(func() {
		o.startClose()
		go func() {
			var err error
			completed := false
			defer func() {
				recovered := recover()
				if !completed || recovered != nil {
					err = errors.Join(err, errHAROwnershipUnproven)
				}
				if err == nil && !o.retiredSuccessfully() {
					err = errHAROwnershipUnproven
				}
				err = errors.Join(err, o.finalizationBudgetError())
				o.finishFinalizationBudget()
				o.mu.Lock()
				o.finishErr = err
				o.mu.Unlock()
				close(o.finishDone)
			}()
			<-o.closeDone
			o.mu.Lock()
			err = o.closeErr
			runtimeDone, documentDone := o.runtimeDone, o.documentDone
			o.mu.Unlock()
			if documentDone != nil {
				<-documentDone
				o.mu.Lock()
				err = errors.Join(err, o.documentErr)
				o.mu.Unlock()
			}
			if runtimeDone != nil {
				<-runtimeDone
				o.mu.Lock()
				err = errors.Join(err, o.runtimeErr)
				o.mu.Unlock()
			}
			if err == nil {
				o.mu.Lock()
				finalKeep, finalRedactor := o.keep, o.redactor
				o.mu.Unlock()
				err = o.finalizeJoined(finalKeep, finalRedactor)
			}
			completed = true
		}()
	})
}

func (o *harCaptureOwner) waitFinish(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, runtimeHARCloseWait)
	defer cancel()
	select {
	case <-o.finishDone:
		o.mu.Lock()
		defer o.mu.Unlock()
		return o.artifactPath, o.finishErr
	case <-ctx.Done():
		return "", errors.Join(errHAROwnershipUnproven, ctx.Err(), o.finalizationBudgetError())
	}
}

func (o *harCaptureOwner) finalizeJoined(keep bool, redactor protocol.TextRedactor) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeHARCloseWait)
	defer cancel()
	n, err := lockHARNamespace(ctx, filepath.Dir(filepath.Dir(filepath.Dir(o.path))))
	if err != nil {
		return err
	}
	retirementProven := false
	defer func() {
		namespaceErr := n.close()
		err = errors.Join(err, namespaceErr)
		if retirementProven && namespaceErr == nil {
			o.mu.Lock()
			o.retired = true
			o.mu.Unlock()
			o.releaseReservation()
		}
	}()
	parentInfo, err := n.root.Stat(".")
	if err != nil || !os.SameFile(parentInfo, o.parentIdentity) {
		return errHAROwnershipUnproven
	}
	current, err := n.work.Lstat(o.generation)
	if err != nil || !os.SameFile(current, o.identity) {
		return errHAROwnershipUnproven
	}
	if err := verifyHARLease(o.root, "owner.lock", o.lease); err != nil {
		return err
	}
	if harFinalizeCheckpoint != nil {
		harFinalizeCheckpoint("scrub")
	}
	var publicationErr error
	publicationCtx, publicationCancel := context.WithDeadline(context.Background(), o.startFinalizationBudget())
	defer publicationCancel()
	if keep && o.finalizationBudgetError() != nil {
		publicationErr = errHARFinalizationBudget
	} else if keep {
		f, openErr := openHARRegular(o.root, "working.har", false)
		if openErr != nil {
			publicationErr = openErr
		} else {
			data, readErr := io.ReadAll(io.LimitReader(f, maxRuntimeHARBytes+1))
			readErr = errors.Join(readErr, f.Close())
			if readErr == nil && len(data) > maxRuntimeHARBytes {
				readErr = fmt.Errorf("browser HAR exceeds size limit")
			}
			if readErr == nil {
				readErr = o.finalizationBudgetError()
				if readErr == nil {
					data, readErr = scrub.HAR(data, scrub.Policy{})
				}
			}
			if readErr == nil {
				// Imports read the browser's protocol as a browser-run
				// observation, never as proxy legs.
				readErr = o.finalizationBudgetError()
				if readErr == nil {
					data, readErr = protocol.TagBrowserHARHTTPVersions(data, protocol.HARProvenanceBrowserRun)
				}
			}
			if readErr == nil && redactor != nil {
				readErr = o.finalizationBudgetError()
				if readErr == nil {
					data, readErr = redactRuntimeHARText(data, redactor)
				}
			}
			if readErr == nil {
				readErr = o.publishScrubbed(publicationCtx, n.root, data)
			}
			publicationErr = readErr
		}
	}
	// A publication failure is observable, but a proven joined raw writer can
	// still be retired. Unproven close never enters this function.
	err = retireHARGeneration(n.work, o.root, o.generation, o.identity, false, o.closeGeneration)
	if err == nil {
		err = o.closeDescriptors()
		retirementProven = err == nil
	}
	return errors.Join(publicationErr, err)
}

func (o *harCaptureOwner) publishScrubbed(ctx context.Context, parent *os.Root, data []byte) (err error) {
	if err := o.finalizationBudgetError(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeHARPrivateDurable(o.root, "scrubbed.part", data); err != nil {
		return err
	}
	if err := o.finalizationBudgetError(); err != nil {
		return err
	}
	name := "apexload_browser_" + o.generation + "_scrubbed.har"
	private := filepath.Join(runtimeHARWorkingDirectory, o.generation, "scrubbed.part")
	privateInfo, err := o.root.Lstat("scrubbed.part")
	if err != nil {
		return err
	}
	// Keep the private link until publication is qualified. A failed publication
	// removes only the link to this exact inode, under the namespace lease.
	if err := parent.Link(private, name); err != nil {
		return err
	}
	defer func() {
		if err == nil {
			return
		}
		current, statErr := parent.Lstat(name)
		if errors.Is(statErr, os.ErrNotExist) {
			return
		}
		if statErr != nil || !os.SameFile(privateInfo, current) {
			err = errors.Join(err, statErr, errHAROwnershipUnproven)
			return
		}
		err = errors.Join(err, parent.Remove(name), syncHARRoot(parent))
	}()
	if err := syncHARRoot(parent); err != nil {
		return err
	}
	if harFinalizeCheckpoint != nil {
		harFinalizeCheckpoint("publish")
	}
	if err := o.finalizationBudgetError(); err != nil {
		return err
	}
	if err := enforceRuntimeHARRetentionAtContext(ctx, parent, maxRetainedRuntimeHARArtifacts, maxRetainedRuntimeHARBytes); err != nil {
		return err
	}
	info, err := parent.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(privateInfo, info) {
		return errors.Join(err, errHAROwnershipUnproven)
	}
	if err := o.finalizationBudgetError(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	if !time.Now().Before(o.finalizationDeadline) {
		o.mu.Unlock()
		return o.finalizationBudgetError()
	}
	o.artifactPath = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(o.path))), name)
	// All close/flush/scrub/publication work has completed. Joined raw retirement
	// remains bounded separately and cannot retroactively expire this artifact.
	o.finalizationComplete = true
	if o.finalizationTimer != nil {
		o.finalizationTimer.Stop()
	}
	o.mu.Unlock()
	return nil
}

// closeGeneration releases the generation's lease and closes it and the
// generation root, once.
func (o *harCaptureOwner) closeGeneration() error {
	o.generationOnce.Do(func() {
		var err error
		o.mu.Lock()
		o.leaseClosed = true
		o.mu.Unlock()
		if o.release != nil {
			err = errors.Join(err, o.release())
		}
		if o.lease != nil {
			err = errors.Join(err, o.lease.Close())
		}
		if o.root != nil {
			err = errors.Join(err, o.root.Close())
		}
		o.generationErr = err
	})
	return o.generationErr
}

func (o *harCaptureOwner) closeDescriptors() error {
	o.descriptorOnce.Do(func() {
		err := o.closeGeneration()
		if o.work != nil {
			err = errors.Join(err, o.work.Close())
		}
		if o.parent != nil {
			err = errors.Join(err, o.parent.Close())
		}
		if o.namespace != nil {
			err = errors.Join(err, o.namespace.close())
		}
		o.descriptorErr = err
	})
	return o.descriptorErr
}

func harGenerationFromDirectory(name string) (string, bool) {
	if validHARGeneration(name) {
		return name, true
	}
	if strings.HasPrefix(name, ".retired-") {
		generation := strings.TrimPrefix(name, ".retired-")
		if validHARGeneration(generation) {
			return generation, true
		}
	}
	return "", false
}

func harWorkingGenerationSize(root *os.Root) (int64, error) {
	directory, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	entries, readErr := directory.ReadDir(harWorkingGenerationEntries + 1)
	closeErr := directory.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(entries) > harWorkingGenerationEntries {
		return 0, errHAROwnershipUnproven
	}
	var size int64
	for _, entry := range entries {
		if !allowedHARWorkingName(entry.Name()) {
			return 0, errHAROwnershipUnproven
		}
		info, err := root.Lstat(entry.Name())
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return 0, errHAROwnershipUnproven
		}
		if info.Size() > maxRuntimeHARWorkingBytes-size {
			return 0, fmt.Errorf("HAR generation byte accounting limit reached")
		}
		size += info.Size()
	}
	return size, nil
}

// retireHARGeneration retires a writer-joined generation or, when abandoned,
// one whose recorded processes provably no longer exist (checked while the
// caller holds its lease). Every platform retires in one order: the
// identity-bound record is made durable under the namespace lease; then
// closeGeneration releases and closes the generation's lease and its root
// (Windows cannot rename a directory or remove files that handles hold
// open); then the directory is renamed to its tombstone, reopened, emptied
// and closed, and removed.
func retireHARGeneration(work, root *os.Root, directoryName string, identity os.FileInfo, abandoned bool, closeGeneration func() error, localFS ...bool) error {
	generation, recognized := harGenerationFromDirectory(directoryName)
	if !recognized || closeGeneration == nil {
		return errHAROwnershipUnproven
	}
	current, err := work.Lstat(directoryName)
	if err != nil || !os.SameFile(identity, current) || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 {
		return errHAROwnershipUnproven
	}
	id, err := persistentHARIdentityAt(work, directoryName, identity)
	if err != nil {
		return err
	}
	r := harRetirementRecord{Kind: "working", Generation: generation, Identity: id}
	old, recorded, err := findHARRetirement(work, "working", generation)
	if err != nil {
		return err
	}
	if recorded {
		if old != r {
			return errHAROwnershipUnproven
		}
	} else {
		if abandoned {
			isLocal := len(localFS) > 0 && localFS[0]
			if !harAbandonmentProven(root, generation, isLocal) {
				return errHAROwnershipUnproven
			}
		} else {
			if err := readHARManifest(root, "manifest.json", generation, "sealed"); err != nil {
				return err
			}
			if err := readHARManifest(root, "writer-joined.json", generation, "writer_joined"); err != nil {
				return err
			}
		}
		if err := registerHARRetirement(work, r); err != nil {
			return err
		}
	}
	if _, err := harWorkingGenerationSize(root); err != nil {
		return err
	}
	if err := closeGeneration(); err != nil {
		return errors.Join(err, errHAROwnershipUnproven)
	}
	tombstone := ".retired-" + generation
	if directoryName != tombstone {
		if err := renameHARNoReplace(work, directoryName, tombstone); err != nil {
			return err
		}
		if err := syncHARRoot(work); err != nil {
			return err
		}
	}
	renamed, err := work.Lstat(tombstone)
	if err != nil || !os.SameFile(identity, renamed) {
		return errHAROwnershipUnproven
	}
	retired, err := openHARPrivateChild(work, tombstone, false)
	if err != nil {
		return err
	}
	// The synced namespace ledger preserves joined proof through every unlink,
	// including a crash after removal of all generation control files.
	var removeErr error
	for _, name := range []string{"working.har", "scrubbed.part", "owner.lock", "writer-entered.json", "writer-joined.json", "manifest.json"} {
		if err := removeHAR(retired, name); err != nil && !os.IsNotExist(err) {
			removeErr = err
			break
		}
	}
	if err := errors.Join(removeErr, retired.Close()); err != nil {
		return err
	}
	if err := removeHAR(work, tombstone); err != nil {
		return err
	}
	if err := syncHARRoot(work); err != nil {
		return err
	}
	if _, info, err := harRetirementPayload(work, r); err != nil || info != nil {
		return errors.Join(err, errHAROwnershipUnproven)
	}
	return clearHARRetirement(work, r)
}

// RuntimeHARWorkingPruneCandidate is an opaque, process-local retention plan.
// Its identities are revalidated and its lease is reacquired when applied.
type RuntimeHARWorkingPruneCandidate struct {
	Path       string
	SizeBytes  int64
	ModifiedAt time.Time
	Completed  bool
	// Abandoned marks a generation without writer-joined proof whose lease
	// was free and whose recorded processes provably no longer exist.
	Abandoned                bool
	terminal                 *harRetirementRecord
	planCursor               *harRetirementPlanCursor
	generation               string
	directoryName            string
	identity, parentIdentity os.FileInfo
}

// MatchesFileIdentity supports deduplication of a completed artifact in one
// directory plan. It does not grant cleanup authority or reveal the held
// descriptor; pruning separately revalidates the namespace, lease and proof.
func (c RuntimeHARWorkingPruneCandidate) MatchesFileIdentity(info os.FileInfo) bool {
	return c.identity != nil && info != nil && os.SameFile(c.identity, info)
}

func readHARManifest(root *os.Root, name, generation, state string) error {
	_, err := readHARManifestFile(root, name, generation, state)
	return err
}

// errHARManifestMalformed: an ownership record that does not decode, which
// a crash mid-write leaves.
var errHARManifestMalformed = errors.New("browser HAR ownership record is malformed")

// readHARManifestFile reads an ownership manifest. A sealed manifest and a
// writer-entered record may be version 2 or version 3, which record process
// identities; version 3 additionally guarantees on a local Unix filesystem
// that the generation lease is held across the writer process tree.
func readHARManifestFile(root *os.Root, name, generation, state string) (harOwnershipManifest, error) {
	f, err := openHARRegular(root, name, false)
	if err != nil {
		return harOwnershipManifest{}, err
	}
	data, err := io.LimitReader(f, maxRuntimeHARManifestBytes+1), error(nil)
	raw, readErr := io.ReadAll(data)
	err = errors.Join(readErr, f.Close())
	if err != nil || len(raw) > maxRuntimeHARManifestBytes {
		return harOwnershipManifest{}, errHAROwnershipUnproven
	}
	var m harOwnershipManifest
	if err := strictHARJSON(raw, &m); err != nil {
		return harOwnershipManifest{}, errors.Join(errHARManifestMalformed, err)
	}
	versioned := m.Version == 1 && len(m.Processes) == 0 || (m.Version == 2 || m.Version == harOwnershipManifestVersion) && (state == "sealed" || state == "writer_entered") && len(m.Processes) == 1
	if !versioned || m.Generation != generation || m.State != state {
		return harOwnershipManifest{}, errHAROwnershipUnproven
	}
	return m, nil
}

// harAbandonmentProven reports whether an unjoined generation's recorded
// generator and any entered native writer provably no longer exist.
func harAbandonmentProven(root *os.Root, generation string, localFS ...bool) bool {
	sealed, err := readHARManifestFile(root, "manifest.json", generation, "sealed")
	if err != nil || len(sealed.Processes) != 1 || sealed.Processes[0].Role != harProcessGenerator {
		return false
	}
	isLocal := len(localFS) > 0 && localFS[0]
	leaseCoversWriterTree := isLocal && harLeaseInheritableAcrossExec && sealed.Version == harOwnershipManifestVersion
	processes := sealed.Processes
	entered, err := readHARManifestFile(root, "writer-entered.json", generation, "writer_entered")
	switch {
	case errors.Is(err, os.ErrNotExist), errors.Is(err, errHARManifestMalformed):
		// No native writer was ever given the working path: the record is
		// durable before the path is handed out, so one that does not
		// decode was never completed.
	case err != nil || len(entered.Processes) != 1 || entered.Processes[0].Role != harProcessWriter:
		return false
	default:
		leaseCoversWriterTree = leaseCoversWriterTree && entered.Version == harOwnershipManifestVersion
		processes = append(processes, entered.Processes[0])
	}
	return harProcessesGoneWithLease(processes, leaseCoversWriterTree)
}

// harGenerationAbandoned reports whether an unjoined generation's lease is
// free and its abandonment is proven.
func harGenerationAbandoned(root *os.Root, generation string, localFS ...bool) bool {
	if _, err := readHARManifestFile(root, "manifest.json", generation, "sealed"); err != nil {
		return false
	}
	lease, err := openHARRegular(root, "owner.lock", false)
	if err != nil {
		return false
	}
	defer lease.Close()
	release, acquired, err := filelock.TryLock(lease)
	if !acquired || err != nil {
		if release != nil {
			_ = release()
		}
		return false
	}
	free := verifyHARLease(root, "owner.lock", lease) == nil
	if release() != nil || !free {
		return false
	}
	return harAbandonmentProven(root, generation, localFS...)
}

// PlanRuntimeHARWorkingCleanup recognizes a durable writer-joined proof, or
// an abandoned generation: a free lease and recorded processes that provably
// no longer exist. An unlocked lease, old timestamp or missing PID alone never
// authorizes deletion.
func PlanRuntimeHARWorkingCleanup(dir string, limit int) []RuntimeHARWorkingPruneCandidate {
	out, _ := PlanRuntimeHARWorkingCleanupContext(context.Background(), dir, limit)
	return out
}

func PlanRuntimeHARWorkingCleanupContext(ctx context.Context, dir string, limit int) ([]RuntimeHARWorkingPruneCandidate, error) {
	if ctx == nil || limit <= 0 {
		return nil, fmt.Errorf("HAR cleanup requires bounded context/limit")
	}
	if limit > maxRuntimeHARWorkingGenerations {
		limit = maxRuntimeHARWorkingGenerations
	}
	if !NativeHARSupported() {
		// This platform never creates a working generation, so it has none
		// to prove and retire.
		return nil, nil
	}
	path, parent, err := openHARPublishedRoot(dir)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	work, err := openHARPrivateChild(parent, runtimeHARWorkingDirectory, false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer work.Close()
	out, cursor, err := readHARLedgerCandidates(ctx, parent, work, path, limit)
	if err != nil {
		return nil, err
	}
	recorded := map[string]bool{}
	for _, c := range out {
		if !c.Completed {
			recorded[c.generation] = true
		}
	}
	directory, err := work.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	parentInfo, err := parent.Stat(".")
	if err != nil {
		return nil, err
	}
	workLocal, workLocalErr := harWorkingDirectoryIsLocal(filepath.Join(path, runtimeHARWorkingDirectory))
	isLocalFS := workLocalErr == nil && workLocal && harLeaseInheritableAcrossExec
	scanned := 0
	for scanned < 1024 && len(out) < limit {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		entries, readErr := directory.ReadDir(32)
		for _, e := range entries {
			scanned++
			if scanned > 1024 || len(out) >= limit {
				break
			}
			generation, recognized := harGenerationFromDirectory(e.Name())
			if !recognized || recorded[generation] {
				continue
			}
			root, openErr := openHARPrivateChild(work, e.Name(), false)
			if openErr != nil {
				continue
			}
			joined := readHARManifest(root, "manifest.json", generation, "sealed") == nil && readHARManifest(root, "writer-joined.json", generation, "writer_joined") == nil
			abandoned := !joined && harGenerationAbandoned(root, generation, isLocalFS)
			if joined || abandoned {
				info, statErr := root.Stat(".")
				size, sizeErr := harWorkingGenerationSize(root)
				if statErr == nil && sizeErr == nil {
					out = append(out, RuntimeHARWorkingPruneCandidate{Path: filepath.Join(path, runtimeHARWorkingDirectory, e.Name()), SizeBytes: size, ModifiedAt: info.ModTime().UTC(), Abandoned: abandoned, generation: generation, directoryName: e.Name(), identity: info, parentIdentity: parentInfo, planCursor: cursor})
				}
			}
			_ = root.Close()
		}
		if readErr == io.EOF {
			return out, nil
		}
		if readErr != nil {
			return out, readErr
		}
	}
	if scanned >= 1024 && len(out) < limit {
		return out, fmt.Errorf("HAR cleanup scan limit reached")
	}
	return out, nil
}

func PruneRuntimeHARWorkingCandidate(ctx context.Context, c RuntimeHARWorkingPruneCandidate) (err error) {
	if ctx == nil || c.parentIdentity == nil || filepath.Base(c.Path) != c.directoryName {
		return errHAROwnershipUnproven
	}
	parentPath := filepath.Dir(filepath.Dir(c.Path))
	if c.Completed {
		parentPath = filepath.Dir(c.Path)
	}
	n, err := lockHARNamespace(ctx, parentPath)
	if err != nil {
		return err
	}
	var cursor *harRetirementPlanCursor
	var nextIdentity os.FileInfo
	var nextRecords []harRetirementRecord
	defer func() {
		namespaceErr := n.close()
		if cursor != nil {
			if err == nil && namespaceErr == nil && nextIdentity != nil {
				cursor.identity, cursor.records = nextIdentity, nextRecords
			}
			cursor.mu.Unlock()
		}
		err = errors.Join(err, namespaceErr)
	}()
	if err := n.verify(); err != nil {
		return err
	}
	parentInfo, err := n.root.Stat(".")
	if err != nil || !os.SameFile(parentInfo, c.parentIdentity) {
		return errHAROwnershipUnproven
	}
	if c.planCursor == nil {
		return errHAROwnershipUnproven
	}
	cursor = c.planCursor
	cursor.mu.Lock()
	ledger, ledgerInfo, ledgerErr := readHARRetirementLedger(n.work)
	identityMatches := cursor.identity == nil && ledgerInfo == nil || cursor.identity != nil && ledgerInfo != nil && os.SameFile(cursor.identity, ledgerInfo)
	if ledgerErr != nil || !identityMatches || !equalHARRetirementRecords(cursor.records, ledger.Records) {
		return errors.Join(ledgerErr, errHAROwnershipUnproven)
	}
	// Every candidate shares the same authorization cursor. A joined but
	// unrecorded generation registers and clears its own transient record;
	// terminal candidates remove their existing record. Both must leave the
	// exact expected remainder before this plan may adopt its own new inode.
	defer func() {
		if err == nil {
			nextIdentity, nextRecords, err = cursor.afterMutation(n.work, c.terminal)
		}
	}()
	if c.terminal != nil {
		found := false
		for _, r := range ledger.Records {
			if r == *c.terminal {
				found = true
			}
		}
		if !found {
			return errHAROwnershipUnproven
		}
		payloadRoot := n.work
		if c.Completed {
			payloadRoot = n.root
		}
		name, info, payloadErr := harRetirementPayload(payloadRoot, *c.terminal)
		if payloadErr != nil || name != c.directoryName || (c.identity == nil) != (info == nil) {
			return errors.Join(payloadErr, errHAROwnershipUnproven)
		}
		if info != nil && !os.SameFile(info, c.identity) {
			return errHAROwnershipUnproven
		}
		if c.Completed {
			return resumeHARCompletedRetirement(n.root, n.work, *c.terminal)
		}
		if info == nil {
			if err := syncHARRoot(n.work); err != nil {
				return err
			}
			if _, info, err := harRetirementPayload(n.work, *c.terminal); err != nil || info != nil {
				return errors.Join(err, errHAROwnershipUnproven)
			}
			return clearHARRetirement(n.work, *c.terminal)
		}
	} else {
		generation, recognized := harGenerationFromDirectory(c.directoryName)
		if !recognized || generation != c.generation || c.identity == nil || c.Completed {
			return errHAROwnershipUnproven
		}
	}
	root, err := openHARPrivateChild(n.work, c.directoryName, false)
	if err != nil {
		return err
	}
	var lease *os.File
	var release func() error
	var generationOnce sync.Once
	var generationErr error
	// closeGeneration releases the lease and closes it and the generation
	// root, once: retirement calls it before renaming, and every return
	// path does if retirement did not.
	closeGeneration := func() error {
		generationOnce.Do(func() {
			if release != nil {
				generationErr = errors.Join(generationErr, release())
			}
			if lease != nil {
				generationErr = errors.Join(generationErr, lease.Close())
			}
			generationErr = errors.Join(generationErr, root.Close())
		})
		return generationErr
	}
	defer func() { err = errors.Join(err, closeGeneration()) }()
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(info, c.identity) {
		return errHAROwnershipUnproven
	}
	lease, err = openHARRegular(root, "owner.lock", false)
	if err == nil {
		var lockErr error
		release, lockErr = filelock.LockContextOwned(ctx, lease)
		if lockErr != nil {
			return lockErr
		}
		if err := verifyHARLease(root, "owner.lock", lease); err != nil {
			return err
		}
	} else if c.terminal == nil || !os.IsNotExist(err) {
		return err
	}
	// A missing generation lease is accepted only with the prior synced,
	// identity-bound writer-joined ledger record under the namespace lease.
	workLocal, workLocalErr := harWorkingDirectoryIsLocal(filepath.Join(n.path, runtimeHARWorkingDirectory))
	isLocalFS := workLocalErr == nil && workLocal && harLeaseInheritableAcrossExec
	return retireHARGeneration(n.work, root, c.directoryName, c.identity, c.Abandoned, closeGeneration, isLocalFS)
}
