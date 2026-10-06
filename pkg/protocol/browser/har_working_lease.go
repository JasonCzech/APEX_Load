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
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

const (
	maxRuntimeHARWorkingGenerations = 128
	maxRuntimeHARPendingOwners      = 64
	maxRuntimeHARWorkingBytes       = int64(2 << 30)
	maxRuntimeHARManifestBytes      = 4096
	runtimeHARCloseWait             = 2 * time.Second
)

var errHAROwnershipUnproven = errors.New("browser HAR physical ownership remains unproven")
var runtimeHAROwners = struct {
	sync.Mutex
	owners map[*harCaptureOwner]bool
}{owners: make(map[*harCaptureOwner]bool)}

type harOwnershipManifest struct {
	Version    int    `json:"version"`
	Generation string `json:"generation"`
	State      string `json:"state"`
}

type harCaptureOwner struct {
	mu                             sync.Mutex
	generation, path, artifactPath string
	parent, work, root             *os.Root
	namespace                      *harNamespace
	parentIdentity, identity       os.FileInfo
	lease                          *os.File
	release                        func() error
	writerEntered                  bool
	closer                         func() error
	// bound closes when the writer's closer is bound or the writer was
	// abandoned, so a close requested while the context opens waits for it.
	bound                 chan struct{}
	boundOnce             sync.Once
	closeOnce, finishOnce sync.Once
	descriptorOnce        sync.Once
	descriptorErr         error
	closeDone, finishDone chan struct{}
	closeErr, finishErr   error
	retired               bool
	runtimeJoinOnce       sync.Once
	runtimeDone           chan struct{}
	runtimeErr            error
	documentJoinOnce      sync.Once
	documentDone          chan struct{}
	documentErr           error
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
	if err == nil {
		o.lease, err = openHARRegular(o.root, "owner.lock", true)
	}
	if err == nil {
		o.release, err = filelock.LockContextOwned(ctx, o.lease)
	}
	if err == nil {
		err = verifyHARLease(o.root, "owner.lock", o.lease)
	}
	if err == nil {
		data, _ := json.Marshal(harOwnershipManifest{1, o.generation, "sealed"})
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
				if err != nil || auxiliary > maxHARRetirementRecords || validateHARPrivateInfo(info, true) != nil || info.Size() > maxHARRetirementLedgerBytes {
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
			files, childErr := children.ReadDir(6)
			_ = children.Close()
			if childErr != nil && childErr != io.EOF || len(files) > 5 {
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

func allowedHARWorkingName(name string) bool {
	switch name {
	case "owner.lock", "manifest.json", "writer-joined.json", "working.har", "scrubbed.part":
		return true
	}
	return false
}

func (o *harCaptureOwner) beginWriter() { o.mu.Lock(); o.writerEntered = true; o.mu.Unlock() }
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
				data, _ := json.Marshal(harOwnershipManifest{1, o.generation, "writer_joined"})
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

func (o *harCaptureOwner) startFinish(keep bool, redactor protocol.TextRedactor) {
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
				err = o.finalizeJoined(keep, redactor)
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
		return "", errors.Join(errHAROwnershipUnproven, ctx.Err())
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
	var publicationErr error
	if keep {
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
				data, readErr = scrub.HAR(data, scrub.Policy{})
			}
			if readErr == nil && redactor != nil {
				data, readErr = redactRuntimeHARText(data, redactor)
			}
			if readErr == nil {
				readErr = o.publishScrubbed(ctx, n.root, data)
			}
			publicationErr = readErr
		}
	}
	// A publication failure is observable, but a proven joined raw writer can
	// still be retired. Unproven close never enters this function.
	err = retireHARGeneration(n.work, o.root, o.generation, o.identity)
	if err == nil {
		err = o.closeDescriptors()
		retirementProven = err == nil
	}
	return errors.Join(publicationErr, err)
}

func (o *harCaptureOwner) publishScrubbed(ctx context.Context, parent *os.Root, data []byte) error {
	if err := writeHARPrivateDurable(o.root, "scrubbed.part", data); err != nil {
		return err
	}
	name := "apexload_browser_" + o.generation + "_scrubbed.har"
	private := filepath.Join(runtimeHARWorkingDirectory, o.generation, "scrubbed.part")
	// Link is atomic and fails if the final name already exists. The closed,
	// synced source is never copied into a visible partial final filename.
	if err := parent.Link(private, name); err != nil {
		return err
	}
	if err := syncHARRoot(parent); err != nil {
		return err
	}
	if err := o.root.Remove("scrubbed.part"); err != nil {
		return err
	}
	if err := syncHARRoot(o.root); err != nil {
		return err
	}
	if err := enforceRuntimeHARRetentionAtContext(ctx, parent, maxRetainedRuntimeHARArtifacts, maxRetainedRuntimeHARBytes); err != nil {
		return err
	}
	info, err := parent.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(err, errHAROwnershipUnproven)
	}
	o.mu.Lock()
	o.artifactPath = filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(o.path))), name)
	o.mu.Unlock()
	return nil
}

