package api

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const analysisUploadOperationTimeout = 2 * time.Minute

var errAnalysisUploadPhysical = errors.New("upload physical ownership remains unproven")
var analysisUploadWriterSlots = make(chan struct{}, 16)
var analysisUploadOwners sync.Map

type analysisUploadOwnerKey struct{}
type analysisUploadDescriptor struct {
	closer io.Closer
	once   sync.Once
	done   chan struct{}
	err    error
	joined bool
}

// This exact parent is registered before body allocation or descriptor opens.
// Neither HTTP cancellation nor the durable intent supplies physical closure.
type analysisUploadPhysicalOwner struct {
	mu                      sync.Mutex
	id                      string
	descriptors             map[io.Closer]*analysisUploadDescriptor
	operations              int
	operationUnknown        bool
	physicalSealed          bool
	completionResolved      bool
	reconciliation          *analysisUploadReconciliationRecord
	requestEnded, resolved  bool
	intent                  *storage.AnalysisUploadWriter
	continuation            func()
	continuationStarted     bool
	releaseOnce, finishOnce sync.Once
	done                    chan struct{}
	receipt                 *storage.AnalysisUploadWriter
	resultErr               error
}

func acquireAnalysisUploadOwner() (*analysisUploadPhysicalOwner, bool) {
	select {
	case analysisUploadWriterSlots <- struct{}{}:
	default:
		return nil, false
	}
	o := &analysisUploadPhysicalOwner{id: uuid.NewString(), descriptors: map[io.Closer]*analysisUploadDescriptor{}, resolved: true, done: make(chan struct{})}
	analysisUploadOwners.Store(o.id, o)
	return o, true
}
func (o *analysisUploadPhysicalOwner) beginOperation() (func(), error) {
	o.mu.Lock()
	if o.requestEnded || o.physicalSealed || o.completionResolved || o.operationUnknown || o.operations >= 4 || len(o.descriptors) >= 64 {
		o.mu.Unlock()
		return nil, errAnalysisUploadPhysical
	}
	o.operations++
	o.mu.Unlock()
	return sync.OnceFunc(func() { o.mu.Lock(); o.operations--; o.mu.Unlock(); o.progress() }), nil
}

// A panic or Goexit in an entered physical operation may have allocated an unreported
// descriptor. Joining its known close set cannot erase that unknown ownership.
func (o *analysisUploadPhysicalOwner) markPhysicalUnknown() {
	o.mu.Lock()
	o.operationUnknown = true
	o.resolved = false
	o.resultErr = errAnalysisUploadPhysical
	known := make([]io.Closer, 0, len(o.descriptors))
	for _, descriptor := range o.descriptors {
		known = append(known, descriptor.closer)
	}
	o.mu.Unlock()
	for _, descriptor := range known {
		go func() { _ = o.CloseOwner(descriptor) }()
	}
}

func (o *analysisUploadPhysicalOwner) sealPhysical() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.joinedLocked() {
		return false
	}
	// Exact metadata reconciliation remains permitted. All later body/artifact
	// admission is permanently refused, including across an ambiguous SQL reply.
	o.physicalSealed = true
	return true
}