func (o *harCaptureOwner) closeDescriptors() error {
	o.descriptorOnce.Do(func() {
		var err error
		if o.release != nil {
			err = errors.Join(err, o.release())
		}
		if o.lease != nil {
			err = errors.Join(err, o.lease.Close())
		}
		if o.root != nil {
			err = errors.Join(err, o.root.Close())
		}
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
	entries, readErr := directory.ReadDir(6)
	closeErr := directory.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(entries) > 5 {
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

func retireHARGeneration(work, root *os.Root, directoryName string, identity os.FileInfo) error {
	generation, recognized := harGenerationFromDirectory(directoryName)
	if !recognized {
		return errHAROwnershipUnproven
	}
	current, err := work.Lstat(directoryName)
	if err != nil || !os.SameFile(identity, current) || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 {
		return errHAROwnershipUnproven
	}
	id, err := persistentHARIdentity(identity)
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
		if err := readHARManifest(root, "manifest.json", generation, "sealed"); err != nil {
			return err
		}
		if err := readHARManifest(root, "writer-joined.json", generation, "writer_joined"); err != nil {
			return err
		}
		if err := registerHARRetirement(work, r); err != nil {
			return err
		}
	}
	if _, err := harWorkingGenerationSize(root); err != nil {
		return err
	}
	tombstone := ".retired-" + generation
	if directoryName != tombstone {
		if _, err := work.Lstat(tombstone); !os.IsNotExist(err) {
			return errHAROwnershipUnproven
		}
		if err := work.Rename(directoryName, tombstone); err != nil {
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
	// The synced namespace ledger preserves joined proof through every unlink,
	// including a crash after removal of all generation control files.
	for _, name := range []string{"working.har", "scrubbed.part", "owner.lock", "writer-joined.json", "manifest.json"} {
		if err := root.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := work.Remove(tombstone); err != nil {
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
	Path                     string
	SizeBytes                int64
	ModifiedAt               time.Time
	Completed                bool
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
	f, err := openHARRegular(root, name, false)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxRuntimeHARManifestBytes+1))
	err = errors.Join(err, f.Close())
	if err != nil || len(data) > maxRuntimeHARManifestBytes {
		return errHAROwnershipUnproven
	}
	var m harOwnershipManifest
	if err := strictHARJSON(data, &m); err != nil {
		return err
	}
	if m.Version != 1 || m.Generation != generation || m.State != state {
		return errHAROwnershipUnproven
	}
	return nil
}

// PlanRuntimeHARWorkingCleanup only recognizes a durable writer-joined proof;
// an unlocked lease, old timestamp or missing PID never authorizes deletion.
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
			if readHARManifest(root, "manifest.json", generation, "sealed") == nil && readHARManifest(root, "writer-joined.json", generation, "writer_joined") == nil {
				info, statErr := root.Stat(".")
				size, sizeErr := harWorkingGenerationSize(root)
				if statErr == nil && sizeErr == nil {
					out = append(out, RuntimeHARWorkingPruneCandidate{Path: filepath.Join(path, runtimeHARWorkingDirectory, e.Name()), SizeBytes: size, ModifiedAt: info.ModTime().UTC(), generation: generation, directoryName: e.Name(), identity: info, parentIdentity: parentInfo, planCursor: cursor})
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
	defer func() { err = errors.Join(err, root.Close()) }()
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(info, c.identity) {
		return errHAROwnershipUnproven
	}
	lease, err := openHARRegular(root, "owner.lock", false)
	if err == nil {
		defer func() { err = errors.Join(err, lease.Close()) }()
		release, lockErr := filelock.LockContextOwned(ctx, lease)
		if release != nil {
			defer func() { err = errors.Join(err, release()) }()
		}
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
	return retireHARGeneration(n.work, root, c.directoryName, c.identity)
}