func (o *analysisUploadPhysicalOwner) TrackOwner(c io.Closer) {
	if c == nil {
		return
	}
	o.mu.Lock()
	if o.physicalSealed {
		o.operationUnknown = true
		o.resolved = false
	}
	if _, ok := o.descriptors[c]; !ok {
		o.descriptors[c] = &analysisUploadDescriptor{closer: c, done: make(chan struct{})}
	}
	o.mu.Unlock()
}
func (o *analysisUploadPhysicalOwner) CloseOwner(c io.Closer) error {
	o.mu.Lock()
	record := o.descriptors[c]
	o.mu.Unlock()
	if record == nil {
		return errAnalysisUploadPhysical
	}
	record.once.Do(func() {
		go func() {
			var err error
			func() {
				defer func() {
					if recover() != nil {
						err = errAnalysisUploadPhysical
					}
				}()
				err = record.closer.Close()
			}()
			o.mu.Lock()
			record.err = err
			record.joined = err == nil
			close(record.done)
			o.mu.Unlock()
			o.progress()
		}()
	})
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-record.done:
		o.mu.Lock()
		err := record.err
		o.mu.Unlock()
		return err
	case <-timer.C:
		return errAnalysisUploadPhysical
	}
}
func (o *analysisUploadPhysicalOwner) joinedLocked() bool {
	if o.operationUnknown || o.operations != 0 {
		return false
	}
	for _, record := range o.descriptors {
		if !record.joined {
			return false
		}
	}
	return true
}
func (o *analysisUploadPhysicalOwner) physicalJoined() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.joinedLocked()
}
func (o *analysisUploadPhysicalOwner) progress() {
	o.mu.Lock()
	joined := o.joinedLocked()
	next := o.continuation
	start := joined && next != nil && !o.continuationStarted
	if start {
		o.continuationStarted = true
	}
	release := joined && o.requestEnded && o.resolved
	o.mu.Unlock()
	if start {
		next()
	}
	if release {
		o.releaseOnce.Do(func() { analysisUploadOwners.Delete(o.id); <-analysisUploadWriterSlots })
	}
}
func (o *analysisUploadPhysicalOwner) endRequest() {
	o.mu.Lock()
	o.requestEnded = true
	o.mu.Unlock()
	o.progress()
}
func (o *analysisUploadPhysicalOwner) setIntent(w *storage.AnalysisUploadWriter) {
	copy := *w
	o.mu.Lock()
	o.intent = &copy
	o.resolved = false
	o.mu.Unlock()
}
func (o *analysisUploadPhysicalOwner) afterPhysicalJoin(next func()) {
	o.mu.Lock()
	o.continuation = next
	o.mu.Unlock()
	o.progress()
}
func (o *analysisUploadPhysicalOwner) finish(receipt *storage.AnalysisUploadWriter, err error) {
	o.finishOnce.Do(func() {
		o.mu.Lock()
		if receipt != nil {
			copy := *receipt
			o.receipt = &copy
		}
		o.resultErr = err
		o.resolved = true
		o.completionResolved = true
		close(o.done)
		o.mu.Unlock()
		o.progress()
	})
}
func (o *analysisUploadPhysicalOwner) outcome() (*storage.AnalysisUploadWriter, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.receipt == nil {
		return nil, o.resultErr
	}
	copy := *o.receipt
	return &copy, o.resultErr
}

// A blocked Read keeps the exact body and allocation admission in its one owned
// operation while the HTTP caller can return on its actual bounded context.
func readAnalysisUploadBody(ctx context.Context, o *analysisUploadPhysicalOwner, body io.ReadCloser) ([]byte, error) {
	if err := analysisUploadContextError(ctx); err != nil {
		return nil, err
	}
	end, err := o.beginOperation()
	if err != nil {
		return nil, err
	}
	o.TrackOwner(body)
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	joined := make(chan struct{})
	go func() {
		var data []byte
		var readErr error
		returned := false
		defer close(joined)
		defer end()
		defer func() {
			if recover() != nil || !returned {
				o.markPhysicalUnknown()
				data = nil
				readErr = errAnalysisUploadPhysical
			}
			closeErr := o.CloseOwner(body)
			done <- result{data, errors.Join(readErr, closeErr)}
		}()
		data, readErr = io.ReadAll(io.LimitReader(body, analysisUploadChunk+1))
		returned = true
	}()
	if err := waitAnalysisUploadContext(ctx, joined, analysisUploadOperationTimeout); err != nil {
		go func() { _ = o.CloseOwner(body) }()
		return nil, err
	}
	got := <-done
	if err := analysisUploadContextError(ctx); err != nil {
		return nil, err
	}
	return got.data, got.err
}

func analysisUploadContextError(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !deadline.After(time.Now()) {
		return context.DeadlineExceeded
	}
	return nil
}

// A context can advertise a deadline with a detached Done channel. Bound the
// wait with our own timer without interpreting timeout as a physical join.
func waitAnalysisUploadContext(ctx context.Context, done <-chan struct{}, maximum time.Duration) error {
	if err := analysisUploadContextError(ctx); err != nil {
		return err
	}
	if done == nil || maximum <= 0 {
		return errAnalysisUploadPhysical
	}
	wait := maximum
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
		wait = time.Until(deadline)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
		return analysisUploadContextError(ctx)
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return context.DeadlineExceeded
	}
}
